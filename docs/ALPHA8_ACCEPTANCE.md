# alpha.8 acceptance — Unreleased / development candidate

核验日期：2026-10-01。指定开发基线为
`529b6bd1592b132da4ab8154bbd07d92882a59fe`（alpha.7），开发分支
`feat/alpha8-notification-webhooks`。仅候选源码/包；没有 tag、Release、正式资产、
安装默认版本修改或生产部署。当前实现与证据进入独立审查/最终验收阶段。

## Identity and compatibility

Project `0.4.0-alpha.8`; DEB `0.4.0~alpha.8`; RPM
`0.4.0-0.alpha.9%{?dist}`. Configuration/control API schema 1 and sensor protocol
5 are unchanged. Database schema 13 expands fixed event channels and saves native
target activation. It preserves schema-12 bodies, decisions, targets, language and
timezone. Older binaries reject the newer database; rollback requires matching
old database, config and credential backups offline, never an in-place downgrade.

Source SHA, final package digests and review/check identities will be recorded
against actual frozen commits below. This document cannot prove its own eventual
commit SHA; code evidence and a later documentation-only commit must be distinguished.
Dirty or `Commit=unknown` packages cannot certify the final candidate.

## Channel evidence levels

| Channel | Implementation/config/TUI | en/zh events/recovery/daily | Contract/mock | Real interface | Human receiver |
| --- | --- | --- | --- | --- | --- |
| Feishu | Complete | Complete | PASS affected tests | NOT RUN | NOT RUN |
| WeCom | Complete | Complete | PASS affected tests | NOT RUN | NOT RUN |
| Discord | Complete | Complete | PASS affected tests | NOT RUN | NOT RUN |
| Slack | Complete | Complete | PASS affected tests | NOT RUN | NOT RUN |
| Teams Workflows | Complete; selected Anyone/Adaptive Card contract | Complete | PASS affected tests; acceptance only | NOT RUN | NOT RUN |
| Google Chat | Complete | Complete | PASS affected tests | NOT RUN | NOT RUN |

Real tests have no authorized endpoint/account in this task. Synthetic mocks do
not prove tenant policy, remote display or human reading. Teams HTTP 202 confirms
only request acceptance. All six channels default disabled; no public vendor
webhook has been used. Each user must create and authorize their own target.

## Automated matrix and current status

Environment: Go 1.26.8, Linux amd64. Dependencies unchanged. Private raw evidence
is outside Git; no URLs/tokens, host inventory or private policy is published.

| Scope | Evidence | Current result |
| --- | --- | --- |
| Six payloads, explicit success, absent/wrong/duplicate fields, malformed/oversized/secret responses | `internal/notify/native_test.go` | PASS affected checks |
| HTTP/business classifications, 429/Retry-After boundaries, DNS/TLS/cancel, resource bounds | native contract/transport tests | PASS affected checks; review fixes being verified |
| Feishu fixed signature vector/new retry timestamp; Discord wait/mentions/API v10/User-Agent; Slack text ok/frozen language; Google query protection; Teams acceptance | native platform-specific tests | PASS affected checks |
| Official URL parsing/SSRF/redirects, ownership/link/permission/strict JSON and leakage negatives | config native URL/file tests and sender tests | PASS affected checks |
| A→B→A, immutable sender snapshot, isolation, six worker partial failure and cancellation | native outbox tests | PASS affected checks |
| Schema-12→13 transaction/foreign keys/backups/old-version rejection; eight-way decisions/admission/budget/cooldown/leases | store native/migration tests | PASS affected checks and scoped race |
| Management cancellation/recovery/rollback, hidden retain/replace/clear, no sends on save, selected test | config/manage/console tests | PASS normal and race |
| en/zh all event kinds/phases, Unicode/controls/mentions, DST, bounded metrics and local commands | native event/daily goldens | PASS affected checks |
| Independent partial-success/Teams accepted evidence and opaque identifiers | evidence native delivery tests | PASS affected checks |
| Purge ownership/bounds/manual-file refusal and ordinary remove retention | packaging synthetic lifecycle tests | PASS; native RPM tool test requires Fedora |
| Final required `make validate` at frozen candidate | shared local/CI entry | PENDING |
| Clean stamped amd64/arm64 binaries, DEB/RPM, source manifest/SBOM/file contents | candidate builds | PENDING |
| Debian 12/13 and Fedora 43/44 install/alpha.7 upgrade/service identity/uninstall | authorized fixed-wrapper VMware lab | PENDING; initial status reads passed, guests stopped |
| Native ARM64 execution | separate hardware/runtime | NOT RUN |
| Independent review, PR checks/CodeQL if enabled, guarded main merge and main workflows | exact candidate/remote identities | PENDING |

The original Telegram/generic Webhook/heartbeat contracts and tests are retained;
final regression status follows the required full candidate checks. MMDB validator
cancellation descendant-exit assertions are retained without weakening or skips.

## Safety and platform terms

[English channel guide](NOTIFICATION_CHANNELS.md) / [中文](NOTIFICATION_CHANNELS.zh-CN.md)
records official references checked 2026-10-01, selected contracts, account/admin
requirements, length/rate/success boundaries and manual rotation/revocation.
Feishu official SPA was verified via its official document endpoint; WeCom's
browser extraction failed but a bounded read of the same official HTML succeeded.
Microsoft's mixed text/Connector examples are resolved by selecting the documented
Teams webhook trigger plus Adaptive Card workflow; actual tenant execution remains
NOT RUN. Unsupported OAuth/Entra modes are not bypassed.

No third-party implementation/SDK or new dependency was imported. MIT source
permission is separate from platform terms, distribution approvals, admin consent
and message permission. Slack commercial distribution may need a separate agreement;
[user agreement](USER_AGREEMENT.md) and [privacy](PRIVACY.md) describe project behavior,
not Marketplace certification or acceptance on the operator's behalf.

Native transports are bounded direct HTTPS with verified TLS and no redirects or
system proxy. Generic/heartbeat retain existing trusted-proxy behavior. In-flight
requests can finish at the original target; timeout/writeback failure can duplicate.
Target switching never revives isolated old work. Manual credentials require atomic
replacement/restart; managed rotation is preferred. Purge refuses user-created or
unknown files rather than deleting them.
