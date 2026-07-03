// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"context"
	"testing"
)

// fakeEventACL is a minimal ACLChecker for event-authorization tests. It grants
// access only to allowFabric and only when the required privilege is at most
// maxPriv (so a View-only subject is denied the Administer that AccessControl
// events require). fabricIndex==0 always passes (PASE bypass).
type fakeEventACL struct {
	allowFabric uint8
	maxPriv     uint8
}

func (f fakeEventACL) CheckACL(_ context.Context, fabricIndex uint8, _ uint64, _ []uint32, _ uint16, _ uint32, requiredPriv uint8) StatusCode {
	if fabricIndex == 0 {
		return StatusSuccess
	}
	if fabricIndex == f.allowFabric && requiredPriv <= f.maxPriv {
		return StatusSuccess
	}
	return StatusUnsupportedAccess
}

var _ ACLChecker = fakeEventACL{}

// fakeACEvent stands in for cluster/core.AccessControlEntryChangedEvent — the
// im package cannot import the cluster packages (import cycle), so the
// fabric-index extraction is duck-typed against any struct that carries a
// FabricIndex field.
type fakeACEvent struct {
	FabricIndex uint8
}

const (
	tstAccessControlCluster uint32 = 0x001F
	tstOnOffCluster         uint32 = 0x0006
	tstACEntryChangedEvent  uint32 = 0x0000
)

func acEventReport(recordFabric uint8) EventReport {
	return EventReport{
		Path: ConcreteEventPath{
			Endpoint: 0, HasEndpoint: true,
			Cluster: tstAccessControlCluster, HasCluster: true,
			Event: tstACEntryChangedEvent, HasEvent: true,
		},
		Number:   7,
		Priority: EventPriorityInfo,
		Data:     AttributeValue{Value: fakeACEvent{FabricIndex: recordFabric}},
	}
}

func plainEventReport() EventReport {
	return EventReport{
		Path: ConcreteEventPath{
			Endpoint: 1, HasEndpoint: true,
			Cluster: tstOnOffCluster, HasCluster: true,
			Event: 0x00, HasEvent: true,
		},
		Number:   8,
		Priority: EventPriorityInfo,
		Data:     AttributeValue{Value: "switch-pressed"},
	}
}

// TestAuthorizeEventReports_FabricSensitiveOwningFabricKept verifies that the
// fabric that OWNS a fabric-sensitive AccessControl event (matching record
// FabricIndex) and holds Administer receives it.
func TestAuthorizeEventReports_FabricSensitiveOwningFabricKept(t *testing.T) {
	t.Parallel()
	auth := EventReadAuthorizer{
		Checker:     fakeEventACL{allowFabric: 1, maxPriv: 5}, // Administer on fabric 1
		FabricIndex: 1,
	}
	got := AuthorizeEventReports(context.Background(), auth, []EventReport{acEventReport(1)})
	if len(got) != 1 {
		t.Fatalf("owning-fabric Administer subject: want 1 event, got %d", len(got))
	}
}

// TestAuthorizeEventReports_FabricSensitiveCrossFabricDropped verifies that a
// DIFFERENT fabric — even one holding Administer — never receives another
// fabric's fabric-sensitive AccessControl event (Matter §8.4.3.2 /
// §9.10.7.1). Reproduces the cross-fabric leak the fix closes.
func TestAuthorizeEventReports_FabricSensitiveCrossFabricDropped(t *testing.T) {
	t.Parallel()
	auth := EventReadAuthorizer{
		Checker:     fakeEventACL{allowFabric: 2, maxPriv: 5}, // Administer on fabric 2
		FabricIndex: 2,
	}
	// Record belongs to fabric 1; the accessing fabric is 2.
	got := AuthorizeEventReports(context.Background(), auth, []EventReport{acEventReport(1)})
	if len(got) != 0 {
		t.Fatalf("cross-fabric fabric-sensitive event must be dropped: got %d events", len(got))
	}
}

