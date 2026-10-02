# Alpha.9 development candidate acceptance

Alpha.9 is **Unreleased / development candidate**. This work does not create a
product tag, release, hosted release attestation or public installation default.
The specified development baseline is
`936dbdbeb5658d1c1386557da4b4ac1239103913`; its actual main CI
[36991385648 / attempt 1](https://github.com/littlesho/NodeRampart/actions/runs/36991385648)
and CodeQL [36991384489 / attempt 1](https://github.com/littlesho/NodeRampart/actions/runs/36991384489)
were read back as successful before development. Those runs certify the baseline,
not alpha.9. The branch is `feat/alpha9-official-notification-channels`.

The final reviewed head, test merge, merged main, package inventories, raw check
outputs and independent review records must be bound to their actual identities
in the development PR and the protected acceptance report. This document does
not assert its own eventual commit hash or turn a pending external run into PASS.
Private policies, host inventories, consent evidence, credentials and raw lab
outputs are excluded from the source manifest and public repository.

## Scope and contract evidence

| Channel | Implemented request | Local contract coverage | Account / real API / human receipt |
| --- | --- | --- | --- |
| QQ Bot | Own AppID/AppSecret → AppAccessToken; API v2 user/group active text | Token cache/single-flight/expiry, own identity header, explicit opaque target type, strict business receipt, refusals and uncertainty | ACCOUNT_AUTHORIZATION_REQUIRED / NOT RUN / NOT RUN |
| LINE | Messaging API Push with long-lived channel token | Platform user/group/room IDs, persistent UUID on first request, accepted 409 validation, frozen request and <24h recovery window, UTF-16 count | ACCOUNT_AUTHORIZATION_REQUIRED / NOT RUN / NOT RUN |
| Twilio SMS | Account-scoped Messages POST, explicit Basic mode and From or Messaging Service | Escaped form/SMS-only, strict SID association, persistent GET-only bounded observation, STOP/21610 lock, GSM-7/UTF-16 segments and reservations | ACCOUNT_AUTHORIZATION_REQUIRED / NOT RUN / NOT RUN; real billing NOT RUN |
| WhatsApp Cloud | Fixed Graph v26.0, selected phone number ID, approved BODY templates | International + recipient, six required scalar slots, independent approved language/routes, strict wamid, template/auth/policy errors, no free-text fallback or invented delivered GET | ACCOUNT_AUTHORIZATION_REQUIRED / NOT RUN / NOT RUN; real billing NOT RUN |

Official documents were read on **2026-10-02**. The paired
[English guide](OFFICIAL_NOTIFICATION_CHANNELS.md) and
[中文指南](OFFICIAL_NOTIFICATION_CHANNELS.zh-CN.md) link the actual vendor contracts,
ID acquisition prerequisites, authentication, limits, permission/terms boundaries
and unread official pages. QQ active entitlement and opaque IDs require the
platform's authorized external event/debug source; the product neither fabricates
reply/event IDs nor installs callbacks/Gateway sessions. QQ's full developer
agreement and a universal text-length unit were not completely retrievable;
its 1,800-byte bound is a local safety bound, not a claimed platform quota.
Some additional Meta Platform Terms pages returned throttling/login content;
that is BLOCKED_READ, not commercial distribution approval. Approved own assets,
recipient consent and platform permission remain operator prerequisites. Source
MIT permission does not supply vendor permission or legal compliance certification.

## Integration and test evidence

All four default disabled, independently select en/zh and use one immutable target.
QQ/LINE default event and daily selection; paid channels default high-severity
events, daily off. Twelve-channel fan-out excludes heartbeat. Saving, validation,
status and local preview make no provider request. Paid testing needs the current
masked local preview plus explicit confirmation, and uses the same persistent
subscription, queue, intent, cooldown and finite UTC budget as events/dailies.

Synthetic contract and actual SQLite tests cover these independently:

- strict response field/type/duplicate-key/size failures; authentication, 429,
  business limits, Retry-After, 5xx ambiguity, cancellation and hostile secret echo;
- token cache generations; LINE first durable UUID, same frozen request across
  restart, window expiration and rollback; non-idempotent unresolved requests held;
- Twilio accepted SID → GET observation without another POST, asynchronous opt-out,
  terminal/unknown status and limits; payload segment estimate vs frozen body;
- approved template route/ordered slots/language fingerprint, bounded parameters
  and coverage; no alternate recipient, free text or message-window fallback;
- A→B→A, disable/resume, privacy/selection tightening, target rotation, activation
  boundaries and independent event/recovery/daily decisions;
- shared queue limits/fairness, concurrent UTC reservations, no uncertainty refund,
  token/target rotation without a fresh allowance, clock rollback and restore hold;
- config upgrade/round trip, hidden retain/replace/clear/cancel transactions,
  explicit consent/revocation, credential ownership and no implicit sending;
- schema 13→14 atomic upgrade/rollback, foreign keys, legacy queues/presentation,
  backup schema verification, restore reconciliation and old-schema rejection;
- bilingual events/stages, Unicode/GSM extension/emoji boundaries, malicious
  control/format text and archived report timezone/DST with actual paid preparation.

Iteration logs preserve earlier failures. Integration review found and corrected
worker recheck ordering (intent must precede the official send check), missing
frozen metadata in pending reads, Twilio observation deadline inconsistency and
LINE Retry-After propagation. Existing negative file-permission tests were not
weakened: the private check wrapper was corrected to use ordinary child umask
while retaining protected evidence permissions. A later green iteration does not
rewrite the original failure. Author checks are distinct from non-author review.

The final candidate requires the unchanged **`make validate`** entry: fmt, vet,
module verification, full ordinary/race/coverage tests, CGO-disabled builds,
packaging/bootstrap/SBOM/lab-harness checks and pinned govulncheck. Existing MMDB
validator descendant-process cancellation assertions, safety detectors, fuzz
jobs and dependency/scanner pins remain in force. PR CI and merged-main CI/CodeQL
are separate exact-source evidence; reaching the bounded monitor deadline leaves
PENDING, not success. Package checks and isolated execution do not prove production.

## Identity, storage and recovery

Project `0.4.0-alpha.9`; DEB `0.4.0~alpha.9`; RPM
`0.4.0-0.alpha.10.fc43/fc44`. Config/control API **1**, sensor protocol **5**;
database **14**. Additive config/API fields do not promise old clients understand
new channels. Historical migrations retain their original eight-channel meaning.
One transaction adds the twelve-channel decisions, dispatch metadata, opt-out /
restore policies and bounded UTC-day ledgers, preserving old queues and sent rows.
Old alpha.8 programs must reject schema 14. Rollback uses matching old database,
config and protected credential backups with the corresponding old program;
there is no in-place schema downgrade.

A dispatch intent and reservation precede external I/O. Accepted does not mean
sent/delivered/read; Twilio's separately observed sent/delivered values retain
their narrower meanings. Unknown QQ/Twilio/Meta requests never automatically
resubmit. LINE retries only its original UUID/request within the safe window.
Retries do not regenerate text, translate old queues, move recipients or refund
uncertain costs. UTC quotas are per local channel and survive recipient/token
rotation; they are not a provider-account currency cap. A restored paid ledger
requires explicit reconciliation and holds old pending/unknown requests;
reconciliation permits future requests and conservatively consumes today's
allowance. Undetectable disk/backup rollback is outside an exactly-once guarantee.

New channels use direct fixed-origin HTTPS/443 with TLS, DNS and actual dial
address checks, no environment proxy, redirect, arbitrary URL or new listener.
Legacy Telegram/generic Webhook/heartbeat proxy and payload contracts remain.
Credentials and raw account/recipient data stay in managed protected files;
status/evidence use bounded local identities and fixed error classes. Purge only
removes demonstrably owned files and is not an atomic installation rollback.

## Candidate builds and isolated execution

Clean candidate packages must carry their exact full commit and original commit
`BUILD_DATE`, with static amd64/arm64 programs, affected DEB/RPM, reviewed source
manifest and final-byte SBOM/buildinfo inspection. RPM uses its actual native
Fedora toolchain and PIE/system loader; CGO=0 does not imply loader-free RPM.
CI/candidate packages do not inherit hosted release provenance. Cross-compilation
and static ARM64 inspection remain **native ARM64 NOT RUN**.

New-candidate package upgrade/service-UID credential/monitoring checks are distinct
from earlier alpha.8 lifecycle evidence. Authorized isolated Debian/Fedora tests
must preserve raw JSON/stdout/stderr/exit status on the host before cleanup and
use the fixed wrapper, exclusive ownership, normal shutdown and independent
STOPPED confirmation. Any unavailable environment/tool stays BLOCKED/NOT RUN;
no network/ACL/Broker/SELinux changes are authorized to obtain a pass.

## Retained limits and security status

The sensor nanosecond receipt versus microsecond watermark precision defect is
**confirmed and UNFIXED** by this version. Existing strict unknown/exit2 evidence
remains valid. Historical snapshot unique root cause is **UNESTABLISHED**.
Fedora44 alpha.8 public checksum-download stage remains **BLOCKED_NETWORK**;
a candidate RPM's local installation is not a public bootstrap pass.

GO-2026-5970: the existing `x/text v0.21.0` module finding remains. New-source
imports/reachability must be checked for all three product commands and both
architectures/build modes; earlier lack of `unicode/norm` imports is not an
automatic new-candidate result. Fixed scanner versions and official advisory
records remain; stripped symbols and Fedora Go suffix stdlib identification leave
binary coverage gaps. Tool advisories remain separate from product exploitability.
No declaration here accepts unknown public-release risk or promises zero flaws.
Teams still confirms only workflow request acceptance; native channels stay
direct without proxy, backups remain matched, purge remains non-atomic.

Six alpha.8 platforms plus the four new real APIs/human receipts, account approvals,
actual paid charges, native ARM64 and production execution are **NOT RUN**.
Published alpha.8 tag/Release/source/22 asset identities stay frozen. No alpha.9
tag/draft/Release, latest change, bootstrap-default change, production deployment
or unauthorized real/charged test belongs to this development task.

本文件记录 alpha.9 开发候选范围与分层证据，不代表公开发行或生产就绪。
四渠道实现、合成契约、真实账户/API、人工接收和实际计费必须分别报告；
完整验收、独立审查、候选包与实验绑定实际来源。旧未知/失败不由后来通过回填，
最终 PR 与 main 的检查分别记录，监看边界后仍运行即 PENDING 并停止。
