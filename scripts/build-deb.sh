#!/bin/sh
# SPDX-License-Identifier: MIT

set -eu
umask 022
command -v dpkg-deb >/dev/null 2>&1 || { echo "dpkg-deb is required" >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo "Go is required to verify binary metadata" >&2; exit 1; }

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
PROJECT_VERSION=$(tr -d '\n' < "$PROJECT_DIR/VERSION")
ARCH=${ARCH:-$(dpkg --print-architecture)}
case "$PROJECT_VERSION" in ""|.*|*.|*[!0-9A-Za-z._-]*) echo "invalid package version" >&2; exit 1;; esac
VERSION=$PROJECT_VERSION
case "$VERSION" in *-alpha.5) VERSION=${VERSION%-alpha.5}~alpha.5;; *-alpha.4) VERSION=${VERSION%-alpha.4}~alpha.4;; *-alpha.3) VERSION=${VERSION%-alpha.3}~alpha.3;; *-alpha.2) VERSION=${VERSION%-alpha.2}~alpha.2;; *-alpha.1) VERSION=${VERSION%-alpha.1}~alpha.1;; *-alpha) VERSION=${VERSION%-alpha}~alpha;; esac
case "$ARCH" in amd64|arm64) ;; *) echo "unsupported architecture: $ARCH" >&2; exit 1;; esac
GO_ARCH=$ARCH

for binary in noderampart noderampartd noderampart-sensor; do
  [ -f "$PROJECT_DIR/bin/$binary" ] && [ ! -L "$PROJECT_DIR/bin/$binary" ] || { echo "run make build first" >&2; exit 1; }
  METADATA=$(go version -m "$PROJECT_DIR/bin/$binary")
  printf '%s\n' "$METADATA" | grep -q 'GOOS=linux' || { echo "bin/$binary is not a Linux binary" >&2; exit 1; }
  printf '%s\n' "$METADATA" | grep -q "GOARCH=$GO_ARCH" || { echo "bin/$binary does not match Debian architecture $ARCH" >&2; exit 1; }
done

BUILD_ROOT=$(mktemp -d /tmp/noderampart-deb.XXXXXX)
trap 'rm -rf -- "$BUILD_ROOT"' EXIT HUP INT TERM
PACKAGE_ROOT="$BUILD_ROOT/noderampart"
install -d "$PACKAGE_ROOT/DEBIAN" "$PACKAGE_ROOT/usr/bin" "$PACKAGE_ROOT/usr/libexec/noderampart" "$PACKAGE_ROOT/etc/noderampart" "$PACKAGE_ROOT/lib/systemd/system" "$PACKAGE_ROOT/usr/share/doc/noderampart/third-party"

sed -e "s/@VERSION@/$VERSION/g" -e "s/@ARCH@/$ARCH/g" "$PROJECT_DIR/packaging/debian/control.template" > "$PACKAGE_ROOT/DEBIAN/control"
install -m 0755 "$PROJECT_DIR/packaging/debian/preinst" "$PACKAGE_ROOT/DEBIAN/preinst"
install -m 0755 "$PROJECT_DIR/packaging/debian/postinst" "$PACKAGE_ROOT/DEBIAN/postinst"
install -m 0755 "$PROJECT_DIR/packaging/debian/prerm" "$PACKAGE_ROOT/DEBIAN/prerm"
install -m 0755 "$PROJECT_DIR/packaging/debian/postrm" "$PACKAGE_ROOT/DEBIAN/postrm"
printf '%s\n' /etc/noderampart/config.json > "$PACKAGE_ROOT/DEBIAN/conffiles"
install -m 0755 "$PROJECT_DIR/bin/noderampart" "$PROJECT_DIR/bin/noderampartd" "$PROJECT_DIR/bin/noderampart-sensor" "$PACKAGE_ROOT/usr/bin/"
install -m 0755 "$PROJECT_DIR/scripts/manage-remove.sh" "$PACKAGE_ROOT/usr/libexec/noderampart/manage-remove"
install -m 0640 "$PROJECT_DIR/configs/noderampart.json" "$PACKAGE_ROOT/etc/noderampart/config.json"
install -m 0644 "$PROJECT_DIR/packaging/systemd/noderampartd.service" "$PROJECT_DIR/packaging/systemd/noderampart-sensor.service" "$PACKAGE_ROOT/lib/systemd/system/"
install -m 0644 "$PROJECT_DIR/LICENSE" "$PACKAGE_ROOT/usr/share/doc/noderampart/copyright"
install -m 0644 "$PROJECT_DIR/LICENSE" "$PROJECT_DIR/README.md" "$PACKAGE_ROOT/usr/share/doc/noderampart/"
install -m 0644 "$PROJECT_DIR/THIRD_PARTY_NOTICES.md" "$PACKAGE_ROOT/usr/share/doc/noderampart/"
install -m 0644 "$PROJECT_DIR"/third_party/licenses/* "$PACKAGE_ROOT/usr/share/doc/noderampart/third-party/"

install -d "$PROJECT_DIR/dist"
dpkg-deb --root-owner-group --build "$PACKAGE_ROOT" "$PROJECT_DIR/dist/noderampart_${PROJECT_VERSION}_${ARCH}.deb"
