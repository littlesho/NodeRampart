#!/bin/sh
# SPDX-License-Identifier: MIT

set -eu
umask 022

command -v rpmbuild >/dev/null 2>&1 || { echo "rpmbuild is required (install rpm-build)" >&2; exit 1; }
command -v tar >/dev/null 2>&1 || { echo "tar is required" >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo "Go 1.25 or newer is required" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "Python 3 is required to stage the reviewed source manifest" >&2; exit 1; }

ARCH=${ARCH:-$(go env GOARCH)}
case "$ARCH" in
  amd64) RPM_TARGET=x86_64;;
  arm64) RPM_TARGET=aarch64;;
  *) echo "unsupported architecture: $ARCH (expected amd64 or arm64)" >&2; exit 1;;
esac

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
COMMIT=${COMMIT:-$(git -C "$PROJECT_DIR" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)}
BUILD_DATE=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
case "$COMMIT" in unknown) ;; ''|*[!0-9a-f]*) echo "invalid build commit" >&2; exit 1;; esac
case "$BUILD_DATE" in ''|*[!0-9TZ:+.-]*) echo "invalid build date" >&2; exit 1;; esac
FULL_VERSION=$(tr -d '\n' < "$PROJECT_DIR/VERSION")
case "$FULL_VERSION" in
  [0-9]*-alpha) ;;
  *) echo "VERSION must use the form X.Y.Z-alpha" >&2; exit 1 ;;
esac
RPM_VERSION=${FULL_VERSION%-alpha}
case "$RPM_VERSION" in
  ''|*[!0-9.]*) echo "RPM version must contain only digits and dots" >&2; exit 1 ;;
esac
OLD_IFS=$IFS
IFS=.
set -- $RPM_VERSION
IFS=$OLD_IFS
[ "$#" -eq 3 ] || { echo "RPM version must contain three numeric components" >&2; exit 1; }
for component in "$@"; do
  [ -n "$component" ] || { echo "RPM version component is empty" >&2; exit 1; }
done
TOPDIR=$(mktemp -d /tmp/noderampart-rpm.XXXXXX)
trap 'rm -rf -- "$TOPDIR"' EXIT HUP INT TERM
install -d "$TOPDIR/BUILD" "$TOPDIR/BUILDROOT" "$TOPDIR/RPMS" "$TOPDIR/SOURCES" "$TOPDIR/SPECS" "$TOPDIR/SRPMS"

SOURCE_NAME="noderampart-${RPM_VERSION}-alpha.tar.gz"
SOURCE_ROOT="$TOPDIR/NodeRampart-${RPM_VERSION}-alpha"
install -d "$SOURCE_ROOT"
python3 "$PROJECT_DIR/scripts/stage-source.py" "$PROJECT_DIR" "$SOURCE_ROOT"
# Resolve and verify only the staged module inputs. A checkout's existing
# vendor directory and other unlisted local files never become build inputs.
(cd "$SOURCE_ROOT" && go mod verify && go mod vendor -o "$SOURCE_ROOT/vendor")
tar -C "$TOPDIR" -czf "$TOPDIR/SOURCES/$SOURCE_NAME" "NodeRampart-${RPM_VERSION}-alpha"
{
  printf '%%global noderampart_commit %s\n' "$COMMIT"
  printf '%%global noderampart_build_date %s\n' "$BUILD_DATE"
  sed "s/^Version:[[:space:]].*/Version:        $RPM_VERSION/" "$PROJECT_DIR/packaging/rpm/noderampart.spec"
} > "$TOPDIR/SPECS/noderampart.spec"

rpmbuild --target "$RPM_TARGET" --define "_topdir $TOPDIR" -ba "$TOPDIR/SPECS/noderampart.spec"
install -d "$PROJECT_DIR/dist/rpm"
find "$TOPDIR/RPMS" "$TOPDIR/SRPMS" -type f -name '*.rpm' -exec install -m 0644 {} "$PROJECT_DIR/dist/rpm/" \;
echo "RPM artifacts written to dist/rpm/"
