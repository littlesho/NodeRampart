# alpha.8 acceptance — Unreleased / development candidate

核验日期：2026-10-01。指定开发基线
`529b6bd1592b132da4ab8154bbd07d92882a59fe`（alpha.7），开发分支
`feat/alpha8-notification-webhooks`，[PR #27](https://github.com/littlesho/NodeRampart/pull/27)。
本记录对应源码候选和本地候选包；不代表正式发布或生产就绪。
未创建 tag、Release 或正式发布资产，未切换安装默认版本，未部署生产。

## Source and version identity

冻结的代码、自动化验收与本地包源码为
`53d2a8861d186dfa5b655011775d5c85502457ce`，tree
`621adefb9b3b907ed4cc8de739ef8a99087a0101`，构建时间取该提交时间
`2026-10-01T20:53:31+08:00`。构建时 Git clean、完整 Commit 已声明；
使用已有构建/SBOM 校验入口不使这些本地包获得正式 release provenance。

本验收更新在其后的文档提交中保存。文档提交不证明自身 SHA，也不把旧包重标为
新提交构建；其与上述源码的实际差异须仅为文档，并通过内容/链接检查和最终
PR checks。最终 PR head、合并状态、main SHA 及 main 检查以 GitHub 的对应提交/
工作流和任务最终报告为准。本记录在合并前冻结，不提前宣称 main 成功。

| Identity | Value |
| --- | --- |
| Project | `0.4.0-alpha.8`, Unreleased / development candidate |
| DEB native Version | `0.4.0~alpha.8` |
| RPM Version / Release | `0.4.0` / `0.alpha.9.fc43` or `0.alpha.9.fc44` |
| Config / control API / sensor protocol | 1 / 1 / 5, unchanged |
| Database | schema 12 → 13, minimal transactional migration |
| Source manifest SHA256 | `d55a56f858648f69b1defd2cf44cd72b7dc2afa62216c48ef3234d9f602d8876` |

RPM Release counting differs from the project alpha suffix. Actual native version
ordering and Debian ordering tests passed. Schema 13 preserves old events, bodies,
queues, targets, decisions, language/timezone, leases and cooldown, with foreign
keys and backups verified. Actual alpha.7 binaries reject schema 13. Rollback
requires an offline matching old database, configuration and credential backup;
there is no in-place schema downgrade.

## Six channel evidence levels

| Channel | Implementation/config/TUI | en/zh events/recovery/daily | Contract/mock | Real interface | Human receiver |
| --- | --- | --- | --- | --- | --- |
| Feishu | Complete | Complete | PASS | NOT RUN | NOT RUN |
| WeCom | Complete | Complete | PASS | NOT RUN | NOT RUN |
| Discord | Complete | Complete | PASS | NOT RUN | NOT RUN |
| Slack | Complete | Complete | PASS | NOT RUN | NOT RUN |
| Teams Workflows | Complete; selected Anyone/Adaptive Card contract | Complete | PASS; request acceptance only | NOT RUN | NOT RUN |
| Google Chat | Complete | Complete | PASS | NOT RUN | NOT RUN |

No authorized real endpoint/account was supplied. Mocks do not prove tenant
policy, display, delivery or human reading. Teams HTTP 202 confirms workflow
request acceptance. Each channel has one independent target and language; all
six default disabled. Saved settings do not send. Telegram and generic Webhook
can run alongside all six; their payload/proxy contracts and heartbeat remain.
No real vendor webhook was used, including during VM acceptance. Users must
create and authorize their own targets.

## Automated acceptance at the frozen source

`PATH=/tmp/noderampart-go-1.26.8/go/bin:$PATH make validate`: **PASS**, exit 0,
confirmed PASS at 2026-10-01 13:06 UTC, Linux amd64 / Go 1.26.8; race uses CGO=1,
product static builds CGO=0. Raw log SHA256
`fb18e3fe8579aa822c21cad783379382fa26f7dc22285ce631c0f402431a9e7c`.
Private raw records remain outside Git and contain no published host inventory.

The unchanged required entry ran fmt checks, module verification, vet, uncached
ordinary/race/coverage tests, static cmd builds, packaging, bootstrap, SBOM,
validation-script, lab-check, netns self-tests and pinned govulncheck. Host
packaging tests retained one existing missing-native-RPM-tools skip; Fedora 44's
actual RPM-tools test is separately recorded below. No scanner, assertion,
ignore list or fixed dependency was weakened. In particular the existing MMDB
validator cancellation test still verifies descendant exit.

| Matrix | Evidence in source | Result |
| --- | --- | --- |
| Six payloads/authentication, explicit success; HTTP 200 business failures; missing/wrong/duplicate fields; empty/malformed/large/secret responses | `internal/notify/native_test.go` | PASS |
| HTTP versus business errors, 401/403/404/payload/429/5xx, Retry-After boundaries, DNS/TLS/timeout/cancel, worker exit and bounded transport | native contract/transport/worker tests | PASS |
| Feishu fixed HMAC vector and renewed retry timestamp; WeCom errcode; Discord wait/mentions/v10; Slack text ok/frozen language; Teams contract/202; Google protected queries | native platform-specific tests | PASS |
| Exact official URLs, SSRF/redirect/encoding negatives, safe files/links/permissions, sanitized errors and leakage negatives | config native, sender, manage/console tests | PASS |
| Eight-way event/daily admission and dedupe, partial success, per-channel/global quota, persisted pacing/cooldown/leases, restart/TTL | store, notify, daemon and report tests | PASS |
| A→B→A, pause/resume, immutable credential snapshot, privacy isolation, send-time recheck, selected body discard, no historical native backfill | native outbox/targets/activation tests | PASS |
| Schema-12 migration, foreign keys, backup validation/restore, old-version rejection, original queue/presentation | migration/native store and existing tests | PASS |
| Draft/validate/review/apply/rollback/cancel recovery; hidden retain/replace/clear; independent test; no send on save | config/manage/console tests | PASS |
| en/zh all 16 event kinds × start/update/recovery, daily, Unicode/emoji/control/mention safety, DST/offset, frozen presentation and usable local commands | native event/daily goldens and tests | PASS |
| Per-channel evidence, opaque identifiers and Teams accepted boundary | evidence native tests | PASS |
| Removal ownership/bounds, manual-file refusal, packaging/source inventory, alpha version order | packaging tests | PASS; host native-tool skip covered on Fedora 44 |
| Original Telegram/generic Webhook/heartbeat and MMDB cancellation regression | complete existing test suite | PASS |

Pinned Gitleaks 8.30.1 directory and full-history scans: PASS. GitHub's independent
CI, secrets, seven fuzz jobs, static amd64/arm64, DEB, all four Fedora RPM build
jobs and CodeQL Go/actions passed for PR head `53d2a886`:
[CI run 36864848277](https://github.com/littlesho/NodeRampart/actions/runs/36864848277),
[CodeQL run 36864843936](https://github.com/littlesho/NodeRampart/actions/runs/36864843936).
PR checks test GitHub's generated merge candidate; their package stamps are not
substituted for the local source-53 package identities below. Final documentation
head checks are required before merging and are visible on PR #27.

Pinned govulncheck exited 0: zero reachable-code findings, zero affected imported
package findings, **one required-module finding**. Unchanged `golang.org/x/text
v0.21.0` has [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970) in
`unicode/norm`; fixed version is v0.39.0. Linux amd64/arm64 product import graphs
do not import that package. The fixed dependency was retained within this task's
no-upgrade scope; this is not a claim of zero vulnerable modules or future safety
if new callers are introduced. No ignore was added.

Earlier results were preserved: the preceding a8b5fa44 full validation reached
bootstrap and failed two collector fixtures still naming alpha.7 after the
collector changed to alpha.8. Only those current synthetic fixtures were corrected;
all assertions and published installer fixtures stayed intact. Final 53 ran all
44 bootstrap cases and the full entry successfully. Initial high-entropy synthetic
webhook strings triggered Gitleaks; replacing only the fixtures with obviously
synthetic low-entropy values resolved them without suppression.

## Local candidate artifacts

All six runtime packages bind full source `53d2a886` and its source commit date.
The source RPM, package inventories, three program digests, source manifest and
SPDX/buildinfo are checked through the existing bounded tools. All six pairs
passed `validate_pair`, and `build-release.sh --collect` passed in a separate
clean source-53 checkout, producing the complete local checksum/metadata set.
Collection publishes nothing. Syft 1.51.1 is
pinned and verified. SBOM scope is the packaged Go programs; operating-system
runtime dependencies are excluded. No candidate asset was published.

| Local package | SHA256 |
| --- | --- |
| `noderampart_0.4.0-alpha.8_amd64.deb` | `01ac2f670e2f30a5488aaa20486a830de300f967181d38e6960a9555290b3579` |
| `noderampart_0.4.0-alpha.8_arm64.deb` | `294f76ad9e76555df6d6c1df5eb4bfb8bd1b03ce809aea05e635dcec186075fb` |
| `noderampart-0.4.0-0.alpha.9.fc43.x86_64.rpm` | `7e6f9f8fcb4ffa1e57e2f92e404d7879c82beccdf370615b076ce654fbe53a13` |
| `noderampart-0.4.0-0.alpha.9.fc43.aarch64.rpm` | `712364d0836921b0d6c340f667ea751a87ab2e9039a59b79dfafbe1332e0ed05` |
| `noderampart-0.4.0-0.alpha.9.fc44.x86_64.rpm` | `071486561b0df46647a5d9ad0cff20aee4efa8a74d4eb9a85d8c3694937883c5` |
| `noderampart-0.4.0-0.alpha.9.fc44.aarch64.rpm` | `e643f7a9624bd620adb5aa63abe711476f1d098e59cd4bf16442804100026d37` |
| `noderampart-0.4.0-0.alpha.9.fc44.src.rpm` | `183962a0b2e5e8db695a754ae9120bfb3c03e4a070c7c8f7f302c3e10b0dc341` |

Standalone and DEB amd64/arm64 ELF programs are static: no interpreter and no
NEEDED libraries. RPM preserves the baseline CGO=0 PIE build: no NEEDED libraries,
but a system ELF loader is required. It is not described as a loader-free static
RPM. Local RPM builds ran on Fedora 44, including a bounded target-dist shim for
fc43; they do not prove native Fedora 43 compilation. PR CI separately passed
builds in both actual Fedora 43 and 44 containers. ARM64 builds are cross-builds;
**native ARM64 execution is NOT RUN**.

## Authorized VMware acceptance

Only the existing approved wrappers and fixed four lab targets were used.
All four guests were cleanly uninstalled and STOPPED status was confirmed through
their approved soft wrappers; baseline snapshots were preserved.
No restore, reset, hard stop, VM mapping, Broker/ACL, host routing/firewall or
production action was performed. Valid synthetic credentials were inspected only
by local config validation under the actual daemon UID, with no DNS or sending;
all optional channels were disabled except an absent credential used to test
fail-closed monitoring. Real webhook tests remain NOT RUN.

| Guest | Install / alpha.7 upgrade / service / uninstall | Evidence scope |
| --- | --- | --- |
| Debian 12 amd64 | PASS | Official alpha.7 → schema 13 preservation, backup/restore, old-binary refusal; final53 fresh package/runtime/service UID and credential reads, removal/purge |
| Debian 13 amd64 | PASS | Same real package/lifecycle scope |
| Fedora 43 amd64 | PASS | Official alpha.7 → final53 upgrade/runtime identity; schema/queues/presentation/leases/TTL/cooldown/FK preserved; actual service UID six credential reads, fail-closed files, removal/purge |
| Fedora 44 amd64 | PASS | Same real lifecycle; 42 native packaging tests including rpmspec PASS without skips; all four RPM SPDX/buildinfo pairs PASS |

Debian upgrade behavior was first executed on a8b5fa44 and reused only after
proving that a8b5fa44 → 53 changes documentation and bootstrap fixtures alone:
product, Go tests, build/packaging/dependency inputs are unchanged. Final53 fresh
installation and all runtime/credential/fail-closed/removal checks were executed
again. This is scoped evidence reuse, not a claim that the earlier package had
source-53 stamps. CLI/daemon expose full build identity; the sensor has no version
CLI, so its identity is checked by package bytes, architecture/static metadata
and actual service startup log.

Fedora service checks used the final53 package itself. An initial private Teams
fixture used unsupported `api-version=2016-10-01`; local validation correctly
rejected it. The fixture was corrected to supported `2016-06-01`, the initial
FAIL preserved, and execution continued from credential checks without repeating
installation or weakening validation. Both Fedora lifecycle runs then passed.

Managed purge preflight rejects manual/unowned credentials before package removal.
Direct `dpkg --purge` can remove package-owned conffiles before postrm rejects a
manual credential. It preserves that credential but does not atomically roll back
the installation. Both paths were exercised; owned test fixtures were moved out
before successful purge. Always back up matching configuration/data/credentials.

## Independent review and fixed findings

Three actual collaborating reviewers examined scopes they did not author, bound
to source53 via direct diff checks and matching affected ordinary/race evidence:

| Reviewer | Independent scope | Result |
| --- | --- | --- |
| storage delegate | Native sender/URL/file security, transport, config/manage/TUI, ownership/purge | PASS; excludes their own store implementation |
| native sender delegate | Store/migration/backups, targets/leases/pacing, root worker/daemon/daily integration | PASS; excludes their own native sender code |
| config/manage delegate | Root daemon/CLI, bilingual presentation/report/evidence, versions/builds and public documentation | PASS; excludes their own config/manage/TUI/purge code |

All blocking findings were repaired and verified: HTTP error status could be
overridden by a contradictory business body; a daemon identity fallback could
re-read a disallowed credential directory; new targets could admit older journal
events; a full daily channel could prevent later healthy targets from queuing.
Localization categories and collector fixtures were also corrected. No remaining
blocking finding was reported within the independent scopes. Second-pass author
review is not counted as independent review. Final documentation updates require
separate content/link review; post-merge workflows are a distinct result.

## Safety, authorization and remaining limits

[English channel guide](NOTIFICATION_CHANNELS.md) / [中文](NOTIFICATION_CHANNELS.zh-CN.md)
record the official references checked 2026-10-01, account/admin prerequisites,
selected contracts, lengths/rates, success/error boundaries and rotation/revocation.
Feishu's official SPA was verified using its official document endpoint; WeCom's
browser extraction failed but a bounded read of the same official HTML succeeded.
Microsoft's mixed examples are resolved by the specified webhook trigger and
Adaptive Card workflow. OAuth/Entra-only tenants remain unsupported; policy is
not bypassed. Workflows need maintained owners/co-owners and connection permissions.

No third-party implementation/SDK or new dependency was imported. MIT source
permission is separate from platform service/distribution terms, admin consent
and message permission. Slack commercial/paid distribution may require a separate
agreement; [user agreement](USER_AGREEMENT.md) and [privacy](PRIVACY.md) describe
NodeRampart's own behavior, not Marketplace/vendor certification or terms accepted
on behalf of an operator.

Native transports use bounded direct HTTPS, verified TLS and no redirects or
system proxy. Generic Webhook/heartbeat retain existing trusted-proxy behavior.
DNS and actual dial addresses are checked; credentials never enter ordinary
configuration, stored bodies, metrics labels or errors. In-flight requests can
finish at the old target; timeout/writeback failures can duplicate. Isolation and
persistent cooldown remain, with no exactly-once or user-read claim. Manual files
should be atomically replaced and the daemon restarted; managed rotation is
preferred. Purge never follows arbitrary credential references.

Remaining unverified scope: all six real APIs and receiver confirmation, native
ARM64 runtime, unsupported tenant authentication/URL variants and production
operation. Default-disabled alpha features may merge once actual repository
checks/review conditions hold; the remaining scope is not silently marked PASS.
