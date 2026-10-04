// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const geoUpdateUnit = "noderampart-geoip-update.service"

// This is the complete published service generated through alpha.9: three
// bounding capabilities and no ambient capabilities. Recognizing all bytes,
// including the installed executable, leaves local changes untouched; an
// unpublished intermediate repair is not another recognized legacy template.
const legacyGeoUpdateService = `[Unit]
Description=NodeRampart local GeoIP data update
After=network-online.target systemd-tmpfiles-setup.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=%s assets update
User=root
Group=root
UMask=0077
NoNewPrivileges=yes
CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH CAP_FOWNER
ProtectSystem=strict
ReadWritePaths=/etc/noderampart /run/noderampart-management.lock
ProtectHome=yes
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
TimeoutStartSec=10min
`

// ReconcileGeoSchedule refreshes only a recognized old generated service.
// Package/source installers call it after installing the new binary. It does
// not download data, change timer enablement, or change public health metadata.
func (m *Manager) ReconcileGeoSchedule(ctx context.Context) (string, error) {
	if os.Geteuid() != 0 {
		return "", errors.New("GeoIP updater reconciliation requires the installed administrative command")
	}
	info, err := os.Stat("/run/systemd/system")
	running := err == nil && info.IsDir()
	// Include ordinary, control, attached and generated unit locations for
	// offline installs. A live manager's DropInPaths is also checked below.
	unitDirs := []string{m.unitDir, "/etc/systemd/system.control", "/run/systemd/system.control", "/run/systemd/transient",
		"/run/systemd/generator.early", "/etc/systemd/system.attached", "/run/systemd/system", "/run/systemd/system.attached",
		"/run/systemd/generator", "/usr/local/lib/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system", "/run/systemd/generator.late"}
	return m.reconcileGeoSchedule(ctx, running, unitDirs)
}

func (m *Manager) reconcileGeoSchedule(ctx context.Context, running bool, unitDirs []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m.binary != "/usr/bin/noderampart" && m.binary != "/usr/local/bin/noderampart" {
		return "", errors.New("GeoIP updater reconciliation requires a fixed installed executable")
	}
	release, err := m.lock()
	if err != nil {
		return "", err
	}
	defer release()
	if _, err := os.Lstat(m.unitDir); errors.Is(err, os.ErrNotExist) {
		return "No managed GeoIP updater service exists; no schedule was changed.", nil
	}
	parent, err := openDirectory(m.unitDir, true)
	if err != nil {
		return "", errors.New("GeoIP updater unit directory is unavailable or unsafe; no unit was changed")
	}
	defer parent.Close()
	var before unix.Stat_t
	err = unix.Fstatat(int(parent.Fd()), geoUpdateUnit, &before, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return "No managed GeoIP updater service exists; no schedule was changed.", nil
	}
	if err != nil {
		return "", errors.New("GeoIP updater service could not be inspected; no unit was changed")
	}
	if !canonicalGeoUnitOwner(before) {
		return "Custom, masked or unsafe GeoIP updater service preserved; review its sandbox manually.", nil
	}
	path := filepath.Join(m.unitDir, geoUpdateUnit)
	file, opened, err := openManagedFileAt(parent, path, 8192, false, 0)
	if err != nil {
		return "", errors.New("GeoIP updater service could not be read safely; no unit was changed")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	var after unix.Stat_t
	if err != nil || len(data) > 8192 || unix.Fstat(int(file.Fd()), &after) != nil ||
		!sameManagedFile(before, opened) || !sameManagedFile(before, after) {
		return "", errors.New("GeoIP updater service changed during inspection; no unit was changed")
	}
	current := []byte(m.geoUpdateService())
	legacy := []byte(fmt.Sprintf(legacyGeoUpdateService, m.binary))
	if !bytes.Equal(data, legacy) && !bytes.Equal(data, current) {
		return "Custom GeoIP updater service preserved; review its sandbox manually.", nil
	}
	custom, err := geoUpdateDropIns(unitDirs)
	if err != nil {
		return "", errors.New("GeoIP updater drop-ins could not be inspected safely; no unit was changed")
	}
	if !custom && running {
		paths, err := m.command(ctx, "/usr/bin/systemctl", "show", "--property=DropInPaths", "--value", geoUpdateUnit)
		if err != nil {
			return "", errors.New("GeoIP updater effective drop-ins are unavailable; no unit was changed")
		}
		custom = strings.TrimSpace(paths) != ""
	}
	warning := ""
	if custom {
		warning = " Drop-ins were preserved; the effective sandbox is not verified. Review overrides before running the updater."
	}
	if bytes.Equal(data, current) {
		return "GeoIP updater service template is current; no unit change or update was run." + warning, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := writeFileExpected(path, bytes.NewReader(current), 8192, 0o644, 0, 0, true, &before); err != nil {
		return "", fmt.Errorf("GeoIP updater service migration could not complete: %w", err)
	}
	if !running {
		return "GeoIP updater service template migrated; systemd will load it when started. No updater was started." + warning, nil
	}
	if _, err := m.command(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return "GeoIP updater service template was migrated; timer enablement and health metadata were preserved." + warning,
			errors.New("GeoIP updater unit was saved but daemon-reload failed; run systemctl daemon-reload before retrying the updater")
	}
	return "GeoIP updater service template migrated and reloaded; timer enablement and health metadata were preserved. No updater was started." + warning, nil
}

func canonicalGeoUnitOwner(stat unix.Stat_t) bool {
	return stat.Mode == unix.S_IFREG|0o644 && stat.Uid == 0 && stat.Gid == 0 && stat.Nlink == 1 && stat.Size > 0 && stat.Size <= 8192
}

func geoUpdateDropIns(unitDirs []string) (bool, error) {
	// systemd also applies type-wide and dash-prefix drop-in directories.
	for _, dir := range unitDirs {
		for _, name := range []string{geoUpdateUnit + ".d", "noderampart-geoip-.service.d", "noderampart-.service.d", "service.d"} {
			path := filepath.Join(dir, name)
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return false, err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return true, nil
			}
			parent, err := os.Open(path)
			if err != nil {
				return false, err
			}
			entries, readErr := parent.ReadDir(257)
			_ = parent.Close()
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return false, readErr
			}
			if len(entries) > 256 {
				return true, nil
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".conf") {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
