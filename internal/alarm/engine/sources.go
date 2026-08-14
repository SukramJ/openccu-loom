// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine

import (
	"context"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// recordSource adds one contributing data point to the running
// incident: to the zone's in-memory list and to the durable ledger.
//
// It reports whether the source was new. A repeat activation of the
// same data point within one incident returns false, so callers do not
// re-publish an unchanged list — a flapping contact must not turn into
// an event storm.
//
// The write is synchronous, matching the journal and the incident
// itself. That ordering is deliberate rather than incidental: trigger
// already persists the incident before firing outputs so a crash can
// only over-count, and the ledger row belongs to that same
// safety-first phase. Making this one write asynchronous would not
// remove the disk from the trigger path — the journal and the incident
// are still on it — it would only make the audit trail lossier than
// the counters it explains.
//
// The caller holds the lock.
func (e *Engine) recordSource(ctx context.Context, a *zone, incidentID int64, cause incidentCause) bool {
	ref := cause.sourceRef(unixMS(e.clk.Now()))
	if ref.Empty() {
		// Causes without a data point (central loss, an adopted siren)
		// carry no source identity; the incident's cause document
		// already records them.
		return false
	}
	if a.sourceSeen == nil {
		a.sourceSeen = map[string]bool{}
	}
	if a.sourceSeen[ref.Ref] {
		return false
	}
	a.sourceSeen[ref.Ref] = true
	a.sources = append(a.sources, ref)

	if e.ledger == nil || incidentID == 0 {
		// An unpersisted incident (a failed Create) still accumulates
		// in memory so the live event stays complete; only the durable
		// half is lost, and that failure is already journaled.
		return true
	}
	row := sqlitestore.AlarmIncidentSource{
		IncidentID:     incidentID,
		ZoneID:         a.id,
		Ref:            ref.Ref,
		CentralName:    ref.Central,
		InterfaceID:    ref.InterfaceID,
		ChannelAddress: ref.ChannelAddress,
		DeviceAddress:  ref.DeviceAddress,
		Parameter:      ref.Parameter,
		SensorID:       ref.SensorID,
		Name:           ref.Name,
		SensorType:     string(ref.SensorType),
		Class:          string(ref.Class),
		Cause:          cause.Kind,
		AtMS:           ref.AtMS,
	}
	if err := e.ledger.Append(ctx, row); err != nil {
		e.log.Error("alarm incident source append failed",
			"zone", a.id, "incident", incidentID, "ref", ref.Ref, "error", err)
	}
	return true
}

// fireCycle hands one output cycle to the driver layer. It stamps the
// zone snapshot every cycle carries — the display name and the sources
// accumulated so far — and journals a driver failure.
//
// Every fire goes through here so the snapshot cannot be forgotten at
// one call site: the driver's notification sink runs under the engine
// lock and has no other way to learn either value ([FireOptions]).
//
// The caller holds the lock.
func (e *Engine) fireCycle(ctx context.Context, a *zone, inc sqlitestore.AlarmIncident, opts FireOptions) {
	opts.ZoneName = a.name
	opts.Sources = a.sourcesCopy()
	if err := e.outputs.FireCycle(ctx, a.id, inc, opts); err != nil {
		e.journalFault(ctx, a, "output_fire_failed", err, inc.ID)
	}
}

// publishSourcesChanged re-publishes the trigger event for a zone
// whose source list grew while it was already triggered. The state
// machine deliberately does not re-trigger in that case, so this is
// the only signal a consumer gets that a second detector fired.
//
// The headline sensor stays the one that opened the incident; the
// growth is in Sources. The caller holds the lock.
func (e *Engine) publishSourcesChanged(a *zone, incidentID int64) {
	sources := a.sourcesCopy()
	if len(sources) == 0 {
		return
	}
	first := sources[0]
	e.sink.Publish(hmevent.AlarmTriggeredEvent{
		Base:       hmevent.NewBaseAt(e.clk.Now()),
		ZoneID:     a.id,
		ZoneName:   a.name,
		IncidentID: incidentID,
		SensorID:   first.SensorID,
		SensorName: first.Name,
		Cause:      causeKindSensor,
		Mode:       a.mode,
		Sources:    sources,
	})
}

// IncidentSources returns the accumulated sources of a zone's running
// incident, oldest first. It returns nil for an unknown zone or one
// without a running incident.
//
// Consumers that need the sources off the engine goroutine — the
// notification fan-out, REST — read them here instead of the database,
// so a notification never waits on a query.
func (e *Engine) IncidentSources(zoneID string) []hmevent.SecuritySourceRef {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[zoneID]
	if !ok {
		return nil
	}
	return a.sourcesCopy()
}
