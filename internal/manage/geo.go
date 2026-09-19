// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/console"
	"golang.org/x/sys/unix"
)

type geoState struct {
	Version    int       `json:"schema_version"`
	Checked    time.Time `json:"checked_at"`
	Updated    time.Time `json:"updated_at"`
	CityBuild  time.Time `json:"city_build"`
	ASNBuild   time.Time `json:"asn_build"`
	Result     string    `json:"result"`
	Generation string    `json:"generation,omitempty"`
}

func (m *Manager) localPath(name string) string {
	return filepath.Join(filepath.Dir(m.ConfigPath), name)
}

func (m *Manager) writeJSON(path string, value any, secret bool) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("management metadata could not be encoded")
	}
	mode, gid := uint32(0o640), m.daemonGID
	if secret {
		mode, gid = 0o600, os.Getegid()
	}
	return writeFile(path, bytes.NewReader(append(data, '\n')), maxManagedJSON, mode, os.Geteuid(), gid, true)
}

func (m *Manager) updateGeoData(ctx context.Context, input map[string]string, refresh bool, updateResult *string) (string, error) {
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	credentials := assets.Credentials{AccountID: input["account_id"], LicenseKey: input["license_key"]}
	if refresh {
		data, err := readFile(m.localPath("maxmind.credentials.json"), 4096, true, -1)
		if err != nil || json.Unmarshal(data, &credentials) != nil {
			*updateResult = "credentials_unavailable"
			return "", errors.New("saved MaxMind credentials are unavailable; use Download and configure first")
		}
	} else if input["accepted_terms"] != "yes" || (input["auto_update"] != "yes" && input["auto_update"] != "no") {
		return "", errors.New("confirm your own MaxMind enrollment/terms acceptance and choose daily updates, or skip GeoIP")
	}
	state := geoState{Version: 1}
	if data, err := readFile(m.localPath("geoip-state.json"), 4096, true, -1); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	state.Version, state.Checked, state.Result = 1, time.Now().UTC(), "download_failed"
	*updateResult = "download_failed"
	defer func() {
		if state.Result != "ok" && state.Result != "unchanged" {
			_ = m.writeJSON(m.localPath("geoip-state.json"), state, true)
		}
	}()
	bundle, err := m.Assets.DownloadGeo(ctx, credentials)
	if err != nil {
		return "", err
	}
	defer bundle.Close()
	state.Result = "activation_failed"
	*updateResult = "activation_failed"
	base := m.localPath("geoip")
	if err := ensureDirectory(base, 0o750, m.daemonGID); err != nil {
		return "", err
	}
	if err := m.cleanGeo(snapshot.Config.Geo.CityMMDB, snapshot.Config.Geo.ASNMMDB); err != nil {
		return "", err
	}
	files := map[string]string{"GeoLite2-City.mmdb": bundle.CityPath, "GeoLite2-ASN.mmdb": bundle.ASNPath}
	if len(bundle.Notices) > 6 {
		return "", errors.New("too many GeoIP license notices")
	}
	for _, notice := range bundle.Notices {
		if filepath.Base(notice) != notice || !strings.HasSuffix(notice, ".txt") {
			return "", errors.New("invalid GeoIP license notice path")
		}
		if _, duplicate := files[notice]; duplicate {
			return "", errors.New("duplicate GeoIP license notice")
		}
		files[notice] = filepath.Join(bundle.Directory, notice)
	}
	unchanged, err := m.geoUnchanged(ctx, snapshot, files)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(snapshot.Config.Geo.CityMMDB)
	if !unchanged {
		dir = filepath.Join(base, "gen-"+rand.Text())
		if err := ensureDirectory(dir, 0o750, m.daemonGID); err != nil {
			return "", err
		}
		defer func() {
			// Retain anything referenced by either active config or an interrupted apply.
			if current, err := m.Load(context.Background()); err == nil {
				_ = m.cleanGeo(current.Config.Geo.CityMMDB, current.Config.Geo.ASNMMDB)
			}
		}()
		for name, source := range files {
			data, err := readFile(source, 256<<20, false, -1)
			if err != nil {
				return "", errors.New("staged GeoIP data is unavailable")
			}
			if err := writeFile(filepath.Join(dir, name), bytes.NewReader(data), 256<<20, 0o640, os.Geteuid(), m.daemonGID, false); err != nil {
				return "", err
			}
		}
	}
	// Credentials are not part of daemon configuration or its ordinary backup.
	// Store only credentials that successfully fetched both validated databases.
	if !refresh {
		if err := m.writeJSON(m.localPath("maxmind.credentials.json"), credentials, true); err != nil {
			return "", err
		}
	}
	result := "GeoLite2 City and ASN are unchanged; activation was not needed. / 本地 GeoLite2 City 和 ASN 内容未变，无需切换或重启。"
	state.Result = "unchanged"
	if !unchanged {
		state.Result = "activation_failed"
		snapshot.Config.Geo.CityMMDB = filepath.Join(dir, "GeoLite2-City.mmdb")
		snapshot.Config.Geo.ASNMMDB = filepath.Join(dir, "GeoLite2-ASN.mmdb")
		result, err = m.saveLocked(ctx, snapshot)
		if err != nil {
			return "", err
		}
		state.Updated, state.Result = time.Now().UTC(), "ok"
		state.CityBuild, state.ASNBuild = bundle.CityBuild, bundle.ASNBuild
		state.Generation = dir
	}
	if err := m.writeJSON(m.localPath("geoip-state.json"), state, true); err != nil {
		return result, errors.New("GeoIP data verified, but update metadata could not be saved")
	}
	*updateResult = state.Result
	if !refresh {
		if _, err := m.scheduleGeo(ctx, input["auto_update"] == "yes"); err != nil {
			return result, errors.New("GeoIP data verified; daily update scheduling failed, inspect GeoIP status")
		}
	}
	if err := m.cleanGeo(snapshot.Config.Geo.CityMMDB, snapshot.Config.Geo.ASNMMDB); err != nil {
		state.Result = "cleanup_failed"
		*updateResult = "activation_failed"
		return result, errors.New("GeoIP data verified; superseded managed database cleanup failed")
	}
	if unchanged {
		return result, nil
	}
	return "GeoLite2 City and ASN installed. / 已安装本地 GeoLite2 City 和 ASN 数据库。\n" + result, nil
}

