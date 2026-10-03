# Verify a NodeRampart release

This document describes published `v0.4.0-alpha.8` and retains all earlier
release records. Examples pin alpha.8; frozen bootstrap still defaults to
alpha.5. Its lightweight tag resolves to
`77ae069b8f00651106b9621a24047b0ad7b4e88d`; DEB is `0.4.0~alpha.8` and RPM is
`0.4.0-0.alpha.9.fc43/fc44`. The source's original BUILD_DATE remains
`2026-10-01T23:59:34+08:00`; publication time is separate. Historical releases
require their own frozen source/tools and byte identities.

本指南面向已公开的非 latest alpha.8 prerelease，保留所有历史发行记录。示例明确
指定 alpha.8，bootstrap 默认仍为 alpha.5。当前 main 文档提交不改变上述发行源或
包字节；候选 CI 包和旧版本证明不能替代本次冻结发行字节。

The release workflow creates a draft for a maintainer to inspect; local workflow
edits and tests neither publish a release nor prove that hosted checks ran.

Remote policy recommendations require separate maintainer approval: require the
actual CI validation/build/package/fuzz check names observed in hosted runs
before merging main, prevent force-push/deletion of release tags, and review
the complete draft asset set before publication. Local scripts do not configure
GitHub branch/tag rules or approve a release.

## Published alpha.5 baseline

