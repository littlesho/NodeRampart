# NodeRampart alpha limitations

<!-- current-release:start -->
The newest published product release is
[v0.4.0-alpha.11](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.11),
an alpha prerelease without GitHub's “Latest” badge.
<!-- current-release:end -->

## Alpha.11 scoped Pre-release acceptance

The [alpha.11 publication record](RELEASE_VERIFICATION.md#alpha11-pre-release-publication-and-distribution-verification) binds the same 22 hosted assets to source `2c9d4416adef3cb64e0523a1b9ac1e691b121c16`. Four x86_64 VMs (Debian12/13, Fedora43/44) passed first-install, basic SQLite and English/Chinese NO-readback/help/cancel checks. Applicable upgrades, reinstalls, normal service cycles, RPM soft stop/start, DEB/RPM keep-data paths and family purge passed. Debian12 normal pending=0 trusted durable ACK passed; ordinary ACK does not establish fault recovery. All 22 assets passed anonymous byte/SHA checks and 21 checksum entries, reusing the same 22 provenance and six SPDX bindings.

**Fedora43/44 native durable ACK remains NOT RUN:** a private MAC-label parser failure prevented the scene from starting. No product defect is confirmed in that scene, and there is no evidence of success. The maintainer's **SCOPED_PRE_RELEASE_WAIVER** accepts this coverage gap only for alpha.11 Pre-release; the original PRE-RELEASE-GATE BLOCKED and all failures remain unchanged.

Complete journal fault/quiet/trusted-once recovery, real MaxMind download and YES-save/new-process/systemd closure, native ARM64, long soak, real VPS deployment and actual third-party notification delivery remain **NOT RUN**. The lab cannot reach MaxMind; no credentials/license-sensitive input was supplied. VPS installation, license confirmation and real GeoIP acceptance are user work after publication. A full new binary vulnerability scan of the 18 hosted programs was not performed; provenance and reachable-source checks are distinct evidence.

This is **Alpha / Pre-release**, not stable or production-ready, and not GitHub Latest. Formal Release/Latest requires user VPS feedback and new explicit approval, including reconsidering Fedora ACK. No old tag, asset, failure or historical cause is rewritten by publication.

中文：四台 x86_64 的限定首装、SQLite、NO 回填／帮助／取消及适用生命周期已通过；Debian12 普通 ACK 不等于故障恢复。Fedora43/44 原生 ACK 因私有工具失败未执行，本次例外只允许 Alpha 预发布；旧 BLOCKED／FAILURE 保留。真实 MaxMind／YES 闭环、原生 ARM64、长期 soak、VPS 和真实通知仍未测，正式 Release／Latest 等待本人 VPS 反馈及新授权。

## Historical alpha.10 acceptance

Its [publication record](RELEASE_VERIFICATION.md#alpha10-publication-and-public-distribution-verification)
binds the original 22 hosted assets to the frozen release source. Authenticated
22 provenance and six package-subject SPDX checks passed. Final hosted Debian13
amd64 and Fedora44 x86_64 packages passed bounded upgrade/service/SSH/GeoIP smoke;
other hosted runtime cases and scans of the 18 hosted programs remain NOT RUN.
Fresh anonymous downloads of all 22 assets and 21 checksum entries passed; the
28 proof results were reused by exact digest. Real vendor APIs / human
receipt, actual fees, native ARM64 and production remain NOT RUN. GO-2026-5970
is still a required-module finding; stripped symbols, Fedora Go suffix coverage
gaps and tool advisories remain disclosed.

Published alpha.8 is also a non-latest prerelease. Its independent
[distribution record](RELEASE_VERIFICATION.md#alpha8-publication-and-distribution-verification)
separates new anonymous downloads and public bootstrap from reused hosted
runtime evidence. Six real vendor APIs / human receivers, native ARM64 and
production operation remain NOT RUN. GO-2026-5970 module findings, stripped
symbol / Fedora Go suffix coverage gaps and tool advisories remain disclosed.
The alpha.8, alpha.7 and earlier results below retain their historical scope.
Current recommendations follow the newest published release while keeping these
unrun checks and risks visible; a rollback or withdrawal requires an explicit
maintainer decision.

中文：当前安装推荐跟随最新公开版，同源本地候选运行证据与最终公开包证据分开。
未执行项、漏洞发现及扫描覆盖缺口继续披露；它们不自动把推荐版本回退为旧版。
以下 alpha.8 及更早结果保留历史范围。

The limited [post-publication diagnostic increment](RELEASE_VERIFICATION.md#post-publication-diagnostic-increment-2026-10-02)
identified a conditional sensor diagnostic precision defect in the unchanged
alpha.8 source: a nanosecond receipt can compare later than the same fully
committed microsecond watermark and yield `sensor_commit_unavailable` / strict
exit2. Real pending or missing commits can produce the same reason, so unknown
must not be ignored or called healthy. Published alpha.8 remains affected; the
subsequent fix included in published alpha.9 is described below and does not repair those
released bytes or change the historical results. The original full Debian doctor/health JSON was
in temporary guest storage and was absent after the new normal start; retained
check/exit/digest and backup evidence does not establish its unique historical
cause. Snapshot inspection also requires a safe standalone file with
invoking-user/root ownership, separately from successful backup verification.

中文：有限增量核验确认已发布 alpha.8 有条件性的 sensor 诊断精度误判；下述
已包含在公开 alpha.9 中的修复不改变旧 alpha.8 发行包或历史结果。真实未提交也可能产生相同 reason，
不能忽略 unknown 或改称健康。
原 Debian 完整诊断 JSON 的临时目录已不存在，剩余摘要不能证明原时点唯一成因。
快照检查的安全归属契约和 backup verify 也须分开；详见上述增量记录。

Fedora44's incremental public Release-script download passed with the frozen
bytes, but the unmodified installer's public `SHA256SUMS` request remained
BLOCKED_NETWORK. RPM download, installation, Chinese TUI and runtime checks
were NOT RUN in this increment. Three new compliant Debian snapshot checks
were valid; all six new live strict results still returned unknown/exit2 and
reproduced the precision defect. Both guests ended STOPPED with task resources
released. These separate results do not establish complete public installation
coverage or a healthy strict diagnosis.

中文：Fedora44 原公开脚本下载已通过，但公开校验文件获取仍受网络阻塞，后续
安装/TUI/运行未执行；Debian三次合规副本 valid，六份实时 strict 仍 unknown/2。
两台最终 STOPPED 并释放资源，不将这些结果解释为全安装矩阵或健康诊断通过。

This milestone adds public installation and terminal management to an alpha
observer. It does not establish production readiness.

<a id="validation-scope"></a>

## Historical alpha.7 validation scope

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
At that alpha.7 check, Debian 12/Fedora 43 runtime, native ARM64, VM race, sustained
pressure, 72-hour soak, real MMDB and optional outgoing targets remain **NOT RUN**.
Natural SSH pending/recovery remains unproved/BLOCKED; earlier known-pending
independent-copy results remain separate. See the [historical distribution record](RELEASE_VERIFICATION.md#alpha7-publication-and-distribution-verification).
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
- Fedora native durable ACK and adversarial availability/native VM race checks. The new fixed-budget first-install result does not cover these cases.
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
- Published alpha.8 and the failed alpha.9 preflight source `2ec534be8d78` can
  create Fedora service accounts through RPM's implicit sysusers processing
  before `%pre` rejects a filesystem conflict. Published alpha.9 includes a
  dependency-free embedded Lua `%pretrans` guard before that stage, preserving
  `%pre` as a later recheck. It rejects pre-existing guarded conflicts without
  package-controlled account creation; it does not prevent hostile concurrent
  path changes between phases or make a multi-package transaction atomic.
  The failed candidate's packages/assets are not release-ready evidence.
  RPM removal preserves state and may save modified config
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

## alpha.8 prerelease notification limits

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

Native package purge is not an atomic installation transaction. Managed removal
checks unknown credentials before package removal, while direct `dpkg --purge`
may remove package-owned conffiles before postrm refuses a manual credential.
Keep the matching backup; refused unknown credentials are preserved.

中文：六个新渠道默认停用，每渠道一目标，仅出站 HTTPS。mock 不是平台/租户/人工
接收验收；Teams 只有工作流接受回执，网络与写回故障仍可能重复。无真实授权目标的
联调为 NOT RUN，交叉编译不等于 ARM64 运行。手工凭据应原子替换并重启，推荐管理
界面轮换；不得绕过租户策略、TLS 或第三方分发条款。

原生包 purge 不是完整安装事务；管理卸载先检查未知凭据，直接 `dpkg --purge`
可能先删除包拥有的 conffile。拒绝未知凭据会保留该文件，但不保证配置完整回滚，
应提前备份。

<a id="alpha9-official-account-development-candidate"></a>

## Published alpha.9 official account channels

Published alpha.9 is a non-latest prerelease: four additional one-target official APIs, disabled by
default, with local consent/cost controls and schema14 durable unknown/acceptance
states. [Account-channel prerequisites](OFFICIAL_NOTIFICATION_CHANNELS.md) are
required; account approval/active-send rights, real API calls, human receipt,
actual billing, native ARM64 and production remain NOT RUN. QQ's complete public
service agreement/universal length unit and some Meta login-only terms were not
fully readable; local bounds are not claims of platform entitlement. LINE
acceptance cannot prove a blocked user received it. WhatsApp is approved-template
only, Graph v26.0, with no inbound callback or wamid delivered/read lookup.
Twilio sent is not delivered; polling is bounded and uncertainty remains held.
Local quotas do not cap an entire platform account's money charges.

The published alpha.8 sensor precision defect remains in its immutable packages.
Alpha.9 diagnosis requires matching receipt session/interface/
sequence and compares that observation in persisted `UnixMicro` units. There is
no time tolerance: a new identity within the same microsecond, a newer receipt,
missing identity/watermark or unreadable state remains unknown. Latest partial
commits remain degraded. A snapshot spanning sequence advancement can still be
unknown: a later watermark does not prove that an earlier sequence committed.
The additive `sensor_receipts` status field carries identity to CLI diagnosis;
install matching CLI/daemon programs. A new CLI cannot infer identity from an
old daemon's timestamp-only status. This fix is included in published alpha.9,
not an alpha.8 replacement or a guarantee that every sensor unknown is gone.
See [diagnostic semantics](V0.4_OPERATIONS.md#sensor-commit-watermarks).

Historical snapshot unique cause remains UNESTABLISHED; Fedora44 alpha.8 public checksum download remains
BLOCKED_NETWORK. Candidate-package tests cannot turn those old outcomes into
PASS. The GO-2026-5970 module finding, stripped symbol / Fedora version-suffix
scan gaps and tool advisories remain separate from new source reachability tests.
First-batch Teams confirms Workflow request acceptance only; new/native channels
are direct without proxies, rollback needs matching backups and purge is not
atomic. The published alpha.9 source remains `9cc75b6936d08099847655b5046c57a485c82ff7`;
later main documentation commits do not rebuild or reissue it. Publication does
not establish production readiness. The retained P3 Chinese Telegram hidden-input
wording ambiguity remains an open nonblocking item, not a fix in this record.

中文：alpha.9 已作为非 latest 的预发布版公开；四平台真实请求／人工接收／收费、账户授权、
原生 ARM64 与生产仍未测。模板／主动权限与真实订阅须操作者落实，本地声明不
替代平台审批，额度也不保证全账户金额上限。不确定提交保留，不自动重发。
已发布 alpha.8 的精度缺陷仍保留；公开 alpha.9 按会话/接口/序号匹配后以
持久化微秒比较，不放宽时间容差。新批次、缺失身份/水位及部分提交仍如实诊断；
跨采样点序号变化也可能保守 unknown。历史根因未建立、Fedora 公开下载阻塞和安全扫描缺口不改为
通过；最终 hosted 包 VM 运行及 18 个程序二进制漏洞新扫描仍为 NOT RUN，
公开不代表生产就绪。P3 中文 Telegram 隐藏输入措辞歧义仍为未修复的非阻塞意见。
