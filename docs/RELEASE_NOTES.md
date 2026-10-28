## 下载选择

- Windows：选择对应架构的 `windows-amd64-setup.exe` 或 `windows-arm64-setup.exe`。
- macOS：Intel 设备选择 `darwin-amd64.dmg`，Apple Silicon 设备选择 `darwin-arm64.dmg`，将应用拖入 Applications。
- Linux：Debian/Ubuntu 选择对应架构的 `.deb`；其他发行版可使用 `.tar.gz`，需要 GTK 3 和可用的图形环境。
- `SHA256SUMS` 包含所有安装包、压缩包及安装脚本的 SHA-256。

## 安装与运行说明

- Windows 安装程序没有代码签名，可能触发 SmartScreen 提示。
- macOS 应用仅作 ad-hoc 签名，未经过 Apple 公证，Gatekeeper 可能阻止打开；请仅在确认来源可信后按系统提示允许打开，不需要关闭系统安全机制。
- 安装包不附带 mihomo；首次启动从官方 GitHub Releases 下载并校验内核，需要可用网络。
- 本工具仅提供本机 HTTP / SOCKS5 代理，不会自动修改系统代理，也不启用 TUN。
- 配置、备份和日志可能包含明文凭据，请勿公开分享。
