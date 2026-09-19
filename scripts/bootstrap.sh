#!/bin/sh
# SPDX-License-Identifier: MIT
# This installer downloads packages only. NodeRampart never self-updates code.
set -eu
umask 077

bootstrap_main() {
  PATH=/usr/sbin:/usr/bin:/sbin:/bin
  export PATH
  LC_ALL=C
  export LC_ALL
  release=v0.4.0-alpha.2
  setup=true
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --version) [ "$#" -ge 2 ] || bootstrap_die '--version requires v0.4.0-alpha.2'; release=$2; shift 2;;
      --no-setup|--non-interactive) setup=false; shift;;
      --help) echo 'usage: bootstrap.sh [--version v0.4.0-alpha.2] [--no-setup|--non-interactive]'; return;;
      *) bootstrap_die 'unknown installer option';;
    esac
  done
  [ "$release" = v0.4.0-alpha.2 ] || bootstrap_die 'this installer supports only v0.4.0-alpha.2; use the installer from the requested release'
  [ "$(id -u)" -eq 0 ] || bootstrap_die 'root is required: run the downloaded installer with sudo sh, or use curl ... | sudo sh -s -- (never sudo -S)'
  [ "$(uname -s)" = Linux ] || bootstrap_die 'Linux is required'
  if [ "$setup" = true ]; then
    ( : <> /dev/tty ) 2>/dev/null || bootstrap_die 'interactive setup needs a terminal; use --no-setup or --non-interactive, then run sudo noderampart setup in a terminal'
  fi
  [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1 || bootstrap_die 'a running systemd host is required'
  [ -r /etc/os-release ] || bootstrap_die 'cannot identify the distribution'
  distribution=$(awk -F= '$1 == "ID" {gsub(/"/, "", $2); print $2}' /etc/os-release)
  os_version=$(awk -F= '$1 == "VERSION_ID" {gsub(/"/, "", $2); print $2}' /etc/os-release)
  case "$distribution:$os_version" in
    debian:12|debian:13) kind=deb; command -v apt-get >/dev/null 2>&1 || bootstrap_die 'apt-get is required';;
    fedora:43|fedora:44) kind=rpm; command -v dnf >/dev/null 2>&1 || bootstrap_die 'dnf is required';;
    *) bootstrap_die 'supported hosts are Debian 12/13 and Fedora 43/44';;
  esac
  case "$(uname -m)" in
    x86_64) arch=amd64; rpm_arch=x86_64;;
    aarch64|arm64) arch=arm64; rpm_arch=aarch64;;
    *) bootstrap_die 'supported architectures are amd64 and arm64';;
  esac
  # Refuse ownership conflicts before installing dependencies or replacing files.
  for path in /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor /usr/local/libexec/noderampart/manage-remove; do
    [ ! -e "$path" ] && [ ! -L "$path" ] || bootstrap_die 'a source installation is present; use the documented source-to-package transition first'
  done
  if [ "$kind" = deb ]; then
    [ "$(dpkg --print-architecture)" = "$arch" ] || bootstrap_die 'Debian userland and kernel architectures disagree'
    asset="noderampart_0.4.0-alpha.2_${arch}.deb"
    installed=$(dpkg-query -W -f='${Version}' noderampart 2>/dev/null || true)
    if [ -n "$installed" ]; then
      dpkg --compare-versions "$installed" le '0.4.0~alpha.2' || bootstrap_die 'refusing an automatic package downgrade'
    fi
  else
    asset="noderampart-0.4.0-0.alpha.3.fc${os_version}.${rpm_arch}.rpm"
    installed=$(rpm -q --qf '%{VERSION}-%{RELEASE}\n' noderampart 2>/dev/null) || installed=
    if [ -n "$installed" ]; then
      printf '%s\n' "$installed" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+-0\.alpha\.[0-9]+\.fc(43|44)$' || bootstrap_die 'unrecognized installed RPM version; use the package manager explicitly'
      newest=$(printf '%s\n%s\n' "$installed" "0.4.0-0.alpha.3.fc${os_version}" | sort -V | tail -n 1)
      [ "$newest" = "0.4.0-0.alpha.3.fc${os_version}" ] || bootstrap_die 'refusing an automatic package downgrade'
    fi
  fi
  # mktemp's private mode cannot protect a directory name in an untrusted
  # shared parent. Accept only the real root-owned /tmp and require sticky
  # semantics whenever another user can write to that parent.
  [ -d /tmp ] && [ ! -L /tmp ] || bootstrap_die '/tmp must be a real directory, not a symlink'
  tmp_owner=$(stat -c '%u' /tmp)
  tmp_mode=$(stat -c '%a' /tmp)
  [ "$tmp_owner" = 0 ] || bootstrap_die '/tmp must be owned by root'
  [ "$((0$tmp_mode & 0022))" -eq 0 ] || [ "$((0$tmp_mode & 01000))" -ne 0 ] || bootstrap_die 'a shared writable /tmp must have its sticky bit set'
  if ! command -v curl >/dev/null 2>&1 || ! command -v prlimit >/dev/null 2>&1 || { [ ! -s /etc/ssl/certs/ca-certificates.crt ] && [ ! -s /etc/pki/tls/certs/ca-bundle.crt ]; }; then
    if [ "$kind" = deb ]; then
      apt-get update
      apt-get install -y --no-install-recommends ca-certificates curl util-linux
    else
      dnf install -y ca-certificates curl util-linux
    fi
  fi
  for command in curl prlimit sha256sum awk wc mktemp stat; do
    command -v "$command" >/dev/null 2>&1 || bootstrap_die "missing installation dependency: $command"
  done
  bootstrap_tmp=$(mktemp -d /tmp/noderampart-bootstrap.XXXXXXXX)
  trap 'rm -rf -- "$bootstrap_tmp"' EXIT HUP INT TERM
  base="https://github.com/littlesho/NodeRampart/releases/download/$release"
  echo "Downloading NodeRampart $release for $distribution $os_version ($arch)."
  bootstrap_download "$base/SHA256SUMS" "$bootstrap_tmp/SHA256SUMS" 65536
  # Fixed names only, canonical lowercase SHA256, exactly one target entry.
  digest=$(awk -v target="$asset" '
    NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-f]/ || $2 ~ /[^A-Za-z0-9_.~+-]/ { bad=1; next }
    $2 == target {count++; hash=$1}
    END {if (bad || count != 1) exit 1; print hash}
  ' "$bootstrap_tmp/SHA256SUMS") || bootstrap_die 'release checksum manifest is invalid or does not contain exactly one matching package'
  bootstrap_download "$base/$asset" "$bootstrap_tmp/$asset" 134217728
  actual=$(sha256sum "$bootstrap_tmp/$asset")
  [ "${actual%% *}" = "$digest" ] || bootstrap_die 'package checksum mismatch; NodeRampart was not changed'
  if [ "$kind" = deb ]; then
    [ "$(dpkg-deb -f "$bootstrap_tmp/$asset" Package)" = noderampart ] &&
      [ "$(dpkg-deb -f "$bootstrap_tmp/$asset" Version)" = '0.4.0~alpha.2' ] &&
      [ "$(dpkg-deb -f "$bootstrap_tmp/$asset" Architecture)" = "$arch" ] || bootstrap_die 'Debian package identity does not match the requested release'
    apt-get update
    apt-get install -y --no-install-recommends -o Dpkg::Options::=--force-confold "$bootstrap_tmp/$asset"
  else
    [ "$(rpm -qp --qf '%{NAME}:%{VERSION}:%{RELEASE}:%{ARCH}' "$bootstrap_tmp/$asset")" = "noderampart:0.4.0:0.alpha.3.fc${os_version}:$rpm_arch" ] || bootstrap_die 'RPM package identity does not match the requested release'
    # Keep the administrator's repository and local-package signature policy.
    # A host requiring RPM signatures must use a properly signed release asset.
    dnf install -y "$bootstrap_tmp/$asset"
  fi
  [ -f /usr/bin/noderampart ] && [ ! -L /usr/bin/noderampart ] || bootstrap_die 'package installation did not provide the expected CLI'
  echo 'Package installed. Existing configuration and service choices were preserved.'
  if [ "$setup" = true ]; then
    /usr/bin/noderampart setup < /dev/tty > /dev/tty 2>&1
  else
    echo 'Setup was skipped. Run: sudo noderampart setup'
    echo 'Fresh Debian packages may already be observing with safe defaults; Fedora follows system presets.'
  fi
}

