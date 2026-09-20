# NodeRampart

**See what is happening on your Linux VPS: suspicious traffic, SSH logins, daily usage and estimated Internet egress costs.**

NodeRampart watches your server in the background. It records events, builds daily reports and can notify you through Telegram. A terminal menu guides you through setup and everyday management over SSH.

It observes and reports. It does not block IP addresses, change your firewall, inspect application payloads or open a web dashboard port. It is not DDoS mitigation or a traffic-scrubbing service.

[中文说明](README.zh-CN.md) · [Detailed operations](docs/V0.4_OPERATIONS.md) · [Security](SECURITY.md) · [Limitations](docs/ALPHA_LIMITATIONS.md)

> **v0.4.0-alpha.2 — first installation-package prerelease, now available.** Download the [published packages](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.2) using the fixed-version commands below. Anonymous downloads and checksums have been verified; this version has not undergone installation lifecycle or ARM64 hardware acceptance testing. Validate it in an isolated environment before production use. Alpha software belongs alongside your existing security controls.

## What can it do?

| You want to know… | NodeRampart shows… |
| --- | --- |
| Is someone trying to log in? | SSH failures, invalid users, successful logins and repeated failure alerts. |
| Is traffic behaving unusually? | Port-scan and SYN, UDP, ICMP or bandwidth threshold events. |
| What happened during an incident? | Retained start, updates and recovery, related SSH context and notification outcomes. |
| How much traffic did this server use? | Incoming/outgoing totals, daily reports and optional country/ASN attribution. |
| Can I trust this time period? | Collection health, missing coverage, dropped samples and storage problems. |
| What might Internet egress cost? | AWS/OCI public tariffs or your own profile, applied to observed guest traffic with explicit assumptions. |
| Am I near my budget, or has observation failed? | Monthly 80%/100% and completed-day usage alerts; collection, storage and managed GeoIP failure/recovery alerts. |
| Why is history missing, and how can I share diagnostics? | Pruning reasons, remaining records and local redacted HTML/JSON evidence exports. |

You can also merge repeated alerts, set silences that expire automatically, fill missing daily reports, create database backups and compare detection thresholds using offline anonymized metadata.

## Install in one command

The installer targets **Debian 12/13** and **Fedora 43/44**, on **amd64/x86_64** and **arm64/aarch64**. Use a systemd host and a terminal with root or sudo access. This download command needs curl and working HTTPS certificates; the installer and apt/dnf handle the remaining installation dependencies. Go is not needed.

~~~bash
curl --proto '=https' --tlsv1.2 -fsSL https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.2/bootstrap.sh | sudo sh -s -- --version v0.4.0-alpha.2
~~~

Alternatively, download into a separate directory and inspect the script before deciding to execute it:

~~~bash
INSTALL_DIR=$(mktemp -d)
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.2/bootstrap.sh \
  -o "$INSTALL_DIR/bootstrap.sh"
less "$INSTALL_DIR/bootstrap.sh"
# Run separately, after reviewing and accepting the script:
sudo sh "$INSTALL_DIR/bootstrap.sh" --version v0.4.0-alpha.2
~~~

