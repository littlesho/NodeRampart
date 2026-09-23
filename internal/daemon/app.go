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
	"github.com/littlesho/NodeRampart/internal/version"
)

type Options struct {
	Config          config.Config
	Store           *store.Store
	Geo             *enrich.Resolver
	Notifier        notify.Sender
	Billing         *billing.Profile
	StorePrivacy    *privacy.Transformer
	NotifyPrivacy   *privacy.Transformer
	Logger          *slog.Logger
	SensorUID       *uint32
	AssetHealthPath string
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
	pendingEvents              []queuedEvent
	pendingEventBytes          int
	eventLosses                [3]eventLoss
	eventIngest                EventIngestStatus
	sensorConnections          atomic.Uint64
	interfaces                 []collector.InterfaceObservation
	sensorByInterface          map[string]time.Time
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
	Monitoring           MonitorStatus                    `json:"monitoring"`
	Interfaces           []collector.InterfaceObservation `json:"interfaces"`
	SensorInterfaces     map[string]time.Time             `json:"sensor_interfaces"`
	Budget               *store.StorageBudgetStatus       `json:"storage_budget,omitempty"`
	AuthDetection        detect.AuthStats                 `json:"auth_detection"`
	AuthWindowReadyAfter time.Time                        `json:"auth_window_ready_after_utc"`
	CoverageGaps         []store.CoverageGap              `json:"coverage_gaps"`
	Version              version.Info                     `json:"version"`
	StartedAt            time.Time                        `json:"started_at_utc"`
	Uptime               string                           `json:"uptime"`
	SensorRequired       bool                             `json:"sensor_required"`
	LastSensorBatch      time.Time                        `json:"last_sensor_batch_utc,omitempty"`
	SensorPeer           ipc.Peer                         `json:"sensor_peer"`
	Batches              uint64                           `json:"batches"`
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
	if err := options.Store.ConfigureNotifications(options.Config.Notifications.MergeWindow.Duration); err != nil {
		return nil, err
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	location, err := config.ReportLocation(options.Config.Reports)
	if err != nil {
		return nil, err
	}
	return &App{options: options, network: detect.NewFleet(options.Config.Detection, options.Config.Sensor.InterfaceLimit()), auth: detect.NewAuth(options.Config.Auth), report: &report.Builder{Store: options.Store, Hostname: options.Config.Hostname, Location: location, TopN: options.Config.Reports.TopN, Billing: options.Billing}, started: time.Now().UTC()}, nil
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
	batches := make(chan protocol.Batch, 64)
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
		worker := &notify.Worker{Store: a.options.Store, Sender: a.options.Notifier, Logger: a.options.Logger}
		start("notification_worker", "running", false, func() error { return worker.Run(child) })
	} else if err := a.options.Store.SetComponentStatus(ctx, "notification_worker", "disabled", time.Now().UTC()); err != nil {
		a.options.Logger.Warn("record disabled component", "component", "notification_worker", "error", err)
	}
	if a.options.Config.Reports.Enabled {
		destination := ""
		if a.options.Notifier != nil {
			destination = "telegram"
		}
		scheduler := &report.Scheduler{Store: a.options.Store, Builder: a.report, DailyAt: a.options.Config.Reports.DailyAt, Destination: destination, Logger: a.options.Logger, BackfillDays: a.options.Config.Reports.BackfillDays}
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
			a.mu.Lock()
			a.interfaceCounterAt = time.Now().UTC()
			a.mu.Unlock()
			a.recordWrite(a.options.Store.AddInterface(ctx, value), "interface", true)
		case <-eventTicker.C:
			a.flushEvents(ctx)
		case now := <-pruneTicker.C:
			if err := a.options.Store.Prune(ctx, now.UTC()); err != nil {
				a.options.Logger.Warn("prune storage", "error", err)
			}
		case now := <-coverageTicker.C:
			a.recordWrite(a.options.Store.MaintainBudget(ctx, now.UTC()), "budget_maintenance", false)
			a.recordWrite(a.options.Store.HeartbeatCoverage(ctx, now.UTC()), "coverage", false)
			if a.options.Config.Sensor.Enabled {
				a.refreshSensorState(ctx)
			}
		}
	}
}