// geoUnchanged only recognizes a complete, trusted managed generation. Invalid
// active data falls through to normal paired replacement; it cannot bypass
// activation merely because the new bundle contains identical bytes.
func (m *Manager) geoUnchanged(ctx context.Context, snapshot console.Snapshot, files map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	city, asn := snapshot.Config.Geo.CityMMDB, snapshot.Config.Geo.ASNMMDB
	dir := filepath.Dir(city)
	if filepath.Dir(dir) != m.localPath("geoip") || !strings.HasPrefix(filepath.Base(dir), "gen-") ||
		city != filepath.Join(dir, "GeoLite2-City.mmdb") || asn != filepath.Join(dir, "GeoLite2-ASN.mmdb") ||
		m.daemonReadable(m.ConfigPath, false) != nil || m.daemonDirectories(dir, 1) != nil {
		return false, nil
	}
	directory, err := openDirectory(dir, true)
	if err != nil {
		return false, nil
	}
	defer directory.Close()
	entries, err := directory.ReadDir(9)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) != len(files) || len(entries) > 8 {
		return false, nil
	}
	parent, err := openDirectory(filepath.Dir(dir), true)
	if err != nil {
		return false, nil
	}
	defer parent.Close()
	var parentMount, directoryMount unix.Statx_t
	if unix.Statx(int(parent.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &parentMount) != nil ||
		unix.Statx(int(directory.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &directoryMount) != nil ||
		parentMount.Mask&unix.STATX_MNT_ID == 0 || directoryMount.Mask&unix.STATX_MNT_ID == 0 || parentMount.Mnt_id != directoryMount.Mnt_id {
		return false, nil
	}
	for _, entry := range entries {
		source, exists := files[entry.Name()]
		if !exists {
			return false, nil
		}
		activeDigest, err := m.geoFileDigest(ctx, dir, entry.Name(), true)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil
		}
		stagedDigest, err := m.geoFileDigest(ctx, filepath.Dir(source), filepath.Base(source), false)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, errors.New("staged GeoIP data could not be compared safely")
		}
		if activeDigest != stagedDigest {
			return false, nil
		}
	}
	// The lock serializes manager operations; an external configuration edit must
	// still be detected before choosing a path that deliberately skips Save.
	current, err := m.Load(ctx)
	if err != nil {
		return false, err
	}
	if current.Fingerprint != snapshot.Fingerprint {
		return false, errors.New("configuration changed since GeoIP download began; reload and retry")
	}
	return true, nil
}

