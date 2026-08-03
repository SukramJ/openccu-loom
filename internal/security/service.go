// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package security is the daemon-level Security & Safety domain: the
// aggregation and reporting plane above the alarm engine.
//
// It answers three questions the alarm engine alone cannot:
//
//   - what is active right now, per hazard class and per zone, whether
//     or not it is enrolled as an alarm trigger;
//   - what is broken, since when, and has anyone acknowledged it;
//   - what should a person be told about it, in a sentence.
//
// It deliberately runs independently of the alarm engine. A household
// with smoke and water detectors but no burglar alarm still gets the
// hazard classes, the fault plane and the notifications; only the
// intrusion and zone halves stay empty. That independence is the point
// of the domain — the concept calls it out as the central requirement.
//
// Not to be confused with internal/auth (who may act) or
// internal/secret (secrets at rest). This package is about physical
// safety.
package security

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Stores bundles the persistence the domain owns.
type Stores struct {
	Faults  *sqlitestore.SecurityFaultStore
	Sources *sqlitestore.SecuritySourceStore
	// Sensors and Zones are read-only here: the alarm domain owns them,
	// the security domain only needs to know which data points carry an
	// alarm role and which zone they belong to.
	Sensors *sqlitestore.AlarmSensorStore
	Zones   *sqlitestore.AlarmZoneStore
}

// Settings configures the domain.
type Settings struct {
	// Locale is the daemon locale used for the rendered half of a
	// report. Consumers with a request locale re-render from the key.
	Locale string
	// PublicURL is the operator-facing base of the config UI; empty
	// suppresses deep links.
	PublicURL string
	// DuressVisibility bounds where a covert trigger may appear.
	DuressVisibility hmenum.DuressVisibility
}

// Deps wires a Service.
type Deps struct {
	Settings Settings
	Registry *central.Registry
	Stores   *Stores
	// AlarmBus is the alarm domain's event bus. A nil bus means the
	// alarm engine is disabled; the domain then runs without the
	// intrusion and zone halves rather than not at all.
	AlarmBus *events.Bus
	Clock    clock.Clock
	Logger   *slog.Logger
	Catalogs *i18n.Catalogs
}

// Service is the daemon-level Security & Safety component.
type Service struct {
	settings Settings
	reg      *central.Registry
	stores   *Stores
	alarmBus *events.Bus
	clk      clock.Clock
	log      *slog.Logger
	render   *renderer

	// bus carries the domain's own events. It is separate from the
	// alarm bus so a consumer can subscribe to one without the other.
	bus *events.Bus

	mu      sync.Mutex
	started bool
	agg     *aggregate
	unsubs  []func()
	// centralUnsubs is keyed by central name so a detach can drop
	// exactly that central's subscriptions.
	centralUnsubs map[string][]func()
}

// New builds the service. Construction is cheap and side-effect free;
// Start loads state and subscribes.
func New(deps Deps) (*Service, error) {
	if deps.Registry == nil || deps.Stores == nil {
		return nil, errors.New("security: missing registry or stores")
	}
	clk := deps.Clock
	if clk == nil {
		clk = clock.New()
	}
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	vis := deps.Settings.DuressVisibility
	if !vis.Valid() {
		vis = hmenum.DuressVisibilityNotifyOnly
	}
	deps.Settings.DuressVisibility = vis
	return &Service{
		settings:      deps.Settings,
		reg:           deps.Registry,
		stores:        deps.Stores,
		alarmBus:      deps.AlarmBus,
		clk:           clk,
		log:           log,
		render:        newRenderer(deps.Catalogs, deps.Settings.Locale, deps.Settings.PublicURL),
		bus:           events.NewBus(),
		agg:           newAggregate(),
		centralUnsubs: map[string][]func(){},
	}, nil
}

// Name implements the north-bridge service lifecycle.
func (s *Service) Name() string { return "security" }

// Bus exposes the domain's event bus for north-bound adapters.
func (s *Service) Bus() *events.Bus { return s.bus }

