package main

import (
	"strings"
	"testing"
	"time"

	"clashgo/internal/core"
	"github.com/egoist/mygo/ui"
)

func TestFeatureNavigation(t *testing.T) {
	s := &desktop{page: "overview"}
	tt := ui.NewTester(s.view, 1080, 760)
	for _, page := range []struct{ label, id, text string }{
		{"连接", "connections", "没有匹配的活动连接；仅显示当前内核捕获的连接。"},
		{"规则", "rules", "启动内核后显示生效规则。"},
		{"资源", "providers", "启动内核后显示资源。"},
		{"工具", "tools", "查询 DNS"},
	} {
		if err := tt.Click(page.label); err != nil {
			t.Fatal(err)
		}
		if s.page != page.id || !tt.HasText(page.text) {
			t.Fatalf("page=%s texts=%q", s.page, tt.Texts())
		}
	}
	if err := tt.Click("查询 DNS"); err != nil {
		t.Fatal(err)
	}
	if s.busy {
		t.Fatal("DNS query enabled without core")
	}
	tt.SetSize(900, 650)
	tt.SetDark(true)
	if !tt.HasText("查询 DNS") {
		t.Fatal("small dark layout lost tools")
	}
}

func TestConnectionFilteringAndConfirmation(t *testing.T) {
	connection := core.Connection{ID: "one", Rule: "Domain", RulePayload: "example.com", Start: time.Now(), Chains: []string{"香港"}}
	connection.Metadata.Host, connection.Metadata.DestinationPort = "example.com", "443"
	connection.Metadata.Process = "browser"
	s := &desktop{page: "connections", state: core.Snapshot{Running: true, Connections: 1, ConnectionDetails: []core.Connection{connection}}}
	tt := ui.NewTester(s.view, 1080, 760)
	if !tt.HasText("example.com:443") {
		t.Fatal(tt.Texts())
	}
	s.connectionSearch = "BROWSER"
	tt.Frame()
	if !tt.HasText("example.com:443") {
		t.Fatal("process filter failed")
	}
	s.connectionSearch = "unrelated"
	tt.Frame()
	if tt.HasText("example.com:443") {
		t.Fatal("connection filter ignored")
	}
	if err := tt.Click("关闭全部连接…"); err != nil {
		t.Fatal(err)
	}
	if !s.showConfirm || s.busy {
		t.Fatal("destructive action skipped confirmation")
	}
	if err := tt.Click("取消操作"); err != nil {
		t.Fatal(err)
	}
	if s.showConfirm || s.busy {
		t.Fatal("cancel performed operation")
	}
}

func TestProfileActionDialogs(t *testing.T) {
	s := &desktop{page: "profiles", state: core.Snapshot{KernelReady: true, Running: true, ActiveProfile: "one", Profiles: []core.Profile{{ID: "one", Name: "服务商"}}}}
	tt := ui.NewTester(s.view, 1080, 760)
	if err := tt.Click("删除配置 服务商"); err != nil {
		t.Fatal(err)
	}
	if s.showConfirm {
		t.Fatal("running config can be deleted")
	}
	if err := tt.Click("重命名 服务商"); err != nil {
		t.Fatal(err)
	}
	if !s.showProfileAction || !tt.Focused("配置名称") {
		t.Fatal("rename did not focus input")
	}
	tt.Key(0, ui.KeyEscape)
	if s.showProfileAction || s.profileID != "" {
		t.Fatal("rename not dismissed")
	}
	if err := tt.Click("更新订阅 服务商"); err != nil {
		t.Fatal(err)
	}
	tt.Type("https://example.com/?token=secret")
	if err := tt.Click("取消编辑"); err != nil {
		t.Fatal(err)
	}
	if s.profileURL != "" || s.showProfileAction {
		t.Fatal("subscription URL retained on cancel")
	}
	s.openProfileAction(s.state.Profiles[0], "编辑 YAML")
	s.profileData = "rules: [MATCH,DIRECT]"
	s.profileError = "校验失败"
	tt.Frame()
	if !tt.HasText("校验失败") {
		t.Fatal("editor did not show failure")
	}
	s.busy = true
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if !s.showProfileAction {
		t.Fatal("closed in-flight save")
	}
	s.busy = false
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if s.profileData != "" || s.showProfileAction {
		t.Fatal("editor retained credentials on close")
	}
}

func TestLogFilteringAndPause(t *testing.T) {
	s := &desktop{page: "logs", state: core.Snapshot{Logs: "info one\nerror two\n"}}
	tt := ui.NewTester(s.view, 1080, 760)
	if err := tt.Click("暂停显示"); err != nil {
		t.Fatal(err)
	}
	s.state.Logs = "new content"
	tt.Frame()
	if !s.logsPaused || !strings.Contains(s.pausedLogs, "info one") {
		t.Fatal("pause lost logs")
	}
	if err := tt.Click("恢复显示"); err != nil {
		t.Fatal(err)
	}
	if s.logsPaused {
		t.Fatal("logs still paused")
	}
	if got := filteredLogs("info one\nERROR two\ninfo three", "error"); got != "ERROR two" {
		t.Fatal(got)
	}
}

func TestPaginationAndRuleFiltering(t *testing.T) {
	for _, test := range []struct{ total, page, start, end int }{{0, 3, 0, 0}, {50, 1, 0, 50}, {51, 2, 50, 51}, {101, -1, 0, 50}} {
		page := test.page
		start, end := pageBounds(test.total, &page)
		if start != test.start || end != test.end {
			t.Fatalf("%+v: %d,%d", test, start, end)
		}
	}
	s := &desktop{page: "rules", state: core.Snapshot{Running: true, Rules: []core.Rule{{Type: "Domain", Payload: "example.com", Proxy: "DIRECT"}, {Type: "Match", Proxy: "REJECT"}}}}
	tt := ui.NewTester(s.view, 1080, 760)
	if !tt.HasText("1. Domain · example.com → DIRECT") {
		t.Fatal(tt.Texts())
	}
	s.ruleSearch = "reject"
	tt.Frame()
	if tt.HasText("1. Domain · example.com → DIRECT") || !tt.HasText("2. Match ·  → REJECT") {
		t.Fatal("rule filter/order broken")
	}
}
