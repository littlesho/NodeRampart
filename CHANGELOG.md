# Changelog

All notable changes will be documented here. NodeRampart follows Semantic Versioning after `1.0.0`; alpha configuration and storage schemas may change.

## 0.4.0-alpha.11 - 2026-10-08 (UTC; Pre-release)

- Read the actual GeoIP timer enable state on every daily-update and setup
  download form opening; report read errors instead of displaying a false NO.
  Keep credentials empty and terms unaccepted in the download form.
- Upgrade SQLite to 1.60.1, libc to 1.77.1, memory to 1.12.1, x/sys to 0.48.0,
  x/text to 0.42.0 and tcell to 2.13.10. Synchronize the Go 1.26 source-build
  requirement, third-party notices and SQLite license removal lists.
- Synchronize journal diagnosis tests with durable acknowledgement, preserving
  quiet/duplicate/trusted recovery assertions and production ACK conditions.
  Observe final terminal cells for Chinese input and navigation regressions.
- Pin download-artifact v8.0.1 and verify both release download paths without
  publishing. Prepare alpha.11 DEB/RPM/SBOM identities and an optional offline
  joint published-list/Latest completion check; retain draft-first staging.
- Retain Alpha maturity for this scoped Pre-release. Formal Release/Latest
  promotion is deferred until user VPS feedback and fresh explicit approval;
  this entry does not claim beta/stable or production readiness.

