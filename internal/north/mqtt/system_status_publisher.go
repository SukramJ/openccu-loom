// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// SystemStatusPublisher subscribes to every registered central's event
// bus and publishes [hmevent.SystemStatusChangedEvent] payloads to the
// MQTT status item `<base>/status/<central>/system/status`. The topic is
// non-retained (QoS 0) so consumers only see live events — a stale
// retained payload would be misleading after a daemon restart.
//
// Mirrors the north-bound subscriber contract described in ADR 0011.
type SystemStatusPublisher struct {
	reg    *central.Registry
	wiring *Wiring
	logger *slog.Logger

	// remove detaches the registry observer Start installed, together with
	// every per-central subscription it attached.
	remove func()
}

// NewSystemStatusPublisher returns a publisher bound to reg and wiring.
// Start/Stop manage the subscription lifetime.
func NewSystemStatusPublisher(reg *central.Registry, w *Wiring, logger *slog.Logger) *SystemStatusPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &SystemStatusPublisher{reg: reg, wiring: w, logger: logger}
}

// systemStatusPayload is the JSON shape published to
// `<base>/status/<central>/system/status`.
type systemStatusPayload struct {
	CentralName        string   `json:"central"`
	Healthy            bool     `json:"healthy"`
	Reason             string   `json:"reason,omitempty"`
	InterfaceID        string   `json:"interface_id,omitempty"`
	ErrorCode          int      `json:"error_code,omitempty"`
	DegradedInterfaces []string `json:"degraded_interfaces,omitempty"`
	Issues             []string `json:"issues,omitempty"`
}

// Start attaches one subscription per central the registry holds now, and one
// more for every central registered later.
func (p *SystemStatusPublisher) Start() {
	if p.reg == nil || p.wiring == nil {
		return
	}
	p.remove = p.reg.OnRegisterDeclared(wiring.Seam{
		Name:         "mqtt.system_status",
		Collaborator: "*mqtt.SystemStatusPublisher",
		Phase:        wiring.PhasePerCentral,
		Why:          "the daemon publishes no system-status topic for the central, so a subscriber sees the last retained value forever",
	}, p.StartCentral)
}

// StartCentral attaches this publisher to a single central's event bus and
// returns the unsubscribe (nil when there was nothing to attach). It is the
// observer the registry runs per central.
//
// Start used to walk the registry as it stood at boot, so a central
// adopted at runtime never published a single message on the MQTT
// system-status plane until the daemon was restarted.
func (p *SystemStatusPublisher) StartCentral(u *central.Unit) func() {
	if p == nil || p.wiring == nil || u == nil {
		return nil
	}
	bus := u.EventBus
	if bus == nil {
		return nil
	}
	centralName := u.Name()
	return bus.Subscribe(func(e hmevent.SystemStatusChangedEvent) {
		// The event is a status item that is not retained (ADR 0083): the
		// component that changed is its `val`, the event's time its `ts`,
		// and the rest of the event under `hm`.
		pay := systemStatusPayload{
			CentralName:        centralName,
			Healthy:            e.Healthy,
			Reason:             e.Reason,
			InterfaceID:        e.InterfaceID,
			ErrorCode:          e.ErrorCode,
			DegradedInterfaces: e.DegradedInterfaces,
			Issues:             e.Issues,
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
		if err := bridge.PublishSystemStatus(ctx, centralName, e.Component, pay, e.Timestamp()); err != nil {
			p.logger.Warn("mqtt.system_status.publish",
				slog.String("central", centralName),
				slog.String("err", err.Error()))
		}
	})
}

// Stop removes the registry observer and drops every subscription it
// attached — including the ones for centrals adopted after Start.
func (p *SystemStatusPublisher) Stop() {
	if p.remove != nil {
		p.remove()
		p.remove = nil
	}
}