[v0.4.0-alpha.5](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.5)
was published as an alpha prerelease at `2026-09-29T14:50:47Z` (Release ID
`395132661`). Its source and tag are
`f539d18c9a91913a49e4c1d9f36d381965f2f7b7`; BUILD_DATE remains
`2026-09-23T22:52:52+08:00`, derived from that source commit.
The [release workflow](https://github.com/littlesho/NodeRampart/actions/runs/35931396244)
completed on attempt 1. All 22 assets passed anonymous re-download/checksum and
provenance verification; six SPDX attestations passed. See the
[acceptance scope](#alpha5-publication-and-distribution-verification) below.
These package identities remain pinned even when later documentation commits
advance main. The steps below verify a download before installation.

## What a release contains

Each of the six runtime packages has two matching files:

| Asset | Purpose |
| --- | --- |
| `PACKAGE.deb` or `PACKAGE.rpm` | Installable program and native package lifecycle. |
| `PACKAGE.deb.spdx.json` or `PACKAGE.rpm.spdx.json` | SPDX 2.3 inventory of the Go programs inside that exact package. |
| `PACKAGE.deb.buildinfo.json` or `PACKAGE.rpm.buildinfo.json` | Package/program SHA256, ELF architecture, embedded Go toolchain, modules and build settings. |

The runtime matrix is Debian `amd64`/`arm64` and Fedora 43/44
`x86_64`/`aarch64`. One Fedora 44 source RPM is also included. It is a source
asset and has no runtime SBOM. `bootstrap.sh`, `release.json` and `SHA256SUMS`
complete the download set.

For alpha.8, public DEB names are
`noderampart_0.4.0-alpha.8_ARCH.deb`, with native Debian version
`0.4.0~alpha.8`. RPM names contain `0.4.0-0.alpha.9.fc43` or
`0.4.0-0.alpha.9.fc44`; the source RPM uses Fedora 44. SBOM/buildinfo names,
checksums and attestations bind the final public filename and actual bytes.
The [alpha.8 distribution record](#alpha8-publication-and-distribution-verification)
identifies the actual hosted run, source and measured native acceptance; the
format description alone is not runtime proof.

The SBOM covers only the three packaged Go programs. It does **not** inventory
runtime system dependencies, inspect your installed host, provide a complete
function-call dependency graph or prove that a component is vulnerability-free.
Automatic remote and local-cache license enrichment is disabled; missing
license conclusions are not evidence of missing bundled license notices.

`declared_build` in each inspection JSON records the version, commit and build
date supplied by the build workflow. These fields are build declarations.
The current `-trimpath -buildvcs=false` binaries do not independently expose the
project's custom `-X` version fields through Go buildinfo. The ELF architecture,
Go version and module data are read from the actual packaged files. Cross
architecture inspection does not execute ARM64 binaries or replace ARM64
runtime testing.

Official release builds and collection require the declared full commit to equal
the checkout's HEAD and reject tracked or untracked source changes. The fixed
`release-input` directory is reserved for downloaded workflow artifacts and is
not treated as build source. CI and release required validation share
`scripts/validate.sh`, including uncached tests, race, coverage, static builds,
packaging/bootstrap/SBOM regressions and pinned `govulncheck@v1.7.0`.

Secret scanning and seven bounded fuzz targets run separately in the shared
`.github/workflows/safety.yml` workflow. Both CI and release invoke it for their
own exact `github.sha`; package jobs and draft creation wait for these results.
The release validation job verifies the version tag and records full commit and
commit timestamp once. Packages, SBOM declarations and collection consume that
same metadata. A local `validate.sh` PASS does not mean the hosted secret/fuzz
jobs, upload checks or attestations ran.

秘密扫描与七项有界 fuzz 在共享 safety workflow 独立执行；CI 与 release 均绑定
各自的准确 `github.sha`，构建和 draft 必须等待这些检查。发行验证一次确定完整提交
及其提交时间，包、SBOM 与收集沿用同一元数据。本地验证不代表远端检查或发布已执行。

## Download and verify

Use a current [GitHub CLI](https://cli.github.com/manual/gh_attestation_verify)
with artifact attestation support. Select the full commit SHA from the release
source you have reviewed; do not treat a value downloaded beside the package as
independent evidence of the expected source.

The example selects published `v0.4.0-alpha.8`, whose reviewed source is
`77ae069b8f00651106b9621a24047b0ad7b4e88d`. Confirm that identity against the
public source before using it as an expected value. Missing statements cannot
pass verification; do not substitute another release's attestations.

```sh
VERIFY_DIR=$(mktemp -d)
gh release download v0.4.0-alpha.8 --repo littlesho/NodeRampart --dir "$VERIFY_DIR"
cd "$VERIFY_DIR"
sha256sum -c SHA256SUMS

PACKAGE=noderampart_0.4.0-alpha.8_amd64.deb
EXPECTED_COMMIT='77ae069b8f00651106b9621a24047b0ad7b4e88d'

# Verify the package's provenance against the intended repository/workflow/tag.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.8 \
  --source-digest "$EXPECTED_COMMIT" \
  --signer-digest "$EXPECTED_COMMIT" \
  --predicate-type https://slsa.dev/provenance/v1 \
  --deny-self-hosted-runners

# Verify the SBOM statement bound to the same package bytes.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.8 \
  --source-digest "$EXPECTED_COMMIT" \
  --signer-digest "$EXPECTED_COMMIT" \
  --predicate-type https://spdx.dev/Document/v2.3 \
  --deny-self-hosted-runners

# The separately downloaded inventory also has its own provenance statement.
gh attestation verify "$PACKAGE.spdx.json" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.8 \
  --source-digest "$EXPECTED_COMMIT" \
  --signer-digest "$EXPECTED_COMMIT" \
  --predicate-type https://slsa.dev/provenance/v1 \
  --deny-self-hosted-runners
```

The CLI may use your existing GitHub login; its download command alone does not
prove anonymous access. The publication record below identifies the separate
clean-client anonymous test.

Replace `PACKAGE` with the exact package for your distribution and architecture.
Keep checksum failures, missing attestations and provenance mismatches as
failures. SHA256 downloaded from the same release checks consistency; the
attestation identity/source constraints provide a separate workflow identity
check. A valid signature authenticates a workflow statement, not the correctness
or completeness of all its contents. Review the release's validation record.
The bootstrap verifies HTTPS downloads, checksums and native package identity;
it does not perform these GitHub attestation checks automatically.

## Maintainer draft verification

The following procedure applies before publication of a future candidate;
alpha.8 is already published. Select that candidate’s independently reviewed
version and source identity. A local candidate can be built without a tag using
`EXPECTED_COMMIT=<full-main-SHA> ./scripts/release-metadata.sh` and passing its
`commit` and source-commit `build_date` to `build-release.sh`. Do not substitute
the local wall clock or claim local artifacts have GitHub attestations.

A draft remains unpublished even if an authenticated maintainer can download it.
Use the same `gh release download` and attestation commands above with an account
that can read the draft. Record its numeric Release ID, tag, full source commit,
workflow run ID and attempt before verification. The tag must resolve to the
reviewed main commit and match `v$(cat VERSION)`; do not move an existing tag.

For frozen alpha.7, the exact asset set is 22 files:

- `noderampart_0.4.0-alpha.7_amd64.deb` and `noderampart_0.4.0-alpha.7_arm64.deb`.
- `noderampart-0.4.0-0.alpha.8.fc43.x86_64.rpm`,
  `noderampart-0.4.0-0.alpha.8.fc43.aarch64.rpm`,
  `noderampart-0.4.0-0.alpha.8.fc44.x86_64.rpm`,
  `noderampart-0.4.0-0.alpha.8.fc44.aarch64.rpm`.
- Each of those six runtime filenames plus `.spdx.json` and `.buildinfo.json`.
- `noderampart-0.4.0-0.alpha.8.fc44.src.rpm`.
- `bootstrap.sh`, `release.json`, `SHA256SUMS`.

Check names and identities, not only the count. GitHub's generated source ZIP/TAR
links are not runtime assets. SHA256SUMS lists the other 21 files, not itself.
Verify `release.json` version/commit/counts, native package version/architecture,
ELF/Go metadata and package/program digests against the corresponding buildinfo
and SBOM. `scripts/release_sbom.py` supplies read-only inspection and pair checks;
never install or execute a downloaded target program as an identity check.
Verify provenance for all assets, including buildinfo, source RPM and bootstrap,
and verify each runtime package's SPDX attestation with the same repository,
workflow, tag and independently reviewed commit constraints above.

Keep the Release as **draft + prerelease** until these checks and the stated
validation scope are reviewed. Record actual test results and unrun lifecycle
or architecture cases in its notes; do not substitute historical private lab
results for new tag acceptance. Public download 404s while the draft is private
must be recorded as unavailable, not checksum or provenance passes. Publish the
same verified draft only after a separate decision to make its assets public.

## Build-time generation

The release workflow scans final DEB/RPM artifacts on a dedicated Linux amd64
runner. It verifies native package identity, safely extracts only the three
regular program files into a private directory, and checks both ELF and Go
architecture metadata before running Syft. No target program is executed.
The collector requires all six package/SBOM/inspection triples and compares
package and program digests before it creates `dist/release`.

For a reviewed local alpha.7 candidate package, with Go 1.26.8 and the appropriate read-only
`dpkg-deb` or `rpm`/`rpm2cpio` inspection tools available:

```sh
TOOLS_DIR=$(mktemp -d)
python3 scripts/release_sbom.py --fetch-syft "$TOOLS_DIR/syft"
COMMIT=unknown
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
export COMMIT BUILD_DATE
make build
./scripts/build-deb.sh
python3 scripts/release_sbom.py dist/noderampart_0.4.0~alpha.7_amd64.deb \
  --syft "$TOOLS_DIR/syft/syft" --output dist/sbom
```

This local example records its declarations before building. Supply the actual
recorded build declarations when inspecting a package built elsewhere, rather
than deriving them from the inspecting checkout. Ordinary dirty local builds
default to literal `COMMIT=unknown`;
that does not certify a source revision and cannot pass the release collector's
required hexadecimal commit declaration. Distribution Go version suffixes are
retained as read from the binary. The helper refuses existing output files; retain or remove the
dedicated output before regenerating. Generating files does not upload them or
create attestations. Observer installation does not require Syft, Cosign or Go.

Source RPM inputs come from [the explicit source manifest](../packaging/source-files.txt).
Every entry must be a regular relative file with no symlinked parent. Add new
approved product files explicitly; private guidance, `.env` files, local caches
and unknown files are not copied. The same manifest works in an extracted
source tree without Git. Module verification and vendoring run in the fresh
source staging tree.

## Pinned Syft tool and upstream verification

Generation uses [Syft v1.51.1](https://github.com/anchore/syft/releases/tag/v1.51.1)
as a separate build tool. It adds no dependency to NodeRampart's Go module.
The downloader fixes the upstream asset URL and both SHA256 values:

```text
syft_1.51.1_linux_amd64.tar.gz
8fcb33017a0dc1058298c923c436d19dfa68ae93968e0b423248542e3afb9fc3
extracted syft executable
abca2def61de9952fa06d3977bb1e064818facb9badfce502b450d3d6846a91f
```

Before fixing these digests, the upstream checksum file's signature was checked
using Cosign v3.0.5 and Anchore's
[official verification procedure](https://oss.anchore.com/docs/installation/verification/):

```sh
cosign verify-blob syft_1.51.1_checksums.txt \
  --certificate syft_1.51.1_checksums.txt.pem \
  --signature syft_1.51.1_checksums.txt.sig \
  --certificate-identity https://github.com/anchore/syft/.github/workflows/release.yaml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c syft_1.51.1_checksums.txt
```

The signature verification returned `Verified OK`; the archive matched the
signed checksum. The upstream release workflow signs from `refs/heads/main`,
not from a tag identity. Cosign's standalone Linux amd64 executable was obtained
from its official v3.0.5 GitHub release and matched the release API's SHA256
`db15cc99e6e4837daabab023742aaddc3841ce57f193d11b7c3e06c8003642b2`.
This is the recorded trust bootstrap, not a claim of independently bootstrapped
Cosign verification. Updating Syft requires repeating upstream verification,
reviewing the scanner configuration and updating its pinned digests together.

During scans, explicit configuration disables update checks, remote license
lookups, host module/vendor cache searches, version guessing and Go packages-lib
execution. Only the Go binary cataloger is selected. See the
[fixed-version configuration implementation](https://github.com/anchore/syft/blob/v1.51.1/cmd/syft/internal/options/golang.go)
and [GitHub attestation action](https://github.com/actions/attest) for the upstream
interfaces used here.

## Candidate metadata and package revision

The `v0.4.0-alpha.2` prerelease replaces two unpublished candidates. The immutable
`v0.4.0-alpha` build failed; the `v0.4.0-alpha.1` draft has a filename/checksum
mismatch because the upload changed tilde-containing names. Neither is a
published installation release. Do not repair alpha.1 by renaming downloads.
For that historical alpha.2 release, the project version is `0.4.0-alpha.2`, Debian version `0.4.0~alpha.2`, and
RPM Version/Release `0.4.0` / `0.alpha.3%{?dist}`. Program version strings keep
`0.4.0-alpha.2`. The validation job checks checkout HEAD against the expected
source commit (peeling a tag object if necessary), then sends that full SHA and
the commit's timestamp to DEB, RPM, SBOM and collection jobs through direct
job dependencies. Release builds reject absent or invalid metadata; they do
not substitute the current time. When building locally, explicitly export
`COMMIT=$(git rev-parse HEAD)` and
`BUILD_DATE=$(git show -s --format=%cI "$COMMIT")` from the reviewed checkout.

Public asset names use only letters, digits, dots, underscores and hyphens,
with no leading/trailing dot. In particular, the DEB download name uses
`0.4.0-alpha.2`, while its internal dpkg Version remains `0.4.0~alpha.2`.
The installer checks both the SHA256 and native Package/Version/Architecture;
these are separate from the download name. SPDX source package version and
buildinfo `package_identity` retain native package identity; `declared_build`
records the full project version and source metadata.

After upload, the workflow reads the draft and its paginated asset list and
requires the exact 22 expected names in `uploaded` state. Maintainer review
then downloads those actual names into a fresh directory using authenticated
API access, runs `sha256sum -c SHA256SUMS` without renaming, and checks package
contents and attestations. Drafts are not anonymously downloadable at the
fixed-version links; public download and installation checks remain separate.

## alpha.3 draft verification

`v0.4.0-alpha.3` includes PR #11's terminal UTF-8 fix and was published on
2026-09-20 at 05:39:54 UTC. Release ID `392320863`, source commit
`fc5630f398b622a98fd7f062efe8c1505ec3424e`, and Release run `35490792825`
(attempt 1) were verified before publication. The prior draft checks used
authenticated downloads; subsequent anonymous downloads matched all 22 verified
asset byte streams without renaming. Its 22 provenance and six SBOM attestation
checks remain valid for those identical bytes. The exact set remains 22 assets: DEBs
`noderampart_0.4.0-alpha.3_{amd64,arm64}.deb`, Fedora 43/44 RPMs
`noderampart-0.4.0-0.alpha.4.fc{43,44}.{x86_64,aarch64}.rpm`, their six
SBOM/buildinfo pairs, `noderampart-0.4.0-0.alpha.4.fc44.src.rpm`, and
`bootstrap.sh`, `release.json`, `SHA256SUMS`. Debian internal Version is
`0.4.0~alpha.3`; the embedded project version is `0.4.0-alpha.3`.

Require the new tag's full commit, commit-derived BUILD_DATE, actual Release run
and original downloaded names throughout checksums, package identity, payload,
SBOM/buildinfo and both attestation types. Use `refs/tags/v0.4.0-alpha.3` and
that tag's independently verified source SHA for the attestation commands.
The collector/SBOM tooling on the alpha.3 tag targets alpha.3; inspect that release
with the tools from its matching tag. Do not rename assets or reuse old proofs.

alpha.3 已公开：此前通过认证验收草稿的全部 22 个资产和 28 项证明；
公开后再次按原名匿名下载，全部字节与已验收内容一致，SHA256SUMS 直接通过。
源码伪终端测试、包内二进制交互检查、真实安装生命周期和用户 SSH 客户端/
字体实测是不同的验收项目；未执行的项目不能写成通过。

Actual x86_64 CLIs extracted from the Debian package and Fedora 43/44 RPMs
passed 12 bounded C/POSIX × setup/tui cases with Chinese → English → Chinese
output in an isolated Debian 13 VM. These were package-binary checks, not
Fedora OS or package installation lifecycle tests. The 13 source PTY scenarios
use a test subprocess; Chinese input and output-length boundaries are covered
by source regressions. ARM64 hardware and the user's SSH client/font remain
unverified.

在隔离 Debian 13 VM 中，从 DEB 和 Fedora 43/44 RPM 提取的实际 x86_64 CLI
通过 12 项 C/POSIX、setup/tui 与中英切换检查；这不是 Fedora 系统或安装生命周期
验收。13 项源码伪终端场景使用测试子进程；中文输入和长度边界依据源码回归。
ARM64 实机及用户具体 SSH 客户端/字体尚未验证。


## alpha.4 publication verification

Release `392429426` was published at `2026-09-20T13:02:35Z` from tag
`v0.4.0-alpha.4`, source `cd61b30a622e0d2c65b398db8c24d2dcea7519f0`,
and Release run `35511091913`, attempt 1. Its 22 public assets were downloaded
anonymously under their original names and matched all accepted draft bytes.
`sha256sum -c SHA256SUMS` passed without renaming. Existing package-content,
source-RPM and 28 attestation results therefore apply to those same bytes.
The exact asset set contains two `noderampart_0.4.0-alpha.4_{amd64,arm64}.deb`, four
`noderampart-0.4.0-0.alpha.5.fc{43,44}.{x86_64,aarch64}.rpm`, six SBOM/buildinfo
pairs, `noderampart-0.4.0-0.alpha.5.fc44.src.rpm`, `bootstrap.sh`, `release.json`
and `SHA256SUMS`. Debian's internal Version is `0.4.0~alpha.4`; the embedded
program version is `0.4.0-alpha.4`.

Bind all 22 provenance and six SBOM attestations to `refs/tags/v0.4.0-alpha.4`,
its complete source commit and the actual Release workflow run. Verify package
contents and source-RPM manifest bytes against that commit, including PR #14's
GeoIP implementation and tests; no temporary diagnostic entry, database or
private report belongs in either package. A successful candidate `.test` binary
is not a substitute for inspecting new release artifacts. Reusing algorithm
and real-sample evidence does not establish new-package download, activation,
scheduling, installation lifecycle or ARM64 runtime acceptance.

alpha.4 已公开，22 项资产已按远端原名匿名下载并直接通过校验和，字节与验收草稿
一致，复用对应包内容、源码及 28 项证明。已有候选 City/ASN 离线通过仅作为算法
证据；本版本包内程序未运行，用户真实下载、激活、每日更新、安装生命周期及
ARM64 实机仍未验收。

## alpha.5 publication and distribution verification

The public bytes match the accepted assets: 22 filenames, asset IDs, sizes and
SHA256 values were unchanged across publication. Fresh anonymous downloads used
fixed-version public entry URLs with TLS validation and no GitHub credentials
or cookies. SHA256SUMS checked its 21 entries; the checksum file itself matched
its sealed digest and provenance. All 22 provenance and six SPDX attestations
were reverified on these bytes against repository, signer workflow, source ref,
source SHA, workflow run/attempt, predicate and subject digest constraints.
SPDX predicates matched the downloaded SBOMs. Proof retrieval used GitHub
authentication separately from anonymous asset transfer.

Debian 12/13 amd64 and Fedora 43/44 x86_64 passed the recorded native/package
lifecycle matrix. Schema 6→7, backup/restore, checkpoint transactions and journal
recovery across subprocess and whole-daemon restarts have scoped acceptance.
The Debian controlled instance-service socket path and Fedora 43/44 vendor
`sshd.socket`/`sshd@.service` loopback port-22 configurations passed; Fedora stayed
SELinux Enforcing. This does not cover arbitrary custom ports, aliases or PAM
variants. Fedora 43 socket-specific pending observations lasted five seconds;
the separate native service recovery scenarios used full 135-second windows.

Public bootstrap installation separately passed on Debian 12.15 (systemd
252.39-1~deb12u2, OpenSSH 9.2p1 Debian-2+deb12u10) and Fedora 43 Server (systemd
258-1.fc43, OpenSSH 10.0p2, SELinux Enforcing, firewalld active). Each guest newly
downloaded `bootstrap.sh` from the public alpha.5 URL, matched SHA256
`c0064f9f2763ba5d2a26031a3e25c5ea6621e06f4a217998542ac610ade60fd4`, and ran
`--version v0.4.0-alpha.5 --no-setup`. Observed bootstrap package bytes matched:

| Package | SHA256 |
| --- | --- |
| `noderampart_0.4.0-alpha.5_amd64.deb` | `19d47a3d2b8635133be8fec3b1c431bde582b6eba41a3fbb7735df9350503127` |
| `noderampart-0.4.0-0.alpha.6.fc43.x86_64.rpm` | `f50be0c9bc8f8fffc385b4e8ea853a389f7fa3ac49b8c5943f40529fd04664da` |

The Debian internal version is `0.4.0~alpha.5`; RPM `0.4.0-0.alpha.6.fc43` is the
package ordering mapping, not an alpha.6 program. Installed CLI and running
daemon version, commit and build date matched the frozen identity above; binary
hashes matched buildinfo. Original service identity/sandbox, control sockets,
journal access and disabled notifications passed. Unsupported version and
unavailable download failed closed; detected architecture and selected package
matched. The full earlier lifecycle/recovery matrix was not rerun for this step.

Only standard process-local variables pointed to an approved existing proxy;
TLS validation and official repositories remained enabled. Debian's first
attempt failed on an unavailable installation-media APT source; that failure
was retained, the media source was temporarily disabled, and official HTTPS
sources were preserved before successful installation. Guests without a default
route used the documented explicit sensor interface setting. No bootstrap,
package, SSH/PAM, firewall or SELinux policy changes were made. Both guests were
restored to their preserved clean baselines and stopped.

ARM64 native remains **NOT RUN**, required before beta. Schema 6 migration
cannot reconstruct previously lost pending state; no-known-pending is not
historical confirmed health. Historical gaps are not described as backfilled.
Transaction/process-crash checks do not guarantee arbitrary host-power-loss
durability. This remains an alpha prerelease, not production readiness.


## alpha.6 publication and distribution verification

[v0.4.0-alpha.6](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.6)
(Release ID `400047056`) was published at `2026-09-30T18:54:24Z`
(`2026-10-01T03:54:24+09:00`, Asia/Tokyo). It remains an alpha prerelease,
`draft=false`, `prerelease=true`, and was not marked latest. Tag/source is
`4d204b43499ca2f41c15b92334537d57bfc8b20c`; embedded BUILD_DATE is the source
commit timestamp `2026-09-30T20:19:52+08:00`, not publication time.
[Main CI](https://github.com/littlesho/NodeRampart/actions/runs/36714019589)
passed on that exact source, and the
[release workflow](https://github.com/littlesho/NodeRampart/actions/runs/36716260742)
passed on attempt 1. Later documentation commits do not move this tag or change
its package identity.

All 22 assets passed fresh anonymous downloads, exact size/SHA256 checks and
`SHA256SUMS` (which lists the other 21 assets). Native identities, all six
runtime packages' 18 actual ELF/Go/buildinfo digests, `release.json`, bootstrap
and the source RPM were rechecked. Those identical bytes reuse the earlier
independently authenticated verification of all 22 provenance statements and
six package-specific SPDX attestations, bound to this source/tag/workflow/run.
Anonymous file download and the authenticated attestation queries are separate
checks. Sidecar provenance is not a substitute for the runtime package's SPDX
statement; the Fedora 44 SRPM is source, not a runtime SPDX subject. The SPDX
scope is the three packaged Go programs, not operating-system dependencies.

The amd64/x86_64 packages used by native acceptance have these exact SHA256s:

| Runtime package | SHA256 |
| --- | --- |
| `noderampart_0.4.0-alpha.6_amd64.deb` | `959d5beb5c537dcce0f809f9bcd5d59302c087758e7011c27ca1fb9f65854ef6` |
| `noderampart-0.4.0-0.alpha.7.fc43.x86_64.rpm` | `4e1de47b1f43d08c9b3bfafd0ee346693fb9e922e1045ce9b068840d40215576` |
| `noderampart-0.4.0-0.alpha.7.fc44.x86_64.rpm` | `dc2333662dcebfa132d5fca115caca83a0141a6211c77ffc19d22af667217d5d` |

Actual published alpha.5 → these final packages, schema 7 → 11, and package
lifecycle checks passed on Debian 12/13 and matching Fedora 43/44 x86_64.
Fixtures, test-merge packages and final installed products remain separate
claims; detailed preserved first failures and rollback limits are summarized in
the [acceptance record](ALPHA6_ACCEPTANCE.md#publication-and-distribution-2026-10-01).
The published packages' README, project/license notices and bundled license
files match the frozen [public source](https://github.com/littlesho/NodeRampart/tree/4d204b43499ca2f41c15b92334537d57bfc8b20c).
Their documentation links resolve there; the runtime packages do not install a
complete offline `docs/` manual. Later documentation does not replace these
frozen package bytes.

Public bootstrap separately passed on Debian 13 and Fedora 44: download and
inspect the public alpha.6 script, run `--version v0.4.0-alpha.6 --no-setup`,
then open actual `sudo noderampart setup`. Normal APT/DNF dependencies, package
identity, installed programs, basic collection, advancing committed watermarks
and consistent online backups passed. Debian default-auto/enabled observation
passed. Fedora's RPM follows host presets by source mechanism; actual setup /
Services Start, basic readiness and process-executable identity passed.
Independent native preset/unit-flag samples before and after Start, and
boot-time enable state, were **NOT RUN**.
Both strict doctor snapshots remained **unknown / exit 2**; Fedora retained
`auth_window_warmup` and transient `sensor_commit_unavailable`. Later status and
backup snapshots show actual progress, not a rewrite of the strict result.
The curl-pipe interactive installation path was **NOT RUN** in this check.
The frozen bootstrap default remains alpha.5 unless `--version` is explicit.

The first anonymous `release.json` inspection caller hit SIGXFSZ because its
response-header limit was too small; the original FAIL was preserved. Only that
caller's header allowance was corrected to 64 KiB, with the exact bounded body
limit retained; the single missing download then passed. Both bootstrap guests'
initial direct GitHub connection failures were retained; scoped standard proxy
variables allowed the same public URLs with normal TLS validation, without
changing guest network/global configuration. Fedora's post-Save inspection
assumed an omitted `interfaces` field was present and failed; continuation did
not replay the completed Save. These are caller/environment results, not
confirmed product defects.

Publication bootstrap cleanup: **PASS**. Both actual removal helpers purged
successfully; separately retained evidence/backups were checked, owned processes
exited and task leases/locks were released with lock inodes preserved. Fixed
soft-stop and independent status queries confirmed both guests **STOPPED**.
Known-pending SSH recovery on independent copies passed in two tested VMs;
natural record-quality pending onset was not proved, so the natural chain is
**BLOCKED** and dependent validation **NOT RUN**. Fedora first-control timing,
native ARM64, VM race, sustained pressure, 72-hour soak, real credentialed MMDB
and actual Telegram/Webhook/heartbeat receivers remain **NOT RUN**.

alpha.6 已公开为预发布版，22 个资产匿名下载与校验通过；字节与之前认证查询的
22 项 provenance、6 项包级 SPDX 一致，文件匿名下载不冒称匿名完成认证查询。
Debian 13/Fedora 44 的固定版本 `--no-setup` 与单独 setup、基本采集/一致备份通过；
两台 strict 快照仍 unknown/2，保留 Fedora 的预热/瞬时提交不可用原因。
自然 SSH 恢复链 BLOCKED，真实 ARM64、VM race、72 小时、持续压力、真实 MMDB
及外发 NOT RUN。包文档保持冻结源码快照，不因后续文档 PR 修改 tag 或包字节。

## alpha.7 publication and distribution verification

[v0.4.0-alpha.7](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.7)
was published at **2026-10-01T08:33:06Z UTC**
(**2026-10-01T17:33:06+09:00 Asia/Tokyo**), Release `400735539`, from
`d164978433b5e49d68d310cf8d6f5819b855e2e0`; BUILD_DATE remains `2026-10-01T14:28:45+08:00`.
[Main CI 36824986470](https://github.com/littlesho/NodeRampart/actions/runs/36824986470)
passed 16 jobs on attempt 1; the
[tag-triggered release 36829587721](https://github.com/littlesho/NodeRampart/actions/runs/36829587721)
passed all 17 jobs on attempt 1. It remains an alpha prerelease,
not beta/stable/latest. Older tags, releases and asset bytes remain unchanged.


The two public-bootstrap VM packages are identified by these exact hashes;
ordinary CI/local packages sharing the version are separate artifacts.

| This publication's runtime package | SHA-256 |
| --- | --- |
| `noderampart_0.4.0-alpha.7_amd64.deb` | `d8ff0ca0ca5a9741ca31b698a41efd32f756783cacc9c7b80ba4f530436a71fb` |
| `noderampart-0.4.0-0.alpha.8.fc44.x86_64.rpm` | `13ef12011d3f49cd9edb0c02abea9dfe331632f28b2350cac21f896f4d0d13c2` |

All 22 assets were freshly downloaded without GitHub auth, cookies/netrc or curl
configuration from fixed tag URLs. API IDs/sizes/digests and SHA256SUMS matched
exactly. Authenticated 22 provenance and six signed package-SPDX results apply
only because every anonymous file matches the certified subject bytes. Proof
queries were authenticated; these file downloads were anonymous. Six native
package/18 program inventories and helper/unit/document bytes also matched.
Sidecar provenance is distinct from package-SPDX, whose scope excludes runtime
system dependencies. The SRPM is source-only. Packaged README/licenses/notices
remain the C snapshot; later documentation cannot change installed bytes or
supply a complete offline manual.


First caller/connection failures remain retained: four early Debian readiness
probes returned exit 255; stderr was not preserved, so their cause is unknown.
Later readiness succeeded. Fedora's first TCP connection timed out at eight
seconds; one bounded continuation succeeded without repeating start/install.
Additional read-only field/format assertions needed caller corrections; their
first failures remain retained, and successful install, Save and Start actions
were not replayed.

保留首轮调用/连接失败：Debian 前四次启动就绪探测退出 255，未保存 stderr，原因
不能判断；后续就绪通过。Fedora 首次 TCP 连接八秒超时，一次有界续作成功，不重放
启动或安装。另有只读字段/格式断言需要修正调用方，首轮失败保留，未重放已成功
的安装、Save 或 Start 动作。

| Current alpha.7 acceptance | Result |
| --- | --- |
| Anonymous 22 downloads / API / SHA256SUMS | PASS |
| Same-byte reuse: 22 provenance + 6 runtime SPDX | PASS |
| Debian 13 public bootstrap / actual setup / collection / backup / cleanup | PASS: fixed-version download, separate 64-column setup, real advancing watermarks, verified schema-12 online backup and installed-helper purge |
| Fedora 44 public bootstrap / actual setup / collection / backup / cleanup | PASS: fixed-version download, separate 64-column setup, real advancing watermarks, verified schema-12 online backup and installed-helper purge; SELinux Enforcing |
| Strict doctor snapshots | unknown / exit 2 on both: Debian 13 `auth_window_warmup` + `sensor_commit_unavailable`; Fedora 44 only `sensor_commit_unavailable` |
| Own-process/lease cleanup and final soft-stop state | PASS: installed-helper purge, verified exports, own processes exited, preserved/released locks, bounded soft stops; both STOPPED |

Both installed 64-column menus saved and reread Asia/Shanghai and Telegram
Chinese without enabling external senders or changing the system timezone. Debian
retained automatic interface selection. Fedora started from disabled presets;
explicit Services Start enabled and started both units, observed active/enabled
afterward. Boot execution was NOT RUN. Later committed-watermark progress and
consistent backups do not turn the earlier strict snapshots healthy.

Publication uses the same frozen bootstrap: default alpha.5, explicit
`--version v0.4.0-alpha.7 --no-setup`, then separate `noderampart setup`.
The interactive curl-pipe path is NOT RUN. Prior test-merge CI packages at
`8b2f70c1883533a967bee8e2d3357aabfafa86e3` have the same source tree but different
embedded commit/date and bytes; they do not substitute for final C runtime
acceptance. Earlier actual old queues were empty; nonempty backlog/claims/
old-body retry/target/privacy/language interactions remain same-C synthetic
SQLite/mock-HTTP coverage, not observed real-backlog delivery.

The first PR Replay fuzz deadline failure and one same-parameter failed-job
rerun remain recorded; the cause is unproved. This publication's first-attempt
main/release successes are separate. Natural SSH pending onset/recovery remains
unproved/BLOCKED. Native ARM64, current alpha.7 Debian12/Fedora43 runtime, VM race,
sustained pressure, 72-hour soak, real MMDB and all optional outgoing targets
remain NOT RUN. Current-instant DST displays do not prove both-season VM clock
changes. Unknown strict results must not become healthy claims.

alpha.7 于上述 UTC/东京时间从固定 C 发布为预发布版，main/release 首次全部通过；
22 个文件真实匿名下载及校验通过，原 28 项认证证明仅因完全同字节复用。两台公开
bootstrap/64 列实际 setup/采集/在线备份/已安装 helper purge 与清理均 **PASS**，
最终均 STOPPED。两台保存并重读 Asia/Shanghai 与 Telegram 中文，外发保持禁用。
strict 快照均 unknown/退出码 2：Debian 13 为 `auth_window_warmup` 与
`sensor_commit_unavailable`；Fedora 44 仅为 `sensor_commit_unavailable`。Fedora
初始 preset 为 disabled，实际 Services Start 后两项服务 enabled/active；开机执行
NOT RUN。后续真实提交水位推进不改变原 strict 快照。默认仍 alpha.5，明确选 alpha.7 并分开运行 setup，
不宣称管道交互路径、真实外发、原生 ARM64 或自然 SSH 恢复链通过。旧 alpha.6
发行历史、资产及 C 包内文档字节不变；本次纯文档更新不重新发行安装包。

## Alpha8 publication and distribution verification

### Fixed public identity

[v0.4.0-alpha.8](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.8)
was published as an alpha prerelease at **2026-10-02T05:42:28Z**
(**2026-10-02T14:42:28+09:00**, Asia/Tokyo), numeric Release ID **401526101**.
The original lightweight tag's ref object is a commit, with no separate
annotated tag object:

| Identity | Frozen value |
| --- | --- |
| Release source / tag commit | `77ae069b8f00651106b9621a24047b0ad7b4e88d` |
| Source tree | `2e9d194173efa97b757dc19dba35999038f2d7ea` |
| BUILD_DATE, original source string | `2026-10-01T23:59:34+08:00` |
| Hosted workflow | [release.yml run 36960260337, attempt 1](https://github.com/littlesho/NodeRampart/actions/runs/36960260337/attempts/1) |
| Workflow event / ref | `push` / `refs/tags/v0.4.0-alpha.8` |
| Project / DEB | `0.4.0-alpha.8` / `0.4.0~alpha.8` |
| RPM | `0.4.0-0.alpha.9.fc43/fc44` |
| Config / API / sensor / DB | 1 / 1 / 5 / 13 |

The existing numeric-ID draft was published once with explicit original tag,
full source, `draft=false`, `prerelease=true`, `make_latest="false"`. Immediate
readback preserved all 22 original asset IDs, names, sizes and SHA256 values.
Latest returned HTTP 404 before and after publication; alpha.8 was not made
latest. Publication did not rerun the release workflow, create or move a tag,
replace/upload assets, resign statements, change repository governance or deploy
production. Later main documentation differs from the frozen release source;
no package or tag snapshot was updated to include this record.

### Anonymous bytes and proof reuse

All **22 new anonymous downloads** passed, including exact original names,
lengths and SHA256 against the independently retained hosted draft inventory.
Anonymous public Release-ID/tag metadata and the web page were accessible.
The first direct download round failed on TCP connections; three completed
files and the failed request journal were retained. One separate round using
the existing administrator's local HTTPS CONNECT proxy passed all 22 files.
No GitHub Authorization, cookie, netrc or automatic client configuration was
used; default CA and destination hostname checks remained enabled. No network,
proxy, DNS, hosts or TLS configuration was changed. Proxy administrator/system
CA trust remains part of that test; local direct-IP checks do not constrain
proxy DNS. This download path does not alter native notifications' direct-only
transport.

`SHA256SUMS` covers exactly **21** files, excluding itself; the independent
inventory includes all **22**. All six runtime package/SPDX/buildinfo pairs,
release.json's exact source/version/original date, and bootstrap's bytes against
the frozen source passed. The source RPM, package contents, all 18 actual
program identities and ARM64 static inspection retain their prior hosted
acceptance by identical package bytes; they were not rebuilt here.

The original **22 provenance + six runtime-package SPDX cryptographic
verifications** were explicitly **REUSED**, with **zero new verifier calls**.
Every new anonymous file digest was independently connected to the actual
previously verified subject and preserved complete validation JSON/exit-0
receipt. The strict policy binds repository `littlesho/NodeRampart`, signer
workflow `littlesho/NodeRampart/.github/workflows/release.yml`, source-ref
`refs/tags/v0.4.0-alpha.8`, source-digest and signer-digest both equal to the
full frozen source above, GitHub Actions issuer/certificate identity and
hosted-runner identity, run **36960260337 / attempt 1**. Predicates are
`https://slsa.dev/provenance/v1` and `https://spdx.dev/Document/v2.3`.
Each SPDX subject is its corresponding runtime package, and its signed
predicate matches the downloaded SPDX document semantically, including
package/program digests. Same-release checksums alone were not treated as
independent proof. Signatures establish workflow statements, not zero
vulnerabilities, human delivery or production readiness.

### Public original-name asset inventory

The following digests were independently frozen before publication and matched
by new anonymous downloads; asset IDs and bytes remained unchanged.

| Asset ID | Original filename | Bytes | SHA256 |
| ---: | --- | ---: | --- |
| 604745222 | `SHA256SUMS` | 2353 | `f6e811894939b2864fce4c5b0f13040608c7e3cf57bd2ceb9cc8c2e70cff4806` |
| 604745220 | `bootstrap.sh` | 10329 | `f9aee566dc67eba96f2391b333a6d9121c4640be6d52d38860e2da6df240c799` |
| 604745221 | `noderampart-0.4.0-0.alpha.9.fc43.aarch64.rpm` | 11972111 | `c05d807d9f1c5a1790f553786265d3e9b19986bfbcd7eb56bfb6faa18f0bffe7` |
| 604745219 | `noderampart-0.4.0-0.alpha.9.fc43.aarch64.rpm.buildinfo.json` | 5546 | `5410037d8e12bae644e125cea674269f127cb2e5e7ca25d50492139200e5139a` |
| 604745218 | `noderampart-0.4.0-0.alpha.9.fc43.aarch64.rpm.spdx.json` | 60715 | `95b048a574c9ac258549add763d9a8865cce2ffbb96363d2d20cb52f18424779` |
| 604745229 | `noderampart-0.4.0-0.alpha.9.fc43.x86_64.rpm` | 12887178 | `0e6e70c4b2d567608aed7e311cd8013449e968516ce0d831b23fd4af0d87f091` |
| 604745239 | `noderampart-0.4.0-0.alpha.9.fc43.x86_64.rpm.buildinfo.json` | 5539 | `f51f5ec166692da30d6f4e41311106cf5d424353d8aedd296161c0d2e0eb6695` |
| 604745238 | `noderampart-0.4.0-0.alpha.9.fc43.x86_64.rpm.spdx.json` | 60675 | `df1024691d442aa9af269c32f7bee05e003c5f8151a7e878679c6dd84136d42b` |
| 604745240 | `noderampart-0.4.0-0.alpha.9.fc44.aarch64.rpm` | 11972111 | `b64c6cd60d5a6487666ceaacd05d310eb1a3911eeb297323c56507459cd6d510` |
| 604745249 | `noderampart-0.4.0-0.alpha.9.fc44.aarch64.rpm.buildinfo.json` | 5546 | `b49f31baa6059a858eaa29a5cf4c3ccaf6485287e5bdf9d4259aa05d713e73ef` |
| 604745250 | `noderampart-0.4.0-0.alpha.9.fc44.aarch64.rpm.spdx.json` | 60715 | `21c533e74a63f0c12405e73d179cfc6794f393a745f66c0c71f2745ae812aa3b` |
| 604745248 | `noderampart-0.4.0-0.alpha.9.fc44.src.rpm` | 31667610 | `0e55916a3330bedd2022a9db16d855bb3d8aa8e5ab8dc2db38883af4e8646ff2` |
| 604745254 | `noderampart-0.4.0-0.alpha.9.fc44.x86_64.rpm` | 12887178 | `1076d5a6e7219c1da26467916f6d90add912c7be0391a87ab4e83489e997e4a7` |
| 604745262 | `noderampart-0.4.0-0.alpha.9.fc44.x86_64.rpm.buildinfo.json` | 5539 | `6b9966d7aba83b6e3c177dcb832ee70ee913cb6360b04e61cc897d3ed25d31aa` |
| 604745263 | `noderampart-0.4.0-0.alpha.9.fc44.x86_64.rpm.spdx.json` | 60675 | `7b2f218935dae19d98823e3e8a67e5b86c0dcf0cb9cdbb9db8423d041fa1ef87` |
| 604745264 | `noderampart_0.4.0-alpha.8_amd64.deb` | 12469540 | `a529b2bfa4d9842eaa98af86fc8d5fc5c49d18f8395481b6c0af5aef9a2c8328` |
| 604745272 | `noderampart_0.4.0-alpha.8_amd64.deb.buildinfo.json` | 7294 | `1e4313e13e4d89a10ec9d4db41ab4ffd5af2dd998420715e871f347122afdf82` |
| 604745273 | `noderampart_0.4.0-alpha.8_amd64.deb.spdx.json` | 65232 | `cbf283426f943e189aa66dd057ed7e0445a55e02eac7a190fee2b8c4e1e1647d` |
| 604745286 | `noderampart_0.4.0-alpha.8_arm64.deb` | 11513494 | `95b7083d86ad2e4dbc19fd5a15a4befe21aafd47c5ebf50e9564d9fd0422da37` |
| 604745297 | `noderampart_0.4.0-alpha.8_arm64.deb.buildinfo.json` | 7300 | `027969a173aafb5acfd35cd7a2626490063196e5e2eedd67422390d2050b1409` |
| 604745296 | `noderampart_0.4.0-alpha.8_arm64.deb.spdx.json` | 65232 | `1104f2effd8c11b9ee6db72652a63670426652b13ef36ff7062921cfb1f371f2` |
| 604745317 | `release.json` | 297 | `6e5b2580d116b1e7d022cdfd53c79ab68bf1082cf6c2a97e46c8ddcb5be3dd90` |

### Installation and reused behavior

| Scope | Debian13 amd64 | Fedora44 x86_64 |
| --- | --- | --- |
| New anonymous guest bootstrap fetch | PASS, direct HTTPS; exact frozen script SHA | BLOCKED_NETWORK, curl timeout; 120-second bound / exit124 |
| Actual public `--version v0.4.0-alpha.8 --no-setup` | PASS; script fetched public checksums/DEB and apt installed it | NOT RUN; script was not verified or executed |
| Actual package/program source and bytes | PASS; M/raw BUILD_DATE and all three installed hashes | NOT RUN in this increment; prior hosted results REUSED |
| Separate real Chinese PTY setup | PASS, 16 observed frames; six empty hidden disabled forms cancelled | NOT RUN |
| Configuration / credentials / notifications | PASS, exact unchanged defaults; eight disabled targets / zero outbox | NOT RUN; no product installed |
| Real daemon identity, schema13/FK, online backup, advancing collection | PASS; nonroot UID, CapEff0 and NoNewPrivs | NOT RUN |
| Strict doctor | **unknown / exit2**, not strict PASS | NOT RUN, no installed program |
| Own cleanup, approved soft stop and independent claim release | PASS, STOPPED | PASS, STOPPED / no product installed |

The Debian script was downloaded inside the guest and checked against both the
frozen source and anonymously verified asset before execution. Its unchanged
installer downloaded the actual public same-release checksum and DEB; no local
package or credential was injected. The installer removed its temporary DEB
normally, so a separate retained outer-package hash in the guest is not claimed.
Its verification and all three installed payload hashes bind the observed
installation to the independently accepted DEB. Chinese setup entered no secret
and selected no Continue/Save/Test; config and file lists remained byte-identical.
The initial private terminal capture's OSC8 parser failed despite an actual
Chinese welcome; that failure/raw evidence was preserved, configuration/process
state reconciled, and only the private parser was corrected before a separate
successful PTY run. Installation was not replayed or product checks weakened.
Strict doctor retained `snapshot_foreign_keys_unavailable` and
`sensor_commit_unavailable`; direct backup SQL integrity/FK and later advancing
watermarks passed separately, without rewriting that unknown snapshot.

Fedora's bounded anonymous script fetch failed before installation. No local
package injection, guest proxy configuration, network/TLS/DNS change or retry
of release publication was used to bypass it. The Release remains publicly
available, but the required two-system public-bootstrap matrix is **incomplete**.
README retains the prior alpha.7 pinned installation example instead of promoting
alpha.8 as a fully verified public installation path. Fedora requires a future
successful public-bootstrap increment under the same approved network/lab scope;
this result authorizes no infrastructure modification or automatic withdrawal.
Both guests were normally stopped and independently confirmed **STOPPED**;
exclusive claims were released, and earlier private evidence was preserved.
This scope was reconciled at `2026-10-02T06:16:28Z`
(`2026-10-02T15:16:28+09:00`, Asia/Tokyo).

中文：公开已发生；22 项匿名字节闭环通过，不等于所有安装路径通过。Debian13 的
实际公开安装、真实中文六表单取消、服务身份、schema13、基础观察和清理通过，
strict doctor 仍为 unknown/退出码2。Fedora44 下载超时，公开 bootstrap 为
BLOCKED_NETWORK，安装/TUI/服务新增项为 NOT RUN，未改网络或用本地包替代。
两台均正常停机、独立确认 STOPPED 并释放资源；公开安装矩阵尚未闭环，README
保留 alpha.7 的原安装示例，不将新路径标成已全面验证。

The prior exact hosted amd64/x86_64 packages passed installation, start, source
identity, schema13/integrity, real daemon UID, protected credential reading,
optional-channel failure isolation and owned removal on Debian12/13 and
Fedora43/44. Those results are **REUSED**, not four new public-bootstrap tests.
The prior actual alpha.7→alpha.8 upgrade preserved data/configuration/queues,
language/timezone, targets, TTL and cooldown; the matching deployed-backup
rollback/rollforward evidence is reused only for its unchanged relevant payload,
scripts and dependencies. Schema13 rollback requires the matching old database,
configuration and credentials offline; no in-place downgrade is supported.
Sensor has no version CLI: source binding uses actual package/installed bytes
and startup records, without inventing a full runtime commit command.

### Remaining limits and safety evidence

Six platform API tests and human receiver confirmation remain **NOT RUN**;
native ARM64 and production operation are **NOT RUN**. Teams supports only the
administrator-permitted Anyone secret-URL / Adaptive Card workflow and confirms
request acceptance; OAuth/Entra-only modes remain unsupported. Native senders
connect directly without environment proxy. Standalone/DEB programs are
loader-free static, whereas RPM retains PIE/system-loader behavior. Purge is
not an atomic rollback of installation. Slack service/distribution terms,
administrator permission and message permissions remain separate from MIT;
no vendor or Marketplace certification is claimed.

The product module graph retains `golang.org/x/text v0.21.0` and
[GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970), fixed in `v0.39.0`.
The official record was rechecked before publication and unchanged. Recorded
source analysis of all three commands × Linux amd64/arm64 × static/RPM PIE
flags found no affected unicode/norm import or reachable affected source symbol
in that scope; this does not close binary coverage gaps. All 18 unchanged
hosted program bytes retain their original scan outputs against the database
observed at `2026-10-01T20:24:15Z`. Stripped inputs expose no package-symbol list;
312 raw GO-2026-5970 fields across the 12 CLI/daemon programs are conservative
scanner expansion, not observed compiled affected symbols or an unconditional
false-positive dismissal. Fedora's actual `go1.26.8-X:nodwarf5` suffix leaves
standard-library matching **UNKNOWN** in pinned govulncheck v1.7.0.

The hosted-stage Syft v1.51.1 executable scan retained **22 advisory records**
(including GO-2026-6505 / GO-2026-6597); govulncheck itself retained **two x/mod
module-only records**. These are recorded scan counts for those actual tools,
not permanent totals or product exploitability conclusions. The bounded offline
SBOM invocation, unchanged inputs and scoped advisory-precondition review were
reused; no new Syft generation or 18-program rescan is claimed. The explicit
maintainer publication authorization accepts only these disclosed limits for
this fixed alpha candidate. It does not mean vulnerabilities are repaired,
scanning is complete, unknown risks accepted or production approved.

Historical exceptions remain: two earlier local fixture tags outside the
preflight authority were cleaned and the fixture tests corrected; the draft
notes stage used two PATCH requests after the first changed its internal tag
association, with the second restoring the original same-ID association.
Original request/response records and the correction were preserved. This phase
did not create fixture tags and does not claim the whole history was anomaly-free.

Read-only governance observation: main branch protection was false, effective
branch rules and repository/parent rulesets were empty. **本次按流程检查，但服务器
未强制执行。** Visible workflows/webhooks showed no release-triggered production
deployment; immutable-release settings were disabled and were not changed.
Normal documentation review/CI/PR is separate from release-byte verification.

### Post-publication diagnostic increment (2026-10-02)

This increment diagnoses the unchanged published source
`77ae069b8f00651106b9621a24047b0ad7b4e88d` and its actual release programs.
Later main documentation commits are not replacement build inputs. It does
not rebuild, replace or re-sign assets, edit the Release, move the tag or
change network, product or diagnostic policies.

The original Debian strict result remains **unknown / exit2**, with
`snapshot_foreign_keys_unavailable` and `sensor_commit_unavailable`. Its full
doctor/health JSON had been kept in the guest's temporary filesystem. On the
new normal start that temporary directory was absent; the retained host
check list, exit codes, original raw SHA256 values and independent persistent
backup/configuration remain available. A historical raw digest cannot recreate
missing bytes. This limits reconstruction of the original same-call status,
snapshot metadata and precise cause; a newly installed process is a separate
observation, never a replay of that original process.

The snapshot option checks an explicit standalone forensic database, separately
from live daemon foreign keys and `backup verify`. In the frozen implementation,
the file must be ordinary, owned by root or the invoking effective UID, have one
link, have no group/world write permission or SQLite sidecars, and have a
supported schema; parents must contain no symlinks. The CLI gives this check
its own two-second deadline. Root's ability to read a service-owned backup does
not satisfy the inspector's ownership contract. Use a supported consistent
backup and a new separate, stable, invoking-user/root-owned private copy;
preserve the original and its sidecars rather than changing its ownership,
deleting a journal or copying a writing live database. Ordinary SQL integrity/FK
success cannot substitute for those product file/schema/deadline conditions.
See the [snapshot contract](STORAGE-BUDGET.md#backups-and-restore).

Independent frozen-source analysis identified a **diagnostic precision defect**
under explicit same-committed-batch conditions. The receipt map retains the
batch's nanoseconds; the store persists its watermark through `UnixMicro` and
reads it back with microsecond precision. `Diagnose` compares the unrounded
receipt using `After`. For example, a receipt ending `.123456789Z` is later
than the same completed batch's stored `.123456Z` by 789ns and can produce
`sensor_commit_unavailable` / strict exit2 despite that complete commit.
An independent Go time/JSON vector exercised all 1,000 microsecond remainders:
999 nonzero remainders compared later; the aligned case did not. This was a
standard-library vector plus source-path analysis, not a rebuilt product or
historical guest replay.

This does not classify every unavailable commit as a false alarm. Receipt
capture and durable-watermark queries occur at different times; genuinely
pending writes, missing/legacy watermarks, incomplete admission and storage
errors remain distinct. Timestamp proximity without sufficient batch identity
does not establish a completed session/sequence. Later progressing watermarks
do not change an earlier unknown result. A separate product repair needs to
align precision or retain appropriate receipt identity, with regressions for
same committed batches, real newer pending work, missing/partial commits and
cross-session state. No such fix is included in this documentation increment.

中文：本次只诊断固定发行源码和实际程序，不重建、换包、补签或修改 Release。
原 Debian unknown/退出码2 及两个 reason_code 保留。原完整 doctor/health JSON
位于 guest 临时文件系统，本次正常启动后目录不存在；仍有脱敏检查表、退出码、
原摘要和持久备份/配置。摘要不能重建原件，新进程也不能冒充原进程复测，因此
原时点的唯一成因仍有证据缺口。

`--foreign-keys-snapshot` 是独立取证副本接口，和 live 外键及 backup verify
不同。文件必须归调用者或 root、普通文件、单链接、非组/全员可写、无 SQLite
sidecar、schema 支持且父路径无符号链接；CLI 内部期限为两秒。root 能读取
daemon 所有的备份，不代表满足该归属契约。使用受支持的一致备份创建新的安全
独立副本，保留原件和 sidecar，不修改原归属、删除日志或复制写入中的 live DB。

已确认一个条件性的产品诊断精度问题：同一已提交批次的纳秒 receipt 与微秒
watermark 作严格 After 比较，可误报 sensor_commit_unavailable/退出码2。
受控时间向量支持该类别，不证明原记录的唯一原因。真实 pending、缺失/不完整
提交和存储错误仍须区分；后续推进或某次健康不改判历史，也不证明持续健康。
修复需独立的产品任务及相应回归，本阶段没有改代码或关闭检查。

#### Debian13: one new same-release process, six fixed samples

Because the original raw JSON was unavailable and the product had already been
removed, the bounded follow-up installed the previously accepted actual public
DEB (`a529b2bfa4d9842eaa98af86fc8d5fc5c49d18f8395481b6c0af5aef9a2c8328`).
This is **NEW_PROCESS_SAME_RELEASE**, not another public-bootstrap test or a
direct retest of the original process. CLI/daemon commit and raw BUILD_DATE,
all three installed program hashes, unchanged default configuration and normal
service identity were checked; no manually trusted UID, auth/sensor setting
change or real notification target was used.

The plan fixed three rounds of two commands: basic `doctor --strict`, followed
by `doctor --strict --foreign-keys-snapshot` on a new safe root-owned private
copy. Only the first four results' actual `auth_window_warmup` caused one
bounded continuation to the same process's true ready-after plus two batch
intervals. All six stdout JSONs, stderr, actual process exits, embedded daemon
states and before/after process/input metadata were retained in persistent
private evidence. No seventh sample or restart-to-green was taken.

| Sample / mode | Started UTC, 2026-10-02 | Actual strict exit | All abnormal check reasons | Receipt minus selected watermark | Durable sequence | Snapshot check |
| --- | --- | ---: | --- | ---: | ---: | --- |
| 1 / basic live | 08:18:47.886208 | 2 | `auth_window_warmup`, `sensor_commit_unavailable` | 167ns | 158 | Not requested |
| 2 / forensic snapshot | 08:18:47.917107 | 2 | `auth_window_warmup`, `sensor_commit_unavailable` | 167ns | 158 | valid |
| 3 / basic live | 08:18:50.887389 | 2 | `auth_window_warmup`, `sensor_commit_unavailable` | 768ns | 161 | Not requested |
| 4 / forensic snapshot | 08:18:50.919345 | 2 | `auth_window_warmup`, `sensor_commit_unavailable` | 768ns | 161 | valid |
| 5 / basic live | 08:21:11.769490 | 2 | `sensor_commit_unavailable` | 452ns | 302 | Not requested |
| 6 / forensic snapshot | 08:21:11.840923 | 2 | `sensor_commit_unavailable` | 452ns | 302 | valid |

The 144-second sampling run stayed within its 330-second bound. Process/session
identity was unchanged; each embedded observation had a complete watermark,
committed events and notification decisions, no unreadable watermark flag or
pending derived events, and basic readiness. Watermarks advanced from 158 to
302; this supports the sampled interval's progress, not historical or sustained
health. The submicrosecond deltas reproduced the precision defect in this new
same-release scene. Restart-pending coverage uncertainty and SSH journal
backfill-limit gaps were also retained, rather than treated as complete history.
No other abnormal strict check was hidden by unknown's precedence over degraded.

The new online backup was verified through the supported backup interface.
Its separate root-owned 0600, single-link, sidecar-free copy had identical
bytes and a safe private parent; all three forensic checks were valid within
the unchanged two-second product deadline. The service-owned source retained
its owner and bytes. Direct root inspection of that service-owned file was
**NOT RUN** as an extra doctor call: its observed nonroot owner does not pass
the documented root/current-eUID ownership predicate, but that is a source/
metadata prediction, not a fabricated seventh result. The original historical
snapshot metadata was not recoverable, so its unique rejection cause remains
unestablished. Neither these valid copies nor disappearing warmup rewrite the
original unknown or make the six new strict results healthy.

The private harness initially assumed the duration spelling `5m0s`; the actual
unchanged default was `5m`. That local assertion failure and original script
were preserved, equivalent duration/default bytes verified, and only the
private assumption corrected before any doctor sample. The installation was
not repeated and the same running process was retained.

中文：旧产品已卸载、原 raw 缺失，本次用已验收的真实发行 DEB 重建一个新进程，
不是原进程复测或新公开安装测试。预先固定三轮 basic/合规副本各一份，共六份，
仅根据实际 warmup 和同进程 ready-after 作一次有界续样；没有第七份或重启凑绿。
六份全部退出2，前四份有两个 reason，后两份仅有 sensor_commit_unavailable；
微小时间差依次为167/167/768/768/452/452ns，同份水位完整且从158推进到302。
这是新场景的精度缺陷复现，不等于原历史唯一原因或持续健康。三份安全 root
独立副本检查 valid，原 service 备份未改归属或字节；没有额外执行 root 对
service 文件的拒绝测试。restart/SSH backfill 覆盖缺口、全部异常、私有时长
文本断言失败及修正均保留，不用副本通过或 warmup 消失改判 strict healthy。

#### Fedora44: public script retrieved, checksum stage still blocked

The original failed fetch used the tag's `raw.githubusercontent.com` script
path, before any installer execution. Its outer 120-second timeout returned
124; the retained stderr reported curl error 28 after 90 seconds with zero
body bytes. The final curl process exit and whether its configured retry had
started are unknown. Those records do not identify DNS, TCP, TLS, IPv6 or CDN
as the cause, and the original failure remains retained.

The incremental check used the canonical public Release asset URL instead.
The guest had no inherited proxy configuration; no proxy, network or trust
settings were added. One anonymous body-download round, with two HTTPS hops
(GitHub 302 then release-assets 200), returned all 10,329 bytes in 1.184 seconds.
Both curl processes exited 0, TLS verification succeeded, and measured internal
retries were zero. The script's SHA256 was
`f9aee566dc67eba96f2391b333a6d9121c4640be6d52d38860e2da6df240c799`,
matching the frozen source and independently verified release inventory.
No GitHub token, cookie, netrc or automatic client configuration participated.
This success does not prove that the original raw-host path recovered.

The unmodified script was then executed **once** with
`--version v0.4.0-alpha.8 --no-setup`. It attempted the public `SHA256SUMS`
on its first GitHub hop, reported curl error 28, and produced no HTTP response
header. The private bounded observer terminated only its identified curl
request after that error, preventing unmeasured further retries; it did not
signal the installer shell or a package-manager transaction. The shell exited
1. The script configures two retries (at most three transfers per curl call),
but the actual internal retry count and final curl process exit remain
**UNKNOWN**. This is **BLOCKED_NETWORK** at the checksum-download stage,
not proof of a package or product defect. The observed download work totalled
16.237 seconds within the predeclared 240-second bound; the installer was not
replayed and no alternate network mode was introduced.

No checksum body or RPM was obtained, DNF did not start, and installed program
identity, service/schema/UID checks, Chinese PTY cancellation and live diagnosis
remain **NOT RUN** for this public-bootstrap increment. Previously accepted
hosted RPM behavior is separate and cannot fill this public-download gap.
Own installer processes were absent, product paths remained absent, and the
normal approved soft stop plus a separate status read confirmed **STOPPED**.
Further installation needs the guest's existing authorized HTTPS route to
complete the public checksum/package requests; adding a proxy or changing
network/trust policy would require separate authorization.

中文：原失败在 raw-tag 脚本获取阶段；外层 timeout 退出124 不等于 curl 最终
退出码，原 stderr 只证明错误28、90秒和零字节，具体网络层及重试进展未知。
本次从公开 Release 的 canonical 地址匿名下载原脚本成功：一轮两跳，10,329字节、
SHA一致、TLS校验成功、两次 curl 均退出0且观测重试0；没有新增代理或网络配置。
原脚本仅执行一次，但下一阶段 SHA256SUMS 的首个 GitHub 请求报错误28且无
HTTP响应头；观察器仅终止自己的该 curl，shell退出1，实际内部重试数与 curl
最终退出仍未知。预算240秒内实际约16.237秒，保留 BLOCKED_NETWORK，不重试
安装、不注入本地包。RPM、依赖安装、中文PTY及服务/身份/schema/运行均 NOT RUN。
没有遗留安装进程或产品路径，正常软停后独立确认 STOPPED 并释放自有资源。

#### Increment conclusions and remaining scope

| Separate result | Actual conclusion |
| --- | --- |
| Published release / original 22-asset identity | REUSED with current read-only fixed-ID/tag/asset checks; no new downloads of the complete set or new signatures |
| Fedora44 public bootstrap | Script download PASS; public checksum stage BLOCKED_NETWORK; RPM, installation, TUI and runtime NOT RUN |
| Debian13 forensic snapshot | Original unique rejection cause UNESTABLISHED; three new compliant standalone snapshot checks valid, without rewriting the original unknown |
| Debian13 live doctor / sensor | Six new strict results unknown/exit2; same-release precision defect reproduced with complete advancing commits, not a health PASS or a repair |

The Debian test objects were removed through the normal product management
and package-removal paths; original persistent evidence and new consistent
backup/sample evidence were retained separately. Both guests were normally
soft-stopped, each STOPPED state independently read, and both task ownership
claims released. Private parser/assertion failures and their corrections were
preserved without replaying completed installs, service actions or stops.
No Release PATCH, tag operation, release rebuild, attestation, infrastructure
change or production action occurred in this increment. README installation
examples were not promoted and bootstrap's no-argument default remains alpha.5.

The six real platform APIs/human receipts, native ARM64 and production remain
NOT RUN. Existing x/text module findings, stripped-symbol and Fedora toolchain
scan coverage gaps and tool advisories retain their previous scope; no new
scan or risk acceptance is claimed. Teams acceptance, direct-only native
notifications, matching-backup rollback and non-atomic purge boundaries remain.
The diagnostic defect requires a separately authorized product repair; the
Fedora network gap requires a working authorized route or a separate network
decision. Neither blocks preserving the already published original release,
nor justifies a claim of complete installation coverage or production readiness.

中文：发行字节只读核对/复用、Fedora公开安装阻塞、Debian合规副本 valid、
Debian实时诊断精度缺陷是四项独立结论，不能合并成“全部通过”。两台均清理自有
对象、保留证据、正常软停并独立确认 STOPPED/释放资源。未修改发行对象、产品、
网络、权限、治理、安装默认或生产；真实外发、原生 ARM64、安全扫描缺口等继续
披露。产品修复和新网络权限属于另行授权任务，本次有限增量到此收口。


### Alpha.9 development follow-up: sensor receipt precision

After the alpha.8 investigation above, a separate alpha.9 Unreleased source fix
matches the receipt's session/interface/sequence with a durable watermark before
comparing `UnixMicro()` timestamps. The store writes `sent_at_us`; sub-microsecond
receipt digits are not a newer observation of that same identity. Time-only
normalization was insufficient because a reconnect's first frame can have a new
identity in the same microsecond before storage rejects nonmonotonic progress.
The bounded additive `sensor_receipts` status map preserves that distinction.
Missing/legacy identity, missing or unreadable watermarks, unmatched sequences,
and partial commits retain unknown/degraded semantics. A later sequence cannot
prove an earlier skipped receipt; cross-snapshot progress may remain unknown.

This source change does not rebuild or replace Release 401526101, its tag,
77ae069b8f00651106b9621a24047b0ad7b4e88d source or any of its 22 assets. Alpha.8
strict results, nanosecond deltas and investigation records above remain exactly
historical observations, not retroactive health PASS. Snapshot unique cause
remains UNESTABLISHED, Fedora public bootstrap remains BLOCKED_NETWORK, and
security/real-platform/native-ARM64/production limits are unchanged. Alpha.9 is
not released. Current semantics are in
[operations](V0.4_OPERATIONS.md#sensor-commit-watermarks); acceptance of a new
candidate must bind its actual commit, tests and any runtime evidence separately.

中文：后续独立修复仅进入 alpha.9 未发布开发源码：先匹配会话/接口/序号，再按
持久化微秒精度比较。重连首帧不受旧连接间隔约束，因此不能只把时间粗化就认定
新批次已提交。新增有界 receipt 身份；真实 pending/missing/partial 及跨采样点
不确定状态继续保留。以上 alpha.8 原始 strict 结果、缺陷复现和未知历史根因均
不改写；固定发行源、tag 和 22 项资产不变。新候选的测试和运行证据须独立绑定，
不代表 alpha.9 已发布或生产就绪。

### Alpha.9 development follow-up: RPM transaction ordering

The no-tag preflight candidate `2ec534be8d78231896fdc288e80d5862f28e953e`
is invalidated. Its real Fedora44 installation with RPM 6.0.2 rejected a
remaining source helper in `%pre`, after implicit `%sysusers` had already
created the service group/users. That failed candidate's six runtime packages,
SRPM, SPDX/buildinfo, 22-asset collection, archive and VM package identities
remain failed-candidate evidence, not evidence for a repaired release source.

Alpha.9 Unreleased adds a dependency-free, read-only embedded Lua `%pretrans`
guard for all existing filesystem-conflict rules before implicit sysusers;
`%pre` remains a later defense-in-depth recheck. Dangling links are detected
with RPM Lua's lstat-based `posix.stat`; only the two full unit paths allow
direct `/dev/null` masks. The sysusers declaration, service accounts, Debian
behavior, version and schemas are unchanged. New package and transaction
evidence must bind the actual repair candidate. After repair merge and main
gates, no-tag preflight must start again from a newly frozen source; this source
change alone is not preflight PASS or release authorization.

The published alpha.8 source/tag/Release 401526101 and its 22 assets are unchanged.
Historical snapshot cause remains UNESTABLISHED and Fedora public bootstrap
remains BLOCKED_NETWORK. GO-2026-5970, scanner coverage gaps and real-platform,
human-receipt, billing, native-ARM64 and production NOT RUN limits remain.

中文：`2ec534be...` 候选在 Fedora44 RPM 6.0.2 的真实安装中，先创建服务账户，
再由 `%pre` 拒绝源码 helper，故其完整候选资产集失效并保留为失败证据。
alpha.9 未发布源码增加只读、无外部解释器依赖的 Lua `%pretrans` 早期检查，
保留 `%pre` 复核、sysusers、账户、Debian 行为及版本/schema。修复包须重新
绑定实际来源并验证交易顺序；合并及 main 检查通过后重新冻结并从零预验收。
不改写任何 alpha.8 历史结果，也不修改已发布对象或宣称 alpha.9 已发布。
