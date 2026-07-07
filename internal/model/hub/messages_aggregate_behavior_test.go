// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Behavior tests for the four hub-message aggregates
// (Alarm / Service / Inbox / Connectivity) covering:
// - Replace / acknowledge round-trips
// - Callback firing on change, deduplication on no-change
// - Suppress path (nil Ack)
// - Quittable-count, LatestTimestamp helpers
// - Inbox Replace + List
// - Connectivity OnState / AllReachable
// - Program.Execute optimistic-update path
// - Sysvar.WriteUnconfirmedValue + OnValue confirmation

package hub

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── Alarm messages ───────────────────────────────────────────────────────────

func TestAlarmMessagesReplaceFiresCallback(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	am := NewAlarmMessages(nil)
	am.OnUpdate(func(_ []AlarmMessage) { calls.Add(1) })

	am.Replace([]AlarmMessage{{ID: "1", Timestamp: time.Now()}})
	if calls.Load() != 1 {
		t.Fatalf("callback count=%d after first Replace, want 1", calls.Load())
	}
}

func TestAlarmMessagesReplaceSameSetNoCallback(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	am := NewAlarmMessages(nil)
	ts := time.Now()
	am.Replace([]AlarmMessage{{ID: "1", Timestamp: ts, Counter: 1}})
	am.OnUpdate(func(_ []AlarmMessage) { calls.Add(1) })
	// Replace with identical set → no callback.
	am.Replace([]AlarmMessage{{ID: "1", Timestamp: ts, Counter: 1}})
	if calls.Load() != 0 {
		t.Fatalf("expected no callback for identical set, got %d", calls.Load())
	}
}

func TestAlarmMessagesAcknowledgeRemovesEntry(t *testing.T) {
	t.Parallel()
	ack := &stubAck{}
	am := NewAlarmMessages(ack)
	am.Replace([]AlarmMessage{{ID: "msg-1", Timestamp: time.Now()}})
	if am.Count() != 1 {
		t.Fatalf("Count=%d before ack, want 1", am.Count())
	}
	if err := am.Acknowledge(context.Background(), "msg-1"); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if am.Count() != 0 {
		t.Fatalf("Count=%d after ack, want 0", am.Count())
	}
	if len(ack.ids) == 0 || ack.ids[0] != "msg-1" {
		t.Errorf("acknowledger not called with msg-1, got %v", ack.ids)
	}
}

func TestAlarmMessagesAcknowledgeWithNilAckReturnsError(t *testing.T) {
	t.Parallel()
	am := NewAlarmMessages(nil)
	am.Replace([]AlarmMessage{{ID: "x", Timestamp: time.Now()}})
	err := am.Acknowledge(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error when Ack is nil")
	}
}

func TestAlarmMessagesLatestTimestamp(t *testing.T) {
	t.Parallel()
	am := NewAlarmMessages(nil)
	if !am.LatestTimestamp().IsZero() {
		t.Fatal("LatestTimestamp on empty must be zero")
	}
	t1 := time.Now().Add(-time.Second)
	t2 := time.Now()
	am.Replace([]AlarmMessage{
		{ID: "a", Timestamp: t1},
		{ID: "b", Timestamp: t2},
	})
	if !am.LatestTimestamp().Equal(t2) {
		t.Errorf("LatestTimestamp=%v, want %v", am.LatestTimestamp(), t2)
	}
}

// ─── Service messages ─────────────────────────────────────────────────────────

func TestServiceMessagesReplaceFiresCallback(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	sm := NewServiceMessages(nil)
	sm.OnUpdate(func(_ []ServiceMessage) { calls.Add(1) })
	sm.Replace([]ServiceMessage{{ID: "s1", Timestamp: time.Now()}})
	if calls.Load() != 1 {
		t.Fatalf("callback count=%d after Replace, want 1", calls.Load())
	}
}

func TestServiceMessagesQuittableCount(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessages(nil)
	sm.Replace([]ServiceMessage{
		{ID: "q1", Quittable: true, Timestamp: time.Now()},
		{ID: "q2", Quittable: false, Timestamp: time.Now()},
		{ID: "q3", Quittable: true, Timestamp: time.Now()},
	})
	if got := sm.QuittableCount(); got != 2 {
		t.Errorf("QuittableCount=%d, want 2", got)
	}
}

func TestServiceMessagesAcknowledgeRequiresAckInterface(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessages(nil)
	sm.Replace([]ServiceMessage{{ID: "svc-1", Timestamp: time.Now()}})
	err := sm.Acknowledge(context.Background(), "svc-1")
	if err == nil {
		t.Fatal("expected error when Ack is nil")
	}
}

