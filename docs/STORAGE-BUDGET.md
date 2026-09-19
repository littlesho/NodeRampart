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
columns or tables, and executable schema objects. Restore verifies the source
and uses SQLite to build another consistent database at a **new** output path.
It never overwrites the original database. Stop the daemon, restore to the new
path, review the result, and select that path in configuration. Keep the original
until the restored daemon and data have been checked.

Unit tests exercise committed-WAL backup/restore, strong-mode backup, exclusive
ownership, corruption/schema rejection, path/symlink checks, cancellation, page
cap exhaustion, and actual database+journal sizes during a large uncommitted
update. These tests are unprivileged local checks. They do not establish power
loss recovery, filesystem fault tolerance, or Debian/Fedora lifecycle results.
