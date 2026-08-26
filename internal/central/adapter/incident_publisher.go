// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// PublishingIncidentRecorder decorates a [reliability.IncidentRecorder] so
// that every successfully persisted incident is also published as a
// [hmevent.IncidentRecordedEvent] onto the recording central's event bus.
// North-bound consumers (the webhook bridge) thus see incidents alongside
// datapoint and system-status events without reaching back into the store.
//
// The reliability package stays free of the central event bus by design;
// this decorator lives in the adapter layer, which already holds the
// central registry and the bus, mirroring the func-adapter pattern used for
// circuit-breaker and coalesce events.
type PublishingIncidentRecorder struct {
	inner reliability.IncidentRecorder
	reg   *central.Registry
}

// NewPublishingIncidentRecorder wraps inner so a successful RecordIncident
// also publishes an [hmevent.IncidentRecordedEvent]. If inner is nil the
// returned recorder is nil (callers treat a nil recorder as "persistence
// disabled"). A nil registry disables publishing but keeps persistence.
func NewPublishingIncidentRecorder(inner reliability.IncidentRecorder, reg *central.Registry) reliability.IncidentRecorder {
	if inner == nil {
		return nil
	}
	return &PublishingIncidentRecorder{inner: inner, reg: reg}
}

// RecordIncident persists the incident via the wrapped recorder and, only on
// a successful persist, publishes the mirrored event onto the matching
// central's bus. A persist error is returned unchanged and suppresses the
// publish — the bus must never carry an incident that was not stored.
func (p *PublishingIncidentRecorder) RecordIncident(ctx context.Context, inc reliability.IncidentRecord) error {
	if err := p.inner.RecordIncident(ctx, inc); err != nil {
		return err
	}
	p.publish(inc)
	return nil
}

// publish resolves the central by name and emits the event onto its bus.
// It is best-effort: an unknown central or a nil bus is silently skipped so
// the (already-persisted) incident path never fails on the publish side.
func (p *PublishingIncidentRecorder) publish(inc reliability.IncidentRecord) {
	if p.reg == nil {
		return
	}
	unit, ok := p.reg.Get(inc.CentralName)
	if !ok || unit == nil || unit.EventBus == nil {
		return
	}
	events.Publish(unit.EventBus, hmevent.IncidentRecordedEvent{
		Base:         hmevent.NewBase(),
		CentralName:  inc.CentralName,
		InterfaceID:  inc.InterfaceID,
		IncidentType: inc.Type,
		Severity:     inc.Severity,
		Message:      inc.Message,
		Details:      inc.Details,
	})
}
