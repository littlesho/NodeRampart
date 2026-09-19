// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/version"
)

type doctorCheck struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type doctorResult struct {
	Version version.Info  `json:"cli_version"`
	Checks  []doctorCheck `json:"checks"`
	Daemon  any           `json:"daemon,omitempty"`
}

func doctorCommand(arguments []string, output io.Writer) error {
	flags := quietFlags("doctor")
	path := flags.String("config", defaultConfig, "configuration file")
	manual := flags.Bool("manual-current-uid", false, "explicitly trust a daemon running as the current UID for a manual lab")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	result := doctorResult{Version: version.Current(), Checks: []doctorCheck{}}
	cfg, err := config.Load(*path)
	if err != nil {
		result.Checks = append(result.Checks, doctorCheck{"configuration", "unavailable", "Configuration is missing, unreadable, unsafe, or invalid; diagnostics use standard paths."})
		cfg = config.Defaults()
	} else {
		result.Checks = append(result.Checks, doctorCheck{"configuration", "valid", "Configuration passed local validation."})
	}
	result.Checks = append(result.Checks, inspectUnits("/")...)
	for _, item := range []struct{ name, path string }{{"database", cfg.Paths.Database}, {"database_wal", cfg.Paths.Database + "-wal"}} {
		info, err := os.Lstat(item.path)
		state, detail := "unavailable", "File is missing or cannot be inspected."
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				state, detail = "unsafe", "Expected a regular file without a symlink."
			} else {
				state, detail = "present", fmt.Sprintf("Regular file present (%d bytes); daemon status provides live storage health.", info.Size())
			}
		}
		result.Checks = append(result.Checks, doctorCheck{item.name, state, detail})
	}
	uid, err := expectedDaemonUID(*manual)
	if err != nil {
		result.Checks = append(result.Checks, doctorCheck{"daemon_identity", "unavailable", "Required daemon service account is unavailable."})
	} else {
		response, err := send(cfg.Paths.ControlSocket, api.Request{Version: api.Version, Command: "doctor"}, uid)
		if err != nil || !response.OK {
			result.Checks = append(result.Checks, doctorCheck{"daemon", "unavailable", "Daemon is stopped, inaccessible, incompatible, or failed peer verification."})
		} else {
			result.Daemon = response.Data
			encoded, _ := json.Marshal(response.Data)
			var daemon struct {
				Version version.Info `json:"version"`
			}
			if json.Unmarshal(encoded, &daemon) == nil && daemon.Version.Version != "" && (daemon.Version.Version != version.Version || daemon.Version.Commit != version.Commit) {
				result.Checks = append(result.Checks, doctorCheck{"binary_versions", "mismatch", "CLI and daemon build versions differ; check installation consistency."})
			}
			result.Checks = append(result.Checks, doctorCheck{"daemon", "reachable", "Daemon identity verified; collection and persistence status are shown below."})
		}
	}
	return printJSON(output, result)
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
		checks = append(checks, doctorCheck{name, state, detail})
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
