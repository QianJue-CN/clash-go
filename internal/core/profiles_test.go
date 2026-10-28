package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSubscriptionNames(t *testing.T) {
	for _, test := range []struct{ title, disposition, want string }{
		{"我的订阅", `attachment; filename="fallback.yaml"`, "我的订阅"},
		{"base64:" + base64.StdEncoding.EncodeToString([]byte("机场 A")), "", "机场 A"},
		{"base64:" + base64.RawStdEncoding.EncodeToString([]byte("机场 B")), "", "机场 B"},
		{"%E8%AE%A2%E9%98%85+A", "", "订阅+A"},
		{"", "attachment; filename*=UTF-8''%E8%AE%A2%E9%98%85.yaml", "订阅"},
		{"base64:invalid!", `attachment; filename="Service.yml"`, "Service"},
		{"", `attachment; filename="../../Service.YAML"`, "Service"},
		{"\x00\u202e", `attachment; filename="Safe.yaml"`, "Safe"},
		{"", "", ""},
		{"base64:invalid!", "broken; filename", ""},
		{string([]byte{0xff}), "", ""},
	} {
		header := http.Header{}
		header.Set("Profile-Title", test.title)
		header.Set("Content-Disposition", test.disposition)
		if got := subscriptionName(header); got != test.want {
			t.Errorf("title=%q disposition=%q got=%q want=%q", test.title, test.disposition, got, test.want)
		}
	}
	if got := cleanProfileName(strings.Repeat("订", 100)); utf8.RuneCountInString(got) != 80 {
		t.Fatal("name not bounded")
	}
}

func TestDownloadSubscriptionMetadata(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/profile", http.StatusFound)
		case "/downgrade":
			http.Redirect(w, r, "http://example.com", http.StatusFound)
		case "/error":
			w.WriteHeader(http.StatusForbidden)
		case "/profile":
			w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("测试订阅")))
			w.Write([]byte(directConfig))
		default:
			w.Write([]byte(directConfig))
		}
	}))
	defer server.Close()
	client := subscriptionHTTP()
	client.Transport = server.Client().Transport
	defer client.CloseIdleConnections()
	for _, path := range []string{"/profile", "/redirect"} {
		profile, err := downloadProfile(context.Background(), client, server.URL+path+"?token=private")
		if err != nil || profile.Name != "测试订阅" || string(profile.Data) != directConfig {
			t.Fatalf("download: %+v %v", profile, err)
		}
	}
	profile, err := downloadProfile(context.Background(), client, server.URL+"/no-name?token=private")
	if err != nil || profile.Name != "" {
		t.Fatal("URL token used as name", err)
	}
	for _, path := range []string{"/downgrade", "/error"} {
		_, err := downloadProfile(context.Background(), client, server.URL+path+"?token=private")
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("unsafe response or leaked token:", err)
		}
	}
}