- Publish the same verified [alpha.11 Pre-release](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.11), Release 403384051, from permanent source `2c9d4416adef3cb64e0523a1b9ac1e691b121c16`. Its 22 anonymous assets and 21 checksums match the frozen Draft and authenticated 22+6 proof bindings; no tag or asset is replaced.
- Explicit scoped waiver accepts missing Fedora43/44 native durable ACK for this Pre-release only. Old PRE-RELEASE-GATE BLOCKED stays unchanged; Debian12 ordinary durable ACK is not fault recovery. Real MaxMind/GeoIP YES, ARM64, long soak and VPS remain NOT RUN. Product remains Alpha, `prerelease=true`, and not GitHub Latest; formal promotion waits for user VPS feedback and new authorization.
- Synchronize current public docs/install examples to alpha.11, keeping historical versions, failures and upgrade sources. See the [publication scope](docs/RELEASE_VERIFICATION.md#alpha11-pre-release-publication-and-distribution-verification).

## 0.4.0-alpha.10 - 2026-10-04 (UTC)

- Restore trusted OpenSSH journal records with genuinely absent unit metadata;
  retain root/executable/transport checks and durable-ACK recovery semantics.
- Add allowlisted SSH and GeoIP failure diagnostics to health, notifications
  and JSON/HTML evidence. Upgrade the CLI and daemon together; strict older
  readers may reject diagnostic fields and alert-context v2. Config/API/sensor/DB
  versions remain 1/1/5/14.
- Correct the GeoIP updater's privilege-drop, cancellation and authenticated
  readiness capabilities. Migrate only the exact old generated service while
  preserving custom units, masks, drop-ins, timer state and failure history.
- Include all 21 new Go files in the explicit RPM source manifest and retain
  its completeness regression and main's installer/documentation checks.
- Publish [v0.4.0-alpha.10](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.10)
  at `2026-10-04T11:54:33Z`, Release ID `402977099`, as an alpha prerelease.
  The permanent source is `79ae500106e5d89b0b65b04bfa48e010dcdb39ac`;
  [release run 37195901702 / attempt 1](https://github.com/littlesho/NodeRampart/actions/runs/37195901702/attempts/1)
  passed 17/17 jobs. DEB is `0.4.0~alpha.10`, RPM `0.4.0-0.alpha.11.fc43/fc44`.
- The exact 22 hosted assets, six SPDX/buildinfo pairs, 21 checksum entries and
  28 actual draft attestation verifications passed. Two final hosted package
  smokes passed on Debian13/Fedora44. Fresh anonymous downloads of all 22 assets
  and 21 checksum entries passed; the 28 proof results were reused by exact digest;
  other hosted runtime cases, native ARM64, real notifications, licensed external
  GeoIP pipeline, production and existing vulnerability/scan limits remain disclosed.
  See the [publication record](docs/RELEASE_VERIFICATION.md#alpha10-publication-and-public-distribution-verification).

## 0.4.0-alpha.9 - 2026-10-04 (UTC)

- Publish [v0.4.0-alpha.9](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.9)
  as a non-latest alpha prerelease at `2026-10-04T04:17:49Z`, Release ID
  `402649106`. The permanent source/tag commit is
  `9cc75b6936d08099847655b5046c57a485c82ff7`; release workflow
  [37144860038 / attempt 1](https://github.com/littlesho/NodeRampart/actions/runs/37144860038/attempts/1)
  succeeded with 17/17 jobs and exactly 22 hosted assets.
- Anonymous 22/22 downloads and 21-entry SHA256SUMS verification passed. The
  authenticated draft's 22 provenance and six runtime-package SPDX subject
  verifications passed and were reused by exact digest identity after publication.
  Official distribution evidence binds hosted bytes, not the earlier local
  candidate, which differed for 20/22 asset digests. Later main documentation
  commits do not change the published source or reissue its assets.
- Final hosted-package VM runtime and 18 hosted-program binary vulnerability
  scans, native ARM64, real platforms / human receipt, actual fees and production
  remain NOT RUN. GO-2026-5970 remains a required-module finding; scanner coverage
  gaps remain. See the [publication record](docs/RELEASE_VERIFICATION.md#alpha9-publication-and-public-distribution-verification).

### Development and repair history before publication

- Check RPM filesystem conflicts in a dependency-free embedded Lua `%pretrans`
  guard before RPM 6's implicit sysusers stage. Retain `%pre` rechecks, service
  accounts and administrator unit masks. The failed `2ec534be8d78` no-tag
  preflight candidate is invalidated; published alpha.8 assets are unchanged.

- Reject a remaining source-install removal helper before DEB/RPM installation,
  including dangling links. Older five-entry transitions retain unrecorded
  helpers for explicit ownership reconciliation; package pre-install never
  deletes them. Preserve administrator masks and drop-ins.

- Fix sensor commit diagnosis by matching receipt session/interface/sequence
  before comparing timestamps at the store's persisted microsecond precision.
  A committed batch's nanosecond remainder no longer causes a false unknown;
  missing, pending, legacy, mismatched and partial observations remain visible.
  Status adds bounded receipt identities without changing schema 14, API/config
  schema 1 or sensor protocol 5. Published alpha.8 bytes remain affected.

- Add one-target official QQ Bot active C2C/group, LINE Messaging API Push,
  Twilio Programmable Messaging SMS and Meta WhatsApp Cloud approved-template
  channels. All default disabled; no callback, Gateway, chat, discovery or
  unofficial account protocol. Each has independent en/zh event/recovery/test /
  daily presentation, event selection and reviewed protected-file TUI setup.
- Persist official request snapshots, dispatch intents, acceptance receipts and
  uncertain outcomes. LINE retries reuse one durable UUID within a bounded
  24-hour window; non-idempotent uncertain submissions remain held. Twilio
  acceptance and bounded SID status GETs never reopen POST. Preserve opt-out
  facts across rotation/restart and hold paid delivery after supported restore.
- Add UTC-day logical/estimated-segment reservations and explicit paid preview /
  confirmation. SMS counts GSM-7 extensions / UTF-16 and essential STOP content,
  rejects unfit summaries and keeps actual provider charges nullable. WhatsApp
  uses fixed Graph v26.0 and frozen approved BODY mappings, with no free-text
  fallback or invented delivery/read lookup. Account/recipient permission and
  commercial obligations remain the operator's responsibility.
- Upgrade database 13 to 14 in one transaction; preserve old eight-channel
  histories and bodies. Configuration/control API 1 and sensor protocol 5
  remain. Old programs reject the new database; rollback needs matching database,
  configuration and credential backups.
- Prepare project alpha.9 / DEB `0.4.0~alpha.9` / RPM
  `0.4.0-0.alpha.10%{?dist}` and explicit candidate bootstrap mapping; default
  remains alpha.5. No release/tag/assets or default installation change.
- Real APIs, account authorization, human receipt, actual billing, native ARM64
  and production remain NOT RUN. Existing alpha.8 network/diagnostic limitations,
  GO-2026-5970 module finding and binary/tool scan coverage gaps remain disclosed.

## 0.4.0-alpha.8 - 2026-10-02 (UTC)

- Published as a non-latest alpha prerelease at `2026-10-02T05:42:28Z`
  (`2026-10-02T14:42:28+09:00`, Asia/Tokyo), Release ID `401526101`, from
  `77ae069b8f00651106b9621a24047b0ad7b4e88d`. Hosted release run
  `36960260337`, attempt 1, was not repeated. All 22 fresh anonymous downloads
  match the accepted hosted draft and its already verified 22 provenance / six
  runtime SPDX subjects. See the independent
  [publication record](docs/RELEASE_VERIFICATION.md#alpha8-publication-and-distribution-verification)
  for actual installation scope, evidence reuse and remaining limits.

- Repair bootstrap explicit alpha.8 selection (DEB ~alpha.8 / RPM 0.alpha.9),
  retaining the alpha.5 default and all older mappings. Missing/unpublished assets
  fail closed. Add controlled regressions and an independent three-stage release
  preflight; its historical proposed notes and local candidates do not inherit
  the published hosted assets' provenance.
- Keep metadata regressions free of real fixture tags while checking source
  commit dates, annotated-tag peeling and mismatched/empty source rejection.
- Add six original outbound-only native senders: Feishu custom group robots,
  WeCom group robots, Discord incoming webhooks, Slack App Incoming Webhooks,
  Microsoft Teams Workflows Adaptive Cards and Google Chat Space webhooks.
  Each has one protected-file target, defaults disabled and runs alongside
  Telegram and the unchanged generic Webhook/heartbeat contracts.
- Add independent en/zh setup/test/event/recovery/daily-summary presentation,
  channel-specific status, errors and isolated-body handling to the reviewed
  management/TUI flow. Freeze bodies and report timezone at admission.
- Reuse durable outbox leases, bounded per-channel admission, immutable
  credential snapshots, target/privacy isolation and restart-safe pacing.
  Validate official HTTPS URL shapes, DNS and connected peers; native URLs
  never use an environment proxy, redirects or disabled TLS verification.
- Database schema 13 expands event decision channels and records native daily
  activation boundaries. Preserve schema 12 data and backups; rollback requires
  the matching older database, configuration and credentials, never in-place
  downgrade. Configuration/API 1 and sensor protocol 5 remain unchanged.
- Project `0.4.0-alpha.8` maps to DEB `0.4.0~alpha.8` and RPM
  `0.4.0-0.alpha.9%{?dist}`. Explicit alpha.8 installation uses the published
  assets; the no-argument bootstrap default remains alpha.5. Publication changed
  the same draft's visibility without moving its tag or replacing assets.
- Six real platform APIs / human receivers and native ARM64 remain NOT RUN.
  GO-2026-5970 remains in the x/text dependency graph; the recorded source
  import/reachability scope, stripped-binary and Fedora Go suffix scan gaps,
  and build/scan tool advisories remain disclosed. This is not stable or
  production-ready, and publication does not remove those findings.
- See [actual acceptance and limitations](docs/ALPHA8_ACCEPTANCE.md),
  [channel operations](docs/NOTIFICATION_CHANNELS.md), [privacy](docs/PRIVACY.md)
  and [user agreement](docs/USER_AGREEMENT.md). Real-platform delivery and
  receiver confirmation require separately authorized targets.

## 0.4.0-alpha.7 - 2026-10-01 (UTC)

- Published as an alpha prerelease at `2026-10-01T08:33:06Z`
  (`2026-10-01T17:33:06+09:00`, Asia/Tokyo), from
  `d164978433b5e49d68d310cf8d6f5819b855e2e0`. All 22 anonymous assets match the
  authenticated provenance/SPDX subjects byte-for-byte. Public bootstrap/runtime
  scope is recorded in [release verification](docs/RELEASE_VERIFICATION.md#alpha7-publication-and-distribution-verification).

- Add an offline searchable report-timezone selector with bilingual names,
  date-specific UTC offsets and embedded IANA rules. Preserve exact existing
  aliases and the system timezone; selection still uses the safe draft/apply flow.
- Add independent English / Simplified Chinese Telegram message language for
  events, test messages and daily reports. Saved queue bodies keep their original
  language and timezone context; Webhook messages remain English.
- SQLite schema 12 adds non-secret language and presentation-timezone metadata
  to the existing outbox. Keep a verified compatible pre-upgrade database and
  configuration backup; older programs cannot read the new schema.
- Project `0.4.0-alpha.7` maps to Debian internal `0.4.0~alpha.7` and RPM
  `0.4.0-0.alpha.8%{?dist}`. Bootstrap supports explicit alpha.7 only; its
  default and existing published-version mappings remain unchanged. This is a
  published alpha prerelease; ordinary local/CI candidates remain separate
  artifacts and do not inherit its release proofs or runtime acceptance.

## 0.4.0-alpha.6 - Unreleased

The heading and entries below retain their pre-publication snapshot. Alpha.6
was subsequently published at `2026-09-30T18:54:24Z` from
`4d204b43499ca2f41c15b92334537d57bfc8b20c`; see its
[historical distribution record](docs/RELEASE_VERIFICATION.md#alpha6-publication-and-distribution-verification).

- Integrate the consolidated reliability fixes: initialize every SQLite
  connection, isolate notification recipients and legacy backlog, preserve
  bounded multi-interface delivery and real sampling gaps, and make management
  cancellation, service readiness and recovery report their actual state.
- Keep optional GeoIP, billing and sender failures visible while basic
  monitoring continues; retain fail-closed privacy, identity and file checks.
- Add bounded local diagnostics, complete report JSON/HTML, 7/30-day trends,
  fixed monthly billing cycles and usage scenarios, textfile monitoring,
  offline threshold previews, optional SSH history hints, HTTPS heartbeat and
  one HTTPS Webhook. New outbound features and SSH hints default to disabled.
- SQLite schema 11 retains public schema 7 journal recovery and adds target
  ownership, complete report documents, committed sensor watermarks and
  per-channel notification decisions. Older binaries need a compatible
  pre-upgrade backup; migration is not reversible in place. Sensor protocol 5
  acknowledges committed datasets, with explicit legacy compatibility limits.
- Project `0.4.0-alpha.6` maps to Debian internal `0.4.0~alpha.6` and RPM
  `0.4.0-0.alpha.7%{?dist}`. Official public DEB names use `0.4.0-alpha.6`.
  Verified source commit/date flow once through package, SBOM and collection
  jobs; shared secret/fuzz checks and uploaded asset validation remain required.
- The bootstrap default remains published `v0.4.0-alpha.5`. This alpha.6
  candidate is unreleased; its hosted workflow, packages, cross-version runtime
  acceptance, upload and attestations require separate recorded verification.
  Earlier package and VM evidence applies to its original source and bytes.

## 0.4.0-alpha.5 - 2026-09-29 (UTC)

- Accept trusted OpenSSH records from strictly bounded systemd login session
  scopes without weakening UID, executable, transport or user-unit checks.
- Keep known journal quality degradation pending across journalctl subprocess
  and daemon restarts until a new trusted record is durably checkpointed.
- Database schema 7 stores current recovery separately from historical coverage
  gaps. Upgrades cannot reconstruct unresolved state lost by older versions;
  an older binary requires a compatible pre-upgrade backup for rollback.
- Project version `0.4.0-alpha.5`, Debian internal `0.4.0~alpha.5`, and
  RPM `0.4.0-0.alpha.6%{?dist}`; the RPM Release sequence is not the project version.
- Published at `2026-09-29T14:50:47Z` from
  `f539d18c9a91913a49e4c1d9f36d381965f2f7b7`. All 22 anonymous asset downloads,
  checksums, 22 provenance and six SPDX attestations passed. Debian 12/13 and
  Fedora 43/44 amd64/x86_64 native/package lifecycle and Debian 12/Fedora 43
  public bootstrap installation passed. ARM64 native remains required before beta.
  Later documentation commits do not change the alpha.5 package source identity.

## 0.4.0-alpha.4 - 2026-09-20 (UTC)

- Published at `2026-09-20T13:02:35Z`. All 22 assets passed anonymous
  original-name download and checksum verification against the accepted draft.
  Package/source correspondence and 28 attestations apply to those same bytes.
  This version’s packaged programs, installation lifecycle and ARM64 hardware
  have not been exercised.

- Include PR #14: avoid repeatedly parsing complete shared MMDB targets that
  exhausted the old 64,000,000-operation budget on a valid City database.
  Bound actual precheck work separately from logical expansion, retain complete
  database verification and paired City/ASN activation, and show safe MMDB
  validation/resource-budget errors without exposing credentials or raw errors.
- Retain 64 million precheck operations; bound logical expansion to 128 million,
  cumulative allocation charge to 8 GiB and data-summary cache to 5 MiB.
  Neither charge nor cache size is a total process RSS limit. Synchronous
  `reader.Verify` cancellation waits for the call to return.
- The matching candidate passed every offline stage on the same real City and
  ASN samples. This is algorithm evidence, not acceptance of these new packages,
  real credentialed downloads, activation or daily updates. HTTP 451 upstream
  legal/compliance restrictions are not addressed or bypassed.
- Project/embedded version `0.4.0-alpha.4`; Debian internal `0.4.0~alpha.4`
  and public DEB names `noderampart_0.4.0-alpha.4_ARCH.deb`; RPM
  `0.4.0-0.alpha.5%{?dist}`. Existing UTF-8 fixes and release identity checks
  remain unchanged. The alpha.4 prerelease is now available; older releases
  and tags remain unchanged.

## 0.4.0-alpha.3 - 2026-09-20 (UTC)

- Published at `2026-09-20T05:39:54Z`. All 22 assets were anonymously downloaded
  with original names and matched the verified draft bytes; SHA256SUMS passed.
  Actual x86_64 package CLIs passed 12 bounded UTF-8 scenarios in an isolated
  Debian 13 VM. Chinese input and length boundaries have source regression
  coverage. Installation lifecycle, ARM64 hardware and user SSH client/font
  acceptance remain unperformed.

- Keep interactive setup/tui in UTF-8 when the effective locale is C/POSIX,
  without changing installer machine parsing or management subprocess policy.
  Preserve Chinese output/input and document the per-command workaround for
  unchanged alpha.2 packages.
- Initialize manual setup/tui encoding before interactive work, diagnose explicit
  non-UTF-8 charsets, and avoid splitting Chinese runes at the output byte limit.

- Package mapping: project `0.4.0-alpha.3`, Debian internal
  `0.4.0~alpha.3` with public filenames using `0.4.0-alpha.3`, RPM
  `0.4.0-0.alpha.4%{?dist}`. The alpha.3 prerelease is now available.
  The existing alpha.2 packages and their temporary UTF-8 workaround remain unchanged.

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
