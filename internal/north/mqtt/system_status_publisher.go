// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
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
// Mirrors the north-bound subscriber contract described in
// ADR 0011 and
// path.
type SystemStatusPublisher struct {
	reg    *central.Registry
	wiring *Wiring
	logger *slog.Logger

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

// Start attaches one subscription per registered central. Safe to call
// multiple times — subsequent calls are no-ops if already started.
func (p *SystemStatusPublisher) Start() {
	if p.reg == nil || p.wiring == nil {
		return
	}
	for _, c := range p.reg.List() {
		bus := c.EventBus
		if bus == nil {
			continue
		}
		centralName := c.Name()
		unsub := events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
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
			if err := p.wiring.Bridge().PublishSystemStatus(ctx, centralName, b); err != nil {
				p.logger.Warn("mqtt.system_status.publish",
					slog.String("central", centralName),
					slog.String("err", err.Error()))
			}
		})
		p.unsubs = append(p.unsubs, unsub)
	}
}

// Stop drops all event-bus subscriptions.
func (p *SystemStatusPublisher) Stop() {
	for _, u := range p.unsubs {
		u()
	}
	p.unsubs = nil
}
