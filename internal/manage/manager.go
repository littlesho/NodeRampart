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
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	lockMu      sync.Mutex
	lockFile    *os.File
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
	daemonUID, err := checkedServiceID(uid)
	if err != nil {
		return nil, errors.New("NodeRampart service UID is invalid")
	}
	group, err := user.LookupGroup("noderampart")
	if err != nil {
		return nil, errors.New("NodeRampart service group is unavailable")
	}
	gid, err := serviceGroupID(group.Gid)
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
	return &Manager{ConfigPath: path, Request: request, Assets: &assets.Client{}, daemonUID: daemonUID, daemonGID: gid,
		lockPath: "/run/noderampart-management.lock", unitDir: "/etc/systemd/system", tmpfilesDir: "/etc/tmpfiles.d", stateDir: "/var/lib/noderampart", runtimeDir: "/run/noderampart", binary: binary, sandbox: true}, nil
}

func serviceGroupID(value string) (int, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, errors.New("invalid service group ID")
	}
	return checkedServiceID(uint32(id))
}

func checkedServiceID(id uint32) (int, error) {
	// Linux chown treats the all-ones UID/GID as "leave unchanged". A service
	// identity must also fit the int accepted by our ownership APIs without
	// becoming negative on a 32-bit build.
	if id == ^uint32(0) || uint64(id) > uint64(^uint(0)>>1) {
		return 0, errors.New("service ID cannot be used for file ownership")
	}
	return int(id), nil
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
	file := os.NewFile(uintptr(fd), "management-lock")
	m.lockMu.Lock()
	m.lockFile = file
	m.lockMu.Unlock()
	return func() {
		m.lockMu.Lock()
		if m.lockFile == file {
			m.lockFile = nil
		}
		// Close, never explicitly unlock: controlled descendants inherit this
		// open-file description and retain exclusion until they actually exit.
		_ = file.Close()
		m.lockMu.Unlock()
	}, nil
}

func (m *Manager) command(ctx context.Context, program string, args ...string) (string, error) {
	return m.runCommand(ctx, false, program, args...)
}

// Removal is cooperative: the installed helper stops dispatching work after a
// cancellation request, but lets an entered package transaction finish with
// its management lock held. Killing the package process group is unsafe.
func (m *Manager) removalCommand(ctx context.Context, program string, args ...string) (string, error) {
	return m.runCommand(ctx, true, program, args...)
}

func (m *Manager) runCommand(ctx context.Context, removal bool, program string, args ...string) (string, error) {
	if m.runner != nil {
		return m.runner(ctx, program, args...)
	}
	work := ctx
	cancel := func() {}
	if !removal {
		work, cancel = context.WithTimeout(ctx, 90*time.Second)
	}
	defer cancel()
	cmd := exec.CommandContext(work, program, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	done := make(chan struct{})
	var cancelOnce sync.Once
	requestGroupCancel := func() {
		cancelOnce.Do(func() {
			select {
			case <-done:
				return
			default:
			}
			_ = unix.Kill(-cmd.Process.Pid, unix.SIGTERM)
			go func() {
				timer := time.NewTimer(250 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-done:
				case <-timer.C:
					_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
				}
			}()
		})
	}
	if removal {
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGUSR1) }
	} else {
		cmd.WaitDelay = 2 * time.Second
		cmd.Cancel = func() error {
			requestGroupCancel()
			return nil
		}
	}
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0", "DEBIAN_FRONTEND=noninteractive"}
	var output limitedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	m.lockMu.Lock()
	if m.lockFile != nil && !removal {
		cmd.ExtraFiles = []*os.File{m.lockFile}
	}
	err := cmd.Start()
	m.lockMu.Unlock()
	stopCancellation := func() bool { return true }
	if err == nil {
		if !removal {
			// os/exec's context watcher can finish when the direct parent exits,
			// while descendants still hold the output pipes. Keep group
			// cancellation active until the complete operation has settled.
			stopCancellation = context.AfterFunc(work, requestGroupCancel)
		}
		err = cmd.Wait()
	}
	stopCancellation()
	close(done)
	if cmd.Process != nil && !removal {
		// A direct child may exit before its descendants or leave an inherited
		// output pipe open. The group belongs solely to this operation.
		_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		if !waitCommandGroup(cmd.Process.Pid, 2*time.Second, 10*time.Millisecond) {
			// An uninterruptible OS operation may not have exited despite
			// SIGKILL. Keep this supervisor and its own lock alive. A deadline
			// requests cancellation; it cannot prove that mutation stopped.
			for !waitCommandGroup(cmd.Process.Pid, 2*time.Second, 200*time.Millisecond) {
			}
		}
	}
	if work.Err() != nil {
		if removal {
			return "", errors.New("cancellation requested; supervised removal finished its current operation; installation state needs inspection / 取消已请求；当前操作已收尾，安装状态需检查")
		}
		return "", work.Err()
	}
	if err != nil {
		return "", errors.New("local service/package operation failed; check service status or the system journal locally")
	}
	return output.String(), nil
}