// Start loads the fault ledger, builds the classification index and
// subscribes to the alarm and central buses.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	s.mu.Unlock()

	if err := s.loadFaults(ctx); err != nil {
		// A failed ledger load degrades the fault plane but must not
		// prevent hazard reporting: the classes matter more.
		s.log.Error("security: load fault ledger", "error", err)
	}
	if err := s.RebuildIndex(ctx); err != nil {
		// A failed index build leaves the domain reporting nothing until
		// the next central attaches and rebuilds it. That is a
		// degradation; refusing to start would make an observability
		// plane able to keep the whole daemon down, which it must not be.
		s.log.Error("security: build classification index", "error", err)
	}
	s.subscribeAlarm()
	for _, u := range s.reg.List() {
		s.attachUnit(u)
	}
	s.log.Info("security service started",
		"duress_visibility", s.settings.DuressVisibility.String())
	return nil
}

// Stop drops every subscription.
func (s *Service) Stop(_ context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	unsubs := s.unsubs
	s.unsubs = nil
	perCentral := s.centralUnsubs
	s.centralUnsubs = map[string][]func(){}
	s.mu.Unlock()

	for _, u := range unsubs {
		u()
	}
	for _, list := range perCentral {
		for _, u := range list {
			u()
		}
	}
	return nil
}

// AttachCentral subscribes a runtime-adopted central.
func (s *Service) AttachCentral(name string) {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return
	}
	if u, ok := s.reg.Get(name); ok {
		s.attachUnit(u)
		// A newly adopted central brings data points the index has not
		// seen; rebuilding is cheaper than reasoning about the delta.
		if err := s.RebuildIndex(context.Background()); err != nil {
			s.log.Error("security: rebuild index after attach", "central", name, "error", err)
		}
	}
}

// DetachCentral drops a removed central's subscriptions and every trace
// of it from the aggregate.
//
// The teardown is the load-bearing half: without it a detached central
// leaves ghost sources behind that pin their class permanently active,
// so `smoke` would stay on for a CCU that is no longer configured.
func (s *Service) DetachCentral(name string) {
	s.mu.Lock()
	list := s.centralUnsubs[name]
	delete(s.centralUnsubs, name)
	s.agg.dropCentral(name)
	snap := s.agg.snapshot()
	s.mu.Unlock()

	for _, u := range list {
		u()
	}
	if _, err := s.stores.Faults.ClearByCentral(context.Background(), name, nowMS(s.clk.Now())); err != nil {
		s.log.Error("security: clear faults of detached central", "central", name, "error", err)
	}
	s.publishState(snap)
}

// Snapshot returns the coherent domain state.
func (s *Service) Snapshot() security.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agg.snapshot()
}

// DuressVisibility reports the configured covert-trigger policy, so a
// north-bound adapter can honour it without re-reading config.
func (s *Service) DuressVisibility() hmenum.DuressVisibility {
	return s.settings.DuressVisibility
}

// loadFaults restores the standing fault set.
func (s *Service) loadFaults(ctx context.Context) error {
	rows, err := s.stores.Faults.ListOpen(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range rows {
		f := faultFromRow(rows[i])
		s.agg.faults[f.ID] = &f
	}
	return nil
}

// publishState announces a folded severity change.
func (s *Service) publishState(snap security.Snapshot) {
	var active []hmenum.SecurityClass
	for c, st := range snap.Classes {
		if st.Active {
			active = append(active, c)
		}
	}
	events.Publish(s.bus, hmevent.SecurityStateChangedEvent{
		Base:          hmevent.NewBaseAt(s.clk.Now()),
		To:            snap.Severity,
		ActiveClasses: active,
		OpenFaults:    len(snap.Faults),
	})
}

// faultFromRow projects a stored fault onto the domain shape.
func faultFromRow(row sqlitestore.SecurityFault) security.Fault {
	ref := hmevent.NewSecuritySourceRef(row.CentralName, row.InterfaceID, row.ChannelAddress, row.Parameter)
	ref.Name = row.Name
	ref.Class = hmenum.SecurityClass(row.Class)
	return security.Fault{
		ID:               row.ID,
		Class:            hmenum.SecurityClass(row.Class),
		Reason:           hmenum.SecurityFaultReason(row.Reason),
		Severity:         hmenum.SecuritySeverity(row.Severity),
		Source:           ref,
		SinceMS:          row.SinceMS,
		AcknowledgedAtMS: row.AcknowledgedAt,
		AcknowledgedBy:   row.AcknowledgedBy,
	}
}
