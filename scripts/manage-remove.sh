#!/bin/sh
# SPDX-License-Identifier: MIT
set -eu
umask 077

# All functions are parsed before main runs: the package manager may unlink this
# script, its interpreter can still complete the already loaded removal routine.
remove_die() { echo "NodeRampart removal: $*" >&2; exit 1; }

remove_trusted_dir() {
  [ ! -L "$1" ] || remove_die "refusing symlinked directory: $1"
  if [ -e "$1" ]; then
    [ -d "$1" ] || remove_die "refusing non-directory: $1"
    directory_owner=$(stat -c '%u' "$1")
    directory_mode=$(stat -c '%a' "$1")
    [ "$directory_owner" = 0 ] && [ "$((0$directory_mode & 0022))" -eq 0 ] || remove_die "directory must be root-owned and not writable by other users: $1"
  fi
}

remove_regular() {
  [ ! -L "$1" ] || remove_die "refusing symlinked managed file: $1"
  if [ -e "$1" ]; then
    [ -f "$1" ] && [ "$(stat -c '%h:%u' "$1")" = '1:0' ] || remove_die "refusing non-regular or untrusted managed file: $1"
    file_mode=$(stat -c '%a' "$1")
    [ "$((0$file_mode & 0022))" -eq 0 ] || remove_die "managed file is writable by another user: $1"
  fi
}

remove_validate_purge() {
  for path in /etc/noderampart /var/lib/noderampart /var/cache/noderampart; do
    [ ! -L "$path" ] || remove_die "refusing to purge symlink: $path"
    [ ! -e "$path" ] || [ -d "$path" ] || remove_die "refusing non-directory purge target: $path"
    if mountpoint -q "$path"; then remove_die "refusing to purge mount point: $path"; fi
    mount_state=$(awk -v target="$path" '
      NF < 10 { invalid = 1; next }
      $1 !~ /^[0-9]+$/ || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+:[0-9]+$/ ||
        $4 !~ /^\// || $5 !~ /^\// || $(NF - 3) != "-" { invalid = 1 }
      $5 == target || index($5, target "/") == 1 { mounted = 1 }
      END {
        if (NR == 0 || invalid) print "invalid"
        else print mounted ? "mounted" : "clear"
      }
    ' /proc/self/mountinfo) || remove_die 'cannot inspect mount information'
    [ "$mount_state" = clear ] || remove_die "unsafe or unreadable mount information for: $path"
  done
}

remove_stop() {
  load_state=$(systemctl show --property=LoadState --value "$1")
  if [ "$load_state" != not-found ]; then
    systemctl disable --now "$1"
  fi
  state=$(systemctl show --property=ActiveState --value "$1")
  case "$state" in inactive|failed) ;; *) remove_die "refusing removal while $1 is $state";; esac
}

remove_halt() {
  load_state=$(systemctl show --property=LoadState --value "$1")
  if [ "$load_state" != not-found ]; then systemctl stop "$1"; fi
  state=$(systemctl show --property=ActiveState --value "$1")
  case "$state" in inactive|failed) ;; *) remove_die "refusing removal while $1 is $state";; esac
}

remove_debian_package() {
  # A failed unpack/configure still leaves a package that dpkg can remove.
  # Validate the complete Status value rather than treating only "installed"
  # as ownership, or accepting arbitrary output from a broken database.
  deb_status=$(dpkg-query -W -f='${Status}' noderampart 2>/dev/null) || return 1
  case "$deb_status" in
    'install '*|'hold '*|'deinstall '*|'purge '*) ;;
    *) remove_die 'unrecognized Debian package selection state';;
  esac
  deb_status=${deb_status#* }
  case "$deb_status" in
    'ok '*|'reinstreq '*) ;;
    *) remove_die 'unrecognized Debian package error state';;
  esac
  deb_status=${deb_status#* }
  case "$deb_status" in
    config-files|half-installed|unpacked|half-configured|triggers-awaited|triggers-pending|installed) return 0;;
    not-installed) return 1;;
    *) remove_die 'unrecognized Debian package installation state';;
  esac
}