// Process.Kill only requests termination. Check actual group members before
// claiming cleanup; zombies cannot execute or retain an open lock descriptor.
func waitCommandGroup(group int, timeout, interval time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		if commandGroupStopped(group) {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}

func commandGroupStopped(group int) bool {
	if errors.Is(unix.Kill(-group, 0), unix.ESRCH) {
		return true
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		file, err := os.Open(filepath.Join("/proc", entry.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 4097))
		_ = file.Close()
		if readErr != nil || len(data) > 4096 {
			return false
		}
		end := bytes.LastIndexByte(data, ')')
		if end < 0 {
			return false
		}
		fields := strings.Fields(string(data[end+1:]))
		if len(fields) < 3 {
			return false
		}
		processGroup, err := strconv.Atoi(fields[2])
		if err != nil {
			return false
		}
		if processGroup == group && fields[0] != "Z" && fields[0] != "X" {
			return false
		}
	}
	return true
}

type limitedOutput struct{ buffer bytes.Buffer }

func (b *limitedOutput) String() string { return b.buffer.String() }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if room := (64 << 10) - b.buffer.Len(); room > 0 {
		_, _ = b.buffer.Write(p[:min(room, n)])
	}
	return n, nil
}

type serviceState struct {
	Active bool   `json:"active"`
	State  string `json:"active_state,omitempty"`
	Unit   string `json:"unit_file_state"`
}

func (s serviceState) running() bool { return s.State == "active" || s.State == "" && s.Active }

func stableServices(states map[string]serviceState) error {
	for _, name := range []string{"noderampartd.service", "noderampart-sensor.service"} {
		state := states[name]
		switch state.Unit {
		case "enabled", "enabled-runtime", "disabled", "masked", "masked-runtime", "static", "indirect", "generated", "transient", "alias", "linked", "linked-runtime":
		default:
			return fmt.Errorf("%s has an unknown unit file state; inspect it before changing configuration or restarting", name)
		}
		if state.running() && strings.HasPrefix(state.Unit, "masked") {
			return fmt.Errorf("%s is active and masked; unmask it explicitly before changing configuration or restarting", name)
		}
		switch state.State {
		case "", "active", "inactive", "failed":
		default:
			return fmt.Errorf("%s is %s; wait for the transition to finish before changing configuration or restarting", name, state.State)
		}
	}
	return nil
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
		state := strings.TrimSpace(active)
		switch state {
		case "active", "inactive", "failed", "activating", "deactivating", "reloading", "refreshing", "maintenance":
		default:
			return nil, fmt.Errorf("%s has an unknown service state; inspect it before changing services", name)
		}
		states[name] = serviceState{Active: state == "active", State: state, Unit: strings.TrimSpace(unit)}
	}
	return states, nil
}

func (m *Manager) activate(ctx context.Context, cfg config.Config, previous map[string]serviceState) error {
	return m.activateServices(ctx, cfg, previous, cfg.Sensor.Enabled)
}

func (m *Manager) activateServices(ctx context.Context, cfg config.Config, previous map[string]serviceState, sensorEnabled bool) error {
	if err := stableServices(previous); err != nil {
		return err
	}
	for _, name := range []string{"noderampartd.service", "noderampart-sensor.service"} {
		if previous[name].running() && strings.HasPrefix(previous[name].Unit, "masked") {
			return errors.New("an active service is masked; unmask it explicitly before restarting")
		}
	}
	if previous["noderampart-sensor.service"].running() {
		if _, err := m.command(ctx, "/usr/bin/systemctl", "stop", "noderampart-sensor.service"); err != nil {
			return err
		}
	}
	if previous["noderampartd.service"].running() {
		if strings.HasPrefix(previous["noderampartd.service"].Unit, "masked") {
			return errors.New("daemon is masked; unmask it explicitly before applying to the running service")
		}
		if err := m.serviceOperation(ctx, "restart", "noderampartd.service"); err != nil {
			return err
		}
	}
	if sensorEnabled && previous["noderampart-sensor.service"].running() && !strings.HasPrefix(previous["noderampart-sensor.service"].Unit, "masked") {
		if err := m.serviceOperation(ctx, "start", "noderampart-sensor.service"); err != nil {
			return err
		}
	}
	return m.waitReadyConfig(ctx, previous["noderampartd.service"].running(), sensorEnabled && previous["noderampart-sensor.service"].running(), cfg)
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
	snapshot, err := m.Load(ctx)
	if err != nil {
		return err
	}
	return m.waitReadyConfig(ctx, daemon, sensor, snapshot.Config)
}

