// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
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
	// ZoneHealth is the optional zone-scoped counterpart of Health; see
	// [ZoneHealthFunc].
	ZoneHealth ZoneHealthFunc
	Logger     *slog.Logger
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

// Notification is one notification output's fire signal.
//
// It carries everything a delivery plane needs to render the alert,
// because the sink runs inside the engine's fire path: the manager
// invokes it with the engine lock held (engine.OutputPort), so an
// implementation that resolved the zone name or the incident sources
// off the engine would deadlock the alarm system on the first
// notification output an operator enrols.
type Notification struct {
	Row      sqlitestore.AlarmOutputRow
	Config   OutputConfig
	Incident sqlitestore.AlarmIncident
	// ZoneName and Sources are the engine's snapshot of the zone at
	// fire time (engine.FireOptions).
	ZoneName string
	Sources  []hmevent.SecuritySourceRef
}

// NotificationSink consumes one notification output firing for an
// incident. Implementations must not call back into the manager, and
// must not call an engine verb (see [Notification]).
type NotificationSink func(n Notification)

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
	clk        clock.Clock
	sched      engine.TimerScheduler
	resolver   DeviceResolver
	ledger     IncidentLedger
	journal    engine.Journal
	rows       OutputRowSource
	health     HealthFunc
	zoneHealth ZoneHealthFunc
	notify     NotificationSink
	log        *slog.Logger

	defaultSiren     time.Duration
	maxPerIncident   time.Duration
	stopVerifyWindow time.Duration

	mu        sync.Mutex
	byZone    map[string][]*instance
	active    map[string]*activation // by output ID
	demands   map[string]demandRec   // by output ID; see arbitration.go
	lastChirp map[string]time.Time
	// failed maps each output ID with an outstanding, unresolved failure
	// (a failed fire, a failed stop write, an unverified stop) to the
	// zone it belongs to. Alarm health is the worst outstanding
	// condition, not the last sample: an output is added on failure and
	// removed only when a verified stop of that same output confirms it
	// is safely inactive, so a verified stop of an unrelated output can
	// never erase the degradation a still-failed output owns (S7). The
	// zone value drives the per-zone panel signal (ZoneHealth) the same
	// way the key set drives the fleet-wide one (Health).
	failed map[string]string // output ID -> zone ID
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
		zoneHealth:       cfg.ZoneHealth,
		notify:           cfg.Notify,
		log:              logger,
		defaultSiren:     cfg.DefaultSirenDuration,
		maxPerIncident:   cfg.MaxAcousticPerIncident,
		stopVerifyWindow: cfg.StopVerifyWindow,
		byZone:           map[string][]*instance{},
		active:           map[string]*activation{},
		demands:          map[string]demandRec{},
		lastChirp:        map[string]time.Time{},
		failed:           map[string]string{},
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
	byZone := map[string][]*instance{}
	for i := range rows {
		row := rows[i]
		cfg, err := ParseOutputConfig(row.ConfigJSON)
		if err != nil {
			return fmt.Errorf("outputs: output %q: %w", row.ID, err)
		}
		byZone[row.ZoneID] = append(byZone[row.ZoneID], &instance{row: row, cfg: cfg})
	}
	for _, list := range byZone {
		sort.Slice(list, func(i, j int) bool { return list[i].row.ID < list[j].row.ID })
	}
	m.mu.Lock()
	m.byZone = byZone
	m.mu.Unlock()
	rowIDs := make(map[string]struct{}, len(rows))
	for i := range rows {
		rowIDs[rows[i].ID] = struct{}{}
	}
	m.pruneDemands(rowIDs)
	m.pruneFailed(rowIDs)
	return nil
}