remove_validate_source() {
  manifest=/usr/local/share/doc/noderampart/source-install.manifest
  remove_regular "$manifest"
  [ -f "$manifest" ] || remove_die 'source ownership manifest is missing; use the documented legacy transition'
  seen='|'
  count=0
  while read -r digest path extra; do
    [ -z "$extra" ] && [ "${#digest}" -eq 64 ] || remove_die 'invalid source ownership manifest'
    case "$digest" in *[!0-9a-f]*) remove_die 'invalid source ownership digest';; esac
    case "$path" in
      /usr/local/bin/noderampart|/usr/local/bin/noderampartd|/usr/local/bin/noderampart-sensor|/etc/systemd/system/noderampartd.service|/etc/systemd/system/noderampart-sensor.service|/usr/local/libexec/noderampart/manage-remove) ;;
      *) remove_die 'unexpected source ownership target';;
    esac
    case "$seen" in *"|$path|"*) remove_die 'duplicate source ownership target';; esac
    seen="$seen$path|"
    remove_regular "$path"
    [ -f "$path" ] || remove_die 'source installation is incomplete'
    actual=$(sha256sum "$path")
    [ "${actual%% *}" = "$digest" ] || remove_die 'source files were modified; reconcile ownership before removal'
    count=$((count + 1))
  done < "$manifest"
  [ "$count" -eq 5 ] || [ "$count" -eq 6 ] || remove_die 'source ownership manifest is incomplete'
  # All original five targets are mandatory even if a helper entry was present.
  for path in /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor /etc/systemd/system/noderampartd.service /etc/systemd/system/noderampart-sensor.service; do
    case "$seen" in *"|$path|"*) ;; *) remove_die 'source ownership manifest is incomplete';; esac
  done
  if [ -e /usr/local/libexec/noderampart/manage-remove ] || [ -L /usr/local/libexec/noderampart/manage-remove ]; then
    case "$seen" in *'|/usr/local/libexec/noderampart/manage-remove|'*) ;; *) remove_die 'the installed management helper has no recorded source ownership';; esac
  fi
}

