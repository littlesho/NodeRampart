# Budget alerts, health alerts and local evidence / 告警与离线证据

<!-- current-release:start -->
Budget/health alerts, local evidence and the SSH/GeoIP diagnostic additions below
are included in published [v0.4.0-alpha.11](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.11).
Current public installation selects this release; see its [publication scope](RELEASE_VERIFICATION.md#alpha11-pre-release-publication-and-distribution-verification).
<!-- current-release:end -->
Both new alert groups are **off by default**. Open `sudo noderampart tui
--language zh` for Chinese or `sudo noderampart tui --language en` for English.
All settings below are available under **Configuration / 功能配置**. Validate,
review and save the draft to apply it.

预算、健康告警、离线证据与下文 SSH/GeoIP 诊断已包含在公开 alpha.10 中；
当前公开安装选择该版本，CLI 与 daemon 应配套升级。

这四项功能分别解决“费用是否快超预算”“采集是否还正常”“怎么分享排障材料”
和“历史数据为什么不见了”。预算与健康告警需要主动开启；证据导出、裁剪台账
不需要外部账号。查看菜单不会发送测试通知，导出也不会上传文件。

## Budget alerts / 公网出站预算告警

Set a monthly traffic allowance, a monthly estimated-cost budget, or a daily
traffic threshold. For example, `monthly_bytes = 107374182400` means 100 GiB;
80 GiB produces the 80% reminder and 100 GiB the 100% reminder. A jump directly
past 100% produces one 100% alert. Each metric remembers its milestones across
daemon restarts and event-history expiry. Editing its threshold or tariff does
not resend milestones already recorded in that same month.

| Configuration field | Meaning / 含义 |
| --- | --- |
| `alerts.budget.enabled` | Enable these rules / 开启预算告警。 |
| `monthly_bytes` | Monthly observed TX allowance in bytes; 0 disables this rule / 月流量上限，单位字节，0 关闭本项。 |
| `monthly_cost` | Monthly estimated cost in the active billing currency; 0 disables. Requires billing enabled / 月费用预算，需先配置费用估算，0 关闭本项。 |
| `daily_bytes` | Completed-day observed TX threshold in bytes; 0 disables / 完整日流量上限，0 关闭本项。 |
| `daily_growth_ratio` | Alert at this multiple of the preceding daily mean; 0 disables, otherwise greater than 1 and at most 100 / 相对历史日均的倍数，例如 2 表示达到两倍。 |
| `baseline_days` | Look back 3–30 completed days, default 7 / 基线回看天数，默认 7。 |

Enable at least one threshold. Bytes are integers up to 9,223,372,036,854,775,807;
cost must be finite, nonnegative and at most 1e12. The billing free allowance
and the alert budget are separate: the former is applied to pricing; the latter
decides when to notify. Cost monitoring uses the one active tariff profile,
not simultaneous account budgets for every cached provider.

Months and days follow the report timezone. Daily rules inspect the **most
recent completed day**, not the current partial day; they record at most one
evaluation per day. Relative growth requires a sufficiently covered target day,
at least three sufficiently covered reference days and a positive mean. The
daemon checks once on start and then every minute. A new installation may need
several days before the relative rule is available. The daily rule does not
backfill every day missed during a long daemon outage.

“足够覆盖”指已有完整性记录满足检查：网卡计数运行覆盖至少 99%，降级、未知、冲突
合计不超过 1%，无相关缺口或已知裁剪，且台账已开始记录。它不是无丢失证明。
基线不足、读取失败或时间回退会显示原因，不会假装已经完成异常检测。月度及单日
绝对阈值即使覆盖不完整也可触发，并携带覆盖说明。

**The measured input is selected-interface guest TX used as a public-egress
estimate.** Private traffic and duplicated interface paths can be included;
missing collection can undercount. UTC hourly buckets can overlap calendar
boundaries. It is not the cloud provider's billing meter or a guaranteed upper
spend limit. A new month starts a new incident; it does not claim an old cloud
bill has recovered. See [egress calculation assumptions](V0.4_OPERATIONS.md#egress-estimates).

流量预算和免费额度不是同一个设置。比如分给本机的免费额度为 100 GiB，可以把月流量
告警也设为 100 GiB；也可以单独设置预计费用预算。免费额度仍应按本机分摊，不能让每台
服务器重复使用整个云账号额度。修改时区会改变统计日历，应检查新旧时间段的状态。

### Month-end closing / 跨月结算

Before moving to a new month, the monitor evaluates its one recorded previous
month against retained totals and the latest successfully observed threshold and
numeric tariff saved for that month. A crossing in the final sampling interval
can therefore still create an alert. Closing and its event commit together;
restarts do not replay the milestone. Read/write failures defer advancement.
`previous_period` shows the result after advancement. A long outage closes only
the recorded month, without inventing checks for every skipped month.

跨月按旧月份保存的 UTC 起止时间、预算和费率结算，不套用新月份的配置。数据不完整
仍会标注覆盖情况；成功结算后才到达的旧数据不会反复触发补算。旧版状态若没有保存
历史费率，会显示 `historical_policy_unavailable`，不会用当前费率猜算历史费用。

## Health alerts / 采集与存储健康告警

Enable **Health alerts / 采集与存储健康告警** to monitor sensor availability,
interface-counter activity, the SSH journal collector, storage pressure/known
ingestion failures and managed GeoIP updates. Deliberately disabled collectors
are not failures. Unknown readings do not count as recovery.

| Field under `alerts.health` | Default and accepted range / 默认值及范围 |
| --- | --- |
| `enabled` | `false` / 默认关闭。 |
| `grace_period` | `2m`; 10s–1h of observed failure before an alert / 持续异常确认时间。 |
| `recovery_period` | `1m`; 10s–1h of observed healthy state before recovery / 持续恢复确认时间。 |
| `reminder_interval` | `24h`; 0 disables reminders, otherwise 1h–7d / 故障持续时的重复提醒间隔。 |
| `geoip_failure_threshold` | 2; 1–30 consecutive failed managed checks / 连续更新失败次数。 |
| `geoip_stale_after` | `72h`; 24h–30d since the last successful scheduled check / 自动更新多久未成功视为过期。 |

Evaluation runs every minute, so a 10-second confirmation setting does not
promise a notification within ten seconds. Startup has its own grace. A health
incident keeps one identity through start, reminders and recovery. A disabled
rule ends tracking without inventing a recovery event.

GeoIP update checks use separate, sanitized root-owned 0640 metadata, readable
by the daemon group. An unchanged but successfully verified download resets
the failure streak. Staleness applies to managed, enabled daily updates. Older
installations create this metadata at their next managed update/schedule action;
a refresh also discovers an existing enabled managed timer. Missing metadata
for manually supplied databases is unknown. An unconfigured GeoIP feature with
no previous managed schedule is disabled. Re-enable/disable daily updates through
the menu when reconciling timer changes made outside NodeRampart.

Download/verification and timer scheduling have independent failure counters.
Repairing the schedule clears only scheduling failures; it does not claim a
successful download or clear an unresolved update failure. Legacy combined
`schedule_failed` metadata leaves the update outcome unknown until a real refresh.

低空间时系统尝试用已有的关键写入预留空间保存告警。如果数据库已经无法写入，告警会
显示为待保存并重试，无法保证 Telegram 还能发送。未保存的观察只在内存中，强制退出
可能丢失。NodeRampart 停止运行或整台机器离线时，也无法通过自己主动报平安；外部存活
监测仍需由独立系统承担。

### SSH journal failure details / SSH 日志故障详情

`journal_unavailable` is the health monitor's top-level classification. New
observations also record the collector's state and component reason, plus a
specific failure cause when one is available. Notifications, event details and
local evidence use these recorded classifications. The classification alone
does not establish whether the cause is a host condition or a collector defect.

`journal_unavailable` 是健康监测的顶层分类。新的观察会同时记录采集器状态、组件原因，
以及能够确定的具体故障原因，并用于通知、事件详情和离线证据。不能仅凭这个顶层分类
断定 VPS 本身有问题；应继续看具体原因和当时的覆盖缺口。

| Example `failure_cause` | Meaning / 含义 |
| --- | --- |
| `missing_origin_metadata` | Required UID, executable or transport metadata is missing / 缺少验证 SSH 来源所需的用户标识、可执行程序或传输类型。 |
| `untrusted_uid`, `untrusted_executable`, `untrusted_unit`, `untrusted_transport` | The named origin check rejected a record / 对应来源字段未通过信任检查。 |
| `permission_denied`, `journal_files_unavailable` | A journal access or execution permission failure, or unavailable journal files / 日志访问或执行权限不足，或日志文件不可用。 |
| `process_signaled`, `process_exited` | The journal subprocess stopped; a recorded signal or exit code provides additional context / journal 子进程终止，可附带当时记录的信号或退出码。 |
| `malformed_record`, `persist_failed`, `recovery_pending` | An invalid record, failed durable write, or failure still awaiting confirmed recovery / 记录格式无效、持久保存失败，或仍等待确认恢复。 |

SSH origin checks require root UID, an approved OpenSSH executable and direct
`syslog` or `journal` transport. A genuinely absent `_SYSTEMD_UNIT` is accepted
when those checks pass and no user service unit is present. An explicitly empty,
null or unapproved unit value remains invalid. `_COMM` and `SYSLOG_IDENTIFIER`
alone do not establish a trusted SSH origin.

有些可信 OpenSSH 记录确实没有 `_SYSTEMD_UNIT`。在 UID 为 root、可执行程序属于允许的
OpenSSH 路径、传输类型为 `syslog` 或 `journal`，且没有用户服务单元信息时，这种真正
缺失的字段可以接受。显式空值、`null` 或不受信任的单元仍会被拒绝。仅将 `_COMM` 或
`SYSLOG_IDENTIFIER` 写成 SSH 名称，不能通过来源验证。

`journal_signal` reports an observed termination signal. A signal such as
`SIGKILL` does not identify who sent it and does not prove an out-of-memory kill.
Use separate host evidence before assigning that cause.

`journal_signal` 只说明观察到了哪个终止信号。即使记录为 `SIGKILL`，也不能据此确定
发送者或认定 OOM；这类归因需要独立的系统证据。

| Diagnostic scope | Interpretation / 解读 |
| --- | --- |
| `current` | Cause associated with the recorded current failure / 与本次记录的故障相关。 |
| `last_failure` | Retained cause of an earlier failure while recovery is still being checked / 保留的上次故障原因，恢复仍待确认。 |
| No specific cause recorded | Older observations or incomplete diagnostic data; do not infer a cause / 旧记录或诊断信息不完整，明确显示未记录具体原因。 |

Read `diagnostic_at_utc` with the diagnostic scope. Starting another journal
subprocess does not clear a pending record-quality failure. Recovery requires
that a new trusted journal entry is durably acknowledged; duplicate/replayed
checkpoints alone do not confirm recovery. Recovery notifications do not reuse
the old failure diagnostic. Existing failure events and recorded collection
gaps remain subject to their retention rules; recovery does not delete a past
gap or prove that collection was complete during it.

应结合 `diagnostic_scope` 和 `diagnostic_at_utc` 判断原因属于哪次观察。重新启动日志
子进程不会清除待恢复的记录质量故障；恢复需要一条新的可信 journal 记录被持久确认，
重复或重放的检查点本身不能确认恢复。恢复通知不会重新附上旧故障原因。既有故障事件
和采集缺口仍按各自规则保留，恢复不会删除过去的缺口，也不能证明缺口期间已经完整
采集。

### GeoIP update failure details / GeoIP 更新故障详情

GeoIP health keeps its existing update result and failure counters. An optional
validated diagnostic adds the stage and cause of the failed attempt. For
example, the component result can remain `download_failed` while the diagnostic
identifies `validation` / `validator_privilege_drop_failed`. This distinguishes
the update pipeline's result from the step that actually failed.

GeoIP 健康元数据继续保留原有更新结果和失败计数，并可附带经过验证的阶段与原因。
例如，组件结果仍可能是 `download_failed`，具体诊断却是 `validation` 阶段的
`validator_privilege_drop_failed`，说明失败发生在 MMDB 验证器降权步骤。

| Stage | Diagnostic examples / 诊断示例 |
| --- | --- |
| `configuration`, `credentials` | Configuration or saved credentials unavailable; invalid credential format / 配置或凭据不可用、凭据格式无效。 |
| `download` | DNS, connection, TLS, timeout, HTTP status or response limits / DNS、连接、TLS、超时、HTTP 状态或响应限制。 |
| `staging`, `archive` | Temporary storage permissions/capacity, or archive integrity/structure / 临时存放区权限或容量、压缩包完整性或结构。 |
| `validation` | MMDB rejection, resource budget, privilege drop, sandbox setup or invalid validator response / MMDB 验证拒绝、资源限制、降权、沙箱设置或验证器响应无效。 |
| `activation`, `cleanup` | Activation, pending recovery, metadata persistence or old-data cleanup / 启用失败、配置待恢复、元数据保存或旧数据清理失败。 |

The diagnostic can identify `City` or `ASN` when the failed edition is known.
An HTTP failure includes only its numeric status, from 100–599 except 200, and
only for the `download` / `http_status` classification. Fixed stage/cause codes
and bounded numbers are allowed; download URLs, credentials, response bodies,
filesystem paths and raw error text are excluded. A legacy failure without a
diagnostic remains explicitly without a recorded specific cause. A schedule
repair alone does not establish that an update succeeded.

能确定失败数据库时，诊断会标明 `City` 或 `ASN`。HTTP 失败只保留状态码，取值为
100–599 且不含 200，并且只与 `download` / `http_status` 配套使用。诊断仅接收固定
阶段、原因和有界数字；下载 URL、凭据、响应正文、文件路径和错误原文不会进入这些
字段。旧失败若没有具体诊断，会明确显示未记录，而不会根据当前配置补造原因。仅修复
定时服务也不能视为数据库更新成功。

## Recording and delivery / 记录与通知

```sh
sudo noderampart alerts status
sudo noderampart status
```

The TUI exposes **Health and diagnosis → Budget and health alerts**. Current
values, periods, reasons, coverage and pending persistence are visible. The
`milestone` and `last_notification_utc` fields refer to durably recorded alert
decisions, not confirmed Telegram delivery. Inspect notification outcomes for
actual attempts and results.

The status view also shows separate health and budget check groups: last attempt,
completion, successful known evaluation, elapsed time, delay and running/timeout
state. Health checks run and persist first; health and budget each have a separate
15-second phase limit. These process-local measurements reset on restart. Per-rule
`last_persisted_observation_utc`, `pending_since_utc` and `pending_age_milliseconds` distinguish
a durable observation from a write still awaiting retry. Unknown/partial checks
are not reported as successful known evaluations.

在告警状态页可区分“尚未检查”“正在检查”“检查超时”和“结果待保存”，避免把检查
没有完成误认为系统健康。这些时间和状态不能证明 Telegram 已送达。

Each alert creates a local timeline event. If Telegram is enabled, the existing
minimum severity, merging, expiring silences and durable retry queue apply.
Monthly traffic/cost kinds are `budget_month_bytes` / `budget_month_cost`;
daily kinds are `budget_day_bytes` / `budget_day_growth`. Health kinds are
`health_sensor`, `health_interface_counter`, `health_ssh_journal`,
`health_storage`, `health_geoip_update`. These kinds or a selected incident can
be silenced through the existing notification menu. Silencing does not remove
the event record. Delivery remains at least once and may be duplicated around
a remote send whose result was not acknowledged.

## Local evidence / 离线证据包

In **Backup, replay and privacy**, choose **Export local diagnostic evidence**. An
incident's detail list also has **Export this incident**, which carries the
selected incident and query period into the export form. Choose a new file in
a private directory. Existing files and symlink paths are rejected.

```sh
# Run from a root-owned private directory when using sudo, for example /root.
sudo noderampart evidence export --output /root/noderampart-evidence.zip

# Replace the example ID with an incident selected from your local timeline.
sudo noderampart evidence export --incident inc_example \
  --output /root/noderampart-incident.html --format html
```

Default diagnostic window: preceding 24 hours. Default incident window:
preceding seven days. `--since` and `--until` accept RFC3339 timestamps, end
exclusive, with a maximum eight-day window. Supplying only `--until` anchors
the default window there. `--format` accepts `zip` (default), `html`, or `json`.
The output is mode 0600 and never overwrites a file. No upload, browser or
notification is started by export.

A ZIP contains `index.html`, `evidence.json` and `SHA256SUMS`. HTML is standalone
and has no scripts or remote resources. JSON/HTML limits are 1 MiB/4 MiB, with
an 8 MiB total output bound. The snapshot includes the first 100 matching
events, at most 100 related SSH records and bounded coverage, retention and
persisted monitor sections. Explicit truncation flags tell you what was omitted;
narrow the requested period when needed. It is a diagnostic excerpt, not a
complete database backup. An incident outside retained history may be unavailable.

导出只保留允许分享的结构化字段：事件类型、阶段、时间、数量、通知结果、覆盖与裁剪
原因等。主机名、用户名、原始 IP/网段、稳定来源哈希、路径、凭据、聊天 ID、任意摘要、
错误原文及日志正文不进入证据包。每次导出重新生成关联别名，映射密钥不会保存。
**UTC 时间、数量和规则指纹仍可能与其他资料关联**，分享前仍请自行检查文件。

Incident details and exported events include an allowlisted typed `alert` context:
the event's own period, observed amount, threshold, milestone, coverage, baseline
or health reason when recorded. `availability` distinguishes `recorded`, `partial`
and `unavailable`. Old or invalid fields stay missing; current status/configuration
is never substituted for historical evidence. HTML explains the same values as JSON.

### Diagnostic evidence and versions / 诊断证据与版本

The event's optional `alert.diagnostic` contains only allowlisted health
diagnostics recorded with that event. JSON and HTML retain the same safe fields:

| Fields | Recorded context / 记录内容 |
| --- | --- |
| `component_reason`, `failure_cause` | Component classification and specific failure cause / 组件分类与具体故障原因。 |
| `diagnostic_scope`, `diagnostic_at_utc` | Current/retained failure scope and observation time / 本次或上次故障的范围与观察时间。 |
| `journal_state`, `journal_exit_code`, `journal_signal` | SSH collector state, exit code 0–255 and an allowed signal name when recorded / SSH 采集状态、已记录的 0–255 退出码及允许的信号名。 |
| `failure_stage`, `geoip_edition`, `http_status` | Valid GeoIP stage/cause combination, optional edition and associated HTTP status / 合法的 GeoIP 阶段与原因组合、可选数据库种类及对应 HTTP 状态。 |

Unknown values and inconsistent combinations are omitted or shown as
unavailable; arbitrary diagnostic text is never echoed. A partial legacy
diagnostic may contain only a component result or timestamp. Absence of a
specific cause is shown explicitly. As with other alert inputs, the exporter
does not fetch today's status to complete an old event.

未知值或相互矛盾的字段组合会被省略或显示为不可用，不会直接输出任意诊断文本。
旧数据可能只保留组件结果或时间，没有具体故障原因；界面会明确说明这一缺失。
导出不会用当前状态补齐历史事件。`availability` 与各字段实际是否存在应一起解读，
不能将部分诊断当作完整的故障调查。

| Contract | Version behavior / 版本行为 |
| --- | --- |
| `alert.schema_version: 1` | Existing events without a diagnostic retain the original v1 shape / 无诊断的既有事件保持原有 v1 结构。 |
| `alert.schema_version: 2` | A validated `diagnostic` is present; it may still lack a specific legacy cause / 存在合法诊断对象，但旧信息仍可能没有具体原因。 |
| Evidence `format_version: 1` | The outer bundle format stays at 1; each alert context is validated against its own version / 外层证据包仍为版本 1，每个告警上下文单独校验版本。 |

Readers that support only alert-context v1 reject v2 contexts. The unchanged
outer bundle version does not imply that an old reader understands new
diagnostics. Use a reader from the matching release; do not edit version fields
to force compatibility.

只支持告警上下文 v1 的旧读取器会拒绝 v2。外层证据包仍为版本 1，不代表旧程序能够
理解新增诊断。请使用匹配版本的读取程序，不要通过手动修改版本号绕过校验。

历史日报详情也会展示当时保存的计费快照：费率档位、免费额度、字节单位、流量、预计
费用、时区和计算版本。没有快照的旧报告明确显示不可用，不会套用当前价格。

History sections use one SQLite read snapshot. Producer version/commit and
the SHA-256 rule fingerprint describe the daemon's **currently loaded** rules
at export; they do not establish which historical configuration produced every
event. The fingerprint excludes identities, interface names, paths, credentials
and tariff text. No complete configuration or current raw status is embedded.

## Retention ledger / 数据保留与裁剪台账

Use **Health and diagnosis → Data retention and pruning**, or:

```sh
sudo noderampart retention --dataset events --reason time_expiry --limit 20
# Use the returned next_before_id with the same --since/--until for another page.
sudo noderampart retention --dataset events --reason time_expiry \
  --since 2026-09-01T00:00:00Z --until 2026-09-12T00:00:00Z --before-id 123
```

Dates and cursor values above are examples. Filters are optional; CLI/TUI freeze
the default seven-day range. Explicit ranges can span up to 400 days, with
1–100 entries per page. The TUI carries the range and filters across pages.

| Ledger reason | What happened / 含义 |
| --- | --- |
| `time_expiry` | Data exceeded its existing retention period / 超过保留期。 |
| `storage_pressure` | Space limits required trimming or compaction / 存储压力导致裁剪或压缩。 |
| `cardinality_compaction` | Detailed source rows were folded into aggregate totals / 来源过多，明细合并为汇总。 |
| `capacity_eviction` | A bounded history table retired older entries / 有界历史表达到容量。 |
| `silence_body_discard` | A silenced notification body was cleared; outcome retained / 静默后清除消息正文，保留结果。 |

Datasets cover events, hourly traffic/authentication/interface data, per-interface
details, collector health, report snapshots, coverage intervals/gaps, notification
outbox and silences. Existing retention policy remains: detailed events about
seven days, hourly data/reports about thirteen calendar months, with additional
capacity and space constraints. This feature explains pruning; it does not
extend those guarantees or recreate deleted history.

Deletion or compaction and its ledger entry commit together. Entries coalesce
by dataset/reason/UTC action day, retaining operation count, affected source-row
count and bounding data/action times. Affected rows are not lost packets or
people, and a time span does not mean every instant lost data. Compaction may
preserve totals. Remaining row counts and their bounds describe data that still
exists, without proving continuous coverage.

明细台账最多保留 1024 条；淘汰后仍保留按固定数据集/原因归类的累计计数与时间范围。
累计值按数据集/原因过滤，**不按查询时间段过滤**，也不等于本页合计。计数到有符号
64 位最大值时饱和。台账开始之前的裁剪历史未知，不会通过迁移补造。缺数据应同时看
完整性记录（当时未采到）和裁剪台账（后来被删除或合并）。新报告、health 和 incident
详情附带有界摘要；旧日报正文保持原样。

## Upgrade / 升级

Back up and verify the database before upgrading. The alpha.9 to alpha.10 upgrade
keeps SQLite schema **14**, config/control API **1**, and sensor protocol **5**.
Upgrades from older supported schemas use the existing transactional migrations.
Older binaries refuse newer database schemas and may reject new configuration
fields. Use a verified matching database/configuration backup for a planned rollback.

Evidence export format remains 1 and monitor JSON remains version 2, with v1 read
compatibility and explicit missing-history limits. A validated health diagnostic
uses alert-context v2; events without it retain v1. Older strict health/evidence
readers may reject the new fields or v2 context. Upgrade the CLI and daemon together.

升级前先备份并验证。不要让旧程序直接打开新数据库，也不要把普通数据库备份当作可公开
分享的脱敏证据包。公开发布和下载入口仍以 README 中的发布状态为准。

### Load diagnostics and reconcile the updater / 加载诊断与迁移更新服务

Upgrade `noderampart` and `noderampartd` from the same release, and restart the
running daemon so it loads the new collector and notification behavior. Older
strict GeoIP health readers may reject metadata containing the new optional
diagnostic. Replacing only the CLI does not update a daemon that is already
running.

应使用同一版本的 `noderampart` 与 `noderampartd`，并重启正在运行的 daemon，让新的
采集与通知逻辑生效。旧版严格读取器可能拒绝含有新增可选诊断的 GeoIP 健康元数据。
仅替换 CLI 文件不会更新已经运行中的 daemon。

The source and package installers call `assets reconcile-schedule` after
installing the new command. For an existing running installation upgraded
manually, the corresponding steps after installing matching binaries are:

```sh
sudo noderampart assets reconcile-schedule
sudo systemctl restart noderampartd.service
sudo noderampart status
```

源代码和软件包安装流程会在安装新命令后执行 `assets reconcile-schedule`。上面的
手动步骤适用于原本就需要运行 daemon 的安装；若安装流程已完成重启，无需重复重启。

Reconciliation migrates only the recognized, canonical old generated
`noderampart-geoip-update.service` to the current template. The template retains
`NoNewPrivileges=yes`, includes the seven bounding capabilities below, and requests
only `CAP_SETUID` as an ambient capability for the validator privilege-drop path:

```ini
NoNewPrivileges=yes
CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH CAP_FOWNER CAP_KILL CAP_SETGID CAP_SETUID CAP_DAC_OVERRIDE
AmbientCapabilities=CAP_SETUID
```

`CAP_DAC_OVERRIDE` is included so the root updater can connect to the daemon's
control socket (mode `0600`) and confirm readiness during activation.

迁移只更新能够确认由 NodeRampart 生成的标准旧版
`noderampart-geoip-update.service`。当前模板保留 `NoNewPrivileges=yes`，使用上面的
七项能力边界，并且仅将 `CAP_SETUID` 加入 ambient 集合，用于验证器降权路径。
`CAP_DAC_OVERRIDE` 用于让 root 更新进程连接 daemon 拥有的 0600 控制 socket，
在激活期间确认服务就绪状态。
不能仅检查 `User=root` 或能力边界中是否列出了 `CAP_SETUID` 就认定服务实际保留了
该能力。

The supported migration recognizes the exact old three-capability generated
template. Custom base units, including a locally edited six-capability base,
masks and unsafe files are preserved. Existing drop-ins are also preserved;
a canonical base unit can still be migrated when drop-ins exist, and the
command reports that the effective sandbox needs manual review. In particular,
a drop-in can override or clear ambient capabilities even after base-unit
migration.

自动迁移识别的是内容完全匹配的旧三项能力模板。自定义基础单元，包括人工改成六项
能力的基础单元，以及 mask 和不安全文件，都会保留。已有 drop-in 也会保留；存在
drop-in 时仍可迁移标准基础单元，但命令会提示需要人工核对最终生效的沙箱设置。
尤其要检查覆盖配置是否重设或清空了 ambient 能力，不能只看基础单元已经更新。

The command keeps timer enablement and health metadata, does not run a database
update, and does not clear an update failure. If it reports that the unit was
saved but `daemon-reload` failed, complete the reload before retrying the updater.
A subsequent real update or verified unchanged result is needed to establish
update recovery.

该命令保留 timer 启用状态和健康元数据，不执行数据库更新，也不清除更新失败计数。
若提示单元已保存但 `daemon-reload` 失败，应先完成重新加载，再重试更新服务。只有
后续真实更新成功，或确认数据未变且通过验证，才能确认更新恢复。
