# Verify a NodeRampart release

The public `v0.4.0-alpha` download and its GitHub attestations have **not been
published**. The workflow creates a draft for a maintainer to inspect; writing
or testing the workflow does not publish a release. The commands below apply
after the intended release and attestations are available.

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
gh release download v0.4.0-alpha --repo littlesho/NodeRampart --dir "$VERIFY_DIR"
cd "$VERIFY_DIR"
sha256sum -c SHA256SUMS

PACKAGE=noderampart_0.4.0~alpha_amd64.deb
EXPECTED_COMMIT='REPLACE_WITH_REVIEWED_FULL_COMMIT_SHA'

# Verify the package's provenance against the intended repository/workflow/tag.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha \
  --source-digest "$EXPECTED_COMMIT" \
  --predicate-type https://slsa.dev/provenance/v1 \
  --deny-self-hosted-runners

# Verify the SBOM statement bound to the same package bytes.
gh attestation verify "$PACKAGE" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha \
  --source-digest "$EXPECTED_COMMIT" \
  --predicate-type https://spdx.dev/Document \
  --deny-self-hosted-runners

# The separately downloaded inventory also has its own provenance statement.
gh attestation verify "$PACKAGE.spdx.json" \
  --repo littlesho/NodeRampart \
  --signer-workflow littlesho/NodeRampart/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0-alpha \
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
python3 scripts/release_sbom.py dist/noderampart_0.4.0~alpha_amd64.deb \
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
