// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package alarm wires the alarm engine, its output-driver layer, and
// the per-central event subscriptions into one daemon-level service
// (notes/concepts/alarm-concept.md §14). The service owns a dedicated alarm
// event bus: zones are daemon-level while every central owns its own
// bus, so north-bound surfaces subscribe to the alarm bus instead of
// fanning across centrals.
package alarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	alarmjournal "github.com/SukramJ/openccu-loom/internal/alarm/journal"
	"github.com/SukramJ/openccu-loom/internal/alarm/outputs"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Settings are the global engine settings from the alarm config
// section.
type Settings struct {
	Enabled                       bool
	DefaultSirenSeconds           int
	MaxAcousticPerIncidentSeconds int
	StopVerifySeconds             int
	JournalRetentionDays          int
	RestartLoopBreaker            int
	// MasterPanelName is the localized display name of the aggregate
	// master panel (resolved by the composition root via the i18n
	// catalogs so REST/WS/MQTT agree).
	MasterPanelName string
}

// Deps wires a Service.
type Deps struct {
	Settings Settings
	Registry *central.Registry
	Stores   *Stores
	Clock    clock.Clock
	Logger   *slog.Logger
	// Health receives service-level health transitions (optional).
	Health outputs.HealthFunc
}

// Service is the daemon-level alarm component: engine + output
// drivers + subscriptions + the alarm event bus. It implements the
// north-bridge service lifecycle (Name / Start / Stop).
type Service struct {
	settings Settings
	reg      *central.Registry
	stores   *Stores
	clk      clock.Clock
	log      *slog.Logger
	health   outputs.HealthFunc
	// outputHealth is the driver layer's fleet-wide health fan-out: the
	// alarm bus and the daemon tracker still see every output failure,
	// but — unlike health — it never triggers onPanelHealthEvent, so it
	// cannot blanket-flip every panel's availability. The output
	// manager's failures reach the panel projection exclusively through
	// its zone-scoped ZoneHealth signal (K1).
	outputHealth outputs.HealthFunc

	bus     *events.Bus
	journal *alarmjournal.Journal
	engine  *engine.Engine
	manager *outputs.Manager
	codes   *codes.Facade

	mu      sync.Mutex
	started bool
	unsubs  map[string][]func()      // per central
	dpIndex map[string]sensorBinding // central|iface|channel|param → routing entry
	// enums resolves enumeration indices to labels for sensors that
	// narrow activation via active_values.
	enums        *enumResolver
	devIndex     map[string][]string // central|deviceAddress → sensor IDs
	devHealth    map[string]engine.SensorHealth
	ifaceDown    map[string]map[string]bool // central → interface → unreachable
	sysvarMirror *sysvarMirror
	panels       *panelRegistry
	intents      *intentRouter
	schedules    *scheduleRunner
	codeSource   CodeSource     // hardware-code identities; nil until wired
	armFailure   ArmFailureHook // FAILED_TO_ARM notification hook; nil until wired
	retention    func()         // retention chain cancel
	// configChanged fires after a successful Reload; nil until wired.
	configChanged func(context.Context)
}

// ArmFailureHook is a FAILED_TO_ARM notification callback. It mirrors
// the MQTT alarm publisher's PublishFailedToArm signature so the daemon
// composition root can wire that publisher in one line via
// SetArmFailureHook. A nil hook — the state of a daemon configured
// without MQTT — means the schedule chain's AutoArm failures stay
// journal-only (notes/concepts/alarm-concept.md §15 row 19).
type ArmFailureHook func(zoneID, zoneName string, mode hmenum.AlarmMode, blockers []hmevent.AlarmBlockerDetail)

