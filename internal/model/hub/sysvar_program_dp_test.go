// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── SysvarDpNumber ────────────────────────────────────────────

// ─── SysvarDpSelect ──────────────────────────────────────

// TestSysvarSetStringLabelResolvesToIndex covers the base [Sysvar.Set]
// path for HubValueTypeList sysvars: HA's `select` round-trips the label
// string ("Normal") back on the command topic, and Sysvar.Set must
// resolve it to the integer CCU index (2) before the Rega write — even
// when the call bypasses the SysvarDpSelect wrapper (as MQTTCommandSink
// does today).
func TestSysvarSetStringLabelResolvesToIndex(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "mode", "", hmenum.HubValueTypeList, w)
	sv.ValueList = []string{"Aus", "Niedrig", "Normal", "Hoch"}

	if err := sv.Set(context.Background(), hmtypes.StringValue("Normal")); err != nil {
		t.Fatalf("Set(\"Normal\") unexpected error: %v", err)
	}
	pair, ok := w.last.Load().([2]any)
	if !ok {
		t.Fatal("writer was not called")
	}
	if pair[1].(int) != 2 {
		t.Errorf("writer received value=%v, want integer index 2", pair[1])
	}
}

// TestSysvarSetIntegerIndexStillAccepted verifies the integer path
// stays unchanged for the List ValueType.
func TestSysvarSetIntegerIndexStillAccepted(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "mode", "", hmenum.HubValueTypeList, w)
	sv.ValueList = []string{"a", "b", "c"}

	if err := sv.Set(context.Background(), hmtypes.IntValue(1)); err != nil {
		t.Fatalf("Set(1) unexpected error: %v", err)
	}
	pair := w.last.Load().([2]any)
	if pair[1].(int) != 1 {
		t.Errorf("writer received value=%v, want 1", pair[1])
	}
}

// TestSysvarSetUnknownLabelReturnsError makes sure unknown labels
// fail-fast rather than silently writing a garbage value to the CCU.
func TestSysvarSetUnknownLabelReturnsError(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "mode", "", hmenum.HubValueTypeList, w)
	sv.ValueList = []string{"a", "b"}

	if err := sv.Set(context.Background(), hmtypes.StringValue("zzz")); err == nil {
		t.Fatal("unknown label must return error")
	}
	if _, written := w.last.Load().([2]any); written {
		t.Fatal("writer must NOT be called for unknown label")
	}
}

// ─── ProgramDpSwitch ───────────────────────────────────────────

// TestProgramDpSwitchValue reports the current active state.
func TestProgramDpSwitchValue(t *testing.T) {
	pg := NewProgram("c1", "p1", "Test", "", false, nil)
	sw := &ProgramDpSwitch{Program: pg}

	// initially false (not observed)
	if sw.Value() {
		t.Fatal("Value() must be false before any OnActive call")
	}
	pg.OnActive(true)
	if !sw.Value() {
		t.Fatal("Value() must be true after OnActive(true)")
	}
	pg.OnActive(false)
	if sw.Value() {
		t.Fatal("Value() must be false after OnActive(false)")
	}
}

// TestProgramDpSwitchTurnOn delegates to SetEnabled(true).
func TestProgramDpSwitchTurnOn(t *testing.T) {
	w := &stubProgram{}
	pg := NewProgram("c1", "p1", "Lights", "", false, w)
	sw := &ProgramDpSwitch{Program: pg}

	if err := sw.TurnOn(context.Background()); err != nil {
		t.Fatalf("TurnOn() unexpected error: %v", err)
	}
	got := w.lastEnabled.Load()
	pair, ok := got.([2]any)
	if !ok {
		t.Fatal("SetProgramEnabled was not called")
	}
	if pair[0] != "p1" || pair[1] != true {
		t.Errorf("SetProgramEnabled(%v, %v), want (p1, true)", pair[0], pair[1])
	}
	active, _ := pg.Active()
	if !active {
		t.Fatal("TurnOn() must update active state")
	}
}

// TestProgramDpSwitchTurnOff delegates to SetEnabled(false).
func TestProgramDpSwitchTurnOff(t *testing.T) {
	w := &stubProgram{}
	pg := NewProgram("c1", "p1", "Lights", "", false, w)
	sw := &ProgramDpSwitch{Program: pg}

	if err := sw.TurnOff(context.Background()); err != nil {
		t.Fatalf("TurnOff() unexpected error: %v", err)
	}
	pair := w.lastEnabled.Load().([2]any)
	if pair[1] != false {
		t.Errorf("TurnOff: expected enabled=false, got %v", pair[1])
	}
}

