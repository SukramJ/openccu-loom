// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

// MatterEvent is the type the REST + bridge layers publish through
// the daemon's WebSocket hub for SPA-side reactive updates. Topic
// strings match the catalogue documented in
// `docs/matter-ui-concept.md` §3.
type MatterEvent struct {
	// Topic is the WS hub topic the event is published under. Use
	// the `MatterTopic*` constants below.
	Topic string
	// Type is the wire `type` field clients see — short, lowercase,
	// matches the trailing segment of Topic.
	Type string
	// When is the event timestamp; the publisher fills this when
	// zero.
	When time.Time
	// Payload is the JSON-marshalable event body.
	Payload any
}

// MatterEventPublisher is the narrow facade the REST handlers + the
// bridge call into to surface SPA-visible state changes. The daemon
// wires this to `ws.Hub.Publish`.
type MatterEventPublisher interface {
	PublishMatterEvent(ctx context.Context, ev MatterEvent)
}

// MatterTopologyReassembler is the narrow facade the exposable-update
// handlers call into after a successful matter_exposures row write so
// the bridge's topology reflects the new allowlist state. Without
// this hook the persisted change only takes effect on the next daemon
// restart — the open-commissioning-window endpoint rejects new
// requests with `topology_no_bridged_endpoints` until then.
//
// The daemon wires this to [Bridge.Reassemble]. nil is a no-op (the
// test paths run without a live bridge).
type MatterTopologyReassembler interface {
	Reassemble(ctx context.Context) error
}

// MatterEventTopic constants documented in
// `docs/matter-ui-concept.md` §3. Adding a new topic here requires
// matching SPA-side wiring + (typically) one publisher call site.
const (
	// MatterTopicExposableChanged fires when an allowlist row is
	// inserted, updated or deleted. Payload: the affected
	// MatterExposureUpdate.
	MatterTopicExposableChanged = "matter.exposable_changed"
	// MatterTopicCommissioningWindowOpened fires when a
	// commissioning window opens (REST + cluster command).
	// Payload: the resulting MatterCommissioningWindowResponse.
	MatterTopicCommissioningWindowOpened = "matter.commissioning_window_opened"
	// MatterTopicCommissioningProgress fires during commissioning
	// state transitions. Payload: free-form progress object
	// (`{stage, message}`).
	MatterTopicCommissioningProgress = "matter.commissioning_progress"
	// MatterTopicFabricAdded fires after AddNOC + initial CASE
	// pickup. Payload: `MatterFabricResponse`.
	MatterTopicFabricAdded = "matter.fabric_added"
	// MatterTopicFabricRemoved fires after a fabric revocation.
	// Payload: `{fabric_index}`.
	MatterTopicFabricRemoved = "matter.fabric_removed"
	// MatterTopicEndpointAssembled fires after Bridge.Reassemble
	// completes. Payload: `{endpoint_count}`.
	MatterTopicEndpointAssembled = "matter.endpoint_assembled"
)

// publishMatterEvent is a tiny safe wrapper for handlers that may
// run with publisher == nil (test paths / bridge disabled).
func publishMatterEvent(ctx context.Context, p MatterEventPublisher, topic string, payload any) {
	if p == nil {
		return
	}
	p.PublishMatterEvent(ctx, MatterEvent{
		Topic:   topic,
		Type:    typeFromTopic(topic),
		When:    time.Now().UTC(),
		Payload: payload,
	})
}

// typeFromTopic strips the leading `matter.` prefix so the wire
// `type` field matches the Type column from the wsapi catalogue.
func typeFromTopic(topic string) string {
	const prefix = "matter."
	if len(topic) > len(prefix) && topic[:len(prefix)] == prefix {
		return topic[len(prefix):]
	}
	return topic
}

// recordMatterAudit is a thin nil-safe wrapper around
// [audit.Recorder.Record]. The Matter mutation handlers call this
// after a successful database write so the change-history view
// reflects the mutation per `docs/matter-ui-concept.md` §6.
//
// The actor is read from the request context via
// [actorFromRequest]; pairing codes / passcodes are NEVER recorded
// (only the fact that a window opened, plus duration). This mirrors
// the §6 "Pairing codes do not appear in the audit log" rule.
func recordMatterAudit(rec audit.Recorder, req *http.Request, action audit.Action, note string) {
	if rec == nil {
		return
	}
	rec.Record(audit.Entry{
		User:   actorFromRequest(req),
		Action: action,
		Note:   note,
	})
}
