// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// IntrospectAdapter exposes read-only live daemon internals — per-interface
// reliability state and a typed event-bus tap — to the REST diagnostics
// layer. It performs no writes and never contacts a CCU. Satisfies
// [interfaces.DiagnosticsIntrospectService].
type IntrospectAdapter struct{ registry *central.Registry }

// NewIntrospectAdapter wires the adapter to the central registry.
func NewIntrospectAdapter(reg *central.Registry) *IntrospectAdapter {
	return &IntrospectAdapter{registry: reg}
}

// ReliabilitySnapshot returns per-(central, interface) reliability state.
func (a *IntrospectAdapter) ReliabilitySnapshot(centralName string) []hmapi.ReliabilityState {
	out := make([]hmapi.ReliabilityState, 0)
	for _, u := range a.registry.List() {
		if centralName != "" && u.Name() != centralName {
			continue
		}
		if u.Clients == nil {
			continue
		}
		for _, e := range u.Clients.List() {
			if e == nil || e.Client == nil {
				continue
			}
			out = append(out, hmapi.ReliabilityState{
				Central:      u.Name(),
				Interface:    e.InterfaceID,
				CircuitState: e.Client.MetricsCircuitState(),
				State:        e.Client.State(),
			})
		}
	}
	return out
}

// ResolveCentral resolves the central for a tap (see the interface doc).
func (a *IntrospectAdapter) ResolveCentral(centralName string) (string, bool) {
	if centralName != "" {
		if _, ok := a.registry.Get(centralName); ok {
			return centralName, true
		}
		return "", false
	}
	if names := a.registry.Names(); len(names) == 1 {
		return names[0], true
	}
	return "", false
}

// TapEventBus subscribes to the central's event bus and forwards each
// curated event (subject to the type filter) to emit until ctx is done.
func (a *IntrospectAdapter) TapEventBus(ctx context.Context, centralName string, types []string, emit func(hmapi.DiagnosticsEvent)) {
	u, ok := a.registry.Get(centralName)
	if !ok || u.EventBus == nil {
		return
	}
	unsub := subscribeCuratedEvents(u.EventBus, types, emit)
	defer unsub()
	<-ctx.Done()
}

// subscribeCuratedEvents subscribes to a curated set of high-value event
// types on bus and forwards each (subject to typeFilter) via emit. The
// returned func unsubscribes every handler.
func subscribeCuratedEvents(bus *events.Bus, typeFilter []string, emit func(hmapi.DiagnosticsEvent)) func() {
	want := func(name string) bool {
		if len(typeFilter) == 0 {
			return true
		}
		for _, t := range typeFilter {
			if strings.EqualFold(strings.TrimSpace(t), name) {
				return true
			}
		}
		return false
	}
	send := func(name string, e any) {
		if !want(name) {
			return
		}
		emit(hmapi.DiagnosticsEvent{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: name, Event: e})
	}

	unsubs := make([]func(), 0, 12)
	unsubs = append(
		unsubs,
		events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) { send("DataPointValueChanged", e) }),
		events.Subscribe(bus, func(e hmevent.RequestCoalescedEvent) { send("RequestCoalesced", e) }),
		events.Subscribe(bus, func(e hmevent.DeviceTriggerEvent) { send("DeviceTrigger", e) }),
		events.Subscribe(bus, func(e hmevent.CentralStateChangedEvent) { send("CentralStateChanged", e) }),
		events.Subscribe(bus, func(e hmevent.ClientStateChangedEvent) { send("ClientStateChanged", e) }),
		events.Subscribe(bus, func(e hmevent.CircuitBreakerStateChangedEvent) { send("CircuitBreakerStateChanged", e) }),
		events.Subscribe(bus, func(e hmevent.ConnectionLostEvent) { send("ConnectionLost", e) }),
		events.Subscribe(bus, func(e hmevent.RecoveryStartedEvent) { send("RecoveryStarted", e) }),
		// The per-stage and per-attempt events carry the operator-facing
		// progress detail between Started and Completed/Failed: which
		// stage the pipeline is in, how many attempts were burned, and
		// the last error. Without them the tap shows a recovery as a
		// silent gap between two endpoints.
		events.Subscribe(bus, func(e hmevent.RecoveryStageChangedEvent) { send("RecoveryStageChanged", e) }),
		events.Subscribe(bus, func(e hmevent.RecoveryAttemptedEvent) { send("RecoveryAttempted", e) }),
		events.Subscribe(bus, func(e hmevent.RecoveryCompletedEvent) { send("RecoveryCompleted", e) }),
		events.Subscribe(bus, func(e hmevent.RecoveryFailedEvent) { send("RecoveryFailed", e) }),
	)
	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}
