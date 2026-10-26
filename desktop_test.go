package main

import (
	"context"
	"image/png"
	"os"
	"testing"

	"clashgo/internal/core"
	"github.com/egoist/mygo/ui"
)

func TestNativeNavigation(t *testing.T) {
	manager, err := core.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &desktop{manager: manager, ctx: context.Background(), page: "overview", state: manager.Snapshot()}
	tt := ui.NewTester(s.view, 1080, 760)
	if !tt.HasText("准备连接") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("启动内核"); err != nil {
		t.Fatal(err)
	}
	if s.busy {
		t.Fatal("start enabled without config")
	}
	if err := tt.Click("前往导入配置"); err != nil {
		t.Fatal(err)
	}
	if s.page != "profiles" || !tt.HasText("HTTPS 订阅") {
		t.Fatal(tt.Texts())
	}
	for _, page := range []struct{ label, id, text string }{
		{"设置", "settings", "mihomo 内核"},
		{"代理组", "proxies", "启动内核后显示代理组和当前节点。"},
		{"运行日志", "logs", "运行日志"},
		{"概览", "overview", "准备连接"},
	} {
		if err := tt.Click(page.label); err != nil {
			t.Fatal(err)
		}
		if s.page != page.id || !tt.HasText(page.text) {
			t.Fatalf("page=%s, texts=%q", s.page, tt.Texts())
		}
	}
	tt.SetDark(true)
	tt.SetSize(900, 650)
	if !tt.HasText("准备连接") {
		t.Fatal("dark/small layout lost content")
	}
	if path := os.Getenv("CLASHGO_SCREENSHOT"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := png.Encode(file, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProxyFiltering(t *testing.T) {
	s := &desktop{page: "proxies", state: core.Snapshot{Running: true, Groups: []core.Group{
		{Name: "香港", Type: "Selector", Now: "DIRECT", All: []string{"DIRECT", "REJECT"}},
		{Name: "Japan", Type: "URLTest", Now: "DIRECT", All: []string{"DIRECT"}},
	}}}
	tt := ui.NewTester(s.view, 1080, 760)
	if !tt.HasText("香港") || !tt.HasText("Japan") {
		t.Fatal(tt.Texts())
	}
	s.search = "japan"
	tt.Frame()
	if tt.HasText("香港") || !tt.HasText("Japan") {
		t.Fatal("filter failed")
	}
}

func TestManagedKernelSettings(t *testing.T) {
	s := &desktop{page: "settings", port: "17890", busy: true,
		kernelProgress: "正在下载 v1.2.3 · 50%",
		state:          core.Snapshot{KernelReady: true, KernelVersion: "v1.0.0", Port: 17890}}
	tt := ui.NewTester(s.view, 1080, 760)
	for _, text := range []string{"检查并更新内核", "已安装版本：v1.0.0", "正在下载 v1.2.3 · 50%"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %s; texts=%q", text, tt.Texts())
		}
	}
	if tt.HasText("选择内核…") || tt.HasText("内核路径") {
		t.Fatal("manual kernel selection still visible")
	}
	if err := tt.Click("检查并更新内核"); err != nil {
		t.Fatal(err)
	}
	if !s.busy {
		t.Fatal("busy update control was enabled")
	}
}

func TestSizeText(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1024: "1.0 KiB", 1 << 20: "1.0 MiB", 1 << 30: "1.00 GiB"} {
		if got := sizeText(n); got != want {
			t.Errorf("%d: got %s want %s", n, got, want)
		}
	}
}
