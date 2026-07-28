// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package alarm wires the alarm engine, its output-driver layer, and
// the per-central event subscriptions into one daemon-level service
// (docs/alarm-concept.md §14). The service owns a dedicated alarm
// event bus: zones are daemon-level while every central owns its own
// bus, so north-bound surfaces subscribe to the alarm bus instead of
// fanning across centrals.
package alarm

import (
	"context"
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
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
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

	bus     *events.Bus
	journal *alarmjournal.Journal
	engine  *engine.Engine
	manager *outputs.Manager
	codes   *codes.Facade

	mu           sync.Mutex
	started      bool
	unsubs       map[string][]func() // per central
	dpIndex      map[string]string   // central|iface|channel|param → sensor ID
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
}

// ArmFailureHook is a FAILED_TO_ARM notification callback. It mirrors
// the MQTT alarm publisher's PublishFailedToArm signature
// (cmd/openccu-loom/daemon_north.go) so the daemon composition root
// can wire that publisher in one line via SetArmFailureHook. A nil
// hook (the default) means the schedule chain's AutoArm failures stay
// journal-only (docs/alarm-concept.md §15 row 19).
type ArmFailureHook func(zoneID, zoneName string, mode hmenum.AlarmMode, blockers []string)

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
		dpIndex:   map[string]string{},
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
	s.journal = alarmjournal.New(deps.Stores.Journal, clk, s.publish, logger)

	resolver := &deviceResolver{reg: deps.Registry}
	mgr, err := outputs.NewManager(outputs.Config{
		Clock:    clk,
		Resolver: resolver,
		// Notification outputs fan out onto the alarm bus; MQTT,
		// webhook, and WS pick the event up per their plane flag.
		Notify:                 s.notifyOutputFired,
		Ledger:                 deps.Stores.Incidents,
		Journal:                s.journal,
		Rows:                   deps.Stores.Outputs,
		Health:                 deps.Health,
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

	eng, err := engine.New(engine.Deps{
		Clock:               clk,
		Zones:               deps.Stores.Zones,
		Sensors:             deps.Stores.Sensors,
		State:               deps.Stores.State,
		Incidents:           deps.Stores.Incidents,
		Runtime:             deps.Stores.Runtime,
		Outputs:             mgr,
		Sink:                sinkFunc(s.publish),
		Journal:             s.journal,
		SensorReader:        &sensorReader{reg: deps.Registry},
		Validator:           s.codes,
		Logger:              logger,
		RestartLoopBreakerK: deps.Settings.RestartLoopBreaker,
	})
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
		ArmFailure: func(zoneID, zoneName string, mode hmenum.AlarmMode, blockers []string) {
			if hook := s.armFailureHookRef(); hook != nil {
				hook(zoneID, zoneName, mode, blockers)
			}
		},
	})
	s.SetCodeSource(codeSourceAdapter{facade: s.codes})
	return s, nil
}

// SetArmFailureHook installs the FAILED_TO_ARM notification hook
// consumed by the schedule chain's AutoArm path (docs/alarm-concept.md
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
// exactly as the engine will enforce it (docs/alarm-concept.md
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
// source keeps hardware-code routing inert (docs/alarm-concept.md §11).
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
	return nil
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

	// Load failures are fail-visible, not fail-fatal: the daemon's
	// other surfaces (MQTT/REST/UI) must not die because the alarm
	// tier cannot come up — the health tracker and log carry the
	// degradation loudly instead (S7), and the SPA alarm surface
	// shows the unavailable state.
	if err := s.startInner(ctx); err != nil {
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
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
	s.manager.StopWatchdogs()
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
func (s *Service) notifyOutputFired(row sqlitestore.AlarmOutputRow, cfg outputs.OutputConfig, incident sqlitestore.AlarmIncident) {
	zoneName := ""
	if s.engine != nil {
		zones := s.engine.Zones()
		for i := range zones {
			if zones[i].ID == row.ZoneID {
				zoneName = zones[i].Name
				break
			}
		}
	}
	events.Publish(s.bus, hmevent.AlarmNotificationEvent{
		Base:       hmevent.NewBaseAt(s.clk.Now()),
		ZoneID:     row.ZoneID,
		ZoneName:   zoneName,
		OutputID:   row.ID,
		OutputName: row.Name,
		IncidentID: incident.ID,
		Mode:       incident.Mode,
		MQTT:       cfg.NotifyMQTTEnabled(),
		Webhook:    cfg.NotifyWebhookEnabled(),
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
	default:
		s.log.Warn("alarm sink dropped unknown event type", "type", string(e.Type()))
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
