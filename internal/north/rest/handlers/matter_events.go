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
// `notes/concepts/matter-ui-concept.md` §3.
type MatterEvent struct {
	// Topic is the WS hub topic the event is published under. Use
	// the `MatterTopic*` constants below.
	Topic string
	// Type is the wire `type` field clients see. It carries the full
	// dotted event name — identical to Topic, `matter.<event>` — because
	// the wsapi.json envelope contract (assets/wsapi.json) defines `type`
	// as "the `name` field of the matching broadcast entry", and every
	// matter broadcast is named with the `matter.` prefix. Both the SPA
	// (assets/ui/src/lib/stores/matter.svelte.ts dispatches on
	// `case "matter.fabric_added"`) and the Python client key on the
	// prefixed name, so a bare trailing segment reaches no consumer.
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
// `notes/concepts/matter-ui-concept.md` §3. Adding a new topic here requires
// matching SPA-side wiring + (typically) one publisher call site.
const (
	// MatterTopicExposableChanged fires when an allowlist row is
	// inserted, updated or deleted. Payload: the affected
	// MatterExposureUpdate — one frame per row, so a bulk write emits
	// one frame per item rather than its request envelope. A subscriber
	// mirroring the allowlist applies each frame on its own.
	MatterTopicExposableChanged = "matter.exposable_changed"
	// MatterTopicCommissioningWindowOpened fires when a
	// commissioning window opens (REST + cluster command).
	// Payload: the resulting MatterCommissioningWindowResponse with the
	// pairing credential cleared — subscribing to a topic requires no
	// role, so passcode / QR / manual code stay in the admin-gated HTTP
	// response.
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
		Topic: topic,
		// The wire `type` carries the full dotted name, identical to the
		// topic: every other WS broadcast family emits its prefixed name
		// and both consumers (SPA + Python client) key on `matter.<event>`.
		Type:    topic,
		When:    time.Now().UTC(),
		Payload: payload,
	})
}

// recordMatterAudit is a thin nil-safe wrapper around
// [audit.Recorder.Record]. The Matter mutation handlers call this
// after a successful database write so the change-history view
// reflects the mutation per `notes/concepts/matter-ui-concept.md` §6.
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
