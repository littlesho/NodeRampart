#!/bin/sh
# SPDX-License-Identifier: MIT

set -eu
umask 077

[ "$(id -u)" -eq 0 ] || { echo "source-to-package.sh must be run as root" >&2; exit 1; }
LEGACY_CHECKOUT=
case "$#" in
  1) [ "$1" = --prepare ] || { echo "usage: source-to-package.sh --prepare [--legacy-checkout DIRECTORY]" >&2; exit 1; };;
  3) [ "$1" = --prepare ] && [ "$2" = --legacy-checkout ] || { echo "usage: source-to-package.sh --prepare [--legacy-checkout DIRECTORY]" >&2; exit 1; }
     LEGACY_CHECKOUT=$3;;
  *) echo "usage: source-to-package.sh --prepare [--legacy-checkout DIRECTORY]" >&2; exit 1;;
esac
command -v sha256sum >/dev/null 2>&1 || { echo "sha256sum is required" >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "systemd is required" >&2; exit 1; }

MANIFEST=/usr/local/share/doc/noderampart/source-install.manifest
for path in /usr/local/share/doc/noderampart "$MANIFEST"; do
  [ ! -L "$path" ] || { echo "refusing symlinked source ownership metadata" >&2; exit 1; }
done
TEMP_DIR=$(mktemp -d /tmp/noderampart-transition.XXXXXX)
trap 'rm -rf -- "$TEMP_DIR"' EXIT HUP INT TERM

if [ -n "$LEGACY_CHECKOUT" ]; then
  # Older source installations had no manifest. Require the exact original
  # build and unit templates; never infer ownership from executable names.
  [ -d "$LEGACY_CHECKOUT" ] && [ ! -L "$LEGACY_CHECKOUT" ] || { echo "legacy checkout must be a regular directory" >&2; exit 1; }
  for binary in noderampart noderampartd noderampart-sensor; do
    path="$LEGACY_CHECKOUT/bin/$binary"
    [ -f "$path" ] && [ ! -L "$path" ] || { echo "exact original legacy binaries are required" >&2; exit 1; }
    digest=$(sha256sum "$path")
    printf '%s  %s\n' "${digest%% *}" "/usr/local/bin/$binary" >> "$TEMP_DIR/legacy.manifest"
  done
  for unit in noderampartd.service noderampart-sensor.service; do
    path="$LEGACY_CHECKOUT/packaging/systemd/$unit"
    [ -f "$path" ] && [ ! -L "$path" ] || { echo "exact original legacy unit templates are required" >&2; exit 1; }
    sed 's#/usr/bin/noderampart#/usr/local/bin/noderampart#g' "$path" > "$TEMP_DIR/$unit"
    digest=$(sha256sum "$TEMP_DIR/$unit")
    printf '%s  %s\n' "${digest%% *}" "/etc/systemd/system/$unit" >> "$TEMP_DIR/legacy.manifest"
  done
  if [ -e /usr/local/libexec/noderampart/manage-remove ] || [ -L /usr/local/libexec/noderampart/manage-remove ]; then
    path="$LEGACY_CHECKOUT/scripts/manage-remove.sh"
    [ -f "$path" ] && [ ! -L "$path" ] || { echo "exact original management helper is required" >&2; exit 1; }
    digest=$(sha256sum "$path")
    printf '%s  %s\n' "${digest%% *}" /usr/local/libexec/noderampart/manage-remove >> "$TEMP_DIR/legacy.manifest"
  fi
  MANIFEST_TO_CHECK="$TEMP_DIR/legacy.manifest"
else
  [ -f "$MANIFEST" ] || { echo "source ownership manifest missing; provide --legacy-checkout with the exact original build" >&2; exit 1; }
  MANIFEST_TO_CHECK=$MANIFEST
fi

# Validate the entire fixed allowlist before stopping anything. Reject modified
# or substituted files and duplicate/extra manifest entries. Custom drop-ins,
# configuration, state and package-owned files are never deletion targets.
seen='|'
count=0
while read -r digest path extra; do
  [ -z "$extra" ] && [ "${#digest}" -eq 64 ] || { echo "invalid source ownership manifest" >&2; exit 1; }
  case "$digest" in *[!0-9a-f]*) echo "invalid source ownership digest" >&2; exit 1;; esac
  case "$path" in
    /usr/local/bin/noderampart|/usr/local/bin/noderampartd|/usr/local/bin/noderampart-sensor|/etc/systemd/system/noderampartd.service|/etc/systemd/system/noderampart-sensor.service|/usr/local/libexec/noderampart/manage-remove) ;;
    *) echo "unexpected source ownership target" >&2; exit 1;;
  esac
  case "$seen" in *"|$path|"*) echo "duplicate source ownership target" >&2; exit 1;; esac
  seen="$seen$path|"
  [ -f "$path" ] && [ ! -L "$path" ] || { echo "refusing non-regular source target" >&2; exit 1; }
  actual=$(sha256sum "$path")
  [ "${actual%% *}" = "$digest" ] || { echo "source files were modified; reconcile them before transition" >&2; exit 1; }
  count=$((count + 1))
done < "$MANIFEST_TO_CHECK"
[ "$count" -eq 5 ] || [ "$count" -eq 6 ] || { echo "incomplete source ownership manifest" >&2; exit 1; }
for path in /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor /etc/systemd/system/noderampartd.service /etc/systemd/system/noderampart-sensor.service; do
  case "$seen" in *"|$path|"*) ;; *) echo "incomplete source ownership manifest" >&2; exit 1;; esac
done

# Managed update units contain the source installation's executable path. Stop
# them before that executable is removed; setup recreates them for the package
# path when scheduled updates are applied again. Preserve saved preferences.
for path in /etc/systemd/system /etc/systemd/system/noderampartd.service.d; do
  [ ! -L "$path" ] || { echo "refusing symlinked managed unit directory: $path" >&2; exit 1; }
done
for unit in noderampart-geoip-update.timer noderampart-geoip-update.service; do
  if [ -e "/etc/systemd/system/$unit" ] || [ -L "/etc/systemd/system/$unit" ]; then
    systemctl disable --now "$unit"
    state=$(systemctl show --property=ActiveState --value "$unit")
    case "$state" in inactive|failed) ;; *) echo "refusing transition while $unit is $state" >&2; exit 1;; esac
    rm -f -- "/etc/systemd/system/$unit"
  fi
done
rm -f -- /etc/systemd/system/noderampartd.service.d/90-noderampart-managed.conf

for unit in noderampart-sensor.service noderampartd.service; do
  systemctl disable --now "$unit"
  state=$(systemctl show --property=ActiveState --value "$unit")
  case "$state" in inactive|failed) ;; *) echo "refusing transition while a source service remains active" >&2; exit 1;; esac
done
rm -f -- /etc/systemd/system/noderampart-sensor.service /etc/systemd/system/noderampartd.service
rm -f -- /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor
case "$seen" in
  *'|/usr/local/libexec/noderampart/manage-remove|'*)
    rm -f -- /usr/local/libexec/noderampart/manage-remove
    rmdir /usr/local/libexec/noderampart 2>/dev/null || true;;
esac
[ ! -f "$MANIFEST" ] || rm -f -- "$MANIFEST"
systemctl daemon-reload
echo "Verified source binaries, full units and any recorded management helper removed; configuration, state and drop-ins preserved."
echo "Install the reviewed DEB/RPM package next, then run noderampart doctor and noderampart status."
