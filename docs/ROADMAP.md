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
  matrix stays unverified until actually run. See [validation scope](ALPHA_LIMITATIONS.md).

## `v0.3.0-alpha`

- Event timelines and incident inspection with SSH context and notification outcomes.
- Alert coalescing and expiring, inspectable silences.
- Historical integrity and health overview with explicit gaps.
- Multiple-interface capture, IPv6 route discovery and route changes.
- Bounded historical daily-report backfill with coverage labels.
- Offline pseudonymized metadata replay and threshold comparison.
- See [v0.3 operations](V0.3_OPERATIONS.md) for the supported contracts.

## `v0.4.0-alpha.1` — current development version

- Local terminal management, optional data-only GeoIP updates and public tariff caches.
- Budget/health alerts, saved pricing evidence and bounded redacted local exports.
- Source publication is separate from package releases; see [current limitations](ALPHA_LIMITATIONS.md).

## `v0.5.0-beta`

- TC/eBPF bounded collector and independently reviewed loader.
- Distribution/architecture compatibility matrix and 72-hour soak.
- Distributed scan/brute-force correlation, data-quality reconciliation, signed price catalogs, and release SBOM/provenance.

## `v1.0.0`

- Independent security review, stable config/schema migration policy, reproducible release process, and documented support lifecycle.

Automatic blocking remains a separate opt-in component considered only after false-positive data is available.