// TestProgramDpSwitchTurnOnNoWriterErr returns an error when no writer is set.
func TestProgramDpSwitchTurnOnNoWriterErr(t *testing.T) {
	pg := NewProgram("c1", "p1", "Test", "", false, nil)
	sw := &ProgramDpSwitch{Program: pg}
	if err := sw.TurnOn(context.Background()); err == nil {
		t.Fatal("TurnOn without writer must return error")
	}
}

// ─── unconfirmed value ───────────────────────────

// TestSysvarUnconfirmedValueShadowsConfirmed verifies that after an
// optimistic write, Value() returns the unconfirmed value.
func TestSysvarUnconfirmedValueShadowsConfirmed(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "x", "", hmenum.HubValueTypeInteger, w)
	sv.OnValue(hmtypes.IntValue(1)) // confirmed value

	sv.WriteUnconfirmedValue(hmtypes.IntValue(99))

	v, ok := sv.Value()
	if !ok || v.Int != 99 {
		t.Fatalf("Value() after WriteUnconfirmed = %+v ok=%v, want Int(99)", v, ok)
	}
}

// TestSysvarOnValueClearsUnconfirmed verifies that a confirmed OnValue
// clears any pending unconfirmed write.
func TestSysvarOnValueClearsUnconfirmed(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "x", "", hmenum.HubValueTypeInteger, w)
	sv.OnValue(hmtypes.IntValue(1))
	sv.WriteUnconfirmedValue(hmtypes.IntValue(99))

	// Simulate CCU confirming a different value
	sv.OnValue(hmtypes.IntValue(2))

	v, _ := sv.Value()
	if v.Int != 2 {
		t.Fatalf("Value() after confirmed OnValue = %d, want 2", v.Int)
	}
}

// TestSysvarResetUnconfirmedValue verifies that ResetUnconfirmedValue
// causes Value() to return the confirmed value again.
func TestSysvarResetUnconfirmedValue(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "x", "", hmenum.HubValueTypeInteger, w)
	sv.OnValue(hmtypes.IntValue(5))
	sv.WriteUnconfirmedValue(hmtypes.IntValue(99))
	sv.ResetUnconfirmedValue()

	v, _ := sv.Value()
	if v.Int != 5 {
		t.Fatalf("Value() after reset = %d, want 5", v.Int)
	}
}

// TestSysvarSetWritesUnconfirmedOnSuccess verifies that Set stores an
// optimistic value after a successful CCU write.
func TestSysvarSetWritesUnconfirmedOnSuccess(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "x", "", hmenum.HubValueTypeInteger, w)
	sv.OnValue(hmtypes.IntValue(0))

	if err := sv.Set(context.Background(), hmtypes.IntValue(42)); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	v, _ := sv.Value()
	if v.Int != 42 {
		t.Fatalf("Value() after Set = %d, want 42", v.Int)
	}
}

// TestSysvarSetWriterErrorNoOptimisticApply verifies that when the
// writer call fails, no optimistic value is applied and the confirmed
// value remains unchanged.
func TestSysvarSetWriterErrorNoOptimisticApply(t *testing.T) {
	w := &errSysvarWriter{err: errors.New("backend down")}
	sv := NewSysvar("c1", "x", "", hmenum.HubValueTypeInteger, w)
	sv.OnValue(hmtypes.IntValue(5))

	_ = sv.Set(context.Background(), hmtypes.IntValue(42))

	v, _ := sv.Value()
	// Backend error: no optimistic value applied → confirmed value returned.
	if v.Int != 5 {
		t.Fatalf("Value() after failed Set = %d, want 5 (confirmed)", v.Int)
	}
	// Ensure the unconfirmed slot was never populated.
	sv.mu.RLock()
	hasUnconfirmed := sv.unconfirmedValue != nil
	sv.mu.RUnlock()
	if hasUnconfirmed {
		t.Fatal("unconfirmedValue must be nil after a failed Set")
	}
}

// TestSysvarConfirmedValueIgnoresUnconfirmed verifies ConfirmedValue()
// always returns the last CCU-confirmed value.
func TestSysvarConfirmedValueIgnoresUnconfirmed(t *testing.T) {
	w := &stubSysvar{}
	sv := NewSysvar("c1", "x", "", hmenum.HubValueTypeInteger, w)
	sv.OnValue(hmtypes.IntValue(7))
	sv.WriteUnconfirmedValue(hmtypes.IntValue(99))

	v, ok := sv.ConfirmedValue()
	if !ok || v.Int != 7 {
		t.Fatalf("ConfirmedValue() = %+v ok=%v, want Int(7)", v, ok)
	}
}

