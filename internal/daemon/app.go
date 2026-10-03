// SPDX-License-Identifier: MIT

package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/detect"
	"github.com/littlesho/NodeRampart/internal/enrich"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/privacy"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/report"
	"github.com/littlesho/NodeRampart/internal/store"
	"github.com/littlesho/NodeRampart/internal/timezones"
	"github.com/littlesho/NodeRampart/internal/version"
)

type Options struct {
	Config                  config.Config
	ConfigDirectory         string
	nativeActivatedAt       map[string]time.Time
	officialActivatedAt     map[string]time.Time
	Store                   *store.Store
	Geo                     *enrich.Resolver
	Notifier                notify.Sender
	NotificationDestination string
	WebhookNotifier         notify.Sender
	WebhookDestination      string
	NativeNotifiers         map[string]notify.Sender
	NativeDestinations      map[string]string
	OfficialNotifiers       map[string]notify.Sender
	OfficialDestinations    map[string]string
	HeartbeatSender         notify.HeartbeatSender
	Billing                 *billing.Profile
	StorePrivacy            *privacy.Transformer
	NotifyPrivacy           *privacy.Transformer
	Logger                  *slog.Logger
	SensorUID               *uint32
	AssetHealthPath         string
	OptionalFailures        map[string]string
}

type App struct {
	options                    Options
	network                    networkDetector
	auth                       *detect.Auth
	report                     *report.Builder
	started                    time.Time
	mu                         sync.RWMutex
	lastSensor                 time.Time
	sensorState                string
	sensorStateGateOnce        sync.Once
	sensorStateGate            chan struct{}
	sensorPeer                 ipc.Peer
	batches                    uint64
	storageHealth              StorageHealth
	storageFailures            map[string]bool
	journalStatus              collector.JournalStatus
	pendingJournal             *store.JournalWrite
	authStats                  detect.AuthStats
	authCoverageState          string
	sensorPrepared             map[string]*sensorPreparedEvents
	pendingEvents              []queuedEvent
	pendingEventBytes          int
	eventLosses                [3]eventLoss
	eventIngest                EventIngestStatus
	sensorConnections          atomic.Uint64
	interfaces                 []collector.InterfaceObservation
	sensorByInterface          map[string]time.Time
	sensorReceipts             map[string]SensorReceipt
	sensorCommittedByInterface map[string]time.Time
	interfaceDiscoveryRequired bool
	discoveryDegraded          bool
	latestDiscoveryDegraded    bool
	interfacesObserved         bool
	interfaceSelectionAt       time.Time
	interfaceGap               *store.CoverageGap
	interfaceCounterState      string
	interfaceCounterAt         time.Time
	monitorMu                  sync.RWMutex
	monitorStatus              MonitorStatus
}

type networkDetector interface {
	Observe(protocol.Batch) []model.Event
	Stats() detect.NetworkStats
}

type Status struct {
	GeneratedAt          time.Time                        `json:"generated_at_utc"`
	Diagnosis            *Diagnosis                       `json:"diagnosis,omitempty"`
	ForeignKeys          *store.ForeignKeyStatus          `json:"foreign_keys,omitempty"`
	SensorEnabled        bool                             `json:"sensor_enabled"`
	AuthEnabled          bool                             `json:"auth_enabled"`
	Readiness            ReadinessStatus                  `json:"readiness"`
	OptionalFailures     map[string]string                `json:"optional_failures"`
	LastInterfaceCommit  time.Time                        `json:"last_interface_commit_utc"`
	Monitoring           MonitorStatus                    `json:"monitoring"`
	Interfaces           []collector.InterfaceObservation `json:"interfaces"`
	SensorInterfaces     map[string]time.Time             `json:"sensor_interfaces"`
	SensorReceipts       map[string]SensorReceipt         `json:"sensor_receipts,omitempty"`
	Budget               *store.StorageBudgetStatus       `json:"storage_budget,omitempty"`
	AuthDetection        detect.AuthStats                 `json:"auth_detection"`
	SensorCommits        []store.SensorWatermark          `json:"sensor_commits"`
	SensorCommitUnknown  bool                             `json:"sensor_commit_unknown"`
	AuthWindowReadyAfter time.Time                        `json:"auth_window_ready_after_utc"`
	CoverageGaps         []store.CoverageGap              `json:"coverage_gaps"`
	Version              version.Info                     `json:"version"`
	StartedAt            time.Time                        `json:"started_at_utc"`
	Uptime               string                           `json:"uptime"`
	SensorRequired       bool                             `json:"sensor_required"`
	LastSensorBatch      time.Time                        `json:"last_sensor_batch_utc,omitempty"`
	SensorPeer           ipc.Peer                         `json:"sensor_peer"`
	Batches              uint64                           `json:"batches"`
	NativeChannels       map[string]bool                  `json:"native_channels"`
	OfficialChannels     map[string]bool                  `json:"official_channels"`
	OfficialPolicies     []store.OfficialChannelStatus    `json:"official_policies"`
	WebhookEnabled       bool                             `json:"webhook_enabled"`
	HeartbeatEnabled     bool                             `json:"heartbeat_enabled"`
	TelegramEnabled      bool                             `json:"telegram_enabled"`
	GeoEnabled           bool                             `json:"geo_enabled"`
	Components           []store.ComponentStatus          `json:"components"`
	Storage              StorageHealth                    `json:"storage"`
	Queue                *store.QueueStatus               `json:"queue,omitempty"`
	Detection            detect.NetworkStats              `json:"detection"`
	Journal              collector.JournalStatus          `json:"journal"`
	EventIngest          EventIngestStatus                `json:"event_ingest"`
}

