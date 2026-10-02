# Development and validation

## Local checks

The current module and dependency graph require **Go 1.25.0 or newer**:
`go.mod`, `modernc.org/sqlite v1.56.0`, `modernc.org/libc v1.74.4`, and
`golang.org/x/sys v0.47.0` declare Go 1.25.0. The source also uses `os.Root`
(introduced in Go 1.24). CI pins Go 1.26.8 for validation; that tested toolchain
is not the project's minimum, and it does not prove a Go 1.25 runtime test.
Use the declared dependency versions; do not upgrade the graph to run checks.

```bash
make validate
make build
```

`make validate` is the shared local/CI/release required entry: formatting,
dependency verification, vet, uncached ordinary/race/coverage tests, static
builds, packaging/bootstrap/SBOM and lab-harness safety regressions, and the
existing fixed `govulncheck@v1.7.0` scanner. It stops on failures; unavailable
tools or network checks are not treated as success. Run a package's targeted
tests while iterating, then this entry for the final candidate. Hosted checks
also reject a checkout that differs from the workflow's full commit.

Ordinary local builds/packages accept dirty workspaces and default their existing
commit declaration to `unknown`. Keep source/artifact digests with local test
evidence. `scripts/build-release.sh` requires a clean checkout and an exact full
HEAD declaration; it cannot turn an uncommitted local candidate into official
release provenance. No command here commits, pushes or publishes a release.

The timezone selector directory in `internal/timezones/names.txt` is copied
from the Go 1.26.8 `lib/time/zoneinfo.zip` name/link list (IANA tzdata 2025c,
598 names; the data are public domain, see [third-party notices](../THIRD_PARTY_NOTICES.md)). Its header retains the source checksum and update instructions.
Rules still use `time.LoadLocation` and standard `time/tzdata` fallback; the
directory supplies names, not another rule engine. Review a supported Go ZIP,
regenerate sorted entry names, keep `Local` separate, and run the timezone,
config and console tests before updating. Common city names and Chinese region
labels are small reviewed tables. `x/text v0.21.0`, already in the dependency
graph, is now direct only for offline CLDR country names in Telegram text;
there is no new dependency or online translation.

时区目录来自上述 Go ZIP 的完整名称/别名列表，来源摘要和更新方法保存在文件头。
更新时审阅支持的 Go ZIP、排序名称、单独保留 `Local`，运行时区/配置/界面回归；
地区规则仍使用标准库及内嵌回退，不另建规则系统。中文常用城市/地域是小型审阅表，
国家中文名使用既有固定 `x/text` 的离线 CLDR；不联网翻译、不升级依赖。

For a dirty local package, use the ordinary paths:

```bash
make build COMMIT=unknown
./scripts/build-deb.sh
# On a Fedora build host with the declared Go dependencies available:
ARCH=amd64 COMMIT=unknown ./scripts/build-rpm.sh
```

A clean local/CI candidate requires a reviewed clean commit and exact metadata.
An official release separately requires its matching tag and publication workflow;
published alpha.8 has a separate frozen source and hosted asset set. Local
build commands below do not create a tag or inherit release provenance.
Read and verify its metadata before setting the exact values for every build:

```bash
export COMMIT="$(git rev-parse HEAD)"
export BUILD_DATE="$(git show -s --format=%cI "$COMMIT")"
EXPECTED_COMMIT="$COMMIT" ./scripts/release-metadata.sh
./scripts/validate.sh
./scripts/build-release.sh --deb amd64
```

Use `--rpm amd64` on Fedora, or the supported `arm64` cross-build target.
Missing metadata, a mismatched commit/date, changed source, and existing official
DEB output or collected release directory are rejected. The release workflow reads these two metadata values once
and passes them to all package, SBOM, and collection jobs. Locally verify the
helper's output before building; the local command does not create a tag.

