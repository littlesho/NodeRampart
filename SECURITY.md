# Security policy

## Supported versions

<!-- current-release:start -->
`v0.4.0-alpha.13` is the newest published release and
[GitHub Latest](https://github.com/littlesho/NodeRampart/releases/latest), with
`draft=false` and `prerelease=false`. Product maturity remains **Alpha**. Security
fixes are applied to the latest alpha branch only until a stable support policy
is published.
<!-- current-release:end -->

## Go crypto/x509 security maintenance notice (2026-10-21)

CVE-2026-111943 / GO-2026-6809 concerns distrust of known compromised
intermediate CA certificates in Go's standard-library TLS certificate
verification. Public alpha.12 binaries were compiled with Go 1.26.9,
which the upstream Go vulnerability database marks affected. The
historical vulnerable-compiler CI scan failed on
[main CI 39558165468](https://github.com/littlesho/NodeRampart/actions/runs/39558165468);
that original failure is retained.

Published [v0.4.0-alpha.13](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.13) replaces the latest distributable Go executables with Go 1.26.10 builds. Fedora 43/44 packages use a SHA256-verified upstream Go compiler; the maintainer's Draft audit checked all 18 packaged program build versions. Release ID `419678004` passed 22/22 anonymous public asset hashes and 21/21 pinned checksums. The original alpha.12 Tag, source, binaries and attestations remain immutable and affected; installing a new Go compiler on a host does not patch existing binaries. This release remains Alpha and native ARM64/VPS/soak are NOT RUN. See the [Go advisory](https://pkg.go.dev/vuln/GO-2026-6809) and [scoped verification](docs/RELEASE_VERIFICATION.md#alpha13-ordinary-release-and-latest-publication).

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub's private vulnerability reporting or open a private draft advisory at:

https://github.com/littlesho/NodeRampart/security/advisories/new

Include the affected version or commit, operating system, configuration needed to reproduce, impact, and the smallest safe reproduction available. Do not include real VPS credentials, Telegram tokens, private SSH keys, production IP inventories, or unrelated user data.

The maintainer will acknowledge a complete report when possible, coordinate a fix and disclosure window, and credit the reporter if requested. No bug bounty is currently offered.

## Deployment warning

NodeRampart is an Alpha observation tool. Keep provider DDoS protection, a correctly configured firewall, SSH hardening, patch management and backups in place. The [current validation scope and limitations](docs/ALPHA_LIMITATIONS.md) describe completed acceptance and remaining work: independent security review, the full distribution/architecture VM matrix and sustained high-rate testing are unfinished. GitHub Release/Latest status does not establish production readiness.

## Installation and management boundaries

The explicitly invoked maintained installer resolves the newest published release
(including prereleases), or honors an explicit `--version`, then pins that release
for its checksum-verified bootstrap and native packages over HTTPS. This checks content/transport
consistency; it is not an independent author signature. Release build provenance
is prepared by the workflow for separate verification. The observer does not
download executable updates, open network listeners or modify firewall rules.

The local administrative TUI requires root. It writes bounded configuration and
data files, invokes fixed installed service/package commands, and records an
interrupted configuration apply for explicit recovery. It preserves stopped and
masked service choices during edits; it does not provide a transaction across
arbitrary concurrent root edits, external package operations, systemd and SQLite.
Root configuration records contain file references, not bot/key contents.
The daemon remains unprivileged and the sensor retains only `CAP_NET_RAW`.

Optional GeoIP updates run as a separate root data-only oneshot, with bounded
archive/MMDB validation and fixed HTTPS hosts. MaxMind account credentials stay
root-only; Telegram and privacy keys are daemon-owned `0600` files. No credentials
or licensed databases are bundled. Manual backups of `/etc/noderampart` contain
secrets and should be handled accordingly; ordinary application database backups
do not include those credential files.
