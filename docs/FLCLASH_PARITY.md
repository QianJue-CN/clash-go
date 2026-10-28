# FlClash → Clash Go 功能迁移对照

## 基准与范围

- 对照源码：`/home/qianjue/FlClash`，检查时 HEAD 为 `7fd417fb`。功能入口主要来自 `lib/views/navigation.dart`、`lib/views/tools.dart`、`lib/core/interface.dart`、`lib/views/config/` 和 `lib/features/`。这是一份模块级对照，不是逐行迁移完成证明。
- 目标：现有 Go + MyGo 原生桌面界面，独立官方 mihomo 子进程，鉴权本机 REST API。保留现有配置安全边界和未提交的项目实现，不迁移 Flutter widget/provider 层，也不把 FlClash 的定制 IPC 方法当作官方 REST API。
- 标记：**已覆盖**表示对应基础功能已接入 UI 和后端；**部分**表示明确列出的子集；**未实现**不提供占位按钮或成功提示；**平台专属**不属于目前的桌面运行目标。

## 功能对照

| FlClash 模块/源码入口（相对于上游根目录） | 状态 | Clash Go 实现与差异 |
| --- | --- | --- |
| `lib/core/desktop/`：内核生命周期 | 已覆盖 | 启停、自动启动已有配置、退出收敛；`internal/core/manager.go`。仍使用串行操作，不是上游的异步 intent 调度器 |
| 内核安装/更新 | 已覆盖 | 官方稳定版自动下载、SHA-256 校验、版本去重、在线升级与回滚；原有实现保留 |
| `views/dashboard/`：状态、时长、流量、模式 | 已覆盖 | 内核版本、运行时间、累计流量、连接数、规则/全局/直连；新增采样速率 |
| `views/dashboard/layout.dart`、`edit.dart`：自定义仪表盘 | 未实现 | 固定原生卡片布局，无拖拽排列、图表历史和小组件定制 |
| `views/proxies/`：分组和节点选择 | 部分 | Selector 选择、组搜索、内核自动组选路；无网格/标签布局、节点收藏和延迟排序 |
| `core/interface.dart` 的 `asyncTestDelay` | 部分 | 自选节点或组、HTTPS 测试地址、5 秒超时、结果 ms；无全量并发测速、历史延迟或自动择优 |
| `views/proxies/providers.dart`、`views/resources.dart` | 部分 | 代理/规则 provider 列表、条目数、更新时间、搜索、HTTP 更新、代理健康检查；不支持资源侧载、GeoData 更新或规则集编译 |
| `views/profiles/add.dart`：HTTPS/文件导入 | 已覆盖 | 独立快照、自动命名、本地文件名、运行中追加、首项自动启用、安全校验 |
| `views/profiles/profiles.dart`：选择和持久化 | 已覆盖 | 多配置选择、原子目录记录、启动失败回滚；不导入 FlClash 的数据库 |
| `views/profiles/edit.dart`：重命名、删除 | 已覆盖 | 名称验证、防重名、确认删除；运行中禁止删除当前项 |
| `views/profiles/edit.dart`：手动更新 | 部分 | 重新输入 HTTPS 地址后原位更新内容，保留 ID 和名称；不持久化 URL |
| 配置自动刷新、流量/到期信息、订阅 UA | 未实现 | 仍需订阅元数据模型、显式凭据保存策略和调度；不暗中保存 URL |
| `pages/editor.dart`、`features/editor/` | 部分 | MyGo TextArea 编辑规范化 YAML、保存前内核校验、失败保留输入；无高亮、补全、diff 或脚本编辑器 |
| 配置预览、导出 | 部分 | YAML 编辑框可查看内容，单配置导出；无原始/合成配置双视图 |
| 单节点链接、二维码、Base64、加密订阅 | 未实现 | 不引入上游 `convertProxies`、`decodeShareLinks`、`decryptConfig` 等定制内核能力 |
| `views/config/rules.dart` | 部分 | 生效规则搜索及顺序展示；通过 YAML 编辑增删规则，无独立规则表单和预设 |
| `views/config/custom_resources.dart`、`providers.dart`、`filters.dart` | 部分 | YAML 可编辑受支持的代理、代理组和 http/inline provider；无跨配置资源合并和过滤器管理 |
| `views/config/dns.dart`、通用 Hosts | 部分 | YAML 编辑 DNS/Hosts，保留规范化安全限制，DNS `listen` 始终清空；无专用表单 |
| `views/config/general.dart` | 部分 | 混合端口 UI；mode/log-level/ipv6/unified-delay/tcp-concurrent/geodata-mode 可以经 YAML 编辑；其他选项不自动等价 |
| `views/config/network.dart`、`tun.dart` | 未实现 | 仍仅本机混合代理，禁用 TUN/LAN/额外入站，不安装权限服务或修改系统路由 |
| `views/config/sniffer.dart`、`ntp.dart` | 未实现 | 不扩展现有顶层白名单，不能通过编辑 YAML 绕过安全策略 |
| `views/config/scripts.dart`、`override.dart` | 未实现 | 无 JS 脚本运行时、可视化覆写或跨配置补丁层 |
| `views/config/on_demand.dart` | 未实现 | 无按网络条件启停和连接条件调度 |
| `views/connection/connections.dart`、`features/connection/` | 部分 | 活动连接详情、流量排序、域名/IP/进程/规则/代理链搜索、单条关闭、确认关闭全部；每页 50 项，无连接历史/单连接速度排名 |
| `views/connection/requests.dart` | 未实现 | 无请求历史、历史重置、已结束请求数据库；活动连接列表不能代替该功能 |
| `views/dns_queries.dart` | 部分 | 手动查询和缓存清理，**没有 DNS 事件历史**；只使用官方 REST DNS 查询能力 |
| `views/logs.dart` | 部分 | 64 KiB stdout/stderr 缓冲、过滤、暂停显示、清空、导出；无结构化日志流和持久日志数据库 |
| `features/ip_quality/`，`probe/outboundIp/serviceCheck/checkRoutes` | 未实现 | IP 质量、出口 IP、流媒体解锁、路由一致性和连通性组合探测依赖额外服务/协议；不以单次延迟测试冒充 |
| `views/backup_and_restore.dart`：本地文件 | 部分 | 自有 JSON 配置备份，全部校验后原子追加恢复，空列表恢复选择；无全应用设置、缓存、原始文件、FlClash ZIP 格式兼容 |
| 同上：WebDAV | 未实现 | 无远程凭据管理、同步冲突和 WebDAV 协议实现 |
| `views/theme.dart`、`font_family.dart` | 部分 | 中文 UI、跟随系统明暗、原生可访问控件；无主题编辑、动态取色、字体设置和移动端自适应 |
| `views/tools.dart`：多语言 | 未实现 | 固定中文，没有导入上游本地化资源 |
| 桌面系统代理 | 未实现 | 不修改系统设置；仍由用户/其他应用手动设置本地端口 |
| 托盘、开机启动、静默启动、全局热键 | 未实现 | MyGo/系统平台集成需单独实现和实机验证；关闭窗口仍退出并停止内核 |
| `views/access.dart`、Android 插件 | 平台专属 | Android VPN、分应用代理、通知、快捷磁贴、扫描、TV 布局不迁入桌面应用 |
| `views/developer.dart` | 未实现 | 不暴露崩溃、强制 GC、内核测试指令；保留测试代码而非生产按钮 |
| `views/about.dart`、发布更新 | 部分 | 关于、依赖/许可证链接、内核更新；无应用自更新、安装包签名或发布渠道切换 |

