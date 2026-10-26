package core

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const directConfig = "proxies: []\nproxy-groups:\n  - name: PROXY\n    type: select\n    proxies: [DIRECT, REJECT]\nrules: [\"MATCH,PROXY\"]\n"

func TestNormalize(t *testing.T) {
	data, err := Normalize([]byte(`
proxies: []
rules: ["MATCH,DIRECT"]
allow-lan: true
mixed-port: 9999
port: 8080
external-controller-tls: 0.0.0.0:9091
external-controller-unix: /tmp/test.sock
external-ui: /tmp/dashboard
listeners: [{type: mixed, port: 9998}]
tunnels: ["tcp,0.0.0.0:1234,1.1.1.1:80"]
tun: {enable: true}
script: {code: bad}
ss-config: "ss://bad"
dns: {enable: true, listen: "0.0.0.0:53", nameserver: ["https://dns.example/dns-query"]}
proxy-providers:
  remote:
    type: http
    url: https://example.com/provider
    path: ../../outside.yaml
`), "127.0.0.1:12345", "test-secret", DefaultMixedPort)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"external-controller-tls", "external-controller-unix", "listeners", "tunnels", "external-ui", "script", "ss-config", "port"} {
		if _, ok := config[key]; ok {
			t.Errorf("unsafe key retained: %s", key)
		}
	}
	if config["allow-lan"] != false || config["mixed-port"] != DefaultMixedPort || config["secret"] != "test-secret" || config["external-controller"] != "127.0.0.1:12345" {
		t.Fatalf("unexpected config: %s", data)
	}
	if config["tun"].(map[string]any)["enable"] != false {
		t.Fatal("TUN enabled")
	}
	if config["dns"].(map[string]any)["listen"] != "" {
		t.Fatal("DNS listener enabled")
	}
	providers := config["proxy-providers"].(map[string]any)
	path := providers["remote"].(map[string]any)["path"].(string)
	if !strings.HasPrefix(path, "./providers/") || strings.Contains(path, "..") {
		t.Fatalf("unsafe provider path %q", path)
	}
	if _, err := Normalize(data, "127.0.0.1:1", "new-secret", DefaultMixedPort); err != nil {
		t.Fatal(err)
	}
}

func TestRejectInvalidConfig(t *testing.T) {
	for _, input := range []string{
		"", "<html>error</html>", "c3M6Ly9ub2Rl", "rules: [a]\n---\nrules: [b]", "rules: [a]\nrules: [b]",
		"rules: [a]\nproxy-providers: [x]", "rules: [a]\ndns: []", "rules: [a]\nproxy-providers: {local: {type: file, path: /etc/passwd}}",
	} {
		if _, err := Normalize([]byte(input), "", "", DefaultMixedPort); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	if _, err := Normalize(bytes.Repeat([]byte("x"), MaxConfigSize+1), "", "", DefaultMixedPort); err == nil {
		t.Fatal("accepted oversize")
	}
}

func TestPrivateAtomicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, text := range []string{"old", "new"} {
		if err := writePrivate(path, []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("read %q: %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestCoreEnvironmentIsIsolated(t *testing.T) {
	t.Setenv("CLASH_POST_UP", "bad-command")
	t.Setenv("SKIP_SAFE_PATH_CHECK", "true")
	t.Setenv("SAFE_PATHS", "/")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := coreCommand(nil, exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range cmd.Env {
		if strings.HasPrefix(value, "CLASH_") || strings.HasPrefix(value, "SKIP_SAFE_PATH_CHECK=") || strings.HasPrefix(value, "SAFE_PATHS=") {
			t.Fatalf("unsafe environment inherited: %s", value)
		}
	}
}

func TestLogBufferBounded(t *testing.T) {
	var b logBuffer
	b.Write(bytes.Repeat([]byte("a"), 100<<10))
	b.Write([]byte("tail"))
	if len(b.String()) != 64<<10 || !strings.HasSuffix(b.String(), "tail") {
		t.Fatal("log cap failed")
	}
}
