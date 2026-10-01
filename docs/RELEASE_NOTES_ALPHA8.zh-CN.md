# 拟用 Release Notes — NodeRampart v0.4.0-alpha.8

**尚未公开的拟用文本。** 此文件不是 GitHub draft/Release、发布日期、资产声明或
公开授权。正式 tag/source、hosted 构建和 attestation 身份只能在后续独立授权阶段取得。

alpha.8 新增六个默认停用的原生单向通知渠道：飞书自定义群机器人（可选请求签名）、
企业微信群机器人、Discord Incoming Webhook、Slack App Incoming Webhook、
Microsoft Teams Workflows 和 Google Chat Space Webhook。每渠道一个受保护凭据文件
目标，独立选择英文/简体中文事件、恢复、测试和日报，与原 Telegram/generic Webhook 并行。

统一通知 TUI 隐藏输入凭据并复用预览/应用/回滚事务。持久 outbox、不可变凭据快照、
目标/隐私隔离、有界容量和重启保留的重试/限流继续生效。官方 HTTPS URL、DNS/实际
连接地址校验、TLS、禁止重定向和有界正文适用于原生渠道；直连不使用环境代理。
保存或查看状态不会发送。六平台真实 API 和人工接收确认均 NOT RUN。

项目 `0.4.0-alpha.8`，DEB `0.4.0~alpha.8`，RPM `0.4.0-0.alpha.9.fc43/fc44`；
配置/控制 API1、sensor 协议5不变。数据库 schema13 事务升级 schema12。
升级前保留匹配旧数据库、配置和凭据；回退采用离线恢复，不原地降 schema。
purge 拒绝无归属文件，但直接包 purge 失败不保证 conffile 原子回滚。

源码 bootstrap 支持显式 `v0.4.0-alpha.8` 并严格校验包身份/checksum；资产公开后
方可公开下载。无参数默认仍 alpha.5，公开示例仍指已验证 alpha.7。404 不回退，
不自动降级。独立程序/DEB 为 loader-free static；RPM 保留 PIE/system-loader。
ARM64 仅交叉构建，原生运行 NOT RUN。本地结果不等于正式 hosted provenance 或公开 bootstrap。

Teams 仅支持管理员允许的 Anyone 秘密 URL/Adaptive Card 工作流，只确认请求接受；
OAuth/Entra-only 模式不支持。Slack 平台服务/分发条款、管理员和消息权限独立于 MIT，
不声称 Marketplace/厂商批准。用户自行创建授权 Webhook，不代其开户、付款或同意协议。

固定依赖仍含 x/text v0.21.0，其 unicode/norm 有 GO-2026-5970，修复版本为 v0.39.0。
最终源码和实际产品包的导入/可达性结果应读取外部冻结预验收记录；旧结论或 scanner
exit0 不代表零漏洞，也不代表代维护者接受风险。

参见[预验收阶段与边界](ALPHA8_RELEASE_PREFLIGHT.md)、[功能验收](ALPHA8_ACCEPTANCE.md)、
[中文渠道说明](NOTIFICATION_CHANNELS.zh-CN.md)、[隐私](PRIVACY.md)与[用户协议](USER_AGREEMENT.md)。
本版仍为 alpha，不承诺生产就绪、端到端必达或 exactly-once。
