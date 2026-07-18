// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Config wires a Manager.
type Config struct {
	Clock     clock.Clock
	Scheduler engine.TimerScheduler
	Resolver  DeviceResolver
	Ledger    IncidentLedger
	Journal   engine.Journal
	Rows      OutputRowSource
	Health    HealthFunc
	Logger    *slog.Logger
	// DefaultSirenDuration bounds acoustic activations whose output
	// does not configure one (engine default, typically 180 s).
	DefaultSirenDuration time.Duration
	// MaxAcousticPerIncident is the cumulative acoustic budget of one
	// incident (S1 cumulative bound); activations clamp to the
	// remaining budget and stop entirely when it is exhausted.
	MaxAcousticPerIncident time.Duration
	// StopVerifyWindow bounds how long a stop is retried at critical
	// priority before it converts into a health incident (S2). Sized
	// beyond the transport's duty-cycle retry latency.
	StopVerifyWindow time.Duration
	// Notify receives each notification output's fire signal (the
	// composition root publishes it on the alarm event bus). Nil
	// drops the signal.
	Notify NotificationSink
}

// NotificationSink consumes one notification output firing for an
// incident. Implementations must not call back into the manager.
type NotificationSink func(row sqlitestore.AlarmOutputRow, cfg OutputConfig, incident sqlitestore.AlarmIncident)

// instance is one enrolled output with its parsed configuration.
type instance struct {
	row sqlitestore.AlarmOutputRow
	cfg OutputConfig
}

// Manager implements the engine's OutputPort against the enrolled
// output rows. All device I/O runs synchronously in the port calls;
// watchdog stops run on scheduler callbacks. The manager never calls
// back into engine verbs (OutputPort contract).
type Manager struct {
	clk      clock.Clock
	sched    engine.TimerScheduler
	resolver DeviceResolver
	ledger   IncidentLedger
	journal  engine.Journal
	rows     OutputRowSource
	health   HealthFunc
	notify   NotificationSink
	log      *slog.Logger

	defaultSiren     time.Duration
	maxPerIncident   time.Duration
	stopVerifyWindow time.Duration

	mu        sync.Mutex
	byArea    map[string][]*instance
	active    map[string]*activation // by output ID
	lastChirp map[string]time.Time
}

// NewManager constructs the driver layer. Call Reload before use.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.Resolver == nil || cfg.Ledger == nil || cfg.Rows == nil {
		return nil, errors.New("outputs: missing required dependency")
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.New()
	}
	sched := cfg.Scheduler
	if sched == nil {
		sched = engine.NewClockScheduler(clk)
	}
	journal := cfg.Journal
	if journal == nil {
		journal = noopJournal{}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{
		clk:              clk,
		sched:            sched,
		resolver:         cfg.Resolver,
		ledger:           cfg.Ledger,
		journal:          journal,
		rows:             cfg.Rows,
		health:           cfg.Health,
		notify:           cfg.Notify,
		log:              logger,
		defaultSiren:     cfg.DefaultSirenDuration,
		maxPerIncident:   cfg.MaxAcousticPerIncident,
		stopVerifyWindow: cfg.StopVerifyWindow,
		byArea:           map[string][]*instance{},
		active:           map[string]*activation{},
		lastChirp:        map[string]time.Time{},
	}
	if m.defaultSiren <= 0 {
		m.defaultSiren = 180 * time.Second
	}
	if m.maxPerIncident <= 0 {
		m.maxPerIncident = 900 * time.Second
	}
	if m.stopVerifyWindow <= 0 {
		m.stopVerifyWindow = 120 * time.Second
	}
	return m, nil
}

// Reload (re)loads the enrolled output rows.
func (m *Manager) Reload(ctx context.Context) error {
	rows, err := m.rows.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("outputs: load rows: %w", err)
	}
	byArea := map[string][]*instance{}
	for i := range rows {
		row := rows[i]
		cfg, err := ParseOutputConfig(row.ConfigJSON)
		if err != nil {
			return fmt.Errorf("outputs: output %q: %w", row.ID, err)
		}
		byArea[row.AreaID] = append(byArea[row.AreaID], &instance{row: row, cfg: cfg})
	}
	for _, list := range byArea {
		sort.Slice(list, func(i, j int) bool { return list[i].row.ID < list[j].row.ID })
	}
	m.mu.Lock()
	m.byArea = byArea
	m.mu.Unlock()
	return nil
}