func TestMultipleProfilesAndSwitching(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	first, err := m.AddProfile(ctx, "", []byte(directConfig))
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.AddProfile(ctx, "服务商", []byte(directConfig+"mode: direct\n"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second || m.Snapshot().ActiveProfile != first {
		t.Fatal("adding overwrote selection")
	}
	if err := m.SwitchProfile(ctx, first); err != nil {
		t.Fatal(err)
	}
	pid := m.proc.cmd.Process.Pid
	if _, err := m.AddProfile(ctx, "", []byte(directConfig)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddProfile(ctx, "服务商", []byte(directConfig)); err != nil {
		t.Fatal(err)
	}
	state := m.Snapshot()
	if state.Profiles[0].Name != "订阅 1" || state.Profiles[2].Name != "订阅 2" || state.Profiles[3].Name != "服务商 (2)" {
		t.Fatalf("names: %+v", state.Profiles)
	}
	if m.proc.cmd.Process.Pid != pid {
		t.Fatal("adding interrupted running profile")
	}
	original, _ := ReadConfig(m.profilePath(first))
	if err := m.SwitchProfile(ctx, second); err != nil {
		t.Fatal(err)
	}
	state = m.Snapshot()
	if !state.Running || state.Mode != "direct" || state.ActiveProfile != second {
		t.Fatalf("switch: %+v", state)
	}
	stillOriginal, _ := ReadConfig(m.profilePath(first))
	if !bytes.Equal(original, stillOriginal) {
		t.Fatal("switch overwrote old config")
	}
	reopened, err := NewManager(m.dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Snapshot().ActiveProfile != second || len(reopened.Snapshot().Profiles) != 4 {
		t.Fatal("catalog not persisted")
	}
	state.Profiles[0].Name = "changed"
	if m.Snapshot().Profiles[0].Name == "changed" {
		t.Fatal("snapshot shares mutable catalog")
	}
	if err := m.SwitchProfile(ctx, first); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Mode != "rule" {
		t.Fatal("old config not reusable")
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{m.ConfigPath(), filepath.Join(m.dir, "profiles.json")} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("permissions: %v %v", info, err)
			}
		}
	}
}

func TestProfileMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(directConfig), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := m.Snapshot()
	if !state.HasConfig || state.ActiveProfile != "legacy" || state.Profiles[0].Name != "原有配置" || m.ConfigPath() != path {
		t.Fatalf("migration: %+v", state)
	}
	data, _ := os.ReadFile(path)
	if string(data) != directConfig {
		t.Fatal("migration touched original config")
	}
	m, err = NewManager(dir)
	if err != nil || len(m.Snapshot().Profiles) != 1 {
		t.Fatal("migration duplicated profile", err)
	}
}

func TestProfileValidationAndRollback(t *testing.T) {
	for _, failure := range []string{"validation", "startup", "commit"} {
		t.Run(failure, func(t *testing.T) {
			m := testManager(t)
			ctx := context.Background()
			oldID, err := m.AddProfile(ctx, "原配置", []byte(directConfig))
			if err != nil {
				t.Fatal(err)
			}
			data := directConfig
			if failure == "startup" {
				data += "hosts: {crash.test: 127.0.0.1}\n"
			}
			newID, err := m.AddProfile(ctx, "新配置", []byte(data))
			if err != nil {
				t.Fatal(err)
			}
			if err := m.SwitchProfile(ctx, oldID); err != nil {
				t.Fatal(err)
			}
			if err := m.SetMode(ctx, "global"); err != nil {
				t.Fatal(err)
			}
			if err := m.Select(ctx, "PROXY", "REJECT"); err != nil {
				t.Fatal(err)
			}
			catalogPath := filepath.Join(m.dir, "profiles.json")
			original, _ := os.ReadFile(catalogPath)
			if failure == "validation" {
				os.WriteFile(m.profilePath(newID), []byte("rules: [INVALID]"), 0600)
			}
			if failure == "commit" {
				os.Remove(catalogPath)
				os.Mkdir(catalogPath, 0700)
			}
			if err := m.SwitchProfile(ctx, newID); err == nil {
				t.Fatal("failed switch accepted")
			}
			if err := m.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			state := m.Snapshot()
			if !state.Running || state.ActiveProfile != oldID || state.Mode != "global" || state.Groups[0].Now != "REJECT" {
				t.Fatalf("old session lost: %+v", state)
			}
			if failure == "commit" {
				os.Remove(catalogPath)
				os.WriteFile(catalogPath, original, 0600)
			}
			reopened, err := NewManager(m.dir)
			if err != nil || reopened.Snapshot().ActiveProfile != oldID {
				t.Fatal("persisted selection changed", err)
			}
		})
	}
}

func TestFailedAddAndInvalidProfileIDs(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if _, err := m.AddProfile(ctx, "", []byte("rules: [INVALID]")); err == nil {
		t.Fatal("bad config accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.AddProfile(canceled, "", []byte(directConfig)); err == nil {
		t.Fatal("canceled add accepted")
	}
	files, _ := os.ReadDir(filepath.Join(m.dir, "profiles"))
	if len(files) != 0 || len(m.Snapshot().Profiles) != 0 {
		t.Fatal("failed add left a profile")
	}
	if err := os.Mkdir(filepath.Join(m.dir, "profiles.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddProfile(ctx, "", []byte(directConfig)); err == nil {
		t.Fatal("failed catalog write accepted")
	}
	files, _ = os.ReadDir(filepath.Join(m.dir, "profiles"))
	if len(files) != 0 {
		t.Fatal("uncommitted config left on disk")
	}
	if err := m.SwitchProfile(ctx, "../../config"); err == nil {
		t.Fatal("unknown profile allowed")
	}
	os.Remove(filepath.Join(m.dir, "profiles.json"))
	for _, catalog := range []profileCatalog{
		{Active: "../escape", Items: []Profile{{ID: "../escape", Name: "bad"}}},
		{Active: "missing", Items: []Profile{{ID: "legacy", Name: "valid"}}},
		{Active: "legacy", Items: []Profile{{ID: "legacy", Name: "one"}, {ID: "legacy", Name: "two"}}},
	} {
		data, _ := json.Marshal(catalog)
		os.WriteFile(filepath.Join(m.dir, "profiles.json"), data, 0600)
		if _, err := NewManager(m.dir); err == nil {
			t.Fatal("unsafe catalog accepted")
		}
	}
}

func TestProviderCachesUseConfiguration(t *testing.T) {
	config := "proxy-providers: {same: {type: http, url: %s}}\nrules: [\"MATCH,DIRECT\"]\n"
	first, err := Normalize([]byte(strings.Replace(config, "%s", "https://one.example/provider", 1)), "", "", DefaultMixedPort)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Normalize([]byte(strings.Replace(config, "%s", "https://two.example/provider", 1)), "", "", DefaultMixedPort)
	if err != nil {
		t.Fatal(err)
	}
	pathLine := func(data []byte) string {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "path:") {
				return line
			}
		}
		return ""
	}
	if pathLine(first) == "" || pathLine(first) == pathLine(second) {
		t.Fatal("different subscription providers share cache paths")
	}
	withHeader, err := Normalize([]byte(strings.Replace(string(first), "type: http", "type: http\n        header: {Authorization: [secret]}", 1)), "", "", DefaultMixedPort)
	if err != nil {
		t.Fatal(err)
	}
	if pathLine(first) == pathLine(withHeader) {
		t.Fatal("different provider credentials share cache paths")
	}
	roundTrip, err := Normalize(withHeader, "", "", DefaultMixedPort)
	if err != nil || !bytes.Equal(roundTrip, withHeader) {
		t.Fatalf("normalization changes cache path on every start: %v", err)
	}
}
