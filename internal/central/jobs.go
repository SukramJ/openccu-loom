// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// StandardJobs is the configurable bundle of background jobs every
// Unit.Start should register. Slots default to no-op when their
// callback is nil — that way callers register only the jobs whose
// dependencies are wired (e.g. Hub connectivity needs the JSON-RPC
// session to be up first).
//
// where the Python daemon ships ~13 concrete refresh tasks.
type StandardJobs struct {
	// HealthHeartbeatInterval is how often the central records a
	// heartbeat sample on the health tracker. Matches Python's
	// `HEALTH_CHECK_INTERVAL` (60 s).
	HealthHeartbeatInterval time.Duration

	// HubConnectivityRefresh runs `Hub.fetch_connectivity_data` on the
	// configured cadence. Nil disables.
	HubConnectivityRefresh  func(ctx context.Context) error
	HubConnectivityInterval time.Duration

	// HubMetricsRefresh pulls the system-health/metrics block from the
	// CCU. Nil disables.
	HubMetricsRefresh  func(ctx context.Context) error
	HubMetricsInterval time.Duration

	// LastEventAgeRefresh observes MetricLastEventAgeSecs — seconds since
	// the most recent CCU callback across all of the central's interfaces.
	// Nil disables.
	LastEventAgeRefresh  func(ctx context.Context) error
	LastEventAgeInterval time.Duration

	// FirmwareUpdateCheck polls for available firmware. Nil disables.
	FirmwareUpdateCheck         func(ctx context.Context) error
	FirmwareUpdateCheckInterval time.Duration

	// FirmwareDeliveringCheck polls for firmware images currently being
	// transmitted to devices.
	FirmwareDeliveringCheck         func(ctx context.Context) error
	FirmwareDeliveringCheckInterval time.Duration

	// FirmwareUpdatingCheck polls for firmware updates currently
	// being applied. Mirrors `_fetch_device_firmware_update_data_in_update`.
	FirmwareUpdatingCheck         func(ctx context.Context) error
	FirmwareUpdatingCheckInterval time.Duration

	// ProgramRefresh refreshes the CCU's program list. Mirrors
	// `_refresh_program_data`.
	ProgramRefresh         func(ctx context.Context) error
	ProgramRefreshInterval time.Duration

	// SysvarRefresh refreshes the CCU's system-variable list. Mirrors
	// `_refresh_sysvar_data`.
	SysvarRefresh         func(ctx context.Context) error
	SysvarRefreshInterval time.Duration

	// InboxRefresh refreshes the CCU's inbox (devices waiting to be
	// accepted). Mirrors `_refresh_inbox_data`.
	InboxRefresh         func(ctx context.Context) error
	InboxRefreshInterval time.Duration

	// ServiceMessagesRefresh refreshes the CCU's service-messages
	// (low-battery, sticky-unreach, …). Mirrors
	// `_refresh_service_messages_data`.
	ServiceMessagesRefresh         func(ctx context.Context) error
	ServiceMessagesRefreshInterval time.Duration

	// AlarmMessagesRefresh refreshes the CCU's alarm-messages.
	// Mirrors `_refresh_alarm_messages_data`.
	AlarmMessagesRefresh         func(ctx context.Context) error
	AlarmMessagesRefreshInterval time.Duration

	// SystemUpdateRefresh polls the CCU's system-firmware update info.
	// Mirrors `_refresh_system_update_data`.
	SystemUpdateRefresh         func(ctx context.Context) error
	SystemUpdateRefreshInterval time.Duration

	// InstallModeRefresh polls the CCU's install-mode (pairing window). While
	// the HubCoordinator owns the live state, the periodic poll catches drift
	// when the CCU's install-mode has been toggled out-of-band (e.g. from the
	// CCU's Web UI). Nil disables.
	InstallModeRefresh         func(ctx context.Context) error
	InstallModeRefreshInterval time.Duration

	// RefreshClientData performs a periodic full VALUES paramset sweep by
	// re-fetching all cached data-point values from the CCU. Nil disables.
	RefreshClientData         func(ctx context.Context) error
	RefreshClientDataInterval time.Duration

	// CheckConnectionInterval controls how often the central.check_connection
	// job fires. A negative value disables the job entirely; zero falls back to
	// [defaultCheckConnectionSlot].
	CheckConnectionInterval time.Duration

	// Reconcile is the hybrid reconciliation pass that compares cached
	// connectivity / system-health against the CCU's authoritative
	// state and emits drift events on divergence. Nil disables.
	Reconcile         func(ctx context.Context) error
	ReconcileInterval time.Duration
}

