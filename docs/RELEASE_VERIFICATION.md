# Verify a NodeRampart release

[v0.4.0-alpha.3](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.3)
is published as a prerelease, with 22 assets and their GitHub attestations.
The release source commit is `fc5630f398b622a98fd7f062efe8c1505ec3424e`.
All assets were downloaded anonymously under their original public names and
matched the verified draft bytes; `sha256sum -c SHA256SUMS` passed directly.
The existing package-content and attestation checks therefore apply to those
same bytes. Installation lifecycle and ARM64 hardware acceptance testing have
not been performed for this version. The steps below let you verify your own
download before deciding to install it.

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

## Download and verify

Use a current [GitHub CLI](https://cli.github.com/manual/gh_attestation_verify)
with artifact attestation support. Select the full commit SHA from the release
source you have reviewed; do not treat a value downloaded beside the package as
independent evidence of the expected source.

```sh
VERIFY_DIR=$(mktemp -d)
gh release download v0.4.0-alpha.3 --repo littlesho/NodeRampart --dir "$VERIFY_DIR"
cd "$VERIFY_DIR"
sha256sum -c SHA256SUMS

PACKAGE=noderampart_0.4.0-alpha.3_amd64.deb
EXPECTED_COMMIT='REPLACE_WITH_REVIEWED_FULL_COMMIT_SHA'

# Verify the package's provenance against the intended repository/workflow/tag.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.3 \
  --source-digest "$EXPECTED_COMMIT" \
  --predicate-type https://slsa.dev/provenance/v1 \
  --deny-self-hosted-runners

# Verify the SBOM statement bound to the same package bytes.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.3 \
  --source-digest "$EXPECTED_COMMIT" \
  --predicate-type https://spdx.dev/Document \
  --deny-self-hosted-runners

# The separately downloaded inventory also has its own provenance statement.
gh attestation verify "$PACKAGE.spdx.json" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.3 \
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

A draft remains unpublished even if an authenticated maintainer can download it.
Use the same `gh release download` and attestation commands above with an account
that can read the draft. Record its numeric Release ID, tag, full source commit,
workflow run ID and attempt before verification. The tag must resolve to the
reviewed main commit and match `v$(cat VERSION)`; do not move an existing tag.

The exact expected asset set is 22 files:

- `noderampart_0.4.0-alpha.3_amd64.deb` and `noderampart_0.4.0-alpha.3_arm64.deb`.
- `noderampart-0.4.0-0.alpha.4.fc43.x86_64.rpm`,
  `noderampart-0.4.0-0.alpha.4.fc43.aarch64.rpm`,
  `noderampart-0.4.0-0.alpha.4.fc44.x86_64.rpm`,
  `noderampart-0.4.0-0.alpha.4.fc44.aarch64.rpm`.
- Each of those six runtime filenames plus `.spdx.json` and `.buildinfo.json`.
- `noderampart-0.4.0-0.alpha.4.fc44.src.rpm`.
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

For a reviewed local package, with Go 1.26.8 and the appropriate read-only
`dpkg-deb` or `rpm`/`rpm2cpio` inspection tools available:

```sh
TOOLS_DIR=$(mktemp -d)
python3 scripts/release_sbom.py --fetch-syft "$TOOLS_DIR/syft"
COMMIT=$(git rev-parse HEAD)
BUILD_DATE=$(git show -s --format=%cI HEAD)
export COMMIT BUILD_DATE
python3 scripts/release_sbom.py dist/noderampart_0.4.0-alpha.3_amd64.deb \
  --syft "$TOOLS_DIR/syft/syft" --output dist/sbom
```

Supply the actual intended build declarations when inspecting a package built
elsewhere. A local build without a known commit may use literal `COMMIT=unknown`;
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


## alpha.4 candidate verification

The current source tooling targets the unpublished `v0.4.0-alpha.4` candidate.
Public installation commands above intentionally remain on published alpha.3;
use its tag when applying those version-specific source-tool examples.
For alpha.4, authenticate to the same draft, retain original remote names and
verify exactly 22 assets: two `noderampart_0.4.0-alpha.4_{amd64,arm64}.deb`, four
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

alpha.4 尚未公开。草稿需认证下载，按远端原名验证 22 个资产及 28 项证明；
匿名下载留待另行授权公开后执行。已有候选 City/ASN 离线通过仅作为算法证据，
不能替代本次新包及其源提交、源码清单和证明核验。
