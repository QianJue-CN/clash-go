# Clash Go

基于 [MyGo](https://github.com/egoist/mygo) 原生 UI 与 [mihomo](https://github.com/MetaCubeX/mihomo) 内核的轻量桌面代理工具。

**全部界面使用 Go + `mygo/ui` 编写**，由 MyGo 原生绘制，不使用 HTML、JavaScript、Electron 或 WebView。这里的“原生 UI”指 MyGo 的 GPU 绘制控件，不是每个平台的系统控件封装。内核以独立子进程运行，通过本机鉴权 REST API 管理。

## 已实现

- 原生中文界面，概览、代理组、配置、日志、设置五个页面，跟随系统明暗主题。
- 首次自动下载并启用最新稳定版 mihomo，无需选择内核；已有配置自动启动，正常退出时停止子进程。
- 设置页一键检查/更新内核，显示下载进度，校验 SHA-256；运行中更新恢复模式与节点，切换失败回滚。
- 本地 YAML / HTTPS 订阅导入，8 MiB 限制，先执行 `mihomo -t` 校验再原子替换配置。
- 规则 / 全局 / 直连模式切换，Selector 代理组节点选择与搜索。
- 累计上传、累计下载、活动连接数、内核版本，每两秒刷新。
- 最近 64 KiB 的内核日志；网络和进程操作在后台执行。
- 可配置 HTTP / SOCKS5 混合端口，默认 `127.0.0.1:7890`。
- 单实例、持久化代理端口与托管内核版本、启动端口冲突检查。

## 环境与构建

- **Go 1.27.3+**，对应固定依赖 MyGo `v0.6.4`。无需 Bun、Node.js 或 C 编译器。
- Linux 原生窗口需要 GTK 3，例如 Ubuntu/Debian：`sudo apt install libgtk-3-0`（部分发行版包名为 `libgtk-3-0t64`），以及可用的图形会话/OpenGL 驱动。无需 WebKitGTK。
- MyGo 还支持 macOS 12+、Windows 10/11；本项目的实际运行验证以 Linux 为准。
- 内核自动从 [mihomo 官方 Releases](https://github.com/MetaCubeX/mihomo/releases) 下载，支持 Linux/macOS/Windows 的 amd64、arm64；amd64 使用兼容版，避免依赖较新的 CPU 指令集。首次安装需要能访问 GitHub，无需手动下载或 chmod。

```sh
make build
./build/clash-go
```

也可以直接运行：

```sh
go run -buildvcs=false .
```

若本地 Go 版本较旧，Go 默认的 `GOTOOLCHAIN=auto` 会下载所需工具链。如果默认 Go 模块代理不可达，可按你的网络环境选择可信的 `GOPROXY`，不要关闭校验。

`-buildvcs=false` 只关闭版本信息嵌入，使源码在未初始化 Git 或父目录有无效 `.git` 时也能构建。

## 第一次使用

1. 启动应用，等待自动下载、校验、解压并启用最新稳定版内核。首次没有代理配置时只安装内核，不伪造节点或直连配置。
2. 打开 **配置**，导入可信服务商的完整 mihomo YAML 或 HTTPS 订阅；校验成功后自动启动内核。已有配置时，下次打开应用也会自动启动。
3. 若端口占用，在 **设置** 改成空闲端口并保存，再到 **概览** 点击 **启动内核**。
4. 在浏览器或需要代理的应用中，手动配置 HTTP 或 SOCKS5 代理 `127.0.0.1:<你设置的端口>`。
5. 在 **代理组** 切换手动选择组的节点；URLTest、Fallback 等组由内核自动选择。

若只想验证运行，可以导入 [`examples/direct.yaml`](examples/direct.yaml)。这个配置只包含 DIRECT 和 REJECT，**不包含远程代理节点，不提供翻墙能力**。

例如，默认端口下通过 HTTP 代理访问：

```sh
curl --proxy http://127.0.0.1:7890 https://example.com
# SOCKS5 代理，让内核解析域名：
curl --proxy socks5h://127.0.0.1:7890 https://example.com
```

**本工具不会自动修改系统代理。** 停止内核后，请关闭你在其他应用中手动设置的代理，否则这些应用可能无法联网。如果已有 Clash/mihomo 占用 7890，可在设置中改成 17890 等空闲端口，无需停止其他代理程序。

## 内核自动安装与更新

- 启动时优先校验已安装内核，已有可用版本不依赖网络；首次缺失或损坏时查询官方 GitHub API 的最新稳定版并下载。
- **设置 → 检查并更新内核** 查询最新版；相同版本不重复下载、不重启，也不会降级。下载失败可再次点击重试。
- 压缩包必须匹配官方 API 给出的大小及 SHA-256，缺失校验值则拒绝安装。只允许官方 GitHub HTTPS 下载/重定向地址；限制压缩包 128 MiB、解压后 256 MiB，拒绝危险 ZIP 路径。
- 下载到独立临时文件，执行 `mihomo -v` 自检及现有配置的 `-t` 校验后才切换；运行中更新会短暂断开现有连接并重启，恢复更新前模式和 Selector 节点。已停止的内核更新后保持停止。
- 新内核启动、状态恢复或激活记录写入失败时保留旧版本并尝试恢复运行；应用正在退出时不重新拉起内核。成功切换后清理旧内核。
- 安装记录原子写入，之后启动前复核本地可执行文件哈希。旧版 `settings.json` 中的 `binary` 字段不再使用，代理端口及代理配置保持不变，也不会删除以前手动指定的外部内核。

内核下载遵循进程的 `HTTPS_PROXY` / `NO_PROXY` 环境设置（本地控制 API 仍不使用代理）。若网络无法直连 GitHub，可在已有代理可用时启动，例如：

```sh
HTTPS_PROXY=http://127.0.0.1:7890 ./build/clash-go
```

不要把首次下载依赖的代理指向尚未启动的本工具。GitHub 限流、TLS/网络错误会显示在界面，不会绕过证书或哈希校验，也不会改用第三方镜像。SHA-256 用于校验资源完整性，不等同于独立的发布签名；官方仓库和 HTTPS 是信任来源。

## 配置兼容性与安全边界

导入不是原样执行订阅。程序保留 `proxies`、`proxy-groups`、`proxy-providers`、`rule-providers`、`rules`、`sub-rules`、`hosts`、`dns`、`mode`、`log-level`、`ipv6`、`unified-delay`、`tcp-concurrent`、`geodata-mode`，其他顶层字段不导入。

- 应用接管混合端口、监听地址、控制器地址及 secret；禁用局域网访问与 TUN，不导入额外 listeners、tunnels、脚本、外部面板及其他入站服务。
- DNS 解析选项保留，但 DNS `listen` 清空，不开放 DNS 服务端口。
- 仅支持 `http` / `inline` provider；HTTP provider 缓存路径重写到应用目录的 `providers/`，不接受本地文件 provider。
- 控制器使用随机本机端口、每次启动新的 256 位 secret、Bearer 鉴权，API 请求不使用环境代理、不跟随重定向。
- 订阅只接受 HTTPS，包括重定向。不会关闭 TLS 校验；URL 不持久化，下载错误不回显含 token 的地址。下载直接连接，不自动借用系统代理。
- 子进程不继承 `CLASH_*`、`MIHOMO_*`、`SAFE_PATHS` 或 `SKIP_SAFE_PATH_CHECK` 环境覆盖项。
- 不支持 Base64 节点订阅、`ss://` 等单节点链接转换；需要服务商提供完整的 mihomo YAML。

这不是内核沙箱：下载并校验的官方内核以当前用户权限运行，可信配置中的节点、provider、DNS 和规则资源仍可发起网络请求。不要运行来源不明的内核，不要导入不可信配置。配置文件和日志可能包含凭据；Unix 下应用目录权限为 `0700`，配置文件为 `0600`，**不做凭据加密**。Windows 使用当前用户目录及其 ACL，需自行保护账号。

## 数据位置与运行语义

使用 `os.UserConfigDir()/clash-go`，例如 Linux 的 `~/.config/clash-go`（遵循 `XDG_CONFIG_HOME`）：

| 文件 | 内容 |
| --- | --- |
| `settings.json` | 已保存的混合端口 |
| `kernel.json` | 当前托管内核版本、文件名及本地 SHA-256 |
| `kernels/` | 自动下载的内核及安装临时文件 |
| `config.yaml` | 已校验并规范化的当前配置 |
| `runtime.yaml` | 本次启动的控制器地址和 secret；正常停止后删除 |
| `providers/` 等 | mihomo 生成的缓存和数据 |

源 YAML 不修改。导入失败不替换已保存配置；内核校验自身可能写入缓存。运行期间禁止导入配置和保存设置。每次启动都重新生成并校验运行配置。

模式切换只对本次应用运行有效，手动停启或重新打开应用回到导入配置的模式；内核更新重启会恢复更新前模式。节点选择由 mihomo `store-selected` 缓存。统计是当前内核运行期间的累计值，不是速度，也不是持久历史。日志只保存在内存中。

正常关闭窗口、退出应用或收到 SIGINT/SIGTERM 时停止内核。**强制结束进程/SIGKILL、断电等非正常退出不保证清理子进程和 runtime 文件**；遇到端口占用时请先检查残留进程，不要直接删除或结束其他代理工具。

## 测试

```sh
make check
```

测试包含 YAML 安全覆盖、原子保存、设置迁移、环境隔离、API 鉴权、进程生命周期及原生 UI 导航。内核管理测试使用模拟 GitHub 响应和可执行的测试内核，覆盖首次安装、离线启动、平台资源选择、哈希/大小校验、重定向限制、ZIP 路径和 gzip 损坏、版本去重/防降级、在线升级状态恢复、下载中断/取消、候选内核崩溃、激活记录写入失败回滚、本地文件损坏修复。

真实官方下载集成测试（需要可用网络；使用临时目录和空闲端口）：

```sh
CLASHGO_TEST_DOWNLOAD=1 \
  go test -buildvcs=false ./internal/core -run '^TestOfficialKernelDownload$' -v -count=1
```

真实内核集成测试为显式启用，使用临时目录和空闲端口，仅向本机测试 HTTP 服务转发请求，不修改系统代理或现有 mihomo 实例：

```sh
MIHOMO_TEST_BINARY=/absolute/path/to/mihomo \
  go test -buildvcs=false ./internal/core -run TestRealMihomo -v -count=1
```

可生成无窗口 UI 测试截图：

```sh
CLASHGO_SCREENSHOT=/tmp/clash-go.png \
  go test -buildvcs=false . -run TestNativeNavigation -count=1
```

## 目前不包含

TUN/提权服务、系统代理自动切换、托盘常驻、开机自启动、多配置管理、定时订阅更新、测速、安装包签名。这是一个可运行的手动代理最小版本，不声称覆盖成熟 Clash 客户端的全部功能。

## 上游与许可证

应用代码使用 MIT。MyGo 使用 MIT；mihomo 是独立依赖，使用 GPL-3.0，分发内核或修改内核时必须单独遵守其许可证及对应源码提供义务。参见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
