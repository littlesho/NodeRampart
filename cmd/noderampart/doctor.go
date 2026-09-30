// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
	"github.com/littlesho/NodeRampart/internal/version"
)

type doctorCheck struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Detail     string `json:"detail"`
	ReasonCode string `json:"reason_code"`
	Impact     string `json:"impact"`
	NextStep   string `json:"next_step"`
}

type doctorResult struct {
	Overall             string                  `json:"overall"`
	StrictExitCode      int                     `json:"strict_exit_code"`
	Version             version.Info            `json:"cli_version"`
	Checks              []doctorCheck           `json:"checks"`
	SnapshotForeignKeys *store.ForeignKeyStatus `json:"snapshot_foreign_keys,omitempty"`
	Daemon              any                     `json:"daemon,omitempty"`
}

type diagnosticExit struct{ code int }

func (e *diagnosticExit) Error() string {
	return "strict diagnostics did not confirm all required checks"
}

func doctorCommand(arguments []string, output io.Writer) error {
	flags := quietFlags("doctor")
	path := flags.String("config", defaultConfig, "configuration file")
	manual := flags.Bool("manual-current-uid", false, "explicitly trust a daemon running as the current UID for a manual lab")
	snapshot := flags.String("foreign-keys-snapshot", "", "read-only orphan check on an explicit standalone private database copy; never migrates or repairs")
	strict := flags.Bool("strict", false, "0 healthy, 1 confirmed degradation, 2 unable to reliably diagnose")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	result := doctorResult{Overall: "healthy", Version: version.Current(), Checks: []doctorCheck{}}
	if *snapshot != "" {
		if !cleanLocalPath(*snapshot) {
			return fmt.Errorf("foreign-key snapshot requires a clean absolute path")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		foreignKeys, err := store.InspectForeignKeysSnapshot(ctx, *snapshot)
		cancel()
		state, detail := "valid", "Standalone snapshot has no detected foreign-key violations; this does not establish live monitoring health."
		if err != nil {
			state, detail = "unavailable", "Standalone historical relationships could not be inspected safely; retain all original evidence."
		} else {
			result.SnapshotForeignKeys = &foreignKeys
			if len(foreignKeys.Violations) > 0 || foreignKeys.Truncated {
				state, detail = "degraded", "Historical orphan relationships detected; preserve private evidence and follow the bounded manual procedure. No data was altered."
			}
		}
		result.Checks = append(result.Checks, doctorCheck{Name: "snapshot_foreign_keys", State: state, Detail: detail})
	}
	cfg, err := config.Load(*path)
	if err != nil {
		result.Checks = append(result.Checks, doctorCheck{Name: "configuration", State: "unavailable", Detail: "Configuration is missing, unreadable, unsafe, or invalid; diagnostics use standard paths."})
		cfg = config.Defaults()
	} else {
		result.Checks = append(result.Checks, doctorCheck{Name: "configuration", State: "valid", Detail: "Configuration passed local validation."})
	}
	units := inspectUnits("/")
	for i := range units {
		if *manual || units[i].Name == "noderampart-sensor.service" && !cfg.Sensor.Enabled {
			units[i].State, units[i].Detail = "not_applicable", "Manual deployment or disabled sensor; a packaged system unit is not required."
		}
	}
	result.Checks = append(result.Checks, units...)
	for _, item := range []struct{ name, path string }{{"database", cfg.Paths.Database}, {"database_wal", cfg.Paths.Database + "-wal"}} {
		info, err := os.Lstat(item.path)
		state, detail := "unavailable", "File is missing or cannot be inspected."
		if item.name == "database_wal" && os.IsNotExist(err) {
			state, detail = "not_applicable", "An absent WAL sidecar is valid for the production rollback-journal configuration."
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				state, detail = "unsafe", "Expected a regular file without a symlink."
			} else {
				state, detail = "present", fmt.Sprintf("Regular file present (%d bytes); daemon status provides live storage health.", info.Size())
			}
		}
		result.Checks = append(result.Checks, doctorCheck{Name: item.name, State: state, Detail: detail})
	}
	uid, err := expectedDaemonUID(*manual)
	if err != nil {
		result.Checks = append(result.Checks, doctorCheck{Name: "daemon_identity", State: "unavailable", Detail: "Required daemon service account is unavailable."})
	} else {
		response, err := send(cfg.Paths.ControlSocket, api.Request{Version: api.Version, Command: "doctor"}, uid)
		if err != nil || !response.OK {
			result.Checks = append(result.Checks, doctorCheck{Name: "daemon", State: "unavailable", Detail: "Daemon is stopped, inaccessible, incompatible, or failed peer verification."})
		} else {
			result.Daemon = response.Data
			encoded, _ := json.Marshal(response.Data)
			var daemon struct {
				Version version.Info `json:"version"`
			}
			if json.Unmarshal(encoded, &daemon) == nil && daemon.Version.Version != "" && (daemon.Version.Version != version.Version || daemon.Version.Commit != version.Commit) {
				result.Checks = append(result.Checks, doctorCheck{Name: "binary_versions", State: "mismatch", Detail: "CLI and daemon build versions differ; check installation consistency."})
			}
			result.Checks = append(result.Checks, doctorCheck{Name: "daemon", State: "reachable", Detail: "Daemon identity verified; collection and persistence status are shown below."})
			appendRuntimeDiagnosis(&result, encoded, cfg)
		}
	}
	finalizeDoctor(&result)
	if err := printJSON(output, result); err != nil {
		return err
	}
	if *strict && result.StrictExitCode != 0 {
		return &diagnosticExit{result.StrictExitCode}
	}
	return nil
}

func inspectUnits(root string) []doctorCheck {
	checks := []doctorCheck{}
	for _, name := range []string{"noderampartd.service", "noderampart-sensor.service"} {
		local := unitInstallKind(filepath.Join(root, "etc/systemd/system", name))
		vendor := unitInstallKind(filepath.Join(root, "usr/lib/systemd/system", name))
		if vendor == "missing" {
			vendor = unitInstallKind(filepath.Join(root, "lib/systemd/system", name))
		}
		state, detail := "present", "Unit location inspected."
		if local == "missing" && vendor == "missing" {
			state, detail = "missing", "No system unit found in standard locations."
		} else if local == "source" && vendor == "package" {
			state, detail = "conflict", "A source installation unit in /etc overrides the packaged unit."
		} else if local == "unreadable" || vendor == "unreadable" {
			state, detail = "unavailable", "A unit could not be safely inspected."
		} else if local != "missing" && vendor != "missing" {
			state, detail = "override", "A local unit overrides a vendor unit; inspect installation consistency."
		}
		checks = append(checks, doctorCheck{Name: name, State: state, Detail: detail})
	}
	return checks
}

func unitInstallKind(path string) string {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "missing"
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return "unreadable"
	}
	file, err := os.Open(path)
	if err != nil {
		return "unreadable"
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return "unreadable"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ExecStart=/usr/local/") {
			return "source"
		}
		if strings.HasPrefix(line, "ExecStart=/usr/bin/noderampart") {
			return "package"
		}
	}
	return "other"
}
