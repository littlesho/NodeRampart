#!/bin/sh
# SPDX-License-Identifier: MIT

set -eu
umask 077

if [ "$(id -u)" -ne 0 ]; then
  echo "install.sh must be run as root" >&2
  exit 1
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

for binary in noderampart noderampartd noderampart-sensor; do
  if [ ! -f "$PROJECT_DIR/bin/$binary" ] || [ -L "$PROJECT_DIR/bin/$binary" ]; then
    echo "missing regular binary: bin/$binary; run make build first" >&2
    exit 1
  fi
done

command -v systemctl >/dev/null 2>&1 || { echo "systemd is required" >&2; exit 1; }
command -v groupadd >/dev/null 2>&1 || { echo "groupadd is required" >&2; exit 1; }
command -v useradd >/dev/null 2>&1 || { echo "useradd is required" >&2; exit 1; }

for path in /etc/noderampart /var/lib/noderampart; do
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -d "$path" ]; }; then
    echo "refusing non-directory or symlinked installation directory: $path" >&2
    exit 1
  fi
done
if [ -L /etc/noderampart/config.json ] || { [ -e /etc/noderampart/config.json ] && [ ! -f /etc/noderampart/config.json ]; }; then
  echo "refusing non-regular existing configuration" >&2
  exit 1
fi
for path in /usr/local/share/doc/noderampart /usr/local/share/doc/noderampart/third-party /usr/local/share/doc/noderampart/source-install.manifest /usr/local/libexec /usr/local/libexec/noderampart /usr/local/libexec/noderampart/manage-remove; do
  if [ -L "$path" ]; then
    echo "refusing symlinked documentation target: $path" >&2
    exit 1
  fi
