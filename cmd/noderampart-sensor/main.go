// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/sensor"
	"github.com/littlesho/NodeRampart/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "noderampart-sensor:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("noderampart-sensor", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/noderampart/config.json", "configuration file")
	manual := flags.Bool("manual-current-uid", false, "explicitly trust a daemon running as the current UID for a manual lab")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if !cfg.Sensor.Enabled {
		return nil
	}
	expectedUID := uint32(os.Geteuid())
	if !*manual {
		expectedUID, err = ipc.ServiceUID(ipc.DaemonUser)
		if err != nil {
			return err
		}
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	sender := sensor.NewBatchSender(cfg.Paths.SensorSocket, expectedUID)
	defer sender.Close()
	logger.Info("NodeRampart sensor started", "version", version.Version, "interface_limit", cfg.Sensor.InterfaceLimit())
	fleet := &sensor.Fleet{Interfaces: cfg.Sensor.InterfaceNames(), Limit: cfg.Sensor.InterfaceLimit(),
		MaxFlows: cfg.Sensor.MaxTrackedFlows, ReceiveBuffer: cfg.Sensor.ReceiveBufferBytes,
		BatchInterval: cfg.Sensor.BatchInterval.Duration, Sender: sender, Logger: logger}
	return fleet.Run(ctx)

}
