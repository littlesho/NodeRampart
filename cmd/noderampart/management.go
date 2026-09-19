// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/console"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/manage"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"golang.org/x/sys/unix"
)

func stdinTerminal() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
	return err == nil
}

func managementCommand(name string, arguments []string) error {
	flags := quietFlags(name)
	path := flags.String("config", defaultConfig, "installed configuration file")
	var language string
	if name != "assets" {
		flags.StringVar(&language, "language", "", "en or zh (default: locale)")
	}
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	if language == "" {
		language = os.Getenv("LC_ALL")
		if language == "" {
			language = os.Getenv("LANG")
		}
	}
	manager, err := manage.New(*path, managementRequest(*path))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if name == "assets" {
		result, err := manager.Action(ctx, "geo_refresh", nil)
		if result != "" {
			fmt.Fprintln(os.Stdout, result)
		}
		return err
	}
	return console.Run(ctx, manager, console.Options{Setup: name == "setup", Language: language})
}

func managementRequest(path string) manage.RequestFunc {
	return func(ctx context.Context, command string, args any) (json.RawMessage, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cfg, err := config.Load(path)
		if err != nil {
			return nil, errors.New("configuration is unavailable or invalid")
		}
		uid, err := ipc.ServiceUID(ipc.DaemonUser)
		if err != nil {
			return nil, errors.New("installed daemon identity is unavailable")
		}
		timeout := 15 * time.Second
		if command == "backup_create" {
			timeout = 2*time.Minute + 5*time.Second
		}
		deadline := time.Now().Add(timeout)
		if outer, ok := ctx.Deadline(); ok && outer.Before(deadline) {
			deadline = outer
		}
		connection, err := ipc.DialUnixPeer(cfg.Paths.ControlSocket, min(3*time.Second, time.Until(deadline)), uid)
		if err != nil {
			return nil, errors.New("authenticated daemon connection is unavailable")
		}
		defer connection.Close()
		cancelClose := context.AfterFunc(ctx, func() { _ = connection.Close() })
		defer cancelClose()
		_ = connection.SetDeadline(deadline)
		encoded, err := json.Marshal(args)
		if err != nil {
			return nil, errors.New("invalid daemon request arguments")
		}
		if err := protocol.WriteFrame(connection, api.Request{Version: api.Version, Command: command, Args: encoded}); err != nil {
			return nil, errors.New("could not send daemon request")
		}
		response, err := readResponse(bufio.NewReader(connection))
		if err != nil {
			return nil, err
		}
		data, ok := response.Data.(json.RawMessage)
		if !ok && response.Data != nil {
			return nil, errors.New("daemon result is invalid")
		}
		if !response.OK {
			return data, errors.New(response.Error)
		}
		return data, nil
	}
}
