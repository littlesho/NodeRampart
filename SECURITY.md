# Security policy

## Supported versions

<!-- current-release:start -->
`v0.4.0-alpha.10` is the newest published release, an alpha prerelease. Security
fixes are applied to the latest alpha branch only until a stable support policy
is published.
<!-- current-release:end -->

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub's private vulnerability reporting or open a private draft advisory at:

https://github.com/littlesho/NodeRampart/security/advisories/new

Include the affected version or commit, operating system, configuration needed to reproduce, impact, and the smallest safe reproduction available. Do not include real VPS credentials, Telegram tokens, private SSH keys, production IP inventories, or unrelated user data.

The maintainer will acknowledge a complete report when possible, coordinate a fix and disclosure window, and credit the reporter if requested. No bug bounty is currently offered.

## Deployment warning

The alpha has not completed independent security review, the full distribution/architecture VM matrix, or sustained high-rate testing. The [current validation scope and limitations](docs/ALPHA_LIMITATIONS.md) distinguish bounded acceptance from unfinished work. Source publication is not package-release or production-readiness approval. NodeRampart is an observation tool and does not replace provider DDoS protection, a correctly configured firewall, SSH hardening, patch management, or backups.

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
