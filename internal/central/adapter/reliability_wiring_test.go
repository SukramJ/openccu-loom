// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

type capturedIncidents struct {
	mu   sync.Mutex
	recs []reliability.IncidentRecord
}

func (c *capturedIncidents) RecordIncident(_ context.Context, inc reliability.IncidentRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, inc)
	return nil
}

func (c *capturedIncidents) snapshot() []reliability.IncidentRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]reliability.IncidentRecord(nil), c.recs...)
}

// TestWireClientReliability_CircuitTripPublishesEventAndIncident locks
// the end-to-end chain: a circuit-breaker trip on a wired client must
// (a) publish a CircuitBreakerStateChangedEvent on the central's bus —
// the signal connection recovery, health wiring and the diagnostics
// tap subscribe to — and (b) record an incident through the recorder
// installed on the CacheCoordinator, even when that recorder is set
// AFTER the hooks were wired (lazy resolution).
func TestWireClientReliability_CircuitTripPublishesEventAndIncident(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "ccu-rel"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	ic, err := client.New(client.Config{
		CentralName: "ccu-rel",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: client.CallerFunc(func(context.Context, string, []any) (any, error) {
			return nil, errors.New("wire down")
		}),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	defer ic.Close()

	const wireID = "ccu-rel-HmIP-RF"
	wireClientReliability(unit, ic, wireID)

	// Recorder is installed AFTER wiring — must still receive incidents.
	rec := &capturedIncidents{}
	unit.Cache.SetIncidentRecorder(rec)

	got := make(chan hmevent.CircuitBreakerStateChangedEvent, 8)
	unsub := events.Subscribe(unit.EventBus, func(e hmevent.CircuitBreakerStateChangedEvent) {
		got <- e
	})
	defer unsub()

	cb := ic.Circuit()
	if cb == nil {
		t.Fatal("client has no circuit breaker")
	}
	for range 64 {
		cb.RecordFailure()
		if cb.State() == hmenum.CircuitStateOpen {
			break
		}
	}
	if cb.State() != hmenum.CircuitStateOpen {
		t.Fatal("circuit breaker did not open after repeated failures")
	}

	select {
	case e := <-got:
		if e.To != hmenum.CircuitStateOpen {
			t.Errorf("event To=%v, want open", e.To)
		}
		if e.InterfaceID != wireID {
			t.Errorf("event InterfaceID=%q, want %q", e.InterfaceID, wireID)
		}
		if e.CentralName != "ccu-rel" {
			t.Errorf("event CentralName=%q, want ccu-rel", e.CentralName)
		}
	default:
		t.Fatal("no CircuitBreakerStateChangedEvent published on the central bus")
	}

	incs := rec.snapshot()
	if len(incs) == 0 {
		t.Fatal("no incident recorded for the circuit trip")
	}
	if incs[0].Type != hmenum.IncidentTypeCircuitBreakerTripped {
		t.Errorf("incident type=%v, want circuit-breaker tripped", incs[0].Type)
	}
	if incs[0].InterfaceID != wireID {
		t.Errorf("incident InterfaceID=%q, want %q", incs[0].InterfaceID, wireID)
	}
}

// TestWireClientReliability_NilUnitOrClientIsSafe guards the nil paths.
func TestWireClientReliability_NilUnitOrClientIsSafe(t *testing.T) {
	t.Parallel()
	wireClientReliability(nil, nil, "x") // must not panic
	unit, err := central.New(central.Config{Name: "ccu-rel-nil"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	wireClientReliability(unit, nil, "x") // must not panic
}
