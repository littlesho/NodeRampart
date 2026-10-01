# NodeRampart

**See what is happening on your Linux VPS: suspicious traffic, SSH logins, daily usage and estimated Internet egress costs.**

NodeRampart watches your server in the background. It records events, builds daily reports and can notify you through configured notification channels. A terminal menu guides you through setup and everyday management over SSH.

It observes and reports. It does not block IP addresses, change your firewall, inspect application payloads or open a web dashboard port. It is not DDoS mitigation or a traffic-scrubbing service.

[中文说明](README.zh-CN.md) · [Detailed operations](docs/V0.4_OPERATIONS.md) · [Security](SECURITY.md) · [Limitations](docs/ALPHA_LIMITATIONS.md)

> **Source development: 0.4.0-alpha.8 (Unreleased).** Six native channels are disabled by default; [setup and platform permissions](docs/NOTIFICATION_CHANNELS.md), [actual acceptance](docs/ALPHA8_ACCEPTANCE.md), [privacy](docs/PRIVACY.md) and [user agreement](docs/USER_AGREEMENT.md). The published installation instructions below remain alpha.7.

> **Release-entry preflight:** candidate source supports explicit alpha.8 only for assets after publication; no-argument default remains alpha.5. The [three-stage preflight](docs/ALPHA8_RELEASE_PREFLIGHT.md) and [proposed notes](docs/RELEASE_NOTES_ALPHA8.en.md) are not a public release or hosted provenance.

> **v0.4.0-alpha.7 — published alpha prerelease.** The [public packages](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.7) add a searchable report timezone and independent English/Chinese Telegram text. The database upgrades to schema 12; verify a matching backup, preserve earlier configuration/keys separately and install all three matching programs before saving new settings.

