// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// SystemStatusPublisher subscribes to every registered central's event
// bus and publishes [hmevent.SystemStatusChangedEvent] payloads to the
// MQTT topic `<base>/<central>/system/status`. The topic is
// non-retained (QoS 0) so consumers only see live events — a stale
// retained payload would be misleading after a daemon restart.
//
// Mirrors the north-bound subscriber contract described in ADR 0011.
type SystemStatusPublisher struct {
	reg    *central.Registry
	wiring *Wiring
	logger *slog.Logger

	// mu guards unsubs against a Start/Stop overlap. StartCentral does not
	// touch the slice at all - it hands its unwire back to the caller, so
	// the runtime-adopt path owns its own detach.
	mu     sync.Mutex
	unsubs []func()
}

// NewSystemStatusPublisher returns a publisher bound to reg and wiring.
// Start/Stop manage the subscription lifetime.
func NewSystemStatusPublisher(reg *central.Registry, wiring *Wiring, logger *slog.Logger) *SystemStatusPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &SystemStatusPublisher{reg: reg, wiring: wiring, logger: logger}
}

// systemStatusPayload is the JSON shape published to
// `<base>/<central>/system/status`.
type systemStatusPayload struct {
	CentralName        string    `json:"central"`
	Component          string    `json:"component"`
	Healthy            bool      `json:"healthy"`
	Reason             string    `json:"reason,omitempty"`
	InterfaceID        string    `json:"interface_id,omitempty"`
	ErrorCode          int       `json:"error_code,omitempty"`
	DegradedInterfaces []string  `json:"degraded_interfaces,omitempty"`
	Issues             []string  `json:"issues,omitempty"`
	EventAt            time.Time `json:"event_at"`
}

// Start attaches one subscription per central registered right now. A
// central that appears later — adopted through the REST admin API — needs
// [SystemStatusPublisher.StartCentral]; this walk cannot see it.
func (p *SystemStatusPublisher) Start() {
	if p.reg == nil {
		return
	}
	for _, u := range p.reg.List() {
		if unwire := p.StartCentral(u); unwire != nil {
			p.mu.Lock()
			p.unsubs = append(p.unsubs, unwire)
			p.mu.Unlock()
		}
	}
}

// StartCentral subscribes exactly one central's event bus and returns the
// unwire for it, or nil when there is nothing to attach.
//
// The composition root routes this through the live-adopt hook chain so a
// CCU adopted at runtime publishes its interface degradation to
// `<base>/<central>/system/status` like a boot-time one. Without it an
// operator's alerting rule silently never fires for the adopted CCU while it
// keeps working for the others.
func (p *SystemStatusPublisher) StartCentral(u *central.Unit) (unwire func()) {
	if p == nil || p.wiring == nil || u == nil || u.EventBus == nil {
		return nil
	}
	centralName := u.Name()
	unsub := events.Subscribe(u.EventBus, func(e hmevent.SystemStatusChangedEvent) {
		pay := systemStatusPayload{
			CentralName:        centralName,
			Component:          e.Component,
			Healthy:            e.Healthy,
			Reason:             e.Reason,
			InterfaceID:        e.InterfaceID,
			ErrorCode:          e.ErrorCode,
			DegradedInterfaces: e.DegradedInterfaces,
			Issues:             e.Issues,
			EventAt:            e.Timestamp(),
		}
		b, err := json.Marshal(pay)
		if err != nil {
			p.logger.Warn("mqtt.system_status.marshal",
				slog.String("central", centralName),
				slog.String("err", err.Error()))
			return
		}
		ctx := context.Background()
		// Nil while MQTT is disabled at runtime — the Wiring stays alive
		// and points its bridge nowhere. This runs from an event-bus
		// handler, so an unguarded dereference would crash the daemon on
		// the next status change rather than merely dropping a publish.
		bridge := p.wiring.Bridge()
		if bridge == nil {
			return
		}
		if err := bridge.PublishSystemStatus(ctx, centralName, b); err != nil {
			p.logger.Warn("mqtt.system_status.publish",
				slog.String("central", centralName),
				slog.String("err", err.Error()))
		}
	})

	return unsub
}

// Stop drops all event-bus subscriptions.
func (p *SystemStatusPublisher) Stop() {
	p.mu.Lock()
	unsubs := p.unsubs
	p.unsubs = nil
	p.mu.Unlock()
	for _, u := range unsubs {
		u()
	}
}
