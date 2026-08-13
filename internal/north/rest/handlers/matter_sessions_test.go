// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// stubSessionLister returns a fixed session set.
type stubSessionLister struct{ sessions []handlers.MatterSessionInfo }

func (s stubSessionLister) MatterSessions() []handlers.MatterSessionInfo { return s.sessions }

// TestMatterSessionsReportsPerPeerIdleAge pins the field the whole
// endpoint exists for.
//
// A controller that disappears without closing its session leaves that
// session open and simply stops sending. Local activity keeps moving —
// the bridge is still reporting into it — so only the peer-side age
// distinguishes a live controller from a dead one. Collapsing the two
// timestamps into a single "last seen" would erase exactly the signal
// an operator needs.
func TestMatterSessionsReportsPerPeerIdleAge(t *testing.T) {
	t.Parallel()

	now := time.Now()
	lister := stubSessionLister{sessions: []handlers.MatterSessionInfo{
		{
			SessionID:        7,
			FabricIndex:      1,
			PeerNodeID:       0xDEADBEEF,
			LocalNodeID:      0x1122334455667788,
			Subscriptions:    3,
			LastActivity:     now.Add(-5 * time.Second),
			LastPeerActivity: now.Add(-20 * time.Minute),
		},
	}}

	rec := httptest.NewRecorder()
	handlers.MatterSessions(lister)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/matter/sessions", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body handlers.MatterSessionList
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(body.Sessions))
	}
	got := body.Sessions[0]
	if got.PeerIdleSeconds < 1100 {
		t.Errorf("peer_idle_seconds = %d, want roughly 1200 — the peer has been quiet for 20 minutes, "+
			"which is the only signal that separates a departed controller from a live one",
			got.PeerIdleSeconds)
	}
	if got.IdleSeconds > 60 {
		t.Errorf("idle_seconds = %d, want a few seconds — the bridge itself was active recently",
			got.IdleSeconds)
	}
	if got.Subscriptions != 3 {
		t.Errorf("subscriptions = %d, want 3", got.Subscriptions)
	}
	if got.PeerNodeID != "00000000DEADBEEF" {
		t.Errorf("peer_node_id = %q, want the 16-digit hex node id", got.PeerNodeID)
	}
}

// TestMatterSessionsWithoutBridgeReports503 pins the same
// bridge-disabled contract the other Matter endpoints follow, so a
// caller can tell "Matter is off" from "no sessions".
func TestMatterSessionsWithoutBridgeReports503(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	handlers.MatterSessions(nil)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/matter/sessions", http.NoBody))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 — an empty list would read as 'no controller connected', which "+
			"is a different and much more alarming statement than 'the bridge is off'", rec.Code)
	}
}

// TestMatterSessionsDistinguishesCommissioningSessions pins that a PASE
// session is reported as such: it is expected to be short-lived and
// fabric-less, so an operator must not read it as a commissioned
// controller that lost its fabric.
func TestMatterSessionsDistinguishesCommissioningSessions(t *testing.T) {
	t.Parallel()

	lister := stubSessionLister{sessions: []handlers.MatterSessionInfo{
		{SessionID: 1, FabricIndex: 0, IsPASE: true, LastActivity: time.Now(), LastPeerActivity: time.Now()},
		{SessionID: 2, FabricIndex: 1, IsPASE: false, LastActivity: time.Now(), LastPeerActivity: time.Now()},
	}}

	rec := httptest.NewRecorder()
	handlers.MatterSessions(lister)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/matter/sessions", http.NoBody))

	var body handlers.MatterSessionList
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(body.Sessions))
	}
	if !body.Sessions[0].IsPASE || body.Sessions[0].FabricIndex != 0 {
		t.Error("the commissioning session must report is_pase with fabric index 0")
	}
	if body.Sessions[1].IsPASE {
		t.Error("the operational session must not report is_pase")
	}
}