// Hash through bounded, no-follow descriptors without retaining whole MMDBs in
// memory. Active files additionally need the daemon's read permission, and a
// bind-mounted member must not be mistaken for a manager-owned ordinary file.
func (m *Manager) geoFileDigest(ctx context.Context, dir, name string, active bool) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	directory, err := openDirectory(dir, true)
	if err != nil {
		return sum, err
	}
	defer directory.Close()
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return sum, errors.New("GeoIP file is unavailable")
	}
	f := os.NewFile(uintptr(fd), "geoip-comparison")
	defer f.Close()
	maximum := int64(1 << 20)
	if name == "GeoLite2-City.mmdb" || name == "GeoLite2-ASN.mmdb" {
		maximum = 160 << 20
	}
	var before unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 ||
		before.Uid != uint32(os.Geteuid()) || before.Mode&0o022 != 0 || before.Size < 0 || before.Size > maximum || active && !m.dac(before, 4) {
		return sum, errors.New("GeoIP file is not a trusted readable managed file")
	}
	var directoryMount, memberMount unix.Statx_t
	if unix.Statx(int(directory.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &directoryMount) != nil ||
		unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &memberMount) != nil ||
		directoryMount.Mask&unix.STATX_MNT_ID == 0 || memberMount.Mask&unix.STATX_MNT_ID == 0 || directoryMount.Mnt_id != memberMount.Mnt_id {
		return sum, errors.New("GeoIP file is mounted or cannot be verified")
	}
	hash := sha256.New()
	reader := io.LimitReader(f, maximum+1)
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		n, err := reader.Read(buffer)
		total += int64(n)
		if total > maximum {
			return sum, errors.New("GeoIP file exceeds its comparison limit")
		}
		_, _ = hash.Write(buffer[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return sum, errors.New("GeoIP file could not be compared")
		}
	}
	var after unix.Stat_t
	if unix.Fstat(fd, &after) != nil || before.Size != total || before.Size != after.Size || before.Mode != after.Mode ||
		before.Uid != after.Uid || before.Gid != after.Gid || before.Nlink != after.Nlink || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return sum, errors.New("GeoIP file changed during comparison")
	}
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}

// cleanGeo only removes flat, bounded, trusted generations created by this
// manager. Any pending recovery journal pins old assets until recovery finishes.
func (m *Manager) cleanGeo(city, asn string) error {
	if _, err := readFile(m.journalPath(), 2*maxManagedJSON, true, -1); !errors.Is(err, os.ErrNotExist) {
		return errors.New("recover the pending configuration apply before replacing GeoIP data")
	}
	base := m.localPath("geoip")
	directory, err := openDirectory(base, true)
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(129)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 128 {
		return errors.New("managed GeoIP directory needs manual inspection")
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "gen-") {
			continue
		}
		path := filepath.Join(base, entry.Name())
		if filepath.Dir(city) == path || filepath.Dir(asn) == path {
			continue
		}
		if err := removeGeneration(path); err != nil {
			return err
		}
	}
	return nil
}

