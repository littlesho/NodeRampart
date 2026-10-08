# Verify a NodeRampart release

<!-- current-release:start -->
Current download and verification examples select published
[v0.4.0-alpha.11](#alpha11-ordinary-release-and-latest-promotion), now an ordinary
GitHub Release and GitHub Latest with Alpha product maturity.
Its frozen source is `2c9d4416adef3cb64e0523a1b9ac1e691b121c16`, Debian native
version is `0.4.0~alpha.11`, and RPM is `0.4.0-0.alpha.12.fc43/fc44`.
Later main installer/documentation commits do not become the source of these
published assets or attestations.
<!-- current-release:end -->

Use the [maintained entry](../README.md#install-the-newest-published-release)
for default-latest installation. The alpha.11 Release bootstrap is a frozen
source snapshot with dynamic default resolution; pass an explicit version when
using the pinned Release-asset example. The current documentation record is [LATEST_RELEASE](../LATEST_RELEASE),
separate from development `VERSION` and never an online fallback.

中文：当前下载与验签示例随最新公开版本更新；默认安装使用 main 维护入口。
固定 Release 的 bootstrap 必须显式传版本。下方各版本发布记录保留当时的测试、
默认值和推荐情况，历史记录不作为当前安装政策；以本节和 README 为准。

The release workflow creates a draft for a maintainer to inspect; local workflow
edits and tests neither publish a release nor prove that hosted checks ran.

Remote policy recommendations require separate maintainer approval: require the
actual CI validation/build/package/fuzz check names observed in hosted runs
before merging main, prevent force-push/deletion of release tags, and review
the complete draft asset set before publication. Local scripts do not configure
GitHub branch/tag rules or approve a release.

<a id="published-alpha5-baseline"></a>

## Historical published alpha.5 baseline

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

<!-- current-release:start -->
For the current published release, public DEB names are
`noderampart_0.4.0-alpha.11_ARCH.deb`, with native version `0.4.0~alpha.11`.
RPM names contain `0.4.0-0.alpha.12.fc43` or `0.4.0-0.alpha.12.fc44`;
the source RPM uses Fedora 44. SBOM/buildinfo names append `.spdx.json` and
`.buildinfo.json` to the exact public package filename. Checksums and
attestations bind those filenames and bytes. The [publication record](#alpha11-pre-release-publication-and-distribution-verification)
identifies the hosted source, run and evidence scope.
<!-- current-release:end -->

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
Only the fuzz jobs pin Go 1.27.1 to fix the deadline cancellation race in
[golang/go#75804](https://github.com/golang/go/issues/75804). Ordinary/race tests,
coverage, Ubuntu builds and release validation retain Go 1.26.8; Fedora package
builds retain their native toolchain. The fuzz pin applies to PR/main CI and
future release safety checks; it does not change published release assets.
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

<!-- current-release:start -->
The example selects published `v0.4.0-alpha.11`, whose reviewed source is
`2c9d4416adef3cb64e0523a1b9ac1e691b121c16`. Confirm that identity against the
public source before using it as an expected value. Missing statements cannot
pass verification; do not substitute another release's attestations.

```sh
VERIFY_DIR=$(mktemp -d)
gh release download v0.4.0-alpha.11 --repo littlesho/NodeRampart --dir "$VERIFY_DIR"
cd "$VERIFY_DIR"
sha256sum -c SHA256SUMS

PACKAGE=noderampart_0.4.0-alpha.11_amd64.deb
EXPECTED_COMMIT='2c9d4416adef3cb64e0523a1b9ac1e691b121c16'

# Verify the package's provenance against the intended repository/workflow/tag.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.11 \
  --source-digest "$EXPECTED_COMMIT" \
  --signer-digest "$EXPECTED_COMMIT" \
  --predicate-type https://slsa.dev/provenance/v1 \
  --deny-self-hosted-runners

# Verify the SBOM statement bound to the same package bytes.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.11 \
  --source-digest "$EXPECTED_COMMIT" \
  --signer-digest "$EXPECTED_COMMIT" \
  --predicate-type https://spdx.dev/Document/v2.3 \
  --deny-self-hosted-runners

# The separately downloaded inventory also has its own provenance statement.
gh attestation verify "$PACKAGE.spdx.json" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha.11 \
  --source-digest "$EXPECTED_COMMIT" \
  --signer-digest "$EXPECTED_COMMIT" \
  --predicate-type https://slsa.dev/provenance/v1 \
  --deny-self-hosted-runners
```

<!-- current-release:end -->

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
the current release is already published. Select that candidate’s independently reviewed
version and source identity. A local candidate can be built without a tag using
`EXPECTED_COMMIT=<full-main-SHA> ./scripts/release-metadata.sh` and passing its
`commit` and source-commit `build_date` to `build-release.sh`. Do not substitute
the local wall clock or claim local artifacts have GitHub attestations.

A draft remains unpublished even if an authenticated maintainer can download it.
Use the same `gh release download` and attestation commands above with an account
that can read the draft. Record its numeric Release ID, tag, full source commit,
workflow run ID and attempt before verification. The tag must resolve to the
reviewed main commit and match `v$(cat VERSION)`; do not move an existing tag.

<!-- current-release:start -->
For the current published release, the exact asset set is 22 files:

- `noderampart_0.4.0-alpha.11_amd64.deb` and `noderampart_0.4.0-alpha.11_arm64.deb`.
- `noderampart-0.4.0-0.alpha.12.fc43.x86_64.rpm`,
  `noderampart-0.4.0-0.alpha.12.fc43.aarch64.rpm`,
  `noderampart-0.4.0-0.alpha.12.fc44.x86_64.rpm`,
  `noderampart-0.4.0-0.alpha.12.fc44.aarch64.rpm`.
- Each of those six runtime filenames plus `.spdx.json` and `.buildinfo.json`.
- `noderampart-0.4.0-0.alpha.12.fc44.src.rpm`.
- `bootstrap.sh`, `release.json`, `SHA256SUMS`.

<!-- current-release:end -->

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
same verified draft only when the explicitly authorized release task's required
gates and artifact/runtime acceptance have passed.

### Alpha.11 preparation and scoped publication decision

The original preparation targeted `0.4.0-alpha.11` while the current public pin still described alpha.10. The actual [publication record](#alpha11-pre-release-publication-and-distribution-verification) below supersedes that unpublished state; it does not rewrite the original preparation/FAILURE evidence.

The DEB native version is `0.4.0~alpha.11`; public filenames are `noderampart_0.4.0-alpha.11_{amd64,arm64}.deb`. RPM Version is `0.4.0`, Release `0.alpha.12.fc43/fc44`; the source RPM is `noderampart-0.4.0-0.alpha.12.fc44.src.rpm`. Freeze successful hosted Draft bytes before native acceptance and bind every result to source C and asset set A. No fixture or manual timer enable replaces legitimate credentialed YES-save acceptance.

On 2026-10-08 the maintainer explicitly accepted a **SCOPED_PRE_RELEASE_WAIVER** for missing Fedora native durable ACK and moved real MaxMind/GeoIP YES to their own VPS after Pre-release. That initial authorization published only the same verified numeric ID with `draft=false`, `prerelease=true`, `make_latest="false"`. Original PRE-RELEASE-GATE BLOCKED and failures remain retained; those unexecuted scenarios are not PASS. At that stage ordinary Release/Latest was deferred; the [later promotion](#alpha11-ordinary-release-and-latest-promotion) records user VPS feedback and new explicit authority. Four x86_64 VMs do not certify native ARM64, long soak, stable readiness or production.

## Current release and installer policy

The maintained `main` bootstrap resolves the highest product version among this
repository's published Releases when `--version` is omitted. Include alpha,
beta and RC prereleases; require `draft=false` and a valid `published_at`.
Compare semantic version precedence, including numeric prerelease components
and stable-over-prerelease ordering. Drafts, unaccompanied tags and development
`VERSION` are excluded. GitHub's [latest endpoint excludes prereleases](https://docs.github.com/en/rest/releases/releases#get-the-latest-release);
neither that endpoint, the “Latest” badge, list order nor release-body text is
the selection authority. Resolve all bounded pages or fail.

Each invocation prints and pins one Release/tag, verifies its bootstrap against
that same Release's checksum file, and dispatches it once with explicit
`--version`. All scripts, checksums and packages belong to that fixed release.
A new publication during installation cannot change the target. Missing packages,
API errors/limits, malformed or incomplete responses and failed identity/checksum
checks are errors; do not fall back to a local record or older release. Explicit
`--version` pins exactly that release without the default list query, preserving
platform, source-install, credential, service-policy and anti-downgrade checks.
Only an active installer invocation selects updates; the daemon never auto-upgrades.

Current user guides, feature summaries, install/download/upgrade/verification
examples and package/SBOM names follow the newest published release.
`LATEST_RELEASE` is the single offline documentation pin; development `VERSION`
may differ. Mark current identity examples with `current-release:start/end`
comments and run `python3 scripts/check-current-release.py`. The check is part
of shared validation and ordinary CI, using local evidence and fixtures without
network access. Preserve historical changelogs, release/tag snapshots, asset
hashes, attestations, incident evidence, explicitly labeled rollback targets and
old upgrade sources. Tool/dependency versions remain independent.

Keep NOT RUN and known risks accurately scoped. They do not authorize retaining
an older default or recommendation; withdrawal or rollback needs an explicit
maintainer decision. Published asset bytes, checksum manifests, attestations and
tag targets remain immutable. Deliver default changes through maintained main
and future releases, never by replacing a published bootstrap.

A task explicitly authorized for final Latest selects its newly published, accepted
release from reviewed main as **GitHub Latest**. A merge alone authorizes no tag
or Release. Keep the workflow's strict `draft=true`, `prerelease=true` staging;
only final publication of the verified same numeric ID uses `draft=false`,
`prerelease=false`, `make_latest="true"`. GitHub [does not permit drafts or
prereleases to be Latest](https://docs.github.com/en/rest/releases/releases#update-a-release).
If the maintainer chooses `prerelease=true`, publish with `make_latest="false"`
and explain that it cannot also satisfy Latest. Alpha.11 was initially published
in that Pre-release channel; it has since been promoted under new explicit
authority to ordinary Release/Latest. Retain the alpha/beta/RC tag and its maturity notice;
platform flags do not waive product maturity gates.

Confirm Latest from actual GET responses and page/download redirects, not from
the PATCH input. Its numeric ID, tag, public state and publication time must
agree with the complete published listing and the highest product version.
Also check the peeled source tag, all 22 public bytes and the 21 checksum entries.
The installer continues to use semantic product ordering over the complete
published list, including prereleases; GitHub Latest remains a separate
distribution setting.

### Publication completion

A release task is complete after this sequence:

1. Publish the reviewed same Release ID in the explicitly authorized channel,
   retaining an Alpha notice. Set Latest only when separately authorized for that
   channel; a Pre-release stays non-Latest. Read back its real public state,
   tag/source and asset identities. Resolve the complete product Release list,
   including prereleases; do not infer the newest release from `VERSION`.
2. Update `LATEST_RELEASE`, current documentation, examples and feature summaries
   to that real release. Keep native DEB/RPM naming and expected source digests
   consistent with the recorded publication identity. Check current Wiki/pinned
   guide/Release-body installation recommendations if those surfaces exist.
3. For the current ordinary Release/Latest, capture the complete fixed-repository
   published list and the actual Latest object, then run the joint check:

   ```sh
   SYNC_EVIDENCE_DIR=$(mktemp -d)
   timeout 120s gh api --paginate 'repos/littlesho/NodeRampart/releases?per_page=100' \
     > "$SYNC_EVIDENCE_DIR/releases.json" &&
   timeout 30s gh api 'repos/littlesho/NodeRampart/releases/latest' \
     > "$SYNC_EVIDENCE_DIR/latest.json" &&
   python3 scripts/check-current-release.py \
       --published-releases-json "$SYNC_EVIDENCE_DIR/releases.json" \
       --published-latest-json "$SYNC_EVIDENCE_DIR/latest.json"
   ```

   For a future explicitly authorized Pre-release-only publication, use the
   list-only check and separately confirm that it was not selected as Latest.
   A real Latest 404 means unset, not Latest PASS. Do not fabricate a Latest
   object or use a request's `make_latest` parameter as readback evidence.

   The checker accepts one Release array, concatenated page arrays or slurped
   page arrays, bounded to 20 MiB, 20 pages and 2,000 entries. Preserve both fetch
   and checker results. A failed/incomplete traversal cannot satisfy this check;
   `&&` prevents checking partial output after a timeout or API failure. Keep the
   ordinary offline checker and affected tests; normal CI requires no network.
   The optional Latest file is bounded to 1 MiB and requires the complete list.
   It must contain one object with a positive integer ID, strict false draft and
   prerelease flags and a valid publication time, uniquely matching the current
   documentation pin and list-selected version. Duplicate or contradictory
   identities/JSON fields, an old Latest and missing paired input fail. An alpha
   tag with `prerelease=false` is valid; it remains Alpha. Run this joint check
   after updating the documentation workspace following real publication;
   ordinary offline checks do not require an unpublished candidate to be Latest.
4. Run required checks and independent review for the exact final PR head,
   normally merge the documentation/installer update through the existing PR
   flow, then read back main's exact merge commit and checks.
5. Read back the maintained raw-main installer entry and record actual results
   before marking the release task complete. PR success alone is not main success.

Publication, synchronization, review, normal merge and main verification are one
authorized release task. They do not require repeated authorization for the same
scope or a new bot/platform. Real failures, unexpected source changes and active
handoff/monitoring boundaries still stop the affected work. Preserve run IDs and
read back pending operations instead of replaying them. Any future event hook
must cover [release.published](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#release),
including prereleases published from drafts, and retain ordinary PR CI and merge
controls.

中文：发布完成包括核对真实公开版、同步当前文档／示例、必要检查与独立审查、正常
PR 合并 main，并读回 main 检查与维护入口。这是一项已授权发布任务的连续步骤。
保留历史及 NOT RUN，明确旧升级来源／回退目标；不得据此静默保留旧推荐，也不得
覆写已发布资产或移动 tag。实际失败、非预期源码变化和有效交接／监看边界仍须停止。

## Build-time generation

The release workflow scans final DEB/RPM artifacts on a dedicated Linux amd64
runner. It verifies native package identity, safely extracts only the three
regular program files into a private directory, and checks both ELF and Go
architecture metadata before running Syft. No target program is executed.
The collector requires all six package/SBOM/inspection triples and compares
package and program digests before it creates `dist/release`.

For a reviewed local development package, with Go 1.26.8 and the appropriate read-only
`dpkg-deb` or `rpm`/`rpm2cpio` inspection tools available:

```sh
TOOLS_DIR=$(mktemp -d)
python3 scripts/release_sbom.py --fetch-syft "$TOOLS_DIR/syft"
COMMIT=unknown
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
export COMMIT BUILD_DATE
make build
./scripts/build-deb.sh
LOCAL_DEB_VERSION=$(tr -d '\n' < VERSION | sed 's/-/~/')
python3 scripts/release_sbom.py "dist/noderampart_${LOCAL_DEB_VERSION}_amd64.deb" \
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


> Historical pre-publication follow-ups: the alpha.9 Unreleased statements below
> describe those earlier repair stages, before the separately authorized
> [alpha.9 publication](#alpha9-publication-and-public-distribution-verification).
> They preserve the original alpha.8 findings and failed-candidate outcomes.

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


## Alpha.9 publication and public distribution verification

> Publication-time record: the tests, defaults and README choice below describe
> that completed publication. Current installation policy and examples are in
> [Download and verify](#download-and-verify) and the [current policy](#current-release-and-installer-policy).

[v0.4.0-alpha.9](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.9) was published as a **non-latest alpha prerelease**.
This is the fixed hosted publication, not a rebuilt local candidate or a later
main documentation commit. Publication and anonymous verification receipts were
preserved separately from the earlier no-tag and authenticated draft records.

| Field | Value |
| --- | --- |
| Tag | `v0.4.0-alpha.9` (lightweight; ref object type `commit`) |
| Release ID | `402649106` |
| Published UTC | `2026-10-04T04:17:49Z` |
| Published Asia/Tokyo | `2026-10-04T13:17:49+09:00` |
| Source commit | `9cc75b6936d08099847655b5046c57a485c82ff7` |
| Source tree | `572f6b9dd6c58220cf786242bfcd4a08e71ddec1` |
| Original BUILD_DATE | `2026-10-04T00:21:25+08:00` (raw Git commit timestamp, unchanged) |
| Workflow | [37144860038 / attempt 1](https://github.com/littlesho/NodeRampart/actions/runs/37144860038/attempts/1), `.github/workflows/release.yml` |
| Workflow result | SUCCESS, 17/17 jobs |
| Release state | `draft=false`, `prerelease=true` |
| Latest | Not set; endpoint remained HTTP 404 at publication verification |
| Assets | Exactly 22 |
| Anonymous download | 22/22 PASS, 107128437 bytes |
| SHA256SUMS | 21 entries PASS; excludes itself |
| Provenance | 22 verified hosted asset subjects |
| SPDX subjects | Six verified runtime-package subjects |
| Project / DEB / RPM | `0.4.0-alpha.9` / `0.4.0~alpha.9` / `0.4.0-0.alpha.10.fc43/fc44` |
| Config / control API / sensor / database | 1 / 1 / 5 / 14 |

### Draft-to-public byte and proof continuity

Authenticated draft acceptance happened before publication. Anonymous download
was explicitly deferred until assets were public. One numeric-ID publication
PATCH returned HTTP 200; no title/body PATCH, rebuild, asset replacement,
rename or re-signing occurred. Immediately afterward, and again after anonymous
download, Release ID `402649106`, tag/source and all 22 asset IDs, original names,
sizes and service-provided digests matched the independently frozen draft set.
Normal download-count and URL changes did not indicate asset replacement.

A fresh anonymous direct-TLS client used no GitHub token, Authorization, cookie,
netrc or client proxy/configuration. Numeric Release ID, public tag endpoint and
complete paginated asset list were read anew; public URLs yielded 22 new byte
streams. Each file matched both the current asset identity and its independently
retained hosted-draft SHA256. The downloaded SHA256SUMS covered exactly the other
21 files and passed `sha256sum --strict --check`; the external inventory covered
all 22, including SHA256SUMS. The release.json source/version/raw BUILD_DATE and
bootstrap bytes matched the frozen source.

The original **22 provenance + six runtime-package subject SPDX cryptographic
verifications** passed against repository `littlesho/NodeRampart`, signer
`.github/workflows/release.yml`, tag ref `refs/tags/v0.4.0-alpha.9`, source and signer
digest `9cc75b6936d08099847655b5046c57a485c82ff7`, GitHub Actions OIDC identity,
GitHub-hosted runners and run/attempt `37144860038/1`. The predicates were
`https://slsa.dev/provenance/v1` and `https://spdx.dev/Document/v2.3`; the SPDX
subjects were the six runtime packages, not merely their sidecar JSON files.
Signed SPDX semantics and program digests matched the downloaded documents.
Full verifier output/exit receipts and policy bindings remain in protected
records. After publication all 28 results were **REUSED by exact subject digest
identity**, not re-executed for repetition or newly signed. Attestations establish
source, not absence of vulnerabilities, successful delivery or runtime readiness.

**20/22 hosted asset digests differed from the earlier local prerelease
candidate** despite the same source and original BUILD_DATE. Official public
artifact/provenance evidence therefore binds hosted bytes, not the local archive.
This does not invalidate appropriately scoped local preflight behavioral/runtime
evidence; it prevents treating local digests or local VM inputs as the digests or
execution evidence of the final hosted packages. The two byte sets stay separate.
The six package/SPDX/buildinfo groups, 18 packaged-program digests and source RPM
closure passed hosted static inspection; that inspection is not VM execution.

### Frozen public asset inventory

These exact rows are copied from the persisted anonymous inventory, independently
matched to the accepted hosted-draft inventory and API metadata. They are not
inferred from an older release. GitHub automatic source ZIP/TAR links are excluded.

| Asset ID | Original name | Bytes | SHA256 |
| ---: | --- | ---: | --- |
| 608354085 | `SHA256SUMS` | 2366 | `068a0108f453c0ab2838948823aaa3e4fcbf3506382e005285708e8a4bf6a34b` |
| 608354084 | `bootstrap.sh` | 10429 | `30c561cf84b702b63aa4aa7173b8e776491e749d2b8bfb18cb85c593af9f27e1` |
| 608354081 | `noderampart-0.4.0-0.alpha.10.fc43.aarch64.rpm` | 12159473 | `1a8ffed16eb033cdfe2dc7c2081088386f4ee23e1acd356cc57fe44266ad02ef` |
| 608354082 | `noderampart-0.4.0-0.alpha.10.fc43.aarch64.rpm.buildinfo.json` | 5547 | `f76239411718fe575e728d9d8585a597507503ceb546595fd57e3a344558efd0` |
| 608354083 | `noderampart-0.4.0-0.alpha.10.fc43.aarch64.rpm.spdx.json` | 60756 | `c7e3dd4c7e3bd6921afe0ea86cdc80cd11873ec40a3228f4aa99d819ce6915e9` |
| 608354093 | `noderampart-0.4.0-0.alpha.10.fc43.x86_64.rpm` | 13087994 | `fcf767a8c60fd1b7a2074291b77153d00f149a3158442d62b45a3db1d4f89f36` |
| 608354095 | `noderampart-0.4.0-0.alpha.10.fc43.x86_64.rpm.buildinfo.json` | 5540 | `70d952f71af473bf2dd09b186aa002372b64ea5c8193ceac1767035e370162e6` |
| 608354100 | `noderampart-0.4.0-0.alpha.10.fc43.x86_64.rpm.spdx.json` | 60716 | `c693d8fc48547210befeffc82e370ae55c45c742410eb517d37c1ee0dd19a0ae` |
| 608354101 | `noderampart-0.4.0-0.alpha.10.fc44.aarch64.rpm` | 12159473 | `433d19ab942a9913596850956be6b51ee310b320841b5f18eddf64db9c880d50` |
| 608354105 | `noderampart-0.4.0-0.alpha.10.fc44.aarch64.rpm.buildinfo.json` | 5547 | `3893648c36006f640bdb2ca1ea0ffe31943e9f7064c5b072113b9aa9365f778d` |
| 608354106 | `noderampart-0.4.0-0.alpha.10.fc44.aarch64.rpm.spdx.json` | 60756 | `dec7b4ae83d2add93ebc54e28fe3a801fe4f6391d130332daa9f2f83627fac7e` |
| 608354109 | `noderampart-0.4.0-0.alpha.10.fc44.src.rpm` | 31832867 | `20e5d7b2d5f91721fc6222eac6646373db50cb832579dead338acc5f9f3fd51a` |
| 608354116 | `noderampart-0.4.0-0.alpha.10.fc44.x86_64.rpm` | 13087994 | `2a51f7d7af7b678f04638b1bffafb98839a79d344be534a5eeef71c9095d243d` |
| 608354115 | `noderampart-0.4.0-0.alpha.10.fc44.x86_64.rpm.buildinfo.json` | 5540 | `45ed6d6abbfbfe515084807b39dd24e343cbb54f808b22433191b6cdb18d1ade` |
| 608354126 | `noderampart-0.4.0-0.alpha.10.fc44.x86_64.rpm.spdx.json` | 60716 | `389a4d5e1606af8715af6e5871528f5e8142c79c3a7b25b37ac30e6ffa322423` |
| 608354123 | `noderampart_0.4.0-alpha.9_amd64.deb` | 12664268 | `4f2e93b84407d6b4967aa133d09d65c8f8747b5eb94c41306bc9f06c44eabf82` |
| 608354128 | `noderampart_0.4.0-alpha.9_amd64.deb.buildinfo.json` | 7294 | `e62360d545e8bee5b2b28732331e9e49786d9c5276055ca57ba798bb8aeb3cd8` |
| 608354130 | `noderampart_0.4.0-alpha.9_amd64.deb.spdx.json` | 65232 | `15777527403778d8fb70e90f7bb4f43b432100fcdcd1fa196ac94dee159bfe02` |
| 608354135 | `noderampart_0.4.0-alpha.9_arm64.deb` | 11713100 | `1ef1e3c9a7d755f8c4495133eb63514b24b0aadbda3f1cf830032ad066e12969` |
| 608354136 | `noderampart_0.4.0-alpha.9_arm64.deb.buildinfo.json` | 7300 | `ebf9e99d50faaa95914d473768c03953227269897c39aa7eced69940980c03ec` |
| 608354139 | `noderampart_0.4.0-alpha.9_arm64.deb.spdx.json` | 65232 | `c9bf266f265bf559a1c98c244c525119cc5a5d378608a96bbc129085169dadb1` |
| 608354137 | `release.json` | 297 | `2f36aabd38f541b1333ca3d2549085a47dd7f28c34114a894e9d28ccf927972b` |

### Acceptance boundaries retained after publication

| Evidence or operation | Actual scope / result |
| --- | --- |
| Local no-tag preflight packages / VM behavior | Earlier local evidence; independent byte identities, not final hosted execution |
| Hosted asset identity / static package inspection | PASS, fixed hosted bytes |
| Authenticated 22 provenance + six package-subject SPDX | PASS; reused after publication by digest identity |
| Anonymous public distribution | PASS, 22/22 downloads and 21 checksum entries |
| Final hosted-package VM runtime | NOT RUN |
| Final 18 hosted-program binary vulnerability scans | NOT RUN |
| Real vendor APIs / human receipt | NOT RUN |
| Actual paid notification fees | NOT RUN |
| Native ARM64 runtime | NOT RUN; cross-build/static inspection is distinct |
| Production | NOT RUN |
| Alpha.8 historical snapshot unique cause | UNESTABLISHED |
| Alpha.8 Fedora public bootstrap | BLOCKED_NETWORK |
| P3 Chinese Telegram hidden-input wording ambiguity | Open, nonblocking; not fixed by this documentation PR |

GO-2026-5970 remains a **required-module vulnerability finding** for
`golang.org/x/text v0.21.0`. Source checks did not find the affected import or
reachable symbol; that does not mean zero vulnerabilities. Stripped-binary
symbol coverage and Fedora Go version-suffix standard-library matching gaps,
and recorded scanner/build-tool advisories remain disclosed. No new binary scan
or maintainer risk acceptance is implied by this documentation.

Published alpha.9 includes the identity-aware sensor receipt/watermark fix and
RPM Lua `%pretrans` conflict gate. Real pending/missing/mismatched/partial sensor
states remain visible; alpha.8 bytes and historical strict unknowns stay unchanged.
Historical failed CI, failed candidates, unknown results and earlier corrections
remain facts. Platform acceptance is not human delivery/read, native channels
remain direct-only, rollback needs matching database/configuration/credential
backups, and purge is not atomic.

No-argument bootstrap still selects **v0.4.0-alpha.5**. The README conservatively
keeps its previously selected alpha.7 runtime example; explicit alpha.9 assets
are public, but final hosted-byte runtime validation is NOT RUN. Later main docs
commits are **not** the alpha.9 release source, source of its 22 assets or attested
source. The tag and Release are not moved, edited or republished by this record.

本次按流程检查，但服务器未强制执行。

中文：alpha.9 已作为非 latest 的 alpha 预发布版公开；发行源永久绑定
`9cc75b6936d08099847655b5046c57a485c82ff7`，与后续 main 文档提交区分。
认证草稿验签先完成，公开后匿名获取 22 个新文件，21 条 checksum 和独立草稿
摘要逐项一致；按 subject digest identity 复用原 22+6 验签结果，未机械重验或
重新签发。20/22 hosted 摘要不同于原本地候选，官方发行证据绑定 hosted 字节。
本地运行证据保留其范围；最终 hosted 包 VM 运行及 18 个程序二进制漏洞新扫描
仍 NOT RUN。平台实网／人工接收／收费、原生 ARM64、生产及所有历史未知、网络
阻塞和安全扫描缺口均保留；公开分发通过不代表生产就绪。


## Alpha.10 publication and public distribution verification

[v0.4.0-alpha.10](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.10)
was published as an alpha prerelease from the final merged main source. The tag,
source and hosted bytes below remain its permanent identity; later documentation
commits on main are not the source of these packages.

| Field | Value |
| --- | --- |
| Tag | `v0.4.0-alpha.10` (lightweight; ref object type `commit`) |
| Release ID | `402977099` |
| Published UTC | `2026-10-04T11:54:33Z` |
| Source commit | `79ae500106e5d89b0b65b04bfa48e010dcdb39ac` |
| Source tree | `51f4b3f3778ac1ba46a5dddd65b13b6c2201602e` |
| Original BUILD_DATE | `2026-10-04T17:56:33+08:00` (raw Git commit timestamp, unchanged) |
| Workflow | [37195901702 / attempt 1](https://github.com/littlesho/NodeRampart/actions/runs/37195901702/attempts/1), `.github/workflows/release.yml` |
| Workflow result | SUCCESS, 17/17 jobs |
| Release state | `draft=false`, `prerelease=true` |
| Latest | No `make_latest` change requested |
| Assets | Exactly 22, 107622742 bytes |
| Authenticated draft checks | 22 identities, six package/SPDX/buildinfo pairs and 21 checksum entries PASS |
| Provenance / SPDX subjects | 22 hosted asset subjects + six runtime-package subjects PASS |
| Anonymous public distribution | 22/22 fresh downloads PASS, 107622742 bytes; 21 checksum entries PASS |
| Project / DEB / RPM | `0.4.0-alpha.10` / `0.4.0~alpha.10` / `0.4.0-0.alpha.11.fc43/fc44` |
| Config / control API / sensor / database | 1 / 1 / 5 / 14 |
| Alert context | v2 for validated diagnostics, otherwise v1; upgrade CLI and daemon together |

### Draft-to-public byte continuity

A successful complete anonymous Release-list read returned nine Releases on one
page and selected alpha.10 as the highest published product version. Numeric-ID
and tag readback matched Release ID `402977099`, source, state and published time.
A fresh anonymous direct-HTTPS client used no Authorization, token, cookie, netrc
or proxy environment. All 22 downloads returned HTTP 200, totaling 107622742
bytes, in 37.813 seconds; download child exit 0, without failures or retries.
Their IDs, original names, sizes and SHA256 values matched the independently
accepted draft set. Actual `sha256sum --strict --check SHA256SUMS` exited 0 with
21 OK entries. The independent inventory also covers SHA256SUMS. Public
release.json and bootstrap match the frozen source identity. No tag movement,
asset replacement, rebuild or re-signing was used for publication.

### Fixes and source acceptance

PR [#39](https://github.com/littlesho/NodeRampart/pull/39) was integrated with the
current main installation and fuzz-toolchain fixes before final validation. The
SSH collector distinguishes a genuinely absent journal unit from explicit empty,
null or unapproved units. An absent unit still requires a root sender, an allowed
OpenSSH executable and direct journal/syslog transport; user-service and inherited
stdout sources are rejected. Missing/null MESSAGE remains a collection-quality
failure until a new trusted record receives a durable ACK. Cursor, pending and
restart recovery retain their real-gap semantics.

GeoIP diagnostics use fixed allowlists through health events, notifications and
JSON/HTML evidence. The updater alone receives the capabilities needed for child
privilege dropping, cancellation and authenticated control-socket readiness.
Mode-0600 socket permissions, NoNewPrivileges, sandbox/SELinux boundaries and
child exit constraints remain enforced. Exact old generated units can migrate;
custom units, masks, drop-ins, timer state and failure history remain protected.
Migration does not download GeoIP, enable a disabled timer or clear health errors;
pending configuration uses guarded recovery.

The final candidate ran `make validate` once: exit 0, 906.18 seconds. The final
integration and merged main have the same source tree, so that matching local
suite and independent code review were reused. Hosted PR checks and the new main
CI/CodeQL checks were read separately; main
[CI 37193754860/1](https://github.com/littlesho/NodeRampart/actions/runs/37193754860/attempts/1)
and [CodeQL 37193754606/1](https://github.com/littlesho/NodeRampart/actions/runs/37193754606/attempts/1)
passed. Ordinary/race/coverage/Ubuntu and release-validation checks retain Go
1.26.8; the seven bounded fuzz targets alone use Go 1.27.1, and Fedora keeps its
native toolchain. Historical fuzz failure `37186197174/1` and PR #39's original
RPM source-manifest failure remain recorded.

### Hosted bytes, proof and runtime scope

The tag-triggered hosted workflow ran once. Authenticated draft downloads matched
all 22 actual API asset identities. Inspection of the six runtime payloads and
18 packaged programs matched their SPDX/buildinfo pairs and frozen source.
DEB programs report Go 1.26.8 and Fedora RPM programs Go
`1.26.8-X:nodwarf5`. The Fedora44 SRPM contains the 555 explicit source paths and
2261 vendor files, including the complete Go manifest, updater template and unit
capability inputs. Unit/capability inspection is static evidence, separate from
runtime execution.

The original cryptographic verification passed for 22 provenance subjects and six
runtime-package SPDX subjects against repository `littlesho/NodeRampart`, signer
`.github/workflows/release.yml`, tag ref `refs/tags/v0.4.0-alpha.10`, source/signer
digest `79ae500106e5d89b0b65b04bfa48e010dcdb39ac`, GitHub Actions OIDC identity,
GitHub-hosted runners and run/attempt `37195901702/1`. Predicates were
`https://slsa.dev/provenance/v1` and `https://spdx.dev/Document/v2.3`; signed SPDX
semantics and program digests matched the downloaded documents. Fresh anonymous
public downloads matched every accepted draft subject digest, so all 28 successful
proof results were **REUSED by exact subject digest identity**. No new
cryptographic verification commands were executed.

The first private static-audit harness exited 1 because its file-size resource
limit caused `dpkg-deb` to exit 2. That failure and original logs were preserved.
Correcting only that private tool limit produced exit 0 on the same unchanged
assets; no package was rebuilt or replaced. The initial authenticated by-tag
HTTP 404 was preserved and resolved through the complete Release list and numeric
ID. Separately, the body-only draft-note update returned an untagged placeholder
in `tag_name`; that original readback failure was preserved. After confirming the
frozen Git tag, an explicit same-ID metadata PATCH restored `tag_name`,
`target_commitish`, body, draft and prerelease identity. The Git tag and all 22
asset IDs/bytes remained unchanged; no new tag or Release was created.

| Evidence or operation | Actual scope / result |
| --- | --- |
| Final hosted Debian13 amd64 package | PASS: bounded alpha.9 upgrade, services, SSH collection and production-sandbox GeoIP MMDB entry/authenticated readiness smoke |
| Final hosted Fedora44 x86_64 package | PASS: bounded alpha.9 upgrade, services, SSH collection and production-sandbox GeoIP MMDB entry/authenticated readiness smoke |
| Other four hosted package runtime cases | NOT RUN |
| Licensed external MMDB download/account pipeline | NOT RUN; controlled fixtures do not establish account/download acceptance |
| Final 18 hosted-program binary vulnerability scans | NOT RUN |
| Real notification APIs / human receipt / actual fees | NOT RUN |
| Native ARM64 / production | NOT RUN |

The same-source candidate child privilege-drop/cancellation results and journal
trust/quality/durable-ACK recovery fixture results were **REUSED within their
unchanged source-input scope**; these cases were not rerun on the final hosted
packages.

Both authorized guests were stopped and exclusive ownership released. Synthetic
fixtures cover missing-unit/untrusted-source and diagnostic boundaries; they are
not real-network observations. The two hosted smokes are limited package/runtime
evidence, not a repeated full lab matrix or real notification delivery.
GO-2026-5970 in `golang.org/x/text v0.21.0`, stripped-binary coverage limitations,
Fedora toolchain suffix matching gaps and existing scan advisories remain disclosed;
this release does not claim zero vulnerabilities.

The alpha.9 publication-time NOT RUN records above remain historical facts.
PR [#40](https://github.com/littlesho/NodeRampart/pull/40) later added two limited
public alpha.9 package-install smoke PASS records on Debian13/Fedora44; those
later results do not rewrite the earlier publication-time record.

### Frozen hosted asset inventory

These actual authenticated hosted-draft rows independently matched all 22 new
anonymous public byte streams and API IDs/names/sizes/digests after numeric-ID
publication. GitHub automatic source ZIP/TAR
links are excluded. SHA256SUMS covers the other 21 assets; this external inventory
also covers SHA256SUMS itself.

| Asset ID | Original name | Bytes | SHA256 |
| ---: | --- | ---: | --- |
| 609674214 | `SHA256SUMS` | 2372 | `f4b3b5a75651b3f5415119de79ec7147f7f47387fa3b686fcd2b61a4bcba0149` |
| 609674217 | `bootstrap.sh` | 18494 | `7e3cd01d49ccc7977e5f1f1b3d27e99fcd6d7d25a1903e619f270bc4163448f4` |
| 609674206 | `noderampart-0.4.0-0.alpha.11.fc43.aarch64.rpm` | 12223763 | `dea72e229b6932082dba7498ff81b5d05125fe58404caa62901c9eef16f7e7af` |
| 609674218 | `noderampart-0.4.0-0.alpha.11.fc43.aarch64.rpm.buildinfo.json` | 5548 | `41cf06050c3b2a0a4cf0356314ca2b934abafe50d1e603d408a6e5f01a39e3ca` |
| 609674215 | `noderampart-0.4.0-0.alpha.11.fc43.aarch64.rpm.spdx.json` | 60756 | `6362bd366e5da1b7e83f88ac9ab8dd7f416624eeb8b65373c605de35acbb5849` |
| 609674232 | `noderampart-0.4.0-0.alpha.11.fc43.x86_64.rpm` | 13158968 | `7d39f4c8dbe5697a197cabc6647c5d509da72d4ab8b94583c47b4515487fed45` |
| 609674240 | `noderampart-0.4.0-0.alpha.11.fc43.x86_64.rpm.buildinfo.json` | 5541 | `86ded10dbbfd9e76a4d200eb4af23ed18329ff2ec9f23b66e66e812c552e6488` |
| 609674243 | `noderampart-0.4.0-0.alpha.11.fc43.x86_64.rpm.spdx.json` | 60716 | `37b4a30bfff35a519d4d301415bd0da0cf2a795ec36522f270f6ea2ede318575` |
| 609674244 | `noderampart-0.4.0-0.alpha.11.fc44.aarch64.rpm` | 12223763 | `b18905d972a9ff0e6c2e6aa3b511e7a390ceca84cafc4d360ff8c317823dd993` |
| 609674258 | `noderampart-0.4.0-0.alpha.11.fc44.aarch64.rpm.buildinfo.json` | 5548 | `67b8576ab8bc9ab07c9a14f07064cbfc3bc957bfb6c23777397019a6d5cb82f6` |
| 609674259 | `noderampart-0.4.0-0.alpha.11.fc44.aarch64.rpm.spdx.json` | 60756 | `5b4d1decd84820420deb9956d648d49afe139b6e90e5faa0961e4b266da7df3f` |
| 609674261 | `noderampart-0.4.0-0.alpha.11.fc44.src.rpm` | 31920838 | `4bd6b96f820e4db2e764c9fc55b95f2509324692441b06d1b2b35cf6534d4b71` |
| 609674269 | `noderampart-0.4.0-0.alpha.11.fc44.x86_64.rpm` | 13158968 | `4c969ac940d4701e873f27c77858a0dfb410aec54e914f08225b61be7864ed6c` |
| 609674273 | `noderampart-0.4.0-0.alpha.11.fc44.x86_64.rpm.buildinfo.json` | 5541 | `eacb7cb07234f2c0c197631a9a69544f7e0110df2b455832de5a821487b0327a` |
| 609674275 | `noderampart-0.4.0-0.alpha.11.fc44.x86_64.rpm.spdx.json` | 60716 | `a797ac662ad0d95b4132db223c71cd70d2cd9dc53167e0a017b7f28dd36774c2` |
| 609674285 | `noderampart_0.4.0-alpha.10_amd64.deb` | 12727950 | `cc38b96d19d78fdb494e402e58e46c606433b88ec884bbc0a52d6a34f6607476` |
| 609674296 | `noderampart_0.4.0-alpha.10_amd64.deb.buildinfo.json` | 7296 | `97975b7659ff8651fc2baa2475e66d7b2e8f44e4673e1c019eb5bac56c27d46e` |
| 609674295 | `noderampart_0.4.0-alpha.10_amd64.deb.spdx.json` | 65273 | `171ad78b52d767c921b377240cdb168dc4c3c9b58f0701df06bcd0723aaf47d8` |
| 609674299 | `noderampart_0.4.0-alpha.10_arm64.deb` | 11777062 | `e914c3ddfa19856b77a8607a04b2136f0f8ec9404b14223821e72e2de4f79629` |
| 609674301 | `noderampart_0.4.0-alpha.10_arm64.deb.buildinfo.json` | 7302 | `cee2d6af8970ce6e4be1190ed26f6b628253e678d129673a55416427d541d573` |
| 609674309 | `noderampart_0.4.0-alpha.10_arm64.deb.spdx.json` | 65273 | `96af96355366da39afdc6ac159eb7f9b433b9588e44e46ebe305c8db98796792` |
| 609674308 | `release.json` | 298 | `c5ae7354bfe016b55653b53d2b33e261d0c3e3bf6d49dd42220aa6c089a66a68` |

The maintained raw-main bootstrap resolves the highest published product version,
including prereleases, without consulting development VERSION or LATEST_RELEASE.
The alpha.10 asset freezes this implementation's code, while an omitted version
can select a later public release. Explicit `--version v0.4.0-alpha.10` pins these
bytes; an explicit alpha.9 pin still names the old release and obeys downgrade
checks. API, pagination, package or checksum failures do not fall back to alpha.9.

中文：alpha.10 已以同一 Release ID 公开，发行源永久为上表 main SHA/tree，
BUILD_DATE 保留原 Git 字符串。真实草稿 22 项身份、六组配对、21 条 checksum 与
22+6 验签通过；Debian13/Fedora44 两个正式 hosted 包完成有限升级与修复 smoke。
其他运行组合、18 程序新二进制扫描、真实通知、收费、ARM64 和生产仍 NOT RUN。
公开后 22 项新匿名下载、21 条 checksum 均通过，按完全相同的 subject digest
复用原 28 项验签，未重跑密码学命令；旧失败和漏洞披露保留。


<a id="alpha11-pre-release-publication-and-distribution-verification"></a>

## Historical alpha.11 initial Pre-release publication and distribution verification

At initial publication, [v0.4.0-alpha.11](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.11) was the highest public product version, **Alpha / Pre-release**, not GitHub Latest. The maintainer approved **SCOPED_PRE_RELEASE_WAIVER** for missing Fedora native durable ACK. This section preserves that stage; the [later ordinary Release/Latest promotion](#alpha11-ordinary-release-and-latest-promotion) is recorded separately. The original PRE-RELEASE-GATE BLOCKED and historical failures remain unchanged.

| Field | Value |
| --- | --- |
| Tag | `v0.4.0-alpha.11` (annotated) |
| Tag object | `3c1a175000cd59668e2db0e98b1ab10f957f2616` |
| Source commit | `2c9d4416adef3cb64e0523a1b9ac1e691b121c16` |
| Source tree | `8cb7a70af1b4bbae2db27c0f755c0e3df55d670b` |
| Release ID | `403384051` |
| Published UTC | `2026-10-08T03:08:01Z` |
| Public state | `draft=false`, `prerelease=true`, publication requested `make_latest="false"` |
| GitHub Latest | Not alpha.11; real before/after anonymous API response is 404 (unset) |
| Project / DEB / RPM | `0.4.0-alpha.11` / `0.4.0~alpha.11` / `0.4.0-0.alpha.12.fc43/fc44` |
| Frozen asset-set identity A | `456da439d5a45ae78bf610c9b029866b923d2d5c6fb8c689eea63f1f7589eb68` |
| SHA256SUMS SHA256 | `79734fb957a1767aba4af783db17a17184e43c95b7c03db77fed5c10d3314ed2` |
| BUILD_DATE | `2026-10-05T12:23:07+08:00` from C |

C is the formal merged release source; subsequent documentation main D does not replace C, move the tag or rebuild the assets. Main C [CI37263281706](https://github.com/littlesho/NodeRampart/actions/runs/37263281706) / attempt1 passed 16 jobs, with CodeQL37263281187 / attempt1 passed 2 analyses. The [Release37265168287](https://github.com/littlesho/NodeRampart/actions/runs/37265168287) / attempt1 passed 17 jobs, actual checkout C. Ubuntu program toolchain is Go1.26.8; Fedora runtime metadata records Go1.26.8-X:nodwarf5. The Go1.26.0 source declaration is not a prebuilt-install requirement. Config/API/sensor/database remain 1/1/5/14.

### Accepted scope and unexecuted cases

Four x86_64 lab VMs (Debian12/13, Fedora43/44) passed first-install, basic SQLite/schema14/integrity/consistent backup checks and English/Chinese TUI/setup NO readback, help and cancel. Applicable upgrades from real alpha.10, same-version reinstalls and normal service cycles passed. RPM soft stop/start passed; Debian VM soft reboot is not claimed. Keep-data removal/reinstall passed on Debian12 and the RPM family (explicit rpmsave restoration); final purge passed once on Debian12 and Fedora43. Outside protected backup/sentinels remained unchanged. These family paths are not four-VM purge coverage.

Debian12 normal trusted OpenSSH durable ACK passed for ordinary pending0 events and one normal service cycle. **Fedora43/44 native durable ACK is NOT RUN**: private MAC-label parsing FAILURE prevented the scene from starting. No product defect is confirmed there, and no evidence proves Fedora ACK works. Ordinary ACK does not prove natural fault, quiet recovery or trusted-once pending1 recovery; complete journal fault recovery remains NOT RUN.

Real MaxMind download and YES-save → exit → new-process YES → actual systemd timer-state closure remain NOT RUN / BLOCKED_BY_LAB_CONNECTIVITY. No MaxMind credentials/license-sensitive input was supplied to lab VMs. Native ARM64, long soak, real VPS and actual third-party notification delivery remain NOT RUN. All four VMs ended STOPPED; original failed environments/evidence were retained, not restored away. Old journal event ordering, old Fedora44 initial status errno and Windows Invalid argument interoperability causes are not backfilled from later successes.

The waiver accepts missing Fedora evidence only for this Alpha Pre-release. User VPS installation, personal license confirmation, real download and timer persistence are subsequent user acceptance. Formal Release or GitHub Latest needs that feedback and new explicit permission, with Fedora ACK coverage reconsidered. See the [English](../README.md#vps-pre-release-acceptance) and [Chinese](../README.zh-CN.md#vps-pre-release-acceptance) handoff; no Codex VPS deployment was performed.

### Public bytes and authenticated proof reuse

The same numeric Draft was published with one PATCH; tag/source/name/target and all 22 asset IDs, sizes and digests remained fixed. Anonymous curl requests used no Authorization, token environment, netrc or curlrc. All 22 files (108,090,970 content bytes) matched A; `sha256sum --check --strict SHA256SUMS` returned 0 for its 21 non-self entries. The full public list was one complete page with 10 objects and selected alpha.11 by product semantic precedence, including prereleases. Public page returned 200 and displayed Pre-release; maintained raw-main and fixed Release bootstrap bytes both match the frozen source hash `16c158c822494352e554f25e9f9fc4f49c4d8c9702f1e23fb9423bf27187b03e`.

The archived 22 provenance subject bindings and 6 runtime SPDX bindings were reused by identical digest, bound to `littlesho/NodeRampart`, `.github/workflows/release.yml`, `refs/tags/v0.4.0-alpha.11`, source/signer digest C, invocation `https://github.com/littlesho/NodeRampart/actions/runs/37265168287/attempts/1`, and deny-self-hosted. There are 22 assets and 28 proof bindings, verified by 7 commands; no new signature commands or asset replacement occurred. Provenance and reachable-source security gates do not imply a full independent binary scan of 18 hosted programs.

Public bootstrap `--help` and pinned `--version v0.4.0-alpha.11 --help` entry checks passed without installing on the host. Public package installation/default-resolution execution on a VPS remains user work; network fetch/byte identity and the complete-list check are distinct from installed runtime evidence. Current documentation uses only `--published-releases-json`; optional formal-Latest validation was NOT RUN.

| Asset ID | Name | Bytes | SHA256 |
| --- | --- | ---: | --- |
| 611571283 | `SHA256SUMS` | 2372 | `79734fb957a1767aba4af783db17a17184e43c95b7c03db77fed5c10d3314ed2` |
| 611571286 | `bootstrap.sh` | 18568 | `16c158c822494352e554f25e9f9fc4f49c4d8c9702f1e23fb9423bf27187b03e` |
| 611571280 | `noderampart-0.4.0-0.alpha.12.fc43.aarch64.rpm` | 12367602 | `7bea01c6cdf8840c6dca0fdf54b6ddf60e1fbf2da570a552911134c727e40749` |
| 611571281 | `noderampart-0.4.0-0.alpha.12.fc43.aarch64.rpm.buildinfo.json` | 5204 | `be48f193005af0d7a05a8b4e71e72b98dd200ffc7a763598f0ee6040ceffd778` |
| 611571285 | `noderampart-0.4.0-0.alpha.12.fc43.aarch64.rpm.spdx.json` | 58829 | `935809e8d9d21002a525998dc7c403ebd6c4b5adad81a5fd7eb184cf68c2a6b0` |
| 611571300 | `noderampart-0.4.0-0.alpha.12.fc43.x86_64.rpm` | 13329526 | `3d12780c280dc16402f61371d5a1cc9cc363e88425ba9f7d25cc8d5afb68b4f4` |
| 611571313 | `noderampart-0.4.0-0.alpha.12.fc43.x86_64.rpm.buildinfo.json` | 5197 | `7c54f11519e57d18c9f2995e18388ef39cfb2b6a422a159dda06ab52fb08edd2` |
| 611571315 | `noderampart-0.4.0-0.alpha.12.fc43.x86_64.rpm.spdx.json` | 58790 | `db7639f06400fc67eb113974a1408a133ef4f3f5be969330ae8f548b06799d0b` |
| 611571314 | `noderampart-0.4.0-0.alpha.12.fc44.aarch64.rpm` | 12367602 | `4908613d3f20e68c991b342926263a7a618924d0d9f15dd3118d91bca90a1786` |
| 611571323 | `noderampart-0.4.0-0.alpha.12.fc44.aarch64.rpm.buildinfo.json` | 5204 | `8a2ce5ff676c0e1027f00911bd84e9b5fb6f76684484db07fd729d58f475b897` |
| 611571334 | `noderampart-0.4.0-0.alpha.12.fc44.aarch64.rpm.spdx.json` | 58829 | `ca4c3d0fc61ca7e9937ab7fac6b6d6634f4c662d6210c87a35abda1c9a9b3fa6` |
| 611571341 | `noderampart-0.4.0-0.alpha.12.fc44.src.rpm` | 31457558 | `c16e84927b2783b4cefa97432dc889f9f7ca166c6677a9ab5a839381f154a0f8` |
| 611571348 | `noderampart-0.4.0-0.alpha.12.fc44.x86_64.rpm` | 13329526 | `02f0b3f04642b77b0bf918b0c3d1a81a4a129a725e93b4be1702ae06fe57d5aa` |
| 611571355 | `noderampart-0.4.0-0.alpha.12.fc44.x86_64.rpm.buildinfo.json` | 5197 | `c0b96a2f3a78a5a43fb4e3a6dff4e85ba7d6f3449246e8a9d00f4b226398b52a` |
| 611571356 | `noderampart-0.4.0-0.alpha.12.fc44.x86_64.rpm.spdx.json` | 58790 | `427e5707e4f7eaf9ec4f7c60a80b6fb60a27643a3e6458489b0dd8f3fba518d7` |
| 611571357 | `noderampart_0.4.0-alpha.11_amd64.deb` | 12886160 | `f6897054aa0d63ae49d4f735edae7ae5d22495fd08ff5060bdcd0a1fec151ed3` |
| 611571367 | `noderampart_0.4.0-alpha.11_amd64.deb.buildinfo.json` | 6884 | `8664eb87d11852c4da7517bfc5f1a6d6c58c0314891b4eb783c51ff1922e733d` |
| 611571368 | `noderampart_0.4.0-alpha.11_amd64.deb.spdx.json` | 63178 | `211e43db0956c2b4181e424d4ab42ebd323805cae399cc5e31a25ba056fc05ed` |
| 611571382 | `noderampart_0.4.0-alpha.11_arm64.deb` | 11935588 | `f4b27e4e7882dc78f18b870ab517e662fc6de210ac35983ef06c3d0f9b8442b3` |
| 611571379 | `noderampart_0.4.0-alpha.11_arm64.deb.buildinfo.json` | 6890 | `705f48a3d8b6ef3658ff4a688c79f85ab17308b36c23c56551d271f4dea0af90` |
| 611571384 | `noderampart_0.4.0-alpha.11_arm64.deb.spdx.json` | 63178 | `db784b75c2b9b95730249d2a51ec207382fd49c7fc059630a79ed13946a389e4` |
| 611571385 | `release.json` | 298 | `3fd843a2a19b6180559e35cabb24c7a6d23d1ccda4f295d9feecf4e710bfb90b` |

中文：alpha.11 已以同一个403384051公开为 Pre-release，未设Latest；C/tag/A永久不变。22项匿名字节及21条校验通过，复用同字节22+6证明。四机限定运行证据与家族卸载／purge通过，但Fedora原生ACK、完整故障恢复、真实MaxMind／YES闭环、ARM64、长期soak、VPS与真实通知仍未测；旧BLOCKED／FAILURE不改为通过。本人VPS验收后，正式Release／Latest须再次授权。

<a id="alpha11-ordinary-release-and-latest-promotion"></a>

## Alpha.11 ordinary Release and Latest promotion

On 2026-10-08 the maintainer reported real VPS acceptance and explicitly
authorized promotion of the same published Release **403384051**. Before the
single PATCH, authenticated reads matched C/tree, annotated tag, documentation
main D and every frozen asset ID/name/size/digest. The complete previous Release
body was retained and a bilingual promotion/acceptance record appended.

| Field | Value |
| --- | --- |
| Tag | `v0.4.0-alpha.11` (unchanged, annotated) |
| Tag object | `3c1a175000cd59668e2db0e98b1ab10f957f2616` |
| Source commit | `2c9d4416adef3cb64e0523a1b9ac1e691b121c16` |
| Source tree | `8cb7a70af1b4bbae2db27c0f755c0e3df55d670b` |
| Documentation main before promotion | `408a72fe4b8734b821fb99fcdf2cf83762e88367` |
| Release ID | `403384051` (unchanged) |
| Initial publication UTC | `2026-10-08T03:08:01Z` (unchanged `published_at`) |
| Promotion update UTC | `2026-10-08T04:55:00Z` (`updated_at` from actual response) |
| Public state | `draft=false`, `prerelease=false`; promotion requested `make_latest="true"` |
| Actual GitHub Latest GET | ID `403384051`, tag `v0.4.0-alpha.11` |
| Project / DEB / RPM | `0.4.0-alpha.11` / `0.4.0~alpha.11` / `0.4.0-0.alpha.12.fc43/fc44` |
| Frozen asset-set identity A | `456da439d5a45ae78bf610c9b029866b923d2d5c6fb8c689eea63f1f7589eb68` |
| SHA256SUMS SHA256 | `79734fb957a1767aba4af783db17a17184e43c95b7c03db77fed5c10d3314ed2` |

### VPS acceptance and retained Alpha gaps

**USER-REPORTED VPS ACCEPTANCE PASS:** the maintainer reports anonymous GitHub
download/upgrade of the published alpha.11 package, successful retest of the
original bug, real City/ASN downloads using their own legitimate MaxMind
credentials and personal license confirmation, and **YES save → full exit →
new-process YES → actual systemd timer enabled**. These are user-reported results,
not independent automated VPS logs. No credentials are recorded; Codex did not
deploy to the VPS or repeat the tests. The report does not claim the optional
VPS NO reverse loop or every UI entry/language combination.

The maintainer explicitly accepts **Fedora43/44 native durable ACK, complete
journal fault recovery, native ARM64 and long soak NOT RUN** for this Alpha
promotion. Private MAC-label parsing FAILURE prevented the Fedora scenario from
starting; neither product failure nor success is established. Debian12 ordinary
durable ACK is not fault recovery, and VPS GeoIP results do not replace Fedora
evidence. Real third-party notification delivery and full independent binary
scans of the 18 hosted programs also remain unverified.

The product remains **Alpha**. Ordinary GitHub Release/Latest and
`prerelease=false` are distribution settings, not stable maturity or production
readiness. Earlier lab MaxMind/YES **NOT RUN / BLOCKED_BY_LAB_CONNECTIVITY** and
the original PRE-RELEASE-GATE BLOCKED, FAILURE, PENDING, SKIP and unknown causes
retain their historical meaning. No lab tests or private harness work were
resumed for this promotion.

### Public readback and unchanged bytes

Independent authenticated and anonymous Release/Latest GETs returned the same
ID/tag and strict false draft/prerelease flags. The complete anonymous published
list contained 10 objects on one complete page and selected alpha.11 as the
highest product version. The Latest page redirected to the fixed alpha.11 tag;
the Latest bootstrap download matched A. Tag peeling remained C.

All **22 assets** were downloaded anonymously again after promotion: **108,090,970
bytes**, exact IDs/names/sizes/SHA256 against the unchanged inventory above, and
all **21** non-self checksum entries passed `sha256sum --check --strict`. The
matching **22 provenance + 6 SPDX bindings** were reused under the unchanged
repository/workflow/tag/source/invocation and deny-self-hosted identity described
in the initial record. No new signature verification, build, upload, asset
replacement or tag movement occurred.

Maintained raw-main bootstrap matched C/A exactly. Its unchanged production
`bootstrap_latest_release()` function was invoked read-only against the real
anonymous API and returned `v0.4.0-alpha.11`, using its original bounded full-list
resolution. Explicit `--version v0.4.0-alpha.11` parameter/native mapping and
no-default-list-query evidence are reused from the matching C tests; no whole
installer or new VPS deployment was executed here. The current documentation
check uses the real complete published-list capture together with the actual
Latest GET through `--published-latest-json`; ordinary offline checks remain
unchanged. Documentation commits do not replace permanent release source C.

中文：同一个 Release 403384051 已按新的明确授权晋级为普通 Release／GitHub
Latest，产品仍是 Alpha。本人 VPS 匿名升级、Bug 复测、合法许可下真实 City／ASN
下载及 YES 保存—退出—新进程回填—timer enabled 仅记为 **USER-REPORTED PASS**。
Fedora 原生 ACK、完整故障恢复、原生 ARM64、长期 soak 仍 **NOT RUN**，本次已明确
接受风险；旧失败和初次 Pre-release 记录保留。22 项重新匿名下载及 21 条 checksum
通过，复用同身份 22+6 证明，C/tag/A 不变；真实 Latest／完整列表联合校验不以请求
参数代替读回，也不意味着 stable 或全部环境验收完成。
