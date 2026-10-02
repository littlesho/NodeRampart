# alpha.8 release preflight / 发布预验收

Subsequent status, 2026-10-02: alpha.8 is published as a non-latest prerelease
from `77ae069b8f00651106b9621a24047b0ad7b4e88d`; see the separate
[publication record](RELEASE_VERIFICATION.md#alpha8-publication-and-distribution-verification).
The no-tag preflight and authorization boundaries below retain their original
stage scope. They do not retroactively prove hosted or public distribution.

This is the **LOCAL PRE-RELEASE CANDIDATE** stage. It does not create a tag,
Release/draft, release asset or attestation. [English proposed notes](RELEASE_NOTES_ALPHA8.en.md)
and [中文拟用说明](RELEASE_NOTES_ALPHA8.zh-CN.md) are unpublished text, not a Release object.
The [previous feature acceptance](ALPHA8_ACCEPTANCE.md) remains a separate snapshot.

本阶段从已合并 PR #27 的 main
`de801c3b4472bc7ab48c1515d1de0cfd13a93553` 开始，修补安装器显式版本入口，
审查并合并必要修复，再冻结新的完整 main SHA。此前 source53 包不重标、不覆盖，
也不能证明本次新包身份。本记录在准备 PR 合并前保存；最终冻结 SHA、tree、
提交时间、22 文件清单和实际日志保存于仓库外验收目录，并由任务报告交付。
文档不证明自身 SHA，不提前宣称未来构建、VM 或 main 检查成功。

## Three separate stages / 三个授权阶段

| Stage | Scope | Current authorization |
| --- | --- | --- |
| Local preflight | Installer/test/docs PR, ordinary CI, clean source freeze, six packages/SBOM/local collect, approved lab delta checks | Authorized; this document's stage |
| Tag + hosted draft | Exact-source tag, matching release.yml run, real draft download, provenance/SPDX verification and hosted-package acceptance | Separate explicit authorization required; NOT RUN here |
| Public distribution | Publish the reviewed draft, anonymously download exact assets, verify names/digests and public bootstrap | Further explicit authorization required; NOT RUN here |

Even when every local check passes, stop before creating **any** local/remote tag.
No release workflow dispatch, draft/Release creation, formal asset upload,
attestation signing, latest/default change or production deployment occurs here.
本地通过不是连续授权；下一阶段结束后公开发行仍须再次明确授权。

Correction to the execution record: an inherited metadata regression initially
created an annotated tag inside its disposable fixture repository, contrary to
this stage's no-local-tag boundary. That fixture was cleaned up; no product or
remote release tag was created. The test now simulates only tag peeling while
retaining real commit/date checks and asserting zero fixture tag refs/objects.
The earlier run is retained as a boundary violation, not described as tag-free.

## Installer repair / 安装器修复

The observed source at de801c3 rejected `--version v0.4.0-alpha.8` in its fixed
version case before root/download/install checks. The minimal repair adds the
fixed mapping below, help and rejection text, using the existing controlled
fixture mechanism for regression tests. It does not add an arbitrary download
source, change DNS/hosts, relax TLS or redirect/size limits, or change service/setup
choices. Default remains `v0.4.0-alpha.5`; alpha/alpha.5/alpha.6/alpha.7 mappings stay.

| Explicit selection | DEB native Version / portable asset | RPM assets |
| --- | --- | --- |
| `v0.4.0-alpha.8` | `0.4.0~alpha.8`; `noderampart_0.4.0-alpha.8_{amd64,arm64}.deb` | `noderampart-0.4.0-0.alpha.9.fc{43,44}.{x86_64,aarch64}.rpm` |

候选源码支持显式 alpha.8；**资产公开后方可用于公开下载安装**。公开安装主示例
仍选择已发布、已验证的 alpha.7；不提供声称当前可用的 alpha.8 下载命令。
404 必须明确停止，不回退旧版/其他渠道。checksum 缺失、重复、错误，包名/版本/
架构错误、超限下载、错误参数、越界重定向、源安装冲突和自动降级必须 fail closed。
`--no-setup`、分步 setup、终端/UTF-8 和原服务启停选择保持兼容。
受控 fixture 的通过不等于匿名官方 bootstrap 安装通过。

## Source freeze and builds / 来源冻结与构建

After normal exact-head PR merge and checks, record `RELEASE_SOURCE` as the
full reviewed remote main commit, `SOURCE_TREE`, its original `%cI` commit
timestamp as `BUILD_DATE`, clean checkout and hashes of VERSION, go.mod/go.sum,
source manifest, package/build/SBOM/metadata scripts and release workflow. A later
main advance does not move the frozen source automatically. Any added fix needs
review, a new freeze and revalidation of affected evidence.

Use a clean independent checkout of that source. Read
`EXPECTED_COMMIT=<full source> scripts/release-metadata.sh` **once**, parse its
exact `commit=` and `build_date=` fields as data, never `eval`, and reuse both
values unchanged for every build, SBOM and collect. Empty values must be rejected,
not replaced with Git defaults or wall-clock time. `make validate` remains the
complete required entry, including race/coverage, scanners and MMDB descendant
cancellation assertions.

The existing `build-release.sh --deb/--rpm/--collect` supports clean no-tag local
work. Its name does not grant hosted release provenance. The matching-version
**tag guard in release.yml remains**; do not create a fake/temporary tag to run it.
Existing ordinary CI may build exact-main RPMs in actual Fedora43/44 containers;
record source/date/run/job/environment and each package's real Go buildinfo.
A distro label shim or Fedora44-only build is not Fedora43 compilation. No CI
permissions, protection, network or guest infrastructure changes are required.

| Build | Required evidence |
| --- | --- |
| DEB amd64/arm64 | Clean frozen source, Go/toolchain/GOOS/GOARCH/CGO, portable name and native Version, three packaged program digests |
| RPM Fedora43/44 × x86_64/aarch64 | Actual corresponding distribution build environment, real Go buildinfo and PIE inputs, canonical sourceRPM from Fedora44/amd64 |
| All six SPDX/buildinfo pairs | Pinned Syft and package inspector, actual package SHA, three program/platform/module inventories, same declared full source/date |

独立程序与 DEB 是 loader-free static；RPM 保留既有 CGO=0 PIE/system-loader
构建方式。CGO=0 不等于无 loader。amd64 CLI/daemon 的内嵌身份需实际观察；sensor
无版本 CLI，使用包字节和实际启动日志。arm64 静态检查不冒充原生执行。

## Exact 22 files / 完整资产集合

`release_assets()` is authoritative. Expect six runtime packages, their six
`.spdx.json` and six `.buildinfo.json`, one
`noderampart-0.4.0-0.alpha.9.fc44.src.rpm`, `bootstrap.sh`, `release.json` and
`SHA256SUMS`: **22 files**. GitHub automatic source ZIP/TAR links are excluded.
`SHA256SUMS` covers the other 21 files, never itself. An external frozen manifest
records **all 22** names, byte counts and SHA256, including SHA256SUMS itself.

Run every existing package/SPDX/buildinfo pair validator and clean
`build-release.sh --collect`. Check bootstrap bytes equal frozen source and
support explicit alpha.8, release.json source/date/version/counts, regular-file/
owner/link/type/size/duplicate bounds, package scripts/helper/units/licenses and
sourceRPM reviewed file manifest. Exclude private policies, inventories,
credentials and raw logs. Source/doc changes can change sourceRPM/package contents;
new package bytes and declarations always need fresh proof.

输出独立新目录并明确标为 LOCAL PRE-RELEASE CANDIDATE。禁止混合 source53 或其他
提交的 package/SPDX/buildinfo；保留旧包和证据。归档及其摘要在外部冻结清单记录，
不是正式 release asset 或 attestation。

## Security and reuse / 安全与证据复用

Freshly read [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970) and record the
official modified timestamp, database check time, scanner version and fixed
version. At the start, x/text v0.21.0 is still in the graph; the official repair
is v0.39.0 for unicode/norm. The earlier no-import conclusion is not automatically
valid for new source or packages. Inspect all three product commands for Linux
amd64/arm64, actual static/PIE/build tags, and actual packaged binaries. Separate
required module, imported affected package, reachable affected symbol and use by
tests/build tools. Exit 0 alone is insufficient. No dependency/scanner upgrade or
ignore expansion is authorized by this repair. New product imports/reachability
require reassessing merge/preflight gates; this task cannot sign maintainer risk
acceptance. Do not claim zero vulnerable modules.

The fresh starting-source assessment on 2026-10-01 bound to de801c3 used
govulncheck v1.7.0 and Go 1.26.8. The official record was checked at
15:03:04 UTC (record modified 2026-08-10T22:42:36Z; database timestamp
2026-09-28T16:43:40Z). Twelve source combinations covered all three commands,
both architectures and static/actual RPM PIE inputs. CLI/daemon included x/text
in their module graph, but did not import unicode/norm or reach affected symbols;
sensor did not include x/text in its product graph. Test dependency inspection
also found no unicode/norm import. This is starting-source evidence, not final
package proof or maintainer risk acceptance; the frozen source and actual binaries
still require their own assessment, including scanner/toolchain compatibility.

Create a reuse table linking old source/package digest, exact test scope, changed
relevant inputs, reusable conclusions and remaining new execution. Old behavior
can support unchanged sender/outbox/schema semantics; it cannot prove new package
bytes/source or an unexecuted rollback. Previous Debian records verified backup
copies/old-binary refusal; Fedora records did not execute backup restoration.
Neither is a complete matching-backup service rollback proof.

For Debian12/13 and Fedora43/44 amd64, install this frozen source's actual package,
start/check embedded CLI/daemon identity and sensor startup, inspect real service
UID safe credentials, optional failure isolation and managed removal. Exercise
alpha.7 upgrade/preserved schema12→13 data/config/queue/language/target/TTL/cooldown
and a matching old DB/config/synthetic credential backup rollback, using supported
approved offline/package methods. Keep every usable synthetic target disabled;
an isolated missing-credential channel may be enabled only to test runtime
fail-closed behavior, with no usable sender or vendor request. Read-only config
validation is distinct from daemon runtime configuration. No vendor send occurs.
Run installer changes through controlled fixtures, not a pretend public source.
Use only approved four wrappers with exclusive ownership and bounded cleanup;
no restore/reset/hard-stop, host/guest network, Broker/ACL/mapping or SELinux change.
Clean own objects, normally stop and independently confirm STOPPED.

真实六平台 API/人工接收、原生 ARM64、公开 bootstrap、正式 hosted provenance 均为
NOT RUN。本次数据不足时必须写 BLOCKED/NOT RUN，不能把旧包或 mock 算作新包安装。
Teams 只支持选定 Anyone 秘密 URL/Adaptive Card 工作流，并仅确认请求接受；原生
渠道直连无代理。schema13 回退须匹配旧 DB/config/credentials；purge 不是原子安装
回滚。平台条款/管理员授权仍独立于源码许可证。

## Governance and next-stage checklist / 治理及后续未执行计划

Initial read-only inspection on 2026-10-01 found main unprotected, no effective
main rules or repository/parent rulesets. Re-read before merge and freeze.
若仍如此，准确记录：**本次按流程检查，但服务器未强制执行。** No protection,
ruleset, tag policy or CI permission is changed. Current same-name tag/Release
existence must be rechecked; existing objects are never moved or overwritten.

The intended future tag is `v0.4.0-alpha.8`; its exact `RELEASE_SOURCE` comes from
the external frozen record, not a moving branch or a self-reported release.json.
The following is a plan, **not executed in local preflight**:

1. Obtain independent tag/draft authorization, create the exact-source tag and let
   matching release.yml build in the hosted environment. Record tag commit,
   workflow/run/attempt/environment and actual numeric Release ID.
2. Keep draft=true and prerelease=true, no latest/default change. Verify actual
   uploaded names, states and bytes, not just counts or a green workflow.
3. Verify provenance for all 22 assets and SPDX attestation for six runtime
   packages. These are verification subjects, not necessarily 22 bundle files.
   Lock repository, release.yml signer workflow, exact tag ref, independently
   reviewed source digest and each actual subject digest during verification.
4. Re-download from the authenticated draft and verify its actual bytes, then
   perform appropriate acceptance on those hosted packages. Local candidates
   do not replace hosted construction, draft-download, attestation or runtime.
5. Obtain further explicit public-distribution authorization. On that same frozen
   Release/asset set, anonymously download and verify original names/digests and
   execute public bootstrap acceptance. Authenticated draft reads are not anonymous
   distribution; injected fixtures are not public installation; cross-build is not
   ARM64-native execution.
6. Hosted bytes may differ from local candidates. Compare each stage's own frozen
   digests rather than requiring cross-environment bit-for-bit identity or writing
   new hosted digests over old local evidence.

Stop after a precise, reviewed source and complete no-tag preflight evidence are
available. A technical PASS only permits requesting the next authorization.
