// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/console"
	"github.com/littlesho/NodeRampart/internal/enrich"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/privacy"
	"golang.org/x/sys/unix"
)

type RequestFunc func(context.Context, string, any) (json.RawMessage, error)

type Manager struct {
	ConfigPath  string
	Request     RequestFunc
	Assets      *assets.Client
	daemonUID   int
	daemonGID   int
	lockPath    string
	unitDir     string
	tmpfilesDir string
	stateDir    string
	runtimeDir  string
	binary      string
	sandbox     bool // Installed unit path visibility; synthetic tests use private trees.
	runner      func(context.Context, string, ...string) (string, error)
}

func New(path string, request RequestFunc) (*Manager, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("open administrative menus with sudo noderampart tui")
	}
	if path != "/etc/noderampart/config.json" {
		return nil, errors.New("installed services use /etc/noderampart/config.json; administrative menus require that configuration path")
	}
	uid, err := ipc.ServiceUID(ipc.DaemonUser)
	if err != nil {
		return nil, errors.New("install the NodeRampart package before running setup")
	}
	group, err := user.LookupGroup("noderampart")
	if err != nil {
		return nil, errors.New("NodeRampart service group is unavailable")
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return nil, errors.New("NodeRampart service group is invalid")
	}
	binary, err := os.Executable()
	if err != nil {
		return nil, errors.New("installed management executable is unavailable")
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil || binary != "/usr/bin/noderampart" && binary != "/usr/local/bin/noderampart" {
		return nil, errors.New("run setup from the installed /usr/bin or /usr/local/bin noderampart")
	}
	return &Manager{ConfigPath: path, Request: request, Assets: &assets.Client{}, daemonUID: int(uid), daemonGID: gid,
		lockPath: "/run/noderampart-management.lock", unitDir: "/etc/systemd/system", tmpfilesDir: "/etc/tmpfiles.d", stateDir: "/var/lib/noderampart", runtimeDir: "/run/noderampart", binary: binary, sandbox: true}, nil
}

func decodeConfig(data []byte) (config.Config, error) {
	cfg := config.Defaults()
	if len(data) > maxManagedJSON {
		return cfg, errors.New("configuration exceeds its size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cfg) != nil || decoder.Decode(new(any)) != io.EOF {
		return cfg, errors.New("configuration is not valid supported JSON")
	}
	return cfg, cfg.Validate()
}

func (m *Manager) Load(ctx context.Context) (console.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return console.Snapshot{}, err
	}
	data, err := readFile(m.ConfigPath, maxManagedJSON, false, -1)
	if errors.Is(err, os.ErrNotExist) {
		return console.Snapshot{Config: config.Defaults()}, nil
	}
	if err != nil {
		return console.Snapshot{}, err
	}
	cfg, err := decodeConfig(data)
	return console.Snapshot{Config: cfg, Fingerprint: digest(data)}, err
}

func (m *Manager) lock() (func(), error) {
	parent, err := openDirectory(filepath.Dir(m.lockPath), true)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(m.lockPath), unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, errors.New("management lock is unavailable")
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 ||
		stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o077 != 0 {
		unix.Close(fd)
		return nil, errors.New("management lock has unsafe ownership or permissions")
	}
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		unix.Close(fd)
		return nil, errors.New("another setup, update or removal is running; retry after it finishes")
	}
	return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }, nil
}

func (m *Manager) command(ctx context.Context, program string, args ...string) (string, error) {
	if m.runner != nil {
		return m.runner(ctx, program, args...)
	}
	work, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(work, program, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0", "DEBIAN_FRONTEND=noninteractive"}
	var output limitedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return "", errors.New("local service/package operation failed; check service status or the system journal locally")
	}
	return output.String(), nil
}

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if room := (64 << 10) - b.Len(); room > 0 {
		_, _ = b.Buffer.Write(p[:min(room, n)])
	}
	return n, nil
}

type serviceState struct {
	Active bool   `json:"active"`
	Unit   string `json:"unit_file_state"`
}

