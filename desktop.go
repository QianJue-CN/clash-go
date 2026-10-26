package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"clashgo/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type desktop struct {
	manager                          *core.Manager
	ctx                              context.Context
	window                           *mygo.Window
	state                            core.Snapshot
	page, port, subscription, search string
	kernelProgress                   string
	message, failure                 string
	busy, closing                    bool
}

func (s *desktop) reportKernel(message string) {
	s.window.Update(func() { s.kernelProgress = message })
}

func (s *desktop) importAndStart(data []byte) error {
	if err := s.manager.Import(s.ctx, data); err != nil {
		return err
	}
	return s.manager.Start(s.ctx)
}

// All UI state stays on the main thread; IO runs on one worker at a time.
func (s *desktop) run(action func() error, message string) {
	if s.busy || s.closing {
		return
	}
	s.busy = true
	s.kernelProgress = ""
	go func() {
		err := action()
		if err == nil {
			err = s.manager.Refresh(s.ctx)
		}
		state := s.manager.Snapshot()
		s.window.Update(func() {
			s.busy, s.state = false, state
			if err != nil {
				s.failure = err.Error()
			} else if message != "" {
				s.failure, s.message = "", message
			}
		})
	}()
}

func (s *desktop) view(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Fill().Gap(0).Background(t.Background).Children(func() {
		ui.Column(c).Width(210).FillHeight().Padding(20, 14).Gap(14).Background(t.Surface).Children(func() {
			ui.Text(c, "Clash Go").FontSize(25).Bold().Padding(8)
			ui.Text(c, "NATIVE · MIHOMO").FontSize(11).TextColor(t.TextMuted).Padding(0, 8)
			ui.Sidebar(c, &s.page, func() {
				ui.SidebarItem(c, "overview", nil, "概览")
				ui.SidebarItem(c, "proxies", nil, "代理组")
				ui.SidebarItem(c, "profiles", nil, "配置")
				ui.SidebarItem(c, "logs", nil, "运行日志")
				ui.SidebarItem(c, "settings", nil, "设置")
			}).Grow(1).FillWidth()
			ui.Divider(c)
			status := "内核未运行"
			if s.state.Running {
				status = "内核运行中"
			}
			ui.Text(c, status).FontSize(12)
			ui.Text(c, fmt.Sprintf("127.0.0.1:%d", s.state.Port)).FontSize(12).TextColor(t.TextMuted)
		})
		ui.Column(c).Grow(1).FillHeight().Padding(28).Gap(18).Children(func() {
			ui.Row(c).Gap(12).Children(func() {
				titles := map[string]string{"overview": "概览", "proxies": "代理组", "profiles": "配置", "logs": "运行日志", "settings": "设置"}
				ui.Text(c, titles[s.page]).FontSize(28).Bold().Grow(1)
				if s.busy {
					ui.Text(c, "处理中…").TextColor(t.TextMuted)
				}
				if ui.Button(c, "刷新").Disabled(s.busy || s.closing).Clicked() {
					s.run(func() error { return nil }, "状态已刷新")
				}
			})
			if s.kernelProgress != "" {
				ui.Text(c, s.kernelProgress).Wrap().TextColor(t.TextMuted)
			}
			if s.failure != "" {
				ui.Text(c, s.failure).TextColor(t.Danger).Wrap().Label("错误信息")
			} else if s.message != "" {
				ui.Text(c, s.message).TextColor(t.TextMuted).Wrap()
			}
			switch s.page {
			case "overview":
				s.overview(c)
			case "proxies":
				s.proxies(c)
			case "profiles":
				s.profiles(c)
			case "logs":
				ui.Text(c, "最近 64 KiB 的内核输出 · 日志可能包含节点或订阅信息，请勿公开分享").FontSize(12).Wrap().TextColor(t.TextMuted)
				logs := s.state.Logs
				ui.TextArea(c, &logs).ReadOnly(true).Label("内核日志").FillWidth().Grow(1)
			case "settings":
				s.settings(c)
			}
		})
	})
}

func card(c *ui.Context, title, value string) {
	t := c.Theme()
	ui.Column(c).Grow(1).Padding(18).Gap(10).Radius(12).Border(1, t.Border).Children(func() {
		ui.Text(c, title).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, value).FontSize(23).Bold()
	})
}
func sizeText(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	if n < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.2f GiB", float64(n)/(1024*1024*1024))
}