type componentFailure struct {
	name  string
	fatal bool
	err   error
}

func New(options Options) (*App, error) {
	if options.Store == nil || options.Geo == nil || options.StorePrivacy == nil || options.NotifyPrivacy == nil {
		return nil, errors.New("daemon dependencies are incomplete")
	}
	if language := options.Config.Notifications.Telegram.Language; language != "" && language != "en" && language != "zh" {
		return nil, errors.New("telegram.language must be en or zh")
	}
	location, err := config.ReportLocation(options.Config.Reports)
	if err != nil {
		return nil, err
	}
	if err := options.Store.ConfigureNotifications(options.Config.Notifications.MergeWindow.Duration); err != nil {
		return nil, err
	}
	if options.NotificationDestination == "" {
		if sender, ok := options.Notifier.(interface{ Destination() string }); ok {
			options.NotificationDestination = sender.Destination()
		}
	}
	if options.NotificationDestination == "" && options.Config.Notifications.Telegram.ChatID != "" {
		// Resolve identity even while delivery is disabled so a disable/enable
		// cycle pauses a known same-target backlog instead of adopting a new one.
		options.NotificationDestination, _ = notify.TelegramDestination(options.Config.Notifications.Telegram.TokenFile, options.Config.Notifications.Telegram.ChatID)
	}
	if options.NotificationDestination == "" {
		options.NotificationDestination = "telegram:unknown"
	}
	if err := options.Store.ConfigureNotificationTarget(context.Background(), "telegram", options.NotificationDestination, options.Config.Privacy.NotificationIP, options.Config.Notifications.Telegram.Enabled, time.Now().UTC()); err != nil {
		return nil, err
	}
	if options.WebhookDestination == "" {
		if sender, ok := options.WebhookNotifier.(interface{ Destination() string }); ok {
			options.WebhookDestination = sender.Destination()
		}
	}
	if options.WebhookDestination == "" {
		w := options.Config.Notifications.Webhook
		if sender, err := notify.NewWebhook(w.Endpoint, w.ReceiverID, w.CredentialFile, w.Timeout.Duration); err == nil {
			options.WebhookDestination = sender.Destination()
		}
	}
	if options.WebhookDestination == "" {
		options.WebhookDestination = "webhook:unknown"
	}
	if err := options.Store.ConfigureNotificationTarget(context.Background(), "webhook", options.WebhookDestination, options.Config.Privacy.NotificationIP, options.Config.Notifications.Webhook.Enabled, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := configureNativeTargets(&options); err != nil {
		return nil, err
	}
	if err := configureOfficialTargets(&options); err != nil {
		return nil, err
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &App{options: options, network: detect.NewFleet(options.Config.Detection, options.Config.Sensor.InterfaceLimit()), auth: detect.NewAuth(options.Config.Auth), report: &report.Builder{Store: options.Store, Hostname: options.Config.Hostname, Location: location, TopN: options.Config.Reports.TopN, Billing: options.Billing, CycleStartDay: options.Config.Billing.CycleStartDay, ThresholdBytes: options.Config.Alerts.Budget.MonthlyBytes, ThresholdCost: options.Config.Alerts.Budget.MonthlyCost}, started: time.Now().UTC()}, nil
}

func (a *App) Run(ctx context.Context) error {
	a.mu.Lock()
	a.interfaceDiscoveryRequired = true
	a.mu.Unlock()
	// Read durable readiness before starting workers or opening listeners.
	var checkpoint store.Checkpoint
	if a.options.Config.Auth.Enabled {
		var err error
		checkpoint, err = a.options.Store.JournalCheckpoint(ctx)
		if err != nil {
			a.mu.Lock()
			a.journalStatus = collector.JournalStatus{State: "degraded", Reason: "recovery_state_unavailable", At: time.Now().UTC(), Since: a.started}
			a.mu.Unlock()
			a.recordWrite(err, "journal_recovery", false)
			return fmt.Errorf("load journal checkpoint: %w", err)
		}
	}
	sensorListener, err := ipc.ListenUnix(a.options.Config.Paths.SensorSocket, 0o660)
	if err != nil {
		return fmt.Errorf("listen sensor socket: %w", err)
	}
	controlListener, err := ipc.ListenUnix(a.options.Config.Paths.ControlSocket, 0o600)
	if err != nil {
		_ = sensorListener.Close()
		return fmt.Errorf("listen control socket: %w", err)
	}
	defer func() {
		_ = sensorListener.Close()
		_ = controlListener.Close()
		_ = os.Remove(a.options.Config.Paths.SensorSocket)
		_ = os.Remove(a.options.Config.Paths.ControlSocket)
	}()
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := a.options.Store.ResetComponentStatus(ctx); err != nil {
		return fmt.Errorf("reset component status: %w", err)
	}
	for name, enabled := range map[string]bool{"geoip": a.options.Config.Geo.CityMMDB != "" || a.options.Config.Geo.ASNMMDB != "", "billing": a.options.Config.Billing.Enabled} {
		state := "disabled"
		if enabled {
			state = "running"
		}
		if a.options.OptionalFailures[name] != "" {
			state = "degraded"
		}
		if err := a.options.Store.SetComponentStatus(ctx, name, state, time.Now().UTC()); err != nil {
			return fmt.Errorf("record optional feature state: %w", err)
		}
	}
	// The retry queue is process-local. A restart cannot prove how many
	// uncommitted network events the previous process held.
	a.eventLosses[eventRestart] = eventLoss{active: true, start: a.started, end: a.started}
	if err := a.options.Store.Prune(ctx, time.Now().UTC()); err != nil {
		a.options.Logger.Warn("startup storage prune failed", "error", err)
	}
	if a.options.Config.Sensor.Enabled {
		a.setSensorState(ctx, "degraded")
	} else {
		a.setSensorState(ctx, "disabled")
	}
	go func() { <-child.Done(); _ = sensorListener.Close(); _ = controlListener.Close() }()
	// One configured interface round is sufficient burst storage. A peer's
	// largest legal frames cannot accumulate sixty-four detail maps in RAM.
	batches := make(chan protocol.Batch, a.options.Config.Sensor.InterfaceLimit())
	journalEntries := make(chan journalDelivery, 1)
	totals := make(chan model.InterfaceTotals, 64)
	componentErrors := make(chan componentFailure, 16)
	var workers sync.WaitGroup
	start := func(name, initialState string, fatal bool, run func() error) {
		if err := a.options.Store.SetComponentStatus(ctx, name, initialState, time.Now().UTC()); err != nil {
			a.options.Logger.Warn("record component start", "component", name, "error", err)
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			err := run()
			if child.Err() == nil {
				a.monitorWorkerStopped(name, time.Now().UTC())
				if err == nil {
					err = errors.New("component stopped unexpectedly")
				}
				statusCtx, statusCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = a.options.Store.SetComponentStatus(statusCtx, name, "degraded", time.Now().UTC())
				statusCancel()
				select {
				case componentErrors <- componentFailure{name: name, fatal: fatal, err: fmt.Errorf("%s: %w", name, err)}:
				default:
				}
			}
		}()
	}
	start("sensor_socket", "running", true, func() error { return a.serveSensor(child, sensorListener, batches) })
	start("control_socket", "running", true, func() error { return a.serveControl(child, controlListener) })
	if a.options.Config.Auth.Enabled {
		journal := collector.Journal{Path: a.options.Config.Auth.Journalctl}
		start("ssh_journal", "degraded", false, func() error {
			return journal.RunReliable(child, collector.JournalOptions{InitialCursor: checkpoint.Cursor, InitialObservedAt: checkpoint.ObservedAt, InitialRecoveryPending: checkpoint.RecoveryPending, OnDegradation: a.recordJournalDegradation, OnStatus: func(status collector.JournalStatus) {
				a.mu.Lock()
				a.journalStatus = status
				a.mu.Unlock()
				state := "degraded"
				if status.State == "running" {
					state = "running"
				}
				a.recordWrite(a.options.Store.SetComponentStatus(child, "ssh_journal", state, time.Now().UTC()), "coverage", false)
				if status.State == "gap" && !status.QualityDegraded() {
					a.recordWrite(a.options.Store.RecordCoverageGap(child, journalCoverageGap(status)), "coverage_gap", false)
				}
			}}, func(deliveryCtx context.Context, entry collector.JournalEntry) error {
				delivery := journalDelivery{entry: entry, ack: make(chan error, 1)}
				select {
				case journalEntries <- delivery:
				case <-deliveryCtx.Done():
					return deliveryCtx.Err()
				}
				select {
				case err := <-delivery.ack:
					return err
				case <-deliveryCtx.Done():
					return deliveryCtx.Err()
				}
			})
		})
	} else if err := a.options.Store.SetComponentStatus(ctx, "ssh_journal", "disabled", time.Now().UTC()); err != nil {
		a.options.Logger.Warn("record disabled component", "component", "ssh_journal", "error", err)
	}
	netdev := collector.NetDev{Interface: a.options.Config.Sensor.Interface, Interfaces: a.options.Config.Sensor.Interfaces, OnInterfaces: func(values []collector.InterfaceObservation) error { return a.updateInterfaces(child, values) }, OnDiscovery: func(err error) error { return a.updateDiscovery(child, err) }, OnState: func(err error) error {
		state := "running"
		if err != nil {
			state = "degraded"
			a.options.Logger.Warn("interface collection unavailable; retrying", "error", err)
		}
		a.mu.Lock()
		a.interfaceCounterState = state
		a.mu.Unlock()
		if err := a.options.Store.SetComponentStatus(child, "interface_counter", state, time.Now().UTC()); err != nil {
			a.options.Logger.Warn("record interface coverage", "state", state, "error", err)
			return err
		}
		return nil
	}}
	start("interface_counter", "degraded", false, func() error { return netdev.Run(child, totals) })
	if a.options.Notifier != nil {
		worker := &notify.Worker{Store: a.options.Store, Sender: a.options.Notifier, Destination: a.options.NotificationDestination, Logger: a.options.Logger}
		start("notification_worker", "running", false, func() error { return worker.Run(child) })
	} else {
		state := "disabled"
		if a.options.Config.Notifications.Telegram.Enabled {
			state = "degraded"
		}
		if err := a.options.Store.SetComponentStatus(ctx, "notification_worker", state, time.Now().UTC()); err != nil {
			a.options.Logger.Warn("record notification component", "component", "notification_worker", "error", err)
		}
	}
	if a.options.WebhookNotifier != nil {
		worker := &notify.Worker{Store: a.options.Store, Sender: a.options.WebhookNotifier, Destination: a.options.WebhookDestination, Logger: a.options.Logger}
		start("webhook_worker", "running", false, func() error { return worker.Run(child) })
	} else {
		state := "disabled"
		if a.options.Config.Notifications.Webhook.Enabled {
			state = "degraded"
		}
		if err := a.options.Store.SetComponentStatus(ctx, "webhook_worker", state, time.Now().UTC()); err != nil {
			a.options.Logger.Warn("record webhook component", "error", err)
		}
	}
	a.startNativeWorkers(ctx, child, start)
	a.startOfficialWorkers(ctx, child, start)
	if a.options.Config.Heartbeat.Enabled && a.options.HeartbeatSender != nil {
		start("heartbeat", "starting", false, func() error { return a.runHeartbeat(child) })
	} else {
		state := "disabled"
		if a.options.Config.Heartbeat.Enabled {
			state = "degraded"
		}
		if err := a.options.Store.SetComponentStatus(ctx, "heartbeat", state, time.Now().UTC()); err != nil {
			a.options.Logger.Warn("record heartbeat component", "error", err)
		}
	}
	if a.options.Config.Reports.Enabled {
		destinations := []string{}
		if a.options.Config.Notifications.Telegram.Enabled {
			destinations = append(destinations, a.options.NotificationDestination)
		}
		if a.options.Config.Notifications.Webhook.Enabled {
			destinations = append(destinations, a.options.WebhookDestination)
		}
		languages := map[string]string{}
		preparers := map[string]report.OfficialPreparer{}
		for _, channel := range config.NativeChannelNames() {
			native := a.options.Config.Notifications.NativeChannels()[channel]
			if native.Enabled {
				destinations = append(destinations, a.options.NativeDestinations[channel])
				languages[channel] = config.NativeChannelLanguage(native)
			}
		}
		for _, channel := range config.OfficialChannelNames() {
			official := a.options.Config.Notifications.OfficialChannels()[channel]
			if official.Enabled && official.DailyEnabled && official.Subscription.Allows("daily") {
				destinations = append(destinations, a.options.OfficialDestinations[channel])
				languages[channel] = config.OfficialChannelLanguage(official)
				if p, ok := a.options.OfficialNotifiers[channel].(report.OfficialPreparer); ok {
					preparers[channel] = p
				}
			}
		}
		scheduler := &report.Scheduler{NativeLanguages: languages, OfficialNotifiers: preparers, Hostname: a.options.Config.Hostname, Store: a.options.Store, Builder: a.report, DailyAt: a.options.Config.Reports.DailyAt, Destinations: destinations, NotificationPrivacy: a.options.Config.Privacy.NotificationIP, TelegramLanguage: config.TelegramLanguage(a.options.Config.Notifications.Telegram), Logger: a.options.Logger, BackfillDays: a.options.Config.Reports.BackfillDays}
		start("report_scheduler", "running", false, func() error { return scheduler.Run(child) })
	} else if err := a.options.Store.SetComponentStatus(ctx, "report_scheduler", "disabled", time.Now().UTC()); err != nil {
		a.options.Logger.Warn("record disabled component", "component", "report_scheduler", "error", err)
	}
	if a.options.Config.Alerts.Budget.Enabled || a.options.Config.Alerts.Health.Enabled {
		workers.Add(1)
		go func() { defer workers.Done(); a.runMonitor(child) }()
	}
	pruneTicker := time.NewTicker(6 * time.Hour)
	defer pruneTicker.Stop()
	coverageTicker := time.NewTicker(10 * time.Second)
	defer coverageTicker.Stop()
	eventTicker := time.NewTicker(time.Second)
	defer eventTicker.Stop()
	a.options.Logger.Info("NodeRampart daemon started", "version", version.Version, "sensor_socket", a.options.Config.Paths.SensorSocket)
	for {
		select {
		case <-ctx.Done():
			cancel()
			workers.Wait()
			a.finishEvents()
			return nil
		case failure := <-componentErrors:
			a.options.Logger.Warn("component stopped; degraded coverage is reported", "component", failure.name, "error", failure.err)
			if failure.fatal {
				cancel()
				workers.Wait()
				a.finishEvents()
				return failure.err
			}
		case batch := <-batches:
			a.handleBatch(ctx, batch)
		case delivery := <-journalEntries:
			delivery.ack <- a.handleJournal(ctx, delivery.entry)
		case value := <-totals:
			err := a.options.Store.AddInterface(ctx, value)
			a.recordWrite(err, "interface", true)
			if err == nil {
				a.mu.Lock()
				a.interfaceCounterAt = time.Now().UTC()
				a.mu.Unlock()
			}
		case <-eventTicker.C:
			a.flushEvents(ctx)
		case now := <-pruneTicker.C:
			if err := a.options.Store.Prune(ctx, now.UTC()); err != nil {
				a.options.Logger.Warn("prune storage", "error", err)
			}
		case now := <-coverageTicker.C:
			a.refreshAuthCoverage(ctx, now.UTC())
			a.recordWrite(a.options.Store.MaintainBudget(ctx, now.UTC()), "budget_maintenance", false)
			a.recordWrite(a.options.Store.HeartbeatCoverage(ctx, now.UTC()), "coverage", false)
			if a.options.Config.Sensor.Enabled {
				a.refreshSensorState(ctx)
			}
		}
	}
}

func (a *App) handleBatch(ctx context.Context, batch protocol.Batch) {
	if batch.ProtocolVersion >= 5 && batch.SessionID != "" && batch.Sequence > 0 {
		a.handleSequencedBatch(ctx, batch)
		return
	}
	if len(a.pendingEvents) > 0 {
		a.flushEvents(ctx)
	}
	a.mu.Lock()
	previousCommit := a.sensorCommittedByInterface[batch.Interface]
	a.recordSensorReceiptLocked(batch)
	a.mu.Unlock()
	a.refreshSensorState(ctx)
	windowStart := batch.SentAt.Add(-time.Duration(batch.IntervalMillis) * time.Millisecond)
	if !previousCommit.IsZero() && windowStart.Sub(previousCommit) > time.Millisecond {
		// The intervening detail was never committed. Count is unknown; IPC
		// loss counters are separate conservative estimates, not this duration.
		err := a.options.Store.RecordCoverageGap(ctx, store.CoverageGap{Name: "sensor_feed", Reason: "sensor_observation_gap", Start: previousCommit, End: windowStart})
		a.recordWrite(err, "sensor_observation_gap", false)
		if err != nil {
			return // Do not advance beyond evidence that could not be recorded.
		}
	}
	healthErr := a.options.Store.RecordBatchHealth(ctx, batch)
	a.recordWrite(healthErr, "sensor_health", true)
	for _, event := range a.network.Observe(batch) {
		a.queueEvent(event)
	}
	a.flushEvents(ctx)
	trafficBatch := make([]model.Traffic, 0, len(batch.Flows))
	for _, flow := range batch.Flows {
		address, err := netip.ParseAddr(flow.RemoteIP)
		if err != nil {
			continue
		}
		geo := a.options.Geo.Lookup(address)
		attributed := geo.CountryCode != "" && geo.CountryCode != "PRIVATE"
		trafficBatch = append(trafficBatch, model.Traffic{HourUTC: batch.SentAt, Direction: flow.Direction, Country: geo.CountryCode, Region: geo.Region, ASN: geo.ASN, ASNOrg: geo.ASNOrg, Bytes: flow.Bytes, Packets: flow.Packets, Attributed: attributed})
	}
	trafficErr := a.options.Store.AddTrafficBatch(ctx, trafficBatch)
	a.recordWrite(trafficErr, "traffic", len(trafficBatch) > 0)
	if healthErr == nil && trafficErr == nil {
		a.mu.Lock()
		if a.sensorCommittedByInterface == nil {
			a.sensorCommittedByInterface = make(map[string]time.Time)
		}
		if _, exists := a.sensorCommittedByInterface[batch.Interface]; !exists && len(a.sensorCommittedByInterface) >= protocol.MaxInterfaces {
			oldest := ""
			for name, at := range a.sensorCommittedByInterface {
				if oldest == "" || at.Before(a.sensorCommittedByInterface[oldest]) {
					oldest = name
				}
			}
			delete(a.sensorCommittedByInterface, oldest)
		}
		if batch.SentAt.After(a.sensorCommittedByInterface[batch.Interface]) {
			a.sensorCommittedByInterface[batch.Interface] = batch.SentAt
		}
		a.mu.Unlock()
	}
}

func (a *App) sensorStaleAfter() time.Duration {
	return max(3*a.options.Config.Sensor.BatchInterval.Duration, 10*time.Second)
}

func (a *App) handleAuth(ctx context.Context, observation collector.AuthObservation) {
	_, sourceRange := a.options.StorePrivacy.IP(observation.SourceIP.String())
	if err := a.options.Store.AddAuth(ctx, observation.ObservedAt, string(observation.Kind), sourceRange); err != nil {
		a.options.Logger.Error("store authentication observation", "kind", observation.Kind, "error", err)
	}
	if event := a.auth.Observe(observation); event != nil {
		a.addAuthHistoryHints(ctx, observation, event)
		a.handleEvent(ctx, *event)
	}
	a.refreshAuthCoverage(ctx, time.Now().UTC())
}

func (a *App) handleEvent(ctx context.Context, event model.Event) {
	a.queueEvent(event)
	a.flushEvents(ctx)
}

func (a *App) prepareEvent(event model.Event) (model.Event, *store.OutboxMessage) {
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}
	if event.ID == "" {
		event.ID = model.NewID("evt")
	}
	if event.IncidentID == "" {
		event.IncidentID = model.NewID("inc")
	}
	if address, err := netip.ParseAddr(event.SourceIP); err == nil {
		event.Geo = a.options.Geo.Lookup(address)
	}
	stored := event
	if event.SourceIP != "" {
		stored.SourceIP, stored.SourceRange = a.options.StorePrivacy.IP(event.SourceIP)
	}
	if !a.notificationsEnabled() || (event.Phase != "recovery" && severityRank(event.Severity) < severityRank(model.SeverityMedium)) {
		return stored, nil
	}
	notification := event
	if event.SourceIP != "" {
		notification.SourceIP, notification.SourceRange = a.options.NotifyPrivacy.IP(event.SourceIP)
	}
	var primary, tail *store.OutboxMessage
	for _, target := range a.notificationTargets() {
		if !target.enabled {
			continue
		}
		// Journal replay and retained observations stay local when predating a
		// newly enabled native target. Local event commit/watermark are unchanged.
		if config.IsNativeChannel(target.channel) {
			activated := a.options.nativeActivatedAt[target.channel]
			if activated.IsZero() || event.ObservedAt.Before(activated) {
				continue
			}
		}
		if config.IsOfficialChannel(target.channel) {
			c := a.options.Config.Notifications.OfficialChannels()[target.channel]
			activated := a.options.officialActivatedAt[target.channel]
			if !c.EventsEnabled || !c.Subscription.Allows("event") || activated.IsZero() || event.ObservedAt.Before(activated) || (event.Phase != "recovery" && severityRank(event.Severity) < severityRank(model.Severity(c.MinSeverity))) {
				continue
			}
		}
		message := &store.OutboxMessage{ID: model.NewID("msg"), DedupeKey: "event:" + event.ID + ":" + target.channel, Channel: target.channel, PrivacyMode: a.options.Config.Privacy.NotificationIP, Destination: target.destination, Body: notify.FormatEvent(a.options.Config.Hostname, notification)}
		// Webhook's English event renderer uses UTC independently of Telegram.
		message.Timezone = "UTC|UTC+00:00"
		if target.channel == "telegram" {
			message.Language = config.TelegramLanguage(a.options.Config.Notifications.Telegram)
			location := a.report.Location // The validated production report location.
			message.Timezone = location.String() + "|" + timezones.Offset(event.ObservedAt.In(location))
			message.Body = notify.FormatEventLocalized(a.options.Config.Hostname, notification, message.Language, location)
		}
		if config.IsNativeChannel(target.channel) {
			native := a.options.Config.Notifications.NativeChannels()[target.channel]
			message.Language = config.NativeChannelLanguage(native)
			location := a.report.Location
			message.Timezone = location.String() + "|" + timezones.Offset(event.ObservedAt.In(location))
			message.Body = notify.FormatNativeEvent(a.options.Config.Hostname, notification, message.Language, location)
		}
		if config.IsOfficialChannel(target.channel) {
			message.Language = config.OfficialChannelLanguage(a.options.Config.Notifications.OfficialChannels()[target.channel])
			location := a.report.Location
			message.Timezone = location.String() + "|" + timezones.Offset(event.ObservedAt.In(location))
			message.LogicalKind = "event"
			body, semantic := notify.FormatOfficialEvent(a.options.Config.Hostname, notification, message.Language, location)
			message.Body = body
			if err := prepareOfficialMessage(a.options.OfficialNotifiers[target.channel], message, semantic); err != nil {
				message.AdmissionFailure = "official_render_rejected"
			}
		}
		if primary == nil {
			primary = message
		} else {
			tail.Secondary = message
		}
		tail = message
	}
	return stored, primary
}

func severityRank(value model.Severity) int {
	switch value {
	case model.SeverityCritical:
		return 5
	case model.SeverityHigh:
		return 4
	case model.SeverityMedium:
		return 3
	case model.SeverityLow:
		return 2
	default:
		return 1
	}
}

func (a *App) Status(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	a.mu.RLock()
	status := Status{Version: version.Current(), StartedAt: a.started, Uptime: time.Since(a.started).Round(time.Second).String(), SensorRequired: a.options.Config.Sensor.Required, LastSensorBatch: a.lastSensor, SensorPeer: a.sensorPeer, Batches: a.batches, TelegramEnabled: a.options.Config.Notifications.Telegram.Enabled, GeoEnabled: a.options.Config.Geo.CityMMDB != "" || a.options.Config.Geo.ASNMMDB != ""}
	status.GeneratedAt = time.Now().UTC()
	status.NativeChannels = map[string]bool{}
	for channel, native := range a.options.Config.Notifications.NativeChannels() {
		status.NativeChannels[channel] = native.Enabled
	}
	status.OfficialChannels = map[string]bool{}
	for channel, c := range a.options.Config.Notifications.OfficialChannels() {
		status.OfficialChannels[channel] = c.Enabled
	}
	status.WebhookEnabled, status.HeartbeatEnabled = a.options.Config.Notifications.Webhook.Enabled, a.options.Config.Heartbeat.Enabled
	status.SensorEnabled, status.AuthEnabled = a.options.Config.Sensor.Enabled, a.options.Config.Auth.Enabled
	status.OptionalFailures = make(map[string]string, len(a.options.OptionalFailures))
	for name, reason := range a.options.OptionalFailures {
		status.OptionalFailures[name] = reason
	}
	status.Interfaces = append([]collector.InterfaceObservation(nil), a.interfaces...)
	status.LastInterfaceCommit = a.interfaceCounterAt
	status.SensorInterfaces = make(map[string]time.Time, len(a.sensorByInterface))
	for name, at := range a.sensorByInterface {
		status.SensorInterfaces[name] = at
	}
	status.SensorReceipts = make(map[string]SensorReceipt, len(a.sensorReceipts))
	for name, receipt := range a.sensorReceipts {
		status.SensorReceipts[name] = receipt
	}
	a.mu.RUnlock()
	for _, channel := range config.OfficialChannelNames() {
		if policy, err := a.options.Store.OfficialChannelStatus(ctx, channel, status.GeneratedAt); err == nil {
			status.OfficialPolicies = append(status.OfficialPolicies, policy)
		}
	}
	components, err := a.options.Store.ComponentStatuses(ctx)
	a.recordWrite(err, "component_status", false)
	if err == nil {
		status.Components = components
	}
	status.Monitoring = a.MonitorStatus()
	status.Detection = a.network.Stats()
	var commitStatusErr error
	status.SensorCommits, commitStatusErr = a.options.Store.SensorWatermarks(ctx)
	status.SensorCommitUnknown = commitStatusErr != nil
	budget, budgetErr := a.options.Store.BudgetStatus(ctx)
	if budgetErr == nil {
		status.Budget = &budget
	}
	gaps, gapErr := a.options.Store.CoverageGaps(ctx, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC(), 10)
	if gapErr == nil {
		status.CoverageGaps = gaps
	}
	status.AuthWindowReadyAfter = a.started.Add(a.options.Config.Auth.Window.Duration)
	queue, queueErr := a.options.Store.QueueStatus(ctx, time.Now().UTC())
	a.recordWrite(queueErr, "queue_status", false)
	if queueErr == nil {
		status.Queue = &queue
	}
	a.mu.RLock()
	status.Storage = a.storageHealth
	status.Journal = a.journalStatus
	status.EventIngest = a.eventIngest
	status.EventIngest.PendingLimit = maxPendingEvents
	status.EventIngest.ByteLimit = maxPendingEventBytes
	status.AuthDetection = a.authStats
	a.mu.RUnlock()
	if a.auth != nil {
		status.AuthDetection = a.auth.Stats()
	}
	status.Readiness = a.readiness(status, err == nil && budgetErr == nil, time.Now().UTC())
	diagnosis := Diagnose(status, false)
	status.Diagnosis = &diagnosis
	return status
}

func (a *App) serveSensor(ctx context.Context, listener *net.UnixListener, output chan<- protocol.Batch) error {
	semaphore := make(chan struct{}, 1)
	minimumInterval := a.options.Config.Sensor.BatchInterval.Duration / 2
	if minimumInterval < 50*time.Millisecond {
		minimumInterval = 50 * time.Millisecond
	}
	readGate := sensorReadGate{interval: minimumInterval / time.Duration(a.options.Config.Sensor.InterfaceLimit()), burst: a.options.Config.Sensor.InterfaceLimit()}
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case semaphore <- struct{}{}:
			go func(conn *net.UnixConn) {
				defer func() { <-semaphore; _ = conn.Close() }()
				peer, err := ipc.PeerCredentials(conn)
				if err != nil || a.options.SensorUID == nil || peer.UID != *a.options.SensorUID {
					a.options.Logger.Warn("reject unauthorized sensor peer")
					return
				}
				stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
				defer stopClose()
				reader := bufio.NewReaderSize(conn, 64<<10)
				previousTimes := make(map[string]time.Time)
				previousSequences := make(map[string]struct {
					session  string
					sequence uint64
				})
				var replyMu sync.Mutex
				connectionID := a.sensorConnections.Add(1)
				for {
					if err := readGate.wait(ctx); err != nil {
						return
					}
					readTimeout := 3*a.options.Config.Sensor.BatchInterval.Duration + 5*time.Second
					_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
					var batch protocol.Batch
					if err := protocol.ReadFrame(reader, &batch); err != nil {
						return
					}
					readGate.readComplete()
					if err := batch.Validate(); err != nil {
						a.options.Logger.Warn("reject invalid sensor batch", "uid", peer.UID, "error", err)
						return
					}
					if !a.allowedSensorInterface(batch.Interface) {
						return
					}
					previousSentAt := previousTimes[batch.Interface]
					previousSequence := previousSequences[batch.Interface]
					duplicate := batch.Sequence > 0 && batch.SessionID == previousSequence.session && batch.Sequence <= previousSequence.sequence
					if previousSentAt.IsZero() && len(previousTimes) >= a.options.Config.Sensor.InterfaceLimit() {
						return
					}
					claimedInterval := time.Duration(batch.IntervalMillis) * time.Millisecond
					maximumInterval := protocol.MaxElapsedInterval(a.options.Config.Sensor.BatchInterval.Duration)
					if claimedInterval < minimumInterval || claimedInterval > maximumInterval {
						a.options.Logger.Warn("reject sensor interval outside configured bounds", "uid", peer.UID)
						return
					}
					if !previousSentAt.IsZero() && !duplicate {
						sentDelta := batch.SentAt.Sub(previousSentAt)
						if sentDelta < minimumInterval || claimedInterval < sentDelta/2 || claimedInterval > 2*sentDelta {
							a.options.Logger.Warn("reject inconsistent or non-monotonic sensor interval", "uid", peer.UID)
							return
						}
					}
					if previousSentAt.IsZero() {
						a.mu.Lock()
						a.sensorPeer = peer
						a.mu.Unlock()
					}
					if !duplicate {
						previousTimes[batch.Interface] = batch.SentAt
						previousSequences[batch.Interface] = struct {
							session  string
							sequence uint64
						}{batch.SessionID, batch.Sequence}
					}
					batch.ConnectionID = connectionID
					if batch.ProtocolVersion >= 5 && batch.Sequence > 0 {
						batch.Acknowledge = func(ack protocol.CommitACK) error {
							replyMu.Lock()
							defer replyMu.Unlock()
							if err := conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
								return err
							}
							return protocol.WriteFrame(conn, ack)
						}
					}
					select {
					case output <- batch:
					case <-ctx.Done():
						return
					}
				}
			}(connection)
		default:
			_ = connection.Close()
			a.options.Logger.Warn("reject excess sensor connection")
		}
	}
}

