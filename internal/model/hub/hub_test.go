// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// --- stubs ---

type stubProgram struct {
	lastID      atomic.Value
	lastEnabled atomic.Value
}

func (s *stubProgram) ExecuteProgram(_ context.Context, id string) error {
	s.lastID.Store(id)
	return nil
}

func (s *stubProgram) SetProgramEnabled(_ context.Context, id string, enabled bool) error {
	s.lastEnabled.Store([2]any{id, enabled})
	return nil
}

type stubSysvar struct {
	last atomic.Value
}

func (s *stubSysvar) SetSysvar(_ context.Context, name string, value any) error {
	s.last.Store([2]any{name, value})
	return nil
}

type stubInstall struct {
	last atomic.Value
}

func (s *stubInstall) SetInstallMode(_ context.Context, iface string, enabled bool, d time.Duration) error {
	s.last.Store([3]any{iface, enabled, d})
	return nil
}

type stubAck struct {
	ids []string
}

func (s *stubAck) AcknowledgeMessage(_ context.Context, id string) error {
	s.ids = append(s.ids, id)
	return nil
}

// --- Program ---

func TestProgramExecute(t *testing.T) {
	w := &stubProgram{}
	p := &Program{ID: "4711", Writer: w}
	if err := p.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.lastID.Load() != "4711" {
		t.Fatalf("last=%v", w.lastID.Load())
	}
}

func TestProgramSetEnabledFlipsActive(t *testing.T) {
	w := &stubProgram{}
	p := &Program{ID: "4711", Writer: w}
	if err := p.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	active, observed := p.Active()
	if !active || !observed {
		t.Fatalf("active=%v observed=%v", active, observed)
	}
}

func TestProgramOnExecutionFires(t *testing.T) {
	p := &Program{}
	var ev ProgramEvent
	p.OnUpdate(func(e ProgramEvent) { ev = e })
	p.OnExecution(true, hmenum.ProgramTriggerAPI)
	if !ev.Success || ev.Trigger != hmenum.ProgramTriggerAPI {
		t.Fatalf("ev=%+v", ev)
	}
}