// Default intervals for the background scheduler jobs.
//
// "Intervall-Konstanten dokumentieren und als
// Konfigurationsparameter exponieren — identisch zu Python."
//
// Go defaults vs. Python ScheduleTimerConfig
//
//	Go constant Go default Python field Python default Note
//	─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
//	defaultHealthHeartbeat 60 s (no direct Python peer) — Go-only; health tracker heartbeat
//	defaultCheckConnectionSlot 30 s  connection_checker_interval 15 s BY DESIGN: Go has BIN-RPC push — the
//	 30 s tick is a stale-callback safety
//	 net (Python doubles up on polling
//	 because it has no push channel).
//	defaultRefreshClientDataSlot 5 min periodic_refresh_interval 15 s BY DESIGN: push architecture means a
//	 full sweep is a recovery mechanism,
//	 not the primary data source; 5 min
//	 reduces CCU load without sacrificing
//	 correctness.
//	defaultProgramRefreshSlot 5 min sys_scan_interval 30 s Parity divergence: Go uses per-job
//	defaultSysvarRefreshSlot 5 min sys_scan_interval 30 s intervals; Python reuses one shared
//	defaultInboxRefreshSlot 5 min sys_scan_interval 30 s `sys_scan_interval` for all five.
//	defaultServiceMessagesSlot 5 min sys_scan_interval 30 s Go 5 min is more conservative; adjust
//	defaultAlarmMessagesSlot 5 min sys_scan_interval 30 s via StandardJobs.*Interval overrides.
//	defaultHubMetrics 5 min metrics_refresh_interval 60 s Slight divergence; non-critical.
//	defaultHubConnectivity 2 min metrics_refresh_interval 60 s Python reuses metrics interval for
//	 connectivity; Go separates them.
//	defaultFirmwareCheckSlot 60 min device_firmware_check_interval 6 h Go is more eager; acceptable as the
//	 CCU call is cheap.
//	defaultFirmwareDeliverySlot 1 h device_firmware_delivering_check 1 h Exact match ✓
//	defaultFirmwareUpdatingSlot 30 s device_firmware_updating_check 5 min BY DESIGN: active firmware flash is
//	 time-sensitive; 30 s gives fast UI
//	 feedback during the update window.
//	defaultSystemUpdateSlot 60 min system_update_check_interval 4 h Go is more eager; non-critical.
//	defaultInstallModeSlot 30 s (no Python peer; Python uses events) — Go-only; polls install-mode drift.
//	defaultReconcileSlot 5 min (no Python peer) — Go-only; hybrid reconciliation pass.
//
// Override any default by setting the corresponding *Interval field on
// [StandardJobs] before calling [RegisterStandardJobs]. A zero value
// falls back to the constant; a negative value disables the job
// entirely (check_connection only).
const (
	defaultHealthHeartbeat       = 60 * time.Second
	defaultHubConnectivity       = 2 * time.Minute
	defaultHubMetrics            = 5 * time.Minute
	defaultLastEventAge          = 30 * time.Second
	defaultFirmwareCheckSlot     = 60 * time.Minute
	defaultFirmwareDeliverySlot  = 1 * time.Hour
	defaultFirmwareUpdatingSlot  = 30 * time.Second
	defaultProgramRefreshSlot    = 5 * time.Minute
	defaultSysvarRefreshSlot     = 5 * time.Minute
	defaultInboxRefreshSlot      = 5 * time.Minute
	defaultServiceMessagesSlot   = 5 * time.Minute
	defaultAlarmMessagesSlot     = 5 * time.Minute
	defaultSystemUpdateSlot      = 60 * time.Minute
	defaultInstallModeSlot       = 30 * time.Second
	defaultReconcileSlot         = 5 * time.Minute
	defaultRefreshClientDataSlot = 5 * time.Minute

	// DefaultCheckConnectionSlot mirrors
	// cadence. Python uses 15 s (ScheduleTimerConfig.connection_checker_interval);
	// we default to 120 s because openccu-loom already has BIN-RPC push events
	// and the heartbeat job covers the 60 s window. 120 s keeps polling light
	// while still catching a stale callback within two ticks.
	// See divergence table above.
	defaultCheckConnectionSlot = 30 * time.Second
)