func (s *desktop) overview(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Gap(20).Children(func() {
		ui.Column(c).Padding(24).Gap(14).Radius(14).Background(t.Surface).Children(func() {
			title, detail := "准备连接", "导入 mihomo 配置，启动本地 HTTP / SOCKS5 代理。"
			if s.state.Running {
				title, detail = "内核已启动", "运行时间 "+time.Since(s.state.Started).Truncate(time.Second).String()+" · "+s.state.Version
			}
			ui.Text(c, title).FontSize(26).Bold()
			ui.Text(c, detail).TextColor(t.TextMuted).Wrap()
			if s.state.Running {
				if ui.Button(c, "停止内核").Disabled(s.busy || s.closing).Clicked() {
					s.run(s.manager.Stop, "内核已停止")
				}
			} else {
				if ui.PrimaryButton(c, "启动内核").Disabled(s.busy || s.closing || !s.state.HasConfig || !s.state.KernelReady).Clicked() {
					s.run(func() error { return s.manager.Start(s.ctx) }, "内核已启动；请将需要代理的应用连接到本地端口")
				}
				if !s.state.KernelReady && ui.Button(c, "下载/重试安装内核").Disabled(s.busy || s.closing).Clicked() {
					s.run(func() error { return s.manager.Initialize(s.ctx, s.reportKernel) }, "内核已就绪；导入配置后将自动启动")
				}
				if !s.state.HasConfig && ui.Button(c, "前往导入配置").Clicked() {
					s.page = "profiles"
				}
			}
		})
		ui.Row(c).Gap(12).Children(func() {
			card(c, "累计上传", sizeText(s.state.Upload))
			card(c, "累计下载", sizeText(s.state.Download))
			card(c, "活动连接", fmt.Sprint(s.state.Connections))
		})
		ui.Text(c, "代理模式").Bold()
		ui.Row(c).Gap(10).Children(func() {
			for _, mode := range []struct{ value, label string }{{"rule", "规则"}, {"global", "全局"}, {"direct", "直连"}} {
				label := mode.label
				if s.state.Mode == mode.value {
					label = "✓ " + label
				}
				if ui.Button(c.Key(mode.value), label).Grow(1).Disabled(s.busy || !s.state.Running || s.closing).Clicked() {
					s.run(func() error { return s.manager.SetMode(s.ctx, mode.value) }, "已切换为"+mode.label+"模式（本次运行）")
				}
			}
		})
		ui.Divider(c)
		ui.Text(c, "连接方式").Bold()
		ui.Text(c, fmt.Sprintf("HTTP / SOCKS5   127.0.0.1:%d", s.state.Port)).FontSize(17)
		ui.Text(c, "本工具不会自动改变系统代理。请在浏览器或其他应用中手动填写以上地址；关闭内核后，请同时关闭这些应用的代理设置。").Wrap().TextColor(t.TextMuted)
		ui.Text(c, "默认仅本机访问 · 无 TUN · 无管理员权限").FontSize(12).TextColor(t.TextMuted)
	})
}

func (s *desktop) proxies(c *ui.Context) {
	ui.TextInput(c, &s.search).Placeholder("搜索代理组").Label("搜索代理组").FillWidth()
	if !s.state.Running {
		ui.Text(c, "启动内核后显示代理组和当前节点。").TextColor(c.Theme().TextMuted)
		return
	}
	ui.Scroll(c).Grow(1).Gap(12).Children(func() {
		shown := 0
		for _, group := range s.state.Groups {
			if !strings.Contains(strings.ToLower(group.Name), strings.ToLower(s.search)) {
				continue
			}
			shown++
			ui.Column(c.Key(group.Name)).Padding(18).Gap(10).Radius(12).Border(1, c.Theme().Border).Children(func() {
				ui.Row(c).Gap(10).Children(func() {
					ui.Text(c, group.Name).Bold().Grow(1)
					ui.Text(c, group.Type).FontSize(12).TextColor(c.Theme().TextMuted)
				})
				selected := group.Now
				if ui.Select(c, &selected, group.All).Label(group.Name + " 的节点").FillWidth().Disabled(s.busy || s.closing || group.Type != "Selector").Changed() {
					s.run(func() error { return s.manager.Select(s.ctx, group.Name, selected) }, "已切换代理节点")
				}
				if group.Type != "Selector" {
					ui.Text(c, "此组由内核自动选择节点").FontSize(12).TextColor(c.Theme().TextMuted)
				}
			})
		}
		if shown == 0 {
			ui.Text(c, "没有匹配的代理组")
		}
	})
}