func (m *Manager) serviceStates(ctx context.Context) (map[string]serviceState, error) {
	states := make(map[string]serviceState)
	for _, name := range []string{"noderampartd.service", "noderampart-sensor.service"} {
		active, err := m.command(ctx, "/usr/bin/systemctl", "show", "--property=ActiveState", "--value", name)
		if err != nil {
			return nil, err
		}
		unit, err := m.command(ctx, "/usr/bin/systemctl", "show", "--property=UnitFileState", "--value", name)
		if err != nil {
			return nil, err
		}
		states[name] = serviceState{Active: strings.TrimSpace(active) == "active", Unit: strings.TrimSpace(unit)}
	}
	return states, nil
}

func (m *Manager) activate(ctx context.Context, cfg config.Config, previous map[string]serviceState) error {
	for _, name := range []string{"noderampartd.service", "noderampart-sensor.service"} {
		if previous[name].Active && strings.HasPrefix(previous[name].Unit, "masked") {
			return errors.New("an active service is masked; unmask it explicitly before restarting")
		}
	}
	if previous["noderampart-sensor.service"].Active {
		if _, err := m.command(ctx, "/usr/bin/systemctl", "stop", "noderampart-sensor.service"); err != nil {
			return err
		}
	}
	if previous["noderampartd.service"].Active {
		if strings.HasPrefix(previous["noderampartd.service"].Unit, "masked") {
			return errors.New("daemon is masked; unmask it explicitly before applying to the running service")
		}
		if err := m.serviceOperation(ctx, "restart", "noderampartd.service"); err != nil {
			return err
		}
	}
	if cfg.Sensor.Enabled && previous["noderampart-sensor.service"].Active && !strings.HasPrefix(previous["noderampart-sensor.service"].Unit, "masked") {
		if err := m.serviceOperation(ctx, "start", "noderampart-sensor.service"); err != nil {
			return err
		}
	}
	return m.waitReady(ctx, previous["noderampartd.service"].Active, cfg.Sensor.Enabled && previous["noderampart-sensor.service"].Active)
}

// Rapid intentional configuration changes can exhaust systemd's native start
// burst even when the application has never crashed. Respect that policy: wait
// one bounded native interval and retry once only for an explicit rate-limit
// result. Never reset failed state/counters or retry ordinary startup failures.
func (m *Manager) serviceOperation(ctx context.Context, operation, unit string) error {
	_, original := m.command(ctx, "/usr/bin/systemctl", operation, unit)
	if original == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := m.command(ctx, "/usr/bin/systemctl", "show", "--property=Result", "--value", unit)
	if err != nil || strings.TrimSpace(result) != "start-limit-hit" {
		return original
	}
	value, err := m.command(ctx, "/usr/bin/systemctl", "show", "--property=StartLimitIntervalUSec", "--value", unit)
	if err != nil {
		return original
	}
	interval, err := time.ParseDuration(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "min", "m"), " ", ""))
	if err != nil || interval <= 0 || interval > 20*time.Second {
		return errors.New("service start is rate-limited; wait for the configured systemd interval before retrying or recovering")
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	_, err = m.command(ctx, "/usr/bin/systemctl", operation, unit)
	return err
}

// Type=simple start completes before the application's initialization. Check
// both process state and the authenticated daemon response before reporting success.
func (m *Manager) waitReady(ctx context.Context, daemon, sensor bool) error {
	if daemon || sensor {
		deadline, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		for {
			ready := true
			for unit, wanted := range map[string]bool{"noderampartd.service": daemon, "noderampart-sensor.service": sensor} {
				if wanted {
					state, err := m.command(deadline, "/usr/bin/systemctl", "show", "--property=ActiveState", "--value", unit)
					ready = ready && err == nil && strings.TrimSpace(state) == "active"
				}
			}
			if ready && daemon {
				if m.Request == nil {
					ready = false
				} else {
					_, err := m.Request(deadline, "status", struct{}{})
					ready = err == nil
				}
			}
			if ready {
				return nil
			}
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-deadline.Done():
				timer.Stop()
				return errors.New("configured services did not become ready; inspect status and the local journal")
			case <-timer.C:
			}
		}
	}
	return nil
}

func within(path, directory string) bool {
	return cleanPath(path) && strings.HasPrefix(path, directory+"/")
}