// defaultRefreshClientData returns a default RefreshClientData implementation
// that delegates to [Unit.LoadAndRefreshDataPointData] and wraps the
// call with [hmevent.DataRefreshTriggeredEvent] / [hmevent.DataRefreshCompletedEvent]
// bookends. Returns nil when the central has no load-refresh function wired,
// so the slot stays nil-by-default and is not registered.
func defaultRefreshClientData(unit *Unit) func(ctx context.Context) error {
	if unit == nil {
		return nil
	}
	return func(ctx context.Context) error {
		const jobName = "central.refresh_client_data"
		start := timeNow()
		if unit.EventBus != nil {
			events.Publish(unit.EventBus, hmevent.DataRefreshTriggeredEvent{
				Base:        hmevent.NewBase(),
				CentralName: unit.cfg.Name,
				JobName:     jobName,
				Scheduled:   true,
			})
		}
		err := unit.LoadAndRefreshDataPointData(ctx)
		duration := timeNow().Sub(start).Milliseconds()
		if unit.EventBus != nil {
			completed := hmevent.DataRefreshCompletedEvent{
				Base:        hmevent.NewBase(),
				CentralName: unit.cfg.Name,
				JobName:     jobName,
				Duration:    duration,
				Success:     err == nil,
			}
			if err != nil {
				completed.ErrorMessage = err.Error()
			}
			events.Publish(unit.EventBus, completed)
		}
		return err
	}
}

// timeNow is a package-level alias for time.Now so tests can override it.
// Only used inside this file.
var timeNow = time.Now

// isOperational returns true when unit's central state machine is in RUNNING
// or DEGRADED — the two states where background jobs should execute.
func isOperational(unit *Unit) bool {
	if unit == nil || unit.StateMachine == nil {
		return false
	}
	s := unit.StateMachine.State()
	return s == hmenum.CentralStateRunning || s == hmenum.CentralStateDegraded
}

// hasConnectionIssue returns true when at least one registered client is not
// CONNECTED. Background non-connection-check jobs should skip their work when
// this is true to avoid producing spurious errors against a CCU that is
// currently unreachable.
func hasConnectionIssue(unit *Unit) bool {
	if unit == nil || unit.Clients == nil {
		return false
	}
	for _, entry := range unit.Clients.List() {
		if !entry.Connected() {
			return true
		}
	}
	return false
}

// gatedRun wraps fn so it only executes when the central is operational
// (RUNNING or DEGRADED) AND has no connection issues. Pass
// skipOnConnectionIssue=false for connection-management jobs that must
// fire regardless of client health (e.g. check_connection itself).
func gatedRun(unit *Unit, skipOnConnectionIssue bool, fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		if !isOperational(unit) {
			return nil
		}
		if skipOnConnectionIssue && hasConnectionIssue(unit) {
			return nil
		}
		return fn(ctx)
	}
}

// gatedRunWithDevicesCreatedGate is like [gatedRun] but adds an additional
// condition: the job only executes after at least one DeviceCreatedEvent has
// been observed (i.e. [Unit.IsDevicesCreated] returns true).
//
// This mirrors
// `central/scheduler.py` which defers hub jobs (sysvar/program/alarm
// refresh) until the device creation phase completes. Use this wrapper
// for hub-level periodic jobs when device presence is a prerequisite.
//
// When [Unit.WireDevicesCreatedGate] has not been called, this
// behaves identically to [gatedRun] (gate is a no-op).
func gatedRunWithDevicesCreatedGate(unit *Unit, skipOnConnectionIssue bool, fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		if !isOperational(unit) {
			return nil
		}
		if skipOnConnectionIssue && hasConnectionIssue(unit) {
			return nil
		}
		if !unit.IsDevicesCreated() {
			return nil
		}
		return fn(ctx)
	}
}