// ─── data_type / convert_value ──────────────────────────────────

// TestSysvarValueTypeConversionBool verifies that a logic sysvar stores
// boolean values correctly.
func TestSysvarValueTypeConversionBool(t *testing.T) {
	sv := NewSysvar("c1", "flag", "", hmenum.HubValueTypeLogic, nil)
	sv.OnValue(hmtypes.BoolValue(true))
	v, ok := sv.Value()
	if !ok || v.Kind != hmtypes.ValueKindBool || !v.Bool {
		t.Fatalf("bool sysvar value=%+v ok=%v, want Bool(true)", v, ok)
	}
}

// TestSysvarValueTypeConversionFloat verifies float sysvar round-trip.
func TestSysvarValueTypeConversionFloat(t *testing.T) {
	sv := NewSysvar("c1", "temp", "", hmenum.HubValueTypeFloat, nil)
	sv.OnValue(hmtypes.FloatValue(3.14))
	v, ok := sv.Value()
	if !ok || v.Float != 3.14 {
		t.Fatalf("float sysvar value=%+v ok=%v", v, ok)
	}
}

// TestSysvarValueTypeConversionString verifies string sysvar round-trip.
func TestSysvarValueTypeConversionString(t *testing.T) {
	sv := NewSysvar("c1", "msg", "", hmenum.HubValueTypeString, nil)
	sv.OnValue(hmtypes.StringValue("hello"))
	v, ok := sv.Value()
	if !ok || v.String != "hello" {
		t.Fatalf("string sysvar value=%+v ok=%v", v, ok)
	}
}

// ─── LegacyName ───────────────────────────────────────────────────────

// TestSysvarLegacyNameMatchesName verifies that LegacyName() returns
// the same value as Name.
func TestSysvarLegacyNameMatchesName(t *testing.T) {
	sv := NewSysvar("c1", "MyVar", "", hmenum.HubValueTypeString, nil)
	if got := sv.LegacyName(); got != sv.Name {
		t.Fatalf("LegacyName()=%q, want %q", got, sv.Name)
	}
}

// ─── Update.InProgress + Install ───────────────────────────────

// TestUpdateInProgressDefaultFalse verifies that InProgress starts false.
func TestUpdateInProgressDefaultFalse(t *testing.T) {
	u := NewUpdate()
	if u.InProgress() {
		t.Fatal("fresh Update must have InProgress=false")
	}
}

// TestUpdateInstallSetsInProgress verifies that Install sets InProgress=true.
func TestUpdateInstallSetsInProgress(t *testing.T) {
	u := NewUpdate()
	u.FirmwareUpdater = &stubFirmwareUpdater{}
	if err := u.Install(context.Background()); err != nil {
		t.Fatalf("Install() unexpected error: %v", err)
	}
	if !u.InProgress() {
		t.Fatal("Install() must set InProgress=true")
	}
}

// TestUpdateInstallNoUpdaterErr returns ErrNoFirmwareUpdater when no
// updater is wired.
func TestUpdateInstallNoUpdaterErr(t *testing.T) {
	u := NewUpdate()
	if err := u.Install(context.Background()); !errors.Is(err, ErrNoFirmwareUpdater) {
		t.Fatalf("expected ErrNoFirmwareUpdater, got %v", err)
	}
}

// TestUpdateSetInProgressFiresCallbacks verifies that SetInProgress
// fires registered OnUpdate callbacks.
func TestUpdateSetInProgressFiresCallbacks(t *testing.T) {
	u := NewUpdate()
	var n int
	u.OnUpdate(func(UpdateInfo) { n++ })
	u.SetInProgress(true)
	u.SetInProgress(false)
	if n != 2 {
		t.Fatalf("callbacks fired %d times, want 2", n)
	}
}