// FireCycle implements engine.OutputPort: one bounded output cycle
// for the incident. Acoustic accounting is written to the incident
// ledger before each device write (S1 over-count direction); a
// per-output failure is journaled and joined into the returned error,
// but never stops the remaining outputs from firing.
func (m *Manager) FireCycle(ctx context.Context, areaID string, incident sqlitestore.AlarmIncident, opts engine.FireOptions) error {
	m.mu.Lock()
	instances := append([]*instance(nil), m.byArea[areaID]...)
	m.mu.Unlock()

	remaining := m.acousticBudget(ctx, incident)
	var errs []error
	for _, inst := range instances {
		if !inst.cfg.InMode(incident.Mode) {
			continue
		}
		if !classEligible(inst.row.Class, opts) {
			continue
		}
		if inst.cfg.Outdoor && opts.Policy.ExcludeOutdoor {
			continue
		}
		var err error
		switch inst.row.Class {
		case hmenum.AlarmOutputClassAcousticSiren:
			err = m.fireSiren(ctx, inst, incident.ID, &remaining, true)
		case hmenum.AlarmOutputClassOpticalSiren:
			err = m.fireSiren(ctx, inst, incident.ID, &remaining, false)
		case hmenum.AlarmOutputClassSwitchedSiren:
			err = m.fireSwitchedSiren(ctx, inst, incident.ID, &remaining)
		case hmenum.AlarmOutputClassSmokeSounder:
			err = m.fireSmokeSounder(ctx, inst, incident.ID, &remaining)
		case hmenum.AlarmOutputClassAlarmLight:
			err = m.fireAlarmLight(ctx, inst, incident.ID)
		case hmenum.AlarmOutputClassNotification:
			// One-shot fire signal; the composition root fans it out
			// to the enrolled delivery planes. Never stop-tracked and
			// never cancelled by a later silence.
			if m.notify != nil {
				m.notify(inst.row, inst.cfg, incident)
			}
		case hmenum.AlarmOutputClassSysvarMirror, hmenum.AlarmOutputClassChirp:
			// The sysvar mirror is state-driven, chirps have their
			// own path.
		}
		if err != nil {
			m.journalFault(ctx, areaID, "output_fire_failed", inst.row.ID, incident.ID, err)
			errs = append(errs, fmt.Errorf("output %s: %w", inst.row.ID, err))
		}
	}
	return errors.Join(errs...)
}

// StopAll implements engine.OutputPort: silence every sounding output
// of the area at critical priority. Notification outputs are never
// touched; stopping more than necessary is the safe direction, so
// every stoppable class is addressed regardless of activation
// records.
func (m *Manager) StopAll(ctx context.Context, areaID string, incidentID int64) error {
	m.mu.Lock()
	instances := append([]*instance(nil), m.byArea[areaID]...)
	m.mu.Unlock()

	var errs []error
	for _, inst := range instances {
		switch inst.row.Class {
		case hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren,
			hmenum.AlarmOutputClassSwitchedSiren, hmenum.AlarmOutputClassSmokeSounder,
			hmenum.AlarmOutputClassAlarmLight, hmenum.AlarmOutputClassChirp:
			if err := m.stopAndVerify(ctx, inst, incidentID); err != nil {
				m.journalFault(ctx, areaID, "output_stop_failed", inst.row.ID, incidentID, err)
				errs = append(errs, fmt.Errorf("output %s: %w", inst.row.ID, err))
			}
		case hmenum.AlarmOutputClassNotification, hmenum.AlarmOutputClassSysvarMirror:
		}
	}
	return errors.Join(errs...)
}