For manual package and provenance checks, see [release verification](docs/RELEASE_VERIFICATION.md#download-and-verify). HTTPS download, same-release SHA256 and GitHub attestation are distinct checks: bootstrap checks package checksums and identity but **does not automatically verify attestations**.

This downloads the package for your distribution and CPU, checks its SHA256 and package identity, installs it with your package manager, then opens setup. Existing configuration and service enable/disable choices are preserved. If the release is unavailable, installation stops with an explanation.

To install a downloaded and verified package manually, follow [the local package instructions](docs/V0.4_OPERATIONS.md#local-package-installation). Build targets do not imply that every distribution and ARM64 runtime has been tested; see [validation scope](docs/ALPHA_LIMITATIONS.md).

For unattended installation, append **--no-setup** and open setup later. No terminal answers or credentials are read from the script pipe. A fresh Debian package enables and starts observation with safe defaults, subject to system service policy; Fedora follows its service presets.

## First setup

Open the guided setup, or return to it at any time:

~~~bash
sudo noderampart setup
~~~

1. **Basic settings:** choose network interfaces, SSH monitoring, thresholds, timezone and report time. The defaults work without external accounts.
2. **Telegram, optional:** enter your bot token and chat ID. The token is hidden. Saving does not send a test message; choose **Send a test notification** separately.
3. **Local GeoIP, optional:** supply your own MaxMind Account ID and License Key, confirm that you have accepted its terms, then download the City and ASN databases. Daily updates are optional.
4. **Egress estimates, optional:** choose AWS or OCI, the appropriate region/group, and the monthly free allowance assigned to this host.
5. Choose **Start configured services**, then inspect **Current status**.

Use arrow keys and Enter for menus, Tab/Shift+Tab for form fields, and Escape to go back. English and Chinese are available from the language menu or the --language en / --language zh option.

## Everyday use

~~~bash
sudo noderampart tui
~~~

Closing the menu leaves the background services running.

| Menu | What you can do |
| --- | --- |
| Current status / Health and diagnosis | Check collection, interfaces, coverage and storage. |
| Configuration | Edit every configurable field, including advanced settings; validate and review before saving. |
| Reports | Read the current report, open saved daily reports and fill missing dates. |
| Events and incidents | Follow the timeline and inspect an incident. |
| Telegram and notifications | Configure delivery, inspect outcomes and manage expiring silences. |
| Local GeoIP databases | Download, refresh, inspect database age and enable/disable daily updates. |
| Cloud egress cost estimates | Fetch public prices, compare cached AWS/OCI scenarios or enter a custom tariff. |
| Backup, replay and privacy | Back up the database, verify backups and compare offline detection rules. |
| Services and uninstall | Start/stop/restart, recover a previous configuration or uninstall. |

Historical report details show the saved tariff and free allowance used at the
time. Incident details explain recorded alert thresholds and observations; the
alert status page distinguishes checks in progress, timeouts and pending writes.

## Alerts and offline evidence

In **Configuration**, enable budget or health alerts, set their thresholds,
validate and save. Both groups default to off. A monthly traffic allowance of
`107374182400` bytes (100 GiB) reminds at 80% and 100%; a cost budget also needs
an active billing profile.

**Health and diagnosis** shows alert status and the retention ledger. **Backup,
replay and privacy** exports local redacted evidence; incident details also offer
export with the selected incident. Files are never uploaded automatically.

~~~bash
sudo noderampart alerts status
sudo noderampart retention
sudo noderampart evidence export --output /root/noderampart-evidence.zip
~~~

Usage is selected-interface guest TX, which can include private or duplicated
traffic. It is an estimate, not a cloud invoice. A stopped host or unwritable
database cannot guarantee its own alert delivery. See [configuration, behavior
and evidence privacy](docs/ALERTS_EVIDENCE.md).

## Common configuration choices

Start with the defaults, inspect your traffic, then adjust thresholds for your server. High traffic is a signal to investigate, not proof of an attack.

| Setting | Default / example |
| --- | --- |
| Network interfaces | Follow IPv4/IPv6 default routes automatically; optionally select up to 8 interfaces. |
| SSH repeated failures | 8 failures within 5 minutes. |
| Port scan | 20 distinct ports within 1 minute. |
| Traffic thresholds | SYN 5,000/s; UDP 10,000/s; ICMP 2,000/s; bandwidth 100 MiB/s. |
| Report schedule | 09:00, host timezone. For example, choose Asia/Shanghai. |
| Repeated incident updates | Merge within a 10-minute window. |
| Optional services | Telegram, GeoIP downloads and egress estimates require setup. |
| IP privacy | Store/send network prefixes by default: IPv4 /24, IPv6 /48. |

The menu manages **/etc/noderampart/config.json**; a full example is [included here](configs/noderampart.json). It preserves advanced fields and checks the complete configuration. File/socket paths remain within the service's supported directories; changing a database path does not migrate existing history.

Experienced users can edit the file and validate it with **sudo noderampart config test**. See [configuration and recovery](docs/V0.4_OPERATIONS.md#configuration-and-recovery) before restarting services manually.

## Telegram and local GeoIP

Create your Telegram bot using [BotFather](https://t.me/BotFather), start a conversation with it or add it to the target group, then enter the token and target chat ID in the menu. NodeRampart stores the token in a restricted local file; it does not require it in command-line arguments. [Telegram setup details](docs/V0.4_OPERATIONS.md#telegram).

Identical verified GeoIP updates keep the active databases and services running without a configuration restart.

For GeoIP, obtain your own [MaxMind GeoLite account](https://www.maxmind.com/en/geolite2/signup) and accept the [GeoLite terms](https://www.maxmind.com/en/geolite/eula). The menu can then download both databases and optionally keep them updated. NodeRampart does not bundle GeoLite data or enroll on your behalf. Address lookups use the local databases; locations are approximate. You can skip GeoIP or use already licensed local MMDB files. [GeoIP details](docs/V0.4_OPERATIONS.md#local-geoip).

Saved reports, incidents and notifications open directly from their lists. Next/previous page controls keep your query period and cursors. Configuration review shows old and new values before saving.

## Understanding the cost estimate

This feature estimates **public Internet egress only**. It does not calculate instance, CPU, memory, disk, NAT Gateway, cross-zone traffic, taxes or your final invoice.

The menu shows official tariff sources, retrieval/effective dates, calculation units, shared allowance information, observed traffic and coverage. It fetches prices when you ask and retains the previous cache on download failure. An old cache is identified as such. No AWS or OCI credentials are needed.

Guest TX is not identical to billable Internet egress. Free allowances and pricing tiers may be shared with other services or hosts. Assign only this host's monthly share; the default is **zero**. The selectable bytes-per-GB assumption is displayed rather than treated as a verified provider meter. [Calculation details](docs/V0.4_OPERATIONS.md#egress-estimates).

## Upgrades and uninstall

NodeRampart does not update its own executable. Before an explicit package upgrade, create and verify a database backup, separately protect required configuration and credentials, and retain the previous package. Use the package manager or the bootstrap from the intended release. Schema 6 migrations are automatic; older binaries cannot open the migrated database. Configuration recovery does not downgrade data. Source installations require the documented [source-to-package transition](docs/V0.4_OPERATIONS.md#upgrades-and-removal), not installation over the source-owned files.

Open **Services and uninstall** in the menu:

- **Uninstall, keep configuration and data** stops services and removes the program. Configuration, credentials, history and caches remain.
- **Uninstall and delete all managed data** also removes the fixed application directories and service accounts. Back up needed records first.

The installed local removal helper provides the same choices for packages:

~~~bash
sudo /usr/libexec/noderampart/manage-remove
# Or, explicitly delete managed configuration, credentials and history:
sudo /usr/libexec/noderampart/manage-remove --purge
~~~

It uses apt/dnf without removing system dependencies. RPM may save edited configuration as config.json.rpmsave; reinstalling does not restore this file automatically. Review and restore needed settings yourself. Retaining files does not guarantee that old settings will be active. [Removal and upgrades](docs/V0.4_OPERATIONS.md#upgrades-and-removal).

## Troubleshooting

**Chinese appears as question marks:** `--language zh` selects the UI language,
not the terminal encoding. The published alpha.2 installer can pass `LC_ALL=C`
to setup. For a UTF-8 SSH client, check `LC_ALL=C.UTF-8 locale charmap`, then run
`sudo env LC_ALL=C.UTF-8 noderampart setup --language zh` (or replace `setup` with
`tui`). If that locale is unavailable, select a UTF-8 name from `locale -a`.
No Chinese language pack is required. sudo may reset locale variables; set the
locale for this command instead of using `sudo -E` or changing system defaults.
See [terminal encoding troubleshooting](docs/V0.4_OPERATIONS.md#terminal-encoding--终端编码)
for client/font checks and the distinction between the source fix and unchanged
alpha.2 packages.

~~~bash
sudo noderampart doctor
sudo noderampart status
sudo journalctl -u noderampartd -u noderampart-sensor --since today
~~~

For an SSH session without a terminal, allocate one with ssh -t, or use the existing noninteractive commands. Installation failures, missing GeoIP data and report coverage are explained in [operations](docs/V0.4_OPERATIONS.md). Event, backup and replay command references remain in [v0.3 operations](docs/V0.3_OPERATIONS.md).

New daily archives retain the full tariff, source, free allowance, byte unit and observed bytes used for their estimate. Backfilled reports use the tariff configured when generated; old archives are not re-priced. Database schema 6 migrates automatically; back up before upgrading because older binaries cannot open the migrated database.

Published packages will include a Go dependency SBOM and GitHub attestations; see [verify a release](docs/RELEASE_VERIFICATION.md).

## Security and alpha limits

The sensor uses AF_PACKET with `CAP_NET_RAW`; the daemon runs as a separate service identity. Setup and service/package management require root. No eBPF collector or automatic program updater is implemented. Keep your firewall and SSH access controls independently configured.

The build targets are Debian 12/13 and Fedora 43/44 on amd64/arm64. Cross-compiling or inspecting a package is not a runtime test. Earlier private Debian 13/Fedora 44 amd64 lab results apply only to those snapshots, not to this tag. Fresh installation/upgrade/removal acceptance for this prerelease and real ARM64 runtime validation are not claimed. See [remaining validation and functional limits](docs/ALPHA_LIMITATIONS.md) and [security reporting](SECURITY.md). Successful scans or valid attestations do not prove absence of vulnerabilities.

## Development and license

Contributors: [development guide](docs/DEVELOPMENT.md), [architecture](docs/ARCHITECTURE.md), [threat model](docs/THREAT_MODEL.md), [contributing](CONTRIBUTING.md). Ordinary tests do not need packet-capture privileges; privileged checks belong in disposable lab VMs.

NodeRampart's original code is [MIT licensed](LICENSE). Bundled dependencies retain their licenses; see [third-party notices](THIRD_PARTY_NOTICES.md). MaxMind data has separate licensing.
