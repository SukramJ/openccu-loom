// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package alarm wires the alarm engine, its output-driver layer, and
// the per-central event subscriptions into one daemon-level service
// (docs/alarm-concept.md §14). The service owns a dedicated alarm
// event bus: areas are daemon-level while every central owns its own
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

	mu           sync.Mutex
	started      bool
	unsubs       map[string][]func() // per central
	dpIndex      map[string]string   // central|iface|channel|param → sensor ID
	devIndex     map[string][]string // central|deviceAddress → sensor IDs
	devHealth    map[string]engine.SensorHealth
	ifaceDown    map[string]map[string]bool // central → interface → unreachable
	sysvarMirror *sysvarMirror
	retention    func() // retention chain cancel
}

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
		health:    deps.Health,
		bus:       events.NewBus(),
		unsubs:    map[string][]func(){},
		dpIndex:   map[string]string{},
		devIndex:  map[string][]string{},
		devHealth: map[string]engine.SensorHealth{},
		ifaceDown: map[string]map[string]bool{},
	}
	s.journal = alarmjournal.New(deps.Stores.Journal, clk, s.publish, logger)

	resolver := &deviceResolver{reg: deps.Registry}
	mgr, err := outputs.NewManager(outputs.Config{
		Clock:                  clk,
		Resolver:               resolver,
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

	eng, err := engine.New(engine.Deps{
		Clock:               clk,
		Areas:               deps.Stores.Areas,
		Sensors:             deps.Stores.Sensors,
		State:               deps.Stores.State,
		Incidents:           deps.Stores.Incidents,
		Runtime:             deps.Stores.Runtime,
		Outputs:             mgr,
		Sink:                sinkFunc(s.publish),
		Journal:             s.journal,
		SensorReader:        &sensorReader{reg: deps.Registry},
		Logger:              logger,
		RestartLoopBreakerK: deps.Settings.RestartLoopBreaker,
	})
	if err != nil {
		return nil, err
	}
	s.engine = eng
	s.sysvarMirror = newSysvarMirror(s)
	return s, nil
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
	s.scheduleRetention()
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
func (s *Service) publish(e hmevent.Event) {
	switch ev := e.(type) {
	case hmevent.AlarmStateChangedEvent:
		events.Publish(s.bus, ev)
		if s.sysvarMirror != nil {
			s.sysvarMirror.onStateChanged(ev)
		}
	case hmevent.AlarmTriggeredEvent:
		events.Publish(s.bus, ev)
	case hmevent.AlarmReadinessChangedEvent:
		events.Publish(s.bus, ev)
	case hmevent.AlarmJournalAppendedEvent:
		events.Publish(s.bus, ev)
	case hmevent.AlarmCountdownEvent:
		events.Publish(s.bus, ev)
	default:
		s.log.Warn("alarm sink dropped unknown event type", "type", string(e.Type()))
	}
}

// sinkFunc adapts the publish func onto the engine's EventSink port.
type sinkFunc func(hmevent.Event)

// Publish implements engine.EventSink.
func (f sinkFunc) Publish(e hmevent.Event) { f(e) }

// reconcile runs the S4 pass over every area: sounding sirens of
// armed areas are adopted as incidents; sounding sirens of disarmed,
// unshared areas are stopped.
func (s *Service) reconcile(ctx context.Context) {
	areas := s.engine.Areas()
	for i := range areas {
		snap := &areas[i]
		sounding := s.manager.Sounding(ctx, snap.ID)
		if len(sounding) == 0 {
			continue
		}
		armed := snap.State != hmenum.AlarmAreaStateDisarmed
		if armed {
			ids := make([]string, 0, len(sounding))
			for _, so := range sounding {
				ids = append(ids, so.OutputID)
			}
			adopted, err := s.engine.AdoptSounding(ctx, snap.ID, ids)
			if err != nil {
				s.log.Error("alarm reconcile adoption failed", "area", snap.ID, "error", err)
				continue
			}
			if !adopted {
				continue // already alarming — no re-accounting
			}
			if snap2, ok := s.engine.Area(snap.ID); ok && snap2.IncidentID != 0 {
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
