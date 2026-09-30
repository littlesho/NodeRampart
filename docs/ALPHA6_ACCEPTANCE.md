# alpha.6 candidate acceptance / 候选验收

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
