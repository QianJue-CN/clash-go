package core

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func releaseFixture(t *testing.T, version, behavior string) (kernelRelease, []byte) {
	t.Helper()
	name, err := assetName(runtime.GOOS, runtime.GOARCH, version)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	data := fakeBinary(t, version, behavior)
	if strings.HasSuffix(name, ".zip") {
		z := zip.NewWriter(&buffer)
		f, err := z.Create(strings.TrimSuffix(name, "-"+version+".zip") + ".exe")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&buffer)
		if _, err := gz.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	digest := sha256.Sum256(buffer.Bytes())
	return kernelRelease{Version: version, Assets: []releaseAsset{{Name: name, URL: "https://github.com/MetaCubeX/mihomo/releases/download/" + version + "/" + name, Size: int64(buffer.Len()), Digest: "sha256:" + hex.EncodeToString(digest[:])}}}, buffer.Bytes()
}

func mockReleaseClient(t *testing.T, release kernelRelease, archive []byte, onDownload func()) *http.Client {
	t.Helper()
	metadata, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	client := kernelHTTP()
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var data []byte
		switch r.URL.String() {
		case latestReleaseURL:
			data = metadata
		case release.Assets[0].URL:
			if onDownload != nil {
				onDownload()
			}
			data = archive
		default:
			return nil, fmt.Errorf("unexpected URL: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})
	return client
}

func TestKernelPlatformAssets(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, err := assetName(goos, arch, "v1.19.33")
			if err != nil {
				t.Fatal(err)
			}
			if arch == "amd64" && !strings.Contains(name, "amd64-compatible") {
				t.Error(name)
			}
			if goos == "windows" && !strings.HasSuffix(name, ".zip") {
				t.Error(name)
			}
		}
	}
	if _, err := assetName("linux", "386", "v1.0.0"); err == nil {
		t.Fatal("unsupported arch accepted")
	}
	for _, invalid := range []string{"latest", "v1.2", "v1.2.3-beta", "v1.2.3/../../", "v01.2.3", "v1.+2.3"} {
		if _, err := versionParts(invalid); err == nil {
			t.Fatal("unsafe version accepted:", invalid)
		}
	}
}

func TestReleaseValidation(t *testing.T) {
	base, archive := releaseFixture(t, "v1.2.3", "ok")
	for _, test := range []struct {
		name   string
		mutate func(*kernelRelease)
	}{
		{"no digest", func(r *kernelRelease) { r.Assets[0].Digest = "" }},
		{"bad digest", func(r *kernelRelease) { r.Assets[0].Digest = "sha256:00" }},
		{"untrusted host", func(r *kernelRelease) { r.Assets[0].URL = "https://evil.example/binary.gz" }},
		{"wrong repo", func(r *kernelRelease) { r.Assets[0].URL = strings.Replace(r.Assets[0].URL, "MetaCubeX", "someone", 1) }},
		{"oversize", func(r *kernelRelease) { r.Assets[0].Size = maxArchiveSize + 1 }},
		{"prerelease", func(r *kernelRelease) { r.Prerelease = true }},
		{"draft", func(r *kernelRelease) { r.Draft = true }},
		{"wrong arch", func(r *kernelRelease) { r.Assets[0].Name = "other.gz" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := base
			r.Assets = append([]releaseAsset(nil), base.Assets...)
			test.mutate(&r)
			client := mockReleaseClient(t, r, archive, func() { t.Error("must reject before downloading") })
			if _, _, err := latestKernel(context.Background(), client, runtime.GOOS, runtime.GOARCH); err == nil {
				t.Fatal("invalid release accepted")
			}
		})
	}
}

func TestKernelRedirectPolicy(t *testing.T) {
	for _, target := range []string{"http://github.com/file", "https://evil.example/file", "https://github.com:444/file", "https://user@github.com/file"} {
		client := kernelHTTP()
		calls := 0
		client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{target}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		})
		if _, err := releaseGET(context.Background(), client, latestReleaseURL); err == nil || calls != 1 {
			t.Fatalf("redirect followed: %s", target)
		}
	}
	if !officialURL("https://release-assets.githubusercontent.com/download?token=x") {
		t.Fatal("official asset host rejected")
	}
}

