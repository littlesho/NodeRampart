# Release binaries use Go -s -w and contain no DWARF for RPM's automatic
# debuginfo/debugsource packages, whose generated file lists would be empty.
%global debug_package %{nil}

Name:           noderampart
%{!?noderampart_commit:%global noderampart_commit unknown}
%{!?noderampart_build_date:%global noderampart_build_date unknown}
%{!?noderampart_version:%global noderampart_version 0.4.0-alpha.2}
Version:        0.4.0
Release:        0.alpha.3%{?dist}
Summary:        Security monitoring and traffic reporting agent for Linux VPS hosts
License:        MIT AND BSD-3-Clause AND ISC AND Apache-2.0
URL:            https://github.com/littlesho/NodeRampart
Source0:        %{name}-%{noderampart_version}.tar.gz
BuildRequires:  golang >= 1.25
BuildRequires:  systemd-rpm-macros
ExclusiveArch:  x86_64 aarch64
Requires(pre):  systemd
Requires(pre):  shadow-utils
Requires(post): systemd
Requires(preun): systemd
Requires(postun): systemd
Requires:       tzdata
Requires:       ca-certificates
Requires:       util-linux

%description
NodeRampart observes bounded packet metadata and OpenSSH authentication events,
sends optional Telegram alerts, and creates daily security and traffic reports.

%prep
%autosetup -n NodeRampart-%{noderampart_version}

%build
export CGO_ENABLED=0
export GOOS=linux
case '%{_target_cpu}' in
  x86_64) export GOARCH=amd64;;
  aarch64) export GOARCH=arm64;;
  *) echo 'unsupported RPM architecture' >&2; exit 1;;
esac
export GOFLAGS="-mod=vendor -buildvcs=false -buildmode=pie -trimpath"
for binary in noderampart noderampartd noderampart-sensor; do
  go build -ldflags "-s -w -X github.com/littlesho/NodeRampart/internal/version.Version=%{noderampart_version} -X github.com/littlesho/NodeRampart/internal/version.Commit=%{noderampart_commit} -X github.com/littlesho/NodeRampart/internal/version.BuildDate=%{noderampart_build_date}" -o "bin/$binary" "./cmd/$binary"
done

%install
install -Dpm0755 bin/noderampart %{buildroot}%{_bindir}/noderampart
install -Dpm0755 bin/noderampartd %{buildroot}%{_bindir}/noderampartd
install -Dpm0755 bin/noderampart-sensor %{buildroot}%{_bindir}/noderampart-sensor
install -Dpm0755 scripts/manage-remove.sh %{buildroot}%{_libexecdir}/noderampart/manage-remove
install -Dpm0640 configs/noderampart.json %{buildroot}%{_sysconfdir}/noderampart/config.json
install -Dpm0644 packaging/systemd/noderampartd.service %{buildroot}%{_unitdir}/noderampartd.service
install -Dpm0644 packaging/systemd/noderampart-sensor.service %{buildroot}%{_unitdir}/noderampart-sensor.service
install -Dpm0644 packaging/sysusers.d/noderampart.conf %{buildroot}%{_sysusersdir}/noderampart.conf

%pre
set -e
for path in %{_sysconfdir}/noderampart /var/lib/noderampart; do
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -d "$path" ]; }; then
    echo "refusing non-directory or symlinked installation directory: $path" >&2
    exit 1
  fi
done
if [ -L %{_sysconfdir}/noderampart/config.json ] || { [ -e %{_sysconfdir}/noderampart/config.json ] && [ ! -f %{_sysconfdir}/noderampart/config.json ]; }; then
  echo "refusing non-regular existing configuration" >&2
  exit 1
fi
for path in /usr/local/bin/noderampart /usr/local/bin/noderampartd /usr/local/bin/noderampart-sensor %{_sysconfdir}/systemd/system/noderampartd.service %{_sysconfdir}/systemd/system/noderampart-sensor.service; do
  if [ -L "$path" ] && [ "$(readlink "$path")" = /dev/null ]; then
    case "$path" in %{_sysconfdir}/systemd/system/*.service) continue;; esac
  fi
  if [ -e "$path" ] || [ -L "$path" ]; then
    echo "source installation or full unit override conflicts with the package; run scripts/source-to-package.sh --prepare for owned source files" >&2
    exit 1
  fi
done
getent group noderampart >/dev/null || groupadd -r noderampart
getent passwd noderampart >/dev/null || useradd -r -g noderampart -d /var/lib/noderampart -s /usr/sbin/nologin -c "NodeRampart daemon" noderampart
getent passwd noderampart-sensor >/dev/null || useradd -r -g noderampart -d /nonexistent -s /usr/sbin/nologin -c "NodeRampart packet sensor" noderampart-sensor
getent group systemd-journal >/dev/null && usermod -a -G systemd-journal noderampart || :

%post
set -e
if [ -L %{_sysconfdir}/noderampart ] || [ -L %{_sysconfdir}/noderampart/config.json ] || [ ! -f %{_sysconfdir}/noderampart/config.json ]; then
  echo "refusing non-regular configuration" >&2
  exit 1
fi
chown root:noderampart %{_sysconfdir}/noderampart/config.json
chmod 0640 %{_sysconfdir}/noderampart/config.json
%{_bindir}/noderampart config test --config %{_sysconfdir}/noderampart/config.json >/dev/null
%systemd_post noderampartd.service noderampart-sensor.service

%preun
if [ "$1" -eq 0 ]; then
  for path in /etc/systemd/system /etc/systemd/system/noderampartd.service.d; do
    [ ! -L "$path" ] || { echo "refusing symlinked managed unit directory: $path" >&2; exit 1; }
  done
  if [ -d /run/systemd/system ]; then
    for unit in noderampart-geoip-update.timer noderampart-geoip-update.service; do
      if [ "$(systemctl show --property=LoadState --value "$unit")" != not-found ]; then
        systemctl disable --now "$unit" || exit 1
      fi
      state=$(systemctl show --property=ActiveState --value "$unit") || exit 1
      case "$state" in inactive|failed) ;; *) echo "refusing removal while $unit is $state" >&2; exit 1;; esac
    done
  fi
  rm -f -- /etc/systemd/system/noderampart-geoip-update.service /etc/systemd/system/noderampart-geoip-update.timer /etc/systemd/system/noderampartd.service.d/90-noderampart-managed.conf || exit 1
fi
%systemd_preun noderampartd.service noderampart-sensor.service

%postun
%systemd_postun_with_restart noderampartd.service noderampart-sensor.service

%files
%license LICENSE
%doc README.md THIRD_PARTY_NOTICES.md third_party/licenses
%{_bindir}/noderampart
%{_bindir}/noderampartd
%{_bindir}/noderampart-sensor
%dir %{_libexecdir}/noderampart
%{_libexecdir}/noderampart/manage-remove
%config(noreplace) %attr(0640,root,noderampart) %{_sysconfdir}/noderampart/config.json
%{_unitdir}/noderampartd.service
%{_unitdir}/noderampart-sensor.service
%{_sysusersdir}/noderampart.conf

%changelog
* Sun Aug 16 2026 NodeRampart maintainers <noreply@github.com> - 0.1.0-0.alpha.1
- Initial alpha package
