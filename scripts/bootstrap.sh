#!/bin/sh
# SPDX-License-Identifier: MIT
# Only published Release installers/packages are executed. The daemon never self-updates.
set -eu
umask 077

bootstrap_main() {
  PATH=/usr/sbin:/usr/bin:/sbin:/bin
  export PATH
  # Machine parsing stays deterministic while setup retains the caller's
  # terminal encoding. An explicit legacy encoding reaches the TUI refusal.
  bootstrap_ui_locale=${LC_ALL:-${LC_CTYPE:-${LANG:-C.UTF-8}}}
  case "$bootstrap_ui_locale" in C|POSIX) bootstrap_ui_locale=C.UTF-8;; esac
  LC_ALL=C
  export LC_ALL
  release=
  dispatch=false
  setup=true
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --version) [ "$#" -ge 2 ] && [ -n "$2" ] || bootstrap_die '--version requires a fixed release tag'; release=$2; shift 2;;
      --no-setup|--non-interactive) setup=false; shift;;
      --help)
        echo 'usage: bootstrap.sh [--version vX.Y.Z[-alpha[.N]|-beta[.N]|-rc[.N]]] [--no-setup|--non-interactive]'
        echo 'Without --version, select the highest published product version, including prereleases.'
        echo 'Release resolution/dispatch requires curl, util-linux (prlimit) and Python 3 (resolution only).'
        echo 'An explicit version is pinned; unavailable or invalid releases fail without fallback.'
        return;;
      *) bootstrap_die 'unknown installer option';;
    esac
  done
  if [ -n "$release" ]; then
    [ "${#release}" -le 128 ] && printf '%s\n' "$release" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)(\.(0|[1-9][0-9]*))?)?$' || bootstrap_die 'invalid product release tag'
  fi
  case "$release" in
    '') dispatch=true;;
    v0.4.0-alpha) deb_version=0.4.0~alpha; rpm_release=0.alpha.1;;
    v0.4.0-alpha.5) deb_version=0.4.0~alpha.5; rpm_release=0.alpha.6;;
    v0.4.0-alpha.6) deb_version=0.4.0~alpha.6; rpm_release=0.alpha.7;;
    v0.4.0-alpha.7) deb_version=0.4.0~alpha.7; rpm_release=0.alpha.8;;
    v0.4.0-alpha.8) deb_version=0.4.0~alpha.8; rpm_release=0.alpha.9;;
    v0.4.0-alpha.9) deb_version=0.4.0~alpha.9; rpm_release=0.alpha.10;;
    v0.4.0-alpha.10) deb_version=0.4.0~alpha.10; rpm_release=0.alpha.11;;
    v0.4.0-alpha.11) deb_version=0.4.0~alpha.11; rpm_release=0.alpha.12;;
    # Future releases own their package mapping; do not guess native versions.
    *) dispatch=true;;
  esac
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
  fi
  if [ "$dispatch" = false ] && [ "$kind" = deb ]; then
    asset="noderampart_${release#v}_${arch}.deb"
    installed=$(dpkg-query -W -f='${Version}' noderampart 2>/dev/null || true)
    if [ -n "$installed" ]; then
      dpkg --compare-versions "$installed" le "$deb_version" || bootstrap_die 'refusing an automatic package downgrade'
    fi
  elif [ "$dispatch" = false ]; then
    asset="noderampart-0.4.0-${rpm_release}.fc${os_version}.${rpm_arch}.rpm"
    installed=$(rpm -q --qf '%{VERSION}-%{RELEASE}\n' noderampart 2>/dev/null) || installed=
    if [ -n "$installed" ]; then
      printf '%s\n' "$installed" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+-0\.alpha\.[0-9]+\.fc(43|44)$' || bootstrap_die 'unrecognized installed RPM version; use the package manager explicitly'
      newest=$(printf '%s\n%s\n' "$installed" "0.4.0-${rpm_release}.fc${os_version}" | sort -V | tail -n 1)
      [ "$newest" = "0.4.0-${rpm_release}.fc${os_version}" ] || bootstrap_die 'refusing an automatic package downgrade'
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
  if [ "$dispatch" = true ]; then
    # A release script must handle its own explicit version. Never recursively
    # dispatch, including if a future release accidentally ships an old mapping.
    [ -z "${NODERAMPART_BOOTSTRAP_DISPATCHED:-}" ] || bootstrap_die 'release installer cannot handle its pinned version; recursive dispatch is refused'
    for command in curl prlimit sha256sum awk wc mktemp; do
      command -v "$command" >/dev/null 2>&1 || bootstrap_die "release dispatch requires $command; install curl and util-linux from your official repository first"
    done
    if [ -z "$release" ]; then
      command -v python3 >/dev/null 2>&1 || bootstrap_die 'latest release resolution requires Python 3; install python3 from your official repository first'
    fi
    bootstrap_tmp=$(mktemp -d /tmp/noderampart-bootstrap.XXXXXXXX)
    trap 'rm -rf -- "$bootstrap_tmp"' EXIT HUP INT TERM
    if [ -z "$release" ]; then
      release=$(bootstrap_latest_release) || bootstrap_die 'could not determine the latest published release; no fallback was attempted'
    fi
    echo "Selected published NodeRampart release: $release"
    base="https://github.com/littlesho/NodeRampart/releases/download/$release"
    bootstrap_download "$base/SHA256SUMS" "$bootstrap_tmp/SHA256SUMS" 65536
    digest=$(bootstrap_checksum bootstrap.sh) || bootstrap_die 'release checksum manifest is invalid or does not contain exactly one bootstrap.sh'
    bootstrap_download "$base/bootstrap.sh" "$bootstrap_tmp/bootstrap.sh" 1048576
    actual=$(sha256sum "$bootstrap_tmp/bootstrap.sh")
    [ "${actual%% *}" = "$digest" ] || bootstrap_die 'release installer checksum mismatch; NodeRampart was not changed'
    # Keep the caller's terminal/locale, explicit version and setup policy. The
    # published installer repeats native anti-downgrade checks before changes.
    if [ "$setup" = true ]; then
      NODERAMPART_BOOTSTRAP_DISPATCHED=1 LC_ALL="$bootstrap_ui_locale" /bin/sh "$bootstrap_tmp/bootstrap.sh" --version "$release"
    else
      NODERAMPART_BOOTSTRAP_DISPATCHED=1 LC_ALL="$bootstrap_ui_locale" /bin/sh "$bootstrap_tmp/bootstrap.sh" --version "$release" --no-setup
    fi
    return
  fi
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
  digest=$(bootstrap_checksum "$asset") || bootstrap_die 'release checksum manifest is invalid or does not contain exactly one matching package'
  bootstrap_download "$base/$asset" "$bootstrap_tmp/$asset" 134217728
  actual=$(sha256sum "$bootstrap_tmp/$asset")
  [ "${actual%% *}" = "$digest" ] || bootstrap_die 'package checksum mismatch; NodeRampart was not changed'
  if [ "$kind" = deb ]; then
    [ "$(dpkg-deb -f "$bootstrap_tmp/$asset" Package)" = noderampart ] &&
      [ "$(dpkg-deb -f "$bootstrap_tmp/$asset" Version)" = "$deb_version" ] &&
      [ "$(dpkg-deb -f "$bootstrap_tmp/$asset" Architecture)" = "$arch" ] || bootstrap_die 'Debian package identity does not match the requested release'
    apt-get update
    apt-get install -y --no-install-recommends -o Dpkg::Options::=--force-confold "$bootstrap_tmp/$asset"
  else
    [ "$(rpm -qp --qf '%{NAME}:%{VERSION}:%{RELEASE}:%{ARCH}' "$bootstrap_tmp/$asset")" = "noderampart:0.4.0:${rpm_release}.fc${os_version}:$rpm_arch" ] || bootstrap_die 'RPM package identity does not match the requested release'
    # Keep the administrator's repository and local-package signature policy.
    # A host requiring RPM signatures must use a properly signed release asset.
    dnf install -y "$bootstrap_tmp/$asset"
  fi
  [ -f /usr/bin/noderampart ] && [ ! -L /usr/bin/noderampart ] || bootstrap_die 'package installation did not provide the expected CLI'
  echo 'Package installed. Existing configuration and service choices were preserved.'
  if [ "$setup" = true ]; then
    LC_ALL="$bootstrap_ui_locale" /usr/bin/noderampart setup < /dev/tty > /dev/tty 2>&1
  else
    echo 'Setup was skipped. Run: sudo noderampart setup'
    echo 'Fresh Debian packages may already be observing with safe defaults; Fedora follows system presets.'
  fi
}

