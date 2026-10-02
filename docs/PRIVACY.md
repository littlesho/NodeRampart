# NodeRampart privacy policy / 隐私政策

Effective / 生效：2026-10-01. This describes this repository's software, not a hosted service. [User agreement](USER_AGREEMENT.md) · [Support](https://github.com/littlesho/NodeRampart/issues) · [Private security reporting](../SECURITY.md).

NodeRampart runs on the operator's Linux host. It observes packet headers/aggregate traffic and trusted SSH journal records, stores bounded events, transformed source addresses, authentication/coverage/budget observations and reports locally, and keeps credentials in protected local files. Default IP privacy uses network prefixes; users may deliberately select other supported privacy modes. Database backups and local full reports contain sensitive monitoring data. The project provides no telemetry receiver, cloud relay or remote management service.

Notification channels default disabled. Enabling a channel and supplying your authorized target directs safe summaries of newly eligible events, recoveries, daily reports and explicit synthetic tests to that provider and its configured recipients. These may include host label, transformed network identifiers, time, severity, observed metrics and coverage limitations. Daily native summaries omit source identities and full local prose; raw logs, diagnostic bundles and full reports are not attached. Notifications are encrypted in transit with validated HTTPS. Local databases are protected by filesystem permissions, not application-level disk encryption; operators should use appropriate encrypted storage/backups for their requirements. Protected webhook URLs/signing secrets remain local file/process secrets and are used only against approved provider origins. They must not be posted in support issues.

No Slack/Google/Teams chat history, private-message discovery, account contacts or inbound content is requested. Provider response bodies are bounded, used for acknowledgment/error classification, then discarded; upstream text and URLs are not logged or persisted. Providers and recipients apply their own privacy, retention, residency and administrator policies. NodeRampart cannot delete already accepted remote messages, determine reading, or enforce a provider's retention. An in-flight request may complete after local disable. Native channels are direct only; legacy administrator proxy use adds that proxy to the credential/data trust boundary.

Operators control configuration, language/privacy and enabled channels through reviewed local management, can inspect bounded queues, erase selected isolated unsent bodies and revoke the provider target. Default event/queue/report retention and storage bounds are described in [storage](STORAGE-BUDGET.md) and [channels](NOTIFICATION_CHANNELS.md); removing the program preserves local data unless an explicit supported purge is chosen. Backups and externally delivered copies require separate handling. Changing privacy settings cannot retract remote data. Redacted local evidence exports use fresh pseudonyms; verify them before sharing. Public GitHub issue content is public under GitHub's policies. Use SECURITY.md for confidential vulnerability reports, and never include real URLs/tokens/raw reports.

You must have authority from your organization and relevant data subjects for collection, storage, transfer and recipients. This software does not supply that authority or accept provider agreements. Commercial distributors/hosted operators must supply their own accurate privacy terms and required authorizations. Updates to software behavior require reviewing this policy alongside the changelog.

NodeRampart 在操作者自己的 Linux 主机运行，观察报文头/流量汇总与可信 SSH 日志，将有界事件、隐私转换后的来源、认证/覆盖/预算观测和报告存于本地，凭据放在受保护文件。默认 IP 使用网段隐私，可明确选择其他模式；完整本地报告和数据库备份含敏感监控数据。本项目没有遥测接收、云中转或远程管理服务。

通知默认关闭。启用自己授权的目标后，提供商与目标接收方会收到新准入事件、恢复、日报和明确合成测试的安全摘要，可能含主机标签、转换后的网络标识、时间、严重程度、指标和覆盖限制。原生日报省略来源及完整原文，不附原始日志、诊断包或完整报告。HTTPS 验证 TLS；本地数据库依靠文件权限，未做应用级磁盘加密，需加密存储/备份时由管理员落实。完整 URL/签名是本地文件和进程秘密，仅发送至批准的提供商源，不得贴入支持问题。

不读取 Slack/Google/Teams 聊天历史，不发现私信、联系人或入站聊天。厂商响应只在边界内用于回执分类后丢弃，不存储/记录厂商原文或秘密 URL。提供商与接收方有自己的隐私、保留期、数据地域及管理员政策；本项目不能删除远端消息、判断已读或控制厂商保留期。在途请求可能在停用后完成。原生渠道仅直连，旧渠道代理把代理加入凭据/数据的信任范围。

用户可在已审阅本地管理流程控制开关、语言、隐私，检查队列、擦除选定隔离未发正文，并在厂商撤销目标。[存储政策](STORAGE-BUDGET.md)和[通知说明](NOTIFICATION_CHANNELS.zh-CN.md)说明默认保留期/限额。普通卸载保留数据，只有明确支持的 purge 删除固定产品数据；备份/远端副本另行处理。隐私修改不能撤回已发送数据。分享伪名化证据前应自查；GitHub 问题公开，机密漏洞依 SECURITY.md 报告，禁止附真实秘密/报告。组织、数据主体和收件权限由操作者取得；源码不提供这些授权、不代接受条款。商业分发/托管方应制定自己的准确政策并取得必要许可。

## Alpha.9 development addition / 开发候选增补

The additional official QQ/LINE/Twilio/Meta channels keep application/account IDs,
sender/recipient identifiers (including phone numbers), tokens and secrets in
protected local credential snapshots. Active consent records keep a local time,
purpose, selected notification types and bounded evidence identifier; no consent
file is collected. The database retains credential-free frozen request content,
intent/unknown/acceptance state, protected provider receipt IDs needed for specific
status queries, local quota reservations and nullable platform segment/price
observations. Ordinary UI/errors/exports mask recipient identifiers or project
fresh pseudonyms. Phone numbers are not anonymized by publishing a bare hash.

When explicitly enabled, providers process the recipient, bounded summary or
approved template parameters and their own account/billing metadata. Twilio
specific-message GETs inspect that accepted request only. No contacts, inbound
chat, SMS/WhatsApp replies, STOP or read callback is collected. Operators must
maintain actual consent/withdrawal and support routes; local revoke suppresses
unsent messages but cannot recall accepted copies. Third-party retention,
regional transfer and charges depend on the user's account/platform contract.
No real paid/platform tests are performed by ordinary CI, setup or status. See
[the account-channel guide](OFFICIAL_NOTIFICATION_CHANNELS.md).

新增 QQ／LINE／Twilio／Meta 渠道的应用／账户、发送身份、收件 ID／手机号、token
与 secret 放在受保护本地快照。订阅只记录本地时间、用途、通知类型和有界依据
编号，不采集同意文件。数据库保存无凭据的冻结请求、意图／未知／受理状态、
特定状态查询必需的受保护 provider 回执 ID、额度预留及可空平台分段／费用。
普通界面／错误／导出遮罩或生成新伪名，不公开手机号的裸哈希。明确启用后平台
处理收件地址、摘要／批准模板参数及自己的计费数据。本程序不采集联系人、入站
聊天、短信／WhatsApp 回复、STOP 或已读回调；操作者维护真实同意／撤回与支持，
本地撤销不撤回远端副本。跨境、保留和收费依实际账户协议，普通 CI／设置／状态
不发真实或付费消息。
