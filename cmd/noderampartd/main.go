// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/daemon"
	"github.com/littlesho/NodeRampart/internal/enrich"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/privacy"
	"github.com/littlesho/NodeRampart/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "noderampartd:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("noderampartd", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/noderampart/config.json", "configuration file")
	manualCurrentUID := flags.Bool("manual-current-uid", false, "explicit manual lab mode: authorize this process UID as the sensor")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	absoluteConfig, err := filepath.Abs(*configPath)
	if err != nil {
		return errors.New("configuration directory is unavailable")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	database, err := store.OpenWithBudget(cfg.Paths.Database, store.BudgetConfig{MaxBytes: cfg.Storage.MaxBytes, MinFreeBytes: cfg.Storage.MinFreeBytes})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()
	var sensorUID *uint32
	if cfg.Sensor.Enabled {
		var uid uint32
		if *manualCurrentUID {
			uid = uint32(os.Geteuid())
		} else {
			uid, err = ipc.ServiceUID("noderampart-sensor")
			if err != nil {
				return fmt.Errorf("resolve sensor service identity: %w", err)
			}
		}
		sensorUID = &uid
	}
	geo, err := enrich.Open(cfg.Geo.CityMMDB, cfg.Geo.ASNMMDB)
	if err != nil {
		return err
	}
	defer geo.Close()
	storePrivacy, err := privacy.New(cfg.Privacy.StoreIP, cfg.Privacy.HashKeyFile)
	if err != nil {
		return err
	}
	notifyPrivacy, err := privacy.New(cfg.Privacy.NotificationIP, cfg.Privacy.HashKeyFile)
	if err != nil {
		return err
	}
	var sender notify.Sender
	if cfg.Notifications.Telegram.Enabled {
		sender, err = notify.NewTelegram(cfg.Notifications.Telegram.TokenFile, cfg.Notifications.Telegram.ChatID, cfg.Notifications.Telegram.Timeout.Duration)
		if err != nil {
			return err
		}
	}
	var priceProfile *billing.Profile
	if cfg.Billing.Enabled {
		priceProfile, err = billing.Load(cfg.Billing.ProfilePath)
		if err != nil {
			return fmt.Errorf("load billing profile: %w", err)
		}
	}
	app, err := daemon.New(daemon.Options{Config: cfg, Store: database, Geo: geo, Notifier: sender, Billing: priceProfile, StorePrivacy: storePrivacy, NotifyPrivacy: notifyPrivacy, Logger: logger, SensorUID: sensorUID, AssetHealthPath: filepath.Join(filepath.Dir(absoluteConfig), "geoip-health.json")})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx)
}
