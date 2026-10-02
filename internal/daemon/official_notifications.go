// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/store"
)

type officialPreparer interface {
	PrepareMessage(*store.OutboxMessage) error
}

func configureOfficialTargets(options *Options) error {
	senders := make(map[string]notify.Sender, 4)
	destinations := make(map[string]string, 4)
	activatedAt := make(map[string]time.Time, 4)
	for _, channel := range config.OfficialChannelNames() {
		c := options.Config.Notifications.OfficialChannels()[channel]
		if err := config.ValidateOfficialChannel(channel, c); err != nil {
			return err
		}
		sender := options.OfficialNotifiers[channel]
		destination := options.OfficialDestinations[channel]
		if identified, ok := sender.(interface{ Destination() string }); ok {
			if destination != "" && destination != identified.Destination() {
				return errors.New("official sender target identity mismatch")
			}
			destination = identified.Destination()
		}
		allowedReference := options.ConfigDirectory != "" && (filepath.Dir(c.CredentialFile) == options.ConfigDirectory || filepath.Dir(c.CredentialFile) == filepath.Join(options.ConfigDirectory, "secrets"))
		if destination == "" && allowedReference && options.OptionalFailures[channel] == "" {
			destination, _ = notify.OfficialDestination(channel, c)
		}
		if destination == "" {
			destination = channel + ":unknown"
		}
		senders[channel], destinations[channel] = sender, destination
		now := time.Now().UTC()
		// Known disabled identities pause their queue. Unknown/changed credentials
		// isolate old bodies through the same target registry as the first batch.
		if err := options.Store.ConfigureNotificationTarget(context.Background(), channel, destination, options.Config.Privacy.NotificationIP, c.Enabled, now); err != nil {
			return err
		}
		consent, _ := time.Parse(time.RFC3339Nano, c.Subscription.ConfirmedAt)
		policy := store.OfficialChannelPolicy{Enabled: c.Enabled, ConsentedAt: consent, Purpose: c.Subscription.Purpose, EvidenceRef: c.Subscription.EvidenceRef, BasisID: c.Subscription.BasisID, NotificationTypes: append([]string(nil), c.Subscription.NotificationTypes...), Revoked: c.Subscription.Revoked, CostConfirmed: c.Subscription.CostConfirmed, DailyMessageLimit: c.DailyMessageLimit, DailySegmentLimit: c.DailySegmentLimit, MaxSegments: c.MaxSegments, PlatformRecoveryConfirmed: c.Subscription.PlatformRecoveryConfirmed}
		if err := options.Store.ConfigureOfficialPolicy(context.Background(), channel, policy, now); err != nil {
			return err
		}
		activated, err := options.Store.NotificationTargetActivatedAt(context.Background(), channel, destination)
		if err != nil {
			return err
		}
		activatedAt[channel] = activated
	}
	options.OfficialNotifiers, options.OfficialDestinations, options.officialActivatedAt = senders, destinations, activatedAt
	return nil
}

func (a *App) startOfficialWorkers(ctx, child context.Context, start func(string, string, bool, func() error)) {
	for _, channel := range config.OfficialChannelNames() {
		c := a.options.Config.Notifications.OfficialChannels()[channel]
		name := channel + "_worker"
		if sender := a.options.OfficialNotifiers[channel]; sender != nil && c.Enabled {
			worker := &notify.Worker{Store: a.options.Store, Sender: sender, Destination: a.options.OfficialDestinations[channel], Logger: a.options.Logger}
			start(name, "running", false, func() error { return worker.Run(child) })
		} else {
			state := "disabled"
			if c.Enabled {
				state = "degraded"
			}
			if err := a.options.Store.SetComponentStatus(ctx, name, state, time.Now().UTC()); err != nil {
				a.options.Logger.Warn("record official notification component", "component", name, "error", err)
			}
		}
	}
}

func prepareOfficialMessage(sender notify.Sender, m *store.OutboxMessage, semantic notify.OfficialSemantic) error {
	data, err := json.Marshal(semantic)
	if err != nil {
		return errors.New("official semantic summary unavailable")
	}
	m.SemanticPayload = string(data)
	preparer, ok := sender.(officialPreparer)
	if !ok {
		return errors.New("official message preparer unavailable")
	}
	return preparer.PrepareMessage(m)
}
