// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package audit

import (
	"context"
	"testing"
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