// acousticBudget returns the incident's remaining cumulative acoustic
// budget, preferring a fresh ledger read over the caller's snapshot.
func (m *Manager) acousticBudget(ctx context.Context, incident sqlitestore.AlarmIncident) time.Duration {
	acc := time.Duration(incident.AcousticMS) * time.Millisecond
	if incident.ID != 0 {
		if fresh, ok, err := m.ledger.Get(ctx, incident.ID); err == nil && ok {
			acc = time.Duration(fresh.AcousticMS) * time.Millisecond
		}
	}
	remaining := m.maxPerIncident - acc
	if remaining < 0 {
		remaining = 0
	}
	return remaining
}

// reserveAcoustic clamps d to the remaining budget and accounts it on
// the ledger BEFORE the activation (S1). A zero return means the
// budget is exhausted — the activation must be skipped.
func (m *Manager) reserveAcoustic(ctx context.Context, incidentID int64, remaining *time.Duration, d time.Duration) (time.Duration, error) {
	if *remaining <= 0 {
		return 0, nil
	}
	if d > *remaining {
		d = *remaining
	}
	if incidentID != 0 {
		if err := m.ledger.AddAcousticMS(ctx, incidentID, d.Milliseconds()); err != nil {
			// Accounting failed → do not activate (the safe
			// direction).
			return 0, fmt.Errorf("acoustic ledger write: %w", err)
		}
	}
	*remaining -= d
	return d, nil
}

// fireSiren activates an ASIR-class siren: one atomic paramset write
// carrying tone/pattern plus a finite duration. Acoustic activations
// reserve ledger budget; optical-only activations are bounded but not
// budgeted (no noise constraint).
func (m *Manager) fireSiren(ctx context.Context, inst *instance, incidentID int64, remaining *time.Duration, acoustic bool) error {
	dev, err := m.resolver.Siren(inst.row.CentralName, inst.row.ChannelAddress)
	if err != nil {
		return err
	}
	var on sirencdp.OnConfig
	var d time.Duration
	if acoustic {
		d, err = m.reserveAcoustic(ctx, incidentID, remaining, inst.cfg.acousticDuration(m.defaultSiren))
		if err != nil {
			return err
		}
		if d <= 0 {
			m.journalFault(ctx, inst.row.AreaID, "acoustic_budget_exhausted", inst.row.ID, incidentID, nil)
			return nil
		}
		on.Duration = d
		if inst.cfg.AcousticTone != "" {
			// The selection pointer is what reaches the wire; the tone
			// field only opts into value-list validation.
			tone := inst.cfg.AcousticTone
			on.AcousticSelection = &tone
			on.AcousticTone = tone
		}
		if inst.cfg.OpticalPattern != "" {
			p := inst.cfg.OpticalPattern
			on.OpticalSelection = &p
		}
	} else {
		d = inst.cfg.opticalDuration()
		on.Duration = d
		// Optical-only: pin the acoustic selection to the device's
		// disable default so the atomic write cannot re-trigger a
		// tone (partial paramset writes are ignored by the hardware).
		if tones := dev.AvailableTones(); len(tones) > 0 {
			off := tones[0]
			on.AcousticSelection = &off
		}
		p := inst.cfg.OpticalPattern
		if p == "" {
			if lights := dev.AvailableLights(); len(lights) > 1 {
				p = lights[len(lights)-1]
			}
		}
		if p != "" {
			on.OpticalSelection = &p
		}
	}
	if err := dev.TurnOn(ctx, on, hmenum.CommandPriorityHigh); err != nil {
		return err
	}
	m.armStopWatchdog(inst, incidentID, d, m.sirenStopper(inst, acoustic))
	return nil
}