func removeGeneration(path string) error {
	directory, err := openDirectory(path, true)
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(9)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 8 {
		return errors.New("managed generation has unexpected contents")
	}
	var ds unix.Stat_t
	if unix.Fstat(int(directory.Fd()), &ds) != nil {
		return errors.New("managed generation is unavailable")
	}
	parent, err := openDirectory(filepath.Dir(path), true)
	if err != nil {
		return err
	}
	defer parent.Close()
	var ps unix.Stat_t
	if unix.Fstat(int(parent.Fd()), &ps) != nil || ps.Dev != ds.Dev {
		return errors.New("refusing to clean a mounted generation")
	}
	var parentMount, directoryMount unix.Statx_t
	if unix.Statx(int(parent.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &parentMount) != nil ||
		unix.Statx(int(directory.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &directoryMount) != nil ||
		parentMount.Mask&unix.STATX_MNT_ID == 0 || directoryMount.Mask&unix.STATX_MNT_ID == 0 || parentMount.Mnt_id != directoryMount.Mnt_id {
		return errors.New("refusing to clean a mounted or unverified generation")
	}
	for _, entry := range entries {
		var st unix.Stat_t
		if unix.Fstatat(int(directory.Fd()), entry.Name(), &st, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Dev != ds.Dev || st.Uid != uint32(os.Geteuid()) {
			return errors.New("managed generation contains unsafe files")
		}
		var memberMount unix.Statx_t
		if unix.Statx(int(directory.Fd()), entry.Name(), unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &memberMount) != nil || memberMount.Mask&unix.STATX_MNT_ID == 0 || memberMount.Mnt_id != directoryMount.Mnt_id {
			return errors.New("refusing to clean a mounted or unverified member")
		}
	}
	for _, entry := range entries {
		if unix.Unlinkat(int(directory.Fd()), entry.Name(), 0) != nil {
			return errors.New("old managed data could not be removed")
		}
	}
	if directory.Sync() != nil || unix.Unlinkat(int(parent.Fd()), filepath.Base(path), unix.AT_REMOVEDIR) != nil || parent.Sync() != nil {
		return errors.New("old managed generation could not be removed durably")
	}
	return nil
}

func (m *Manager) scheduleGeoService(ctx context.Context, enabled bool) (string, error) {
	if !enabled {
		_, err := m.command(ctx, "/usr/bin/systemctl", "disable", "--now", "noderampart-geoip-update.timer")
		if err != nil {
			// Absence of an /etc file alone cannot exclude a loaded, runtime or
			// vendor timer. Only systemd's absent+inactive state is sufficient.
			load, loadErr := m.command(ctx, "/usr/bin/systemctl", "show", "--property=LoadState", "--value", "noderampart-geoip-update.timer")
			active, activeErr := m.command(ctx, "/usr/bin/systemctl", "show", "--property=ActiveState", "--value", "noderampart-geoip-update.timer")
			if loadErr != nil || activeErr != nil || strings.TrimSpace(load) != "not-found" || strings.TrimSpace(active) != "inactive" {
				return "", err
			}
		}
		return "Daily GeoIP updates disabled. / 已关闭每日更新。", nil
	}
	if _, err := readFile(m.localPath("maxmind.credentials.json"), 4096, true, -1); err != nil {
		return "", errors.New("download GeoIP with your own credentials before enabling daily updates")
	}
	// /run is cleared on boot. Create the lock through the normal boot tmpfiles
	// service before systemd tries to bind it into this oneshot's write sandbox.
	// Existing lock inodes are preserved; never replace/unlink an active flock.
	if err := ensureDirectory(m.tmpfilesDir, 0o755, os.Getegid()); err != nil {
		return "", err
	}
	if err := writeFile(filepath.Join(m.tmpfilesDir, "noderampart-management.conf"), strings.NewReader("# NodeRampart management lock; preserve existing inode.\nf /run/noderampart-management.lock 0600 root root - -\n"), 4096, 0o644, os.Geteuid(), os.Getegid(), true); err != nil {
		return "", err
	}
	service := fmt.Sprintf(`[Unit]
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
`, m.binary)
	timer := "[Unit]\nDescription=Daily NodeRampart GeoIP data refresh\n\n[Timer]\nOnCalendar=daily\nRandomizedDelaySec=1h\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
	for name, content := range map[string]string{"noderampart-geoip-update.service": service, "noderampart-geoip-update.timer": timer} {
		if err := writeFile(filepath.Join(m.unitDir, name), strings.NewReader(content), 8192, 0o644, os.Geteuid(), os.Getegid(), true); err != nil {
			return "", err
		}
	}
	if _, err := m.command(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return "", err
	}
	if _, err := m.command(ctx, "/usr/bin/systemctl", "enable", "--now", "noderampart-geoip-update.timer"); err != nil {
		return "", err
	}
	return "Daily GeoIP updates enabled. / 已启用每日更新。", nil
}

func (m *Manager) geoStatus(ctx context.Context) (string, error) {
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "City: %s\nASN: %s\n", snapshot.Config.Geo.CityMMDB, snapshot.Config.Geo.ASNMMDB)
	out.WriteString("Verified update and scheduling outcomes / 数据验证与定时配置结果:\n" + pretty(m.previousGeoHealth()) + "\n")
	if data, err := readFile(m.localPath("geoip-state.json"), 4096, true, -1); err == nil {
		var state geoState
		if json.Unmarshal(data, &state) == nil && state.Version == 1 {
			out.WriteString("Last managed update / 上次托管更新:\n" + pretty(state) + "\n")
			current := state.Generation != "" && filepath.Dir(snapshot.Config.Geo.CityMMDB) == state.Generation && filepath.Dir(snapshot.Config.Geo.ASNMMDB) == state.Generation
			if !current {
				out.WriteString("Current file paths differ from the last managed download; its build dates do not describe the current files. / 当前路径与上次托管下载不同，以下历史构建日期不代表当前文件。\n")
			}
			if current && (time.Since(state.CityBuild) > 30*24*time.Hour || time.Since(state.ASNBuild) > 30*24*time.Hour) {
				out.WriteString("Database age exceeds 30 days; refresh and check MaxMind license requirements. / 数据库超过 30 天，请更新并检查许可要求。\n")
			}
		}
	}
	for _, property := range []string{"ActiveState", "UnitFileState", "NextElapseUSecRealtime"} {
		if value, err := m.command(ctx, "/usr/bin/systemctl", "show", "--property="+property, "--value", "noderampart-geoip-update.timer"); err == nil {
			fmt.Fprintf(&out, "%s: %s\n", property, strings.TrimSpace(value))
		}
	}
	return out.String(), nil
}
