#!/bin/sh
# SPDX-License-Identifier: MIT
set -eu
umask 022

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
cd "$PROJECT_DIR"
VERSION=$(tr -d '\n' < VERSION)
[ "$VERSION" = 0.4.0-alpha.8 ] || { echo 'release tooling currently targets 0.4.0-alpha.8' >&2; exit 1; }
# Empty workflow outputs must never fall back to local Git or the wall clock.
# Obtain both declarations once with release-metadata.sh in the source job.
COMMIT=${COMMIT-}
BUILD_DATE=${BUILD_DATE-}
python3 - "$COMMIT" "$BUILD_DATE" <<'METADATA'
import datetime, re, sys
commit, date = sys.argv[1:]
if not re.fullmatch(r'[0-9a-f]{40}', commit):
    raise SystemExit('invalid commit: release commit must be the full checked-out commit')
if not re.fullmatch(r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:Z|[+-][0-9]{2}:[0-9]{2})', date):
    raise SystemExit('release build date must be the source commit timestamp')
datetime.datetime.fromisoformat(date.replace('Z', '+00:00'))
METADATA
export COMMIT BUILD_DATE
[ "$#" -eq 2 ] || { echo 'usage: build-release.sh {--deb|--rpm} {amd64|arm64} | --collect ARTIFACT_DIRECTORY' >&2; exit 1; }
# Container builders may own the checkout differently. Trust only this fixed
# source root for these commands; never change global Git configuration.
CHECKED_COMMIT=$(git -c safe.directory="$PROJECT_DIR" rev-parse --verify HEAD)
[ "$COMMIT" = "$CHECKED_COMMIT" ] || { echo 'release commit does not match the checked-out source' >&2; exit 1; }
[ "$BUILD_DATE" = "$(git -c safe.directory="$PROJECT_DIR" show -s --format=%cI "$COMMIT")" ] || { echo 'release build date does not match the source commit' >&2; exit 1; }
git -c safe.directory="$PROJECT_DIR" diff --quiet HEAD -- || { echo 'release source has tracked changes; use ordinary local build/package commands for dirty candidates' >&2; exit 1; }
# Downloaded workflow artifacts are not source. Every other untracked input
# makes an official clean-commit assertion uncertain; never print their names.
UNTRACKED_INPUTS=$(git -c safe.directory="$PROJECT_DIR" ls-files --others --exclude-standard -- . ':(exclude)release-input')
[ -z "$UNTRACKED_INPUTS" ] || { echo 'release source has untracked inputs; use ordinary local build/package commands for dirty candidates' >&2; exit 1; }
case "$1" in
  --deb|--rpm)
    ARCH=$2
    case "$ARCH" in amd64|arm64) ;; *) echo 'unsupported architecture' >&2; exit 1;; esac
    export ARCH
    if [ "$1" = --deb ]; then
      LOCAL_PACKAGE="dist/noderampart_0.4.0~alpha.8_${ARCH}.deb"
      RELEASE_PACKAGE="dist/noderampart_${VERSION}_${ARCH}.deb"
      [ ! -e "$RELEASE_PACKAGE" ] && [ ! -L "$RELEASE_PACKAGE" ] || { echo 'official package output already exists; preserve it before rebuilding' >&2; exit 1; }
      GOOS=linux GOARCH="$ARCH" make build
      ./scripts/build-deb.sh
      # Native Debian Version retains '~' ordering; public asset names must
      # remain portable so GitHub does not rename the checksum-bound package.
      mv -- "$LOCAL_PACKAGE" "$RELEASE_PACKAGE"
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
from release_sbom import runtime_packages, release_assets, validate_pair
if not source.is_dir():
    raise SystemExit('artifact input must be a directory')
packages = runtime_packages()
names = release_assets() - {'bootstrap.sh', 'release.json', 'SHA256SUMS'}
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
    (stage / 'release.json').write_text(json.dumps({'format': 1, 'version': '0.4.0-alpha.8',
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