// FireCycle implements engine.OutputPort: one bounded output cycle
// for the incident. Acoustic accounting is written to the incident
// ledger before each device write (S1 over-count direction); a
// per-output failure is journaled and joined into the returned error,
// but never stops the remaining outputs from firing.
func (m *Manager) FireCycle(ctx context.Context, zoneID string, incident sqlitestore.AlarmIncident, opts engine.FireOptions) error {
	m.mu.Lock()
	instances := append([]*instance(nil), m.byZone[zoneID]...)
	m.mu.Unlock()

	eligible := make([]*instance, 0, len(instances))
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
		eligible = append(eligible, inst)
	}
	// The siren rows are grouped by channel first: an ASIR takes tone,
	// pattern and duration in one atomic paramset and ignores partial
	// writes, so two rows on one channel cannot fire independently.
	channels := groupSirenChannels(eligible)

	remaining := m.acousticBudget(ctx, incident)
	var errs []error
	for _, inst := range eligible {
		var err error
		covered := []*instance{inst}
		switch inst.row.Class {
		case hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren:
			ch := channels[demandChannelKey(inst.row.CentralName, inst.row.ChannelAddress)]
			if ch.rows[0] != inst {
				continue // already written with the first row of its channel
			}
			err = m.fireSirenChannel(ctx, ch, incident.ID, &remaining)
			covered = ch.rows
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
				m.notify(Notification{
					Row: inst.row, Config: inst.cfg, Incident: incident,
					ZoneName: opts.ZoneName, Sources: opts.Sources,
				})
			}
		case hmenum.AlarmOutputClassSysvarMirror, hmenum.AlarmOutputClassChirp:
			// The sysvar mirror is state-driven, chirps have their
			// own path.
		}
		if err == nil {
			continue
		}
		// One write can carry several rows; each of them stayed dark,
		// so each is reported.
		for _, member := range covered {
			m.outputFailed(ctx, zoneID, "output_fire_failed", member.row.ID, incident.ID, err)
			errs = append(errs, fmt.Errorf("output %s: %w", member.row.ID, err))
		}
	}
	return errors.Join(errs...)
}

// sirenChannel is the set of eligible siren rows of one cycle that
// address the same physical channel.
//
// The pairing is the design-intended one — an operator enrolls an ASIR
// channel as an acoustic siren and as an optical siren, the optical row
// being the one the concept lets run its own way — but the hardware
// takes both halves plus the duration in a single VALUES paramset. Two
// independent writes therefore do not add up: the second replaces the
// first, and the optical write, which pins the acoustic half to the
// device's disable entry so it cannot start a tone of its own, silences
// the acoustic row on its own channel. Both writes succeed, so the
// alarm flashes without a sound and nothing reports it.
type sirenChannel struct {
	// rows keeps the cycle's enrollment order; rows[0] is the row the
	// merged write is attributed to.
	rows     []*instance
	acoustic *instance
	optical  *instance
}

// groupSirenChannels indexes the cycle's eligible siren rows by
// physical channel. Rows of other classes are left out; every siren row
// of eligible is present, which is what lets the fire loop look its
// group up unconditionally.
func groupSirenChannels(eligible []*instance) map[string]*sirenChannel {
	channels := map[string]*sirenChannel{}
	for _, inst := range eligible {
		if !sirenClass(inst.row.Class) {
			continue
		}
		key := demandChannelKey(inst.row.CentralName, inst.row.ChannelAddress)
		ch, ok := channels[key]
		if !ok {
			ch = &sirenChannel{}
			channels[key] = ch
		}
		ch.rows = append(ch.rows, inst)
		// A second row of the same class on one channel adds nothing to
		// the write; it still gets its demand and its watchdog.
		if inst.row.Class == hmenum.AlarmOutputClassAcousticSiren && ch.acoustic == nil {
			ch.acoustic = inst
		}
		if inst.row.Class == hmenum.AlarmOutputClassOpticalSiren && ch.optical == nil {
			ch.optical = inst
		}
	}
	return channels
}

// sirenClass reports whether the class activates through the shared
// ASIR channel write.
func sirenClass(class hmenum.AlarmOutputClass) bool {
	return class == hmenum.AlarmOutputClassAcousticSiren || class == hmenum.AlarmOutputClassOpticalSiren
}