func (m *Manager) preflight(cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !within(cfg.Paths.Database, m.stateDir) || filepath.Dir(cfg.Paths.SensorSocket) != m.runtimeDir || filepath.Dir(cfg.Paths.ControlSocket) != m.runtimeDir {
		return errors.New("managed database paths must stay under /var/lib/noderampart and sockets directly under /run/noderampart; external paths need a separate reviewed service configuration")
	}
	parentPath, parentBits := filepath.Dir(cfg.Paths.Database), uint32(3)
	missingState := false
	if parentPath == m.stateDir {
		if _, err := os.Lstat(m.stateDir); errors.Is(err, os.ErrNotExist) {
			// Fresh Fedora presets can leave services stopped. systemd creates
			// their default StateDirectory with the service identity on start.
			missingState, parentPath, parentBits = true, filepath.Dir(m.stateDir), 1
		}
	}
	if m.daemonDirectories(parentPath, parentBits) != nil || m.daemonDirectories(filepath.Dir(m.ConfigPath), 1) != nil {
		return errors.New("database parent must already be writable by the daemon and configuration parents searchable; setup does not move history")
	}
	if !missingState {
		parent, err := openDirectory(filepath.Dir(cfg.Paths.Database), false)
		if err != nil {
			return err
		}
		var existing unix.Stat_t
		err = unix.Fstatat(int(parent.Fd()), filepath.Base(cfg.Paths.Database), &existing, unix.AT_SYMLINK_NOFOLLOW)
		parent.Close()
		if err == nil && (existing.Mode&unix.S_IFMT != unix.S_IFREG || existing.Nlink != 1 || existing.Uid != uint32(m.daemonUID) || !m.dac(existing, 6)) || err != nil && !errors.Is(err, unix.ENOENT) {
			return errors.New("existing database must be a regular daemon-owned writable file without hardlinks")
		}
	}
	for _, path := range []string{cfg.Geo.CityMMDB, cfg.Geo.ASNMMDB, cfg.Billing.ProfilePath} {
		if path != "" && m.daemonReadable(path, false) != nil {
			return errors.New("a GeoIP or billing file is unsafe or unreadable by the daemon")
		}
	}
	geo, err := enrich.Open(cfg.Geo.CityMMDB, cfg.Geo.ASNMMDB)
	if err != nil {
		return errors.New("configured GeoIP database is invalid; download or select a valid local MMDB first")
	}
	_ = geo.Close()
	if cfg.Billing.Enabled {
		if _, err := billing.Load(cfg.Billing.ProfilePath); err != nil {
			return errors.New("configured billing profile is invalid")
		}
	}
	if cfg.Notifications.Telegram.Enabled {
		if m.daemonReadable(cfg.Notifications.Telegram.TokenFile, true) != nil {
			return errors.New("Telegram token must be daemon-owned 0600 and readable through its parent directories")
		}
		if _, err := notify.NewTelegram(cfg.Notifications.Telegram.TokenFile, cfg.Notifications.Telegram.ChatID, cfg.Notifications.Telegram.Timeout.Duration); err != nil {
			return errors.New("Telegram token is missing or invalid; use Telegram setup")
		}
	}
	if cfg.Privacy.NotificationIP == "hash" || cfg.Privacy.StoreIP == "hash" {
		if m.daemonReadable(cfg.Privacy.HashKeyFile, true) != nil {
			return errors.New("privacy hash key must be daemon-owned 0600; use Generate privacy key")
		}
		if _, err := privacy.New("hash", cfg.Privacy.HashKeyFile); err != nil {
			return errors.New("privacy hash key is invalid")
		}
	}
	return nil
}

func (m *Manager) daemonReadable(path string, secret bool) error {
	if !cleanPath(path) {
		return errors.New("unsafe daemon file")
	}
	for _, hidden := range []string{"/home", "/root", "/run/user", "/tmp", "/var/tmp"} {
		if m.sandbox && (path == hidden || within(path, hidden)) {
			return errors.New("asset is hidden by the installed service sandbox; place it under /etc/noderampart")
		}
	}
	parent, err := openDirectory(filepath.Dir(path), false)
	if err != nil {
		return err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("daemon file unavailable")
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Mode&0o022 != 0 || secret && stat.Mode&0o077 != 0 {
		return errors.New("unsafe daemon file")
	}
	readable := m.dac(stat, 4)
	if !readable {
		return errors.New("daemon file is not readable by its service UID/group")
	}
	return m.daemonDirectories(filepath.Dir(path), 1)
}

func (m *Manager) daemonDirectories(path string, finalBits uint32) error {
	for dir := path; dir != "/"; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("daemon file parent unavailable")
		}
		var ds unix.Stat_t
		bits := uint32(1)
		if dir == path {
			bits = finalBits
		}
		if unix.Lstat(dir, &ds) != nil || !m.dac(ds, bits) {
			return errors.New("daemon cannot traverse an asset parent directory")
		}
	}
	return nil
}

