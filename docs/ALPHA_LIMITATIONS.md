# `v0.4.0-alpha.4` candidate limitations

This milestone adds public installation and terminal management to an alpha
observer. It does not establish production readiness.

## Validation scope

Earlier development snapshots passed bounded local checks and selected native
installation, management, retained removal, reinstall and purge checks in
isolated Debian 13 and Fedora 44 amd64 VMs. Those results are historical; they
are not a fresh privileged acceptance of this public source snapshot. Private
lab logs and internal handoffs are not distributed with the source.

ARM64 DEB/RPM artifacts were previously cross-built and inspected, not executed
on ARM64. The checks required for a new build are listed in the
[development guide](DEVELOPMENT.md). Public source availability does not imply a
hosted workflow run, published packages, a matching tag or a completed release.
The bootstrap command requires separately published matching release assets.

For alpha.3, 22 published assets passed anonymous download/checksum verification.
Actual x86_64 package CLIs from the DEB and Fedora 43/44 RPMs passed 12 bounded
UTF-8 scenarios inside an isolated Debian 13 VM; no package installation took
place. The 13 source PTY scenarios use test subprocesses; Chinese input and
length boundaries have source regression coverage. This is not Fedora runtime
acceptance, installation/upgrade/removal acceptance or user SSH client/font
validation. See [release verification](RELEASE_VERIFICATION.md).

alpha.3 已完成匿名下载校验及隔离 Debian VM 内实际 x86_64 包内 CLI 检查。
源码伪终端、中文输入/长度边界回归不等于安装生命周期、ARM64 实机或用户客户端验收。

## GeoIP alpha.4 candidate

alpha.4 is not yet published; alpha.3 remains the current installation download.
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

alpha.4 候选尚未公开，默认安装入口仍为 alpha.3。匹配候选对两份现场
City/ASN 归档的完整离线校验通过，不等于新包或用户正式下载、激活和每日更新通过。
预检查 6400 万次、逻辑展开 1.28 亿次、累计分配收费 8 GiB、数据缓存 5 MiB
分别受限；后两者均不是整个进程的 RSS 上限。同步 Verify 取消需等待调用返回；
诊断工具的 300 秒/2 GiB 限制不是生产保证。保留完整校验、整组激活和 UTF-8 修复；
不绕过 HTTP 451。新包安装生命周期、ARM64 实机及用户客户端验收尚未执行。

## Still unvalidated

- Debian 12 and Fedora 43 runtime and package lifecycle.
- Real `arm64` runtime; cross-builds do not execute the sensor.
- Authenticated MaxMind downloads with real customer credentials and live
  Telegram delivery from the new setup menu. Automated tests use synthetic
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
  tests for parser errors and IPC loss recovery do not establish these results.

## Functional limits

- AF_PACKET is the alpha sensor. The planned production path is a smaller TC/eBPF collector with fixed maps and an independently reviewed loader.
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
- IPC send-loss counts are estimates, not daemon persistence acknowledgments.
  Network events have a bounded process-local persistence retry queue; a daemon
  crash/restart can lose uncommitted queued events. Capacity losses are counted,
  and restart records uncertainty rather than claiming an exact recovery count.
  Pending counters survive reconnects only within the sensor process; a sensor
  restart loses them. Recovered health is recorded in the recovery hour, and
  version 1 sensors do not provide the new IPC loss fields.
- Hourly traffic, authentication, and interface buckets can over-include partial UTC hours for timezones whose local-day boundary is not aligned to a UTC hour.
- GeoIP city/region data is an estimate. Optional City/ASN downloads require the
  user's MaxMind enrollment and license acceptance. Downloads and daily updates
  are opt-in; databases and credentials are not bundled. Superseded managed
  generations are cleaned after successful activation; unmanaged copies and
  independent backups remain the user's responsibility.
- Active database and rollback-journal bytes have a configured bound; explicit backups and filesystem metadata are outside it. Exclusive rollback mode trades WAL concurrency and dirty-page RAM for this bound. Automatic corruption repair and database downgrade are not implemented; see [Storage budget](STORAGE-BUDGET.md).
- Update coalescing preserves event detail but only merges matching unattempted/unclaimed incident updates. Delivery remains at least once. A claimed send can finish after a silence is created; suppression prevents later retry.
- Report backfill is bounded to 31 completed dates within thirteen months and archives locally. It cannot reconstruct missing data or pruned seven-day event details. Timezone conflicts preserve the existing snapshot.
- Timeline/incident context uses retained source keys and time proximity, which do not prove a common actor. Health history is bounded; no retained gap does not prove complete data.
- Offline replay uses pseudonymized metadata, not payloads or reconstructed hourly windows. Timing/ports/volume remain linkable; rule counts are not measured false-positive rates.
- Billing supports custom profiles and explicitly refreshed official AWS/OCI
  public-egress catalogs. Cache age, byte-unit assumptions, monthly host-assigned
  free allowance and coverage are shown; account usage is not discovered.
  Cross-region/AZ/NAT/LB/CDN paths, taxes and invoice reconciliation are excluded.
  Guest TX can include private traffic and duplicate interface paths.
- Management edits the installed configuration path and preserves the service
  sandbox. External database paths, arbitrary unit customization, automatic
  history migration and an atomic transaction across external root edits and
  package operations are unsupported. Interrupted applies require recovery.
- No active response, automatic executable update, signed rule feed, web UI,
  Prometheus endpoint, or multi-node collector is included.
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
tariff history. The current schema 6 requires a matching verified backup for a
planned downgrade.