func TestServiceMessagesAcknowledgeCallsAckerAndRemoves(t *testing.T) {
	t.Parallel()
	ack := &stubAck{}
	sm := NewServiceMessagesWithCentral("c1", ack)
	sm.Replace([]ServiceMessage{{ID: "svc-2", Quittable: true, Timestamp: time.Now()}})
	if err := sm.Acknowledge(context.Background(), "svc-2"); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if sm.Count() != 0 {
		t.Fatalf("Count=%d after ack, want 0", sm.Count())
	}
	if len(ack.ids) == 0 || ack.ids[0] != "svc-2" {
		t.Errorf("acker not called with svc-2, got %v", ack.ids)
	}
}

// TestServiceMessagesDisableDelegatesToAcknowledge verifies that Disable
// shares Acknowledge's underlying call (the CCU has one dismiss
// primitive) — same acker invocation, same removal from the live set.
func TestServiceMessagesDisableDelegatesToAcknowledge(t *testing.T) {
	t.Parallel()
	ack := &stubAck{}
	sm := NewServiceMessagesWithCentral("c1", ack)
	sm.Replace([]ServiceMessage{{ID: "svc-3", Quittable: true, Timestamp: time.Now()}})
	if err := sm.Disable(context.Background(), "svc-3"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if sm.Count() != 0 {
		t.Fatalf("Count=%d after disable, want 0", sm.Count())
	}
	if len(ack.ids) == 0 || ack.ids[0] != "svc-3" {
		t.Errorf("acker not called with svc-3, got %v", ack.ids)
	}
}

// TestServiceMessagesDisableRequiresAckInterface mirrors
// TestServiceMessagesAcknowledgeRequiresAckInterface for Disable.
func TestServiceMessagesDisableRequiresAckInterface(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessages(nil)
	sm.Replace([]ServiceMessage{{ID: "svc-4", Timestamp: time.Now()}})
	if err := sm.Disable(context.Background(), "svc-4"); err == nil {
		t.Fatal("expected error when Ack is nil")
	}
}

func TestServiceMessagesLatestTimestamp(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessages(nil)
	if !sm.LatestTimestamp().IsZero() {
		t.Fatal("LatestTimestamp on empty must be zero")
	}
	t1 := time.Now().Add(-time.Second)
	t2 := time.Now()
	sm.Replace([]ServiceMessage{
		{ID: "x", Timestamp: t1},
		{ID: "y", Timestamp: t2},
	})
	if !sm.LatestTimestamp().Equal(t2) {
		t.Errorf("LatestTimestamp=%v, want %v", sm.LatestTimestamp(), t2)
	}
}

// ─── Inbox ────────────────────────────────────────────────────────────────────

func TestInboxReplaceAndList(t *testing.T) {
	t.Parallel()
	inbox := NewInboxWithCentral("c1")
	if inbox.Count() != 0 {
		t.Fatalf("Count=%d before any Replace, want 0", inbox.Count())
	}
	inbox.Replace([]InboxDevice{
		{Address: "DEV001", Model: "HM-001"},
		{Address: "DEV002", Model: "HM-002"},
	})
	if inbox.Count() != 2 {
		t.Fatalf("Count=%d after Replace with 2, want 2", inbox.Count())
	}
	list := inbox.List()
	if len(list) != 2 {
		t.Fatalf("List len=%d, want 2", len(list))
	}
}

func TestInboxReplaceFiresCallbackOnChange(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	inbox := NewInbox()
	inbox.OnUpdate(func(_ []InboxDevice) { calls.Add(1) })
	inbox.Replace([]InboxDevice{{Address: "D1"}})
	if calls.Load() != 1 {
		t.Fatalf("callback count=%d, want 1", calls.Load())
	}
	// Same address set: no further callback.
	inbox.Replace([]InboxDevice{{Address: "D1"}})
	if calls.Load() != 1 {
		t.Fatalf("callback should not fire for identical set, got %d", calls.Load())
	}
}

// ─── Connectivity ─────────────────────────────────────────────────────────────

func TestConnectivityOnStateAndAllReachable(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	ok, observed := c.AllReachable()
	if observed {
		t.Fatal("AllReachable observed must be false before first observation")
	}
	_ = ok

	c.OnState("HmIP-RF", true)
	c.OnState("BidCos-RF", true)
	ok, observed = c.AllReachable()
	if !observed || !ok {
		t.Errorf("AllReachable=(%v,%v), want (true,true)", ok, observed)
	}

	c.OnState("HmIP-RF", false)
	ok, observed = c.AllReachable()
	if !observed || ok {
		t.Errorf("AllReachable=(%v,%v), want (false,true) when one is down", ok, observed)
	}
}

func TestConnectivityFiresCallbackOnChange(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	c := NewConnectivity()
	c.OnUpdate(func(_ InterfaceReachability) { calls.Add(1) })
	c.OnState("iface-1", true)
	if calls.Load() != 1 {
		t.Fatalf("callback count=%d, want 1", calls.Load())
	}
	// Same value again: no callback.
	c.OnState("iface-1", true)
	if calls.Load() != 1 {
		t.Fatalf("no callback expected for same state, got %d", calls.Load())
	}
}

// ─── Program Execute optimistic-update ───────────────────────────────────────

type stubProgramWriterParity struct {
	err    error
	called bool
}

func (s *stubProgramWriterParity) ExecuteProgram(_ context.Context, _ string) error {
	s.called = true
	return s.err
}

func (s *stubProgramWriterParity) SetProgramEnabled(_ context.Context, _ string, _ bool) error {
	return nil
}

func TestProgramExecuteCallsWriter(t *testing.T) {
	t.Parallel()
	w := &stubProgramWriterParity{}
	p := NewProgram("c1", "prog-1", "Light Schedule", "", false, w)
	if err := p.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !w.called {
		t.Fatal("writer.ExecuteProgram not called")
	}
}

func TestProgramExecuteNilWriterReturnsError(t *testing.T) {
	t.Parallel()
	p := NewProgram("c1", "prog-2", "No Writer", "", false, nil)
	err := p.Execute(context.Background())
	if err == nil {
		t.Fatal("expected error when Writer is nil")
	}
}

func TestProgramExecuteWriterErrorPropagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("writer failure")
	w := &stubProgramWriterParity{err: boom}
	p := NewProgram("c1", "prog-3", "Will Fail", "", false, w)
	if err := p.Execute(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Execute error=%v, want %v", err, boom)
	}
}

