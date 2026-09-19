// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
)

func offlineBackupCommand(arguments []string, name string, output io.Writer) error {
	flags := quietFlags(name)
	path := flags.String("config", defaultConfig, "configuration file")
	input := flags.String("input", "", "backup path")
	var target string
	if name == "backup_restore" {
		flags.StringVar(&target, "output", "", "new restored database path; existing databases are never replaced")
	}
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	if !cleanLocalPath(*input) || (name == "backup_restore" && !cleanLocalPath(target)) {
		return errors.New("input and restore output must be clean absolute paths")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if name == "backup_verify" {
		info, err := store.VerifyBackup(ctx, *input)
		if err != nil {
			return errors.New("backup verification failed")
		}
		return printJSON(output, info)
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return errors.New("valid configuration is required to check the stopped daemon before restore")
	}
	if err := requireDaemonStopped(cfg.Paths.ControlSocket); err != nil {
		return err
	}
	info, err := store.RestoreBackup(ctx, *input, target)
	if err != nil {
		return errors.New("backup restore failed; output must be a new database path")
	}
	return printJSON(output, map[string]any{"backup": info, "next_step": "Configure the daemon to use this verified database before starting it; the previous database was preserved."})
}

func requireDaemonStopped(socket string) error {
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		return errors.New("stop the daemon and ensure its control socket is absent before restoring")
	}
	return nil
}
