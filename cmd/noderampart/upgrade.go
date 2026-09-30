// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/littlesho/NodeRampart/internal/manage"
)

func upgradeCommand(arguments []string, output io.Writer) error {
	if len(arguments) == 0 || arguments[0] != "preflight" && arguments[0] != "rehearse" {
		return errors.New("usage: upgrade preflight|rehearse --config FILE --backup FILE --directory DIR [--target-package FILE] [--strict]")
	}
	flags := quietFlags("upgrade")
	options := manage.UpgradeOptions{}
	flags.StringVar(&options.ConfigPath, "config", defaultConfig, "validated configuration; credentials remain outside the backup")
	flags.StringVar(&options.BackupPath, "backup", "", "explicit verified standalone backup")
	flags.StringVar(&options.Directory, "directory", "", "existing trusted private isolation directory")
	flags.StringVar(&options.TargetPackage, "target-package", "", "optional local DEB/RPM metadata, never executed")
	strict := flags.Bool("strict", false, "0 passed required checks, 1 confirmed failure, 2 unknown target or diagnostics")
	if err := parseFlags(flags, arguments[1:]); err != nil {
		return err
	}
	if options.BackupPath == "" || options.Directory == "" {
		return errors.New("upgrade checks require explicit backup and isolation directory")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if arguments[0] == "rehearse" {
		result, err := manage.RehearseRestore(ctx, options)
		if outputErr := printJSON(output, result); outputErr != nil {
			return outputErr
		}
		if err != nil {
			return err
		}
		if *strict && result.Preflight.Overall != "pass" {
			code := 2
			if result.Preflight.Overall == "fail" {
				code = 1
			}
			return &diagnosticExit{code: code}
		}
		return nil
	}
	result, err := manage.UpgradePreflight(ctx, options)
	if outputErr := printJSON(output, result); outputErr != nil {
		return outputErr
	}
	if err != nil {
		if *strict {
			return &diagnosticExit{code: 2}
		}
		return err
	}
	if *strict && result.Overall != "pass" {
		code := 2
		if result.Overall == "fail" {
			code = 1
		}
		return &diagnosticExit{code: code}
	}
	return nil
}
