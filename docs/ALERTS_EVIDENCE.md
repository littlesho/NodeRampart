# Budget alerts, health alerts and local evidence / 告警与离线证据

These features are included in the `0.4.0-alpha.1` package candidate (source public, packages not yet published).
Both new alert groups are **off by default**. Open `sudo noderampart tui
--language zh` for Chinese or `sudo noderampart tui --language en` for English.
All settings below are available under **Configuration / 功能配置**. Validate,
review and save the draft to apply it.

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

Back up and verify the database before upgrading. SQLite automatically migrates
to **schema 6**, adding durable monitor state and the retention ledger. Config
and API remain schema 1, with optional alert fields and additive commands.
Older binaries refuse the migrated database and may reject new configuration
fields. Restoring previous configuration alone is not a database downgrade;
use a verified matching pre-upgrade backup for a planned rollback.

This follow-up keeps SQLite schema 6 and export format 1. Monitor JSON advances to
version 2, with v1 read compatibility and explicit missing-history limits. Older
strict monitor readers cannot evaluate v2 state; older GeoIP health readers may
reject the separate scheduling metadata and show unknown. Use a matching verified
backup for rollback instead of editing version fields.

升级前先备份并验证。不要让旧程序直接打开新数据库，也不要把普通数据库备份当作可公开
分享的脱敏证据包。公开发布和下载入口仍以 README 中的发布状态为准。
