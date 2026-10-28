package main

import (
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"

	"clashgo/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func matches(query string, values ...string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func pageBounds(total int, page *int) (int, int) {
	const perPage = 50
	*page = max(0, min(*page, max(0, (total-1)/perPage)))
	start := *page * perPage
	return start, min(start+perPage, total)
}

func pagination(c *ui.Context, total int, page *int) (int, int) {
	start, end := pageBounds(total, page)
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, fmt.Sprintf("共 %d 项 · %d–%d", total, min(start+1, total), end)).Grow(1).TextColor(c.Theme().TextMuted)
		if ui.Button(c, "上一页").Disabled(start == 0).Clicked() {
			*page--
		}
		if ui.Button(c, "下一页").Disabled(end == total).Clicked() {
			*page++
		}
	})
	return pageBounds(total, page)
}

func (s *desktop) connections(c *ui.Context) {
	ui.Text(c, "活动连接 · 关闭连接会中断对应请求；应用可能自动重连。").Wrap().TextColor(c.Theme().TextMuted)
	if ui.TextInput(c, &s.connectionSearch).Placeholder("搜索域名、IP、进程、规则或代理链").Label("搜索连接").FillWidth().Changed() {
		s.connectionPage = 0
	}
	ui.Row(c).Gap(10).Children(func() {
		ui.Select(c, &s.connectionSort, []string{"最新连接", "下载最多", "上传最多"}).Label("连接排序").Width(160)
		if ui.Button(c, "关闭全部连接…").Disabled(s.busy || s.closing || !s.state.Running || s.state.Connections == 0).Clicked() {
			s.confirm("关闭全部活动连接？现有下载、通话和请求可能中断。", func() error { return s.manager.CloseConnections(s.ctx) })
		}
	})
	items := make([]core.Connection, 0, len(s.state.ConnectionDetails))
	for _, item := range s.state.ConnectionDetails {
		m := item.Metadata
		if matches(s.connectionSearch, m.Host, m.SourceIP, m.DestinationIP, m.Process, m.ProcessPath, item.Rule, item.RulePayload, strings.Join(item.Chains, " → ")) {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		switch s.connectionSort {
		case "下载最多":
			return items[i].Download > items[j].Download
		case "上传最多":
			return items[i].Upload > items[j].Upload
		default:
			return items[i].Start.After(items[j].Start)
		}
	})
	start, end := pagination(c, len(items), &s.connectionPage)
	ui.Scroll(c).Grow(1).Gap(10).Children(func() {
		for _, item := range items[start:end] {
			ui.Column(c.Key(item.ID)).Padding(14).Gap(6).Border(1, c.Theme().Border).Radius(10).Children(func() {
				m := item.Metadata
				host := m.Host
				if host == "" {
					host = m.DestinationIP
				}
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, net.JoinHostPort(host, m.DestinationPort)).Bold().Grow(1).Wrap()
					if ui.Button(c, "关闭").Label("关闭连接 " + item.ID).Disabled(s.busy || s.closing || !s.state.Running).Clicked() {
						s.run(func() error { return s.manager.CloseConnection(s.ctx, item.ID) }, "连接已关闭")
					}
				})
				ui.Text(c, m.Network+" · "+m.Type+" · "+net.JoinHostPort(m.SourceIP, m.SourcePort)).FontSize(12).Wrap()
				ui.Text(c, "↑ "+sizeText(item.Upload)+"  ↓ "+sizeText(item.Download)+" · "+item.Start.Local().Format("01-02 15:04:05")).FontSize(12)
				ui.Text(c, item.Rule+" "+item.RulePayload+" → "+strings.Join(item.Chains, " → ")).Wrap().FontSize(12)
				if m.ProcessPath != "" || m.Process != "" {
					ui.Text(c, m.Process+" "+m.ProcessPath).Wrap().FontSize(12).TextColor(c.Theme().TextMuted)
				}
			})
		}
		if len(items) == 0 {
			ui.Text(c, "没有匹配的活动连接；仅显示当前内核捕获的连接。")
		}
	})
}