// TestAuthorizeEventReports_NonAdministerSubjectDenied verifies that a subject
// on the owning fabric that holds only View is denied the AccessControl event
// (which requires Administer).
func TestAuthorizeEventReports_NonAdministerSubjectDenied(t *testing.T) {
	t.Parallel()
	auth := EventReadAuthorizer{
		Checker:     fakeEventACL{allowFabric: 1, maxPriv: 1}, // only View on fabric 1
		FabricIndex: 1,
	}
	got := AuthorizeEventReports(context.Background(), auth, []EventReport{acEventReport(1)})
	if len(got) != 0 {
		t.Fatalf("View-only subject must be denied AccessControl events: got %d", len(got))
	}
}

// TestAuthorizeEventReports_PlainEventReturnedToAuthorized verifies that a
// non-fabric-sensitive event (OnOff switch) is returned to a View subject and
// is NOT subject to the fabric-sensitive record drop.
func TestAuthorizeEventReports_PlainEventReturnedToAuthorized(t *testing.T) {
	t.Parallel()
	auth := EventReadAuthorizer{
		Checker:     fakeEventACL{allowFabric: 1, maxPriv: 1}, // View on fabric 1
		FabricIndex: 1,
	}
	got := AuthorizeEventReports(context.Background(), auth, []EventReport{plainEventReport()})
	if len(got) != 1 {
		t.Fatalf("plain event to View subject: want 1, got %d", len(got))
	}
}

// TestAuthorizeEventReports_PASEBypass verifies that a PASE session
// (FabricIndex==0) receives every event unchanged — ACL/fabric filtering does
// not apply before commissioning.
func TestAuthorizeEventReports_PASEBypass(t *testing.T) {
	t.Parallel()
	auth := EventReadAuthorizer{
		Checker:     fakeEventACL{allowFabric: 1, maxPriv: 5},
		FabricIndex: 0, // PASE
	}
	in := []EventReport{acEventReport(1), plainEventReport()}
	got := AuthorizeEventReports(context.Background(), auth, in)
	if len(got) != 2 {
		t.Fatalf("PASE bypass: want 2 events, got %d", len(got))
	}
}

// TestAuthorizeEventReports_NilCheckerFailOpen verifies that a nil ACLChecker
// returns every event unchanged, matching the attribute path's "dispatcher
// without ACLChecker" fallback.
func TestAuthorizeEventReports_NilCheckerFailOpen(t *testing.T) {
	t.Parallel()
	auth := EventReadAuthorizer{Checker: nil, FabricIndex: 1}
	in := []EventReport{acEventReport(1), plainEventReport()}
	got := AuthorizeEventReports(context.Background(), auth, in)
	if len(got) != 2 {
		t.Fatalf("nil checker fail-open: want 2 events, got %d", len(got))
	}
}

// TestAuthorizeEventReports_FabricSensitiveUnknownFabricFailsClosed verifies
// that a fabric-sensitive event whose payload exposes no FabricIndex is dropped
// (fail closed) — the record's fabric cannot be positively matched.
func TestAuthorizeEventReports_FabricSensitiveUnknownFabricFailsClosed(t *testing.T) {
	t.Parallel()
	ev := acEventReport(1)
	ev.Data = AttributeValue{Value: "no-fabric-index-here"} // payload lacks FabricIndex
	auth := EventReadAuthorizer{
		Checker:     fakeEventACL{allowFabric: 1, maxPriv: 5},
		FabricIndex: 1,
	}
	got := AuthorizeEventReports(context.Background(), auth, []EventReport{ev})
	if len(got) != 0 {
		t.Fatalf("fabric-sensitive event with undeterminable fabric must fail closed: got %d", len(got))
	}
}

// TestEventPayloadFabricIndex covers the duck-typed FabricIndex extraction.
func TestEventPayloadFabricIndex(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload any
		want    uint8
		ok      bool
	}{
		{"struct value", fakeACEvent{FabricIndex: 3}, 3, true},
		{"struct pointer", &fakeACEvent{FabricIndex: 4}, 4, true},
		{"nil", nil, 0, false},
		{"nil pointer", (*fakeACEvent)(nil), 0, false},
		{"no field", struct{ X int }{X: 5}, 0, false},
		{"non-struct", "str", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := eventPayloadFabricIndex(tc.payload)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("eventPayloadFabricIndex(%v) = (%d,%v), want (%d,%v)", tc.payload, got, ok, tc.want, tc.ok)
			}
		})
	}
}