func TestProgramWithoutWriterErrs(t *testing.T) {
	p := &Program{}
	if err := p.Execute(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// --- Sysvar ---

func TestSysvarSetAndObserve(t *testing.T) {
	w := &stubSysvar{}
	s := &Sysvar{HubDataPoint: HubDataPoint{Name: "X"}, Writer: w, ValueType: hmenum.HubValueTypeInteger}
	if err := s.Set(context.Background(), hmtypes.IntValue(5)); err != nil {
		t.Fatal(err)
	}
	last := w.last.Load().([2]any)
	if last[0] != "X" || last[1].(int) != 5 {
		t.Fatalf("writer=%+v", last)
	}
	v, ok := s.Value()
	if !ok || v.Int != 5 {
		t.Fatalf("value=%+v ok=%v", v, ok)
	}
}

func TestSysvarOnValueFiresOnChangeOnly(t *testing.T) {
	s := &Sysvar{HubDataPoint: HubDataPoint{Name: "X"}, Writer: &stubSysvar{}}
	var n int
	s.OnUpdate(func(_, _ hmtypes.ParamValue) { n++ })
	s.OnValue(hmtypes.IntValue(1))
	s.OnValue(hmtypes.IntValue(1)) // no change
	s.OnValue(hmtypes.IntValue(2))
	if n != 2 {
		t.Fatalf("n=%d, want 2", n)
	}
}

func TestSysvarNoneValueRejected(t *testing.T) {
	s := &Sysvar{HubDataPoint: HubDataPoint{Name: "X"}, Writer: &stubSysvar{}}
	if err := s.Set(context.Background(), hmtypes.NoneValue()); err == nil {
		t.Fatal("expected error")
	}
}

// Sysvar extended fields (Min/Max/Vid/IsExtended/PreviousValue)

func TestSysvarMetaFields(t *testing.T) {
	minVal := hmtypes.FloatValue(0.0)
	maxVal := hmtypes.FloatValue(100.0)
	s := &Sysvar{
		HubDataPoint: HubDataPoint{Name: "Y"},
		Writer:       &stubSysvar{},
		ValueType:    hmenum.HubValueTypeFloat,
		Min:          &minVal,
		Max:          &maxVal,
		Vid:          42,
		IsExtended:   true,
	}
	if s.Min == nil || s.Min.Float != 0.0 {
		t.Fatalf("Min unexpected: %v", s.Min)
	}
	if s.Max == nil || s.Max.Float != 100.0 {
		t.Fatalf("Max unexpected: %v", s.Max)
	}
	if s.Vid != 42 {
		t.Fatalf("Vid=%d, want 42", s.Vid)
	}
	if !s.IsExtended {
		t.Fatal("IsExtended must be true")
	}
}

func TestSysvarPreviousValueAbsentBeforeSecondObservation(t *testing.T) {
	s := &Sysvar{HubDataPoint: HubDataPoint{Name: "Z"}, Writer: &stubSysvar{}}
	_, ok := s.PreviousValue()
	if ok {
		t.Fatal("PreviousValue must be absent before any OnValue call")
	}
	s.OnValue(hmtypes.IntValue(1))
	_, ok = s.PreviousValue()
	if ok {
		t.Fatal("PreviousValue must be absent after first OnValue (no prior)")
	}
}

func TestSysvarPreviousValueAfterSecondObservation(t *testing.T) {
	s := &Sysvar{HubDataPoint: HubDataPoint{Name: "Z"}, Writer: &stubSysvar{}}
	s.OnValue(hmtypes.IntValue(7))
	s.OnValue(hmtypes.IntValue(9))
	prev, ok := s.PreviousValue()
	if !ok {
		t.Fatal("PreviousValue must be present after second OnValue")
	}
	if prev.Int != 7 {
		t.Fatalf("PreviousValue.Int=%d, want 7", prev.Int)
	}
}

// --- InstallMode ---

func TestInstallModeEnableDisable(t *testing.T) {
	w := &stubInstall{}
	m := NewInstallMode("HmIP-RF", w)
	if err := m.Enable(context.Background(), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	enabled, remain, ok := m.InstallState()
	if !enabled || !ok {
		t.Fatalf("enabled=%v ok=%v", enabled, ok)
	}
	if remain <= 0 || remain > 5*time.Minute {
		t.Fatalf("remain=%v", remain)
	}
	if err := m.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	enabled, _, _ = m.InstallState()
	if enabled {
		t.Fatal("disable did not flip state")
	}
}

func TestInstallModeRejectsZeroDuration(t *testing.T) {
	m := NewInstallMode("x", &stubInstall{})
	err := m.Enable(context.Background(), 0)
	if !errors.Is(err, ErrInstallModeInvalidDuration) {
		t.Fatalf("got %v, want ErrInstallModeInvalidDuration", err)
	}
}

// --- AlarmMessages ---

func TestAlarmMessagesReplaceAndAcknowledge(t *testing.T) {
	ack := &stubAck{}
	a := NewAlarmMessages(ack)
	var fired int
	a.OnUpdate(func([]AlarmMessage) { fired++ })

	now := time.Now()
	a.Replace([]AlarmMessage{{ID: "a", Timestamp: now, Counter: 1}, {ID: "b", Timestamp: now, Counter: 1}})
	if a.Count() != 2 {
		t.Fatalf("count=%d", a.Count())
	}
	if fired != 1 {
		t.Fatal("first Replace should fire once")
	}
	// Same set again → no fire.
	a.Replace([]AlarmMessage{{ID: "a", Timestamp: now, Counter: 1}, {ID: "b", Timestamp: now, Counter: 1}})
	if fired != 1 {
		t.Fatal("duplicate Replace should not fire")
	}
	// Ack removes and fires once.
	if err := a.Acknowledge(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if a.Count() != 1 {
		t.Fatalf("after ack count=%d", a.Count())
	}
	if fired != 2 {
		t.Fatalf("after ack fired=%d", fired)
	}
	if len(ack.ids) != 1 || ack.ids[0] != "a" {
		t.Fatalf("ack ids=%v", ack.ids)
	}
}

// --- ServiceMessages ---

func TestServiceMessagesRoundTrip(t *testing.T) {
	ack := &stubAck{}
	s := NewServiceMessages(ack)
	s.Replace([]ServiceMessage{{ID: "x", Name: "Low battery", Timestamp: time.Now()}})
	if s.Count() != 1 {
		t.Fatalf("count=%d", s.Count())
	}
	_ = s.Acknowledge(context.Background(), "x")
	if s.Count() != 0 {
		t.Fatalf("after ack count=%d", s.Count())
	}
}

func TestServiceMessagesQuittableCountAndLatestTimestamp(t *testing.T) {
	s := NewServiceMessages(&stubAck{})
	if s.QuittableCount() != 0 || !s.LatestTimestamp().IsZero() {
		t.Fatal("empty service set must report zero counts/timestamp")
	}
	t1 := time.Date(2026, 4, 26, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	s.Replace([]ServiceMessage{
		{ID: "a", Timestamp: t1, Quittable: true},
		{ID: "b", Timestamp: t2, Quittable: false},
		{ID: "c", Timestamp: t1, Quittable: true},
	})
	if got := s.QuittableCount(); got != 2 {
		t.Fatalf("quittable=%d want 2", got)
	}
	if got := s.LatestTimestamp(); !got.Equal(t2) {
		t.Fatalf("latest=%v want %v", got, t2)
	}
}

func TestAlarmMessagesCounterAndLatestTimestamp(t *testing.T) {
	a := NewAlarmMessages(&stubAck{})
	if a.Counter() != 0 || !a.LatestTimestamp().IsZero() {
		t.Fatal("empty alarm set must report zero counter/timestamp")
	}
	t1 := time.Date(2026, 4, 26, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 26, 11, 30, 0, 0, time.UTC)
	a.Replace([]AlarmMessage{
		{ID: "smoke-1", Timestamp: t1, Counter: 3},
		{ID: "smoke-2", Timestamp: t2, Counter: 7},
	})
	// Counter mirrors len(messages), not the cumulative per-entry
	// trigger sum — the HA MQTT statistics-template expects the
	// active-alarm count.
	if got := a.Counter(); got != 2 {
		t.Fatalf("counter=%d want 2", got)
	}
	if got := a.LatestTimestamp(); !got.Equal(t2) {
		t.Fatalf("latest=%v want %v", got, t2)
	}
}

func TestAlarmMessagesAdditionalInformationIndexed(t *testing.T) {
	a := NewAlarmMessages(&stubAck{})
	if got := a.AdditionalInformationIndexed(); len(got) != 0 {
		t.Fatalf("empty alarm set must produce empty indexed map, got %v", got)
	}
	a.Replace([]AlarmMessage{
		{ID: "smoke-1", Name: "Smoke", DeviceName: "Hallway"},
		{ID: "low-bat", Name: "Low Battery", DeviceName: "Garage"},
	})
	got := a.AdditionalInformationIndexed()
	if len(got) != 2 {
		t.Fatalf("indexed map size = %d, want 2", len(got))
	}
	want := map[string]bool{
		"Hallway: Smoke":      true,
		"Garage: Low Battery": true,
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("unexpected indexed value %q", v)
		}
	}
}

// --- Connectivity ---

func TestConnectivityFlipsFireCallback(t *testing.T) {
	c := NewConnectivity()
	var fired []InterfaceReachability
	c.OnUpdate(func(r InterfaceReachability) { fired = append(fired, r) })
	c.OnState("HmIP-RF", true)
	c.OnState("HmIP-RF", true)  // no change
	c.OnState("HmIP-RF", false) // change
	if len(fired) != 2 {
		t.Fatalf("fired=%d", len(fired))
	}
	c.OnState("BidCos-RF", false)
	all, _ := c.AllReachable()
	if all {
		t.Fatal("one interface down should break AllReachable")
	}
}

// --- Update ---

func TestUpdateDedupesEqualSnapshots(t *testing.T) {
	u := NewUpdate()
	var n int
	u.OnUpdate(func(UpdateInfo) { n++ })
	u.OnInfo(UpdateInfo{CurrentFirmware: "1.0", CheckScriptAvailable: true})
	u.OnInfo(UpdateInfo{CurrentFirmware: "1.0", CheckScriptAvailable: true})
	u.OnInfo(UpdateInfo{CurrentFirmware: "1.1", CheckScriptAvailable: true, UpdateAvailable: true})
	if n != 2 {
		t.Fatalf("n=%d", n)
	}
}

func TestProgramOnRemovedFiresFromHubRemove(t *testing.T) {
	h := NewHub("test")
	prog := &Program{HubDataPoint: HubDataPoint{Name: "x"}, ID: "P1"}
	h.PutProgram(prog)
	var fired int
	prog.OnRemoved(func() { fired++ })
	if !h.RemoveProgram("P1") {
		t.Fatal("RemoveProgram must report true on existing entry")
	}
	if fired != 1 {
		t.Fatalf("OnRemoved fired %d times, want 1", fired)
	}
	if h.RemoveProgram("P1") {
		t.Fatal("second RemoveProgram must report false")
	}
	if fired != 1 {
		t.Fatalf("OnRemoved fired %d times after second Remove, want 1", fired)
	}
}

func TestInstallModeIsActiveAndRemaining(t *testing.T) {
	m := NewInstallMode("HmIP-RF", nil)
	if m.IsActive() {
		t.Fatal("fresh install mode must not report active")
	}
	if got := m.Remaining(); got != 0 {
		t.Fatalf("fresh Remaining=%v want 0", got)
	}
	m.OnState(true, 5*time.Second)
	if !m.IsActive() {
		t.Fatal("OnState(true, 5s) → IsActive true")
	}
	if got := m.Remaining(); got <= 0 || got > 5*time.Second {
		t.Fatalf("Remaining after enable=%v want >0 and ≤5s", got)
	}
	m.OnState(false, 0)
	if m.IsActive() {
		t.Fatal("after Disable IsActive must be false")
	}
}