bootstrap_die() { echo "NodeRampart installer: $*" >&2; exit 1; }

bootstrap_checksum() {
  # Fixed names only, canonical lowercase SHA256, exactly one target entry.
  awk -v target="$1" '
    NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-f]/ || $2 ~ /[^A-Za-z0-9_.~+-]/ { bad=1; next }
    $2 == target {count++; hash=$1}
    END {if (bad || count != 1) exit 1; print hash}
  ' "$bootstrap_tmp/SHA256SUMS"
}

bootstrap_latest_release() {
  # Python is an installer-only JSON dependency. Isolated mode ignores caller
  # Python paths/startup settings; network data is never evaluated or sourced.
  python3 -I - "$bootstrap_tmp" <<'PYTHON'
import datetime
import json
from pathlib import Path
import re
import subprocess
import sys
import time

# Fixed anonymous API path, no token/curlrc, no redirects or /releases/latest.
# Twenty pages (100 objects each), 2 MiB per response, 30 s per request and
# 120 s overall. Reaching the bound without the terminating empty page fails.
root = Path(sys.argv[1])
pattern = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(alpha|beta|rc)(?:\.(0|[1-9][0-9]*))?)?")
deadline = time.monotonic() + 120
best = None
seen = set()


class ResolutionError(Exception):
    pass