func (a *App) serveControl(ctx context.Context, listener *net.UnixListener) error {
	semaphore := make(chan struct{}, 16)
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case semaphore <- struct{}{}:
			go func() { defer func() { <-semaphore }(); a.handleControl(ctx, connection) }()
		default:
			_ = connection.Close()
		}
	}
}

func (a *App) handleControl(ctx context.Context, connection *net.UnixConn) {
	defer connection.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopClose()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	peer, err := ipc.PeerCredentials(connection)
	if err != nil {
		return
	}
	if peer.UID != 0 && peer.UID != uint32(os.Geteuid()) {
		_ = protocol.WriteFrame(connection, api.Response{Version: api.Version, OK: false, Error: "control socket requires root or daemon UID"})
		return
	}
	var request api.Request
	if err := protocol.ReadFrame(bufio.NewReaderSize(connection, 16<<10), &request); err != nil {
		return
	}
	timeout := 10 * time.Second
	if request.Command == "backup_create" && request.Version == api.Version {
		var args api.BackupArgs
		if api.DecodeArgs(request.Args, &args) == nil && filepath.IsAbs(args.Output) && filepath.Clean(args.Output) == args.Output && filepath.Dir(args.Output) == filepath.Join(filepath.Dir(a.options.Config.Paths.Database), "backups") {
			timeout = 2 * time.Minute
		}
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stopRequest := context.AfterFunc(requestCtx, func() { _ = connection.Close() })
	defer stopRequest()
	_ = connection.SetDeadline(time.Now().Add(timeout))
	response := a.controlResponse(requestCtx, request)
	_ = protocol.WriteFrame(connection, response)
}
