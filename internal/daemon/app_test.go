// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/enrich"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/privacy"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestAuthAndNetworkWiring(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	geo, err := enrich.Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer geo.Close()
	transformer, err := privacy.New("prefix", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Auth.Threshold = 3
	cfg.Auth.Cooldown = config.Duration{}
	cfg.Detection.SYNPacketsPerSecond = 100
	app, err := New(Options{Config: cfg, Store: database, Geo: geo, StorePrivacy: transformer, NotifyPrivacy: transformer, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		app.handleAuth(ctx, collector.AuthObservation{ObservedAt: now.Add(time.Duration(i) * time.Second), Kind: collector.AuthFailure, SourceIP: netip.MustParseAddr("203.0.113.9"), User: "root", Method: "password"})
	}
	app.handleBatch(ctx, protocol.Batch{
		ProtocolVersion: protocol.Version,
		SentAt:          now.Add(4 * time.Second),
		IntervalMillis:  1000,
		Interface:       "eth0",
		RXBytes:         7200,
		RXPackets:       120,
		InboundSYN:      120,
		Flows: []protocol.Flow{{
			Direction: model.DirectionInbound,
			RemoteIP:  "203.0.113.7",
			Protocol:  "tcp",
			LocalPort: 22,
			TCPFlags:  0x02,
			Packets:   120,
			Bytes:     7200,
		}},
	})
	summary, err := database.Summary(ctx, now.Add(-time.Minute), now.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Auth) != 1 || summary.Auth[0].Count != 3 {
		t.Fatalf("unexpected auth aggregation: %#v", summary.Auth)
	}
	if len(summary.Events) != 2 || len(summary.Traffic) != 1 || summary.Batches != 1 {
		t.Fatalf("unexpected daemon summary: %#v", summary)
	}
	if len(summary.Components) != 1 || summary.Components[0].Name != "sensor_feed" || summary.Components[0].State != "running" {
		t.Fatalf("unexpected component coverage: %#v", summary.Components)
	}
}