bootstrap_die() { echo "NodeRampart installer: $*" >&2; exit 1; }

bootstrap_download() {
  download_url=$1
  download_output=$2
  download_limit=$3
  redirects=0
  while :; do
    case "$download_url" in
      https://github.com/littlesho/NodeRampart/releases/download/*|https://release-assets.githubusercontent.com/*|https://objects.githubusercontent.com/*) ;;
      *) bootstrap_die 'refusing a download redirect outside official GitHub asset hosts';;
    esac
    # Older curl versions cannot enforce --max-filesize on an unknown-length
    # body. A child-only kernel file-size limit also bounds that case, without
    # relying on Content-Length or allowing core dumps. Ignore implicit curlrc.
    code=$(prlimit --fsize="$download_limit:$download_limit" --core=0:0 -- curl --disable --globoff --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 180 --retry 2 --retry-delay 1 \
      --max-filesize "$download_limit" --silent --show-error --output "$download_output" \
      --dump-header "$bootstrap_tmp/headers" --write-out '%{http_code}' "$download_url") || bootstrap_die 'download failed; check connectivity or HTTPS proxy settings and retry'
    case "$code" in
      200)
        [ "$(wc -c < "$download_output")" -le "$download_limit" ] || bootstrap_die 'release download exceeded its size limit'
        return;;
      301|302|303|307|308)
        redirects=$((redirects + 1))
        [ "$redirects" -le 4 ] || bootstrap_die 'too many release download redirects'
        download_url=$(awk 'tolower($1) == "location:" {sub(/\r$/, ""); count++; print substr($0, index($0, ":")+2)} END {if (count != 1) exit 1}' "$bootstrap_tmp/headers") || bootstrap_die 'invalid release redirect'
        [ "${#download_url}" -le 4096 ] || bootstrap_die 'release redirect is too long'
        printf '%s' "$download_url" | LC_ALL=C grep -q '[[:cntrl:]]' && bootstrap_die 'invalid control character in release redirect'
        ;;
      404) bootstrap_die 'this public release or architecture asset is not available yet; repository publication and matching release assets are required';;
      *) bootstrap_die "release download returned HTTP $code; NodeRampart was not changed";;
    esac
  done
}

# Keep the whole implementation parsed before touching package-managed files.
bootstrap_main "$@"
