package main

import (
	"fmt"
	"strings"

	"clashgo/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (s *desktop) backupButtons(c *ui.Context) {
	ui.Row(c).Gap(10).Children(func() {
		if ui.Button(c, "备份全部配置…").Disabled(s.busy || s.closing || len(s.state.Profiles) == 0).Clicked() {
			s.run(func() error {
				data, err := s.manager.BackupProfiles()
				if err != nil {
					return err
				}
				path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: s.window, Title: "备份配置（明文包含节点凭据，请妥善保管）", DefaultPath: "clash-go-profiles.json"})
				if err != nil || path == "" {
					return err
				}
				return core.ExportFile(path, data)
			}, "配置备份对话框已完成")
		}
		if ui.Button(c, "从备份添加配置…").Disabled(s.busy || s.closing || !s.state.KernelReady).Clicked() {
			s.run(func() error {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: s.window, Title: "导入 Clash Go 配置备份（追加，不覆盖现有配置）", Filters: []mygo.FileFilter{{Name: "JSON 备份", Extensions: []string{"json"}}}})
				if err != nil || len(paths) == 0 {
					return err
				}
				data, err := core.ReadBackup(paths[0])
				if err != nil {
					return err
				}
				return s.manager.RestoreProfiles(s.ctx, data)
			}, "备份导入对话框已完成；成功导入的配置可在列表中启用")
		}
	})
}

func (s *desktop) profileButtons(c *ui.Context, profile core.Profile) {
	disabled := s.busy || s.closing
	ui.Row(c).Gap(8).Children(func() {
		for _, action := range []string{"重命名", "编辑 YAML", "更新订阅"} {
			if ui.Button(c, action).Label(action + " " + profile.Name).Disabled(disabled || (action != "重命名" && !s.state.KernelReady)).Clicked() {
				if action == "编辑 YAML" {
					var data []byte
					s.runWithResult(func() error { var err error; data, err = s.manager.ReadProfile(profile.ID); return err }, "", func(err error) {
						if err == nil {
							s.openProfileAction(profile, action)
							s.profileData = string(data)
						}
					})
				} else {
					s.openProfileAction(profile, action)
				}
			}
		}
		if ui.Button(c, "导出…").Label("导出配置 " + profile.Name).Disabled(disabled).Clicked() {
			s.run(func() error {
				data, err := s.manager.ReadProfile(profile.ID)
				if err != nil {
					return err
				}
				path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: s.window, Title: "导出配置（包含节点凭据，请妥善保管）", DefaultPath: "profile.yaml", Filters: []mygo.FileFilter{{Name: "YAML", Extensions: []string{"yaml"}}}})
				if err != nil || path == "" {
					return err
				}
				return core.ExportFile(path, data)
			}, "配置导出对话框已完成")
		}
		if ui.Button(c, "删除…").Label("删除配置 " + profile.Name).Disabled(disabled || (s.state.Running && profile.ID == s.state.ActiveProfile)).Clicked() {
			s.confirm(fmt.Sprintf("删除配置“%s”？已保存的配置文件将删除，无法撤销。", profile.Name), func() error { return s.manager.DeleteProfile(profile.ID) })
		}
	})
}

func (s *desktop) openProfileAction(profile core.Profile, action string) {
	s.profileID, s.profileName, s.profileAction = profile.ID, profile.Name, action
	s.profileData, s.profileURL, s.profileError = "", "", ""
	s.showProfileAction = true
}

func (s *desktop) clearProfileAction() {
	s.showProfileAction = false
	s.profileID, s.profileData, s.profileURL, s.profileError = "", "", "", ""
}

func (s *desktop) profileDialog(c *ui.Context) {
	dialog := ui.Modal(c, &s.showProfileAction, func() {
		width := float32(520)
		if s.profileAction == "编辑 YAML" {
			width = 680
		}
		ui.Column(c).Width(width).Gap(12).Children(func() {
			ui.Text(c, s.profileAction+" · "+s.profileName).FontSize(20).Bold().Wrap()
			switch s.profileAction {
			case "重命名":
				ui.TextInput(c, &s.profileName).Label("配置名称").FillWidth().AutoFocus().Disabled(s.busy)
			case "更新订阅":
				ui.Text(c, "重新填写 HTTPS 地址；下载并校验成功才替换配置。链接不保存。当前配置更新会重启内核并中断连接，失败恢复原配置。").Wrap()
				ui.TextInput(c, &s.profileURL).Label("更新订阅地址").Placeholder("https://example.com/config.yaml").FillWidth().AutoFocus().Disabled(s.busy)
			case "编辑 YAML":
				ui.Text(c, "编辑的是安全规范化后的配置，包含节点凭据。规则、DNS、代理及代理组均可在此修改；监听端口、TUN 等仍由应用接管。当前配置保存会重启内核。").Wrap().FontSize(12)
				ui.TextArea(c, &s.profileData).Label("配置 YAML").FillWidth().Height(300).Disabled(s.busy)
			}
			if s.profileError != "" {
				ui.Text(c, s.profileError).Wrap().TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消编辑").Disabled(s.busy).Clicked() {
					s.clearProfileAction()
				}
				if ui.PrimaryButton(c, "保存修改").Disabled(s.busy || s.closing).Clicked() {
					s.saveProfileAction()
				}
			})
		})
	})
	if dialog.Dismissed() {
		if s.busy {
			s.showProfileAction = true
		} else {
			s.clearProfileAction()
		}
	}
}

func (s *desktop) saveProfileAction() {
	id, action, name, data, address := s.profileID, s.profileAction, s.profileName, s.profileData, strings.TrimSpace(s.profileURL)
	s.profileError = ""
	saved := false
	s.runWithResult(func() error {
		var err error
		switch action {
		case "重命名":
			err = s.manager.RenameProfile(id, name)
		case "编辑 YAML":
			err = s.manager.ReplaceProfile(s.ctx, id, []byte(data))
		case "更新订阅":
			var downloaded core.DownloadedProfile
			downloaded, err = core.Download(s.ctx, address)
			if err == nil {
				err = s.manager.ReplaceProfile(s.ctx, id, downloaded.Data)
			}
		default:
			err = fmt.Errorf("无效配置操作")
		}
		saved = err == nil
		return err
	}, "配置已保存", func(err error) {
		if saved {
			s.clearProfileAction()
		} else if err != nil {
			s.profileError = err.Error()
		}
	})
}
