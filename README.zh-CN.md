# NodeRampart

**在 SSH 终端里了解你的 VPS：谁在尝试登录、流量是否异常、每天用了多少流量，以及公网出站可能花多少钱。**

NodeRampart 在服务器后台观察网络和 SSH 登录，保存事件、生成日报，并可通过 Telegram 通知你。安装后用中文终端菜单完成配置、查看状态和报告，无需搭建 Web 面板。

它负责观察和提醒，不会自动封禁 IP 或修改防火墙，不保存或分析应用层通信内容，也不会新增 Web 监听端口。它不是 DDoS 防护或流量清洗服务。

[English](README.md) · [详细操作说明](docs/V0.4_OPERATIONS.md) · [安全政策](SECURITY.md) · [当前限制](docs/ALPHA_LIMITATIONS.md)

> **v0.4.0-alpha.7：已发布的 alpha 预发布版本。** [公开安装包](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.7) 新增可搜索的报告时区及独立中英 Telegram 正文。数据库升级为 schema 12；升级前验证匹配备份并分别保护旧配置/密钥，先安装三个匹配程序再保存新配置。

> **本次分发验收：** 22 项匿名下载及校验和通过，与已认证的 22 项 provenance、6 项 runtime SPDX 完全同字节。Debian 13/Fedora 44 公开 bootstrap、setup、基础采集、在线备份和清理：**PASS**。两台 strict 诊断快照仍为 **unknown / 退出码 2**。当前 alpha.7 的 Debian 12/Fedora 43 运行、真实 ARM64 和自然 SSH 恢复链仍未验收，不代表生产就绪。见[本次发布核验](docs/RELEASE_VERIFICATION.md#alpha7-publication-and-distribution-verification)。

以下 alpha.6 范围保留为历史记录，不替代 alpha.7 运行证明：

> **v0.4.0-alpha.6：已发布的 alpha 预发布版本。** [公开安装包](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.6) 增加通知目标隔离、完整本地报告、诊断及采集提交水位。数据库升级至 schema 11，采集协议升级至 v5；升级前请保留已验证的兼容备份。

> **验收范围：** 22 项资产的新环境匿名下载及校验和通过，字节与已认证核验的 22 项 provenance、6 项 SPDX 一致。Debian 12/13、Fedora 43/44 的准确最终包跨版本升级及生命周期通过；Debian 13、Fedora 44 的公开 bootstrap 与分开运行 setup 通过。真实 ARM64 和 SSH 自然恢复链仍未验证，不代表生产就绪。详见[发布验证](docs/RELEASE_VERIFICATION.md#alpha6-publication-and-distribution-verification)。

> **GeoIP 修复范围：** alpha.4 修复共享 MMDB 数据重复解析造成的预算误拒绝，并提供安全的 MMDB 校验/资源预算错误提示。匹配候选对 City/ASN 的完整离线校验通过；用户正式下载、激活和每日更新尚未验证。详见[验证范围与资源限制](docs/ALPHA_LIMITATIONS.md#geoip-alpha4-validation)及[升级与 GeoIP 验收](docs/V0.4_OPERATIONS.md#alpha4-upgrade-and-geoip-acceptance)。

> **固定版本安装：** 请在下列命令中明确选择 `v0.4.0-alpha.7`。冻结的 bootstrap 省略 `--version` 时仍默认 alpha.5。[本次验收摘要](docs/RELEASE_VERIFICATION.md#alpha7-publication-and-distribution-verification)区分实际结果与未验证范围。

## 能帮你做什么？

| 你关心的问题 | 可以查看的内容 |
| --- | --- |
| 有没有人在尝试登录？ | SSH 登录失败、无效用户、成功登录，以及连续失败告警。 |
| 服务器是不是遇到了异常流量？ | 端口扫描、SYN/UDP/ICMP 和带宽超过阈值的事件。 |
| 一次异常事件是怎么发展的？ | 开始、更新、恢复的时间线，相关 SSH 上下文与通知结果。 |
| 每天用了多少流量，主要来自哪里？ | 入站/出站总量、日报，以及可选的国家/ASN 归属信息。 |
| 某段时间的数据是不是完整的？ | 采集中断、覆盖缺口、丢失数据和存储健康状态。 |
| AWS/OCI 公网出站可能花多少钱？ | 用公开价格或自定义价格，对本机已观测流量进行估算，并列出计算假设。 |
| 快超预算或采集出问题了吗？ | 月度 80%/100%、完整日流量异常，以及采集、存储和 GeoIP 更新故障/恢复告警。 |
| 怎样解释缺失历史、分享排障材料？ | 查看裁剪原因、剩余记录，并导出本地脱敏 HTML/JSON 证据包。 |

还可以合并重复告警、设置到期自动解除的静默、补齐缺失日报、备份数据库，以及通过离线脱敏元数据比较不同检测阈值。

## 固定版本安装

安装器面向 **Debian 12/13、Fedora 43/44**，支持 **amd64/x86_64、arm64/aarch64**。需要运行 systemd，并在具有 root 或 sudo 权限的终端中操作。下载命令需要系统已有 curl 和有效的 HTTPS 证书；安装程序与 apt/dnf 会处理其余安装依赖，服务器无需安装 Go。ARM64 安装包已提供，但真实 ARM64 运行尚未验收。

先下载到独立目录，查看脚本后再决定是否执行：

~~~bash
INSTALL_DIR=$(mktemp -d)
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.7/bootstrap.sh \
  -o "$INSTALL_DIR/bootstrap.sh"
less "$INSTALL_DIR/bootstrap.sh"
# 查看并接受脚本行为后，再单独执行：
sudo sh "$INSTALL_DIR/bootstrap.sh" --version v0.4.0-alpha.7 --no-setup
sudo noderampart setup
~~~

安装包与证明的人工核验步骤见[发布验证](docs/RELEASE_VERIFICATION.md#download-and-verify)。HTTPS 下载、同一 Release 的 SHA256 和 GitHub attestation 是不同检查；bootstrap 会检查包校验和与包身份，**不会自动执行 attestation 验证**。

安装器会识别系统和 CPU，下载对应 DEB/RPM，核对 SHA256、版本和架构，通过包管理器安装。`--no-setup` 将菜单留给随后单独运行的 setup。已有配置和服务启用/禁用状态会保留；下载源或制品尚未发布时会明确报错并停止。

明确选择 alpha.7、下载后用 `--no-setup` 安装并单独运行 setup 的路径在 Debian 13、Fedora 44 的本次验收通过；受支持的 curl 管道交互安装路径未执行。省略 `--version` 仍选择 alpha.5。支持构建的平台不等于所有平台均已完成真实运行验收，具体边界见[当前限制](docs/ALPHA_LIMITATIONS.md)。

上述安装命令使用 **--no-setup** 完成无人值守安装，之后再单独打开配置向导。安装器不会从脚本管道读取交互答案或凭据。Debian 首次装包会按系统服务策略启用服务并以安全默认配置开始观察；Fedora 遵循系统的服务 preset。

## 第一次怎么配置？

随时运行下面的命令打开向导：

~~~bash
sudo noderampart setup --language zh
~~~

1. **基本设置**：选择网卡、SSH 监控、检测阈值、时区和日报时间。不提供外部账户，也能使用基础观察功能。
2. **Telegram（可选）**：输入自己的 Bot Token 和 Chat ID，Token 隐藏显示。保存配置不会自动发送测试消息，需要另选“发送测试通知”。
3. **本地 GeoIP（可选）**：输入自己的 MaxMind Account ID 和 License Key，确认已接受其条款，程序自动下载 City 和 ASN 两个数据库。每日更新可自行开启。
4. **出站费用估算（可选）**：选择 AWS 或 OCI、对应区域/分组，并填写分配给本机的整月免费额度。
5. 配置完成后选择**启动已配置的服务**，再查看**当前运行状态**。

菜单用方向键和 Enter 操作，表单用 Tab/Shift+Tab 切换字段，Escape 返回。也可在菜单中随时切换中文和 English。

alpha.7 提供可搜索的地区/城市时区列表及独立的 Telegram
**English / 简体中文**正文选择，缺省仍为英文，与界面语言独立。只有新入队消息使用
新呈现设置，旧正文/重试不重译。保存与旧消息规则见[时区与推送语言操作说明](docs/V0.4_OPERATIONS.md#timezone-selector-and-telegram-language)。

## 平时怎么使用？

~~~bash
sudo noderampart tui --language zh
~~~

关闭菜单不会停止后台服务。

| 菜单 | 可以做什么 |
| --- | --- |
| 当前运行状态 / 完整性与诊断 | 检查采集、网卡、数据缺口和存储情况。 |
| 功能配置 | 编辑全部可配置字段，包括高级选项；检查草稿后再确认保存。 |
| 报告 | 生成当前报告、查看已保存日报、补齐缺失日期。 |
| 事件与 Incident | 查看时间线和单次异常事件已保留的过程。 |
| Telegram 与通知 | 配置 Bot、查看投递结果、设置和撤销到期静默。 |
| 本地 GeoIP 数据库 | 下载或更新数据库、检查库龄、设置每日自动更新。 |
| 云公网出站费用估算 | 获取公开价格、比较已缓存的 AWS/OCI 估算、填写自定义价格。 |
| 备份、回放与隐私 | 备份数据库、验证备份、离线比较检测规则。 |
| 服务与卸载 | 启动、停止、重启、恢复之前的配置或卸载。 |

## 新告警和证据包怎么用？

历史日报详情会显示当时保存的费率和免费额度；事件详情可以查看触发告警的阈值、
观测值和覆盖情况。告警状态页还能区分检查中、超时和结果待保存。

在**功能配置**中开启“公网出站预算告警”或“采集与存储健康告警”，填写阈值后检查并保存。两组告警默认关闭。月流量可设为 `107374182400` 字节（100 GiB），分别在 80% 和 100% 提醒；费用预算还需先配置费用估算。

**完整性与诊断**可查看告警状态、数据保留与裁剪台账；**备份、回放与隐私**可导出脱敏证据，Incident 详情也有导出入口。文件只保存到本地，不会自动上传。

~~~bash
sudo noderampart alerts status
sudo noderampart retention
sudo noderampart evidence export --output /root/noderampart-evidence.zip
~~~

流量输入是所选网卡的 TX 估算，可能包含内网或重复路径；不是云厂商账单。机器完全离线或数据库无法写入时，不能保证本机告警送达。[配置、告警行为与证据隐私说明](docs/ALERTS_EVIDENCE.md)。

## 常用配置怎么选？

建议先用默认值观察实际流量，再调整阈值。流量达到阈值只是排查线索，不代表一定遭到了攻击。

| 配置项 | 默认值或示例 |
| --- | --- |
| 观察哪些网卡 | 默认根据 IPv4/IPv6 默认路由自动选择；也可显式指定，最多 8 张。 |
| SSH 连续失败 | 5 分钟内 8 次。 |
| 端口扫描 | 1 分钟内访问 20 个不同端口。 |
| 流量阈值 | SYN 每秒 5,000 包；UDP 10,000 包；ICMP 2,000 包；带宽 100 MiB/s。 |
| 日报时间 | 主机所在时区的 09:00；例如可将时区改为 Asia/Shanghai。 |
| 重复事件更新 | 默认在 10 分钟窗口内合并。 |
| 可选功能 | Telegram、GeoIP 下载和出站费用估算需要单独配置。 |
| IP 隐私 | 默认保存和通知中使用网段前缀：IPv4 /24、IPv6 /48。 |

菜单管理的配置文件固定为 **/etc/noderampart/config.json**，[完整默认配置](configs/noderampart.json)随项目提供。菜单会保留高级字段并检查整份配置。数据库和套接字路径须位于服务支持的目录内；修改数据库路径不会自动搬迁历史数据。

熟悉 JSON 的用户也可手工修改，然后运行 **sudo noderampart config test** 检查。手工重启前请阅读[配置与恢复说明](docs/V0.4_OPERATIONS.md#configuration-and-recovery)。

### 采样间隔与组件升级

配置周期仍为 100ms–1 分钟。协议 5 保留真实经过时间，容许有界调度抖动：
周期的 10%，至少 250ms、最多 5 秒。因此 1 分钟周期的 60001ms 观测合法；
更长暂停会丢弃明细、保留损失计数并重建基线，缺失观测不会重放进下一速率窗口。

daemon 允许最多 8 张接口的一轮有界帧突发，随后仍按原有持续读取速率限制。
一轮所有接口的写入共享 250ms 传输期限。升级时先更换 daemon，再更换 sensor，
或停止后同时更换：新 daemon 接受协议 1–5，旧 daemon 拒绝协议 5。
协议 1–4 和不带序号的离线采样没有持久化确认。

sensor 为每个接口分配有界会话和批次序号；status 展示实际提交水位，socket
写成功不会推进水位。采集健康计数、流量聚合与水位在同一个 SQLite 事务提交。
ACK 另行说明派生事件及通知入队决策是否已持久化；队列拒绝或事件待处理仍为
部分完成，不代表外部收件方已收到通知。重复序号不会重复累计流量或健康计数；
序号和观测缺口仍会显示。本版没有磁盘 spool 或历史批次重放，sensor 重启及
未提交的流量明细仍可能留下覆盖缺口。

### 可选 SSH 历史提示

认证菜单中的“SSH 历史提示”（`auth.history_hints_enabled`）默认关闭。
当前 daemon 进程连续覆盖七天后，使用最多 1,000 条保留的成功登录观测，
至少需要三个本地日期的 20 条记录。可提示首次观测来源（prefix 隐私模式下
仅为首次观测前缀）或未观测过的本地小时。这表示偏离已知历史，不表示入侵。
历史缺失、裁剪或过载时不作提示；修改配置或隐私 key 后重启会重新观察七天。
沿用现有隐私设置，不额外保存原始地址或建立另一份基线。

也提供默认关闭的 HTTPS 外部心跳和一个通用 JSON Webhook；在通知配置中设置固定目标与受保护凭据文件，不新增端口。心跳区分进程存活和功能降级，Webhook复用目标隔离发件队列。[配置、身份变化与重试限制](docs/V0.4_OPERATIONS.md#fixed-https-heartbeat-and-webhook)。

## Telegram 和 GeoIP 需要准备什么？

Telegram：使用 [BotFather](https://t.me/BotFather) 创建 Bot，先与它发起对话或将其加入目标群组，再将 Token 和目标 Chat ID 填进菜单。程序会把 Token 存入权限受限的本地文件，不需要将它写到命令参数里。[详细步骤](docs/V0.4_OPERATIONS.md#telegram)。

请使用数字 Chat ID。消息绑定 Bot/Chat 身份，数据库不保存 Token。更换 Bot/Chat 或收紧通知隐私后，旧的未发送消息会保留隔离；切回旧目标也不会自动接管它们。同目标换 Token 保留重试与限流状态，停用通知只暂停已确认的同目标队列。可在“通知消息列表”检查，并显式选择“丢弃隔离通知正文”。已在发送中的请求可能在原收件方完成；隔离的未发送消息仍遵循原有七天到期规则。

GeoIP：需要你自己的 [MaxMind GeoLite 账户](https://www.maxmind.com/en/geolite2/signup)，并已接受 [GeoLite 条款](https://www.maxmind.com/en/geolite/eula)。向导可以自动下载两个库，并按你的选择定期更新；本项目不会代注册、代接受条款或直接捆绑这些数据库。运行时通过本地库查询，地理位置仅供参考。也可以跳过，或使用已经合法取得的本地 MMDB。[详细说明](docs/V0.4_OPERATIONS.md#local-geoip)。

报告、Incident 和通知可以直接从列表进入详情；翻页自动保留查询时间段和游标。保存配置前会显示每项设置的旧值与新值。GeoIP 下载验证后若内容相同，会保留当前数据库和服务，避免无意义重启。

## 费用估算应该怎么看？

这里只估算**公网出站流量费**，不包括实例、CPU、内存、磁盘、NAT Gateway、跨可用区传输、税费等，也不代表最终云账单。

菜单会显示官方价格来源、获取时间、生效时间、计算单位、共享免费额度、本机流量和数据覆盖情况。价格在你选择获取时更新；下载失败会保留之前的缓存，并标明缓存时间。无需提供 AWS 或 OCI 账户凭据。

网卡 TX 不一定全是云厂商收费的公网出站。免费额度和阶梯也可能与其他主机、服务共享。请只分配本机应占的整月额度，默认按 **0** 计算；不要为每台机器重复填写整个账户的免费额度。每个价格 GB 对应多少字节会明确显示为可修改的计算假设。[计算边界](docs/V0.4_OPERATIONS.md#egress-estimates)。

## 完整本地报告、趋势和结算周期预测

以下命令读取本地 daemon。新日报单独保存完整结构化内容；Telegram 和 Webhook
只接收省略来源标识的简短指标摘要。历史快照不可变，旧快照保留原短正文，明确说明
原始完整内容不可用，不会重建后冒充原快照。

~~~bash
sudo noderampart report export --date 2026-09-29 --format html
sudo noderampart report show --date 2026-09-29 --format json
sudo noderampart report trend --days 7
sudo noderampart report trend --days 30
sudo noderampart report forecast
~~~

HTML 输出到 stdout，静态、自包含、正确转义，不加载外部脚本或资源。完整正文限
128 KiB，结构化文档限 256 KiB，归属排名最多 50 条，生成限 20 秒。TUI 展示不超过
32 KiB 的完整正文；更大文档明确显示短预览，可用 CLI 导出已保存的完整内容。

趋势区分完整、部分、缺失和裁剪，缺失用量为 null。完整日需完整的已记录网卡计数
覆盖及每个重叠 UTC 小时的记录，不代表账单准确或抓包无损。预测要求当期数据完整，
并有连续 7 或 30 个完整历史日；显示简单速率情景、周期末用量/费用范围及预计触线时间，
不称为统计置信区间。`billing.cycle_start_day` 支持 1–28，默认 1，使用报告时区，
用量、免费额度、预算阈值、价格预览和预测采用同一周期。切换起始日会记录原周期已变更，
不会沿用旧周期的告警水位。Guest TX 和既有费用模型仍只提供估算。

## 本地诊断与 textfile 监控

~~~bash
sudo noderampart doctor --strict --config /etc/noderampart/config.json
DIAG_DIR=$(sudo mktemp -d)
sudo noderampart metrics export --output "$DIAG_DIR/noderampart.prom"
~~~

严格 doctor 输出 JSON，包含稳定原因码、影响和下一步；退出码 0 表示必要检查正常，
1 表示已确认异常/降级，2 表示未知或诊断失败。禁用项不视为失败，未知不视为健康；
旧的非严格 doctor 退出码语义保留。`status` 也附带诊断，不会自动修复。

textfile 导出不新增端口，不安装服务或 timer；原子发布固定组件标签、生成时间、
五分钟有效期及采集成功标记。采集失败会用失败标记替换旧的健康输出；消费方必须
检查过期，即使旧成功标记仍为 1。新文件权限 0600，已有安全的 0640 权限保留；
已有采集器的读取权限由管理员明确安排。标签不包含 IP、事件 ID、秘密 URL 或任意错误文本。

## 怎么卸载？

在**服务与卸载**中选择：

- **卸载并保留配置与数据**：停止服务并移除程序，保留配置、凭据、历史和缓存。
- **卸载并删除所有托管数据**：同时删除固定应用目录和服务账户。需要的记录请先备份。

使用原生包安装时，也可直接运行随包安装的本地卸载工具：

~~~bash
sudo /usr/libexec/noderampart/manage-remove
# 明确需要删除托管配置、凭据和历史时：
sudo /usr/libexec/noderampart/manage-remove --purge
~~~

工具通过 apt/dnf 卸载，不移除系统依赖。RPM 卸载后，修改过的配置可能保存为 config.json.rpmsave；重装不会自动恢复此文件，需要自行检查并恢复所需设置。保留文件不等于自动启用旧设置。[升级与卸载细节](docs/V0.4_OPERATIONS.md#upgrades-and-removal)。

## 遇到问题和参与开发

~~~bash
sudo noderampart doctor
sudo noderampart status
sudo journalctl -u noderampartd -u noderampart-sensor --since today
~~~

SSH 会话没有终端时，可用 ssh -t 分配终端，或使用原有的非交互命令。下载失败、GeoIP 缺失、数据不完整等情况见[操作说明](docs/V0.4_OPERATIONS.md)。事件、备份和回放的详细命令仍可查阅 [v0.3 操作手册](docs/V0.3_OPERATIONS.md)。

`upgrade preflight` 只检查指定备份、配置、密钥、磁盘和本地包元数据，不升级；`upgrade rehearse` 验证临时恢复副本并清理。缺乏已验证目标信息时，目标 schema 兼容性仍为 unknown。`threshold preview` 离线比较当前/候选规则；TUI 将草稿试运行接到原有确认保存流程。有界本地 `threshold feedback` 标记不训练或自动调规则。[操作示例与限制](docs/V0.4_OPERATIONS.md#upgrade-preflight-and-restore-rehearsal)。

开发资料：[开发指南](docs/DEVELOPMENT.md)、[架构](docs/ARCHITECTURE.md)、[威胁模型](docs/THREAT_MODEL.md)、[贡献说明](CONTRIBUTING.md)。普通测试不需要抓包权限；特权测试仅在可丢弃的授权实验虚拟机中进行。

项目原创代码采用 [MIT License](LICENSE)。编译依赖保留各自许可证，见[第三方声明](THIRD_PARTY_NOTICES.md)；MaxMind 数据使用独立的数据许可。

新生成的日报会保存完整单价、来源、免费额度、字节单位和观测流量，便于复算。
历史补报使用生成时配置的价格，已有存档不会重新计价。alpha.6 自动迁移到 schema 11，保留公开 schema 7 的日志恢复状态，并新增目标隔离、完整报告文档、采集提交水位及分通道投递决策。
升级前请备份，旧版程序不能直接打开已迁移的数据库；只有通道名、没有可靠收件归属的历史 Telegram 消息会保留隔离，不绑定当前收件方。

已发布 alpha.6 产物附有 Go 依赖 SBOM 与匹配的 GitHub 来源证明，验证步骤见[发布验证](docs/RELEASE_VERIFICATION.md)。包内 README 与许可证保持冻结发布源码的快照；之后的文档更新不替换安装包字节，也不代表包中安装了完整离线手册。