> **This distribution:** All 22 anonymous downloads and checksums match the authenticated 22 provenance and six runtime SPDX subjects byte-for-byte. Debian 13/Fedora 44 public bootstrap, setup, basic collection, online backup and cleanup: **PASS**. Both strict doctor snapshots remained **unknown / exit 2**. Current alpha.7 Debian 12/Fedora 43 runtime, native ARM64 and natural SSH recovery remain unvalidated; this is not production readiness. See [this release verification](docs/RELEASE_VERIFICATION.md#alpha7-publication-and-distribution-verification).

The following alpha.6 scope is historical and does not prove alpha.7 runtime acceptance:

> **v0.4.0-alpha.6 — published alpha prerelease.** The [public packages](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.6) add isolated notification targets, full local reports, diagnosis and committed sensor watermarks. The database upgrades to schema 11 and the sensor protocol to v5; retain a verified compatible pre-upgrade backup.

> **Validation:** All 22 assets passed fresh anonymous download/checksum checks; their bytes match the authenticated 22 provenance and six SPDX checks. Exact final-package upgrades and lifecycle checks passed on Debian 12/13 and Fedora 43/44 amd64/x86_64. Public bootstrap with separate setup passed on Debian 13 and Fedora 44. Native ARM64 and the natural SSH recovery chain remain unvalidated; this does not establish production readiness. See [release verification](docs/RELEASE_VERIFICATION.md#alpha6-publication-and-distribution-verification).

> **GeoIP fix scope:** alpha.4 fixes repeated parsing of shared MMDB data that could exhaust the validation budget, and adds safe MMDB validation/resource-budget errors. Matching candidate City/ASN samples passed complete offline validation; user download, activation and daily updates remain unverified. See [validation scope and resource limits](docs/ALPHA_LIMITATIONS.md#geoip-alpha4-validation) and [upgrade and GeoIP acceptance](docs/V0.4_OPERATIONS.md#alpha4-upgrade-and-geoip-acceptance).

> **Pinned installation:** explicitly select `v0.4.0-alpha.7` below. The frozen bootstrap still defaults to alpha.5 when `--version` is omitted. The [current acceptance](docs/RELEASE_VERIFICATION.md#alpha7-publication-and-distribution-verification) separates measured results from remaining limits.

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

## Install a pinned release

The installer targets **Debian 12/13** and **Fedora 43/44**, on **amd64/x86_64** and **arm64/aarch64**. Use a systemd host and a terminal with root or sudo access. The download command needs curl and working HTTPS certificates; the installer and apt/dnf handle the remaining installation dependencies. Go is not needed. ARM64 packages are available but native ARM64 execution is not yet validated.

Download into a separate directory and inspect the script before deciding to execute it:

~~~bash
INSTALL_DIR=$(mktemp -d)
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.7/bootstrap.sh \
  -o "$INSTALL_DIR/bootstrap.sh"
less "$INSTALL_DIR/bootstrap.sh"
# Run separately, after reviewing and accepting the script:
sudo sh "$INSTALL_DIR/bootstrap.sh" --version v0.4.0-alpha.7 --no-setup
sudo noderampart setup
~~~

For manual package and provenance checks, see [release verification](docs/RELEASE_VERIFICATION.md#download-and-verify). HTTPS download, same-release SHA256 and GitHub attestation are distinct checks: bootstrap checks package checksums and identity but **does not automatically verify attestations**.

The installer downloads the package for your distribution and CPU, checks its SHA256 and package identity, and installs it with your package manager. `--no-setup` leaves the menu to the separate setup command. Existing configuration and service enable/disable choices are preserved. If the release is unavailable, installation stops with an explanation.

The explicit alpha.7 download, `--no-setup` installation and separate setup passed on Debian 13 and Fedora 44. The supported curl-pipe interactive path was not exercised in this publication check. Omitting `--version` still selects alpha.5. Build targets do not imply that every distribution and ARM64 runtime has been tested; see [validation scope](docs/ALPHA_LIMITATIONS.md).

**--no-setup** permits unattended installation; open setup separately afterward. No terminal answers or credentials are read from the script pipe. A fresh Debian package enables and starts observation with safe defaults, subject to system service policy; Fedora follows its service presets.

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

Alpha.7 adds a searchable region/city timezone selector and independent Telegram
**English / 简体中文** text, defaulting to English regardless of UI language. Only
newly admitted messages use changed presentation settings; old bodies and retries
are not translated again. See the bilingual [timezone and message language
instructions](docs/V0.4_OPERATIONS.md#timezone-selector-and-telegram-language).

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
| Notification channels | Configure Telegram, generic Webhook and six native channels; inspect independent outcomes and expiring silences. |
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

### Sampling and component upgrades

The configured sampling period remains 100 ms–1 minute. Protocol 5 preserves
the actual elapsed time, allowing bounded scheduling jitter: 10% of the period,
at least 250 ms and at most 5 seconds. Thus a 60001 ms observation at a one-minute
period is valid; longer pauses discard detail, retain loss counters and establish
a new baseline. Missing observations are not replayed into a later rate window.

The daemon permits one bounded round of up to eight interface frames together,
then retains its sustained read limit. All interface writes in a round share a
250 ms transport deadline. Upgrade the daemon before the sensor, or stop and
replace both: this daemon accepts versions 1–5, while older daemons reject version
5. Versions 1–4 and unsequenced offline captures have no persistence confirmation.

The sensor assigns each interface a bounded session and batch sequence. Status
shows the actual committed watermark; socket writes alone do not advance it.
Collector health, traffic aggregation and that watermark commit in one SQLite
transaction. The acknowledgment separately identifies whether derived events
and notification admission decisions were persisted; a full queue or pending
event remains partial. It never confirms delivery to an external receiver.
Duplicate sequences do not add traffic or health again. Sequence and observation
gaps remain visible. This version has no disk spool or historical batch replay;
sensor restart and uncommitted flow details can still leave coverage gaps.

### Optional SSH history hints

The Auth menu's **SSH history hints** option (`auth.history_hints_enabled`) is off
by default. After seven continuously covered days in the current daemon process,
it compares successful logins with up to 1,000 retained successes, requiring at
least 20 observations on three local dates. It can mark a first observed source
(a first observed prefix in prefix privacy mode) or an unseen local hour.
These are deviations from retained observations, not evidence of intrusion.
Missing, pruned or overloaded history suppresses hints. Restarting after a
configuration or privacy-key change starts a new observation period. Existing
privacy settings apply; no additional raw addresses or baseline are stored.

HTTPS heartbeat and a single generic JSON Webhook are also available, disabled by default. Configure fixed targets and protected bearer files in Notifications; no port is opened. Heartbeat separates process liveness from functional degradation, while Webhook shares the isolated outbox. [Configuration, identity changes and retry bounds](docs/V0.4_OPERATIONS.md#fixed-https-heartbeat-and-webhook).

## Native channels in the alpha.8 source candidate

Feishu, WeCom, Discord, Slack, Teams Workflows and Google Chat each support one
independent target and message language. Configure hidden credentials in
**Notification channels**, review/apply, then explicitly select one test target.
Eligible events/recoveries and short daily summaries use the existing durable
outbox; no incoming commands, listeners or SDK runtime. Read the
[English/Chinese operations](docs/NOTIFICATION_CHANNELS.md) for administrator
requirements, Teams' acceptance boundary, Slack distribution terms, rotation
and rollback. Real-platform tests require your separately authorized webhook.
These are candidate source features, not downloadable alpha.8 release assets.

## Telegram and local GeoIP

Create your Telegram bot using [BotFather](https://t.me/BotFather), start a conversation with it or add it to the target group, then enter the token and target chat ID in the menu. NodeRampart stores the token in a restricted local file; it does not require it in command-line arguments. [Telegram setup details](docs/V0.4_OPERATIONS.md#telegram).

Use a numeric chat ID. Messages are bound to the bot/chat identity without storing the token in the database. Changing bot/chat or tightening notification privacy retains older unsent messages in isolation; switching back does not adopt them. Same-target token rotation keeps retry/cooldown state, and disabling delivery pauses a known same-target queue. Inspect **Notification messages** and explicitly choose **Discard isolated notification bodies** if those bodies are no longer needed. An in-flight request may finish at its original receiver. Isolated unsent messages retain the existing seven-day expiry.

Identical verified GeoIP updates keep the active databases and services running without a configuration restart.

For GeoIP, obtain your own [MaxMind GeoLite account](https://www.maxmind.com/en/geolite2/signup) and accept the [GeoLite terms](https://www.maxmind.com/en/geolite/eula). The menu can then download both databases and optionally keep them updated. NodeRampart does not bundle GeoLite data or enroll on your behalf. Address lookups use the local databases; locations are approximate. You can skip GeoIP or use already licensed local MMDB files. [GeoIP details](docs/V0.4_OPERATIONS.md#local-geoip).

Saved reports, incidents and notifications open directly from their lists. Next/previous page controls keep your query period and cursors. Configuration review shows old and new values before saving.

## Understanding the cost estimate

This feature estimates **public Internet egress only**. It does not calculate instance, CPU, memory, disk, NAT Gateway, cross-zone traffic, taxes or your final invoice.

The menu shows official tariff sources, retrieval/effective dates, calculation units, shared allowance information, observed traffic and coverage. It fetches prices when you ask and retains the previous cache on download failure. An old cache is identified as such. No AWS or OCI credentials are needed.

Guest TX is not identical to billable Internet egress. Free allowances and pricing tiers may be shared with other services or hosts. Assign only this host's monthly share; the default is **zero**. The selectable bytes-per-GB assumption is displayed rather than treated as a verified provider meter. [Calculation details](docs/V0.4_OPERATIONS.md#egress-estimates).

## Full local reports, trends and cycle forecasts

These commands read the local daemon. New daily snapshots preserve an independent
full document; Telegram and Webhook receive a short numeric summary with source
identifiers omitted. Original snapshots stay immutable. Older snapshots retain
their short body and explicitly report that original full content is unavailable.

~~~bash
sudo noderampart report export --date 2026-09-29 --format html
sudo noderampart report show --date 2026-09-29 --format json
sudo noderampart report trend --days 7
sudo noderampart report trend --days 30
sudo noderampart report forecast
~~~

HTML goes to stdout and is static, self-contained and escaped, with no external
scripts or assets. Full bodies are limited to 128 KiB, structured documents to
256 KiB, displayed attribution to at most 50 entries and construction to 20
seconds. The TUI displays full bodies up to 32 KiB and explicitly uses its short
preview for larger documents; CLI export retains the saved full content.

Trends distinguish complete, partial, missing and pruned days. Missing totals
are null. Complete days need full recorded counter coverage and every overlapping
UTC-hour row; this does not prove a provider bill or lossless capture. Forecasts
require complete current-cycle data and 7 or 30 complete historical days. They
show simple rate scenarios, projected usage/cost ranges and possible threshold
dates, not statistical confidence intervals. `billing.cycle_start_day` accepts
1–28, defaults to 1 and uses the report timezone for usage, free allowance,
budget thresholds, pricing previews and forecasts. A cycle change records the
previous cycle as changed rather than reusing its alert milestone. Guest TX and
the configured tariff retain the existing estimate limitations.

## Local diagnostics and textfile monitoring

~~~bash
sudo noderampart doctor --strict --config /etc/noderampart/config.json
DIAG_DIR=$(sudo mktemp -d)
sudo noderampart metrics export --output "$DIAG_DIR/noderampart.prom"
~~~

Strict doctor emits JSON with stable reasons, impact and next steps: exit 0 means
required checks are normal, 1 confirms a problem/degradation and 2 means the
diagnosis is unknown or failed. Disabled checks are not failures; unknown is not
healthy. The existing non-strict doctor's exit behavior is preserved. `status`
also includes the diagnosis. No command performs automatic repairs.

Textfile export opens no port and installs no service or timer. It atomically
publishes fixed component labels, generation time, five-minute validity and
collection success. A failed collection replaces old healthy output with a
failure marker. Consumers must reject expired output even if its old success
flag is 1. New files are 0600; existing safe 0640 permissions are preserved.
Grant an existing collector read access deliberately. No IP, event ID, secret
URL or arbitrary error becomes a label.

## Uninstall

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

## Troubleshooting and development

~~~bash
sudo noderampart doctor
sudo noderampart status
sudo journalctl -u noderampartd -u noderampart-sensor --since today
~~~

For an SSH session without a terminal, allocate one with ssh -t, or use the existing noninteractive commands. Installation failures, missing GeoIP data and report coverage are explained in [operations](docs/V0.4_OPERATIONS.md). Event, backup and replay command references remain in [v0.3 operations](docs/V0.3_OPERATIONS.md).

`upgrade preflight` checks an explicit backup, configuration, keys, disk and local package metadata without upgrading; `upgrade rehearse` validates a temporary restored copy and cleans it afterward. Target schema compatibility remains unknown without verified target information. `threshold preview` compares current/candidate rules offline; the TUI connects draft preview to its existing confirmed save. Bounded local `threshold feedback` labels do not train or tune rules. See [operation examples and limits](docs/V0.4_OPERATIONS.md#upgrade-preflight-and-restore-rehearsal).

New daily archives retain the full tariff, source, free allowance, byte unit and observed bytes used for their estimate. Backfilled reports use the tariff configured when generated; old archives are not re-priced. Alpha.6 migrates to database schema 11, preserving public schema 7 journal recovery while adding target isolation, full report documents, sensor commit watermarks and separate per-channel delivery decisions. Back up before upgrading because older binaries cannot open the migrated database. Legacy channel-only Telegram messages remain isolated rather than being assigned to the current receiver.

Published alpha.6 packages include a Go dependency SBOM and matching GitHub attestations; see [verify a release](docs/RELEASE_VERIFICATION.md). Packaged README/license files remain the frozen release-source snapshot; later documentation updates do not replace package bytes or install a complete offline manual.

Contributors: [development guide](docs/DEVELOPMENT.md), [architecture](docs/ARCHITECTURE.md), [threat model](docs/THREAT_MODEL.md), [contributing](CONTRIBUTING.md). Ordinary tests do not need packet-capture privileges; privileged checks belong in disposable lab VMs.

NodeRampart's original code is [MIT licensed](LICENSE). Bundled dependencies retain their licenses; see [third-party notices](THIRD_PARTY_NOTICES.md). MaxMind data has separate licensing.
