# Storage budget and consistent backup

The daemon applies `storage.max_bytes` (default 1 GiB, supported range 64 MiB–1
TiB) and `storage.min_free_bytes` (default 128 MiB). Existing schema-version-1
configuration files inherit these defaults. This changes the daemon's SQLite
mode from the previous WAL default to `DELETE` rollback journaling with
`synchronous=FULL`, `cache_spill=OFF`, `temp_store=MEMORY`, an exclusive connection
lock, and an enforced `max_page_count`. Store.Open alone retains its old behavior
for migration/offline utilities; the daemon configures the budget before running
collectors.

## Exact active-file bound

Let B be max_bytes and P the database page size. The configured maximum page
count is floor((B − 1 MiB) / (2P + 8)). One database image uses at most N×P bytes;
one preimage per original page in the rollback journal uses N×(P+8) bytes. The
remaining 1 MiB covers the initial journal header and small sidecar overhead.
Disabling cache spills prevents additional journal-header segments during a
transaction. Successful mode conversion removes WAL; a blocked checkpoint or an
existing database larger than the resulting page cap prevents startup with an
explicit error. The page cap is verified before writes, including after a driver
reconnect. The exclusive SQLite connection lock prevents another SQLite writer
from bypassing these settings while the daemon owns the database.

This is the bound on the apparent lengths of the configured active database and
its transaction journal, based on SQLite's page and rollback-journal layout. It
is **not a filesystem quota**: it excludes filesystem allocation/metadata,
explicit backup/export outputs, and SQLite query/statement temporary files.
`temp_store=MEMORY` moves supported temporary structures into memory. It does not
limit other programs' disk use. An administrator or process capable of replacing
the underlying files can invalidate application-level bounds. SQLite describes
its [page-count limit](https://www.sqlite.org/pragma.html#pragma_max_page_count),
[rollback journal layout](https://www.sqlite.org/fileformat.html#the_rollback_journal),
and [extra headers caused by cache spills](https://www.sqlite.org/atomiccommit.html).
The formula above is an implementation bound derived from those mechanisms for
the configured single-database connection, not a quota provided by SQLite.

The tradeoff is reduced concurrency and increased RAM for dirty pages during a
transaction. Mutations and pressure pruning use bounded batches; a single large
manual SQL transaction is outside the application's operating contract. The
exclusive connection also means offline tools must wait for the daemon to stop.
A 1 GiB active-file budget provides approximately 511 MiB of main-database page
capacity, with the other half reserved for rollback safety. Do not interpret
max_bytes as a 1 GiB payload allowance.

## Admission, pressure, and retained evidence

Before each mutation, the store inspects active-file sizes and available space.
Normal writes reserve up to 64 MiB of temporary write headroom above the free
space watermark; critical evidence may use the smaller reserve. These checks
reduce disk-exhaustion failures but cannot reserve space against another process
or promise that available disk never changes during an operation.

The last one-eighth of database page capacity is reserved for critical events
and coverage. Every writer, including critical authentication ingestion, attempts
one bounded reclamation step when approaching that reserve. Traffic is deleted
in transactions of at most 256 rows. Next, authentication source detail is
compacted within one hour/kind into `_storage_pressure`, preserving the hourly
observation total. A pass inspects at most 256 indexed rows and never compacts
that summary bucket again. The delete, summary, cardinality adjustment and
`storage_auth_detail_compacted` coverage marker commit atomically. Once a bounded
walk has exhausted authentication candidates, maintenance can delete at most 64
oldest events; intervening ordinary writes cannot consume that maintenance turn.
Counters report rejected writes, compacted source rows and deleted traffic/events.
The compaction count describes source-aggregate rows, not observations or unique
IP addresses. Per-hour loss markers persist (within the 1,000-gap history bound);
budget counters reset when the process/budget is initialized. Compaction does
not guarantee capacity if only nonreclaimable data remains: diagnostics continue
to report reserve pressure or `database_capacity_exhausted`.

Freed SQLite pages are reused; pressure maintenance does
not run a whole-database VACUUM or pretend that deletion shrinks the physical
file. Retention can therefore shorten when the configured budget is reached.

Status reports the database page capacity, live pages, active sidecar sizes,
available bytes, journal mode, rejection/pruning counters, and the current
reason for degraded ingestion. A critical write cannot clear the status while
normal writes remain below their watermark. During a long storage operation,
budget status returns `busy` immediately; it does not block diagnostics behind a
backup. Waiting mutations and backups respect their contexts.

## Backups and restore

Backup uses SQLite `VACUUM INTO`, verifies integrity and the complete supported
schema/migration chain, syncs the completed file, and publishes a new 0600 target
without overwriting an existing path or sidecar. It includes committed WAL data
when called on a legacy WAL store. SQLite documents that
[VACUUM INTO produces a consistent snapshot](https://www.sqlite.org/lang_vacuum.html).
The requested output consumes additional disk outside the active-file budget;
free space is checked separately before starting.

Each parent component is opened without following symlinks. A missing immediate
backup directory may be created as 0700; missing deeper trees are refused. A
private temporary directory and directory descriptors keep preparation and
publication tied to the inspected parent. These controls are intended for the
service's private state directory. SQLite may canonicalize `/proc/self/fd`
paths internally; arbitrary concurrent renaming or replacement by a writer to
an untrusted output ancestor is not claimed to be a supported trust boundary.
Online control restricts output to the state's direct `backups` directory.

Verification opens a standalone snapshot read-only. It rejects live SQLite
sidecars, corruption, unsupported/missing migration steps, missing required
columns or tables, executable schema objects, and foreign-key violations. Restore verifies the source
and uses SQLite to build another consistent database at a **new** output path.
It never overwrites the original database. Stop the daemon, restore to the new
path, review the result, and select that path in configuration. Keep the original
until the restored daemon and data have been checked.

Historical orphan detection is read-only. Enabling foreign keys on every new
connection prevents new violations but cannot repair retained old rows. A
violation is a confirmed history inconsistency, not an instruction to erase
it. A normal backup containing a violation fails verification.

For an operator-reviewed repair, first stop all users of this database and
retain a private forensic copy of the closed database and any remaining SQLite
sidecars. Do not discard an unresolved journal or call this copy a verified
backup. Inspect `PRAGMA foreign_key_check` on a separate copy, identify the
exact affected relationship and reviewed row IDs, and repair only those rows
inside a transaction. Retain the original and an audit of the selected IDs;
never delete all unlinked history. Re-run integrity, foreign-key, schema and
backup verification before considering a recovered copy for use. This release
does not provide an automatic database repair command.

When historical orphans prevent a migration (including schema 11), live daemon
diagnostics may be unavailable. After stopping **all** database users, preserve
the original database and every remaining `-wal`, `-shm` and `-journal` in a
private forensic evidence directory before doing anything else. Preserve that
complete evidence unchanged. Do not delete or rename an unresolved journal to
make a command accept the database. On a separate copy that has independently
been established as a standalone SQLite database, use:

~~~bash
noderampart doctor --foreign-keys-snapshot /private/standalone-copy.db --config /private/config.json
~~~

The supplied snapshot must be a bounded regular file owned by the invoking user
or root, without hardlinks, writable sharing, symlink traversal or any SQLite
sidecar. This check opens it read-only, accepts verified supported historical
schemas, and reports at most 100 table/parent relationships with a truncation
flag. It prints no event IDs or bodies, performs no migration or repair, and
never deletes evidence. Unknown/future schemas, active sidecars, unsafe inputs
and cancellation mean the check could not reliably determine foreign-key
status; they are not healthy results. This check is not full backup verification.

历史孤儿检测只读；每条新连接启用外键不会自动修复旧历史。发现外键违规后，正常
备份验证会失败。人工修复必须先停止所有数据库使用者，私密保留已关闭数据库及剩余
SQLite sidecar 的取证副本，不丢弃未恢复的 journal，也不把取证副本称为已验证备份。
只在另一份隔离副本上查看 `PRAGMA foreign_key_check`，逐项审查关系及行标识，事务内
仅修复明确选定的行，保留原库及审计记录。完成完整性、外键、schema 和备份验证后再
决定是否启用恢复副本；本版本没有自动数据库修复器。

历史孤儿导致迁移（包括 schema 11）拒绝时，daemon 无法启动，在线诊断也可能不可用。
必须先停止所有数据库使用者，私密完整保留原库及剩余的 `-wal`、`-shm`、`-journal`
作为取证证据，保持该完整副本不变；绝不能为让命令通过而删除或改名未决 journal。
只有另一份已独立确认是 standalone SQLite 的副本才可运行上述
`doctor --foreign-keys-snapshot /private/standalone-copy.db --config /private/config.json`。
输入须为当前调用用户或 root 所有、有界且不可共享写入的普通文件，不允许硬链接、
符号链接遍历或任何 SQLite sidecar。检查只读打开、接受已核验的历史支持 schema，
最多报告 100 个表/父表关系并标明截断，不打印事件 ID/正文，不迁移、修复或删除证据。
未来/未知 schema、活动 sidecar、不安全输入及取消均表示无法可靠判断，不代表健康；
此专项检测也不等于完整备份验证。

Unit tests exercise committed-WAL backup/restore, strong-mode backup, exclusive
ownership, corruption/schema rejection, path/symlink checks, cancellation, page
cap exhaustion, and actual database+journal sizes during a large uncommitted
update. These tests are unprivileged local checks. They do not establish power
loss recovery, filesystem fault tolerance, or Debian/Fedora lifecycle results.

## alpha.8 notification admission and schema 13

Eight fixed channels share the existing 10,000-pending-row / 32-MiB outbox bound.
Each channel has a 1,250-row / 4-MiB admission share across all credential identities,
including quarantined/isolated unsent rows. This prevents a failing or repeatedly
rotated target from consuming the whole queue. Upgraded queues above a new share
are retained and may drain; new admission waits below the bound. Pending counts,
rejections, isolation, suppression and expiry remain observable. The seven-day
TTL, bounded retries and retention behavior are preserved. Native summaries are
at most 1,800 UTF-8 bytes; native rows do not use Telegram HTML coalescing.

One worker per configured channel fetches at most 20 rows per pass, claims short
leases and validates target binding immediately before dispatch. A short atomic
reservation persists the minimum attempt interval even after failures/restart;
server Retry-After is never shortened. Explicit operator resume can clear a
cooldown and is not automatic. External requests occur outside write transactions.
Per-channel event decisions are atomic with local event admission and independent
of other channels' success. Teams acknowledgments are exposed as accepted.

Migration 13 expands the fixed event-channel constraint and adds native target
activation timestamps; historic bodies/decisions/targets/language/timezone remain.
Backup schema verification and foreign-key checks include the new version; older
binaries reject it. Daily reports finished before native activation are not
sent automatically. Backfill stays local-only. Rollback needs matching old
configuration, protected credentials and a schema-12 database backup.

中文：八渠道各有 1250 条/4 MiB 准入份额，跨轮换且包括隔离未发正文，全局仍
10000 条/32 MiB。旧超份额队列保留可排空，拒绝/过期可观察。固定 worker/有界租约，
尝试间隔和厂商等待持久化，不在事务内外发；每渠道独立去重/结果。schema 13 保存
原生日报启用边界，不补历史，备份/外键校验同步；回退使用匹配旧备份。

## Alpha.9 dispatch records and budget ledger

Database schema14 adds one bounded dispatch record per official outbox message
(cascade lifetime), four fixed channel-policy rows and UTC-day usage rows bounded
to 400 days per channel. Outbox capacity remains 10,000 rows / 32 MiB, counting
both body and frozen payload. The previous eight channels retain 1,250 rows /
4 MiB shares; the new four have 750 rows / 2 MiB shares. Admission reserves 64
rows / 256 KiB for each other enabled target. Existing over-share history remains
retained while further admission is refused and counted. This is a shared global
bound, not twelve independent global budgets.

Paid defaults reserve at most 20 logical requests per UTC day; Twilio additionally
40 estimated SMS segments with at most two per notification. Zero prohibits
sending. Reservations persist before HTTP and remain consumed on uncertainty;
receipt-write failure cannot refund and requeue. Unknown actual provider price
is null, not zero. Rotation/report timezone/restart do not grant another allowance.
Clock rollback and unsafe cross-day paid retries hold rather than bypass the
ledger. LINE's same logical request retains its UUID/window and reservation.

Supported backup restore places paid channels into reconciliation hold and
isolates old pending bodies. Explicit reconciliation records a bounded local
reference and consumes the current day's allowance; future messages become
eligible under active consent, old uncertainty/opt-out facts remain. The existing
SQLite journal/synchronous policy is unchanged. Restoring an old disk/database
cannot prove a remote API never accepted later messages. Old schema13 binaries
reject schema14; rollback uses matching old DB/config/credential backups, never
in-place schema downgrade. Backup schema verification and foreign-key checks
include these new tables and constraints.
