# Development and validation

## Local checks

```bash
make fmt-check
make vet
make test
make test-race
make build
go mod verify
python3 scripts/test-packaging.py
python3 scripts/test-lab-check.py
python3 scripts/test-netns.py --self-test
```

The ordinary test suite does not require root or packet-capture privileges.
Run `gofmt -w` on changed Go files before `make fmt-check`. The required direct
static-build check is `CGO_ENABLED=0 go build ./cmd/...`; `make build` also stamps
the three binaries with version and commit metadata. Packaging regressions use
temporary paths and mocked lifecycle commands; the native RPM case is skipped
when `rpmspec` is absent, so run it on Fedora as well.

CI also runs a native Fedora 44 RPM/SRPM build and seven separate bounded fuzz
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
the previous build available. New source installations record SHA256 ownership
for their three binaries and two full units. Prepare a source-to-package
transition with the checked-out script, then install the reviewed package:

```bash
sudo ./scripts/source-to-package.sh --prepare
sudo apt install ./dist/noderampart_0.4.0~alpha.1_amd64.deb
sudo /usr/bin/noderampart doctor
sudo /usr/bin/noderampart status
```

On Fedora, use `sudo dnf install ./dist/rpm/noderampart-*.x86_64.rpm` for the
package step. The transition verifies every ownership hash before stopping
services and removes only those five source files and their ownership manifest.
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
  --install-kind package --expected-version 0.4.0-alpha.1 \
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

Earlier private lab evidence applies only to its exact development snapshot.
See [the public validation scope](ALPHA_LIMITATIONS.md) for the remaining gaps.
These selected checks do not replace the VM matrix below.

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

## Package release checklist

Publishing source alone creates no package release or version tag.

1. `go mod verify`, formatting, vet, race tests, and all unit tests pass.
2. Static amd64 and arm64 binaries build with `CGO_ENABLED=0`.
3. Full Git history and artifacts pass a secret scan.
4. Dependency vulnerability and license review is recorded.
5. DEB/RPM lifecycle matrix and systemd sandbox tests pass.
6. Changelog, compatibility matrix, known limitations, checksums, SBOM, and provenance are attached.
7. No release is marked stable until the privileged VM matrix and soak exit criteria are met.