func (m *Manager) waitReadyConfig(ctx context.Context, daemon, sensor bool, cfg config.Config) error {
	if daemon || sensor {
		timeout := 8 * time.Second
		if sensor {
			timeout = max(timeout, 2*cfg.Sensor.BatchInterval.Duration+5*time.Second)
		}
		deadline, cancel := context.WithTimeout(ctx, timeout)
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
					data, err := m.Request(deadline, "status", struct{}{})
					var status struct {
						Readiness struct {
							ConfigurationLoaded bool   `json:"configuration_loaded"`
							ConfigFingerprint   string `json:"config_fingerprint"`
							StorageReady        bool   `json:"storage_ready"`
							InterfaceReady      bool   `json:"interface_ready"`
							SensorReady         bool   `json:"sensor_ready"`
							BasicReady          bool   `json:"basic_ready"`
						} `json:"readiness"`
					}
					ready = err == nil && json.Unmarshal(data, &status) == nil && status.Readiness.ConfigurationLoaded &&
						status.Readiness.ConfigFingerprint == config.Fingerprint(cfg) && status.Readiness.StorageReady &&
						status.Readiness.InterfaceReady && (!sensor || status.Readiness.SensorReady) && status.Readiness.BasicReady
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
	for _, channel := range config.NativeChannelNames() {
		n := cfg.Notifications.NativeChannels()[channel]
		if !n.Enabled && (!m.sandbox || n.CredentialFile == "") {
			continue
		}
		credentialDir := filepath.Dir(n.CredentialFile)
		managedDir := filepath.Dir(m.ConfigPath)
		if credentialDir != managedDir && credentialDir != filepath.Join(managedDir, "secrets") {
			return errors.New(channel + " credentials must remain in the managed configuration or secrets directory / 凭据必须位于托管配置或 secrets 目录")
		}
		if !n.Enabled {
			continue
		}
		if m.daemonReadable(n.CredentialFile, false) != nil {
			return errors.New(channel + " credentials must be protected and readable by the daemon service identity / 凭据须受保护且守护进程服务身份可读")
		}
		if _, err := notify.NewNative(channel, n); err != nil {
			return errors.New(channel + " credentials or official webhook target are invalid / 凭据或官方 Webhook 目标无效")
		}
	}
	for _, channel := range config.OfficialChannelNames() {
		c := cfg.Notifications.OfficialChannels()[channel]
		if !c.Enabled && (!m.sandbox || c.CredentialFile == "") {
			continue
		}
		managedDir := filepath.Dir(m.ConfigPath)
		credentialDir := filepath.Dir(c.CredentialFile)
		if credentialDir != managedDir && credentialDir != filepath.Join(managedDir, "secrets") {
			return errors.New("official credentials must remain in the managed configuration directory / 官方渠道凭据须位于托管配置目录")
		}
		if !c.Enabled {
			continue
		}
		credential, err := m.readOfficialCredential(channel, c.CredentialFile)
		if err != nil {
			return err
		}
		if err := config.ValidateOfficialCredentialPolicy(channel, c, credential); err != nil {
			return err
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
	Version     int                     `json:"version"`
	Before      []byte                  `json:"before"`
	BeforeSHA   string                  `json:"before_sha256,omitempty"`
	AfterSHA    string                  `json:"after_sha256"`
	Services    map[string]serviceState `json:"services"`
	Stage       string                  `json:"stage,omitempty"`
	StartedAt   time.Time               `json:"started_at_utc,omitempty"`
	UpdatedAt   time.Time               `json:"updated_at_utc,omitempty"`
	FailureCode string                  `json:"failure_code,omitempty"`
}

func (m *Manager) recordApply(record *applyRecord, stage, failure string) error {
	record.Stage, record.FailureCode, record.UpdatedAt = stage, failure, time.Now().UTC()
	journal, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeFile(m.journalPath(), bytes.NewReader(journal), 2*maxManagedJSON, 0o600, os.Geteuid(), os.Getegid(), record.Stage != "prepared")
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
	current, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	for _, channel := range config.OfficialChannelNames() {
		if !reflect.DeepEqual(current.Config.Notifications.OfficialChannels()[channel].Subscription, snapshot.Config.Notifications.OfficialChannels()[channel].Subscription) {
			return "", errors.New("change subscriptions through explicit consent or revoke actions / 请通过明确同意或撤销操作修改订阅")
		}
	}
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
	if err := stableServices(states); err != nil {
		return "", err
	}
	record := applyRecord{Version: 2, Before: before, BeforeSHA: digest(before), AfterSHA: digest(data), Services: states, StartedAt: time.Now().UTC()}
	if err := m.recordApply(&record, "prepared", ""); err != nil {
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
		err = m.recordApply(&record, "candidate_written", "")
	}
	if err == nil {
		err = m.recordApply(&record, "activating", "")
	}
	if err == nil {
		err = m.activate(ctx, snapshot.Config, states)
	}
	if err != nil {
		_ = m.recordApply(&record, "failed", "apply_failed")
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
	for _, state := range states {
		if state.State == "failed" {
			return "Configuration saved; a previously failed service remains failed. Inspect service status before explicitly starting it. / 配置已保存；此前失败的服务仍保持失败，请检查服务状态后再明确启动。", nil
		}
	}
	if !states["noderampartd.service"].running() {
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
	if err := stableServices(record.Services); err != nil {
		return err
	}
	if err := m.preflight(old); err != nil {
		return errors.New("previous configuration dependencies are unavailable; restore them before recovery")
	}
	if err := m.recordApply(&record, "recovering", ""); err != nil {
		return err
	}
	if err := writeFile(m.ConfigPath, bytes.NewReader(record.Before), maxManagedJSON, 0o640, os.Geteuid(), m.daemonGID, true); err != nil {
		_ = m.recordApply(&record, "failed", "config_restore_failed")
		return err
	}
	if err := m.recordApply(&record, "config_restored", ""); err != nil {
		return err
	}
	// Repeated recovery must restore services even if a prior attempt already
	// restored config bytes and then failed during service activation.
	for _, name := range []string{"noderampart-sensor.service", "noderampartd.service"} {
		if !record.Services[name].running() {
			if _, err := m.command(ctx, "/usr/bin/systemctl", "stop", name); err != nil {
				_ = m.recordApply(&record, "failed", "service_restore_failed")
				return err
			}
		}
	}
	if err := m.activateServices(ctx, old, record.Services, record.Services["noderampart-sensor.service"].running()); err != nil {
		_ = m.recordApply(&record, "failed", "service_restore_failed")
		return err
	}
	if err := m.recordApply(&record, "services_restored", ""); err != nil {
		return err
	}
	return removeFile(m.journalPath())
}

func (m *Manager) recoverConfig(ctx context.Context) (string, error) {
	data, err := readFile(m.journalPath(), 2*maxManagedJSON, true, -1)
	var record applyRecord
	if err != nil || json.Unmarshal(data, &record) != nil || record.Version != 1 && record.Version != 2 || len(record.AfterSHA) != 64 || len(record.Before) > maxManagedJSON || len(record.Services) != 2 {
		return "", errors.New("no valid interrupted configuration record is available")
	}
	for _, unit := range []string{"noderampartd.service", "noderampart-sensor.service"} {
		state, ok := record.Services[unit]
		if !ok {
			return "", errors.New("interrupted configuration record has unknown service targets")
		}
		if state.State != "" && state.Active != (state.State == "active") {
			return "", errors.New("interrupted configuration record service state is inconsistent")
		}
	}
	if record.Version == 2 && (record.BeforeSHA != digest(record.Before) || record.StartedAt.IsZero() || record.UpdatedAt.Before(record.StartedAt)) {
		return "", errors.New("interrupted configuration record fingerprints or timestamps are invalid")
	}
	if record.Version == 2 {
		switch record.Stage {
		case "prepared", "candidate_written", "activating", "recovering", "config_restored", "services_restored", "failed":
		default:
			return "", errors.New("interrupted configuration record stage is unsupported")
		}
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
	if name == "service_stop" {
		_, err := m.command(ctx, "/usr/bin/systemctl", "stop", "noderampart-sensor.service", "noderampartd.service")
		if err != nil {
			return "", err
		}
		return "Services stopped; history and configuration retained. / 服务已停止，数据和配置保留。", nil
	}
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	if err := m.preflight(snapshot.Config); err != nil {
		return "", err
	}
	states, err := m.serviceStates(ctx)
	if err != nil {
		return "", err
	}
	if err := stableServices(states); err != nil {
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
	if err := m.waitReadyConfig(ctx, true, snapshot.Config.Sensor.Enabled, snapshot.Config); err != nil {
		return "", err
	}
	return "Configured services enabled and started. / 已启用并启动配置中的服务。", nil
}
