# Changelog

All notable changes will be documented here. NodeRampart follows Semantic Versioning after `1.0.0`; alpha configuration and storage schemas may change.

## 0.4.0-alpha.2 - 2026-09-20 (UTC)

- First public installation-package prerelease, published at
  `2026-09-20T00:46:14Z`. All 22 assets were anonymously downloaded and matched
  the verified draft bytes; the checksum manifest passed without renaming.
  Download verification does not constitute installation or runtime acceptance.

- Separate public asset filenames from Debian's internal version: download
  `noderampart_0.4.0-alpha.2_ARCH.deb`, while dpkg checks `0.4.0~alpha.2`.
  SBOM, buildinfo, checksums and attestations use the final public filename.
- Verify the exact uploaded asset names and states before considering the
  Release workflow successful. The alpha.1 draft remains unpublished: GitHub
  changed its tilde-containing filenames, breaking the download/checksum chain.
- Retain the UID/GID fix, bilingual documentation and verified source timestamp
  propagation. RPM Version/Release is `0.4.0` / `0.alpha.3%{?dist}`; program
  version is `0.4.0-alpha.2`. No new installation or ARM64 runtime acceptance
  is claimed. Both earlier tags remain immutable.

## 0.4.0-alpha.1 - Unreleased

- Carry forward the 0.4.0 feature set, PR #5 UID/GID validation fix, and bilingual
  installation documentation. This candidate changes release packaging, not
  product capabilities.
- Read the verified source commit and its timestamp once in validation, then
  pass them to every package, SBOM and collection job. Fedora builds no longer
  depend on a container shell's checkout ownership exception.
- Project version `0.4.0-alpha.1` maps to Debian `0.4.0~alpha.1` and RPM
  `0.4.0-0.alpha.2%{?dist}`; embedded program versions retain the full project
  version. Both native package versions sort after the original preview.
- The immutable `v0.4.0-alpha` tag is retained. Its Release run failed before
  completing the package set; it was not a published installation release.
  This replacement candidate remains a draft until independently verified
  and explicitly published. No publication date is assigned yet.

## 0.4.0-alpha - Unreleased

### Added

- Event-specific alert explanations in incident details and offline HTML/JSON,
  saved tariff details in TUI reports, and monitor execution/persistence status.

- Opt-in monthly byte/cost budgets with durable 80%/100% milestones and completed-day absolute/relative usage alerts.
- Debounced collection, storage and managed GeoIP health alerts, reminders and recovery, with explicit unavailable/pending state.
- Local redacted incident/diagnostic ZIP, HTML and JSON exports with per-export aliases, snapshot consistency and private file publication.
- Transactional retention/compaction accounting, bounded ledger history, lifetime totals and remaining-row inventories through CLI/TUI, health, incidents and new reports.
- Native package bootstrap with fixed-version release checksums, dependency
  installation and a controlling-terminal setup menu; a draft release workflow
  prepares Debian amd64/arm64 and Fedora 43/44 x86_64/aarch64 packages.
- English/Chinese terminal menus for configuration, status, health, reports,
  incidents, notifications, backups, replay, service control and retained removal
  or explicit purge. Existing noninteractive CLI commands remain available.
- Masked Telegram configuration, managed privacy keys and optional authenticated
  GeoLite2 City/ASN downloads with bounded validation and opt-in daily updates.
- Official AWS/OCI egress tariff retrieval, dated offline caches and observed
  month-to-date estimates with explicit byte units, host-assigned monthly free
  allowances and coverage limitations.
- Serialized local management writes, configuration conflict checks and recovery
  records for failed/interrupted service activation.

### Fixed

- PR #5: parse service GIDs as unsigned 32-bit values before permission and
  ownership operations; reject negative, out-of-range and malformed values.
  Shared UID/GID checks also reject the chown reserved value and values that
  cannot fit a native int. Invalid identities fail manager construction without
  falling back to root. Boundary tests preserve valid identity/DAC behavior and
  verify the existing safe model conversion bounds.
- Include the service identity regression tests in the explicit source-package
  manifest. The first installation-package candidate includes the merged PR #5 fix.

- R44–R49: bounded month-end budget closing with recorded policy; notification
  admission uses actual time; indexed retention queries and cheaper monitor
  coverage; independent GeoIP update/schedule health; precise fractional-time
  event bounds; evidence timestamps follow acquisition of the pinned snapshot.

- Replay publication rejects untrusted/shared output directories and replaced
  temporary files (R31).
- Report archive contention honors cancellation; previous-day period conflicts
  no longer block unrelated automatic backfill (R36/R37).

- Reliability follow-up fixes R32–R35 and R38–R43: bounded/canonical sensor IPs,
  partial-family capture continuity, accurate flush cutoffs under backpressure,
  coalesced interface gaps, serialized durable sensor health, finite tariff
  estimates, cancel-safe tier drafts, partial Debian removal, preserved stopped
  source services, and explicit source-package input manifests.
- TUI configuration old/new review, direct record selection, automatic query
  pagination and AWS region selection; identical GeoIP content skips activation.
- Report pricing snapshots keep full inputs for reproduction; release verification
  adds per-package Go dependency SBOMs and attestation instructions.

### Compatibility and availability

- Monitor JSON v2 reads v1 but cannot reconstruct missing historical tariffs;
  older strict readers cannot evaluate v2 or extended GeoIP health metadata.
- Configuration/API schema 1 gains optional alerts and additive commands. SQLite
  schema 6 adds monitor state and retention accounting after schema 5 pricing inputs; old reports remain without a fabricated price history. Billing profiles
  gain optional `unit_bytes`; omitted/zero preserves decimal GB calculations.