func (m *Manager) dac(st unix.Stat_t, bits uint32) bool {
	shift := uint(0)
	if st.Uid == uint32(m.daemonUID) {
		shift = 6
	} else if st.Gid == uint32(m.daemonGID) {
		shift = 3
	}
	return st.Mode>>shift&bits == bits
}

type applyRecord struct {
	Version  int                     `json:"version"`
	Before   []byte                  `json:"before"`
	AfterSHA string                  `json:"after_sha256"`
	Services map[string]serviceState `json:"services"`
}

func (m *Manager) journalPath() string {
	return filepath.Join(filepath.Dir(m.ConfigPath), ".management-apply.json")
}

func (m *Manager) Save(ctx context.Context, snapshot console.Snapshot) (string, error) {
	release, err := m.lock()
	if err != nil {
		return "", err
	}
	defer release()
	return m.saveLocked(ctx, snapshot)
}

func (m *Manager) saveLocked(ctx context.Context, snapshot console.Snapshot) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := m.preflight(snapshot.Config); err != nil {
		return "", err
	}
	if err := ensureDirectory(filepath.Dir(m.ConfigPath), 0o750, m.daemonGID); err != nil {
		return "", err
	}
	if _, err := readFile(m.journalPath(), 2*maxManagedJSON, true, -1); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("an interrupted configuration apply exists; use Recover previous configuration before saving again")
	}
	before, err := readFile(m.ConfigPath, maxManagedJSON, false, -1)
	if errors.Is(err, os.ErrNotExist) && snapshot.Fingerprint == "" {
		before = nil
	} else if err != nil || digest(before) != snapshot.Fingerprint {
		return "", errors.New("configuration changed since this form was opened; reload it before saving")
	}
	data, err := json.MarshalIndent(snapshot.Config, "", "  ")
	if err != nil {
		return "", errors.New("configuration could not be encoded")
	}
	data = append(data, '\n')
	states, err := m.serviceStates(ctx)
	if err != nil {
		return "", err
	}
	record := applyRecord{Version: 1, Before: before, AfterSHA: digest(data), Services: states}
	journal, _ := json.Marshal(record)
	if err := writeFile(m.journalPath(), bytes.NewReader(journal), 2*maxManagedJSON, 0o600, os.Geteuid(), os.Getegid(), false); err != nil {
		return "", err
	}
	// Recheck after preparation, while the management lock excludes other menus
	// and updaters. This detects observed external edits without overwriting them.
	current, currentErr := readFile(m.ConfigPath, maxManagedJSON, false, -1)
	if !(len(before) == 0 && errors.Is(currentErr, os.ErrNotExist)) && (currentErr != nil || digest(current) != digest(before)) {
		_ = removeFile(m.journalPath())
		return "", errors.New("configuration changed during preparation; no configuration was replaced")
	}
	err = writeFile(m.ConfigPath, bytes.NewReader(data), maxManagedJSON, 0o640, os.Geteuid(), m.daemonGID, len(before) > 0)
	if err == nil {
		err = m.activate(ctx, snapshot.Config, states)
	}
	if err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if rollbackErr := m.rollback(rollbackCtx, record); rollbackErr != nil {
			return "", errors.New("activation failed and automatic recovery could not finish; use Recover previous configuration and inspect local service status")
		}
		return "", errors.New("activation failed; previous configuration and service state were restored")
	}
	if err := removeFile(m.journalPath()); err != nil {
		return "", errors.New("configuration activated but recovery-record cleanup failed; inspect local management state")
	}
	if !states["noderampartd.service"].Active {
		return "Configuration saved. Existing stopped/disabled services were preserved. Choose Start services when ready. / 配置已保存；服务保持停止，请按需选择启动。", nil
	}
	return "Configuration saved and active services restarted successfully. / 配置已保存并成功生效。", nil
}