func TestArchiveSafety(t *testing.T) {
	for _, name := range []string{"../mihomo.exe", "folder/mihomo.exe", "mihomo-windows-amd64-compatible.exe"} {
		t.Run(name, func(t *testing.T) {
			var buffer bytes.Buffer
			z := zip.NewWriter(&buffer)
			f, _ := z.Create(name)
			f.Write([]byte("executable"))
			z.Close()
			path := filepath.Join(t.TempDir(), "test.zip")
			os.WriteFile(path, buffer.Bytes(), 0600)
			var result bytes.Buffer
			err := unpackKernel(path, "mihomo-windows-amd64-compatible-v1.2.3.zip", "v1.2.3", &result)
			if name == "mihomo-windows-amd64-compatible.exe" {
				if err != nil || result.String() != "executable" {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsafe zip name accepted")
			}
		})
	}
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	gz.Write([]byte("test"))
	gz.Close()
	broken := buffer.Bytes()
	broken[len(broken)-1] ^= 0xff
	path := filepath.Join(t.TempDir(), "broken.gz")
	os.WriteFile(path, broken, 0600)
	if err := unpackKernel(path, "test.gz", "v1.2.3", io.Discard); err == nil {
		t.Fatal("gzip corruption accepted")
	}
}

func TestAutomaticInstallAndOfflineStartup(t *testing.T) {
	t.Setenv("CLASHGO_TEST_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	release, archive := releaseFixture(t, "v1.2.3", "ok")
	m.downloads = mockReleaseClient(t, release, archive, nil)
	var progress []string
	if err := m.Initialize(context.Background(), func(s string) { progress = append(progress, s) }); err != nil {
		t.Fatal(err)
	}
	if !m.Snapshot().KernelReady || m.Snapshot().KernelVersion != "v1.2.3" || m.Snapshot().Running || len(progress) == 0 {
		t.Fatal("first install not activated")
	}
	if err := m.SaveSettings(Settings{Port: freePort(t)}); err != nil {
		t.Fatal(err)
	}
	if err := m.Import(context.Background(), []byte(directConfig)); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewManager(m.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	reopened.downloads.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("cached startup contacted network")
		return nil, fmt.Errorf("offline")
	})
	if err := reopened.Initialize(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !reopened.Snapshot().Running {
		t.Fatal("existing config not auto-started")
	}
}

func TestRunningKernelUpgrade(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if err := m.Import(ctx, []byte(directConfig)); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.SetMode(ctx, "global"); err != nil {
		t.Fatal(err)
	}
	if err := m.Select(ctx, "PROXY", "REJECT"); err != nil {
		t.Fatal(err)
	}
	oldPath := m.kernelPath()
	release, archive := releaseFixture(t, "v1.2.3", "ok")
	m.downloads = mockReleaseClient(t, release, archive, nil)
	if err := m.UpdateKernel(ctx, nil); err != nil {
		t.Fatal(err)
	}
	s := m.Snapshot()
	if !s.Running || s.Version != "v1.2.3" || s.Mode != "global" || s.Groups[0].Now != "REJECT" {
		t.Fatalf("session not restored: %+v", s)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("old binary not cleaned up")
	}
	reopened, err := NewManager(m.dir)
	if err != nil || reopened.kernel.Version != "v1.2.3" {
		t.Fatalf("activation not persisted: %v", err)
	}
	pid := m.proc.cmd.Process.Pid
	for _, version := range []string{"v1.2.3", "v1.0.0"} {
		r, data := releaseFixture(t, version, "ok")
		m.downloads = mockReleaseClient(t, r, data, func() { t.Error("downloaded same or older release") })
		if err := m.UpdateKernel(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if m.proc.cmd.Process.Pid != pid {
			t.Fatal("unnecessary restart")
		}
	}
}

func TestFailedUpdatePreservesOldKernel(t *testing.T) {
	for _, failure := range []string{"digest", "truncated", "bad-config", "crash", "cancel", "commit"} {
		t.Run(failure, func(t *testing.T) {
			m := testManager(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := m.Import(ctx, []byte(directConfig)); err != nil {
				t.Fatal(err)
			}
			if err := m.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.SetMode(ctx, "global"); err != nil {
				t.Fatal(err)
			}
			behavior := "ok"
			if failure == "bad-config" || failure == "crash" {
				behavior = failure
			}
			release, archive := releaseFixture(t, "v1.2.3", behavior)
			if failure == "digest" {
				release.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
			}
			if failure == "truncated" {
				archive = archive[:len(archive)/2]
			}
			if failure == "commit" {
				os.Remove(filepath.Join(m.dir, "kernel.json"))
				os.Mkdir(filepath.Join(m.dir, "kernel.json"), 0700)
			}
			oldPath := m.kernelPath()
			m.downloads = mockReleaseClient(t, release, archive, func() {
				if failure == "cancel" {
					cancel()
				}
			})
			if err := m.UpdateKernel(ctx, nil); err == nil {
				t.Fatal("failure accepted")
			}
			if m.kernelPath() != oldPath {
				t.Fatal("old activation changed")
			}
			if err := m.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			state := m.Snapshot()
			if !state.Running || state.Version != "v1.0.0" || state.Mode != "global" {
				t.Fatalf("old core not recovered: %+v", state)
			}
			files, err := os.ReadDir(m.kernelDir())
			if err != nil || len(files) != 1 {
				t.Fatalf("partial files left: %v %v", files, err)
			}
		})
	}
}

func TestInstalledKernelTampering(t *testing.T) {
	m := testManager(t)
	if err := os.WriteFile(m.kernelPath(), []byte("not executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("tampered binary accepted")
	}
	release, archive := releaseFixture(t, "v1.0.0", "ok")
	m.downloads = mockReleaseClient(t, release, archive, nil)
	if err := m.UpdateKernel(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := m.verifyKernel(context.Background()); err != nil {
		t.Fatal("repair failed:", err)
	}
}

func TestCancelDuringKernelSwitch(t *testing.T) {
	m := testManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Import(ctx, []byte(directConfig)); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	release, archive := releaseFixture(t, "v1.2.3", "ok")
	m.downloads = mockReleaseClient(t, release, archive, nil)
	err := m.UpdateKernel(ctx, func(message string) {
		if strings.HasPrefix(message, "正在切换内核") {
			cancel()
		}
	})
	if err == nil || m.Snapshot().Running || m.kernel.Version != "v1.0.0" {
		t.Fatal("shutdown restarted a kernel or changed activation")
	}
	reopened, err := NewManager(m.dir)
	if err != nil || reopened.kernel.Version != "v1.0.0" {
		t.Fatal("old activation lost", err)
	}
}

func TestKernelRecordAndVersionSafety(t *testing.T) {
	m := testManager(t)
	if err := probeKernel(context.Background(), m.kernelPath(), "v9.0.0"); err == nil {
		t.Fatal("version mismatch accepted")
	}
	for _, name := range []string{"../outside", "mihomo-../../outside", `mihomo-..\outside`, "mihomo-C:outside"} {
		record := m.kernel
		record.File = name
		data, _ := json.Marshal(record)
		if err := writePrivate(filepath.Join(m.dir, "kernel.json"), data); err != nil {
			t.Fatal(err)
		}
		if _, err := NewManager(m.dir); err == nil {
			t.Fatalf("unsafe activation filename %q", name)
		}
	}
}

// Opt in: downloads, verifies and activates the real official release in a temp directory.
func TestOfficialKernelDownload(t *testing.T) {
	if os.Getenv("CLASHGO_TEST_DOWNLOAD") != "1" {
		t.Skip("CLASHGO_TEST_DOWNLOAD not set")
	}
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if err := m.Initialize(ctx, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSettings(Settings{Port: freePort(t)}); err != nil {
		t.Fatal(err)
	}
	if err := m.Import(ctx, []byte(directConfig)); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.Snapshot().Running {
		t.Fatal("downloaded core not running")
	}
	if err := m.UpdateKernel(ctx, nil); err != nil {
		t.Fatal("check latest:", err)
	}
}
