# Verify a NodeRampart release

This document describes published `v0.4.0-alpha.6` and retains earlier release
records. The examples explicitly select alpha.6; its frozen bootstrap default
remains `v0.4.0-alpha.5`. Review the intended source/tag and verify the matching
assets and attestations before installation.
The current source/build tools target unreleased alpha.7 (DEB `0.4.0~alpha.7`,
RPM `0.4.0-0.alpha.8`). Use the frozen alpha.6 source/tools for its historical
asset collection assertions. Alpha.7 CI artifacts are experimental candidates,
without a tag, Release or release-provenance claim. The published examples below
remain alpha.6.

当前源码/构建工具面向未发布 alpha.7 候选；核对 alpha.6 历史发行集合应使用其冻结
源码工具。候选 CI 包不等于正式发行包或发行证明，下方公开安装例仍是 alpha.6。

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

For alpha.6, public DEB names are
`noderampart_0.4.0-alpha.6_ARCH.deb`, with native Debian version
`0.4.0~alpha.6`. RPM names contain `0.4.0-0.alpha.7.fc43` or
`0.4.0-0.alpha.7.fc44`; the source RPM uses Fedora 44. SBOM/buildinfo names,
checksums and attestations bind the final public filename and actual bytes.
The [alpha.6 distribution record](#alpha6-publication-and-distribution-verification)
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

The example selects published `v0.4.0-alpha.6`, whose reviewed source is
`4d204b43499ca2f41c15b92334537d57bfc8b20c`. Confirm that identity against the
public source before using it as an expected value. Missing statements cannot
pass verification; do not substitute another release's attestations.

```sh
VERIFY_DIR=$(mktemp -d)
gh release download v0.4.0-alpha.6 --repo littlesho/NodeRampart --dir "$VERIFY_DIR"
cd "$VERIFY_DIR"
sha256sum -c SHA256SUMS

PACKAGE=noderampart_0.4.0-alpha.6_amd64.deb
EXPECTED_COMMIT='4d204b43499ca2f41c15b92334537d57bfc8b20c'

# Verify the package's provenance against the intended repository/workflow/tag.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.6 \
  --source-digest "$EXPECTED_COMMIT" \
  --predicate-type https://slsa.dev/provenance/v1 \
  --deny-self-hosted-runners

# Verify the SBOM statement bound to the same package bytes.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.6 \
  --source-digest "$EXPECTED_COMMIT" \
  --predicate-type https://spdx.dev/Document/v2.3 \
  --deny-self-hosted-runners

# The separately downloaded inventory also has its own provenance statement.
gh attestation verify "$PACKAGE.spdx.json" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.6 \
  --source-digest "$EXPECTED_COMMIT" \
  --predicate-type https://slsa.dev/provenance/v1 \
  --deny-self-hosted-runners
```

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
alpha.6 is already published. Select that candidate’s independently reviewed
version and source identity. A local candidate can be built without a tag using
`EXPECTED_COMMIT=<full-main-SHA> ./scripts/release-metadata.sh` and passing its
`commit` and source-commit `build_date` to `build-release.sh`. Do not substitute
the local wall clock or claim local artifacts have GitHub attestations.

A draft remains unpublished even if an authenticated maintainer can download it.
Use the same `gh release download` and attestation commands above with an account
that can read the draft. Record its numeric Release ID, tag, full source commit,
workflow run ID and attempt before verification. The tag must resolve to the
reviewed main commit and match `v$(cat VERSION)`; do not move an existing tag.

For frozen alpha.6, the exact asset set is 22 files:

- `noderampart_0.4.0-alpha.6_amd64.deb` and `noderampart_0.4.0-alpha.6_arm64.deb`.
- `noderampart-0.4.0-0.alpha.7.fc43.x86_64.rpm`,
  `noderampart-0.4.0-0.alpha.7.fc43.aarch64.rpm`,
  `noderampart-0.4.0-0.alpha.7.fc44.x86_64.rpm`,
  `noderampart-0.4.0-0.alpha.7.fc44.aarch64.rpm`.
- Each of those six runtime filenames plus `.spdx.json` and `.buildinfo.json`.
- `noderampart-0.4.0-0.alpha.7.fc44.src.rpm`.
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

For a reviewed unreleased alpha.7 local package, with Go 1.26.8 and the appropriate read-only
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
