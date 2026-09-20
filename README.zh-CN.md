# NodeRampart

**在 SSH 终端里了解你的 VPS：谁在尝试登录、流量是否异常、每天用了多少流量，以及公网出站可能花多少钱。**

NodeRampart 在服务器后台观察网络和 SSH 登录，保存事件、生成日报，并可通过 Telegram 通知你。安装后用中文终端菜单完成配置、查看状态和报告，无需搭建 Web 面板。

它负责观察和提醒，不会自动封禁 IP 或修改防火墙，不保存或分析应用层通信内容，也不会新增 Web 监听端口。它不是 DDoS 防护或流量清洗服务。

[English](README.md) · [详细操作说明](docs/V0.4_OPERATIONS.md) · [安全政策](SECURITY.md) · [当前限制](docs/ALPHA_LIMITATIONS.md)

> **v0.4.0-alpha.2：首次预发布安装包现已提供。** 可从[公开 Release](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.2) 或以下固定版本命令下载。匿名下载及校验和已经验证；本版本尚未执行安装生命周期及 ARM64 实机验收。用于生产前，请先在隔离环境中验证。Alpha 版本应与现有安全措施配合使用。

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

## 一行安装

安装器面向 **Debian 12/13、Fedora 43/44**，支持 **amd64/x86_64、arm64/aarch64**。需要运行 systemd，并在具有 root 或 sudo 权限的终端中操作。下载这一行命令需要系统已有 curl 和有效的 HTTPS 证书；安装程序与 apt/dnf 会处理其余安装依赖，服务器无需安装 Go。

~~~bash
curl --proto '=https' --tlsv1.2 -fsSL https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.2/bootstrap.sh | sudo sh -s -- --version v0.4.0-alpha.2
~~~

也可以先下载到独立目录，查看脚本后再决定是否执行：

~~~bash
INSTALL_DIR=$(mktemp -d)
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.2/bootstrap.sh \
  -o "$INSTALL_DIR/bootstrap.sh"
less "$INSTALL_DIR/bootstrap.sh"
# 查看并接受脚本行为后，再单独执行：
sudo sh "$INSTALL_DIR/bootstrap.sh" --version v0.4.0-alpha.2
~~~

