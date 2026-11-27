// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package audit

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// TestRecordFromStampsTheRequestActor pins the three rules RecordFrom applies:
// the request's principal fills an empty User, a producer's own User wins,
// and OnBehalfOf comes only from the request.
func TestRecordFromStampsTheRequestActor(t *testing.T) {
	ctx := WithActor(context.Background(), Actor{User: "ha-token", OnBehalfOf: "Alex (ha:1)"})
	buf := NewBuffer(10)

	RecordFrom(ctx, buf, Entry{Action: ActionParamsetWrite})
	RecordFrom(ctx, buf, Entry{Action: ActionParamsetWrite, User: "system"})
	RecordFrom(context.Background(), buf, Entry{Action: ActionParamsetWrite, OnBehalfOf: "forged"})

	got := buf.List(10)
	if len(got) != 3 {
		t.Fatalf("recorded %d rows, want 3", len(got))
	}
	// Head is most recent.
	if e := got[2]; e.User != "ha-token" || e.OnBehalfOf != "Alex (ha:1)" {
		t.Errorf("empty producer user: got user %q on_behalf_of %q", e.User, e.OnBehalfOf)
	}
	if e := got[1]; e.User != "system" || e.OnBehalfOf != "Alex (ha:1)" {
		t.Errorf("producer user: got user %q on_behalf_of %q", e.User, e.OnBehalfOf)
	}
	if e := got[0]; e.User != "" || e.OnBehalfOf != "" {
		t.Errorf("no request actor: got user %q on_behalf_of %q, want both empty", e.User, e.OnBehalfOf)
	}

	RecordFrom(ctx, nil, Entry{Action: ActionParamsetWrite}) // must not panic
}

// TestReplayKeepsTheStoredRowVerbatim pins the boot-time hydration path: a row
// the durable store already holds comes back with its principal, its "on
// behalf of" assertion and its ID unchanged. Routing it through RecordFrom
// instead would erase the assertion, because a replay carries no request.
func TestReplayKeepsTheStoredRowVerbatim(t *testing.T) {
	buf := NewBuffer(10)
	stored := Entry{ID: 41, Action: ActionParamsetWrite, User: "ha-token", OnBehalfOf: "Alex (ha:1)"}

	buf.Replay(stored)

	got := buf.List(1)
	if len(got) != 1 {
		t.Fatalf("replayed %d rows, want 1", len(got))
	}
	if e := got[0]; e.ID != 41 || e.User != "ha-token" || e.OnBehalfOf != "Alex (ha:1)" {
		t.Errorf("replayed row: got id %d user %q on_behalf_of %q, want 41 %q %q",
			e.ID, e.User, e.OnBehalfOf, "ha-token", "Alex (ha:1)")
	}
}

// TestActorFromWithoutRequest pins that work no request started reports no
// actor, for a nil context as for one that never had an actor attached.
func TestActorFromWithoutRequest(t *testing.T) {
	var none context.Context
	if a, ok := ActorFrom(none); ok || a != (Actor{}) {
		t.Errorf("nil context: got %+v, %v; want zero actor, false", a, ok)
	}
	if a, ok := ActorFrom(context.Background()); ok || a != (Actor{}) {
		t.Errorf("background context: got %+v, %v; want zero actor, false", a, ok)
	}
}

// TestRecordFromStampsTheBoundCentral pins how a row gets its central: the
// central the producer resolved the device on wins, an empty one takes the
// request's central binding, and an unbound request with no producer central
// leaves the row central-less (a daemon-wide change).
func TestRecordFromStampsTheBoundCentral(t *testing.T) {
	bound := hmreqctx.WithCentralScope(context.Background(), "ccu-02")
	buf := NewBuffer(10)

	RecordFrom(bound, buf, Entry{Action: ActionParamsetWrite})
	RecordFrom(bound, buf, Entry{Action: ActionParamsetWrite, Central: "ccu-01"})
	RecordFrom(context.Background(), buf, Entry{Action: ActionParamsetWrite})

	got := buf.List(10)
	if len(got) != 3 {
		t.Fatalf("recorded %d rows, want 3", len(got))
	}
	// Head is most recent.
	if got[2].Central != "ccu-02" {
		t.Errorf("bound request, empty producer central: got %q, want ccu-02", got[2].Central)
	}
	if got[1].Central != "ccu-01" {
		t.Errorf("producer central: got %q, want ccu-01", got[1].Central)
	}
	if got[0].Central != "" {
		t.Errorf("unbound request: got %q, want empty", got[0].Central)
	}
}