## 本次新增实现位置

- `internal/core/inspection.go`：连接模型、按页加载规则/资源、关闭连接、延迟、资源更新/检查、DNS、日志清理、速率计算。
- `internal/core/profile_actions.go`：配置读取/重命名/删除、共用规范化校验、新修订发布和导出。
- `internal/core/profiles.go`：修订 ID 校验、配置目录兼容、复用启用/回滚过程。
- `internal/core/backup.go`：32 MiB 有界备份、恢复逐项校验、单次目录提交。
- `desktop_inspection.go`：连接/规则/资源/工具页面、延迟、日志操作、分页和确认框。
- `desktop_profiles.go`：编辑/更新弹窗、确认删除、配置导出和备份入口。
- `desktop.go`、`main.go`：导航、速率卡片、后台刷新及弹窗期间暂停自动刷新。

## 数据与安全语义

1. 导入、编辑、手动更新、恢复备份都调用同一规范化/校验入口。仍不支持本地文件 provider、任意 listener、TUN 或订阅定义的控制器。
2. 更新先写新的私有 YAML 修订并校验。当前项运行中则尝试启用新修订，成功才发布 `profiles.json`；失败恢复旧记录并尝试恢复进程。停止状态或非当前项更新不启动/重启内核。
3. 新修订发布成功后清理旧文件。进程异常退出或文件清理失败可能留下未引用文件；这些文件仍可能包含凭据，删除配置不是安全擦除整个历史目录。
4. 订阅地址只保留于输入/下载期间，不写入目录、备份或日志。手动更新不能替代定时更新。
5. 备份为明文，包含配置中的节点、provider 和 DNS 凭据。恢复是追加，不覆盖用户现有内容；不加载备份里的文件路径或运行程序。
6. API 继续随机本机地址、Bearer secret、禁止环境代理/重定向和 8 MiB 响应上限。节点/provider 名称作为 URL 路径段转义；延迟/DNS 参数通过 URL 编码。
7. 延迟和健康检查会真实发起网络请求；连接关闭和当前配置编辑会中断现有网络连接。UI 明示这些副作用，未调整系统代理。