done
for path in /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor /etc/systemd/system/noderampartd.service /etc/systemd/system/noderampart-sensor.service; do
  if [ -L "$path" ]; then
    case "$path" in
      /etc/systemd/system/*.service)
        if [ "$(readlink "$path")" = /dev/null ]; then
          echo "source unit is permanently masked; installation stopped before changes: $path" >&2
          exit 1
        fi
        ;;
    esac
    echo "refusing symlinked installation target: $path" >&2
    exit 1
  fi
done

# Installed files still conflict when their services are disabled or stopped.
for path in /usr/bin/noderampart /usr/bin/noderampartd /usr/bin/noderampart-sensor /usr/lib/systemd/system/noderampartd.service /usr/lib/systemd/system/noderampart-sensor.service /lib/systemd/system/noderampartd.service /lib/systemd/system/noderampart-sensor.service; do
  if [ -e "$path" ] || [ -L "$path" ]; then
    echo "native package installation conflicts with source installation; use the package manager" >&2
    exit 1
  fi
done
if command -v dpkg-query >/dev/null 2>&1 && dpkg-query -W -f='${Status}' noderampart 2>/dev/null | grep -q ' installed$'; then
  echo "noderampart is owned by dpkg; use the package manager" >&2
  exit 1
fi
if command -v rpm >/dev/null 2>&1 && rpm -q noderampart >/dev/null 2>&1; then
  echo "noderampart is owned by RPM; use the package manager" >&2
  exit 1
fi
command -v sha256sum >/dev/null 2>&1 || { echo "sha256sum is required to record source ownership" >&2; exit 1; }

SOURCE_UPGRADE=false
for path in /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor /etc/systemd/system/noderampartd.service /etc/systemd/system/noderampart-sensor.service /usr/local/share/doc/noderampart/source-install.manifest; do
  if [ -e "$path" ]; then SOURCE_UPGRADE=true; fi
done
source_service_state() {
  active=$(systemctl show --property=ActiveState --value "$1") || return 1
  enabled=$(systemctl show --property=UnitFileState --value "$1") || return 1
  case "$active" in active|inactive|failed) ;; *) echo "source service is changing state; retry after it settles: $1" >&2; return 1;; esac
  case "$enabled" in enabled|enabled-runtime|disabled|masked|masked-runtime|static|indirect|linked|linked-runtime) ;;
    *) echo "cannot determine source service enablement: $1" >&2; return 1;;
  esac
  printf '%s:%s\n' "$active" "$enabled"
}
if [ "$SOURCE_UPGRADE" = true ]; then
  # Snapshot both units before writing binaries, configuration or unit files.
  # Existing enablement links are left in place; only initially active,
  # unmasked units are restarted after the new configuration passes validation.
  DAEMON_STATE=$(source_service_state noderampartd.service)
  SENSOR_STATE=$(source_service_state noderampart-sensor.service)
fi

if ! getent group noderampart >/dev/null; then
  groupadd --system noderampart
fi
if ! id noderampart >/dev/null 2>&1; then
  useradd --system --gid noderampart --home-dir /var/lib/noderampart --shell /usr/sbin/nologin noderampart
fi
if ! id noderampart-sensor >/dev/null 2>&1; then
  useradd --system --gid noderampart --home-dir /nonexistent --shell /usr/sbin/nologin noderampart-sensor
fi
if getent group systemd-journal >/dev/null; then
  usermod -a -G systemd-journal noderampart
fi

install -d -m 0750 -o root -g noderampart /etc/noderampart
install -d -m 0750 -o noderampart -g noderampart /var/lib/noderampart
install -m 0755 "$PROJECT_DIR/bin/noderampart" /usr/local/bin/noderampart
install -m 0755 "$PROJECT_DIR/bin/noderampartd" /usr/local/bin/noderampartd
install -m 0755 "$PROJECT_DIR/bin/noderampart-sensor" /usr/local/bin/noderampart-sensor
install -d -m 0755 /usr/local/libexec/noderampart
install -m 0755 "$PROJECT_DIR/scripts/manage-remove.sh" /usr/local/libexec/noderampart/manage-remove
install -d -m 0755 /usr/local/share/doc/noderampart/third-party
install -m 0644 "$PROJECT_DIR/LICENSE" "$PROJECT_DIR/README.md" "$PROJECT_DIR/THIRD_PARTY_NOTICES.md" /usr/local/share/doc/noderampart/
install -m 0644 "$PROJECT_DIR"/third_party/licenses/* /usr/local/share/doc/noderampart/third-party/

if [ -e /etc/noderampart/config.json ] || [ -L /etc/noderampart/config.json ]; then
  if [ -L /etc/noderampart/config.json ] || [ ! -f /etc/noderampart/config.json ]; then
    echo "refusing non-regular existing configuration" >&2
    exit 1
  fi
else
  install -m 0640 -o root -g noderampart "$PROJECT_DIR/configs/noderampart.json" /etc/noderampart/config.json
fi
chown root:noderampart /etc/noderampart/config.json
chmod 0640 /etc/noderampart/config.json

TEMP_DIR=$(mktemp -d /tmp/noderampart-install.XXXXXX)
trap 'rm -rf -- "$TEMP_DIR"' EXIT HUP INT TERM
for unit in noderampartd.service noderampart-sensor.service; do
  sed 's#/usr/bin/noderampart#/usr/local/bin/noderampart#g' "$PROJECT_DIR/packaging/systemd/$unit" > "$TEMP_DIR/$unit"
  install -m 0644 "$TEMP_DIR/$unit" "/etc/systemd/system/$unit"
done
sha256sum /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor /etc/systemd/system/noderampartd.service /etc/systemd/system/noderampart-sensor.service /usr/local/libexec/noderampart/manage-remove > "$TEMP_DIR/source-install.manifest"
install -m 0644 "$TEMP_DIR/source-install.manifest" /usr/local/share/doc/noderampart/source-install.manifest

/usr/local/bin/noderampart config test --config /etc/noderampart/config.json

systemctl daemon-reload
if [ "$SOURCE_UPGRADE" = false ]; then
  systemctl enable noderampartd.service noderampart-sensor.service
  systemctl restart noderampartd.service noderampart-sensor.service
else
  set --
  case "$DAEMON_STATE" in active:masked|active:masked-runtime) ;; active:*) set -- "$@" noderampartd.service;; esac
  case "$SENSOR_STATE" in active:masked|active:masked-runtime) ;; active:*) set -- "$@" noderampart-sensor.service;; esac
  if [ "$#" -gt 0 ]; then systemctl restart "$@"; fi
fi

echo "NodeRampart installed. Run: sudo noderampart status"
echo "Telegram remains disabled until explicitly configured."
