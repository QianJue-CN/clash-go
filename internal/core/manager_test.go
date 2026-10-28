package core

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestMain(m *testing.M) {
	if os.Getenv("CLASHGO_TEST_HELPER") == "1" {
		os.Exit(fakeCore())
	}
	os.Exit(m.Run())
}

func fakeCore() int {
	self, _ := os.Executable()
	data, _ := os.ReadFile(self)
	marker := []byte("\nCLASHGO_KERNEL=")
	index := bytes.LastIndex(data, marker)
	if index < 0 {
		return 2
	}
	parts := strings.Fields(string(data[index+len(marker):]))
	version, behavior := parts[0], parts[1]
	flags := flag.NewFlagSet("fake-mihomo", flag.ContinueOnError)
	check := flags.Bool("t", false, "")
	showVersion := flags.Bool("v", false, "")
	flags.String("d", "", "")
	path := flags.String("f", "", "")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println("Mihomo Meta", version)
		return 0
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return 2
	}
	var config map[string]any
	if yaml.Unmarshal(data, &config) != nil || bytes.Contains(data, []byte("INVALID")) || behavior == "bad-config" {
		return 3
	}
	if *check {
		return 0
	}
	if os.Getenv("CLASHGO_TEST_EXIT") == "1" || behavior == "crash" || bytes.Contains(data, []byte("crash.test")) {
		return 4
	}
	secret, _ := config["secret"].(string)
	mode, _ := config["mode"].(string)
	chosen := "DIRECT"
	var mu sync.Mutex
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/version":
			json.NewEncoder(w).Encode(map[string]string{"version": version})
		case "/configs":
			if r.Method == "PATCH" {
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				mode = body["mode"]
				w.WriteHeader(204)
			} else {
				json.NewEncoder(w).Encode(map[string]string{"mode": mode})
			}
		case "/proxies":
			json.NewEncoder(w).Encode(map[string]any{"proxies": map[string]Group{"PROXY": {Type: "Selector", Now: chosen, All: []string{"DIRECT", "REJECT"}}}})
		case "/proxies/PROXY":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			chosen = body["name"]
			w.WriteHeader(204)
		case "/connections":
			io.WriteString(w, `{"uploadTotal":42,"downloadTotal":1024,"connections":[]}`)
		default:
			w.WriteHeader(404)
		}
	})
	if err := http.ListenAndServe(config["external-controller"].(string), handler); err != nil {
		return 5
	}
	return 0
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func fakeBinary(t *testing.T, version, behavior string) []byte {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, []byte("\nCLASHGO_KERNEL="+version+" "+behavior+"\n")...)
}

func installFixture(t *testing.T, m *Manager, data []byte, version string) {
	t.Helper()
	name := "mihomo-fixture"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(m.kernelDir(), name)
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	digest, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m.kernel = installedKernel{Version: version, File: name, SHA256: digest}
	record, _ := json.Marshal(m.kernel)
	if err := writePrivate(filepath.Join(m.dir, "kernel.json"), record); err != nil {
		t.Fatal(err)
	}
}

func testManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("CLASHGO_TEST_HELPER", "1")
	// Race-instrumented child processes otherwise wait a second on each successful exit.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSettings(Settings{Port: freePort(t)}); err != nil {
		t.Fatal(err)
	}
	installFixture(t, m, fakeBinary(t, "v1.0.0", "ok"), "v1.0.0")
	t.Cleanup(func() {
		if err := m.Stop(); err != nil {
			t.Error(err)
		}
	})
	return m
}

