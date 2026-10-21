# Security policy

## Supported versions

<!-- current-release:start -->
`v0.4.0-alpha.12` is the newest published release and
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
newest vulnerable-compiler CI scan failed on
[main CI 39558165468](https://github.com/littlesho/NodeRampart/actions/runs/39558165468);
that original failure is retained.

The remediation candidate upgrades release and CI Go to 1.26.10,
uses an independently SHA256-checked upstream compiler in Fedora 43/44
build jobs, and requires Go 1.26.10 in final package buildinfo. A new
Release is required to distribute recompiled executables; modifying
existing tag/attestations or server-side Go installations does not fix
already built Go binaries. Until that new release has passed its
reviewed Draft and distribution gates, alpha.12 remains the published
Latest with this known security limitation. See the
[Go security advisory](https://pkg.go.dev/vuln/GO-2026-6809).


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
