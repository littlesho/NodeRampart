#!/bin/sh
# SPDX-License-Identifier: MIT

set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "uninstall.sh must be run as root" >&2
  exit 1
fi

PURGE=false
if [ "$#" -eq 1 ] && [ "$1" = "--purge" ]; then
  PURGE=true
elif [ "$#" -ne 0 ]; then
  echo "usage: uninstall.sh [--purge]" >&2
  exit 1
fi

for path in /usr/bin/noderampart /usr/bin/noderampartd /usr/bin/noderampart-sensor /usr/lib/systemd/system/noderampartd.service /lib/systemd/system/noderampartd.service; do
  if [ -e "$path" ] || [ -L "$path" ]; then
    echo "native package files are present; use the package manager for removal" >&2
    exit 1
  fi
done

for path in /usr/local/share/doc/noderampart /usr/local/share/doc/noderampart/third-party /usr/local/libexec /usr/local/libexec/noderampart /usr/local/libexec/noderampart/manage-remove; do
  if [ -L "$path" ]; then
    echo "refusing symlinked documentation path: $path" >&2
    exit 1
  fi
done

if [ "$PURGE" = true ]; then
  command -v mountpoint >/dev/null 2>&1 || { echo "mountpoint is required for safe purge" >&2; exit 1; }
  command -v awk >/dev/null 2>&1 || { echo "awk is required for safe purge" >&2; exit 1; }
  # Validate every target before stopping services or deleting any files.
  for path in /etc/noderampart /var/lib/noderampart /var/cache/noderampart; do
    if [ -L "$path" ]; then
      echo "refusing to purge symlink: $path" >&2
      exit 1
    fi
    if mountpoint -q "$path"; then
      echo "refusing to purge mount point: $path" >&2
      exit 1
    fi
    # --one-file-system does not protect same-device bind mounts below a target.
    mount_state=$(awk -v target="$path" '
      NF < 10 { invalid = 1; next }
      $1 !~ /^[0-9]+$/ || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+:[0-9]+$/ ||
        $4 !~ /^\// || $5 !~ /^\// || $(NF - 3) != "-" { invalid = 1 }
      $5 == target || index($5, target "/") == 1 { mounted = 1 }
      END {
        if (NR == 0 || invalid) print "invalid"
        else print mounted ? "mounted" : "clear"
      }
    ' /proc/self/mountinfo) || { echo "cannot inspect mounts for safe purge" >&2; exit 1; }
    case "$mount_state" in
      clear) ;;
      mounted) echo "refusing to purge target containing a mount: $path" >&2; exit 1;;
      *) echo "cannot validate mount information for safe purge" >&2; exit 1;;
    esac
  done
fi

for path in /etc/systemd/system /etc/systemd/system/noderampartd.service.d; do
  [ ! -L "$path" ] || { echo "refusing symlinked managed unit directory: $path" >&2; exit 1; }
done
for unit in noderampart-geoip-update.timer noderampart-geoip-update.service noderampart-sensor.service noderampartd.service; do
  load_state=$(systemctl show --property=LoadState --value "$unit")
  if [ "$load_state" != not-found ]; then
    systemctl disable --now "$unit"
  fi
  active_state=$(systemctl show --property=ActiveState --value "$unit")
  case "$active_state" in
    inactive|failed) ;;
    *) echo "refusing removal while $unit is $active_state" >&2; exit 1;;
  esac
done
rm -f -- /etc/systemd/system/noderampart-geoip-update.service /etc/systemd/system/noderampart-geoip-update.timer /etc/systemd/system/noderampartd.service.d/90-noderampart-managed.conf
rm -f -- /etc/systemd/system/noderampart-sensor.service /etc/systemd/system/noderampartd.service
rm -f -- /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor
rm -f -- /usr/local/libexec/noderampart/manage-remove
rmdir /usr/local/libexec/noderampart 2>/dev/null || true
rm -f -- /usr/local/share/doc/noderampart/LICENSE /usr/local/share/doc/noderampart/README.md /usr/local/share/doc/noderampart/THIRD_PARTY_NOTICES.md /usr/local/share/doc/noderampart/source-install.manifest
for notice in github.com_dustin_go-humanize_LICENSE github.com_gdamore_encoding_LICENSE github.com_gdamore_tcell_v2_LICENSE github.com_google_uuid_LICENSE github.com_lucasb-eyer_go-colorful_LICENSE github.com_mattn_go-runewidth_LICENSE github.com_oschwald_maxminddb-golang_LICENSE github.com_remyoudompheng_bigfft_LICENSE github.com_rivo_tview_LICENSE github.com_rivo_uniseg_LICENSE golang.org_x_sys_LICENSE golang.org_x_term_LICENSE golang.org_x_term_PATENTS golang.org_x_text_LICENSE golang.org_x_text_PATENTS modernc.org_libc_LICENSE modernc.org_mathutil_LICENSE modernc.org_memory_LICENSE modernc.org_sqlite_LICENSE; do
  rm -f -- "/usr/local/share/doc/noderampart/third-party/$notice"
done
rmdir /usr/local/share/doc/noderampart/third-party /usr/local/share/doc/noderampart 2>/dev/null || true
systemctl daemon-reload

if [ "$PURGE" = true ]; then
  for path in /etc/noderampart /var/lib/noderampart /var/cache/noderampart; do
    [ ! -e "$path" ] || rm -rf --one-file-system -- "$path"
  done
  if id noderampart-sensor >/dev/null 2>&1; then userdel noderampart-sensor; fi
  if id noderampart >/dev/null 2>&1; then userdel noderampart; fi
  if getent group noderampart >/dev/null 2>&1; then groupdel noderampart; fi
  echo "NodeRampart binaries, configuration, state, and service accounts removed."
else
  echo "NodeRampart removed; /etc/noderampart and /var/lib/noderampart were preserved."
  echo "Run uninstall.sh --purge only after backing up any required data."
fi