// TestUpdateInstallUpdaterError returns error and does NOT set InProgress
// when the firmware updater fails.
func TestUpdateInstallUpdaterError(t *testing.T) {
	u := NewUpdate()
	sentinel := errors.New("rega fail")
	u.FirmwareUpdater = &stubFirmwareUpdater{err: sentinel}
	if err := u.Install(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if u.InProgress() {
		t.Fatal("InProgress must be false after failed Install")
	}
}

// ─── Hub.InstallModeDPs + Hub.Update ───────────────────────────

// TestHubInstallModeDPsPutAndGet verifies that InstallMode data points
// can be registered and retrieved by interface ID.
func TestHubInstallModeDPsPutAndGet(t *testing.T) {
	h := NewHub("ccu-01")
	m1 := NewInstallMode("HmIP-RF", nil)
	m2 := NewInstallMode("BidCos-RF", nil)
	h.PutInstallMode(m1)
	h.PutInstallMode(m2)
	h.PutInstallMode(nil)            // ignored
	h.PutInstallMode(&InstallMode{}) // empty ID — ignored

	got, ok := h.InstallModeDP("HmIP-RF")
	if !ok || got != m1 {
		t.Fatal("InstallModeDP(HmIP-RF) did not return m1")
	}

	all := h.InstallModeDPs()
	if len(all) != 2 {
		t.Fatalf("InstallModeDPs len=%d, want 2", len(all))
	}
}

// TestHubUpdateFieldInitialised verifies that NewHub wires a non-nil
// Update aggregate.
func TestHubUpdateFieldInitialised(t *testing.T) {
	h := NewHub("ccu-01")
	if h.Update == nil {
		t.Fatal("NewHub must initialise the Update field")
	}
	if h.Update.InProgress() {
		t.Fatal("fresh hub Update must not be in-progress")
	}
}

// ─── Hub.FetchAlarmMessagesData + Hub.FetchInboxData ─────────

// stubDataFetcher is a test double for DataFetcher.
type stubDataFetcher struct {
	alarms   []AlarmMessage
	inbox    []InboxDevice
	alarmErr error
	inboxErr error
}

func (s *stubDataFetcher) FetchAlarmMessages(_ context.Context) ([]AlarmMessage, error) {
	return s.alarms, s.alarmErr
}

func (s *stubDataFetcher) FetchInboxDevices(_ context.Context) ([]InboxDevice, error) {
	return s.inbox, s.inboxErr
}

// TestHubFetchAlarmMessagesDataUpdatesMessages verifies that
// FetchAlarmMessagesData calls the fetcher and updates Messages.
func TestHubFetchAlarmMessagesDataUpdatesMessages(t *testing.T) {
	h := NewHub("ccu-01")
	ts := time.Now()
	fetcher := &stubDataFetcher{
		alarms: []AlarmMessage{{ID: "a1", Timestamp: ts, Counter: 1}},
	}
	if err := h.FetchAlarmMessagesData(context.Background(), fetcher); err != nil {
		t.Fatalf("FetchAlarmMessagesData() error: %v", err)
	}
	if h.Messages.Count() != 1 {
		t.Fatalf("Messages.Count()=%d, want 1", h.Messages.Count())
	}
}

// TestHubFetchAlarmMessagesDataNilFetcher returns an error for nil fetcher.
func TestHubFetchAlarmMessagesDataNilFetcher(t *testing.T) {
	h := NewHub("ccu-01")
	if err := h.FetchAlarmMessagesData(context.Background(), nil); err == nil {
		t.Fatal("nil fetcher must return error")
	}
}

// TestHubFetchAlarmMessagesDataFetcherError propagates fetcher error.
func TestHubFetchAlarmMessagesDataFetcherError(t *testing.T) {
	h := NewHub("ccu-01")
	sentinel := errors.New("backend down")
	fetcher := &stubDataFetcher{alarmErr: sentinel}
	if err := h.FetchAlarmMessagesData(context.Background(), fetcher); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// TestHubFetchInboxDataUpdatesInbox verifies that FetchInboxData calls
// the fetcher and updates Inbox.
func TestHubFetchInboxDataUpdatesInbox(t *testing.T) {
	h := NewHub("ccu-01")
	fetcher := &stubDataFetcher{
		inbox: []InboxDevice{{Address: "0001", Model: "HmIP-BROLL"}},
	}
	if err := h.FetchInboxData(context.Background(), fetcher); err != nil {
		t.Fatalf("FetchInboxData() error: %v", err)
	}
	if h.Inbox.Count() != 1 {
		t.Fatalf("Inbox.Count()=%d, want 1", h.Inbox.Count())
	}
}

// TestHubFetchInboxDataNilFetcher returns an error for nil fetcher.
func TestHubFetchInboxDataNilFetcher(t *testing.T) {
	h := NewHub("ccu-01")
	if err := h.FetchInboxData(context.Background(), nil); err == nil {
		t.Fatal("nil fetcher must return error")
	}
}

// TestHubFetchInboxDataFetcherError propagates fetcher error.
func TestHubFetchInboxDataFetcherError(t *testing.T) {
	h := NewHub("ccu-01")
	sentinel := errors.New("rega fail")
	fetcher := &stubDataFetcher{inboxErr: sentinel}
	if err := h.FetchInboxData(context.Background(), fetcher); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// ─── Sysvar data points for the newer value types ────────────────────────────
