# 原生 Webhook 通知渠道

<!-- current-release:start -->
[English](NOTIFICATION_CHANNELS.md)。本指南面向当前公开版
[v0.4.0-alpha.9](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.9)。
<!-- current-release:end -->

这六个原生渠道首次随 alpha.8 公开，现与另外六个渠道共用通知菜单。六平台真实 API
与人工接收均为 **NOT RUN**。参见[当前公开分发范围](RELEASE_VERIFICATION.md#alpha9-publication-and-public-distribution-verification)、
历史[功能验收矩阵](ALPHA8_ACCEPTANCE.md)、[隐私政策](PRIVACY.md)与[用户协议](USER_AGREEMENT.md)。

## 通用配置、测试与安全

Feishu、WeCom、Discord、Slack、Microsoft Teams Workflows 和 Google Chat 各支持一个独立目标，全部默认关闭，可与 Telegram 和既有 generic JSON Webhook 同时运行。它们覆盖既有可通知的 SSH、网络、预算、健康事件及开始、更新、恢复和同一日报日程；既有严重程度和静默规则不变。仅出站 HTTPS，无入站机器人、远程命令、私信发现、文件上传、OAuth 商店安装、新端口、网页或额外产品运行依赖。

运行 `sudo noderampart tui`，进入**通知渠道**，选择相应设置，通过隐藏输入填写完整厂商 URL，独立选择 `en` / `zh`，预览并应用。空白且未修改的输入保留秘密；使用明确的 URL/签名操作替换或清空。保存、打开设置和查看状态都不发送测试。启用前取得组织允许。另选**发送测试通知**及一个明确渠道，并同时检查本地队列和接收端。非交互测试同样只入队一个合成通知：

```sh
sudo noderampart notify test --channel feishu
sudo noderampart notify status
sudo noderampart notify list --limit 20
```

另外五个渠道参数是 `wecom`、`discord`、`slack`、`teams`、`google_chat`。queued 仅为本地准入；接口确认也不证明已读或端到端送达。Teams 的 accepted 仅表示工作流接受请求。部分成功时在时间线查看各渠道独立结果，不将一个成功概括为全部成功。

普通配置只保存受保护文件引用；完整 URL、查询参数和 secret 都是秘密。最小配置见英文页中的六渠道 JSON 示例；例如：

```json
{"notifications":{"feishu":{"enabled":false,"credential_file":"/etc/noderampart/feishu.credential.json","language":"zh","timeout":"10s"}}}
```

应合并到现有配置，不覆盖完整配置。管理器在配置目录下的受保护 secrets 目录创建有归属的版本文件。凭据采用严格 JSON，字段为 `url` 和可选飞书 `secret`，不要写入普通配置、命令参数、Git、截图或支持证据。运行身份为现有 noderampart 服务；0600 文件应由可读取的可信服务用户拥有，0640 仅允许核验过的服务组。root-only 且服务不可读会令该渠道不可用。文件必须是单链接普通文件，所有父路径须可信且无符号链接、共享可写目录或全员读取权限。允许目录为实际配置同目录或其 secrets 子目录；安装默认在 `/etc/noderampart`。数据库备份不含凭据，凭据应单独保护和备份。

新原生 HTTPS 直连且不使用系统代理，每次请求重新核验 DNS 和实际连接地址，拒绝回环、私网、链路本地、元数据及等价 IPv6，TLS 验证开启，只用 443，不跟随重定向。需要合法 DNS/路由；不支持 TLS 跳过校验。旧 generic Webhook/heartbeat 的管理员/系统代理契约保持原样，代理必须被信任可以接触 Bearer；本地地址检查不能约束代理端 DNS。

正文在入队时固定，最多 1800 UTF-8 字节；最终 JSON 16 KiB、响应头 16 KiB、正文 64 KiB，请求超时 1–30 秒。摘要优先保留严重程度、时间、核心指标、覆盖不足，省略可选详情时可见缩略提示和有效本地事件/报告查看命令。不发送完整报告、日志、诊断包，不自动分片；重试不重新翻译或改正文。通知按报告时区呈现，切换语言/时区不更改旧正文或原报告边界。

## Feishu / 飞书

前提：有群自定义机器人管理权限，机器人安全策略获得管理员允许。在群机器人设置添加**自定义机器人**，按[官方入口](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot)取得 Webhook，可选择签名校验。这是群自定义机器人，非 OAuth 应用机器人。若要求关键词，可采用通知中固定的 `NodeRampart`；若限制出站 IP，应按真实合法出口配置，不能绕过群策略。

在飞书设置隐藏输入 URL 和可选签名 secret，保存后单独测试。请求为 `msg_type: text`、`content.text`。签名时间单位为 Unix 秒，HMAC-SHA256 的 key 是 timestamp + 换行 + secret，消息为空，再 Base64；每次实际请求重新生成，不是钉钉算法。HTTP 200 且数值 `code: 0` 才确认成功；缺字段或类型错误不成功。`11232` 为业务限频；签名/IP/关键词等永久错误先本地修正再重试。2026-10-01 核验官方界限：请求 20 KB、每分钟 100 次/每秒 5 次；本程序使用更小正文/请求界限和至少一秒的持久成功间隔。

停用只暂停已知同目标队列；通过设置替换凭据隔离旧未发消息，最终在飞书撤销/移除机器人。

## WeCom / 企业微信

前提：允许群机器人并有群管理权限。从**群设置 → 群机器人 → 添加**创建，参见[官方群机器人文档](https://developer.work.weixin.qq.com/document/path/91770)。不是智能机器人长连接，也不是 CorpSecret 企业应用。URL 中 `key` 为秘密，不增加不存在的签名机制。

隐藏输入 URL，保存并明确测试。使用 `msgtype: text`、`text.content`，提及列表为空。HTTP 200 必须包含数值 `errcode: 0`；`45009` 持久限频，`-1` 临时重试，其余业务失败隔离待修正。检查机器人 key/群权限，错误不显示厂商正文。2026-10-01 核验：文本最多 2048 UTF-8 字节，每分钟 20 条；成功后至少三秒持久冷却。停用后在企业微信撤销 key 或移除机器人。

## Discord

前提：目标服务器/频道管理员授予 **Manage Webhooks** 权限。在**频道设置 → integrations → webhooks** 创建 Incoming Webhook，参见[Execute Webhook 官方文档](https://docs.discord.com/developers/resources/webhook)。在 Discord 设置隐藏保存完整秘密 URL，只能发送安装绑定频道，不能给任意用户私信。

请求显式 `wait=true`，HTTP 200 且存在合法消息 ID/频道 ID 才确认。禁用所有 allowed_mentions，消除提及语法，动态正文使用安全代码块，事件中的 everyone/here/角色不会扩散通知，不根据返回 URL 改目标。最终 content 最多 2000 字符；官方速率按 route/bucket 动态决定。HTTP 429 与小数 `retry_after`、Retry-After 一起保守处理，超长等待不截短。目标/权限/payload 无效需本地修正。停用后在频道中轮换或删除 Webhook；不能证明同目标的轮换会隔离旧正文。

## Slack

前提：管理员允许用户自己的 Slack App 和 Incoming Webhooks。按[官方创建入口](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/)启用 Incoming Webhooks、授权工作区及绑定频道，再隐藏输入 URL。本版不使用 `chat.postMessage`、Bot Token 或 OAuth 商店流程，不覆盖安装时绑定的频道、用户名和头像。

请求使用固定可访问性摘要和 `plain_text` section，避免动态事件产生格式或特殊提及。HTTP 200 + 文本 `ok` 才成功。归档频道、管理员禁止、失效 token/service、格式错误等永久失败会隔离；429 保留 Retry-After。应检查 App/频道/管理员设置，不反复尝试已撤销凭据。官方通常每频道每秒一条且允许有限突发；本程序至少一秒持久间隔，不保证突发额度。1800 字节摘要低于 section 3000 字符界限。停用后在自己的 App 设置撤销/重建 URL。

源码许可与平台分发许可不同。[API 条款](https://slack.com/terms-of-service/api)对付费、freemium 或连接收费产品的商业分发要求另行授权；面向第三方提供应用还需可公开访问的用户协议和隐私政策（已在页首链接）。用户授权 URL 不能豁免。此免费源码项目未获 Marketplace、合作伙伴或认证声明；商业分发方需自行取得必要 Slack/Salesforce 协议。本程序不代接受协议或注册 App。2026-10-01 复核 2025-10-10 生效条款及[开发者政策](https://docs.slack.dev/developer-policy/)。

## Microsoft Teams Workflows

前提：管理员允许 Workflows/Power Automate、连接具有目标频道发布权限，而且租户允许受保护秘密 URL 的 **Anyone** 触发鉴权。租户用户/Entra/OAuth-only 模式本版不支持，不能关闭或绕过租户安全策略。不新增旧 Office 365 Connector、MessageCard 或任意 Azure 主机支持。

本版选择从头创建的 **When a Teams webhook request is received** 触发器，鉴权 Anyone，再添加 **For each** attachment 与 **Post adaptive card in a chat or channel** 操作，把当前 attachment 的 `content` 作为卡片输入，并固定允许的连接、Team 和频道。保存并确认简单工作流在触发接受后返回 202，再在 Teams 设置输入生成的 callback URL。这是 **Post to a channel when a webhook request is received** 的 Adaptive Card 变体，不是仅 `{text}` 的 *Send webhook alerts to a channel* 变体。参照[创建说明](https://learn.microsoft.com/en-us/microsoftteams/platform/webhooks-and-connectors/how-to/add-incoming-webhook)及[Teams connector 触发契约](https://learn.microsoft.com/en-us/connectors/teams/)。

完整合成示例：

```json
{"type":"message","attachments":[{"contentType":"application/vnd.microsoft.card.adaptive","contentUrl":null,"content":{"$schema":"http://adaptivecards.io/schemas/adaptive-card.json","type":"AdaptiveCard","version":"1.2","body":[{"type":"TextBlock","text":"NodeRampart 合成通知","wrap":true}]}}]}
```

仅支持有界 `<environment>[.<两字符scale>].environment.api.powerplatform.com` callback 形态与生成的 `prod-<数字>.<region>.logic.azure.com` 工作流运行时 URL，路径为文档中的 direct（含 `cu/<数字>`）或 workflows trigger 路径，保留必要 `api-version`、`sp`、`sv`、`sig`。Logic runtime 回调不等于旧 Connector；不允许 webhook.office.com 或宽泛 Azure 域名。未支持形态拒绝，反馈时只提供非秘密主机/路径形态，不提交 query。Microsoft 的回调域名迁移后应重新复制当前 URL。

只有 HTTP 202 + 空正文确认**工作流请求已接受**，不能证明后续操作执行、Teams 展示或已读。测试后检查工作流运行历史和频道。所有者需维持连接授权，按组织策略添加共同所有者，避免离职后工作流无人拥有；失效连接应修复/重新授权，URL 通过本地设置轮换。额度取决于 Power Automate profile/licensing/trigger；遵守 429/Retry-After，不把旧 Connector 的速率边界套用于 Workflows。本客户端的请求限制更保守。2026-10-01 核验，真实租户联调仍 NOT RUN。

## Google Chat

前提：Workspace 管理员允许目标 Space 的 Incoming Webhook，用户有创建权限。在**Space → Apps & integrations → Webhooks** 创建 NodeRampart Webhook，参见[官方 quickstart](https://developers.google.com/workspace/chat/quickstart/webhooks)。仅向这个 Space 发送，不能给任意 Google 账号私信，也不索取聊天历史读取权限。

隐藏输入完整 URL，保留秘密 `key`/`token` 查询参数。请求 JSON `text` 做安全 Markdown 转义。HTTP 200 必须返回同一 Space 的 message `name` 和 thread.name，缺失/空/畸形响应不成功。凭据/权限/目标错误为永久问题，429/5xx 按持久冷却重试。官方完整消息上限 32000 字节，Space 写入每秒一次且与其他 App/Webhook 共享；客户端正文更小且至少一秒成功间隔。停用后删除或重建 Space Webhook 撤销访问。

## 队列、恢复与回退

所有修改使用 draft → validate → 非秘密 review → apply/rollback 与既有恢复 journal。sender 持有已检验凭据快照；URL/签名修改保守创建新非秘密目标身份并隔离旧未发正文。A → B → A 不复活旧 A。停用已知同目标只暂停，重新启用遵守原七天 TTL、租约与持久 cooldown；隐私收紧或不可比较变化隔离旧正文。已发出的在途请求仍可能在原目标完成，不能撤回。

明确选择**丢弃隔离正文**或 `sudo noderampart notify discard-isolated --channel slack` 只擦除该渠道隔离未发正文，不改其他目标、已发历史或冷却。新目标不接收历史事件/队列或启用前结束的日报；原有 report backfill 仍只生成本地归档，不发送消息。服务器等待超长/不可表示时暂停等待显式 resume，不提前重试。永久凭据/权限/payload 错误 quarantined；超时、断链或远端成功但本地写回失败仍可重复，用本地消息/事件 ID 关联，不能保证 exactly-once。

全局 outbox 限 10000 条/32 MiB；原有 8 个渠道各有 1250 条/4 MiB 准入份额，跨凭据轮换身份一起计。旧队列超份额仍保留并可发送，新增需降至份额。拒绝、隔离、过期可在状态观察；新原生事件不使用 Telegram HTML 合并。固定每渠道一个 worker、每次最多 20 行，外部网络不在数据库写事务中执行。

schema 13 迁移加入事件渠道 CHECK 和原生日报启用边界；当前公开版使用包含账户渠道状态的 schema 14，配置/控制 API 1、sensor 协议 5 不变。升级前保存匹配的旧数据库、配置及凭据备份。旧程序拒绝较新 schema；有意回退 alpha.7 时须离线恢复 alpha.7 匹配备份，回退其他版本同样需要其匹配备份，不提供原地降 schema。普通卸载保留凭据；明确 purge 仅处理固定产品拥有路径，不追随任意凭据引用删除用户文件。

手工 `*.credential.json` 或 `secrets/slack-user.secret` 等不是产品创建的凭据；purge 会拒绝，须先移出产品目录。手工凭据修改应原子替换后重启；推荐使用隐藏设置中的管理轮换。尝试间隔（包括失败）为企业微信三秒、其他原生渠道一秒；显式操作员 resume 可清除冷却，普通重启/重新启用不会。

原生包 purge 前应备份配置、数据库和凭据。管理卸载命令先检查未知凭据，再移除包；直接 `dpkg --purge` 可能在 postrm 拒绝未知凭据前已删除包拥有的 conffile，因此失败不保证安装完全未变。未知凭据本身仍保留。

[alpha.9 的 QQ Bot／LINE Push／Twilio SMS／WhatsApp 模板账户渠道](OFFICIAL_NOTIFICATION_CHANNELS.zh-CN.md)已包含在当前公开版，另有订阅、费用与发送意图规则；本文六个 Webhook 契约保持不变。