func (a *App) handleBatch(ctx context.Context, batch protocol.Batch) {
	if len(a.pendingEvents) > 0 {
		a.flushEvents(ctx)
	}
	a.mu.Lock()
	a.lastSensor = batch.SentAt
	if a.sensorByInterface == nil {
		a.sensorByInterface = make(map[string]time.Time)
	}
	if _, exists := a.sensorByInterface[batch.Interface]; !exists && len(a.sensorByInterface) >= a.options.Config.Sensor.InterfaceLimit() {
		oldest := ""
		for name, at := range a.sensorByInterface {
			if oldest == "" || at.Before(a.sensorByInterface[oldest]) {
				oldest = name
			}
		}
		delete(a.sensorByInterface, oldest)
	}
	a.sensorByInterface[batch.Interface] = batch.SentAt
	a.batches++
	a.mu.Unlock()
	a.refreshSensorState(ctx)
	a.recordWrite(a.options.Store.RecordBatchHealth(ctx, batch), "sensor_health", true)
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
	a.recordWrite(a.options.Store.AddTrafficBatch(ctx, trafficBatch), "traffic", len(trafficBatch) > 0)
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
		a.handleEvent(ctx, *event)
	}
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
	if a.options.Notifier == nil || (event.Phase != "recovery" && severityRank(event.Severity) < severityRank(model.SeverityMedium)) {
		return stored, nil
	}
	notification := event
	if event.SourceIP != "" {
		notification.SourceIP, notification.SourceRange = a.options.NotifyPrivacy.IP(event.SourceIP)
	}
	return stored, &store.OutboxMessage{ID: model.NewID("msg"), DedupeKey: "event:" + event.ID + ":telegram", Destination: "telegram", Body: notify.FormatEvent(a.options.Config.Hostname, notification)}
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
	status := Status{Version: version.Current(), StartedAt: a.started, Uptime: time.Since(a.started).Round(time.Second).String(), SensorRequired: a.options.Config.Sensor.Required, LastSensorBatch: a.lastSensor, SensorPeer: a.sensorPeer, Batches: a.batches, TelegramEnabled: a.options.Notifier != nil, GeoEnabled: a.options.Config.Geo.CityMMDB != "" || a.options.Config.Geo.ASNMMDB != ""}
	status.Interfaces = append([]collector.InterfaceObservation(nil), a.interfaces...)
	status.SensorInterfaces = make(map[string]time.Time, len(a.sensorByInterface))
	for name, at := range a.sensorByInterface {
		status.SensorInterfaces[name] = at
	}
	a.mu.RUnlock()
	components, err := a.options.Store.ComponentStatuses(ctx)
	a.recordWrite(err, "component_status", false)
	if err == nil {
		status.Components = components
	}
	status.Monitoring = a.MonitorStatus()
	status.Detection = a.network.Stats()
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
	status.AuthDetection = a.authStats
	status.Journal = a.journalStatus
	status.EventIngest = a.eventIngest
	status.EventIngest.PendingLimit = maxPendingEvents
	status.EventIngest.ByteLimit = maxPendingEventBytes
	a.mu.RUnlock()
	return status
}

func (a *App) serveSensor(ctx context.Context, listener *net.UnixListener, output chan<- protocol.Batch) error {
	semaphore := make(chan struct{}, 1)
	minimumInterval := a.options.Config.Sensor.BatchInterval.Duration / 2
	if minimumInterval < 50*time.Millisecond {
		minimumInterval = 50 * time.Millisecond
	}
	readGate := sensorReadGate{interval: minimumInterval / time.Duration(a.options.Config.Sensor.InterfaceLimit())}
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
					if err := batch.Validate(); err != nil {
						a.options.Logger.Warn("reject invalid sensor batch", "uid", peer.UID, "error", err)
						return
					}
					if !a.allowedSensorInterface(batch.Interface) {
						return
					}
					previousSentAt := previousTimes[batch.Interface]
					if previousSentAt.IsZero() && len(previousTimes) >= a.options.Config.Sensor.InterfaceLimit() {
						return
					}
					claimedInterval := time.Duration(batch.IntervalMillis) * time.Millisecond
					if previousSentAt.IsZero() {
						maximumInterval := 10 * a.options.Config.Sensor.BatchInterval.Duration
						if a.options.Config.Sensor.InterfaceLimit() > 1 {
							maximumInterval = max(maximumInterval, 5*time.Second)
						}
						if claimedInterval < minimumInterval || claimedInterval > maximumInterval {
							a.options.Logger.Warn("reject sensor interval outside configured bounds", "uid", peer.UID)
							return
						}
					} else {
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
					previousTimes[batch.Interface] = batch.SentAt
					batch.ConnectionID = connectionID
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