def reject_constant(value):
    raise ValueError("non-finite JSON number")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON member")
        result[key] = value
    return result


try:
    for page in range(1, 21):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise ResolutionError("release API time limit exceeded")
        limit = 2097152
        response = root / "release-page.json"
        result = subprocess.run([
            "prlimit", f"--fsize={limit}:{limit}", "--core=0:0", "--",
            "curl", "--disable", "--globoff", "--proto", "=https", "--tlsv1.2",
            "--connect-timeout", "10", "--max-time", str(min(30, remaining)),
            "--retry", "0", "--max-filesize", str(limit), "--silent", "--show-error",
            "--header", "Accept: application/vnd.github+json",
            "--header", "X-GitHub-Api-Version: 2022-11-28",
            "--output", str(response), "--dump-header", str(root / "api-headers"),
            "--write-out", "%{http_code}",
            f"https://api.github.com/repos/littlesho/NodeRampart/releases?per_page=100&page={page}",
        ], capture_output=True, text=True, timeout=min(35, remaining), check=False)
        if result.returncode != 0:
            raise ResolutionError("release API transport failed")
        if result.stdout != "200":
            # Do not repeat arbitrary response bodies/URLs/credentials in logs.
            code = result.stdout if re.fullmatch(r"[0-9]{3}", result.stdout) else "invalid status"
            raise ResolutionError("release API returned HTTP " + code)
        if response.stat().st_size > limit:
            raise ResolutionError("release API response exceeds size limit")
        document = json.loads(response.read_text(encoding="utf-8"), object_pairs_hook=unique_object,
                              parse_constant=reject_constant)
        if not isinstance(document, list) or len(document) > 100:
            raise ResolutionError("invalid release API page")
        if not document:
            break
        for entry in document:
            if (not isinstance(entry, dict) or type(entry.get("id")) is not int or entry["id"] <= 0
                    or type(entry.get("draft")) is not bool or type(entry.get("prerelease")) is not bool
                    or not isinstance(entry.get("tag_name"), str)
                    or "published_at" not in entry
                    or entry["published_at"] is not None and not isinstance(entry["published_at"], str)):
                raise ResolutionError("invalid release metadata")
            if entry["id"] in seen:
                raise ResolutionError("repeated release across pages; retry a stable listing")
            seen.add(entry["id"])
            if entry["draft"] or entry["published_at"] is None:
                continue
            stamp = entry["published_at"]
            if not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", stamp):
                raise ResolutionError("invalid release publication timestamp")
            datetime.datetime.strptime(stamp, "%Y-%m-%dT%H:%M:%SZ")
            tag = entry["tag_name"]
            match = pattern.fullmatch(tag) if len(tag) <= 128 else None
            if match is None:
                continue  # Non-product objects do not become install targets.
            major, minor, patch, channel, number = match.groups()
            key = (int(major), int(minor), int(patch), channel is None,
                   channel or "", -1 if number is None else int(number))
            if best is None or key > best[0]:
                best = (key, tag)
    else:
        raise ResolutionError("release API page limit exceeded before complete listing")
    if time.monotonic() > deadline:
        raise ResolutionError("release API time limit exceeded")
    if best is None:
        raise ResolutionError("no published product release found")
    print(best[1])
except ResolutionError as error:
    print("NodeRampart installer: " + str(error), file=sys.stderr)
    sys.exit(1)
except (OSError, ValueError, RecursionError, subprocess.SubprocessError):
    # Exception details may include server-controlled content. Keep output fixed.
    print("NodeRampart installer: release API failed validation, transport, or resource limits", file=sys.stderr)
    sys.exit(1)
PYTHON
}

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