// NewService builds the alarm service. Construction is cheap and
// side-effect free; Start loads state and subscribes.
func NewService(deps Deps) (*Service, error) {
	if deps.Registry == nil || deps.Stores == nil {
		return nil, errors.New("alarm: missing registry or stores")
	}
	clk := deps.Clock
	if clk == nil {
		clk = clock.New()
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{
		settings:  deps.Settings,
		reg:       deps.Registry,
		stores:    deps.Stores,
		clk:       clk,
		log:       logger,
		bus:       events.NewBus(),
		unsubs:    map[string][]func(){},
		dpIndex:   map[string]sensorBinding{},
		enums:     newEnumResolver(deps.Registry),
		devIndex:  map[string][]string{},
		devHealth: map[string]engine.SensorHealth{},
		ifaceDown: map[string]map[string]bool{},
	}
	// The health callback fans out twice: to the daemon health tracker
	// and onto the alarm bus, so surfaces render the alarm-health state
	// live (the anti-silent-failure surface, S7).
	inner := deps.Health
	s.health = func(healthy bool, note string) {
		if inner != nil {
			inner(healthy, note)
		}
		s.publish(hmevent.AlarmHealthChangedEvent{
			Base: hmevent.NewBaseAt(s.clk.Now()), Healthy: healthy, Note: note,
		})
	}
	// outputHealth mirrors s.health's fan-out to the inner tracker and
	// the alarm bus, but publishes directly onto the bus instead of
	// through s.publish — the output manager's failures are scoped to
	// one zone via the ZoneHealth callback wired below, and going
	// through s.publish would additionally hit onPanelHealthEvent's
	// fleet-wide flip (see the field doc on Service.outputHealth).
	s.outputHealth = func(healthy bool, note string) {
		if inner != nil {
			inner(healthy, note)
		}
		events.Publish(s.bus, hmevent.AlarmHealthChangedEvent{
			Base: hmevent.NewBaseAt(s.clk.Now()), Healthy: healthy, Note: note,
		})
	}
	s.journal = alarmjournal.New(deps.Stores.Journal, clk, s.publish, logger)

	resolver := &deviceResolver{reg: deps.Registry}
	mgr, err := outputs.NewManager(outputs.Config{
		Clock:    clk,
		Resolver: resolver,
		// Notification outputs fan out onto the alarm bus; MQTT,
		// webhook, and WS pick the event up per their plane flag.
		Notify:  s.notifyOutputFired,
		Ledger:  deps.Stores.Incidents,
		Journal: s.journal,
		Rows:    deps.Stores.Outputs,
		// The fan-out wrapper, not deps.Health: the driver layer is the
		// only producer of alarm health transitions, and a failed fire or
		// an unverified stop has to reach the alarm bus as well as the
		// daemon tracker. Passing the inner callback here leaves every
		// live surface reporting a healthy alarm system while a siren is
		// stuck on. outputHealth rather than health: see the field doc
		// on Service.outputHealth for why the two must stay distinct.
		Health: s.outputHealth,
		// ZoneHealth drives the alarm-control-panel projection's
		// per-zone availability (K1): a failure scoped to one zone must
		// not remove Home Assistant's disarm control from the others.
		ZoneHealth:             s.onOutputZoneHealth,
		Logger:                 logger,
		DefaultSirenDuration:   time.Duration(deps.Settings.DefaultSirenSeconds) * time.Second,
		MaxAcousticPerIncident: time.Duration(deps.Settings.MaxAcousticPerIncidentSeconds) * time.Second,
		StopVerifyWindow:       time.Duration(deps.Settings.StopVerifySeconds) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	s.manager = mgr

	// The codes facade is built before the engine so its Validate
	// method can be wired as the engine's CodeValidator port; it is
	// wired onto the intent router below via SetCodeSource, through the
	// codeSourceAdapter (the facade cannot implement alarm.CodeSource
	// directly without importing this package, which already imports
	// the facade's package to construct it).
	s.codes = codes.New(codes.Deps{
		Store:   deps.Stores.Codes,
		Journal: s.journal,
		Clock:   clk,
		Logger:  logger,
	})

	eng, err := engine.New(s.engineDeps(deps, clk, mgr, logger))
	if err != nil {
		return nil, err
	}
	s.engine = eng
	s.sysvarMirror = newSysvarMirror(s)
	s.panels = newPanelRegistry(deps.Settings.MasterPanelName)
	s.intents = newIntentRouter(s)
	s.schedules = newScheduleRunner(scheduleRunnerDeps{
		Zones:   deps.Stores.Zones,
		Engine:  eng,
		Journal: s.journal,
		Publish: s.publish,
		Clock:   clk,
		Logger:  logger,
		ArmFailure: func(zoneID, zoneName string, mode hmenum.AlarmMode, blockers []hmevent.AlarmBlockerDetail) {
			if hook := s.armFailureHookRef(); hook != nil {
				hook(zoneID, zoneName, mode, blockers)
			}
		},
	})
	s.SetCodeSource(codeSourceAdapter{facade: s.codes})
	return s, nil
}

// SetArmFailureHook installs the FAILED_TO_ARM notification hook
// consumed by the schedule chain's AutoArm path (notes/concepts/alarm-concept.md
// §15 row 19). Wiring is optional and mirrors SetCodeSource: a nil
// hook (the default) keeps the schedule chain journal-only.
func (s *Service) SetArmFailureHook(fn ArmFailureHook) {
	s.mu.Lock()
	s.armFailure = fn
	s.mu.Unlock()
}

// armFailureHookRef returns the currently wired hook (may be nil).
func (s *Service) armFailureHookRef() ArmFailureHook {
	s.mu.Lock()
	fn := s.armFailure
	s.mu.Unlock()
	return fn
}

// Codes exposes the codes facade for management surfaces (REST/WS
// code CRUD, hmcli).
func (s *Service) Codes() *codes.Facade { return s.codes }

// NotifyCodesChanged publishes the codes-changed reconcile poke on the
// alarm bus. The composition root wires it into the code CRUD adapter;
// the MQTT publisher re-derives its discovery flags (effective
// code_arm_required / code_disarm_required) on it, and the panel
// registry refreshes the same flags on its entity projections. The
// refresh runs on the caller (a management write, never the engine
// sink), so the store reads it needs are safe here.
func (s *Service) NotifyCodesChanged() {
	events.Publish(s.bus, hmevent.AlarmCodesChangedEvent{Base: hmevent.NewBaseAt(s.clk.Now())})
	s.refreshPanelCodePolicies(context.Background())
}

// EffectiveCodePolicy resolves the per-zone arm/disarm code requirement
// exactly as the engine will enforce it (notes/concepts/alarm-concept.md
// §11/§13.3): the policy half comes from the zone config (RequireDisarm
// defaults to required when nil), and the "codes exist" half from the
// codes facade — an zone without an applicable enabled pin code
// advertises no requirement (the engine passes an empty code through),
// while an zone with one advertises it even off the nil default, so
// clients prompt for the code the engine is going to demand.
// Advertising either half alone would trap disarm: requirement-without-
// codes leaves the client prompting for a code that cannot exist,
// codes-without-requirement makes it send a bare disarm the engine
// refuses. A missing zone, parse error, or absent store degrades to no
// requirement.
func (s *Service) EffectiveCodePolicy(ctx context.Context, zoneID string) (armRequired, disarmRequired bool) {
	if s.stores == nil || s.stores.Zones == nil {
		return false, false
	}
	row, ok, err := s.stores.Zones.Get(ctx, zoneID)
	if err != nil || !ok {
		return false, false
	}
	cfg, err := engine.ParseZoneConfig(row.ConfigJSON)
	if err != nil {
		return false, false
	}
	armRequired = cfg.CodePolicy.RequireArm
	disarmRequired = cfg.CodePolicy.RequireDisarm == nil || *cfg.CodePolicy.RequireDisarm
	if armRequired || disarmRequired {
		hasPIN := s.codes != nil && s.codes.HasPINCodes(ctx, zoneID)
		armRequired = armRequired && hasPIN
		disarmRequired = disarmRequired && hasPIN
	}
	return armRequired, disarmRequired
}

// codeSourceAdapter maps the codes facade's Row projection onto the
// CodeRow shape the intent router consumes. It lives in this package
// (not internal/alarm/codes) so the codes package never has to import
// package alarm — package alarm already imports codes to construct the
// facade, and a two-way import would cycle.
type codeSourceAdapter struct {
	facade *codes.Facade
}

// Rows implements CodeSource.
func (a codeSourceAdapter) Rows(ctx context.Context) ([]CodeRow, error) {
	rows, err := a.facade.Rows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CodeRow, len(rows))
	for i := range rows {
		r := &rows[i]
		out[i] = CodeRow{
			ID:     r.ID,
			Name:   r.Name,
			Kind:   CodeKind(r.Kind),
			Duress: r.Duress,
			Perms: CodePerms{
				Arm:     r.Perms.Arm,
				Disarm:  r.Perms.Disarm,
				Silence: r.Perms.Silence,
			},
			Zones: r.Zones,
			Binding: CodeBinding{
				Central:        r.Binding.Central,
				DeviceAddress:  r.Binding.DeviceAddress,
				Slot:           r.Binding.Slot,
				ArmMode:        r.Binding.ArmMode,
				ChannelAddress: r.Binding.ChannelAddress,
				Parameter:      r.Binding.Parameter,
				Action:         r.Binding.Action,
				ZoneID:         r.Binding.ZoneID,
			},
			ValidFromMS:  r.ValidFromMS,
			ValidUntilMS: r.ValidUntilMS,
			Enabled:      r.Enabled,
		}
	}
	return out, nil
}

// SetCodeSource wires the parsed-code source consumed by keypad and
// remote intent routing. The codes facade injects it once built; a nil
// source keeps hardware-code routing inert (notes/concepts/alarm-concept.md §11).
func (s *Service) SetCodeSource(src CodeSource) {
	s.mu.Lock()
	s.codeSource = src
	s.mu.Unlock()
}

// codeSourceRef returns the currently wired code source (may be nil).
func (s *Service) codeSourceRef() CodeSource {
	s.mu.Lock()
	src := s.codeSource
	s.mu.Unlock()
	return src
}

// codeRows loads the parsed code rows, or returns nil when no source is
// wired.
func (s *Service) codeRows(ctx context.Context) ([]CodeRow, error) {
	src := s.codeSourceRef()
	if src == nil {
		return nil, nil
	}
	return src.Rows(ctx)
}

// Name implements the bridge service contract.
func (s *Service) Name() string { return "alarm" }

// Bus exposes the alarm event bus for north-bound surfaces (WS, MQTT,
// webhook adapters subscribe here).
func (s *Service) Bus() *events.Bus { return s.bus }

// Engine exposes the engine for command surfaces (REST/WS handlers,
// hmcli).
func (s *Service) Engine() *engine.Engine { return s.engine }

// Manager exposes the output-driver layer (test fire, reconcile).
func (s *Service) Manager() *outputs.Manager { return s.manager }

// Stores exposes the alarm store bundle for the management surface.
func (s *Service) Stores() *Stores { return s.stores }

// Reload re-reads the alarm configuration after a management write:
// output rows, event-routing indexes, and the engine's zones/sensors.
func (s *Service) Reload(ctx context.Context) error {
	if err := s.manager.Reload(ctx); err != nil {
		return err
	}
	if err := s.rebuildIndexes(ctx); err != nil {
		return err
	}
	if err := s.engine.Reload(ctx); err != nil {
		return err
	}
	s.seedPanels(ctx)
	s.schedules.start(ctx) // recompute every schedule's daily-time chain
	// Enrollment decides which data points the Security & Safety domain
	// treats as security-relevant and which zone owns them, so a config
	// change has to reach its index too. Without this a newly enrolled
	// sensor stays outside every aggregate until the next daemon restart.
	if hook := s.configChangedHookRef(); hook != nil {
		hook(ctx)
	}
	return nil
}

// SetConfigChangedHook installs a callback fired after every successful
// Reload. The composition root uses it to rebuild the Security & Safety
// classification index, which reads the alarm enrollment.
func (s *Service) SetConfigChangedHook(fn func(context.Context)) {
	s.mu.Lock()
	s.configChanged = fn
	s.mu.Unlock()
}

func (s *Service) configChangedHookRef() func(context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configChanged
}

// Start loads configuration and persisted state, subscribes to every
// known central, runs the reconciliation pass (S4), and starts the
// journal-retention chain.
func (s *Service) Start(ctx context.Context) error {
	if !s.settings.Enabled {
		s.log.Info("alarm service disabled by configuration")
		return nil
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	s.mu.Unlock()

	// Before startInner: the engine's restore pass publishes state
	// transitions through the sink, and an export queued while the
	// worker is down is dropped.
	s.sysvarMirror.start(ctx)

	// Load failures are fail-visible, not fail-fatal: the daemon's
	// other surfaces (MQTT/REST/UI) must not die because the alarm
	// tier cannot come up — the health tracker and log carry the
	// degradation loudly instead (S7), and the SPA alarm surface
	// shows the unavailable state.
	if err := s.startInner(ctx); err != nil {
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
		s.sysvarMirror.stop()
		if errors.Is(err, context.Canceled) {
			return nil // shutdown race during boot — not a failure
		}
		s.log.Error("alarm service failed to start", "error", err)
		if s.health != nil {
			s.health(false, "alarm start failed")
		}
		return nil
	}
	for _, u := range s.reg.List() {
		s.attachUnit(u)
	}
	s.reconcile(ctx)
	// A central that is already southbound-ready fires no readiness event
	// this service will hear, so its enrolled sensors are checked against
	// the model here: a device deleted while the daemon was down is
	// otherwise restored as available and never corrected.
	s.refreshSensorPresence(ctx)
	s.seedPanels(ctx)
	s.scheduleRetention()
	s.schedules.start(ctx)
	s.log.Info("alarm service started")
	return nil
}

// startInner performs the fallible part of Start.
func (s *Service) startInner(ctx context.Context) error {
	if err := s.manager.Reload(ctx); err != nil {
		return fmt.Errorf("alarm: %w", err)
	}
	if err := s.rebuildIndexes(ctx); err != nil {
		return fmt.Errorf("alarm: %w", err)
	}
	if err := s.engine.Start(ctx); err != nil {
		return fmt.Errorf("alarm: engine start: %w", err)
	}
	return nil
}

// Stop unsubscribes and stops the engine, persisting fresh state.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	unsubs := s.unsubs
	s.unsubs = map[string][]func(){}
	retention := s.retention
	s.retention = nil
	s.mu.Unlock()

	for _, list := range unsubs {
		for _, u := range list {
			u()
		}
	}
	if retention != nil {
		retention()
	}
	s.schedules.stop()
	// Shutdown, not StopWatchdogs: an output class the device does not
	// bound itself keeps running after this process is gone, and the
	// watchdog it drops here was the only thing that would ever have
	// stopped it.
	s.manager.Shutdown(ctx)
	s.sysvarMirror.stop()
	s.engine.Stop(ctx)
	return nil
}

// AttachCentral subscribes a runtime-adopted central (the
// central-added orchestrator hook).
func (s *Service) AttachCentral(name string) {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return
	}
	if u, ok := s.reg.Get(name); ok {
		s.attachUnit(u)
		// A runtime-adopted central is a blind window ending: adopt or
		// stop its sounding sirens and refresh sensor values (§10.1).
		ctx := context.Background()
		s.reconcile(ctx)
		s.engine.ReevaluateSensors(ctx)
		s.refreshSensorPresence(ctx)
	}
}

// DetachCentral drops the subscriptions of a removed central.
func (s *Service) DetachCentral(name string) {
	s.mu.Lock()
	list := s.unsubs[name]
	delete(s.unsubs, name)
	s.mu.Unlock()
	for _, u := range list {
		u()
	}
}

// publish fans an alarm event onto the alarm bus. events.Publish is
// generic over the concrete type, so the sink dispatches explicitly.
// notifyOutputFired publishes one notification output's fire signal
// on the alarm bus (outputs.NotificationSink); MQTT, webhook, and WS
// pick it up per their plane flag.
//
// It runs inside the engine's fire path, with the engine lock held, so
// it resolves nothing from the engine: the zone name and the incident
// sources travel with the cycle instead (engine.FireOptions). Reading
// them back off the engine here self-deadlocks the whole alarm system —
// the trigger holds the lock the read would need, and every later verb,
// Disarm and Silence included, blocks behind it with the siren already
// sounding.
func (s *Service) notifyOutputFired(n outputs.Notification) {
	// The cause comes from the incident document, not from the source
	// list: the causes that carry no data point at all — a panic key, a
	// lost central, an adopted siren — record no source, and gating the
	// label on the list left exactly those notifications unlabelled and
	// indistinguishable from an intrusion alert.
	cause := incidentCauseKind(n.Incident.CauseJSON)
	events.Publish(s.bus, hmevent.AlarmNotificationEvent{
		Base:       hmevent.NewBaseAt(s.clk.Now()),
		ZoneID:     n.Row.ZoneID,
		ZoneName:   n.ZoneName,
		OutputID:   n.Row.ID,
		OutputName: n.Row.Name,
		IncidentID: n.Incident.ID,
		Mode:       n.Incident.Mode,
		MQTT:       n.Config.NotifyMQTTEnabled(),
		Webhook:    n.Config.NotifyWebhookEnabled(),
		Cause:      cause,
		Sources:    n.Sources,
	})
}

func (s *Service) publish(e hmevent.Event) {
	switch ev := e.(type) {
	case hmevent.AlarmStateChangedEvent:
		events.Publish(s.bus, ev)
		if s.sysvarMirror != nil {
			s.sysvarMirror.onStateChanged(ev)
		}
		if s.panels != nil {
			s.onPanelStateEvent(ev)
		}
	case hmevent.AlarmTriggeredEvent:
		events.Publish(s.bus, ev)
	case hmevent.AlarmReadinessChangedEvent:
		events.Publish(s.bus, ev)
	case hmevent.AlarmJournalAppendedEvent:
		events.Publish(s.bus, ev)
	case hmevent.AlarmCountdownEvent:
		events.Publish(s.bus, ev)
	case hmevent.AlarmWalkTestEvent:
		events.Publish(s.bus, ev)
	case hmevent.AlarmHealthChangedEvent:
		events.Publish(s.bus, ev)
		if s.panels != nil {
			s.onPanelHealthEvent(ev)
		}
	case hmevent.AlarmPanelChangedEvent:
		events.Publish(s.bus, ev)
	case hmevent.AlarmDuressEvent:
		// Never omit this case again. A duress code entered under
		// coercion reached the default branch and was logged instead of
		// published, so the whole covert-trigger feature produced one
		// hidden journal row and nothing else — on every surface, at
		// every configured visibility level. The visibility policy sits
		// downstream of here and could not compensate.
		events.Publish(s.bus, ev)
	case hmevent.AlarmReminderEvent:
		// Same omission, second event: a schedule reminder never left
		// the sink either.
		events.Publish(s.bus, ev)
	default:
		// Reaching this branch means a producer emits an event type this
		// fan-out does not know, and every consumer of it is silently
		// dead. TestAlarmSinkFansOutEveryEventType pins the set; if that
		// test is green and this line still fires in production, the
		// producer is new and the test needs the case first.
		s.log.Error("alarm sink dropped unknown event type; consumers of it are silently dead",
			"type", string(e.Type()))
	}
}

// sinkFunc adapts the publish func onto the engine's EventSink port.
type sinkFunc func(hmevent.Event)

// Publish implements engine.EventSink.
func (f sinkFunc) Publish(e hmevent.Event) { f(e) }

// reconcile runs the S4 pass over every zone: sounding sirens of
// armed zones are adopted as incidents; sounding sirens of disarmed,
// unshared zones are stopped.
func (s *Service) reconcile(ctx context.Context) {
	zones := s.engine.Zones()
	for i := range zones {
		snap := &zones[i]
		sounding := s.manager.Sounding(ctx, snap.ID)
		if len(sounding) == 0 {
			continue
		}
		armed := snap.State != hmenum.AlarmZoneStateDisarmed
		if armed {
			ids := make([]string, 0, len(sounding))
			for _, so := range sounding {
				ids = append(ids, so.OutputID)
			}
			adopted, err := s.engine.AdoptSounding(ctx, snap.ID, ids)
			if err != nil {
				s.log.Error("alarm reconcile adoption failed", "zone", snap.ID, "error", err)
				continue
			}
			if !adopted {
				continue // already alarming — no re-accounting
			}
			if snap2, ok := s.engine.Zone(snap.ID); ok && snap2.IncidentID != 0 {
				s.manager.AdoptBounded(ctx, snap.ID, snap2.IncidentID, ids)
			}
		} else {
			s.manager.StopUnowned(ctx, snap.ID)
		}
	}
}

// scheduleRetention starts the daily journal-retention chain. The
// chain runs on scheduler callbacks, detached from any caller context
// by design.
//
//nolint:contextcheck // periodic retention has no caller ctx; runs on the service lifetime
func (s *Service) scheduleRetention() {
	if s.settings.JournalRetentionDays <= 0 {
		return
	}
	maxAge := time.Duration(s.settings.JournalRetentionDays) * 24 * time.Hour
	sched := engine.NewClockScheduler(s.clk)
	var chain func()
	chain = func() {
		cancel := sched.Schedule(24*time.Hour, func() {
			if n, err := s.journal.Purge(context.Background(), maxAge); err != nil {
				s.log.Error("alarm journal retention failed", "error", err)
			} else if n > 0 {
				s.log.Info("alarm journal retention", "deleted", n)
			}
			s.purgeIncidents(maxAge)
			s.mu.Lock()
			started := s.started
			s.mu.Unlock()
			if started {
				chain()
			}
		})
		s.mu.Lock()
		s.retention = cancel
		s.mu.Unlock()
	}
	chain()
}

// incidentCauseKind extracts the cause token from a persisted incident
// cause document. An unparsable or empty document yields the empty
// string — the notification then carries its sources without a cause
// label rather than failing.
func incidentCauseKind(causeJSON string) string {
	if causeJSON == "" {
		return ""
	}
	var doc struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(causeJSON), &doc); err != nil {
		return ""
	}
	return doc.Kind
}

