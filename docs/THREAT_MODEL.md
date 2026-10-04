# Threat model

## Assets

- VPS availability and network access.
- Root and service-account boundaries.
- Telegram Bot token and privacy hash key.
- Remote IP observations, authentication usernames, traffic history, and security events.
- Integrity of binaries, configuration, systemd units, MMDB files, billing profiles, and SQLite state.
- Accuracy indicators that prevent missing data from appearing as zero attacks or zero traffic.

## Adversaries

- An unauthenticated remote host generating malformed or high-cardinality traffic.
- A remote host repeatedly attempting SSH authentication.
- A local unprivileged user trying to inject sensor/control messages or read security data.
- A compromised notification endpoint returning malicious text or excessive responses.
- A malicious or compromised dependency/build action.
- An operator mistake involving paths, permissions, thresholds, price profiles, or purge.

An attacker who already has root is outside the confidentiality boundary. NodeRampart should still avoid making persistence, credential discovery, or lateral movement easier.

## Major threats and controls

| Threat | Control in `v0.2.0-alpha` | Residual risk |
| --- | --- | --- |
| Packet parser memory corruption | Go bounds checks, explicit header/length checks, no C parser | Logical parser bugs and CPU load remain possible |
| Flow-cardinality exhaustion | Fixed per-batch key cap, overflow counters, global protocol totals, and kernel packet/drop statistics | Drops remain possible; statistics-read failures and reconciliation gaps are surfaced |
| Sensor IPC interruption | Bounded pending health counters, IPC send-loss estimates, atomic parser-error accounting, and a write deadline | No daemon persistence acknowledgment; pending counters are lost on sensor restart and attributed to the recovery hour |
| Privileged sensor compromise | Separate user, only `CAP_NET_RAW`, no AF_INET/AF_INET6, no payload persistence | `CAP_NET_RAW` remains sensitive; independent review is pending |
| Fake sensor messages | Socket mode `0660`, explicit sensor UID, strict frames and verified daemon UID | A compromised sensor UID can inject summaries |
| Unauthorized local control | Socket mode `0600`, root/daemon UID peer check, 16 concurrent handlers | Root can control or replace the service by definition |
| Config/path substitution | Absolute clean paths, regular-file and symlink checks, strict JSON | Parent path races need more adversarial testing |
| Secret disclosure | Token only in mode-`0600` file, no CLI token, redacted delivery errors, no token in DB | Host root and process memory can access it |
| Notification injection | HTML escaping, control-character removal, 4,096-byte cap | Social engineering within legitimate field text is still possible |
| Notification outage/flood | Persistent deduplicated outbox, retry backoff, 10,000-message/32-MiB pending caps | New notifications are dropped with a logged error once either cap is reached |
| SQLite corruption/full disk | Enforced active-file budget, reserved critical capacity, bounded retention/pruning, verified snapshots and independent failure health | Backups/filesystem overhead are outside the quota; corruption repair and power-loss behavior need further validation |
| Billing misinformation | Disabled by default, required source URL/effective date, “estimate” warning | Guest traffic cannot reproduce provider billing categories |
| Unsafe uninstall | Fixed purge targets, root check, symlink and nested-mount rejection before deletion, and service-stop checks | Privileged concurrent path/mount changes can still race preflight checks; package-manager behavior requires VM validation |
| Supply-chain action drift | GitHub Actions pinned to full commit SHAs, read-only workflow permissions, weekly dependency update proposals | Go module provenance and release attestations need expansion |

## Security invariants

1. Network content beyond headers is not sent from the sensor or stored.
2. The sensor service has only `CAP_NET_RAW`; the daemon service has empty
   capability sets. The root GeoIP updater's bounding set is `CAP_CHOWN`,
   `CAP_DAC_READ_SEARCH`, `CAP_FOWNER`, `CAP_KILL`, `CAP_SETGID`, `CAP_SETUID`
   and `CAP_DAC_OVERRIDE`.
   Only `CAP_SETUID` is ambient for that updater, and `NoNewPrivileges=yes`
   remains enabled.
   `CAP_DAC_OVERRIDE` allows the root updater to connect to the daemon's control
   socket (mode `0600`) for readiness checks during activation.
   中文：`CAP_DAC_OVERRIDE` 用于让 root 更新进程连接 daemon 拥有的 0600 控制 socket，
   在激活期间确认服务就绪状态。
3. Sensor input is bounded before allocation and before state growth.
4. A data-collection failure must be visible in status or report quality fields.
5. NodeRampart never makes firewall changes in the alpha.
6. Notification credentials never appear in process arguments, config dumps, SQLite, or normal errors.
7. Billing output always identifies itself as an estimate and carries a profile effective date.

## Required reviews before stable release

- AF_PACKET parser fuzzing and prolonged high-cardinality traffic.
- Unix socket ownership and peer behavior across Debian/Fedora package installs.
- systemd sandbox compatibility and `systemd-analyze security` review.
- SELinux enforcing policy on Fedora.
- SQLite migration, disk-full, power-loss, and corruption recovery.
- Telegram 401/403/429/5xx, TLS, DNS, and prolonged outage handling.
- Install, upgrade, remove, purge, symlink, and mount-point adversarial tests.
- Independent review of the sensor, IPC, packaging, release workflow, and secret handling.

## v0.2 reliability boundaries

