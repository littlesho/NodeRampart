# `v0.4.0-alpha.7` limitations

This milestone adds public installation and terminal management to an alpha
observer. It does not establish production readiness.

## Validation scope

[alpha.7](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.7)
was published as an alpha prerelease at `2026-10-01T08:33:06Z`
(`2026-10-01T17:33:06+09:00`, Asia/Tokyo) from `d164978433b5e49d68d310cf8d6f5819b855e2e0`.
All 22 anonymous downloads/checksums matched the already authenticated 22
provenance and six runtime SPDX subjects byte-for-byte. Public Debian 13/Fedora 44
bootstrap, setup, basic collection, online backup and cleanup: **PASS**; both ended
STOPPED. Strict snapshots remained **unknown / exit 2**: Debian 13 retained
`auth_window_warmup` and `sensor_commit_unavailable`; Fedora 44 retained only
`sensor_commit_unavailable`. Later advancing watermarks do not change those
snapshots; unknown is not healthy. Fedora Services Start enabled and started both
units, observed active/enabled afterward; boot execution remains **NOT RUN**.
Current alpha.7 Debian 12/Fedora 43 runtime, native ARM64, VM race, sustained
pressure, 72-hour soak, real MMDB and optional outgoing targets remain **NOT RUN**.
Natural SSH pending/recovery remains unproved/BLOCKED; earlier known-pending
independent-copy results remain separate. See the [current distribution record](RELEASE_VERIFICATION.md#alpha7-publication-and-distribution-verification).
Schema 12 adds saved outbox language/timezone; older programs require matching
verified earlier DB/configuration/key dependencies for rollback. No in-place
schema downgrade is provided. Config/API 1 and protocol v5 remain unchanged.

alpha.7 已发布为预发布版，22 项匿名文件与原认证证明完全同字节；两台公开 bootstrap、
setup、采集、在线备份和清理均 **PASS**，最终两台均 STOPPED。strict 快照仍
unknown / 退出码 2：Debian 13 保留 `auth_window_warmup` 与
`sensor_commit_unavailable`，Fedora 44 仅为 `sensor_commit_unavailable`；后续水位
推进不改变原快照。Fedora 的 Services Start 实际启用并启动两项服务，之后状态
为 active/enabled；开机执行仍为 **NOT RUN**。
旧非空积压由同源合成测试覆盖，不称实际外发；自然 SSH 恢复链、原生 ARM64 等范围仍未证明。

### Historical alpha.6 acceptance

[alpha.6](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.6)
was published as an alpha prerelease at `2026-09-30T18:54:24Z` from
`4d204b43499ca2f41c15b92334537d57bfc8b20c`. All 22 assets passed fresh anonymous
download/checksum checks and matched the authenticated 22 provenance / six
runtime SPDX proofs. Exact final-package upgrades and lifecycle checks passed
on Debian 12/13 and Fedora 43/44 x86_64; public bootstrap with separate setup
passed on Debian 13 and Fedora 44. See the [current acceptance](ALPHA6_ACCEPTANCE.md#publication-and-distribution-2026-10-01)
and [distribution record](RELEASE_VERIFICATION.md#alpha6-publication-and-distribution-verification).
Earlier locally stamped `0.4.0-alpha` / `unknown` packages and test-merge packages
are separate evidence, not proof of the final release bytes.

Basic collection and consistent online backups passed, but strict doctor
snapshots on both bootstrap guests remained **unknown / exit 2**. Fedora's
`auth_window_warmup` and transient `sensor_commit_unavailable` reasons remain
recorded; later advancing watermarks do not turn the earlier snapshot healthy.
Fedora's actual Services Start/basic readiness/process identity passed, but
independent native preset/unit flags before and after Start and boot-time
enable state were **NOT RUN**; the RPM's host-preset behavior is a source
mechanism, not measured native flags.
Known-pending SSH recovery closed on independent copies in two tested VMs.
Natural record-quality pending onset was not proved: that natural chain is
**BLOCKED**, and dependent acceptance is **NOT RUN**. Fedora's first-control
availability timing is **NOT RUN**. Native ARM64, VM race, sustained pressure,
72-hour soak, real credentialed MMDB and real optional outbound receivers remain
**NOT RUN**. No beta, stable/latest or production-readiness claim is made.

alpha.6 已公开为预发布版；22 个资产匿名下载/校验通过，与已认证的 provenance/
SPDX 证明字节一致；准确最终包的四系统跨版本及生命周期、Debian 13/Fedora 44
公开 bootstrap 与单独 setup 通过。两台 strict 快照仍 unknown/2，Fedora 保留
窗口预热与瞬时提交不可用原因。Fedora 实际运行启动/基础就绪/进程身份通过，
原生 preset/unit flags 前后及开机启用状态 NOT RUN。known-pending 独立副本恢复闭环仅在两台实测通过；
自然 record-quality pending 置位未证明，自然链 BLOCKED、依赖验收 NOT RUN。
Fedora 首次控制可用计时、真实 ARM64、VM race、持续压力、72 小时、真实 MMDB
和真实外发均 NOT RUN；不表示 beta、稳定版/latest 或生产就绪。

### Historical public acceptance

alpha.5 was published at `2026-09-29T14:50:47Z` from source
`f539d18c9a91913a49e4c1d9f36d381965f2f7b7`. All 22 public assets passed fresh
anonymous download/checksum verification, 22 provenance and six SPDX checks.
Debian 12/13 and Fedora 43/44 amd64/x86_64 native and package lifecycle acceptance
passed. Public bootstrap installation passed on Debian 12 and Fedora 43 with
unchanged uploaded packages, standard proxy variables and normal TLS checks.
See the [scoped release record](RELEASE_VERIFICATION.md#alpha5-publication-and-distribution-verification).

ARM64 DEB/RPM artifacts were cross-built and inspected, not executed on ARM64.
Real ARM64 validation is required before beta by the
[development guide](DEVELOPMENT.md). Private operational evidence is not
packaged or included in public source; validation does not establish production readiness.

Schema 6 has no durable evidence for the new recovery-pending protocol. Migration
to schema 7 defaults to no known pending under that protocol, not historical
confirmed health; it cannot reconstruct unresolved state already lost by older
versions. Historical coverage gaps remain history, not proof of backfill.
Tested transactions and process-crash recovery do not guarantee zero loss from
arbitrary host power failures.

For alpha.3, 22 published assets passed anonymous download/checksum verification.
Actual x86_64 package CLIs from the DEB and Fedora 43/44 RPMs passed 12 bounded
UTF-8 scenarios inside an isolated Debian 13 VM; no package installation took
place. The 13 source PTY scenarios use test subprocesses; Chinese input and
length boundaries have source regression coverage. This is not Fedora runtime
acceptance, installation/upgrade/removal acceptance or user SSH client/font
validation. See [release verification](RELEASE_VERIFICATION.md).

alpha.3 已完成匿名下载校验及隔离 Debian VM 内实际 x86_64 包内 CLI 检查。
源码伪终端、中文输入/长度边界回归不等于安装生命周期、ARM64 实机或用户客户端验收。

## GeoIP alpha.4 validation

alpha.4 was published at `2026-09-20T13:02:35Z`. All 22 assets passed anonymous
download and checksum checks against the verified draft; existing package and
attestation evidence applies to identical bytes. The alpha.4 packaged programs
have not been executed; installation lifecycle and ARM64 runtime remain untested.
PR #14's matching candidate passed complete offline validation of the same real
City and ASN archives. City took 22.779 s (17.929 s in `reader.Verify`) with an
observed diagnostic-process peak RSS of 408,584,192 bytes; ASN took 1.915 s.
These are measurements of those samples in the offline tool, not fixed limits,
new-package runtime acceptance or proof of user download/activation recovery.

Precheck work is capped at 64 million operations, logical expanded values at
128 million, cumulative allocation charge at 8 GiB, and the data-summary cache
at 5 MiB. The charge is not resident memory and the cache is not the entire
process. Existing per-record, depth, archive, tree and full verification checks
remain. Synchronous `reader.Verify` cannot interrupt inside the call: cancellation
is checked before and after it returns, without an abandoned background task.
The offline tool's 300-second/2-GiB process protections are not production limits.

The HTTP request budget remains 30 seconds; a TUI action has a cooperative
5-minute context and the scheduled updater a 10-minute systemd start timeout.
These do not establish a hard production RSS limit. City/ASN remain a complete
update group; failed validation does not partially activate one database.
Actual credentialed download, activation and daily updates are still unverified.
Upstream HTTP 451 legal/compliance refusals are not fixed or bypassed.

alpha.4 已于 `2026-09-20T13:02:35Z` 公开，22 个资产匿名下载字节与已验收草稿
一致，复用其包内容和证明结果；本版本包内程序尚未运行。匹配候选对两份现场
City/ASN 归档的完整离线校验通过，不等于新包或用户正式下载、激活和每日更新通过。
预检查 6400 万次、逻辑展开 1.28 亿次、累计分配收费 8 GiB、数据缓存 5 MiB
分别受限；后两者均不是整个进程的 RSS 上限。同步 Verify 取消需等待调用返回；
诊断工具的 300 秒/2 GiB 限制不是生产保证。保留完整校验、整组激活和 UTF-8 修复；
不绕过 HTTP 451。新包安装生命周期、ARM64 实机及用户客户端验收尚未执行。

## Still unvalidated

- Natural SSH record-quality pending onset and its recovery chain; injected or
  known-pending independent-copy results do not prove that natural chain.
- Fedora first-control availability timing and native VM race checks.
- Real `arm64` runtime; cross-builds do not execute the sensor.
- Authenticated MaxMind downloads with real customer credentials and live
  Telegram/Webhook delivery and HTTPS heartbeat to actual receivers. Tests use synthetic
  credentials, generated MMDB data and mocked HTTP responses.
- Every systemd hardening directive under adversarial workloads.
- Dedicated AppArmor confinement or a NodeRampart-specific SELinux policy.
- Arbitrary package rollback and database downgrade; the documented Debian
  preview-version correction is a separate, explicitly tested transaction.
- Every OpenSSH distribution/localization/PAM variant.
- nftables, firewalld, Docker, Podman, WireGuard, VLAN, tunnel, and arbitrary
  multi-NIC interactions beyond the isolated veth fixture.
- Sustained high PPS, real capture/socket loss under pressure, CPU/memory budgets,
  penetration testing, power-loss recovery, and a 72-hour soak. Bounded unit
  tests, bounded RSS/CPU/storage measurements and the four-VM lifecycle window
  do not establish full deployment resource ceilings or sustained-load results.

## Functional limits

- AF_PACKET is the alpha sensor. TC/eBPF with fixed maps and an independently reviewed loader remains independent research, not an alpha.6 prerequisite.
- Automatic selection supports main-table IPv4/IPv6 defaults; ECMP, nexthop objects and policy routing require explicit interfaces. AF_PACKET capture supports Ethernet links.
- Interface counters retry once per second when initial route lookup or reads
  fail. Selection/link identity is reconciled every second. After
  a read gap or counter reset, a new baseline avoids fabricated deltas but
  omits traffic in the gap. Coverage history is bounded to 10,000 intervals and
  1,000 explicit gaps; earlier/unrecorded time remains unknown.
- IPv6 extension parsing is bounded to eight headers. ESP is counted as `other` without transport ports.
- Single-source SYN and unmatched UDP scans are detected. ACK/FIN probe rules and distributed same-prefix/ASN correlation are not implemented. UDP request matching is bounded to 30 seconds/16,384 tuples and pauses uncertain classification after losses or cold starts.
- SSH grammar and journal origins cover supported OpenSSH locations/units. Arbitrary custom units, localizations and PAM stacks are not accepted automatically. Journal replay is limited to 15 minutes/10,000 entries; invalid cursor boundaries may leave visible gaps. In-memory detection windows rebuild after restart.
- Interface totals are authoritative for guest-visible bytes. Per-country/ASN attribution may be lower because of sensor loss, map overflow, non-IP frames, VPN outer addresses, or unsupported packets.
- IPC send-loss counts remain estimates. Protocol v5 adds session/interface
  sequences and durable commit ACKs: collector-health deltas, traffic and their
  watermark commit in one transaction. Event persistence and per-channel outbox
  decisions use separate transactions and explicit ACK flags; a partial result
  remains partial even if later retries finish. `complete` never means external
  delivery. Socket receipt/write alone does not advance a commit watermark.
  At most 64 stream watermarks are retained; retired/uncertain old sessions are
  refused rather than silently replayed. Duplicates do not repeat accounted
  traffic/health. Older protocols 1–4 and unsequenced v5 have no commit-ACK
  guarantee. Upgrade the daemon before the sensor when retaining old config;
  older daemons reject v5. No disk spool, historical flow replay or exactly-once
  end-to-end guarantee is implemented. Uncommitted in-memory event queues and
  sensor counters can be lost on process restart; gap counts can be unknown.
- Hourly traffic, authentication, and interface buckets can over-include partial UTC hours for timezones whose local-day boundary is not aligned to a UTC hour.
- GeoIP city/region data is an estimate. Optional City/ASN downloads require the
  user's MaxMind enrollment and license acceptance. Downloads and daily updates
  are opt-in; databases and credentials are not bundled. Superseded managed
  generations are cleaned after successful activation; unmanaged copies and
  independent backups remain the user's responsibility.
- Active database and rollback-journal bytes have a configured bound; explicit backups and filesystem metadata are outside it. Exclusive rollback mode trades WAL concurrency and dirty-page RAM for this bound. Automatic corruption repair and database downgrade are not implemented; see [Storage budget](STORAGE-BUDGET.md).
- Update coalescing preserves event detail but only merges matching unattempted/unclaimed incident updates. Delivery remains at least once. A claimed send can finish after a silence is created; suppression prevents later retry.
- Report backfill is bounded to 31 completed dates within thirteen months and
  archives locally. New full documents are bounded to 256 KiB, with a 128 KiB
  body and explicit entry/query limits; notification summaries are separate.
  HTML is static and escaped. Old short snapshots stay immutable and explicitly
  lack original full content. A partial snapshot created by `report now` is not
  replaced later. Trends cover the preceding 7/30 civil dates and distinguish
  complete/partial/missing/pruned; missing totals are null, never invented zero.
  Backfill cannot reconstruct missing observations or pruned event details.
- Timeline/incident context uses retained source keys and time proximity, which do not prove a common actor. Health history is bounded; no retained gap does not prove complete data.
- Offline replay uses pseudonymized metadata, not payloads or reconstructed hourly windows. Timing/ports/volume remain linkable; rule counts are not measured false-positive rates.
- Billing supports custom profiles and explicitly refreshed official AWS/OCI
  public-egress catalogs. Cache age, byte-unit assumptions, monthly host-assigned
  free allowance and coverage are shown; account usage is not discovered.
  Cross-region/AZ/NAT/LB/CDN paths, taxes and invoice reconciliation are excluded.
  Guest TX can include private traffic and duplicate interface paths. Fixed
  monthly cycle start days 1–28 share the report timezone across usage, free
  allowance, budget alerts and prediction. Forecasts require complete current
  coverage and 7/30 complete-day scenarios; they are simple extrapolations, not
  statistical confidence intervals or bills. Insufficient history is unavailable.
- Management edits the installed configuration path and preserves the service
  sandbox. External database paths, arbitrary unit customization, automatic
  history migration and an atomic transaction across external root edits and
  package operations are unsupported. Interrupted applies require recovery.
- Default-off SSH history hints reuse at most 1000 retained privacy-transformed
  successful logins. A restarted process observes seven days and needs complete
  journal coverage plus at least 20 successes on three local dates. Missing,
  pruned or excessive history suppresses hints. Prefix identity is a shared
  range, not an exact host; privacy/key changes and restarts require observation
  again. An unfamiliar source/hour is a history deviation, not proof of intrusion.
- Doctor strict diagnosis distinguishes confirmed degradation from unknown;
  disabled checks are not failures. Manual Prometheus textfile export has fixed
  labels and a five-minute expiry. Failed publication cannot make an old file
  current; consumers must check generation, expiry and collection success.
- No active response, automatic executable update, signed rule feed, web UI,
  Prometheus listener, or multi-node collector is included. Default-off fixed
  HTTPS heartbeat/Webhook send only to explicitly configured targets; they do
  not install a receiver, timer or background broker.
- Fedora RPM can create service accounts through native sysusers processing
  before an unsafe path is rejected by the package's pre-install script.
  Rejection preserves the symlink target, but does not guarantee an entirely
  unchanged system. RPM removal preserves state and may save modified config
  as `.rpmsave`; it is not a purge or automatic settings restore on reinstall.

Additional alert/evidence limits are documented in [the feature guide](ALERTS_EVIDENCE.md):
new alert groups default off; daily anomaly checks need historical coverage;
minute-based checks and local persistence do not guarantee delivery during full
storage or host outages. Evidence is a bounded redacted excerpt with linkable UTC
timing/counts, and its rules fingerprint describes loaded configuration at export.
Retention details are bounded to 1024 entries; pre-migration removal is unknown.

## Deployment guidance

Use a disposable VPS or isolated VM with a console/recovery path. Keep provider firewall rules and SSH key authentication independently configured. Do not test attack traffic against public or third-party addresses. Keep Telegram disabled until basic status and local reports are verified.

Healthy route families continue under partial discovery, while coverage stays
degraded. ECMP/policy-routing limits remain. GeoIP unchanged detection avoids
activation but still downloads and validates the pair. New report archives
preserve configured pricing inputs; old archives contain no reconstructed
tariff history. Schema 11 cannot be opened by alpha.5; planned rollback requires
restoring a matching verified old backup in an isolated environment.

alpha.6 safely copies GeoIP inputs as streams and uses a fixed-purpose,
unprivileged verification subprocess with bounded cancellation and resources.
This preserves full Verify and paired activation; it does not inherit the
historical real-MMDB result as acceptance of new program bytes.

## alpha.8 development candidate notification limits

Six new adapters are implemented behind default-disabled configuration. One target
per channel, outbound HTTPS only: no private-message bots, incoming chat, commands,
OAuth/Entra token acquisition, file uploads or runtime SDK downloads. Native
messages are one deterministic summary, not a complete report. There is no
unbounded splitting or guaranteed exactly-once delivery. Interface confirmation
is the observation boundary; Teams only confirms workflow request acceptance.

Contract mocks do not certify real accounts, tenant policies, delivery or human
reading. Real six-platform tests are NOT RUN unless an explicitly authorized
synthetic target and receiver confirmation are recorded in [acceptance](ALPHA8_ACCEPTANCE.md).
Native ARM64 execution is separate from cross-build evidence. Unsupported official
URL shapes fail closed; users must not bypass tenant policy or TLS checks.

Manual in-place reversible credential edits may share filesystem timestamp
granularity; use managed rotation, or atomically replace a manual file and restart.
Persistent target transitions still never revive isolated A→B→A queues. Secrets
in backup archives remain protected by operator storage permissions, not database
encryption. Slack commercial distribution requirements are separate from MIT;
this source makes no Marketplace or vendor approval claim.

中文：六个新渠道默认停用，每渠道一目标，仅出站 HTTPS。mock 不是平台/租户/人工
接收验收；Teams 只有工作流接受回执，网络与写回故障仍可能重复。无真实授权目标的
联调为 NOT RUN，交叉编译不等于 ARM64 运行。手工凭据应原子替换并重启，推荐管理
界面轮换；不得绕过租户策略、TLS 或第三方分发条款。
