# alpha.6 acceptance / 验收

The original preparation snapshot below is retained with its date and pending
claims. Alpha.6 publication results are appended in
[Publication and distribution (2026-10-01)](#publication-and-distribution-2026-10-01).
For the current version, use the [installation guide](../README.md) and
[current release record](RELEASE_VERIFICATION.md#alpha11-ordinary-release-and-latest-promotion).

## Preparation snapshot (2026-09-30)

This is a scoped, public-safe preparation record dated 2026-09-30, before
candidate freeze. It does not announce publication. Final run IDs and exact
asset digests belong in the reviewed PR and draft Release notes, without
changing a frozen tag to update this source document.

## Source and identity

- Public base: `d1bf8dffeebcbcaec8af304977ca0a4c38ca3d03`.
- Published alpha.5 source: `f539d18c9a91913a49e4c1d9f36d381965f2f7b7`.
- Candidate: `0.4.0-alpha.6`; Debian `0.4.0~alpha.6`; RPM Version `0.4.0`,
  Release `0.alpha.7` plus distribution suffix.
- Only reviewed product, tests, build inputs and public documentation are
  integrated. Private archive history, task/governance records, VM controls,
  credentials, databases, raw logs and evidence kits are excluded.

## Acceptance gates

At this source-document time, candidate local shared checks, bounded fuzz and
fixed-version secret scans are pending; hosted candidate CI/proofs and exact
alpha.5 → alpha.6 VM upgrades are pending. Results must identify the PR head,
test merge and final public source separately. Earlier package acceptance does
not verify changed candidate bytes. See [release verification](RELEASE_VERIFICATION.md)
for the existing shared gates, asset set and cryptographic checks.

The required package tests use real published alpha.5 packages and an old
program-created schema 7, an independently verified backup, then normal apt/dnf
upgrade to schema 11 on disposable Debian 12/13 and Fedora 43/44 x86_64.
Synthetic historical rows and injected recovery states are labelled fixtures.
Final tests use bytes downloaded from the authenticated draft, with fc43 on
Fedora 43 and fc44 on Fedora 44. Same-version reinstall, cross-version upgrade,
fixture behavior and actual package lifecycle remain separate claims.

The protocol changes to v5: traffic/collector-health/watermark commit atomically;
event and per-channel outbox flags describe separate transactions and partial
ACKs. Loss counters remain estimates; ACK never means external delivery.
Old unknown-target outbox rows remain isolated. Old short reports remain
immutable and cannot become original full snapshots. Preserve service intent
and unresolved management/journal recovery; silence is not proof of recovery.
Install all three new programs before saving extended configuration. A manual
rolling transport update uses new daemon before new sensor. Old programs reject
schema 11; rollback requires a compatible old backup, never an in-place schema
downgrade. Config/API 1 and opt-in doctor strict exit codes 0/1/2 stay unchanged.

## Limits

Real ARM64 execution, 72-hour soak, sustained pressure, real credentialed MMDB
activation and external Telegram/Webhook/heartbeat receivers: **NOT RUN**.
A draft authenticated download does not demonstrate anonymous bootstrap
installation. Attestations prove source/build binding, not runtime safety.
The candidate must remain draft and prerelease; it is not beta or production
certification. Historical public validation remains in
[limitations](ALPHA_LIMITATIONS.md) and the release verification guide.

本摘要只公开必要的来源、身份、验收边界，记录时点在候选冻结之前；未声明发布。
准确 run ID、包摘要及最终结果在本轮 PR 与草稿正文记录，不为补结果移动冻结 tag。
本候选待执行的门禁不能用旧本地包或旧版成功替代；真实旧 alpha.5 程序建 schema 7，
备份验证后正常升级 schema 11；注入历史/恢复状态明确称为 fixture。最终用草稿下载
的准确包验收四个发行环境，Fedora 43 使用 fc43，Fedora 44 使用 fc44。
同版本重装与跨版本升级、源码测试与包生命周期分别记录。保留旧快照、未知目标积压
隔离、服务意图及未完成恢复；v5 部分 ACK 不代表外发成功，损失仍是估计。
旧程序拒绝新 schema，回退须恢复匹配旧备份。真实 ARM64、长跑及真实可选外发未跑；
草稿不是匿名公开发行，候选保持 draft=true、prerelease=true。


## Publication and distribution (2026-10-01)

[v0.4.0-alpha.6](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.6)
was published at `2026-09-30T18:54:24Z` (Release ID `400047056`),
`draft=false`, `prerelease=true`, not marked latest. Frozen source/tag is
`4d204b43499ca2f41c15b92334537d57bfc8b20c`, source BUILD_DATE
`2026-09-30T20:19:52+08:00`; later documentation cannot change these identities.
[Main CI](https://github.com/littlesho/NodeRampart/actions/runs/36714019589)
and [release attempt 1](https://github.com/littlesho/NodeRampart/actions/runs/36716260742)
passed. Schema 11, protocol v5 and config/API 1 retain the upgrade/partial-ACK
limits in the preparation snapshot.

| Scope | Actual result |
| --- | --- |
| Public distribution | PASS: fresh anonymous downloads of all 22 exact assets, SHA256SUMS and native/ELF/Go/buildinfo identities. |
| Build proofs | PASS: unchanged bytes match authenticated 22 provenance and six runtime SPDX verifications; source RPM is not runtime. |
| Actual alpha.5 → final alpha.6 | PASS on Debian 12/13 and matching Fedora 43/44 x86_64; actual old program-created schema 7 → 11, with labelled history/recovery fixtures. |
| Final package lifecycle | PASS on all four systems; same-version reinstall and preserve/remove/purge cases remain separate from cross-version upgrade. |
| Public bootstrap | PASS on Debian 13/Fedora 44: explicit alpha.6 download, normal APT/DNF install with `--no-setup`, actual separate setup and basic collection. |
| Online backup and watermarks | PASS on both bootstrap guests: consistent schema-11 backup and actual committed-watermark progression. |
| Strict diagnosis snapshots | Unknown / exit 2 on both; Fedora retained `auth_window_warmup` and transient `sensor_commit_unavailable`. Later progress does not rewrite this snapshot. |
| Publication bootstrap cleanup | PASS: actual helper purge, separately retained evidence/backups, owned-process and lease/lock cleanup; fixed soft-stop and independent status confirmed both STOPPED. |
| Known-pending SSH recovery | PASS on independent copies in two tested VMs; injected/known state is distinct from natural onset. |
| Natural SSH pending/recovery chain | BLOCKED: natural record-quality pending onset not proved; dependent checks NOT RUN. |
| Fedora native preset/unit flags before and after Start / boot-time enable state | NOT RUN; actual runtime start and basic readiness passed. |
| Native ARM64 / VM race / first Fedora control timing | NOT RUN; inspected ARM64 artifacts are not native execution. |
| Sustained pressure / 72-hour soak / real MMDB / real outbound receivers | NOT RUN. |

Published bootstrap's default remains alpha.5; the README explicitly pins
alpha.6. Debian default-auto/enabled collection passed. Fedora's RPM follows
host presets by source mechanism; actual setup / Services Start, basic readiness
and process-executable identity passed, with SELinux Enforcing. Independent
native preset/unit-flag samples before and after Start, and boot-time enable
state, were **NOT RUN**.
The curl-pipe interactive installation path was NOT RUN here.
Package README/license files remain the frozen source snapshot; runtime packages
do not install the complete offline manual. See [distribution verification](RELEASE_VERIFICATION.md#alpha6-publication-and-distribution-verification)
for exact package hashes and proof scope.

First failures remain part of the acceptance scope. PR CI attempt 1 had two
30-second fuzz deadline failures with no crash sample; one bounded same-source,
same-parameter rerun passed. A possible Go cancellation race is an inference
from toolchain source; precise hosted scheduling was not reconstructed. Debian
12 default-auto readiness failed without a usable default route; documented
explicit interface selection passed without adding routes. Fedora 43's first
old backup was lost under application-state purge; a separate actual old→final
case independently retained its old backup/configuration/synthetic key outside
application paths and passed. It does not reconstruct the missing original.

Publication callers retained an initial response-header-limit SIGXFSZ, direct
network failures and Fedora's post-Save omitted-field assertion. Only the
bounded caller header limit and scoped proxy environment were corrected;
completed installs/Saves were not replayed. The final missing anonymous file
then passed. Fixture recovery, quiet logs and later status success do not prove
natural SSH recovery or retroactively make an unknown diagnostic healthy.

保留上方 2026-09-30 冻结前快照；本节才记录已发布 alpha.6 的准确来源和实际结果。
22 个资产匿名下载/校验、已认证证明的同字节复用、四系统最终包升级/生命周期及
Debian 13/Fedora 44 公开 bootstrap、实际 setup/基本采集/在线备份通过。
两台 strict 快照仍 unknown/2；Fedora 的预热及瞬时提交不可用原因保留。
Fedora 实际运行启动/基础就绪/进程身份通过，原生 preset/unit flags 前后及开机启用状态 NOT RUN。
known-pending 独立副本恢复闭环仅在两台实测通过，自然置位未证明，链仍 BLOCKED。
首次失败不抹去，后续独立补验收不冒称原件；真实 ARM64、VM race、Fedora 首次控制
计时、72 小时、持续压力、真实 MMDB/外发 NOT RUN。实际 helper purge、独立证据/备份核验、
自己的进程/锁/lease 清理通过；固定 soft-stop 后独立确认两台 STOPPED。