remove_main() {
  PATH=/usr/sbin:/usr/bin:/sbin:/bin
  export PATH
  purge=false
  case "$#" in
    0) ;;
    1) [ "$1" = --purge ] || remove_die 'usage: manage-remove [--purge]'; purge=true;;
    *) remove_die 'usage: manage-remove [--purge]';;
  esac
  [ "$(id -u)" -eq 0 ] || remove_die 'root is required'
  for command in stat flock systemctl mountpoint awk sha256sum; do
    command -v "$command" >/dev/null 2>&1 || remove_die "missing removal dependency: $command"
  done
  for path in /run /etc /etc/tmpfiles.d /etc/systemd /etc/systemd/system /etc/systemd/system/noderampartd.service.d /usr /usr/local /usr/local/bin /usr/local/libexec /usr/local/libexec/noderampart /usr/local/share /usr/local/share/doc /usr/local/share/doc/noderampart /usr/local/share/doc/noderampart/third-party /var /var/lib /var/cache; do
    remove_trusted_dir "$path"
  done
  lock=/run/noderampart-management.lock
  remove_regular "$lock"
  if [ ! -e "$lock" ]; then
    ( set -C; : > "$lock" ) || remove_die 'cannot create management lock'
  fi
  [ "$(stat -c '%a' "$lock")" = 600 ] || remove_die 'management lock must have mode 0600'
  exec 9<> "$lock"
  flock -x -w 30 9 || remove_die 'another management operation is in progress'
  if [ "$purge" = true ]; then remove_validate_purge; fi
  for path in /etc/systemd/system/noderampart-geoip-update.service /etc/systemd/system/noderampart-geoip-update.timer /etc/systemd/system/noderampartd.service.d/90-noderampart-managed.conf /etc/tmpfiles.d/noderampart-management.conf; do
    if [ -L "$path" ] && [ "$(readlink "$path")" = /dev/null ]; then
      case "$path" in *.service|*.timer) continue;; esac
    fi
    remove_regular "$path"
  done
  deb=false
  rpm=false
  if command -v dpkg-query >/dev/null 2>&1 && remove_debian_package; then deb=true; fi
  if command -v rpm >/dev/null 2>&1 && rpm -q noderampart >/dev/null 2>&1; then rpm=true; fi
  [ "$deb:$rpm" != true:true ] || remove_die 'both package databases claim NodeRampart'
  if [ "$deb" = true ]; then
    for path in /usr/bin/noderampart /usr/bin/noderampartd /usr/bin/noderampart-sensor; do
      if [ -e "$path" ] || [ -L "$path" ]; then
        deb_owner=$(dpkg-query -S "$path" 2>/dev/null) || remove_die 'unowned native binary; refusing removal'
        case "$deb_owner" in
          "noderampart: $path"|"noderampart:amd64: $path"|"noderampart:arm64: $path") ;;
          *) remove_die 'native binary is not exclusively owned by the NodeRampart Debian package';;
        esac
      fi
    done
  fi
  source=false
  for path in /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor; do
    if [ -e "$path" ] || [ -L "$path" ]; then source=true; fi
  done
  if [ "$source" = true ]; then
    [ "$deb:$rpm" = false:false ] || remove_die 'mixed source and package installation requires reconciliation'
    for path in /usr/bin/noderampart /usr/bin/noderampartd /usr/bin/noderampart-sensor /usr/lib/systemd/system/noderampartd.service /lib/systemd/system/noderampartd.service; do
      [ ! -e "$path" ] && [ ! -L "$path" ] || remove_die 'native files conflict with source ownership'
    done
    remove_validate_source
  elif [ "$deb:$rpm" = false:false ]; then
    for path in /usr/bin/noderampart /usr/bin/noderampartd /usr/bin/noderampart-sensor; do
      [ ! -e "$path" ] && [ ! -L "$path" ] || remove_die 'unowned native binary; refusing removal'
    done
    [ "$purge" = true ] || remove_die 'no installed NodeRampart package or owned source installation was found'
  fi
  remove_stop noderampart-geoip-update.timer
  remove_stop noderampart-geoip-update.service
  remove_halt noderampart-sensor.service
  remove_halt noderampartd.service
  # Remove only our boot-time rule. Keep the runtime lock inode open and linked
  # until every concurrent user has released it; never unlink it during removal.
  rm -f -- /etc/systemd/system/noderampart-geoip-update.service /etc/systemd/system/noderampart-geoip-update.timer /etc/systemd/system/noderampartd.service.d/90-noderampart-managed.conf /etc/tmpfiles.d/noderampart-management.conf
  systemctl daemon-reload
  if [ "$deb" = true ]; then
    operation=remove
    [ "$purge" = false ] || operation=purge
    apt-get "$operation" -y --no-auto-remove noderampart
  elif [ "$rpm" = true ]; then
    dnf remove -y --setopt=clean_requirements_on_remove=False noderampart
  elif [ "$source" = true ]; then
    remove_stop noderampart-sensor.service
    remove_stop noderampartd.service
    rm -f -- /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor /etc/systemd/system/noderampartd.service /etc/systemd/system/noderampart-sensor.service /usr/local/libexec/noderampart/manage-remove /usr/local/share/doc/noderampart/source-install.manifest
    rm -f -- /usr/local/share/doc/noderampart/LICENSE /usr/local/share/doc/noderampart/README.md /usr/local/share/doc/noderampart/THIRD_PARTY_NOTICES.md
    for notice in github.com_dustin_go-humanize_LICENSE github.com_gdamore_encoding_LICENSE github.com_gdamore_tcell_v2_LICENSE github.com_google_uuid_LICENSE github.com_lucasb-eyer_go-colorful_LICENSE github.com_mattn_go-runewidth_LICENSE github.com_oschwald_maxminddb-golang_LICENSE github.com_remyoudompheng_bigfft_LICENSE github.com_rivo_tview_LICENSE github.com_rivo_uniseg_LICENSE golang.org_x_sys_LICENSE golang.org_x_term_LICENSE golang.org_x_term_PATENTS golang.org_x_text_LICENSE golang.org_x_text_PATENTS modernc.org_libc_LICENSE modernc.org_mathutil_LICENSE modernc.org_memory_LICENSE modernc.org_sqlite_LICENSE; do
      path="/usr/local/share/doc/noderampart/third-party/$notice"
      if [ -f "$path" ] && [ ! -L "$path" ]; then rm -f -- "$path"; fi
    done
    rmdir /usr/local/share/doc/noderampart/third-party /usr/local/share/doc/noderampart /usr/local/libexec/noderampart 2>/dev/null || true
    systemctl daemon-reload
  fi
  if [ "$purge" = true ]; then
    # Recheck after the package manager: never treat removal success as proof of
    # safe data paths or successful service termination.
    remove_validate_purge
    for unit in noderampart-sensor.service noderampartd.service; do
      remove_stop "$unit"
    done
    for path in /etc/noderampart /var/lib/noderampart /var/cache/noderampart; do
      [ ! -e "$path" ] || rm -rf --one-file-system -- "$path"
    done
    if id noderampart-sensor >/dev/null 2>&1; then userdel noderampart-sensor; fi
    if id noderampart >/dev/null 2>&1; then userdel noderampart; fi
    if getent group noderampart >/dev/null 2>&1; then groupdel noderampart; fi
    echo 'NodeRampart removed; fixed application configuration, credentials, state, cache and accounts were purged.'
  else
    echo 'NodeRampart removed; configuration, credentials, data and caches were preserved.'
    echo 'On RPM systems, modified configuration may be saved as config.json.rpmsave.'
  fi
}

remove_main "$@"
