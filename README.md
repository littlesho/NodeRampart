# NodeRampart

**See what is happening on your Linux VPS: suspicious traffic, SSH logins, daily usage and estimated Internet egress costs.**

NodeRampart watches your server in the background. It records events, builds daily reports and can send alerts, recovery notices and daily summaries through multiple notification channels. A terminal menu guides you through setup and everyday management over SSH.

[中文说明](README.zh-CN.md) · [Detailed operations](docs/V0.4_OPERATIONS.md) · [Security](SECURITY.md) · [Limitations](docs/ALPHA_LIMITATIONS.md)

## Version status and validation scope

<!-- current-release:start -->
**[GitHub Latest: v0.4.0-alpha.11](https://github.com/littlesho/NodeRampart/releases/latest)** is the newest published product version and an ordinary GitHub Release (`draft=false`, `prerelease=false`). Product maturity remains **Alpha**, with the validation gaps below. This version fixes GeoIP daily-update state readback, updates dependencies and Go build requirements, and improves journal and terminal regression tests.
<!-- current-release:end -->

| Validation | Result and scope |
| --- | --- |
| Debian 12/13 and Fedora 43/44, x86_64 | **PASS:** first install, basic SQLite, English/Chinese TUI/setup NO readback, help and cancel with the published packages. |
| DEB/RPM lifecycle | **PASS:** applicable upgrades/reinstalls, normal service cycles and keep-data removal; purge checked on Debian 12 and Fedora 43. |
| Debian 12 durable journal acknowledgement (ACK) | **PASS:** normal trusted OpenSSH events and one normal service cycle. |
| Release downloads and supply-chain verification | **PASS:** 22 anonymous asset downloads, 21 checksum entries, 22 provenance bindings and six runtime-package SPDX bindings. |

**USER-REPORTED PASS:** the maintainer reports anonymous VPS download/upgrade, retesting the original bug, licensed MaxMind City/ASN downloads, and GeoIP **YES save → full exit → new-process YES → systemd timer enabled**. Independent automated VPS logs were not collected.

**NOT RUN:** Fedora 43/44 native durable ACK, complete journal fault recovery, native ARM64, long soak, actual third-party notification delivery and full independent vulnerability scans of the 18 hosted programs. Stable and production readiness remain unestablished. See the [current promotion and validation record](docs/RELEASE_VERIFICATION.md#alpha11-ordinary-release-and-latest-promotion) and [Alpha limitations](docs/ALPHA_LIMITATIONS.md) for retained failures and evidence boundaries.

Installation examples follow the newest published release. [Historical releases](https://github.com/littlesho/NodeRampart/releases), [release verification](docs/RELEASE_VERIFICATION.md) and [CHANGELOG](CHANGELOG.md) retain earlier versions and their original results.

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

### Product scope

NodeRampart observes network metadata and SSH events, stores local reports and sends configured notifications. It leaves firewall rules unchanged, does not block IPs or inspect application payloads, and opens no web dashboard port. DDoS mitigation and traffic scrubbing require separate services.

<a id="install-a-pinned-release"></a>

## Install the newest published release

The installer targets **Debian 12/13** and **Fedora 43/44**, on **amd64/x86_64** and **arm64/aarch64**. Use a systemd host and a terminal with root or sudo access. The maintained entry needs curl, working HTTPS certificates, Python 3 and `prlimit` (util-linux) for bounded JSON parsing. These are installer dependencies; the daemon gains no Python dependency. Go, Node, Docker and GitHub CLI are unnecessary for installation. ARM64 packages are available; see the validation scope above. Ubuntu is outside the maintained installer's supported host list.

The maintained entry resolves the highest published product version, including alpha/beta/RC prereleases, when `--version` is omitted. Download into a separate directory and inspect the script:

~~~bash
INSTALL_DIR=$(mktemp -d)
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/littlesho/NodeRampart/main/scripts/bootstrap.sh \
  -o "$INSTALL_DIR/bootstrap.sh"
less "$INSTALL_DIR/bootstrap.sh"
# Run separately, after reviewing and accepting the script:
sudo sh "$INSTALL_DIR/bootstrap.sh" --no-setup
sudo noderampart setup
~~~

The installer prints and fixes one selected Release for the whole invocation, verifies that Release's bootstrap against its SHA256SUMS, and passes the explicit selected version to it. Packages come from the published Release. Development `VERSION` and main-source binaries are not installation targets. A missing platform package, API limit, incomplete response or failed download/check fails the installation; it never falls back to an older release. Selection happens only when you actively run the installer; running daemons do not auto-upgrade.

### Pin a reproducible release

<!-- current-release:start -->
Use `--version v0.4.0-alpha.11` to pin the current published release exactly. A pin does not query the default Release list or silently substitute another version. The alpha.11 bootstrap is a frozen source snapshot whose default can follow later published versions. **Pass the version explicitly** to keep this asset installation pinned to alpha.11:

~~~bash
INSTALL_DIR=$(mktemp -d)
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.11/bootstrap.sh \
  -o "$INSTALL_DIR/bootstrap.sh"
less "$INSTALL_DIR/bootstrap.sh"
sudo sh "$INSTALL_DIR/bootstrap.sh" --version v0.4.0-alpha.11 --no-setup
sudo noderampart setup
~~~
<!-- current-release:end -->

For manual package and provenance checks, see [release verification](docs/RELEASE_VERIFICATION.md#download-and-verify). Bootstrap checks SHA256 and package version/architecture; **attestation verification is a separate step**. Existing source-install conflicts, unsupported platforms and downgrades are refused. Configuration and service enable/disable choices are preserved.

**--no-setup** permits unattended installation; open setup separately afterward. Without it, setup uses a controlling terminal, independently of any script pipe; terminal answers and credentials are never read from that pipe. A fresh Debian package enables and starts observation with safe defaults, subject to system service policy; Fedora follows its service presets.

## First setup

Open the guided setup, or return to it at any time:

~~~bash
sudo noderampart setup
~~~

1. **Basic settings:** choose network interfaces, SSH monitoring, thresholds, timezone and report time. The defaults work without external accounts.
2. **Notification channels (optional):** choose a channel and follow its protected credential instructions; secret inputs are hidden. Basic setup works without a notification account. See [notification channels](#notification-channels) for saving, testing and paid-channel confirmation rules.
3. **Local GeoIP (optional):** download licensed City and ASN databases and choose whether to enable daily updates. Follow the [GeoIP setup and verification guide](#local-geoip).
4. **Egress estimates, optional:** choose AWS or OCI, the appropriate region/group, and the monthly free allowance assigned to this host.
5. Choose **Start configured services**, then inspect **Current status**.

Use arrow keys and Enter for menus, Tab/Shift+Tab for form fields, and Escape to go back. English and Chinese are available from the language menu or the --language en / --language zh option.

The report timezone selector works offline. Notification language is separate from UI language; changing either does not translate old queued messages. See [timezone and Telegram language](docs/V0.4_OPERATIONS.md#timezone-selector-and-telegram-language) and the channel-specific guides below. From this setup page, **Back** / Escape returns to setup; from `tui`, it returns to the main menu.

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
| Notification channels | Reach all 12 published channels, inspect independent delivery outcomes and messages, choose a single test target and manage expiring silences. |
| Local GeoIP databases | Download, refresh, inspect database age and enable/disable daily updates. |
| Cloud egress cost estimates | Fetch public prices, compare cached AWS/OCI scenarios or enter a custom tariff. |
| Backup, replay and privacy | Back up the database, verify backups and compare offline detection rules. |
| Services and uninstall | Start/stop/restart, recover a previous configuration or uninstall. |

Historical report details show the saved tariff and free allowance used at the
time. Incident details explain recorded alert thresholds and observations; the
alert status page distinguishes checks in progress, timeouts and pending writes.

Reports, incidents and notifications open directly from their lists; paging preserves the query period and cursors. Configuration review shows old and new values before saving.

## Notification channels

The published release provides one shared entry: **setup → Notification channels (optional)** or **tui → Notification channels**. Choose the existing channel form or configuration editor there; channel-specific credentials and delivery rules remain separate. All channels are optional and default disabled.

<a id="native-channels-in-the-alpha8-prerelease"></a>

| Channel / identifier | Sending method | Configuration and guide | Version range |
| --- | --- | --- | --- |
| Telegram / `telegram` | Bot API text to one chat | Telegram setup · [operations](docs/V0.4_OPERATIONS.md#telegram) | Included in the current release |
| Generic HTTPS Webhook / `webhook` | Fixed receiver, JSON and Bearer credential | Notification configuration · [operations](docs/V0.4_OPERATIONS.md#fixed-https-heartbeat-and-webhook) | Included in the current release |
| Feishu / `feishu` | Custom group-bot Webhook, optional signing | Feishu setup · [guide](docs/NOTIFICATION_CHANNELS.md#feishu) | Published alpha.8 and later |
| WeCom / `wecom` | Group-bot Webhook | WeCom setup · [guide](docs/NOTIFICATION_CHANNELS.md#wecom) | Published alpha.8 and later |
| Discord / `discord` | Channel Incoming Webhook | Discord setup · [guide](docs/NOTIFICATION_CHANNELS.md#discord) | Published alpha.8 and later |
| Slack / `slack` | Slack App Incoming Webhook | Slack setup · [guide](docs/NOTIFICATION_CHANNELS.md#slack) | Published alpha.8 and later |
| Microsoft Teams Workflows / `teams` | Supported Adaptive Card workflow, request acceptance only | Teams setup · [guide](docs/NOTIFICATION_CHANNELS.md#microsoft-teams-workflows) | Published alpha.8 and later |
| Google Chat / `google_chat` | Space Incoming Webhook | Google Chat setup · [guide](docs/NOTIFICATION_CHANNELS.md#google-chat) | Published alpha.8 and later |
| QQ Bot / `qqbot` | Official active C2C/group text with authorized platform IDs | QQ Bot actions · [guide](docs/OFFICIAL_NOTIFICATION_CHANNELS.md#qq-bot-active-c2c-or-group-messages) | Published alpha.9 and later |
| LINE Messaging API Push / `line` | Official Account push to an authorized user/group/room | LINE actions · [guide](docs/OFFICIAL_NOTIFICATION_CHANNELS.md#line-official-account-messaging-api-push) | Published alpha.9 and later |
| Twilio SMS / `twilio_sms` | Programmable Messaging SMS to one E.164 recipient | Twilio SMS actions · [guide](docs/OFFICIAL_NOTIFICATION_CHANNELS.md#twilio-sms-individual-consent-and-finite-segment-budget) | Published alpha.9 and later |
| WhatsApp Cloud API / `whatsapp_cloud` | Meta Cloud API approved templates to one recipient | WhatsApp actions · [guide](docs/OFFICIAL_NOTIFICATION_CHANNELS.md#whatsapp-cloud-approved-body-templates-only) | Published alpha.9 and later |

The channels send eligible events, start/update/recovery notices and short daily summaries using the durable outbox. Telegram, the six native Webhook channels and four account channels have independent English/Simplified Chinese choices; the generic Webhook retains its existing English JSON contract. WhatsApp requires approved templates in the chosen language, and SMS uses a compact summary with a segment limit. Paid daily summaries default off. Language/timezone changes do not rewrite queued content.

Browsing and local previews do not call the platform or send a message. Saving does not automatically validate with the platform or queue/send a test; the enabled daemon can deliver eligible notifications after settings apply. Explicit tests select one target; paid tests additionally require a current preview and cost confirmation. Operator account/admin authorization and recipient consent remain necessary, with finite persistent limits for paid channels. Platform acceptance does not prove delivery or reading; real platform, human-receipt and fee tests remain **NOT RUN**. Teams confirms only workflow request acceptance. Native Webhook/account transports connect directly without an environment proxy.

See the [six-channel guide](docs/NOTIFICATION_CHANNELS.md), [four account-channel guide](docs/OFFICIAL_NOTIFICATION_CHANNELS.md), [privacy](docs/PRIVACY.md), [user agreement](docs/USER_AGREEMENT.md) and [limits](docs/ALPHA_LIMITATIONS.md) for prerequisites, platform terms, retry/unknown-delivery behavior and costs. Heartbeat, local GeoIP and cloud egress estimates are separate features.

<a id="telegram-and-local-geoip"></a>

### Telegram setup

Create your own bot using [BotFather](https://t.me/BotFather), start a conversation with it or add it to the intended group, then select **Set up Telegram** from Notification channels. Enter the token privately and a numeric chat ID. The token is stored in a restricted local file and does not belong in CLI arguments. [Full setup and target-isolation rules](docs/V0.4_OPERATIONS.md#telegram).

Changing bot/chat or tightening privacy isolates older unsent messages; switching back does not adopt them. Same-target token rotation keeps retry/cooldown state. Inspect **Notification messages** before explicitly discarding isolated bodies. An in-flight request may finish at its original receiver; isolated unsent messages keep their seven-day expiry.

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
| Optional services | Notification channels, GeoIP downloads and egress estimates require separate setup. |
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

HTTPS heartbeat is an optional health-reporting feature alongside notification channels. It distinguishes process liveness from functional degradation and opens no listener. [Heartbeat configuration](docs/V0.4_OPERATIONS.md#fixed-https-heartbeat-and-webhook).

<a id="local-geoip"></a>
<a id="vps-pre-release-acceptance"></a>
<a id="vps-acceptance-and-local-geoip-verification"></a>

## Local GeoIP setup and verification

GeoIP adds approximate country, city and ASN information through local databases. You can use already licensed MMDB files or download City and ASN through setup with your own [MaxMind GeoLite account](https://www.maxmind.com/en/geolite2/signup). Accept the [GeoLite terms](https://www.maxmind.com/en/geolite/eula) yourself; NodeRampart does not bundle the data or enroll on your behalf. GeoIP is optional.

1. Run `sudo noderampart version` and `sudo noderampart status` to check the installed program, services and collection. For a pinned alpha.11 installation, expect version `0.4.0-alpha.11` and source commit `2c9d4416adef3cb64e0523a1b9ac1e691b121c16`.
2. In `sudo noderampart setup`, choose **Download local GeoIP (optional)**; in `sudo noderampart tui`, choose **Local GeoIP databases → Set up local GeoIP downloads**. Enter your own Account ID and License Key in the protected form, confirm the license terms, and download City and ASN. Inspect the update result and database age. Keep credentials out of chat, recordings and reports.
3. To enable daily updates, open **tui → Local GeoIP databases → Daily GeoIP updates**, save **YES**, fully exit and reopen it in a new process. It should still show YES. Compare it with these read-only systemd queries:

   ~~~bash
   sudo systemctl show noderampart-geoip-update.timer \
     --property=LoadState,UnitFileState,ActiveState,SubState,NextElapseUSecRealtime
   sudo systemctl list-timers --all noderampart-geoip-update.timer
   ~~~

4. To disable daily updates, save **NO**, exit and reopen the form to confirm NO. Inspect the timer again and record the actual result if you are collecting diagnostic evidence.

YES reflects `UnitFileState=enabled` or `enabled-runtime`, even if the timer is inactive or failed. Check updater health and download success separately. Read errors are reported as errors, not NO. Enabling this persistent timer can trigger a missed update immediately.

Identical verified downloads keep the active databases and services running without a configuration restart. See [GeoIP operations](docs/V0.4_OPERATIONS.md#local-geoip) for database paths and update behavior; the [validation record](docs/RELEASE_VERIFICATION.md#alpha11-ordinary-release-and-latest-promotion) distinguishes lab checks from the maintainer's VPS report.

## Understanding the cost estimate

This feature estimates **public Internet egress only**. It does not calculate instance, CPU, memory, disk, NAT Gateway, cross-zone traffic, taxes or your final invoice.

The menu shows official tariff sources, retrieval/effective dates, calculation units, shared allowance information, observed traffic and coverage. It fetches prices when you ask and retains the previous cache on download failure. An old cache is identified as such. No AWS or OCI credentials are needed.

Guest TX is not identical to billable Internet egress. Free allowances and pricing tiers may be shared with other services or hosts. Assign only this host's monthly share; the default is **zero**. The selectable bytes-per-GB value is an explicit calculation assumption. [Calculation details](docs/V0.4_OPERATIONS.md#egress-estimates).

## Full local reports, trends and cycle forecasts

These commands read the local daemon. New daily snapshots preserve an independent full document; notifications use channel-specific short summaries or approved templates rather than sending the full local report. Original snapshots stay immutable. Older snapshots retain their short body and explicitly report that original full content is unavailable.

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

Daily archives retain the tariff, source, free allowance, byte unit and observations used for their estimate. Backfills use the configuration at generation; saved archives are not re-priced. Back up the database and preserve matching configuration/credentials before upgrading; old binaries cannot open a newer schema. See [upgrade and rollback](docs/V0.4_OPERATIONS.md#upgrades-and-removal) and [historical release verification](docs/RELEASE_VERIFICATION.md). Packaged README/license files stay with their frozen release source; later documentation does not replace those bytes or install a complete offline manual.

Contributors: [development guide](docs/DEVELOPMENT.md), [architecture](docs/ARCHITECTURE.md), [threat model](docs/THREAT_MODEL.md), [contributing](CONTRIBUTING.md). Ordinary tests do not need packet-capture privileges; privileged checks belong in disposable lab VMs.

NodeRampart's original code is [MIT licensed](LICENSE). Bundled dependencies retain their licenses; see [third-party notices](THIRD_PARTY_NOTICES.md). MaxMind data has separate licensing.