Ordinary local alpha.9 DEBs retain the filename
`noderampart_0.4.0~alpha.9_amd64.deb`; the clean build entry renames its own output to
`noderampart_0.4.0-alpha.9_amd64.deb`. Both have Debian version
`0.4.0~alpha.9`; these are unpublished development candidates. Current RPM is
`0.4.0-0.alpha.10%{?dist}`; project alpha numbering and RPM Release are distinct.
Earlier explicit mappings remain fixed. `VERSION` identifies alpha.9 and no
alpha.9 tag/Release is created by ordinary validation/package builds.
Published alpha.7 remains `0.4.0~alpha.7` / `0.4.0-0.alpha.8%{?dist}`. The frozen tag/source
and exact alpha.8 release bytes are recorded in [release verification](RELEASE_VERIFICATION.md#alpha8-publication-and-distribution-verification).
Ordinary local/CI packages remain experimental artifacts and do not inherit
release provenance merely by sharing a version or source tree. Bootstrap still
defaults to alpha.5. The alpha.8 public-bootstrap matrix remains incomplete;
see the publication record before selecting it. SBOMs use each package's native version, inspect its final bytes,
and bind the three program digests to those bytes. `--collect` requires the
complete package/SBOM/buildinfo set; the separate hosted draft step checks the
actual uploaded names and states after upload. Local checks do not establish
that the GitHub workflow, upload, or attestation ran.

CI's static binaries cover amd64/arm64; its package artifacts cover Debian
amd64 and Fedora 43/44 x86_64/aarch64. ARM64 packages are cross-built on the
amd64 runner; this does not prove native ARM64 execution. They can support isolated tests only after
the downloaded bytes, embedded version/commit and workflow source SHA are
verified. This subset is not the complete release set. The release workflow
builds all six runtime packages from its own verified commit and does not
silently reuse artifacts from another CI run. Record new acceptance against
the exact candidate source and actual package hashes; earlier dirty `Commit=unknown`
packages and VM evidence do not certify this candidate.

The ordinary test suite does not require root or packet-capture privileges.
Run `gofmt -w` on changed Go files before `make fmt-check`. The required direct
static-build check is `CGO_ENABLED=0 go build ./cmd/...`; `make build` also stamps
the three binaries with version and commit metadata. Packaging regressions use
temporary paths and mocked lifecycle commands; the native RPM case is skipped
when `rpmspec` is absent, so run it on Fedora as well.

CI and tag releases call the same safety workflow against their exact source
SHA. It scans full Git history and the checkout with checksum-verified
Gitleaks 8.30.1, upstream detectors, full redaction, and no inline/ignore-file
suppression. Release package builds and draft creation require that scan and
all seven bounded fuzz jobs to pass; a passing check on another commit is not
accepted. Local checks do not execute hosted workflows or publish drafts.

CI also builds Fedora 43/44 RPM/SRPMs for both architectures and runs seven separate bounded fuzz
jobs for SSH text, journal JSON/checkpoints, sensor IPC frames, configuration,
and Ethernet decoding. Each job mutates inputs for 30 seconds with two workers
and a two-minute command timeout. Run one locally with:

```bash
GOMAXPROCS=2 go test ./internal/collector -run='^$' \
  -fuzz='^FuzzParseSSH$' -fuzztime=30s -parallel=2 -timeout=2m
```

The other targets are `FuzzJournalRecord` in `internal/collector`,
`FuzzReadFrame` in `internal/protocol`, `FuzzLoadConfig` in `internal/config`,
and `FuzzDecodeEthernet` in `internal/sensor`. v0.3 also runs `FuzzDefaultRoute`
in `internal/collector` and `FuzzReplayMetadata` in `internal/replay`. Seeds include forged SSH log
fragments, journal field arrays, frame length overflow, trailing JSON,
unknown configuration fields, and unsafe path/duration values. Fuzz jobs use
synthetic data and temporary files; they do not read the host journal or emit
network traffic.

For a reproducible allocation/cost baseline, run
`go test ./internal/sensor -run='^$' -bench='^BenchmarkDecodeEthernet$' -benchtime=200ms -benchmem`.
The fixtures cover IPv4 TCP, an IPv6 noninitial fragment, and seven IPv6
extension headers followed by UDP. Record the Go version, CPU and architecture
alongside the output. These isolated decode costs exclude capture, scheduling,
aggregation, IPC and storage; they establish no supported packet rate.

The isolated capture fixture runs only as root in an explicitly authorized
disposable VM with Python 3 and iproute2 (no product capability changes):

```bash
CGO_ENABLED=0 go test -c -o /absolute/path/noderampart-sensor.test ./internal/sensor
sudo python3 scripts/test-netns.py --authorized-disposable-lab \
  --test-binary /absolute/path/noderampart-sensor.test
```

It creates two temporary network namespaces connected only to each other,
exchanges 20 TCP and 20 UDP request/reply pairs, and checks real AF_PACKET capture,
port attribution, UDP reply correlation, and absence of scan false positives.
The harness removes its own namespaces/processes on success or failure. It does
not alter host routes, services or firewall rules. Ordinary Go tests skip this
privileged case; `--self-test` checks harness guards without privileges. This
small correctness fixture establishes no throughput or sustained-load budget.

v0.3 adds `scripts/test-multi-interface.py --authorized-disposable-lab --test-binary
/absolute/path/noderampart-sensor.test`, also only inside an authorized disposable
VM. Two newly created namespaces exchange five UDP replies over each of two
isolated veth pairs. It checks multi-interface capture, IPv6-only route discovery,
route removal/restoration and capture cancellation, then removes its fixtures.

## Source and package transitions

Installers reject the other installation's binaries and full systemd units,
including installations whose services are stopped or disabled. Native package
preinstall accepts administrator masks pointing to `/dev/null` and preserves
drop-ins. A full custom unit must be reconciled explicitly because it overrides
the package unit. Source uninstall also refuses native package files.

Before changing installation type, create a consistent database backup and keep
the previous build available. New source installations record six SHA256
ownership entries: three binaries, two full units, and
`/usr/local/libexec/noderampart/manage-remove`. The transition also accepts an
older five-entry manifest containing the three binaries and two units; it
never deletes an unrecorded helper. Prepare a source-to-package
transition with the checked-out script, then install the reviewed package:

```bash
sudo ./scripts/source-to-package.sh --prepare
sudo apt install ./dist/noderampart_0.4.0~alpha.8_amd64.deb
sudo /usr/bin/noderampart doctor
sudo /usr/bin/noderampart status
```

On Fedora, use `sudo dnf install ./dist/rpm/noderampart-*.x86_64.rpm` for the
package step. The transition verifies every ownership hash before stopping
services and removes those five source files, the helper when recorded, and
their ownership manifest.
It preserves configuration, database state, and systemd drop-ins. Modified files,
symlinks, incomplete manifests, or services that cannot stop abort the operation.
The package's first-install policy determines subsequent service activation.

Source installations predating the manifest require the exact original source
build, including `bin/` and `packaging/systemd/`. Pass that retained checkout:

```bash
sudo ./scripts/source-to-package.sh --prepare \
  --legacy-checkout /absolute/path/to/original-build
```

Rebuilding the old version with different build metadata may produce different
hashes. A failed ownership check requires manual reconciliation; the script does
not infer ownership from a filename or run the installed executables to identify
them. Debian upgrades preserve current enable/disable state and restart only
active services through `deb-systemd-invoke`, which respects `policy-rc.d`.
Fresh administrator masks remain masked. The mocked packaging suite covers
these transitions and service-policy refusals.

## Sanitized disposable-lab snapshots

Use the local harness inside an explicitly authorized disposable VM after
performing a lifecycle or recovery scenario:

```bash
sudo ./scripts/lab-check.py --authorized-disposable-lab \
  --install-kind package --expected-version "$(cat VERSION)" \
  --expected-commit COMMIT_HEX --output /absolute/new/path/runtime-check.json
```

Use `--install-kind source` for a source installation and an existing safe
directory for the new artifact. The harness requires a VM and root, performs
read-only checks, and emits fixed check names with pass/fail values. It checks
build identity, daemon peer verification, unit locations, separate service
accounts, actual process capabilities, sandbox properties, IPC socket modes,
fresh sensor/persistence timestamps, journal-reader process state, and disabled
Telegram. It does not include hostnames, source IPs, tokens, raw configuration,
or journal text in its artifact.

Capture a snapshot after fresh install, upgrade, source/package transition, and
restart/resume scenarios. Record the operator's scenario and exact artifacts in
the validation report separately. A green snapshot proves current runtime
properties; it does not by itself prove cursor recovery, authentication rule
warmup, power-loss safety, throughput, or privileged package lifecycle behavior.
`python3 scripts/test-lab-check.py` tests artifact reduction using synthetic
fixtures without inspecting the host.

The public [current limitations](ALPHA_LIMITATIONS.md) and
[v0.4 operations](V0.4_OPERATIONS.md) describe the validation boundaries.
Historical v0.1/v0.2/v0.3 checks retain their original versions, dates and
artifact scope; they do not certify this candidate or replace the remaining
VM matrix below.

## Repository rules

- Do not add automatic firewall changes to an observer milestone.
- Do not accept executable rule languages, shell snippets, runtime-downloaded binaries, or user-controlled command templates.
- All packet, journal, IPC, MMDB, billing, and notification inputs require explicit size and cardinality limits.
- Any new privileged operation requires a threat-model update and a negative test.
- Data loss must increment a visible metric; it must never silently become a zero-value report.
- Keep CI actions SHA-pinned and workflow permissions read-only unless a reviewed release job requires otherwise.

## Required VM matrix

| Dimension | Values |
| --- | --- |
| Distribution | Debian 13, Debian 12, Fedora 44, Fedora 43 |
| Architecture | amd64 mandatory; real arm64 before beta |
| Network | IPv4, IPv6, one NIC, two NICs, reboot, link flap |
| Security framework | SELinux enforcing, AppArmor where configured |
| Firewall/runtime | nftables, firewalld, Docker, Podman |
| Lifecycle | fresh install, reinstall, upgrade, remove, purge |
| Sensor | available, permission denied, absent, overflow, malformed frames |
| Notifications | success, 401, 403, 429, 5xx, DNS/TLS failure, recovery |

Use an isolated management network and a separate no-NAT traffic network. High-rate work must never have a route to the public Internet. A single-host VM lab validates logic; credible throughput measurements require a separate generator host.

## Release checklist

1. `go mod verify`, formatting, vet, race tests, and all unit tests pass.
2. Static amd64 and arm64 binaries build with `CGO_ENABLED=0`.
3. Full Git history and artifacts pass a secret scan.
4. Dependency vulnerability and license review is recorded.
5. DEB/RPM lifecycle matrix and systemd sandbox tests pass.
6. Changelog, compatibility matrix, known limitations, checksums, SBOM, and provenance are attached.
7. No release is marked stable until the privileged VM matrix and soak exit criteria are met.

当前 `VERSION` 为 alpha.8；其 hosted prerelease 已独立公开，DEB 原生版本
`0.4.0~alpha.8`，RPM `0.4.0-0.alpha.9%{?dist}`，排序高于已发布 alpha.7。普通 local/CI 包仍是独立候选，
相同版本或源码树不使其继承正式 Release provenance。alpha.8 公开安装矩阵尚未闭环，
无参数默认仍 alpha.5；冻结来源、发布日期及验收范围见
[发布核验](RELEASE_VERIFICATION.md#alpha8-publication-and-distribution-verification)。
schema 13 扩展固定渠道约束及原生日报启用边界，保留 schema 12 消息呈现元数据；
配置/API 1、协议 v5 不变。回退需
匹配旧数据库备份和旧配置/密钥，不能原地降 schema。


For alpha.8, use [acceptance](ALPHA8_ACCEPTANCE.md) and the bilingual
[channel guide](NOTIFICATION_CHANNELS.md). Contract tests use synthetic protected
files and injected transports/resolvers/certificates, never real vendor endpoints.
The golden event matrix covers every notifyable kind and start/update/recovery
in en/zh; daily tests preserve archived timezone/DST and language. Keep MMDB
validator cancellation/descendant-exit regressions unchanged. Candidate builds
are not official release provenance, even when clean and exactly commit-stamped.

## alpha.8 no-tag release preflight

The [scoped preflight](ALPHA8_RELEASE_PREFLIGHT.md) retains its historical no-tag
stage. Source supports explicit alpha.8, now separately published; the default
remains alpha.5. For a future authorized local candidate, obtain source metadata
once as data; retain empty-value rejection, clean
source and matching COMMIT/BUILD_DATE across all six packages and pairs. Collect
exactly22 files locally; the external manifest includes SHA256SUMS itself, whose
contents cover only the other21 files. Do not run release.yml or create any tag
to test local tools. Ordinary actual-Fedora43/44 CI package builds may supply
matched-source local candidate inputs, without release provenance/attestations.

公开安装默认不变。文档/sourceRPM 内容变化必须核对新包字节；source53 的旧包不能
证明新包身份。最终冻结 main 和真实差量 VM 证据保存在仓库外，不为文档自 SHA 反复提交。
