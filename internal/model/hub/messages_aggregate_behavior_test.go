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

// stubSuppressor records SuppressServiceMessage calls and serves a
// canned suppressed-parameter list per channel for GetSuppressedServiceMessages.
type stubSuppressor struct {
	calls  []suppressCall
	live   map[string][]string // channel → suppressed params
	err    error
	getErr error
}

type suppressCall struct {
	iface, channel, parameter string
	suppress                  bool
}

func (s *stubSuppressor) SuppressServiceMessage(_ context.Context, iface, channel, parameter string, suppress bool) error {
	if s.err != nil {
		return s.err
	}
	s.calls = append(s.calls, suppressCall{iface, channel, parameter, suppress})
	return nil
}

func (s *stubSuppressor) GetSuppressedServiceMessages(_ context.Context, _, channel string) ([]string, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.live[channel], nil
}

// TestServiceMessagesDisableSuppressesChannelParameter verifies that
// Disable resolves the message's channel + parameter and durably
// suppresses it on the CCU, then records it and removes it from the live
// set.
func TestServiceMessagesDisableSuppressesChannelParameter(t *testing.T) {
	t.Parallel()
	sup := &stubSuppressor{}
	sm := NewServiceMessagesWithCentral("c1", &stubAck{})
	sm.SetSuppressor(sup)
	sm.Replace([]ServiceMessage{{
		ID: "svc-3", Address: "ABC123:1", Parameter: "LOWBAT",
		InterfaceID: "HmIP-RF", Quittable: true, Timestamp: time.Now(),
	}})
	if err := sm.Disable(context.Background(), "svc-3"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if sm.Count() != 0 {
		t.Fatalf("Count=%d after disable, want 0", sm.Count())
	}
	if len(sup.calls) != 1 {
		t.Fatalf("suppressor calls=%d, want 1 (%v)", len(sup.calls), sup.calls)
	}
	got := sup.calls[0]
	if got.iface != "HmIP-RF" || got.channel != "ABC123:1" || got.parameter != "LOWBAT" || !got.suppress {
		t.Errorf("suppress call = %+v, want {HmIP-RF ABC123:1 LOWBAT true}", got)
	}
	if sm.SuppressedCount() != 1 {
		t.Errorf("SuppressedCount=%d, want 1", sm.SuppressedCount())
	}
}

// TestServiceMessagesDisableRequiresSuppressor asserts Disable fails when
// no suppressor is wired instead of silently succeeding.
func TestServiceMessagesDisableRequiresSuppressor(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessages(nil)
	sm.Replace([]ServiceMessage{{ID: "svc-4", Timestamp: time.Now()}})
	if err := sm.Disable(context.Background(), "svc-4"); err == nil {
		t.Fatal("expected error when no suppressor is configured")
	}
}

// TestServiceMessagesDisableUnknownID errors for an unknown message id.
func TestServiceMessagesDisableUnknownID(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessages(nil)
	sm.SetSuppressor(&stubSuppressor{})
	if err := sm.Disable(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for unknown message id")
	}
}

// TestServiceMessagesUnsuppressClearsRecord verifies Unsuppress clears the
// CCU suppression and drops the record from the management view.
func TestServiceMessagesUnsuppressClearsRecord(t *testing.T) {
	t.Parallel()
	sup := &stubSuppressor{live: map[string][]string{"ABC123:1": {"LOWBAT"}}}
	sm := NewServiceMessages(nil)
	sm.SetSuppressor(sup)
	sm.Replace([]ServiceMessage{{
		ID: "svc-5", Address: "ABC123:1", Parameter: "LOWBAT",
		InterfaceID: "HmIP-RF", Timestamp: time.Now(),
	}})
	if err := sm.Disable(context.Background(), "svc-5"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	// Interface omitted on unsuppress → resolved from the stored record.
	if err := sm.Unsuppress(context.Background(), "", "ABC123:1", "LOWBAT"); err != nil {
		t.Fatalf("Unsuppress: %v", err)
	}
	if sm.SuppressedCount() != 0 {
		t.Fatalf("SuppressedCount=%d after unsuppress, want 0", sm.SuppressedCount())
	}
	last := sup.calls[len(sup.calls)-1]
	if last.suppress || last.iface != "HmIP-RF" || last.parameter != "LOWBAT" {
		t.Errorf("last call = %+v, want a clear on HmIP-RF/LOWBAT", last)
	}
}

// TestServiceMessagesDisableSuppressorErrorPropagates verifies that a
// failing CCU suppress call is returned to the caller and the message
// stays in the active set (nothing is recorded as suppressed).
func TestServiceMessagesDisableSuppressorErrorPropagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("ccu unreachable")
	sup := &stubSuppressor{err: boom}
	sm := NewServiceMessages(nil)
	sm.SetSuppressor(sup)
	sm.Replace([]ServiceMessage{{
		ID: "svc-err", Address: "ABC123:1", Parameter: "LOWBAT",
		InterfaceID: "HmIP-RF", Timestamp: time.Now(),
	}})

	if err := sm.Disable(context.Background(), "svc-err"); !errors.Is(err, boom) {
		t.Fatalf("Disable error = %v, want %v", err, boom)
	}
	if sm.Count() != 1 {
		t.Errorf("Count=%d after failed Disable, want 1 (message must stay active)", sm.Count())
	}
	if sm.SuppressedCount() != 0 {
		t.Errorf("SuppressedCount=%d after failed Disable, want 0", sm.SuppressedCount())
	}
}

// TestServiceMessagesUnsuppressSuppressorErrorPropagates verifies that a
// failing CCU clear call is returned to the caller and the management
// record is kept (not silently dropped).
func TestServiceMessagesUnsuppressSuppressorErrorPropagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("ccu unreachable")
	sup := &stubSuppressor{live: map[string][]string{"ABC123:1": {"LOWBAT"}}}
	sm := NewServiceMessages(nil)
	sm.SetSuppressor(sup)
	sm.Replace([]ServiceMessage{{
		ID: "svc-clear", Address: "ABC123:1", Parameter: "LOWBAT",
		InterfaceID: "HmIP-RF", Timestamp: time.Now(),
	}})
	if err := sm.Disable(context.Background(), "svc-clear"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	sup.err = boom
	if err := sm.Unsuppress(context.Background(), "HmIP-RF", "ABC123:1", "LOWBAT"); !errors.Is(err, boom) {
		t.Fatalf("Unsuppress error = %v, want %v", err, boom)
	}
	if sm.SuppressedCount() != 1 {
		t.Errorf("SuppressedCount=%d after failed Unsuppress, want 1 (record must survive)", sm.SuppressedCount())
	}
}

// TestServiceMessagesUnsuppressRequiresSuppressor asserts Unsuppress fails
// when no suppressor is wired instead of silently succeeding.
func TestServiceMessagesUnsuppressRequiresSuppressor(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessages(nil)
	if err := sm.Unsuppress(context.Background(), "HmIP-RF", "ABC123:1", "LOWBAT"); err == nil {
		t.Fatal("expected error when no suppressor is configured")
	}
}

// TestServiceMessagesSuppressedToleratesReadError verifies that a failed
// per-channel getSuppressedServiceMessages read leaves that channel's
// records in place instead of dropping them from the management view — a
// transient CCU read failure must not look like "suppression cleared".
func TestServiceMessagesSuppressedToleratesReadError(t *testing.T) {
	t.Parallel()
	sup := &stubSuppressor{getErr: errors.New("rega timeout")}
	sm := NewServiceMessages(nil)
	sm.SetSuppressor(sup)
	sm.Replace([]ServiceMessage{{
		ID: "svc-tolerate", Address: "ABC123:1", Parameter: "LOWBAT",
		InterfaceID: "HmIP-RF", Timestamp: time.Now(),
	}})
	if err := sm.Disable(context.Background(), "svc-tolerate"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	got := sm.Suppressed(context.Background())
	if len(got) != 1 || got[0].Channel != "ABC123:1" || got[0].Parameter != "LOWBAT" {
		t.Fatalf("Suppressed on read error = %+v, want the record kept", got)
	}
}

// TestServiceMessagesSuppressedEmptyParameterAlwaysKept verifies that a
// suppression targeting every parameter of a channel (Parameter == "") is
// never dropped by the CCU-reconcile pass, regardless of what the CCU's
// live per-parameter list reports.
func TestServiceMessagesSuppressedEmptyParameterAlwaysKept(t *testing.T) {
	t.Parallel()
	// CCU reports no suppressed parameters at all for the channel.
	sup := &stubSuppressor{live: map[string][]string{}}
	sm := NewServiceMessages(nil)
	sm.SetSuppressor(sup)
	sm.Replace([]ServiceMessage{{
		ID: "svc-all", Address: "ABC123:1", Parameter: "",
		InterfaceID: "HmIP-RF", Timestamp: time.Now(),
	}})
	if err := sm.Disable(context.Background(), "svc-all"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	got := sm.Suppressed(context.Background())
	if len(got) != 1 || got[0].Parameter != "" {
		t.Fatalf("Suppressed with all-parameters record = %+v, want kept with empty Parameter", got)
	}
}

// TestServiceMessagesSuppressedReconcilesAgainstCCU verifies the management
// view drops records the CCU no longer reports as suppressed.
func TestServiceMessagesSuppressedReconcilesAgainstCCU(t *testing.T) {
	t.Parallel()
	// CCU reports LOWBAT still suppressed on ABC123:1 but nothing on DEF:2.
	sup := &stubSuppressor{live: map[string][]string{"ABC123:1": {"LOWBAT"}}}
	sm := NewServiceMessages(nil)
	sm.SetSuppressor(sup)
	sm.Replace([]ServiceMessage{
		{ID: "a", Address: "ABC123:1", Parameter: "LOWBAT", InterfaceID: "HmIP-RF", Timestamp: time.Now()},
		{ID: "b", Address: "DEF:2", Parameter: "ERROR", InterfaceID: "HmIP-RF", Timestamp: time.Now()},
	})
	_ = sm.Disable(context.Background(), "a")
	_ = sm.Disable(context.Background(), "b")
	got := sm.Suppressed(context.Background())
	if len(got) != 1 || got[0].Channel != "ABC123:1" || got[0].Parameter != "LOWBAT" {
		t.Fatalf("Suppressed reconcile = %+v, want only ABC123:1/LOWBAT", got)
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