// RegisterStandardJobs wires the configured jobs onto the central's
// scheduler. Returns the names of registered jobs for diagnostics. The
// scheduler must not have been started yet — same lifecycle constraint
// as [scheduler.Scheduler.Add].
func RegisterStandardJobs(unit *Unit, cfg StandardJobs) ([]string, error) { //nolint:gocognit,gocyclo,funlen // composition/wiring: long sequential setup
	if unit == nil {
		return nil, errors.New("central: nil unit")
	}
	if unit.Scheduler == nil {
		return nil, errors.New("central: nil scheduler")
	}

	registered := make([]string, 0, 4)

	// Health-heartbeat: stamps the per-central component as healthy
	// while the state machine reports Running. This complements the
	// bus-driven WireHealth path (event-triggered samples) with a
	// regular liveness signal so a silent central does not stay
	// "healthy from yesterday's event".
	hbInterval := cfg.HealthHeartbeatInterval
	if hbInterval <= 0 {
		hbInterval = defaultHealthHeartbeat
	}
	// Baseline for the scheduler liveness sample below. TotalFailures is a
	// monotonic counter, so the heartbeat compares the delta against the count
	// seen at the previous tick — only *new* failures degrade the component.
	lastSchedulerFailures := unit.Scheduler.TotalFailures()
	if err := unit.Scheduler.Add(scheduler.Job{
		Name:       "central.health_heartbeat",
		Interval:   hbInterval,
		RunOnStart: false,
		Run: func(_ context.Context) error {
			running := unit.StateMachine != nil && unit.StateMachine.State() == hmenum.CentralStateRunning
			// Zero registered clients means the central is still in its
			// gated-startup wait — the southbound bring-up only registers
			// clients once the CCU reports ready. That is a "starting" state,
			// not a failure, so keep the critical `central` component healthy
			// rather than flapping /health to 503 while a slow CCU boots. A
			// genuine outage always leaves clients registered (disconnected),
			// which still reports unhealthy below.
			startingUp := unit.Clients == nil || len(unit.Clients.List()) == 0
			unit.Health.Record("central", health.Sample{
				Healthy: running || startingUp,
				Note:    "heartbeat",
			})
			// Per-interface liveness sample. The CCU only pushes
			// ClientStateChanged when something actually changes, so
			// a happily-connected interface would otherwise let its
			// last sample age past the tracker's StaleAfter cutoff
			// (90 s by default) and decay to UNKNOWN. Emitting one
			// healthy sample per heartbeat keeps the verdict accurate
			// without piggybacking on the much-quieter event stream.
			if unit.Clients != nil {
				for _, entry := range unit.Clients.List() {
					if entry.Client == nil {
						continue
					}
					connected := entry.Client.ClientState() == hmenum.ClientStateConnected
					unit.Health.Record(entry.InterfaceID, health.Sample{
						Healthy: connected,
						Note:    "heartbeat",
					})
				}
			}
			// Scheduler liveness. A job that accrued new failures since the
			// previous heartbeat marks the `scheduler` component degraded for
			// this tick; a quiet interval restores it. The delta — not the
			// absolute monotonic count — is what reflects *recent* trouble,
			// complementing the cumulative scheduler.failures gauge.
			currentSchedulerFailures := unit.Scheduler.TotalFailures()
			unit.Health.Record("scheduler", health.Sample{
				Healthy: currentSchedulerFailures == lastSchedulerFailures,
				Note:    "heartbeat",
			})
			lastSchedulerFailures = currentSchedulerFailures
			return nil
		},
	}); err != nil {
		return registered, fmt.Errorf("central: register heartbeat job: %w", err)
	}
	registered = append(registered, "central.health_heartbeat")

	// check_connection: per-interface liveness poll. Iterates over every
	// registered client and emits [hmevent.ConnectionLostEvent] for any
	// interface whose callback channel is stale or whose client state is not
	// CONNECTED. A negative interval disables the job without error so callers
	// that want the old behaviour (pure push-only, no polling) can opt out.
	ccInterval := cfg.CheckConnectionInterval
	if ccInterval == 0 {
		ccInterval = defaultCheckConnectionSlot
	}
	if ccInterval > 0 && unit.Clients != nil {
		if err := unit.Scheduler.Add(scheduler.Job{
			Name:       "central.check_connection",
			Interval:   ccInterval,
			RunOnStart: false,
			Run: func(ctx context.Context) error {
				centralName := unit.cfg.Name
				for _, entry := range unit.Clients.List() {
					if entry.Client == nil {
						continue
					}
					// Probe the CCU on every tick so the circuit
					// breaker advances OPEN → HALF_OPEN → CLOSED on
					// its own. Without this the CB only refreshes
					// when an unrelated code path happens to call
					// `Do(...)` — and an OPEN breaker rejects every
					// non-bypass call, so a quiet daemon can sit on
					// a stale OPEN state for minutes. `ping` is on
					// the breaker's bypass list, so this is the one
					// call that always reaches the CCU.
					// CUxD speaks BIN-RPC and is push-only: its liveness is
					// the BIN-RPC connection + inbound callbacks, not an
					// XML-RPC-style ping. Probing it with `ping` (as the
					// other interfaces use) is a false negative, so skip the
					// ping check for CUxD and judge it by client state +
					// callbacks — matching the per-interface checker, which is
					// deliberately not started for CUxD.
					pushOnly := entry.Interface == hmenum.InterfaceCUxD
					// Probe with ping-pong tracking enabled: the periodic
					// keepalive is exactly where outbound PINGs should be
					// recorded so the matching PONG callbacks correlate and
					// drive mismatch / health-degradation detection. The
					// client gates the tracking on its own ping-pong
					// capability, so non-ping-pong backends fall through to a
					// plain probe.
					alive := pushOnly || entry.Client.CheckConnectionAvailability(ctx, true)
					connected := entry.Client.ClientState() == hmenum.ClientStateConnected
					callbackAlive := entry.Client.IsCallbackAlive()
					if alive && connected && callbackAlive {
						continue
					}
					reason := hmenum.FailureReasonUnknown
					if !alive {
						reason = hmenum.FailureReasonNetwork
					} else if !connected {
						reason = hmenum.FailureReasonNetwork
					} else if !callbackAlive {
						reason = hmenum.FailureReasonTimeout
					}
					events.Publish(unit.EventBus, hmevent.ConnectionLostEvent{
						CentralName: centralName,
						InterfaceID: entry.InterfaceID,
						Reason:      reason,
					})
				}
				return nil
			},
		}); err != nil {
			return registered, fmt.Errorf("central: register check_connection job: %w", err)
		}
		registered = append(registered, "central.check_connection")
	}

	// All jobs below are gated: they only run when the central is
	// RUNNING or DEGRADED and when there is no connection issue
	// . The connection-check job above is exempt from the
	// connection-issue gate because it is responsible for detecting
	// issues in the first place.

	if cfg.HubConnectivityRefresh != nil {
		interval := cfg.HubConnectivityInterval
		if interval <= 0 {
			interval = defaultHubConnectivity
		}
		if err := unit.Scheduler.Add(scheduler.Job{
			Name:     "hub.connectivity_refresh",
			Interval: interval,
			Run:      gatedRunWithDevicesCreatedGate(unit, true, cfg.HubConnectivityRefresh),
		}); err != nil {
			return registered, fmt.Errorf("central: register connectivity job: %w", err)
		}
		registered = append(registered, "hub.connectivity_refresh")
	}

	if cfg.HubMetricsRefresh != nil {
		interval := cfg.HubMetricsInterval
		if interval <= 0 {
			interval = defaultHubMetrics
		}
		if err := unit.Scheduler.Add(scheduler.Job{
			Name:     "hub.metrics_refresh",
			Interval: interval,
			Run:      gatedRunWithDevicesCreatedGate(unit, true, cfg.HubMetricsRefresh),
		}); err != nil {
			return registered, fmt.Errorf("central: register metrics job: %w", err)
		}
		registered = append(registered, "hub.metrics_refresh")
	}

	if cfg.LastEventAgeRefresh != nil {
		interval := cfg.LastEventAgeInterval
		if interval <= 0 {
			interval = defaultLastEventAge
		}
		if err := unit.Scheduler.Add(scheduler.Job{
			Name:     "hub.last_event_age_refresh",
			Interval: interval,
			Run:      gatedRunWithDevicesCreatedGate(unit, true, cfg.LastEventAgeRefresh),
		}); err != nil {
			return registered, fmt.Errorf("central: register last_event_age job: %w", err)
		}
		registered = append(registered, "hub.last_event_age_refresh")
	}

	if cfg.FirmwareUpdateCheck != nil {
		interval := cfg.FirmwareUpdateCheckInterval
		if interval <= 0 {
			interval = defaultFirmwareCheckSlot
		}
		if err := unit.Scheduler.Add(scheduler.Job{
			Name:     "central.firmware_check",
			Interval: interval,
			Run:      gatedRunWithDevicesCreatedGate(unit, true, cfg.FirmwareUpdateCheck),
		}); err != nil {
			return registered, fmt.Errorf("central: register firmware job: %w", err)
		}
		registered = append(registered, "central.firmware_check")
	}

	type extraJobSpec struct {
		name              string
		fn                func(context.Context) error
		interval          time.Duration
		dflt              time.Duration
		needsDevicesReady bool
	}
	for _, j := range []extraJobSpec{
		// firmware_delivery_check / firmware_updating_check track CCU-side
		// firmware-update transactions (queued / in-progress) — these can
		// fire even before the daemon has hydrated its own device list, so
		// they only need the operational gate. central.firmware_check
		// (above) hydrates the per-device firmware tracker and therefore
		// needs the devices-created gate; the other two do not.
		{"central.firmware_delivery_check", cfg.FirmwareDeliveringCheck, cfg.FirmwareDeliveringCheckInterval, defaultFirmwareDeliverySlot, false},
		{"central.firmware_updating_check", cfg.FirmwareUpdatingCheck, cfg.FirmwareUpdatingCheckInterval, defaultFirmwareUpdatingSlot, false},
		{"hub.program_refresh", cfg.ProgramRefresh, cfg.ProgramRefreshInterval, defaultProgramRefreshSlot, true},
		{"hub.sysvar_refresh", cfg.SysvarRefresh, cfg.SysvarRefreshInterval, defaultSysvarRefreshSlot, true},
		{"hub.inbox_refresh", cfg.InboxRefresh, cfg.InboxRefreshInterval, defaultInboxRefreshSlot, true},
		{"hub.service_messages_refresh", cfg.ServiceMessagesRefresh, cfg.ServiceMessagesRefreshInterval, defaultServiceMessagesSlot, true},
		{"hub.alarm_messages_refresh", cfg.AlarmMessagesRefresh, cfg.AlarmMessagesRefreshInterval, defaultAlarmMessagesSlot, true},
		{"hub.system_update_refresh", cfg.SystemUpdateRefresh, cfg.SystemUpdateRefreshInterval, defaultSystemUpdateSlot, true},
		{"hub.install_mode_refresh", cfg.InstallModeRefresh, cfg.InstallModeRefreshInterval, defaultInstallModeSlot, true},
	} {
		if j.fn == nil {
			continue
		}
		interval := j.interval
		if interval <= 0 {
			interval = j.dflt
		}
		runFn := gatedRun(unit, true, j.fn)
		if j.needsDevicesReady {
			runFn = gatedRunWithDevicesCreatedGate(unit, true, j.fn)
		}
		if err := unit.Scheduler.Add(scheduler.Job{
			Name:     j.name,
			Interval: interval,
			Run:      runFn,
		}); err != nil {
			return registered, fmt.Errorf("central: register %s: %w", j.name, err)
		}
		registered = append(registered, j.name)
	}

	// RefreshClientData: periodic full VALUES paramset sweep — re-fetches
	// all data-point values from the CCU to catch any push events that
	// Were missed.
	// (central/scheduler.py).
	//
	// Provide a default implementation when the caller leaves this
	// slot nil so that the sweep always runs even without custom wiring.
	// The default delegates to [Unit.LoadAndRefreshDataPointData]
	// and wraps it in DataRefreshTriggeredEvent / DataRefreshCompletedEvent
	// Bookends
	// (central/scheduler.py:693).
	if cfg.RefreshClientData == nil {
		cfg.RefreshClientData = defaultRefreshClientData(unit)
	}
	if cfg.RefreshClientData != nil {
		interval := cfg.RefreshClientDataInterval
		if interval <= 0 {
			interval = defaultRefreshClientDataSlot
		}
		if err := unit.Scheduler.Add(scheduler.Job{
			Name:     "central.refresh_client_data",
			Interval: interval,
			Run:      gatedRun(unit, true, cfg.RefreshClientData),
		}); err != nil {
			return registered, fmt.Errorf("central: register refresh_client_data job: %w", err)
		}
		registered = append(registered, "central.refresh_client_data")
	}

	if cfg.Reconcile != nil {
		interval := cfg.ReconcileInterval
		if interval <= 0 {
			interval = defaultReconcileSlot
		}
		if err := unit.Scheduler.Add(scheduler.Job{
			Name:     "central.reconcile",
			Interval: interval,
			Run:      gatedRun(unit, false, cfg.Reconcile),
		}); err != nil {
			return registered, fmt.Errorf("central: register reconcile job: %w", err)
		}
		registered = append(registered, "central.reconcile")
	}

	return registered, nil
}