// fireSwitchedSiren activates a plug-in siren behind an actuator: the
// device-side auto-off travels with the switch-on (S1) and the
// watchdog verifies the off transition (S2).
func (m *Manager) fireSwitchedSiren(ctx context.Context, inst *instance, incidentID int64, remaining *time.Duration) error {
	dev, err := m.resolver.Actuator(inst.row.CentralName, inst.row.ChannelAddress)
	if err != nil {
		return err
	}
	d, err := m.reserveAcoustic(ctx, incidentID, remaining, inst.cfg.acousticDuration(m.defaultSiren))
	if err != nil {
		return err
	}
	if d <= 0 {
		m.journalFault(ctx, inst.row.AreaID, "acoustic_budget_exhausted", inst.row.ID, incidentID, nil)
		return nil
	}
	if err := dev.TurnOnBounded(ctx, d, inst.cfg.Level, hmenum.CommandPriorityHigh); err != nil {
		return err
	}
	m.armStopWatchdog(inst, incidentID, d, m.actuatorStopper(inst))
	return nil
}

// fireSmokeSounder activates smoke-detector sounders. The device has
// no duration parameter and likely fans out to its whole smoke group:
// the engine watchdog is the only bound, so the stop is scheduled
// before the command can be considered successful.
func (m *Manager) fireSmokeSounder(ctx context.Context, inst *instance, incidentID int64, remaining *time.Duration) error {
	dev, err := m.resolver.SmokeSounder(inst.row.CentralName, inst.row.ChannelAddress)
	if err != nil {
		return err
	}
	d, err := m.reserveAcoustic(ctx, incidentID, remaining, inst.cfg.acousticDuration(m.defaultSiren))
	if err != nil {
		return err
	}
	if d <= 0 {
		m.journalFault(ctx, inst.row.AreaID, "acoustic_budget_exhausted", inst.row.ID, incidentID, nil)
		return nil
	}
	// Watchdog first: if the activation write succeeds but the
	// process dies before scheduling, nothing bounds this device.
	m.armStopWatchdog(inst, incidentID, d, m.smokeStopper(inst))
	if err := dev.TurnOn(ctx, hmenum.CommandPriorityHigh); err != nil {
		m.cancelWatchdog(inst.row.ID)
		return err
	}
	return nil
}

// fireAlarmLight switches the alarm light on. The light lifecycle is
// episode-scoped (off at silence/disarm via StopAll); a lingering
// light is annoying, not dangerous, so no hard bound is forced.
func (m *Manager) fireAlarmLight(ctx context.Context, inst *instance, incidentID int64) error {
	dev, err := m.resolver.Actuator(inst.row.CentralName, inst.row.ChannelAddress)
	if err != nil {
		return err
	}
	_ = incidentID
	return dev.TurnOnSteady(ctx, inst.cfg.Level, hmenum.CommandPriorityHigh)
}

// classEligible applies the policy filter of the cycle.
func classEligible(class hmenum.AlarmOutputClass, opts engine.FireOptions) bool {
	if opts.Degraded {
		// Restart-loop breaker: optical + light + notification only.
		switch class {
		case hmenum.AlarmOutputClassOpticalSiren, hmenum.AlarmOutputClassAlarmLight,
			hmenum.AlarmOutputClassNotification, hmenum.AlarmOutputClassSysvarMirror:
			return true
		default:
			return false
		}
	}
	if opts.Policy.Silent && class.Acoustic() {
		return false
	}
	if class == hmenum.AlarmOutputClassSmokeSounder && !opts.Policy.SmokeSounders {
		return false
	}
	return true
}

// journalFault records a driver fault (fail-visible, S7).
func (m *Manager) journalFault(ctx context.Context, areaID, event, outputID string, incidentID int64, cause error) {
	details := map[string]any{"output_id": outputID}
	if cause != nil {
		details["error"] = cause.Error()
	}
	if _, err := m.journal.Append(ctx, engine.JournalEntry{
		AreaID: areaID, Class: hmenum.AlarmJournalClassFault, Event: event,
		IncidentID: incidentID, Details: details,
	}); err != nil {
		m.log.Error("alarm output journal append failed", "area", areaID, "event", event, "error", err)
	}
}

// noopJournal drops entries (unwired manager in tests).
type noopJournal struct{}

func (noopJournal) Append(context.Context, engine.JournalEntry) (int64, error) { return 0, nil }