func TestLifecycleAndRollback(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if err := m.Import(ctx, []byte(directConfig)); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(m.ConfigPath())
	if err := m.Import(ctx, []byte("rules: [INVALID]")); err == nil {
		t.Fatal("invalid config accepted")
	}
	after, _ := os.ReadFile(m.ConfigPath())
	if !bytes.Equal(original, after) {
		t.Fatal("invalid import overwrote config")
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.Snapshot()
	if !s.Running || s.Version != "v1.0.0" || s.Download != 1024 || len(s.Groups) != 1 {
		t.Fatalf("snapshot: %+v", s)
	}
	if err := m.Import(ctx, []byte(directConfig)); err == nil {
		t.Fatal("import while running allowed")
	}
	if err := m.Start(ctx); err == nil {
		t.Fatal("second start allowed")
	}
	if err := m.SetMode(ctx, "global"); err != nil {
		t.Fatal(err)
	}
	if err := m.Select(ctx, "PROXY", "REJECT"); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Mode != "global" || m.Snapshot().Groups[0].Now != "REJECT" {
		t.Fatal("actions did not apply")
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Running {
		t.Fatal("still running")
	}
	if _, err := os.Stat(m.runtimePath()); !os.IsNotExist(err) {
		t.Fatal("runtime secret left behind")
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal("restart:", err)
	}
	if err := m.proc.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-m.proc.done
	if m.Snapshot().Running {
		t.Fatal("crash not detected")
	}
}

func TestFailedStartCleanup(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if err := m.Import(ctx, []byte(directConfig)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLASHGO_TEST_EXIT", "1")
	if err := m.Start(ctx); err == nil {
		t.Fatal("accepted crashed core")
	}
	if m.Snapshot().Running {
		t.Fatal("reported running after failure")
	}
	if _, err := os.Stat(m.runtimePath()); !os.IsNotExist(err) {
		t.Fatal("runtime file left")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.Start(canceled); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestBusyProxyPort(t *testing.T) {
	m := testManager(t)
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", m.Settings().Port))
	if err != nil {
		t.Skip("port already occupied")
	}
	defer listener.Close()
	if err := m.Import(context.Background(), []byte(directConfig)); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("accepted occupied proxy port")
	}
}

// Set MIHOMO_TEST_BINARY to opt into a real-core test. Traffic stays on loopback.
func TestRealMihomo(t *testing.T) {
	binary := os.Getenv("MIHOMO_TEST_BINARY")
	if binary == "" {
		t.Skip("MIHOMO_TEST_BINARY not set")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	if err := m.SaveSettings(Settings{Port: freePort(t)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, err := coreCommand(ctx, binary, "-v")
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(output))
	if len(fields) < 3 {
		t.Fatal("missing version")
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	installFixture(t, m, data, fields[2])
	if err := m.Import(ctx, []byte(directConfig)); err != nil {
		t.Fatalf("%v\n%s", err, m.Snapshot().Logs)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatalf("%v\n%s", err, m.Snapshot().Logs)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.SetMode(ctx, "rule"); err != nil {
		t.Fatal(err)
	}
	if err := m.Select(ctx, "PROXY", "DIRECT"); err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "proxied successfully") }))
	defer target.Close()
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", m.Settings().Port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "proxied successfully" {
		t.Fatalf("proxy response %q: %v", body, err)
	}
	first := m.Snapshot().ActiveProfile
	second, err := m.AddProfile(ctx, "真实内核切换测试", []byte(directConfig+"mode: direct\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SwitchProfile(ctx, second); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().ActiveProfile != second || m.Snapshot().Mode != "direct" {
		t.Fatal("real profile switch failed")
	}
	transport.CloseIdleConnections()
	secondResponse, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	secondBody, readErr := io.ReadAll(secondResponse.Body)
	secondResponse.Body.Close()
	if readErr != nil || string(secondBody) != "proxied successfully" {
		t.Fatal("proxy failed after profile switch", readErr)
	}
	if err := m.SwitchProfile(ctx, first); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Mode != "rule" {
		t.Fatal("old profile could not be restored")
	}
	for _, page := range []string{"rules", "providers"} {
		if err := m.RefreshPage(ctx, page); err != nil {
			t.Fatalf("real %s API: %v", page, err)
		}
	}
	if len(m.Snapshot().Rules) == 0 {
		t.Fatal("real rules not loaded")
	}
	if err := m.FlushDNS(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, connection := range m.Snapshot().ConnectionDetails {
		if err := m.CloseConnection(ctx, connection.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.CloseConnections(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.ReplaceProfile(ctx, first, []byte(directConfig+"mode: direct\n")); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Mode != "direct" || !m.Snapshot().Running {
		t.Fatal("real live edit not applied")
	}
	backup, err := m.BackupProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RestoreProfiles(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if len(m.Snapshot().Profiles) != 4 || m.Snapshot().ActiveProfile != first {
		t.Fatal("real backup restore replaced profiles")
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Running {
		t.Fatal("core still running")
	}
}