func (s *desktop) rules(c *ui.Context) {
	ui.Text(c, "内核当前生效规则（保持匹配顺序）；编辑规则请到配置页编辑 YAML。").Wrap().TextColor(c.Theme().TextMuted)
	if ui.TextInput(c, &s.ruleSearch).Label("搜索规则").Placeholder("搜索规则类型、内容或目标代理").FillWidth().Changed() {
		s.rulePage = 0
	}
	var indices []int
	for i, rule := range s.state.Rules {
		if matches(s.ruleSearch, rule.Type, rule.Payload, rule.Proxy) {
			indices = append(indices, i)
		}
	}
	start, end := pagination(c, len(indices), &s.rulePage)
	ui.Scroll(c).Grow(1).Gap(10).Children(func() {
		for _, i := range indices[start:end] {
			rule := s.state.Rules[i]
			ui.Text(c.Key(fmt.Sprint(i)), fmt.Sprintf("%d. %s · %s → %s", i+1, rule.Type, rule.Payload, rule.Proxy)).Wrap()
		}
		if !s.state.Running {
			ui.Text(c, "启动内核后显示生效规则。")
		}
	})
}

func (s *desktop) providers(c *ui.Context) {
	ui.Text(c, "代理与规则资源 · 更新会访问配置指定的服务器；健康检查会对资源中的节点发起探测。").Wrap().TextColor(c.Theme().TextMuted)
	if ui.TextInput(c, &s.providerSearch).Label("搜索资源").FillWidth().Changed() {
		s.providerPage = 0
	}
	var items []core.Provider
	for _, provider := range s.state.Providers {
		if matches(s.providerSearch, provider.Name, provider.Kind) {
			items = append(items, provider)
		}
	}
	start, end := pagination(c, len(items), &s.providerPage)
	ui.Scroll(c).Grow(1).Gap(12).Children(func() {
		for _, provider := range items[start:end] {
			ui.Column(c.Key(provider.Kind+"/"+provider.Name)).Padding(14).Gap(10).Border(1, c.Theme().Border).Radius(10).Children(func() {
				kind, count := "规则", provider.RuleCount
				if provider.Kind == "proxies" {
					kind, count = "代理", len(provider.Proxies)
				}
				ui.Text(c, provider.Name).Bold().Wrap()
				ui.Text(c, fmt.Sprintf("%s · %s · %d 项", kind, provider.VehicleType, count)).FontSize(12)
				if !provider.UpdatedAt.IsZero() {
					ui.Text(c, "更新于 "+provider.UpdatedAt.Local().Format("2006-01-02 15:04:05")).FontSize(12)
				}
				ui.Row(c).Gap(10).Children(func() {
					if ui.Button(c, "更新资源").Label("更新资源 " + provider.Name).Disabled(s.busy || s.closing || !s.state.Running || !strings.EqualFold(provider.VehicleType, "HTTP")).Clicked() {
						s.run(func() error { return s.manager.UpdateProvider(s.ctx, provider.Kind, provider.Name, false) }, "资源已更新")
					}
					if provider.Kind == "proxies" && ui.Button(c, "健康检查").Label("健康检查 "+provider.Name).Disabled(s.busy || s.closing || !s.state.Running).Clicked() {
						s.run(func() error { return s.manager.UpdateProvider(s.ctx, provider.Kind, provider.Name, true) }, "资源健康检查已完成")
					}
				})
			})
		}
		if !s.state.Running {
			ui.Text(c, "启动内核后显示资源。")
		}
	})
}

func (s *desktop) delayControls(c *ui.Context) {
	var names []string
	for _, group := range s.state.Groups {
		names = append(names, group.Name)
		names = append(names, group.All...)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	if s.testURL == "" {
		s.testURL = "https://www.gstatic.com/generate_204"
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Select(c, &s.testProxy, names).Label("测速节点或代理组").Width(180).Disabled(s.busy)
		ui.TextInput(c, &s.testURL).Label("HTTPS 测速地址").Grow(1).Disabled(s.busy)
		if ui.Button(c, "测试延迟").Disabled(s.busy || s.closing || !s.state.Running || s.testProxy == "").Clicked() {
			name, address := s.testProxy, s.testURL
			var delay int
			s.delayResult = ""
			s.runWithResult(func() error { var err error; delay, err = s.manager.TestDelay(s.ctx, name, address); return err }, "延迟测试完成", func(err error) {
				if err == nil {
					s.delayResult = fmt.Sprintf("%s · %d ms", name, delay)
				}
			})
		}
	})
	ui.Text(c, "延迟测试会经所选节点请求上方 HTTPS 地址（5 秒超时），不是带宽测速。").FontSize(12).Wrap().TextColor(c.Theme().TextMuted)
	if s.delayResult != "" {
		ui.Text(c, s.delayResult)
	}
}