Supported OpenSSH origins require the journal-assigned root UID (`_UID=0`), an
approved OpenSSH executable (`_EXE`) and direct `syslog` or `journal` transport
(`_TRANSPORT`). When `_SYSTEMD_UNIT` is present, it must identify an approved SSH
system service or recognized login session scope. A genuinely absent
`_SYSTEMD_UNIT` is accepted only when those strong origin checks pass and no user
service unit is identified by `_SYSTEMD_USER_UNIT`. Explicitly empty, null or
unapproved unit values are rejected; missing UID, executable or transport
metadata still fails origin validation. `_COMM` and `SYSLOG_IDENTIFIER` alone
do not authenticate a sender.

Complete log grammar isolates arbitrary usernames from endpoint fields. Custom
SSH units/paths need explicit compatibility work; trusting a syslog tag or
renamed process alone is insufficient.

中文：SSH 来源验证仍要求 journal 记录的 root UID、允许的 OpenSSH 可执行程序，以及
`syslog` 或 `journal` 直接传输。`_SYSTEMD_UNIT` 存在时必须属于允许的 SSH 系统服务或
有效登录会话；只有该字段真正缺失、上述强来源条件均成立且没有用户服务单元身份时，
才允许接受。显式空值、`null` 或错误单元仍被拒绝；缺少 UID、程序或传输元数据也不会
放行。`_COMM` 和 `SYSLOG_IDENTIFIER` 等名称不能单独证明来源可信。

The storage budget limits configured active database/journal files and rejects
writes before reserved capacity is exhausted. Independent health preserves
failed-operation evidence when a different kind of write succeeds. Consistent
backups contain the same sensitive data as the database and live outside the
active-file quota. Restore creates a new file, validates schema/integrity and
requires offline operator selection; it does not repair arbitrary corruption.

Static exports escape stored text and use no executable or remote content.
Notification errors omit upstream response bodies, request URLs and secrets.
Destination cooldown, quarantine and expiry are durable; queued is not sent.

## Disposable-VM network test boundary

The test-only namespace fixture requires explicit disposable-lab authorization,
root and VM detection. It verifies the namespace differs from PID 1 and matches
its harness handle, then restricts interfaces, addresses and routes to a fresh
pair with no host uplink. Traffic and execution time are bounded; cleanup owns
only the namespaces and child processes created by that invocation. This adds
no production capabilities and does not establish privileged lifecycle, packet
rate, or complete VM-matrix acceptance.

## alpha.8 native notifications (development candidate)

Complete vendor webhook URLs are credentials. Protected reference-only files
use descriptor-based, no-link reads, bounded strict JSON, trusted ownership and
minimal daemon-readable modes. Management uses the existing review/journal and
atomic replacement; failed optional credentials degrade only their own channel.
Diagnostics retain fixed error categories and numeric codes, never request URLs,
response prose or protected query/signature values. Queue destinations are hashes.

Exact vendor HTTPS host/path/query allowlists do not broaden generic Webhook or
heartbeat validation. Native direct connections reject private, loopback,
link-local and reserved addresses after DNS and at the actual dial peer, reject
redirects, and keep TLS verification enabled. Native transports do not consume
system proxy configuration. Legacy administrator proxies remain a separate trust
boundary: they can see their routed credentials and perform remote DNS.

Messages neutralize terminal controls and dynamic mentions/formatting, contain
bounded summaries rather than reports/logs, and use platform-specific response
checks. A permanent payload/permission failure quarantines that row; rate limits
and attempt floors survive restart. Rotation/privacy changes isolate older
bodies; requests already sent can finish at the original receiver. Timeouts and
local acknowledgment failures may duplicate notifications. Neither acknowledgments
nor Teams workflow acceptance establish reading or end-to-end delivery.

Source MIT licensing is separate from vendor service terms, administrator approval
and message permission. Slack commercial distribution may require a separate
agreement; no platform certification is claimed. See [privacy](PRIVACY.md),
[user agreement](USER_AGREEMENT.md) and [contracts](NOTIFICATION_CHANNELS.md).

中文：完整 URL/签名为秘密，配置、队列、状态、日志与 evidence 不保存正文凭据；
每个可选渠道独立 fail closed。原生直连每次校验 DNS/实际地址，不重定向、不跳过
TLS、不使用系统代理。固定摘要防提及/格式注入，显式成功判据拒绝缺字段。
目标切换不能撤回在途请求，重试可能重复，Teams 只证明请求接受；许可不等于平台审批。

## Alpha.9 paid / official account notification boundary

Possible external acceptance followed by timeout, crash or ledger-write failure
is a side effect, not a safe retry signal. Official intents precede HTTP. Only
LINE's persisted first-request retry UUID can authorize recovery in its bounded
window; other unresolved submissions remain `delivery_unknown`. Reserved charges
are not refunded on ambiguity. Fixed-channel UTC budgets survive target/token
rotation and clock rollback; they constrain this instance, not all account fees.
Twilio polling reads only a durable Account/SID. Neither status lookup nor ordinary
resume reopens POST. Opt-out evidence survives ordinary rotation; supported paid
ledger restore requires explicit external reconciliation before new sends.

The protected files now contain phone numbers, platform recipient/application
IDs and account/token credentials. They are not CLI/config/log/metric labels.
Masked previews and pseudonymized exports limit disclosure; raw platform errors
are untrusted and never persisted. DNS and connected peers are checked on every
new direct connection, strict TLS/HTTPS remains mandatory and environment proxies
are excluded. Templates are bounded approved positional BODY mappings, with no
free-text fallback. Subscription declarations are not platform approval or proof
of consent; operators must actually maintain withdrawals and a support route.
No inbound STOP or delivery callbacks are observed here. Already accepted or
in-flight messages cannot be recalled locally. Existing vulnerability findings
and stripped/Fedora scan coverage gaps remain; attestation does not remove them.