- This is the first public installation-package candidate. The source is public;
  packages are prepared as a draft prerelease. Fixed-version bootstrap downloads
  become available only when that same release is published. No release date is
  assigned while this entry is Unreleased; earlier alpha headings describe
  development milestones, not dated public package releases.
- MaxMind requires the user's own account, enrollment and license acceptance;
  databases and credentials are not bundled. Estimates are not cloud invoices.

## 0.3.0-alpha - Unreleased

### Added

- Bounded event timelines and incident inspection with retained phases, SSH
  source/time context and notification decisions/outcomes.
- Fixed-window incident update coalescing, delivery claims and expiring,
  revocable incident/kind silences that preserve event history.
- Historical health segments with explicit unknown/conflicting periods, loss
  evidence, current ingestion health and per-interface traffic history.
- Up to eight explicit interfaces, IPv4/IPv6 main-route discovery and live
  reconciliation, with shared budgets and isolated detector continuity.
- Bounded local report backfill, automatic two-date recovery passes, immutable
  timezone-aware archives and historical coverage/detail-retention labels.
- Offline metadata pseudonymization and deterministic production-detector
  threshold comparison, with strict file/input/output bounds.

### Compatibility

- SQLite schema 4 migrates atomically under the configured storage budget;
  backup verification still supports schemas 1–4. New metadata is not fabricated
  for historical records. Configuration schema 1, control API 1 and sensor
  protocol 3 remain compatible with existing configurations and older sensors.
- New configuration fields require v0.3 binaries. See
  [v0.3 operations](docs/V0.3_OPERATIONS.md) and [current limitations](docs/ALPHA_LIMITATIONS.md).

## 0.2.0-alpha - Unreleased

### Fixed

- Validate complete SSH log grammar and trusted journal origin; count canonical
  authentication failures once and retain the correct event timestamp.
- Exclude normal TCP/UDP replies from scans, preserve noninitial IPv6 fragments,
  prune state before admission, and avoid recovery claims from lossy windows.
- Parse bounded Telegram confirmations/waits and persist destination cooldowns.
- Skip already-generated report queries, correct month-end billing intervals,
  and enforce outbox capacity in UTF-8 bytes.
- Retry temporarily uncommitted network events with bounded, privacy-transformed
  state and explicit loss accounting; preserve flood incident phases and drain
  pending work within the shutdown deadline.
- Recover authentication ingestion under storage pressure by atomically
  compacting source detail while retaining hourly observation totals.
- Shape IPC reads to accept legitimate backlog, retaining rate/identity bounds
  and degraded live coverage while old batches are processed.
- Resolve missing/repeated midnight and scheduled times by civil date, including
  skipped dates and billing month boundaries; clarify journal startup status.

### Added

- Acknowledged journal recovery, bounded coverage history, independent storage
  health, queue diagnostics/quarantine/retry/expiry, and failed-login evidence.
- Schema 3 report snapshots, event history and JSON/text/static HTML exports;
  local doctor, consistent backup verification and restore to a new file.
- Configured active-file storage budget with SQLite page enforcement, reserved
  critical capacity, low-space admission and bounded priority pruning.
- Protocol 3 remote ports, explicit IPC service identities, bounded control
  concurrency, source/package transition checks and service-policy preservation.
- Bounded fuzz jobs/corpora, native RPM CI and a sanitized disposable-lab harness.

Historical validation applies to its original snapshots. See the public
[validation scope](docs/ALPHA_LIMITATIONS.md) and [v0.2 operations](docs/V0.2_OPERATIONS.md).

## 0.1.0-alpha - Unreleased

### Added

- Split CLI, daemon, and bounded `CAP_NET_RAW` sensor architecture.
- OpenSSH authentication, brute-force, flood, and port-scan observation.
- SQLite event, aggregated authentication/traffic, component-health, report, and bounded notification-outbox storage.
- Offline GeoIP/ASN enrichment, Telegram alerts, and daily reports.
- Custom billing profiles with explicit estimation caveats.
- Hardened systemd units, source installer/uninstaller, DEB/RPM builders, and CI.
- Bounded IPC send-loss estimates, parser-error accounting, and SQLite schema 2
  health migration with visible saturation reporting.
- Selected historical Debian 13/Fedora 44 amd64 native build and lifecycle
  checks, including reboot, security-window and application cleanup verification.
  These do not establish the remaining [validation matrix](docs/ALPHA_LIMITATIONS.md).

### Fixed

- Reject flow capacity settings above the 4,096-flow wire limit.
- Restart Debian/source services after reinstall or upgrade to run new binaries.
- Reject unsafe configuration paths before package metadata changes; preflight
  all purge targets and nested mounts before deletion and report stop failures.
- Use Debian prerelease version ordering and retain RPM build provenance.
- Avoid empty RPM debugsource packages for stripped Go binaries.
- Include modern OpenSSH `sshd-session` and `sshd-auth` journal identifiers.
- Retry interface-counter collection when the default route is late at boot or
  interface reads fail; rebase after gaps/resets and report coverage recovery.

### Security

- Strict configuration and IPC decoding, frame rate/counter bounds, bounded memory/storage cardinality, kernel-drop visibility, and capacity-limited notifications.
- Separate capability-limited sensor, local peer-checked sockets, secret-file permissions, safe HTML framing, redirect refusal, symlink-aware paths, and hardened systemd units.
- SHA-pinned read-only CI, vulnerability scanning, dependency update proposals, and bundled third-party license notices.