## 验证与未验证范围

执行命令参见根目录 README：`make check`、`go build` 及显式启用的 `TestRealMihomo`。

新增自动测试覆盖：

- API 鉴权、路径/查询编码、数据解码、失效响应、不完整资源结果不发布、离线操作拒绝、URL 错误脱敏。
- 速率首样本/累计值回退、日志清空、过滤、暂停、列表分页和搜索。
- 配置重命名/重复名、运行中删除保护、无效 ID/修订 ID、有效编辑持久化、校验/启动/目录写入失败回滚、取消。
- 本地备份恢复的追加语义、当前进程保留、失败不发布部分条目、容量限制及导出。
- MyGo 无窗口 UI 的新增导航、禁用状态、确认取消、弹窗焦点、Escape、保存中禁止关闭和关闭后清除输入。
- 真实 mihomo 的规则/provider 列表、DNS 缓存清理、连接操作、运行中编辑、备份恢复以及原有本机代理转发。

未验证真实订阅服务商、外网延迟质量、不同 provider 的健康检查行为、原生文件对话框交互、Windows/macOS 实机、Android、断电恢复和超大列表的帧时间。DNS 查询/延迟/provider 更新协议的成功与失败分支由模拟服务器测试，不等价于外网联调。

## 后续迁移顺序

1. 订阅元数据、URL 的显式保存选择、定时刷新和失败重试；先确定凭据/备份策略。
2. 桌面平台模块：系统代理的原值保存与恢复、托盘、开机启动、热键；每个平台独立验证退出和崩溃恢复。
3. TUN 与提权服务：独立最小权限边界、路由/DNS 恢复，不直接让整个 UI 以 root 运行。
4. 请求/DNS 历史及高级探测：评估官方 API 与定制内核的差距、存储上限和隐私策略。
5. 专用规则/DNS/代理编辑器、覆写层、WebDAV、多语言和主题；不以增加空壳页面计算覆盖率。

参考 FlClash GPL-3.0 源码定位功能与协议，本次代码以本项目现有 Go/MyGo 结构独立实现，没有复制 Dart widget、图片资源、脚本运行时或嵌入上游定制内核。将来若直接移植 GPL 源码或资源，需重新评估许可证与分发义务。
