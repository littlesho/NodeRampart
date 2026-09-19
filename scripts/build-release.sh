#!/bin/sh
# SPDX-License-Identifier: MIT
set -eu
umask 022

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
cd "$PROJECT_DIR"
VERSION=$(tr -d '\n' < VERSION)
[ "$VERSION" = 0.4.0-alpha ] || { echo 'release tooling currently targets 0.4.0-alpha' >&2; exit 1; }
COMMIT=${COMMIT:-$(git rev-parse HEAD)}
BUILD_DATE=${BUILD_DATE:-$(git show -s --format=%cI HEAD)}
case "$COMMIT" in ''|*[!0-9a-f]*) echo 'invalid commit' >&2; exit 1;; esac
case "$BUILD_DATE" in ''|*[!0-9TZ:+.-]*) echo 'invalid build date' >&2; exit 1;; esac
export COMMIT BUILD_DATE
[ "$#" -eq 2 ] || { echo 'usage: build-release.sh {--deb|--rpm} {amd64|arm64} | --collect ARTIFACT_DIRECTORY' >&2; exit 1; }
case "$1" in
  --deb|--rpm)
    ARCH=$2
    case "$ARCH" in amd64|arm64) ;; *) echo 'unsupported architecture' >&2; exit 1;; esac
    export ARCH
    if [ "$1" = --deb ]; then
      GOOS=linux GOARCH="$ARCH" make build
      ./scripts/build-deb.sh
    else
      ./scripts/build-rpm.sh
    fi
    ;;
  --collect)
    # Collection does not publish anything. The CI publishing job consumes this
    # complete directory only after all architecture jobs have passed.
    python3 - "$2" "$PROJECT_DIR" "$COMMIT" "$BUILD_DATE" <<'PY'
import hashlib, json, os, pathlib, shutil, stat, sys, tempfile
source, project = (pathlib.Path(p).resolve(strict=True) for p in sys.argv[1:3])
commit, build_date = sys.argv[3:5]
sys.path.insert(0, str(project / 'scripts'))
from release_sbom import runtime_packages, validate_pair
if not source.is_dir():
    raise SystemExit('artifact input must be a directory')
packages = runtime_packages()
names = set(packages)
names.update(name + suffix for name in packages for suffix in ('.spdx.json', '.buildinfo.json'))
names.add('noderampart-0.4.0-0.alpha.1.fc44.src.rpm')
found = {}
for path in source.rglob('*'):
    if path.name not in names:
        continue
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > 256 * 1024 * 1024:
        raise SystemExit('release input contains an unsafe or oversized asset')
    if path.name in found:
        raise SystemExit('release input contains duplicate asset names')
    found[path.name] = path
if set(found) != names:
    raise SystemExit('release assets are incomplete: ' + ', '.join(sorted(names - set(found))))
for name in sorted(packages):
    validate_pair(found[name], found[name + '.spdx.json'], found[name + '.buildinfo.json'], commit, build_date)
dist = project / 'dist'
dist.mkdir(exist_ok=True)
output = dist / 'release'
if output.exists() or output.is_symlink():
    raise SystemExit('dist/release already exists; retain or remove that dedicated output before collecting again')
stage = pathlib.Path(tempfile.mkdtemp(prefix='.release-', dir=dist))
try:
    for name, path in found.items():
        shutil.copyfile(path, stage / name)
    shutil.copyfile(project / 'scripts/bootstrap.sh', stage / 'bootstrap.sh')
    (stage / 'release.json').write_text(json.dumps({'format': 1, 'version': '0.4.0-alpha',
        'commit': commit, 'build_date': build_date, 'package_count': 6,
        'source_package_count': 1, 'sbom_count': 6,
        'sbom_scope': 'packaged Go programs; runtime system dependencies excluded'}, indent=2) + '\n')
    checksums = []
    for path in sorted(stage.iterdir()):
        with path.open('rb') as stream:
            digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        checksums.append(f'{digest}  {path.name}\n')
        path.chmod(0o644)
    (stage / 'SHA256SUMS').write_text(''.join(checksums))
    stage.chmod(0o755)
    stage.rename(output)
finally:
    if stage.exists():
        shutil.rmtree(stage)
print('Complete release assets collected in dist/release; nothing was published.')
PY
    ;;
  *) echo 'unknown release build operation' >&2; exit 1;;
esac
