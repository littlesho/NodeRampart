# 官方账户通知渠道

[English](OFFICIAL_NOTIFICATION_CHANNELS.md) · [第一批六个渠道](NOTIFICATION_CHANNELS.zh-CN.md) · [隐私政策](PRIVACY.md) · [用户协议](USER_AGREEMENT.md)

<!-- current-release:start -->
本指南面向最新公开版 **[v0.4.0-alpha.11](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.11)**，
已设为普通 GitHub Release／[Latest](https://github.com/littlesho/NodeRampart/releases/latest)。
产品成熟度仍为 **Alpha**；参见[晋级与验收范围](RELEASE_VERIFICATION.md#alpha11-ordinary-release-and-latest-promotion)。
<!-- current-release:end -->

本版包含 `qqbot`、`line`、`twilio_sms`、
`whatsapp_cloud`，每个渠道一个独立目标，与旧八渠道并行；heartbeat 不计入分发。
四项默认关闭。操作者需按所选渠道要求，自行准备获授权的账户、已接受的协议、号码、批准模板及
收件人同意。

自动化使用合成账户和受控 transport。四平台实网、人工接收、实际收费、原生
ARM64 和生产运行均为 **NOT RUN**。接口受理不代表送达或已读。

## 设置、预览与测试

打开原有终端 setup → **通知渠道**，按渠道操作：

1. **受保护凭据**：选择“替换”，逐项输入隐藏的账户、token、发送身份和目标。
   留空保留已有字段；`clear_fields` 明确填写要清空的字段名。整份清空须先停用。
   取消保留原设置。不要把秘密或完整收件地址放进 CLI 参数或 shell 历史。
2. **接收者订阅**：记录真实用途、同意的类型 `event,daily,test` 和有界本地依据
   编号。明确记录新同意才生成本地时间和不可变依据 ID；“保留”不改变它们，
   “撤销”立即抑制未发通知。付费渠道还须明确确认费用。编号不读取同意文件，
   也不是法律合规的自动证明；平台资格和授权须另外落实。
3. **配置渠道**：独立选择 `en`／`zh`、事件最低级别、日报开关、有限额度与启用。
   保存走 draft → validate → review → apply／rollback，不获取 token、不查询
   账户、不发测试，也不收费。通知语言不随界面语言静默改变。
4. **本地预览**：

   ```bash
   sudo noderampart notify preview --channel twilio_sms
   ```

   展示实际遮罩目标、冻结正文／模板、估算分段、有限额度及“实际费用未知”。
   不入队、不预留额度、不联网；无有效订阅或不兼容的正文会被本地拒绝。
5. **独立测试**：付费测试另须确认当前预览与费用：

   ```bash
   sudo noderampart notify test --channel twilio_sms --confirm-paid --preview-id <当前预览ID>
   ```

   TUI 使用相同预览与确认。已启用的 QQ／LINE 可分别用 `notify test --channel
   qqbot` 或 `line`。一次仅生成所选目标的一条合成逻辑通知，仍受订阅、持久化
   意图、额度、限流与有效期约束，不能作为绕过入口。

普通配置只保存凭据文件引用与本地非秘密策略。托管随机凭据文件放在受保护的
服务组目录，由真实 daemon UID 读取，模式 0600。拒绝重复／未知 JSON 字段、
符号链接、多链接、危险父目录、错误权限、变化中的文件及超限内容。预览／状态／
错误不回显秘密。手工替换凭据创建新投递身份并隔离旧未发正文，A→B→A 不复活
旧 A 队列；QQ 正常自动 token 刷新不改目标与额度。收紧或改变不可比较的事件／
模板策略会隔离旧请求；语言／时区变化不翻译旧消息，也不重写归档报告边界。

## QQ Bot：主动 C2C 或群通知

须有自己注册、获准用于相应场景的 QQ 开放平台机器人 AppID／AppSecret、主动
发送资格，以及允许通知的用户／群。受保护字段：`app_id`、`app_secret`、
`target_type=user|group` 与平台签发的 `target_id`。openid／group_openid **不是
普通 QQ 号／群号**，不能直接转换。通过同一机器人身份的已授权官方事件来源或
已有合法集成安全导入 ID。本版不部署回调、常驻 Gateway、不枚举收件人，也不
虚构控制台获取群 ID 的页面。没有合法 ID 或主动能力时保持关闭，状态属于
**ACCOUNT_AUTHORIZATION_REQUIRED**。

核验的当前源为 `https://api.bot.qq.com`，token 交换路径
`/app/getAppAccessToken`，消息路径 `/v2/users/{openid}/messages` 或
`/v2/groups/{group_openid}/messages`，使用 QQBot access 鉴权与自己的应用身份。
仅主动纯文本，不伪造 reply `msg_id`、`event_id`，不借用被动窗口额度。短 token
只在有界内存缓存，过期前单飞刷新，与当前投递共用总时限；明确鉴权拒绝最多一次
合规刷新。不确定的消息提交保留未知状态，不把 `msg_seq` 假定为永久主动幂等键。

按实际数字业务错误区分拒收、权限、额度／限流和回执异常。正文最多 1800 UTF-8
字节是**产品本地限制，并非宣称平台字符配额**；场景、内容／链接权限仍由平台
决定。停用、平台撤销 secret／收件许可后可重新配置凭据；普通 resume 不授予
缺失的主动资格。

## LINE：Official Account 的 Messaging API Push

使用 LINE Official Account／Messaging API channel，首版支持控制台签发的长期
Channel Access Token。受保护字段：`channel_access_token`、
`target_type=user|group|room`、`target_id`。目标是相应 channel 的 U/C/R ID，
不是昵称、可搜索 LINE ID、手机号或另一 Provider 的用户 ID。本人 user ID 可
按相应 Provider 开发者控制台流程取得；群／room ID 须来自已加入机器人的官方
事件。程序不创建回调、不发现联系人。先确认 push 资格、接收者同意、账户套餐／
月额度和现行条款。

POST `https://api.line.me/v2/bot/message/push`，Bearer 鉴权，每条逻辑通知一个
纯文本 message。首次请求前落盘 UUID `X-Line-Retry-Key`，绑定同一目标、正文和
数组顺序；有效重复 409 须有匹配的官方响应及 accepted-request ID，任意 409
不是成功。重试在官方 24 小时内提前停止（本地 23 小时 55 分），重启／时钟回拨
不绕过；过期不换新 key 偷偷补发。200／重复受理只证明平台处理，拉黑／删除的
账户可能收不到。月额度耗尽暂停，不无限重试。手工 token／目标替换隔离旧消息。
不使用 LINE Notify、reply、broadcast 或 multicast。最终正文按 UTF-16 计数，
另受本地 1800 字节限制，保持在官方文本上限内。

## Twilio SMS：分段、同意与有限费用预留

使用自己的已授权账户与目标地区可用的发送身份：E.164 `from` 或
`messaging_service_sid` 二选一；收件 `to` 为 E.164，不接受 `whatsapp:` 等前缀。
推荐 `account_sid`、`auth_mode=api_key`、`api_key_sid`、`api_key_secret`；明确
选择 `auth_mode=auth_token` 时使用 `account_sid` 与 `auth_token`。权限须覆盖
发送及所选消息状态查询。HTTP Basic 秘密只在头中，不进 URL。

官方 Messages 接口为表单 POST
`/2010-04-01/Accounts/{AccountSid}/Messages.json`。回执须有有效 Message SID，
且账户／收件人／正文与请求一致。落盘 SID 后只 GET 同一 Account／SID，不再次
POST，不按手机号／正文模糊搜索历史。最多八次查询、最长 24 小时，重启不重置。
`queued`、`sending`、`sent`、`delivered`、`undelivered`、`failed` 等分别记录；
sent 不是 delivered，delivered 不是人工已读。未知新枚举／无效响应不猜测送达。

默认每 UTC 日 **20 条逻辑提交、40 个估算分段**，每条最多 **2 段**，事件默认高
级别，**日报默认关闭**。GSM-7 扩展字符计两个 septet；非 GSM 按 UTF-16 单元计算，
emoji 的 surrogate pair 计两个。单段按 160／70，多段为 toll-free／无法确定
Messaging Service 实际 sender 保守使用 152／66。先计入产品身份、时间／级别／
核心指标、覆盖提示、本地命令和 STOP 文本，再估算收费。主机名缩略可见；必需
字段放不下会拒绝，不无控拆分、不启用 Smart Encoding／URL 缩短／MMS 回退。
平台 `num_segments`／price／currency 与本地预留分开，缺失费用显示**未知**。
外部账户的编码优化或后台配置会影响最终账单；本地条数／分段限额不是账户金额
硬上限，也不包含其他实例、号码固定费或运营商附加费。

须满足号码／账户验证、明确 opt-in、地区规则，以及适用时的美国 A2P 10DLC；
“只给自己发”不自动免除要求。落实平台 STOP／Advanced Opt-Out 和其他撤回处理；
本程序不收取入站 SMS。21610 同步拒绝或异步查询结果会持久锁定订阅，重启、
轮换、普通 resume 不清除。只有收件者完成平台允许的恢复路径后取得的新同意
依据才可满足恢复条件，不调用 opt-out override、不换发送身份绕过拒收。
平台 ValidityPeriod 为本地剩余 TTL 与五分钟的较小值；已受理消息不承诺本地
停用即可撤回。

## WhatsApp：直接 Meta Cloud API、仅批准 BODY 模板

使用自己拥有／获授权的 WhatsApp Business Platform 资产及具备发送权限的服务端
token，通常为 `whatsapp_business_messaging`。受保护字段：`phone_number_id`
（平台 ID，不是发送手机号）、`access_token`、国际数字 `recipient`、固定
`graph_version=v26.0`。2026-10-02 实际核验官方版本表，不自动追 latest，不接受
任意 Graph URL 或 query token。短期控制台测试 token 不适合长期服务；标为长期
的 System User token 也可能撤销或失去资产权限。单纯发送不索取全 Business
管理权限；模板发现／第三方客户授权的额外权限与审批须分开处理。首版要求 token
对所选业务号码仅有一个 message account 的发送权限；须额外指定
`messaging_account_id` 的多账户范围不受支持，不能猜测计费账户。管理员须使用
有授权的单账户范围。文件保存完整国际数字，API 请求明确加 `+`，避免平台隐式
附加业务号码国家码而误投。

`templates` 明确配置 event、daily、test 各自的已批准 `name`、实际 `language`
及有序 `parameters`。首版仅 BODY 位置文本参数，白名单为
`host_alias,event_kind,phase,severity,time,bounded_summary,local_reference`。
每个路由必须各含后六个字段一次，host_alias 可选；顺序必须与批准模板一致。
bounded_summary 包含覆盖提示，遗漏级别、时间或本地引用的静态模板本地拒绝。
只有已批准结构真正兼容时才能复用模板。NodeRampart 的 en／zh 与模板语言 code
分别绑定；没有中文模板不能伪造 zh code 或偷偷改发英文。入队时冻结模板名、
语言、字段顺序、值与结构指纹。没有自由文本回退、24 小时聊天窗口猜测、媒体、
动态 URL 按钮、营销／认证流程。参数是有界、无制表符／换行的标量，不塞完整
报告或日志绕过审批。

可自行提交的模板草案（**不保证审批通过或归类 Utility**）：

```text
英文 BODY：NodeRampart {{1}}: {{2}} / {{3}}. Severity {{4}}, time {{5}}.
{{6}}. Local reference: {{7}}.
中文 BODY：NodeRampart {{1}}：{{2}}／{{3}}。级别 {{4}}，时间 {{5}}。
{{6}}。本地查看：{{7}}。
参数顺序：host_alias,event_kind,phase,severity,time,bounded_summary,local_reference
```

在自己获授权 WABA 的官方管理入口提交相应语言版本；测试／日报使用相应措辞的
独立或确实兼容的批准模板。POST
`https://graph.facebook.com/v26.0/{phone_number_id}/messages`，Bearer 鉴权，
`type=template`。有效收件关联与 `wamid` 只证明受理；本版没有按 wamid GET
`delivered/read` 的接口，也没有回调监听器。模板拒绝／暂停／禁用、语言或参数
错配、权限／号码／质量／政策与限流分别处理，不能换模板／sender 绕过。
默认 UTC 日最多 20 条逻辑模板提交、高级别事件、日报关闭。操作者须维护实际
可达的支持／退订途径，并处理平台内外撤回；本程序观察不到 STOP、回复或窗口
更新，也不声称所有模板有自动 STOP 处理。自托管自己的账户不是多客户 SaaS／
Tech Provider 授权，不冒称认证伙伴。官方平台说明禁止未授权的第三方工具；
操作者须确认自己的应用、资产及本用途所需的合法权限。启用勾选与 MIT 源码许可
不能提供平台审批或补救被禁止用途。本次部分附加 Meta Platform Terms 页面返回
登录／限流内容，不能把未读条款当成商业分发许可；账户授权仍是独立门槛。
同一收件人本地最短间隔六秒，131056 按有界指数退避，不截短平台等待时间。

## 持久状态、恢复和出站安全

queued 是本地入队；外部 I/O 前持久化意图与预留。accepted 是平台回执；
delivery_unknown 表示可能受理但缺可靠确认。没有官方幂等保证的 QQ／Twilio／
WhatsApp 在租约过期、关闭、断链或回执落盘失败后不自动重发；LINE 只在持久 key
有效期内按原请求恢复。`notify list/status`、timeline 与诊断显示真实状态。
可查看／隔离未知请求，普通 retry／resume／test 不复活它们。不保证 exactly-once，
在途请求仍可能在原目标完成。

未知结果不退还预留；固定渠道范围的 UTC 日桶跨目标／token 轮换、启停、重启与
报告时区变化保持，回拨 fail closed。全局 outbox 仍为 10,000 条／32 MiB，另有
渠道份额及其他已启用目标的预留；冻结 payload 计入容量，不把 12 倍额度无限
累加。远端 I/O 不进入 SQLite 写事务、监控锁或配置应用锁。

schema **13→14** 单次事务保留旧队列／决定／语言／时区。配置／API 仍为 1，
sensor 为 5；旧程序拒绝 schema14。新增字段须使用匹配程序，不能据 schema 数字
不变推断旧二进制理解新渠道。回退用匹配旧数据库、配置及凭据备份，禁止原地降
schema。产品支持的备份恢复让付费渠道进入核对暂停；人工核对远端回执和额度后
用 `notify reconcile-paid --channel twilio_sms --evidence-ref <本地编号> --confirm`
明确恢复未来通知，保守耗尽当前 UTC 日额度，旧未知请求／退订事实不清除。
磁盘／账本回滚不能证明平台没受理后来请求。

代码构造固定官方 HTTPS／443 目的地，严格 TLS、DNS 与实际公网拨号检查；禁止
重定向、私网／链路本地、环境代理、监听器、运行时下载代码或新增 daemon 权限。
旧 Telegram／generic Webhook／heartbeat 的契约和代理行为保持。认证／消息／
查询响应均有界，公开 evidence 仅固定状态和新伪名，不含账户／手机号／provider
原始 ID。普通卸载保留数据；purge 只处理核实归属的产品路径，可能部分失败，
不承诺原子回滚。

## 官方核验与许可边界

核验日期 **2026-10-02**。MIT 源码许可、平台条款、组织管理员授权与收件人同意
是不同义务；本项目没有厂商认证、不继承 OpenClaw AppID／身份，使用原创标准库
发送实现，没有加入厂商 SDK／新依赖。

- [QQ API 概览](https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/overview.html)、
  [官方开发者入口](https://bot.q.qq.com/wiki/)及其中当前 token／C2C／群文档；
  [腾讯技术参考](https://github.com/tencent-connect/openclaw-qqbot)不代替服务协议。
  当前完整开发者服务协议与普遍文本长度单位未能从可读公共页确认，须由操作者
  核对实际批准场景与协议，不声称公开插件许可授予商用平台权利。
- [LINE API](https://developers.line.biz/en/reference/messaging-api/)、
  [重试](https://developers.line.biz/en/docs/messaging-api/retrying-api-request/)、
  [ID 来源](https://developers.line.biz/en/docs/messaging-api/getting-user-ids/)、
  [条款](https://developers.line.biz/en/terms-and-policies/)。
- [Twilio Messages](https://www.twilio.com/docs/messaging/api/message-resource)、
  [SMS 分段](https://www.twilio.com/docs/glossary/what-sms-character-limit)、
  [21610](https://www.twilio.com/docs/api/errors/21610)、
  [Advanced Opt-Out](https://www.twilio.com/docs/messaging/tutorials/advanced-opt-out)、
  [A2P 10DLC](https://www.twilio.com/docs/messaging/compliance/a2p-10dlc)、
  [消息政策](https://www.twilio.com/en-us/legal/messaging-policy)、
  [服务条款](https://www.twilio.com/en-us/legal/tos)。
- [Meta Cloud API](https://developers.facebook.com/docs/whatsapp/cloud-api/)、
  [Graph 版本表](https://developers.facebook.com/docs/graph-api/changelog/versions/)、
  [官方 API collection](https://www.postman.com/meta/whatsapp-business-platform/collection/wlk6lh4/whatsapp-cloud-api)、
  [消息政策](https://business.whatsapp.com/policy)、
  [商业条款](https://www.whatsapp.com/legal/business-terms/)。部分开发者页限流，
  使用可读的官方版本、现行模板／参数／错误页与 collection 补齐契约；部分附加
  Meta 条款须登录，未把空壳或登录页算作完整已读。除明确指出的官方规则外，本页
  数字为产品本地安全上限。操作者仍须审阅实际账户条款、国家／号码／模板资格、
  模板分类和收费，不由本项目代接受协议。

付费日报保留归档区间、不含结束时刻：`2026-03-08-0500/09-0400` 只省略相同的
结束年/月，保留两端 DST 偏移；非零点区间保留实际时分秒。B 是字节，
KiB/MiB/GiB 是二进制字节单位，`~` 标示紧凑近似值。覆盖未知/部分及本地命令保留。