// ─── Sysvar optimistic-update (WriteUnconfirmedValue + OnValue) ───────────────

type stubSysvarWriterParity struct {
	calls []any
	err   error
}

func (s *stubSysvarWriterParity) SetSysvar(_ context.Context, _ string, v any) error {
	s.calls = append(s.calls, v)
	return s.err
}

func TestSysvarWriteUnconfirmedValueIsVisible(t *testing.T) {
	t.Parallel()
	w := &stubSysvarWriterParity{}
	sv := NewSysvar("c1", "light_mode", "", hmenum.HubValueTypeInteger, w)

	// Before any write: Value() returns the zero value with ok=false.
	_, ok := sv.ConfirmedValue()
	if ok {
		t.Fatal("expected ok=false before first observation")
	}

	// Write optimistic value; Value() shadows ConfirmedValue.
	sv.WriteUnconfirmedValue(hmtypes.IntValue(3))
	v, ok := sv.Value()
	if !ok {
		t.Fatal("expected ok=true after WriteUnconfirmedValue")
	}
	if v.Int != 3 {
		t.Errorf("Value Int=%d, want 3", v.Int)
	}
}

func TestSysvarConfirmedValueIgnoresOptimistic(t *testing.T) {
	t.Parallel()
	w := &stubSysvarWriterParity{}
	sv := NewSysvar("c1", "light_mode2", "", hmenum.HubValueTypeInteger, w)

	// First confirm a real value.
	sv.OnValue(hmtypes.IntValue(5))
	// Now write an optimistic.
	sv.WriteUnconfirmedValue(hmtypes.IntValue(99))

	// Value() should return optimistic.
	v, _ := sv.Value()
	if v.Int != 99 {
		t.Errorf("Value()=%d, want 99 (optimistic)", v.Int)
	}

	// ConfirmedValue() must still return 5.
	cv, ok := sv.ConfirmedValue()
	if !ok || cv.Int != 5 {
		t.Errorf("ConfirmedValue()=(%v,%v), want (5,true)", cv, ok)
	}
}

func TestSysvarOnValueClearsUnconfirmedParity(t *testing.T) {
	t.Parallel()
	w := &stubSysvarWriterParity{}
	sv := NewSysvar("c1", "light_mode3", "", hmenum.HubValueTypeInteger, w)
	sv.WriteUnconfirmedValue(hmtypes.IntValue(7))

	// CCU confirms a value: unconfirmed is cleared, Value() returns confirmed.
	sv.OnValue(hmtypes.IntValue(7))
	v, ok := sv.Value()
	if !ok {
		t.Fatal("expected ok=true after OnValue")
	}
	// The confirmed and displayed value should be 7.
	if v.Int != 7 {
		t.Errorf("Value after OnValue=%d, want 7", v.Int)
	}
	// ResetUnconfirmedValue should now be a no-op (already nil).
	sv.ResetUnconfirmedValue()
}