func (s *desktop) tools(c *ui.Context) {
	ui.Text(c, "DNS 查询使用当前内核解析器，需配置启用 DNS；不是全量 DNS 查询历史。").Wrap().TextColor(c.Theme().TextMuted)
	if s.dnsType == "" {
		s.dnsType = "A"
	}
	ui.Row(c).Gap(10).Children(func() {
		ui.TextInput(c, &s.dnsName).Label("DNS 名称").Placeholder("example.com").Grow(1)
		ui.Select(c, &s.dnsType, []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "PTR"}).Label("DNS 类型").Width(100)
		if ui.Button(c, "查询 DNS").Disabled(s.busy || s.closing || !s.state.Running || strings.TrimSpace(s.dnsName) == "").Clicked() {
			name, kind := s.dnsName, s.dnsType
			var result string
			s.dnsResult = ""
			s.runWithResult(func() error { var err error; result, err = s.manager.QueryDNS(s.ctx, name, kind); return err }, "DNS 查询完成", func(err error) {
				if err == nil {
					s.dnsResult = result
				}
			})
		}
	})
	if ui.Button(c, "清空 DNS 缓存").Disabled(s.busy || s.closing || !s.state.Running).Clicked() {
		s.run(func() error { return s.manager.FlushDNS(s.ctx) }, "DNS 缓存已清空")
	}
	ui.TextArea(c, &s.dnsResult).ReadOnly(true).Label("DNS 查询结果").FillWidth().Grow(1)
}

func filteredLogs(logs, query string) string {
	if strings.TrimSpace(query) == "" {
		return logs
	}
	var lines []string
	for _, line := range strings.Split(logs, "\n") {
		if matches(query, line) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func (s *desktop) logs(c *ui.Context) {
	ui.Text(c, "最近 64 KiB 的内核输出 · 日志可能包含节点或订阅信息，请勿公开分享").FontSize(12).Wrap().TextColor(c.Theme().TextMuted)
	ui.TextInput(c, &s.logSearch).Label("过滤日志").Placeholder("搜索关键词或日志级别").FillWidth()
	logs := s.state.Logs
	if s.logsPaused {
		logs = s.pausedLogs
	}
	logs = filteredLogs(logs, s.logSearch)
	ui.Row(c).Gap(10).Children(func() {
		label := "暂停显示"
		if s.logsPaused {
			label = "恢复显示"
		}
		if ui.Button(c, label).Clicked() {
			s.logsPaused = !s.logsPaused
			s.pausedLogs = s.state.Logs
		}
		if ui.Button(c, "清空日志").Disabled(s.busy || s.closing).Clicked() {
			s.pausedLogs = ""
			s.run(func() error { s.manager.ClearLogs(); return nil }, "内存日志已清空")
		}
		if ui.Button(c, "导出日志…").Disabled(s.busy || s.closing).Clicked() {
			s.exportData("导出当前显示日志（可能包含凭据）", "clash-go.log", []byte(logs))
		}
	})
	ui.TextArea(c, &logs).ReadOnly(true).Label("内核日志").FillWidth().Grow(1)
}

func (s *desktop) exportData(title, name string, data []byte) {
	s.run(func() error {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: s.window, Title: title, DefaultPath: name})
		if err != nil || path == "" {
			return err
		}
		return core.ExportFile(path, data)
	}, "导出对话框已完成")
}

func (s *desktop) confirm(text string, action func() error) {
	s.confirmText, s.confirmAction, s.showConfirm = text, action, true
}

func (s *desktop) confirmDialog(c *ui.Context) {
	ui.Modal(c, &s.showConfirm, func() {
		ui.Column(c).Width(440).Gap(16).Children(func() {
			ui.Text(c, s.confirmText).Wrap()
			ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消操作").Clicked() {
					s.showConfirm = false
				}
				if ui.PrimaryButton(c, "确认操作").Disabled(s.busy || s.closing).Clicked() {
					s.showConfirm = false
					s.run(s.confirmAction, "操作已完成")
				}
			})
		})
	})
}