安装包与证明的人工核验步骤见[发布验证](docs/RELEASE_VERIFICATION.md#download-and-verify)。HTTPS 下载、同一 Release 的 SHA256 和 GitHub attestation 是不同检查；bootstrap 会检查包校验和与包身份，**不会自动执行 attestation 验证**。

安装器会识别系统和 CPU，下载对应 DEB/RPM，核对 SHA256、版本和架构，通过包管理器安装，随后进入配置向导。已有配置和服务启用/禁用状态会保留；下载或校验失败时会明确报错并停止。

如需手动安装已下载并核验的包，请按[本地安装包说明](docs/V0.4_OPERATIONS.md#local-package-installation)操作。支持构建的平台不等于所有平台均已完成真实运行验收，具体边界见[当前限制](docs/ALPHA_LIMITATIONS.md)。

无人值守安装可在命令末尾加 **--no-setup**，之后再打开配置向导。安装器不会从脚本管道读取交互答案或凭据。Debian 首次装包会按系统服务策略启用服务并以安全默认配置开始观察；Fedora 遵循系统的服务 preset。

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

## Telegram 和 GeoIP 需要准备什么？

Telegram：使用 [BotFather](https://t.me/BotFather) 创建 Bot，先与它发起对话或将其加入目标群组，再将 Token 和目标 Chat ID 填进菜单。程序会把 Token 存入权限受限的本地文件，不需要将它写到命令参数里。[详细步骤](docs/V0.4_OPERATIONS.md#telegram)。

GeoIP：需要你自己的 [MaxMind GeoLite 账户](https://www.maxmind.com/en/geolite2/signup)，并已接受 [GeoLite 条款](https://www.maxmind.com/en/geolite/eula)。向导可以自动下载两个库，并按你的选择定期更新；本项目不会代注册、代接受条款或直接捆绑这些数据库。运行时通过本地库查询，地理位置仅供参考。也可以跳过，或使用已经合法取得的本地 MMDB。[详细说明](docs/V0.4_OPERATIONS.md#local-geoip)。

报告、Incident 和通知可以直接从列表进入详情；翻页自动保留查询时间段和游标。保存配置前会显示每项设置的旧值与新值。GeoIP 下载验证后若内容相同，会保留当前数据库和服务，避免无意义重启。

## 费用估算应该怎么看？

这里只估算**公网出站流量费**，不包括实例、CPU、内存、磁盘、NAT Gateway、跨可用区传输、税费等，也不代表最终云账单。

菜单会显示官方价格来源、获取时间、生效时间、计算单位、共享免费额度、本机流量和数据覆盖情况。价格在你选择获取时更新；下载失败会保留之前的缓存，并标明缓存时间。无需提供 AWS 或 OCI 账户凭据。

网卡 TX 不一定全是云厂商收费的公网出站。免费额度和阶梯也可能与其他主机、服务共享。请只分配本机应占的整月额度，默认按 **0** 计算；不要为每台机器重复填写整个账户的免费额度。每个价格 GB 对应多少字节会明确显示为可修改的计算假设。[计算边界](docs/V0.4_OPERATIONS.md#egress-estimates)。

## 怎样升级和卸载？

NodeRampart 不会自动更新可执行程序。主动升级安装包前，请创建并验证数据库备份，单独妥善保存必要配置和凭据，并保留旧安装包。使用包管理器或目标 Release 的 bootstrap 升级。数据库会自动迁移到 schema 6，旧程序不能打开迁移后的数据库；配置恢复不会降级数据。源码安装必须先按[源码转安装包说明](docs/V0.4_OPERATIONS.md#upgrades-and-removal)迁移，不能直接覆盖源码安装拥有的文件。

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

## 遇到问题怎么排查？

**中文显示为问号：** `--language zh` 选择界面语言，不决定终端编码。
已发布 alpha.2 的安装器可能把 `LC_ALL=C` 传给 setup。使用 UTF-8 SSH 客户端时，
先检查 `LC_ALL=C.UTF-8 locale charmap`，再运行
`sudo env LC_ALL=C.UTF-8 noderampart setup --language zh`；主菜单可把 `setup` 换成 `tui`。
若该 locale 不可用，从 `locale -a` 中选择可用的 UTF-8 名称，无需中文语言包。
sudo 可能重置 locale，应对本次命令指定，不使用 `sudo -E` 或修改系统默认值。
[终端编码排障](docs/V0.4_OPERATIONS.md#terminal-encoding--终端编码)说明了客户端/字体检查，
以及源码修复与尚未改变的 alpha.2 安装包的区别。

~~~bash
sudo noderampart doctor
sudo noderampart status
sudo journalctl -u noderampartd -u noderampart-sensor --since today
~~~

SSH 会话没有终端时，可用 ssh -t 分配终端，或使用原有的非交互命令。下载失败、GeoIP 缺失、数据不完整等情况见[操作说明](docs/V0.4_OPERATIONS.md)。事件、备份和回放的详细命令仍可查阅 [v0.3 操作手册](docs/V0.3_OPERATIONS.md)。

新生成的日报会保存完整单价、来源、免费额度、字节单位和观测流量，便于复算。
历史补报使用生成时配置的价格，已有存档不会重新计价。数据库自动迁移到 schema 6，新增持久化告警状态与裁剪台账；
升级前请备份，旧版程序不能直接打开已迁移的数据库。

发布产物将附带 Go 依赖 SBOM 与 GitHub 来源证明，验证步骤见[发布验证](docs/RELEASE_VERIFICATION.md)。

## 安全与 Alpha 限制

传感器使用 AF_PACKET 和 `CAP_NET_RAW`，守护进程使用独立服务身份。配置管理、服务管理和装卸包需要 root。尚未实现 eBPF 采集器或程序自动更新；防火墙和 SSH 访问控制仍需独立配置。

构建目标是 Debian 12/13、Fedora 43/44 的 amd64/arm64。交叉编译或检查包内容不等于真实运行测试。过去私有 Debian 13/Fedora 44 amd64 实验结果仅适用于当时快照，不代表当前 tag 已通过新的安装、升级、卸载验收，也不代表真实 ARM64 运行通过。[剩余验收与功能限制](docs/ALPHA_LIMITATIONS.md)和[安全问题报告](SECURITY.md)说明了边界。扫描成功或证明有效均不代表不存在漏洞。

## 开发与许可

开发资料：[开发指南](docs/DEVELOPMENT.md)、[架构](docs/ARCHITECTURE.md)、[威胁模型](docs/THREAT_MODEL.md)、[贡献说明](CONTRIBUTING.md)。普通测试不需要抓包权限；特权测试仅在可丢弃的授权实验虚拟机中进行。

项目原创代码采用 [MIT License](LICENSE)。编译依赖保留各自许可证，见[第三方声明](THIRD_PARTY_NOTICES.md)；MaxMind 数据使用独立的数据许可。