func (m *Manager) rollback(ctx context.Context, record applyRecord) error {
	current, err := readFile(m.ConfigPath, maxManagedJSON, false, -1)
	if err != nil || digest(current) != record.AfterSHA && (len(record.Before) == 0 || digest(current) != digest(record.Before)) {
		return errors.New("current configuration changed; refusing to overwrite it during recovery")
	}
	if len(record.Before) == 0 {
		return errors.New("no previous configuration exists; inspect the saved candidate and service status")
	}
	old, err := decodeConfig(record.Before)
	if err != nil {
		return errors.New("previous configuration is invalid")
	}
	if err := writeFile(m.ConfigPath, bytes.NewReader(record.Before), maxManagedJSON, 0o640, os.Geteuid(), m.daemonGID, true); err != nil {
		return err
	}
	// Repeated recovery must restore services even if a prior attempt already
	// restored config bytes and then failed during service activation.
	old.Sensor.Enabled = record.Services["noderampart-sensor.service"].Active
	for _, name := range []string{"noderampart-sensor.service", "noderampartd.service"} {
		if !record.Services[name].Active {
			if _, err := m.command(ctx, "/usr/bin/systemctl", "stop", name); err != nil {
				return err
			}
		}
	}
	if err := m.activate(ctx, old, record.Services); err != nil {
		return err
	}
	return removeFile(m.journalPath())
}

func (m *Manager) recoverConfig(ctx context.Context) (string, error) {
	data, err := readFile(m.journalPath(), 2*maxManagedJSON, true, -1)
	var record applyRecord
	if err != nil || json.Unmarshal(data, &record) != nil || record.Version != 1 || len(record.AfterSHA) != 64 || len(record.Before) > maxManagedJSON {
		return "", errors.New("no valid interrupted configuration record is available")
	}
	if err := m.rollback(ctx, record); err != nil {
		return "", err
	}
	return "Previous configuration restored. / 已恢复之前的配置。", nil
}

func pretty(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "Result unavailable"
	}
	return string(data)
}

func (m *Manager) serviceAction(ctx context.Context, name string) (string, error) {
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	if name == "service_stop" {
		_, err := m.command(ctx, "/usr/bin/systemctl", "stop", "noderampart-sensor.service", "noderampartd.service")
		if err != nil {
			return "", err
		}
		return "Services stopped; history and configuration retained. / 服务已停止，数据和配置保留。", nil
	}
	if err := m.preflight(snapshot.Config); err != nil {
		return "", err
	}
	states, err := m.serviceStates(ctx)
	if err != nil {
		return "", err
	}
	if name == "service_restart" {
		if err := m.activate(ctx, snapshot.Config, states); err != nil {
			return "", err
		}
		return "Previously active services restarted; stopped and disabled choices preserved. / 已重启原本运行的服务。", nil
	}
	for _, unit := range []string{"noderampartd.service", "noderampart-sensor.service"} {
		if unit == "noderampart-sensor.service" && !snapshot.Config.Sensor.Enabled {
			continue
		}
		if strings.HasPrefix(states[unit].Unit, "masked") {
			return "", fmt.Errorf("%s is masked; unmask it explicitly before starting", unit)
		}
	}
	units := []string{"noderampartd.service"}
	if snapshot.Config.Sensor.Enabled {
		units = append(units, "noderampart-sensor.service")
	}
	if _, err := m.command(ctx, "/usr/bin/systemctl", append([]string{"enable"}, units...)...); err != nil {
		return "", err
	}
	for _, unit := range units {
		if err := m.serviceOperation(ctx, "start", unit); err != nil {
			return "", err
		}
	}
	if !snapshot.Config.Sensor.Enabled {
		if _, err := m.command(ctx, "/usr/bin/systemctl", "stop", "noderampart-sensor.service"); err != nil {
			return "", err
		}
	}
	if err := m.waitReady(ctx, true, snapshot.Config.Sensor.Enabled); err != nil {
		return "", err
	}
	return "Configured services enabled and started. / 已启用并启动配置中的服务。", nil
}