// StopAll implements engine.OutputPort: silence every sounding output
// of the zone at critical priority. Notification outputs are never
// touched; stopping more than necessary is the safe direction, so
// every stoppable class is addressed regardless of activation
// records.
func (m *Manager) StopAll(ctx context.Context, zoneID string, incidentID int64) error {
	m.mu.Lock()
	instances := append([]*instance(nil), m.byZone[zoneID]...)
	m.mu.Unlock()

	var errs []error
	for _, inst := range instances {
		switch inst.row.Class {
		case hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren,
			hmenum.AlarmOutputClassSwitchedSiren, hmenum.AlarmOutputClassSmokeSounder,
			hmenum.AlarmOutputClassAlarmLight, hmenum.AlarmOutputClassChirp:
			if err := m.stopAndVerify(ctx, inst, incidentID); err != nil {
				m.outputFailed(ctx, zoneID, "output_stop_failed", inst.row.ID, incidentID, err)
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

// fireSirenChannel activates one ASIR-class channel for every eligible
// row addressing it: a single atomic paramset write carrying
// tone/pattern plus a finite duration. Acoustic activations reserve
// ledger budget; optical-only activations are bounded but not budgeted
// (no noise constraint).
func (m *Manager) fireSirenChannel(ctx context.Context, ch *sirenChannel, incidentID int64, remaining *time.Duration) error {
	lead := ch.rows[0]
	dev, err := m.resolver.Siren(lead.row.CentralName, lead.row.ChannelAddress)
	if err != nil {
		return err
	}
	on, fires, err := m.sirenOnConfig(ctx, dev, ch, incidentID, remaining)
	if err != nil || !fires {
		return err
	}
	if err := dev.TurnOn(ctx, on, hmenum.CommandPriorityHigh); err != nil {
		return err
	}
	for _, inst := range ch.rows {
		m.noteDemand(inst)
		// The device holds one duration for both halves, so every row
		// of the channel is watchdogged against the duration actually
		// written — the earliest stop silences the channel, which is
		// the safe direction.
		m.armStopWatchdog(inst, incidentID, on.Duration,
			m.sirenStopper(inst, inst.row.Class == hmenum.AlarmOutputClassAcousticSiren))
	}
	return nil
}

// sirenOnConfig builds the channel's single activation write. It
// reports false when nothing is left to activate (the acoustic budget
// is spent and no optical row shares the channel).
//
// When both halves fire, the acoustic bound governs the write: the
// device applies one duration to both, and stretching it to the
// optical row's would sound the tone past the budget reserved for it.
func (m *Manager) sirenOnConfig(
	ctx context.Context, dev SirenDevice, ch *sirenChannel,
	incidentID int64, remaining *time.Duration,
) (sirencdp.OnConfig, bool, error) {
	var on sirencdp.OnConfig
	acousticFires := false
	if a := ch.acoustic; a != nil {
		d, err := m.reserveAcoustic(ctx, incidentID, remaining, a.cfg.acousticDuration(m.defaultSiren))
		if err != nil {
			return on, false, err
		}
		if d <= 0 {
			// An exhausted noise budget silences the tone; it does not
			// stop an optical row sharing the channel from flashing.
			m.journalFault(ctx, a.row.ZoneID, "acoustic_budget_exhausted", a.row.ID, incidentID, nil)
		} else {
			acousticFires = true
			on.Duration = d
			if a.cfg.AcousticTone != "" {
				// The selection pointer is what reaches the wire; the
				// tone field only opts into value-list validation.
				tone := a.cfg.AcousticTone
				on.AcousticSelection = &tone
				on.AcousticTone = tone
			}
			if a.cfg.OpticalPattern != "" {
				p := a.cfg.OpticalPattern
				on.OpticalSelection = &p
			}
		}
	}
	if o := ch.optical; o != nil {
		if !acousticFires {
			on.Duration = o.cfg.opticalDuration()
			// No tone in this write: pin the acoustic selection to the
			// device's disable default so the atomic write cannot
			// re-trigger one (partial paramset writes are ignored by
			// the hardware).
			if tones := dev.AvailableTones(); len(tones) > 0 {
				off := tones[0]
				on.AcousticSelection = &off
			}
		}
		p := o.cfg.OpticalPattern
		if p == "" && on.OpticalSelection == nil {
			if lights := dev.AvailableLights(); len(lights) > 1 {
				p = lights[len(lights)-1]
			}
		}
		if p != "" {
			on.OpticalSelection = &p
		}
	}
	return on, acousticFires || ch.optical != nil, nil
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
		m.journalFault(ctx, inst.row.ZoneID, "acoustic_budget_exhausted", inst.row.ID, incidentID, nil)
		return nil
	}
	if err := dev.TurnOnBounded(ctx, d, inst.cfg.Level, hmenum.CommandPriorityHigh); err != nil {
		return err
	}
	m.noteDemand(inst)
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
		m.journalFault(ctx, inst.row.ZoneID, "acoustic_budget_exhausted", inst.row.ID, incidentID, nil)
		return nil
	}
	// Watchdog first: if the activation write succeeds but the
	// process dies before scheduling, nothing bounds this device.
	m.noteDemand(inst)
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
	if err := dev.TurnOnSteady(ctx, inst.cfg.Level, hmenum.CommandPriorityHigh); err != nil {
		return err
	}
	// Steady-on has no watchdog — the demand is released by the
	// eventual StopAll (or pruned when the row disappears).
	m.noteDemand(inst)
	return nil
}

// classEligible applies the class filters of the cycle. The
// restrictions intersect: a degraded pre-alarm cycle fires only what
// both admit.
func classEligible(class hmenum.AlarmOutputClass, opts engine.FireOptions) bool {
	if opts.Degraded && !degradedClass(class) {
		return false
	}
	if opts.PreAlarm && !preAlarmClass(class) {
		return false
	}
	if opts.Policy.Silent && class.Acoustic() {
		return false
	}
	if class == hmenum.AlarmOutputClassSmokeSounder && !opts.Policy.SmokeSounders {
		return false
	}
	return true
}

// degradedClass reports whether the class survives the restart-loop
// breaker: optical + light + notification only.
func degradedClass(class hmenum.AlarmOutputClass) bool {
	switch class {
	case hmenum.AlarmOutputClassOpticalSiren, hmenum.AlarmOutputClassAlarmLight,
		hmenum.AlarmOutputClassNotification, hmenum.AlarmOutputClassSysvarMirror:
		return true
	default:
		return false
	}
}

// preAlarmClass reports whether the class belongs to the pre-alarm
// phase (chirp + notification + light, plus the state-driven sysvar
// mirror so the mirrored state stays consistent).
//
// Every siren class is excluded on purpose. The pre-alarm window exists
// so a resident can silence a false alarm before the sirens sound; a
// phase that already fires them is not a warning window, and it burns
// the incident's acoustic budget twice over — once at second 0 and
// again when the phase escalates.
func preAlarmClass(class hmenum.AlarmOutputClass) bool {
	switch class {
	case hmenum.AlarmOutputClassChirp, hmenum.AlarmOutputClassNotification,
		hmenum.AlarmOutputClassAlarmLight, hmenum.AlarmOutputClassSysvarMirror:
		return true
	default:
		return false
	}
}

// journalFault records a driver fault (fail-visible, S7).
func (m *Manager) journalFault(ctx context.Context, zoneID, event, outputID string, incidentID int64, cause error) {
	m.journalEntry(ctx, hmenum.AlarmJournalClassFault, zoneID, event, outputID, incidentID, cause)
}

// journalEntry appends one output-scoped journal entry under class.
func (m *Manager) journalEntry(
	ctx context.Context, class hmenum.AlarmJournalClass,
	zoneID, event, outputID string, incidentID int64, cause error,
) {
	details := map[string]any{"output_id": outputID}
	if cause != nil {
		details["error"] = cause.Error()
	}
	if _, err := m.journal.Append(ctx, engine.JournalEntry{
		ZoneID: zoneID, Class: class, Event: event,
		IncidentID: incidentID, Details: details,
	}); err != nil {
		m.log.Error("alarm output journal append failed", "zone", zoneID, "event", event, "error", err)
	}
}

// outputFailed records a failed output command on both the surfaces S7
// requires: the journal, which is what an operator reads afterwards, and
// the health signal, which is what tells them to look at all.
//
// The two halves have to move together. A command that only journals
// leaves /api/v1/health reporting the alarm domain healthy while a siren
// did not go off, and that is the exact shape of the failure S7 exists
// to prevent — an alarm quietly non-functional for weeks.
func (m *Manager) outputFailed(
	ctx context.Context, zoneID, event, outputID string, incidentID int64, cause error,
) {
	m.journalFault(ctx, zoneID, event, outputID, incidentID, cause)
	note := "alarm output " + outputID + " " + event
	if cause != nil {
		note += ": " + cause.Error()
	}
	m.noteFailure(outputID, zoneID, note)
}

// zoneStillFailedLocked reports whether any output of zoneID still
// carries an outstanding failure. The caller holds m.mu.
func (m *Manager) zoneStillFailedLocked(zoneID string) bool {
	for _, z := range m.failed {
		if z == zoneID {
			return true
		}
	}
	return false
}

// noteFailure records an outstanding failure for outputID and emits the
// degradation, fleet-wide and — for the panel projection — scoped to
// zoneID alone. The output stays in the outstanding-failure set until a
// verified stop of that same output resolves it (see resolveFailure), so
// a later success on an unrelated output cannot erase this degradation —
// alarm health reflects the worst outstanding condition, not the last
// sample (S7).
func (m *Manager) noteFailure(outputID, zoneID, note string) {
	m.mu.Lock()
	m.failed[outputID] = zoneID
	m.mu.Unlock()
	if m.health != nil {
		m.health(false, note)
	}
	if m.zoneHealth != nil {
		m.zoneHealth(zoneID, false)
	}
}

// resolveFailure clears any outstanding failure of outputID — a verified
// stop confirms that output is safely inactive — and reports recovery
// only when no other output still carries an outstanding failure. A
// verified stop of one output must never clear the degradation another
// failed output still owns: a siren whose fire failed and never sounded
// stays degraded until its own condition resolves, not until any
// unrelated stop verifies. The same rule applies per zone for the panel
// projection: zoneID only reports recovered once none of ITS outputs
// are still failed, regardless of another zone's condition.
func (m *Manager) resolveFailure(outputID, zoneID string) {
	m.mu.Lock()
	delete(m.failed, outputID)
	recovered := len(m.failed) == 0
	zoneRecovered := !m.zoneStillFailedLocked(zoneID)
	m.mu.Unlock()
	if recovered && m.health != nil {
		m.health(true, "alarm output stop verified")
	}
	if zoneRecovered && m.zoneHealth != nil {
		m.zoneHealth(zoneID, true)
	}
}

// pruneFailed drops outstanding failures whose enrolled row no longer
// exists after a Reload (the operator deleted the failing output),
// mirroring pruneDemands for the health-degradation side of a removed
// row. Without this a failure can never resolve once its row is gone —
// resolveFailure only runs from a verified stop of that same output,
// and a deleted row's watchdog and stop never run again — so the
// domain would stay degraded, naming a device that no longer exists,
// until the daemon restarts.
//
// The health signal only fires when this prune is what empties the
// set: Reload runs after every alarm management write, not only
// output changes, so an unconditional health(true) here would
// republish "healthy" on every unrelated config save.
func (m *Manager) pruneFailed(rowIDs map[string]struct{}) {
	m.mu.Lock()
	pruned := false
	prunedZones := map[string]bool{}
	for id, zoneID := range m.failed {
		if _, ok := rowIDs[id]; !ok {
			delete(m.failed, id)
			pruned = true
			prunedZones[zoneID] = true
		}
	}
	recovered := pruned && len(m.failed) == 0
	var zonesRecovered []string
	for zoneID := range prunedZones {
		if !m.zoneStillFailedLocked(zoneID) {
			zonesRecovered = append(zonesRecovered, zoneID)
		}
	}
	m.mu.Unlock()
	if recovered && m.health != nil {
		m.health(true, "alarm output removed while degraded")
	}
	if m.zoneHealth != nil {
		for _, zoneID := range zonesRecovered {
			m.zoneHealth(zoneID, true)
		}
	}
}

// noopJournal drops entries (unwired manager in tests).
type noopJournal struct{}

func (noopJournal) Append(context.Context, engine.JournalEntry) (int64, error) { return 0, nil }
