# Roadmap

## `v0.1.0-alpha`

- Source-complete observation pipeline, bounded AF_PACKET prototype, SSH detection, local enrichment, SQLite, Telegram, daily reports, installation assets, and CI.
- Unit and unprivileged build verification.
- A documented, limited Debian 13/Fedora 44 amd64 validation run, including
  final reboot, security-window, and application cleanup checks.

## `v0.2.0-alpha`

- Fix all ten confirmed September 11 review defects with semantic regressions.
- Trusted/recoverable SSH journal ingestion, coverage history, storage health,
  hard active-file budget, consistent backup/restore and local queue operations.
- Archived reports, event history, doctor and static exports.
- Explicit IPC identities, safe source/package transition, administrator service
  state preservation, bounded fuzz jobs and native RPM CI.
- Exact-source Debian 13/Fedora 44 lab validation; remaining distro/arm64/pressure
  matrix stays unverified until actually run. See [current limitations](ALPHA_LIMITATIONS.md).

## `v0.3.0-alpha`

- Event timelines and incident inspection with SSH context and notification outcomes.
- Alert coalescing and expiring, inspectable silences.
- Historical integrity and health overview with explicit gaps.
- Multiple-interface capture, IPv6 route discovery and route changes.
- Bounded historical daily-report backfill with coverage labels.
- Offline pseudonymized metadata replay and threshold comparison.
- See [v0.3 operations](V0.3_OPERATIONS.md) for supported contracts.

## `v0.4.0-alpha.6` — historical published alpha

- Local terminal management, optional data-only GeoIP updates and public tariff caches.
- Budget/health alerts, saved pricing evidence and bounded redacted local exports.
- Local review follow-ups add bounded diagnostics, reports and integrations; the
  [operations guide](V0.4_OPERATIONS.md) describes implemented commands.
- Source publication, local packages and an approved upgrade release are separate;
  see [current validation scope and limitations](ALPHA_LIMITATIONS.md).
- Published alpha.7 adds the searchable report timezone and independent Telegram
  language. Its exact source/distribution/runtime scope is recorded in
  [release verification](RELEASE_VERIFICATION.md#alpha7-publication-and-distribution-verification);
  prior alpha.6 acceptance does not certify the new package bytes.

<!-- current-release:start -->
## `v0.4.0-alpha.11` — current published alpha

- Twelve optional notification channels through the shared terminal menu,
  including four official-account channels with persistent consent/cost controls.
- Trusted SSH records with absent unit metadata, durable-ACK recovery and
  allowlisted SSH/GeoIP diagnostics; updater privilege-drop/readiness and guarded
  unit migration fixes. Config/API/sensor/DB remain 1/1/5/14; upgrade CLI/daemon
  together. Identity-aware sensor receipt diagnosis and early RPM installation
  conflict checks remain included; see the [promotion scope](RELEASE_VERIFICATION.md#alpha11-ordinary-release-and-latest-promotion).
- Current install and verification examples track this published release; the
  maintained bootstrap resolves future published versions when actively invoked.
<!-- current-release:end -->

## `v0.5.0-beta`

- TC/eBPF collector and independently reviewed loader: independent research,
  not a prerequisite for this alpha candidate.
- Distribution/architecture compatibility matrix and 72-hour soak.
- Distributed scan/brute-force correlation, data-quality reconciliation and signed price catalogs.
- Verify the existing SBOM/provenance release path for each exact candidate.

## `v1.0.0`

- Independent security review, stable config/schema migration policy, reproducible release process, and documented support lifecycle.

Automatic blocking remains a separate opt-in component considered only after false-positive data is available.

TC/eBPF 是独立研究，不是当前 alpha 的前置条件。SBOM/provenance 已有实现，
本轮只验证准确候选的构建与证明，不新增发布系统。
