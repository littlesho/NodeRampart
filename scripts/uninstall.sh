#!/bin/sh
# SPDX-License-Identifier: MIT

set -eu

# Credential files entered by administrators are not automatically product-owned.
# Purge recognizes only the managed random-file namespace, never arbitrary names.
purge_native_credentials_guard() {
  [ -e /etc/noderampart ] || [ -L /etc/noderampart ] || return 0
  [ ! -L /etc/noderampart ] && [ -d /etc/noderampart ] || { echo 'Refusing purge of an unsafe configuration directory.' >&2; return 1; }
  for credential_entry in /etc/noderampart/* /etc/noderampart/.[!.]* /etc/noderampart/..?*; do
    [ -e "$credential_entry" ] || [ -L "$credential_entry" ] || continue
    case "${credential_entry##*/}" in
      config.json|config.json.rpmsave|config.json.rpmnew|telegram.token|webhook.token|heartbeat.token|maxmind.credentials.json|geoip-state.json|geoip-health.json|prices-aws.json|prices-azure.json|prices-gcp.json|prices-alicloud.json|.management-apply.json|geoip|prices|secrets) ;;
      *) echo 'Refusing purge of a file whose ownership is unknown; move manually supplied credentials outside the product directory first.' >&2; return 1;;
    esac
  done
  credential_directory=/etc/noderampart/secrets
  [ -e "$credential_directory" ] || [ -L "$credential_directory" ] || return 0
  [ ! -L "$credential_directory" ] && [ -d "$credential_directory" ] || { echo 'Refusing purge of an unsafe credentials directory.' >&2; return 1; }
  credential_uid=$(id -u noderampart) || return 1
  credential_gid=$(id -g noderampart) || return 1
  case "$credential_uid:$credential_gid" in *[!0-9:]*|:*|*:) echo 'Cannot verify credential service ownership.' >&2; return 1;; esac
  [ "$(stat -c '%u:%g' "$credential_directory")" = "0:$credential_gid" ] || { echo 'Refusing purge of a credentials directory whose ownership is unknown.' >&2; return 1; }
  credential_mode=$(stat -c '%a' "$credential_directory") || return 1
  [ "$((0$credential_mode & 0022))" -eq 0 ] || { echo 'Refusing purge of a shared writable credentials directory.' >&2; return 1; }
  credential_count=0
  for credential_entry in "$credential_directory"/* "$credential_directory"/.[!.]* "$credential_directory"/..?*; do
    [ -e "$credential_entry" ] || [ -L "$credential_entry" ] || continue
    credential_count=$((credential_count + 1))
    [ "$credential_count" -le 128 ] || { echo 'Credentials directory exceeds the managed file limit; inspect it before purging.' >&2; return 1; }
    credential_name=${credential_entry##*/}
    case "$credential_name" in
      telegram-*.secret|privacy-*.secret|feishu-*.secret|wecom-*.secret|discord-*.secret|slack-*.secret|teams-*.secret|google_chat-*.secret) ;;
      *) echo 'Refusing purge of an unowned credential file; move manually supplied credentials first.' >&2; return 1;;
    esac
    credential_generation=${credential_name#*-}
    credential_generation=${credential_generation%.secret}
    case "$credential_generation" in *[!A-Z2-7]*) echo 'Refusing purge of an unowned credential file.' >&2; return 1;; esac
    [ "${#credential_generation}" -ge 26 ] && [ "${#credential_generation}" -le 128 ] || { echo 'Refusing purge of an unowned credential file.' >&2; return 1; }
    [ ! -L "$credential_entry" ] && [ -f "$credential_entry" ] && [ "$(stat -c '%h:%u:%g:%a' "$credential_entry")" = "1:$credential_uid:$credential_gid:600" ] || { echo 'Refusing purge of a credential file whose ownership or permissions are unsafe.' >&2; return 1; }
    credential_bytes=$(stat -c '%s' "$credential_entry") || return 1
    [ "$credential_bytes" -le 8192 ] || { echo 'Refusing purge of a credential file outside managed size limits.' >&2; return 1; }
  done
}

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
  purge_native_credentials_guard || exit 1
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
  purge_native_credentials_guard || exit 1
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