func (s *desktop) profiles(c *ui.Context) {
	ui.Scroll(c).Grow(1).Gap(18).Children(func() {
		status := "尚未导入配置"
		if s.state.HasConfig {
			status = "已保存配置 · 导入新配置将替换当前配置"
		}
		ui.Text(c, status).Bold()
		ui.Text(c, s.manager.ConfigPath()).FontSize(12).Wrap().TextColor(c.Theme().TextMuted)
		ui.Text(c, "仅导入可信的完整 mihomo YAML。内核校验成功后才替换旧配置并自动启动；原始文件保持不变。").Wrap()
		if s.state.Running {
			ui.Text(c, "请先停止内核再导入配置。").TextColor(c.Theme().TextMuted)
		}
		disabled := s.busy || s.closing || s.state.Running || !s.state.KernelReady
		if ui.PrimaryButton(c, "导入本地 YAML…").Disabled(disabled).Clicked() {
			s.run(func() error {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: s.window, Title: "导入 mihomo 配置", Filters: []mygo.FileFilter{{Name: "YAML 配置", Extensions: []string{"yaml", "yml"}}}})
				if err != nil || len(paths) == 0 {
					return err
				}
				data, err := core.ReadConfig(paths[0])
				if err != nil {
					return err
				}
				return s.importAndStart(data)
			}, "文件选择已完成；导入成功后自动启动内核")
		}
		ui.Divider(c)
		ui.Text(c, "HTTPS 订阅").Bold()
		ui.TextInput(c, &s.subscription).Placeholder("https://example.com/config.yaml").Label("HTTPS 订阅地址").FillWidth().Disabled(disabled)
		if ui.Button(c, "下载并导入").Disabled(disabled || strings.TrimSpace(s.subscription) == "").Clicked() {
			address := strings.TrimSpace(s.subscription)
			s.run(func() error {
				data, err := core.Download(s.ctx, address)
				if err != nil {
					return err
				}
				return s.importAndStart(data)
			}, "订阅已校验并导入，内核已启动")
		}
		ui.Text(c, "不保存订阅地址；最大 8 MiB。支持 http / inline provider，不支持 Base64 节点列表和本地文件 provider。").FontSize(12).Wrap().TextColor(c.Theme().TextMuted)
		ui.Divider(c)
		ui.Text(c, "导入时接管监听设置").Bold()
		ui.Text(c, "保留节点、代理组、规则及 DNS 解析设置。覆盖本地端口，禁用局域网访问、TUN、额外监听器及 DNS 监听；不导入脚本和外部面板。Provider 缓存统一放在应用数据目录。").Wrap().TextColor(c.Theme().TextMuted)
	})
}

func (s *desktop) settings(c *ui.Context) {
	ui.Scroll(c).Grow(1).Gap(16).Children(func() {
		ui.Text(c, "mihomo 内核").Bold()
		ui.Text(c, "首次启动自动下载官方最新稳定版；已有内核可离线使用，无需手动选择文件。").Wrap().TextColor(c.Theme().TextMuted)
		version := "尚未安装"
		if s.state.KernelReady {
			version = s.state.KernelVersion
		}
		ui.Text(c, "已安装版本："+version).Bold()
		if ui.PrimaryButton(c, "检查并更新内核").Disabled(s.busy || s.closing).Clicked() {
			s.run(func() error { return s.manager.UpdateKernel(s.ctx, s.reportKernel) }, "内核检查/更新已完成")
		}
		ui.Text(c, "下载前检查系统架构，执行前核对 SHA-256。运行中更新会重启内核、恢复模式和节点选择，现有连接会短暂中断；失败保留旧版本。停止状态更新后仍保持停止。").Wrap().TextColor(c.Theme().TextMuted)
		ui.Link(c, "mihomo 发行版与 GPL-3.0 源码 ↗", "https://github.com/MetaCubeX/mihomo/releases")
		ui.Divider(c)
		ui.Text(c, "本地 HTTP / SOCKS5 端口（1024–65535）").FontSize(12)
		ui.TextInput(c, &s.port).Label("代理端口").Width(160).Disabled(s.busy || s.state.Running || s.closing)
		ui.Row(c).Gap(10).Children(func() {
			if ui.PrimaryButton(c, "保存设置").Disabled(s.busy || s.state.Running || s.closing).Clicked() {
				portText := s.port
				s.run(func() error {
					port, err := strconv.Atoi(strings.TrimSpace(portText))
					if err != nil {
						return fmt.Errorf("代理端口必须是整数")
					}
					return s.manager.SaveSettings(core.Settings{Port: port})
				}, "代理端口已保存")
			}
		})
		ui.Divider(c)
		ui.Text(c, "关于 Clash Go").Bold()
		ui.Text(c, "0.1.0 · MyGo 原生 UI · 独立 mihomo 内核").TextColor(c.Theme().TextMuted)
		ui.Text(c, "没有 HTML、JavaScript 或 WebView。界面跟随系统主题；后台请求不会阻塞窗口。").Wrap()
		ui.Link(c, "MyGo ↗", "https://github.com/egoist/mygo")
	})
}