// purgeIncidents applies the journal retention window to closed
// incidents and then drops the source rows their incidents left
// behind. Order matters: the incident purge is a plain DELETE with no
// cascade, so sweeping orphans afterwards is what keeps the ledger
// from outliving every incident it describes.
//
// Retention runs on the service lifetime, detached from any caller.
//
//nolint:contextcheck // periodic retention has no caller ctx
func (s *Service) purgeIncidents(maxAge time.Duration) {
	if s.stores == nil || s.stores.Incidents == nil {
		return
	}
	ctx := context.Background()
	cutoff := s.clk.Now().Add(-maxAge).UnixMilli()
	if n, err := s.stores.Incidents.PurgeClosedBefore(ctx, cutoff); err != nil {
		s.log.Error("alarm incident retention failed", "error", err)
	} else if n > 0 {
		s.log.Info("alarm incident retention", "deleted", n)
	}
	if s.stores.IncidentSources == nil {
		return
	}
	if n, err := s.stores.IncidentSources.PurgeOrphans(ctx); err != nil {
		s.log.Error("alarm incident source retention failed", "error", err)
	} else if n > 0 {
		s.log.Info("alarm incident source retention", "deleted", n)
	}
}

// engineDeps assembles the engine's dependency set. Extracted from
// NewService so the constructor stays inside the length budget and the
// wiring reads as one list rather than a block inside a longer
// function.
func (s *Service) engineDeps(deps Deps, clk clock.Clock, mgr *outputs.Manager, logger *slog.Logger) engine.Deps {
	return engine.Deps{
		Clock:               clk,
		Zones:               deps.Stores.Zones,
		Sensors:             deps.Stores.Sensors,
		State:               deps.Stores.State,
		Incidents:           deps.Stores.Incidents,
		Runtime:             deps.Stores.Runtime,
		Outputs:             mgr,
		Sink:                sinkFunc(s.publish),
		Journal:             s.journal,
		SourceLedger:        deps.Stores.IncidentSources,
		SensorReader:        &sensorReader{reg: deps.Registry},
		MotionReset:         newMotionResetter(deps.Registry),
		Validator:           s.codes,
		Logger:              logger,
		RestartLoopBreakerK: deps.Settings.RestartLoopBreaker,
	}
}
