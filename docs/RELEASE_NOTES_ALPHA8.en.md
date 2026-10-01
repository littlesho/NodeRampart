# Proposed release notes — NodeRampart v0.4.0-alpha.8

**Unpublished draft text.** This file is not a GitHub draft/Release, release date,
asset claim or authorization to publish. Final tag/source, hosted build and
attestation identities will be supplied only by separately authorized stages.

Alpha.8 adds six default-disabled native outgoing channels: Feishu custom group
robots (optional request signature), WeCom group robots, Discord Incoming Webhook,
Slack App Incoming Webhook, Microsoft Teams Workflows and Google Chat Space Webhook.
Each has one protected-file target and independent en/zh event, recovery, test and
daily-summary presentation, alongside existing Telegram and generic Webhook.

The unified notification TUI uses hidden credential input and the existing reviewed
management transaction. Persistent outbox, immutable credential snapshots,
recipient/privacy isolation, bounded quotas and restart-safe retries/pacing remain.
Official HTTPS URL/DNS/peer checks, verified TLS, no redirects and bounded payloads
apply. Native channels connect directly without environment proxy. Settings save
and status viewing do not send. Six real APIs and human confirmation are NOT RUN.

Project `0.4.0-alpha.8`, DEB `0.4.0~alpha.8`, RPM
`0.4.0-0.alpha.9.fc43/fc44`; config/control API1 and sensor protocol5 are unchanged.
DB schema13 upgrades schema12 transactionally. Retain matching old database,
configuration and credentials before upgrade; rollback is offline restoration,
not in-place schema downgrade. Purge refuses unowned files but direct package
purge failure does not promise atomic conffile rollback.

Source bootstrap supports explicit `v0.4.0-alpha.8` with strict package/checksum
identity. These assets are not available publicly until publication. Default
remains alpha.5 and public examples remain verified alpha.7. No auto-downgrade or
fallback on 404 is provided. Standalone/DEB programs are loader-free static;
RPMs retain PIE/system-loader behavior. ARM64 is cross-built; native runtime is
NOT RUN. Local candidate results are not hosted release provenance or public
bootstrap acceptance.

Teams supports the selected administrator-permitted Anyone secret-URL Adaptive
Card workflow and confirms request acceptance only; OAuth/Entra-only modes are
unsupported. Slack service/distribution terms and administrator/message permission
are separate from MIT; no Marketplace/vendor approval is claimed. Operators create
and authorize their own Webhooks; no account, payment or terms are accepted for them.

The fixed dependency graph includes x/text v0.21.0, with GO-2026-5970 in unicode/norm,
fixed at v0.39.0. Final-source/product-package import and reachability results must
be read in the external frozen preflight evidence; neither prior results nor scanner
exit0 establish zero vulnerabilities or maintainer risk acceptance.

See [preflight stages and boundaries](ALPHA8_RELEASE_PREFLIGHT.md),
[feature acceptance](ALPHA8_ACCEPTANCE.md), [channel guide](NOTIFICATION_CHANNELS.md),
[privacy](PRIVACY.md) and [user agreement](USER_AGREEMENT.md). This remains an alpha,
not a claim of production readiness, end-to-end delivery or exactly-once messages.
