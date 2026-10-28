# 构建与发布

## 自动发布流程

`.github/workflows/release.yml` 在推送 `v主版本.次版本.修订号` 标签时运行，也可以在 GitHub Actions 的 **Release → Run workflow** 中填写已有标签重新执行。普通分支提交不会创建 Release。

1. 检查标签格式和提交，执行 Go 静态检查、竞态测试及发布脚本测试。
2. 从同一提交并行构建六个目标。Linux 和 Windows 在 Ubuntu runner 上交叉编译；macOS 在 macOS runner 上生成应用包和 DMG。
3. MyGo CLI 负责生成安装程序。版本取自标签，写入安装包、应用元数据和关于页面；不需要手动修改 Go 源码版本。
4. 六个目标全部成功后，验证产物齐全，生成 `SHA256SUMS`，创建草稿、上传文件，再发布 GitHub Release。

| 平台 | 架构 | Release 文件 |
| --- | --- | --- |
| Windows | amd64、arm64 | 每个架构一个 NSIS `-setup.exe` 安装程序，提供当前用户安装、开始菜单快捷方式和卸载入口 |
| macOS | amd64、arm64 | 每个架构一个 `.dmg`，内含 `.app` |
| Linux | amd64、arm64 | 每个架构一份 `.deb`、`.tar.gz` 和 `-install.sh` |

共 10 个产物及一份 `SHA256SUMS`。`.deb` 声明 GTK 3 依赖，不要求 WebKitGTK。Linux 安装脚本与对应压缩包一同下载后，可使用 `sh <安装脚本> <压缩包>` 安装到当前用户目录。该脚本不是启动应用所必需的。

不生成 Android/iOS、RPM、AppImage 或额外 CPU 架构的安装包。应用框架仍为 Go/MyGo，没有增加 Flutter、Node.js 或 WebView。

## 首次启用

将此工作流和打包配置提交到 GitHub 仓库的默认分支。仓库需要允许 GitHub Actions；构建工作只读取仓库，只有最终发布任务申请 `contents: write`，使用自动提供的 `GITHUB_TOKEN`，不需要保存个人访问令牌。

确认当前代码已提交并推送后，选择未使用过的版本号，例如：

```sh
git tag v0.2.0
git push origin v0.2.0
```

标签必须指向包含本工作流和脚本的提交。支持 `v0.2.0` 形式的稳定版本；暂不接受预发布后缀，每个数字不超过 65535，以兼容 Windows 版本元数据。

在 GitHub 的 **Actions → Release** 查看进度。发布后在 **Releases** 下载对应系统和架构的文件。推送标签前的网络/认证失败不会触发 Actions。

## 失败与重试

- 检查或任一构建失败都不会发布不完整的 Release。已成功的构建产物保留为 Actions artifact 14 天。
- 同一标签串行执行，不取消正在运行的发布。
- 上传中途失败可能留下草稿；可重新运行失败任务或手动填写同一标签重跑，替换草稿中的同名附件后再发布。
- 已经公开发布的版本不会被覆盖。修复应使用新标签，不移动已发布标签。
- 发布前再次核对远端标签指向的提交，防止构建期间标签被移动。

## 本地检查与打包

MyGo CLI 通过 `go.mod` 的 `tool` 声明固定到项目所用的版本，无需安装其他 Go 打包工具。Python 脚本只使用标准库。

```sh
make check
python3 -m unittest discover -s scripts -p 'test_*.py' -v

python3 scripts/release.py prepare v0.2.0 --target linux-amd64
go tool mygo build -platform linux/amd64
python3 scripts/release.py collect v0.2.0 --target linux-amd64
```

`prepare` 会修改本地 `mygo.json` 的版本，并在 `build/` 中生成依赖许可证汇总；Actions 中只修改临时检出的工作区，不提交回仓库。运行后可自行保留或恢复配置版本。

Windows 安装程序需要 NSIS（Linux 上安装 `nsis`）；macOS DMG 需要在 macOS 上构建。产物位于 `build/packages/<系统>-<架构>/`，用于发布的标准化文件位于 `build/release/`。收集步骤拒绝缺少安装程序或覆盖已有收集文件；本地重复收集请使用干净的输出目录。

## 签名、许可证与运行边界

- 当前 Windows 安装包未签名；macOS 为 ad-hoc 签名，未使用 Developer ID 或 Apple 公证。SmartScreen/Gatekeeper 可能提示或阻止运行。这些构建不等同于已签名发行版。
- 本流程不接收、不索要或提交签名证书。若后续需要可信发布，应单独接入 Windows 签名证书或云签名服务、Apple Developer ID 和公证凭据，通过 GitHub Secrets 管理。
- 安装包包含项目 `LICENSE`、`THIRD_PARTY_NOTICES.md`、Go 工具链和模块依赖的许可证汇总。生成汇总失败会停止打包。
- 不捆绑 mihomo 内核；首次运行保持官方下载安装和哈希校验行为。首次安装仍依赖能访问 GitHub。
- GitHub Actions 会消耗仓库构建额度；macOS runner 的计费和可用性由 GitHub 账户与仓库类型决定。
- CI 构建成功只证明编译与打包成功，不代表六种目标均完成真实设备上的安装、升级和卸载验证。
