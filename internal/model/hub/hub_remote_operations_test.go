// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── stub helpers ──────────────────────────────────────────────────────────

type stubSysvarMutator struct {
	createErr error
	updateErr error
	deleteErr error
	mu        sync.Mutex
	created   []string
	updated   []string
	renamedTo []string
	deleted   []string
}

func (s *stubSysvarMutator) CreateSysvar(_ context.Context, name, _, _, _, _, _ string, _ []string) error {
	s.mu.Lock()
	s.created = append(s.created, name)
	s.mu.Unlock()
	return s.createErr
}

func (s *stubSysvarMutator) UpdateSysvar(_ context.Context, name, newName, _, _, _, _ string, _ []string) error {
	s.mu.Lock()
	s.updated = append(s.updated, name)
	s.renamedTo = append(s.renamedTo, newName)
	s.mu.Unlock()
	return s.updateErr
}

func (s *stubSysvarMutator) DeleteSysvar(_ context.Context, name string) error {
	s.mu.Lock()
	s.deleted = append(s.deleted, name)
	s.mu.Unlock()
	return s.deleteErr
}

type stubRoomMutator struct {
	err error
}

func (s *stubRoomMutator) SetDeviceRooms(_ context.Context, _ string, _ []string) error {
	return s.err
}

type stubFunctionMutator struct {
	err error
}

func (s *stubFunctionMutator) SetDeviceFunctions(_ context.Context, _ string, _ []string) error {
	return s.err
}

type stubBackupTrigger struct {
	triggerErr  error
	statusErr   error
	statusValue string
}

func (s *stubBackupTrigger) TriggerBackup(_ context.Context) error { return s.triggerErr }
func (s *stubBackupTrigger) BackupStatus(_ context.Context) (string, error) {
	return s.statusValue, s.statusErr
}

type stubFirmwareUpdater struct{ err error }

func (s *stubFirmwareUpdater) TriggerFirmwareUpdate(_ context.Context) error { return s.err }

type stubInboxAccepter struct{ err error }

func (s *stubInboxAccepter) AcceptDeviceInInbox(_ context.Context, _ string) error { return s.err }

type errAck struct{ err error }

func (e *errAck) AcknowledgeMessage(_ context.Context, _ string) error { return e.err }

type errInstallWriter struct{ err error }

func (e *errInstallWriter) SetInstallMode(_ context.Context, _ string, _ bool, _ time.Duration) error {
	return e.err
}

type errProgramWriter struct{ err error }

func (e *errProgramWriter) ExecuteProgram(_ context.Context, _ string) error { return e.err }
func (e *errProgramWriter) SetProgramEnabled(_ context.Context, _ string, _ bool) error {
	return e.err
}

type errSysvarWriter struct{ err error }

func (e *errSysvarWriter) SetSysvar(_ context.Context, _ string, _ any) error { return e.err }

// ─── Hub remote methods ─────────────────────────────────────────────────────

func TestHubRemoveSysvar(t *testing.T) {
	h := NewHub("ccu")
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "Foo"}})
	if !h.RemoveSysvar("Foo") {
		t.Fatal("expected true on first remove")
	}
	if h.RemoveSysvar("Foo") {
		t.Fatal("expected false on second remove")
	}
}

func TestHubCreateSysvarRemote_noMutator(t *testing.T) {
	h := NewHub("ccu")
	err := h.CreateSysvarRemote(context.Background(), "X", "integer", "", "0", "100", "", nil)
	if !errors.Is(err, ErrNoSysvarMutator) {
		t.Fatalf("want ErrNoSysvarMutator, got %v", err)
	}
}

func TestHubCreateSysvarRemote_withMutator(t *testing.T) {
	h := NewHub("ccu")
	mut := &stubSysvarMutator{}
	h.SysvarMutator = mut
	if err := h.CreateSysvarRemote(context.Background(), "MyVar", "integer", "°C", "0", "100", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(mut.created) != 1 || mut.created[0] != "MyVar" {
		t.Fatalf("created=%v", mut.created)
	}
}

func TestHubDeleteSysvarRemote_noMutator(t *testing.T) {
	h := NewHub("ccu")
	if !errors.Is(h.DeleteSysvarRemote(context.Background(), "X"), ErrNoSysvarMutator) {
		t.Fatal("want ErrNoSysvarMutator")
	}
}

func TestHubDeleteSysvarRemote_ok(t *testing.T) {
	h := NewHub("ccu")
	mut := &stubSysvarMutator{}
	h.SysvarMutator = mut
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "DelMe"}})
	if err := h.DeleteSysvarRemote(context.Background(), "DelMe"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.Sysvar("DelMe"); ok {
		t.Fatal("sysvar should have been removed from cache")
	}
	if len(mut.deleted) != 1 || mut.deleted[0] != "DelMe" {
		t.Fatalf("deleted=%v", mut.deleted)
	}
}

func TestHubDeleteSysvarRemote_propagatesError(t *testing.T) {
	h := NewHub("ccu")
	sentinel := errors.New("rega error")
	h.SysvarMutator = &stubSysvarMutator{deleteErr: sentinel}
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "X"}})
	if err := h.DeleteSysvarRemote(context.Background(), "X"); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	// Cache should NOT be cleared on error.
	if _, ok := h.Sysvar("X"); !ok {
		t.Fatal("sysvar must stay in cache after failed delete")
	}
}

func TestHubUpdateSysvarRemote(t *testing.T) {
	h := NewHub("ccu")
	mut := &stubSysvarMutator{}
	h.SysvarMutator = mut
	if err := h.UpdateSysvarRemote(context.Background(), "X", "", "°C", "0", "50", "desc", nil); err != nil {
		t.Fatal(err)
	}
	if len(mut.updated) != 1 || mut.updated[0] != "X" {
		t.Fatalf("updated=%v", mut.updated)
	}
}

func TestHubUpdateSysvarRemote_noMutator(t *testing.T) {
	h := NewHub("ccu")
	if !errors.Is(h.UpdateSysvarRemote(context.Background(), "X", "", "", "", "", "", nil), ErrNoSysvarMutator) {
		t.Fatal("want ErrNoSysvarMutator")
	}
}

// A non-empty newName renames the cached entry once the CCU-side patch
// succeeds, so the new name is visible before the next periodic refresh.
func TestHubUpdateSysvarRemote_renamesCacheKey(t *testing.T) {
	h := NewHub("ccu")
	mut := &stubSysvarMutator{}
	h.SysvarMutator = mut
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "Old"}})
	if err := h.UpdateSysvarRemote(context.Background(), "Old", "New", "", "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(mut.renamedTo) != 1 || mut.renamedTo[0] != "New" {
		t.Fatalf("renamedTo=%v", mut.renamedTo)
	}
	if _, ok := h.Sysvar("Old"); ok {
		t.Fatal("old key must be dropped from the cache after rename")
	}
	sv, ok := h.Sysvar("New")
	if !ok {
		t.Fatal("new key must be present in the cache after rename")
	}
	if sv.Name != "New" {
		t.Fatalf("entry Name = %q, want New", sv.Name)
	}
}

// A failed CCU-side patch must not re-key the local cache.
func TestHubUpdateSysvarRemote_renameSkippedOnError(t *testing.T) {
	h := NewHub("ccu")
	sentinel := errors.New("rega error")
	h.SysvarMutator = &stubSysvarMutator{updateErr: sentinel}
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "Old"}})
	if err := h.UpdateSysvarRemote(context.Background(), "Old", "New", "", "", "", "", nil); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if _, ok := h.Sysvar("Old"); !ok {
		t.Fatal("old key must survive a failed rename")
	}
	if _, ok := h.Sysvar("New"); ok {
		t.Fatal("new key must not appear after a failed rename")
	}
}

// TestHubRenameSysvar exercises [Hub.RenameSysvar] directly (rather than
// through UpdateSysvarRemote) so every no-op branch — same name, empty
// target, unknown source, and a name collision — is covered in isolation.
func TestHubRenameSysvar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		seed        []string // sysvar names to pre-populate
		oldName     string
		newName     string
		wantOK      bool
		wantOldGone bool
		wantNewName string // name to expect present after the call; empty = don't check
	}{
		{
			name:    "same name is a no-op",
			seed:    []string{"Same"},
			oldName: "Same",
			newName: "Same",
			wantOK:  false,
		},
		{
			name:    "empty target name is a no-op",
			seed:    []string{"Old"},
			oldName: "Old",
			newName: "",
			wantOK:  false,
		},
		{
			name:    "unknown source name is a no-op",
			seed:    []string{"Other"},
			oldName: "Ghost",
			newName: "Fresh",
			wantOK:  false,
		},
		{
			name:    "target name already taken is a no-op",
			seed:    []string{"Old", "Taken"},
			oldName: "Old",
			newName: "Taken",
			wantOK:  false,
		},
		{
			name:        "successful rename re-keys the cache",
			seed:        []string{"Old"},
			oldName:     "Old",
			newName:     "New",
			wantOK:      true,
			wantOldGone: true,
			wantNewName: "New",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := NewHub("ccu")
			var original *Sysvar
			for _, n := range tc.seed {
				sv := &Sysvar{HubDataPoint: HubDataPoint{Name: n}}
				h.PutSysvar(sv)
				if n == tc.oldName {
					original = sv
				}
			}

			got := h.RenameSysvar(tc.oldName, tc.newName)
			if got != tc.wantOK {
				t.Fatalf("RenameSysvar(%q, %q) = %v, want %v", tc.oldName, tc.newName, got, tc.wantOK)
			}

			if !tc.wantOK {
				// A rejected rename must leave every seeded entry untouched.
				for _, n := range tc.seed {
					if _, ok := h.Sysvar(n); !ok {
						t.Fatalf("seeded sysvar %q must survive a rejected rename", n)
					}
				}
				return
			}

			if tc.wantOldGone {
				if _, ok := h.Sysvar(tc.oldName); ok {
					t.Fatalf("old key %q must be dropped after a successful rename", tc.oldName)
				}
			}
			sv, ok := h.Sysvar(tc.wantNewName)
			if !ok {
				t.Fatalf("new key %q must be present after a successful rename", tc.wantNewName)
			}
			if sv.Name != tc.wantNewName {
				t.Fatalf("renamed entry Name = %q, want %q", sv.Name, tc.wantNewName)
			}
			if sv != original {
				t.Fatal("rename must preserve the original *Sysvar pointer so subscribers stay valid")
			}
		})
	}
}

func TestHubSetDeviceRoomsRemote(t *testing.T) {
	h := NewHub("ccu")
	if !errors.Is(h.SetDeviceRoomsRemote(context.Background(), "addr", nil), ErrNoRoomMutator) {
		t.Fatal("want ErrNoRoomMutator")
	}
	h.RoomMutator = &stubRoomMutator{}
	if err := h.SetDeviceRoomsRemote(context.Background(), "addr", []string{"living"}); err != nil {
		t.Fatal(err)
	}
}

func TestHubSetDeviceFunctionsRemote(t *testing.T) {
	h := NewHub("ccu")
	if !errors.Is(h.SetDeviceFunctionsRemote(context.Background(), "addr", nil), ErrNoFunctionMutator) {
		t.Fatal("want ErrNoFunctionMutator")
	}
	h.FunctionMutator = &stubFunctionMutator{}
	if err := h.SetDeviceFunctionsRemote(context.Background(), "addr", []string{"heating"}); err != nil {
		t.Fatal(err)
	}
}

func TestHubTriggerBackupRemote(t *testing.T) {
	h := NewHub("ccu")
	if !errors.Is(h.TriggerBackupRemote(context.Background()), ErrNoBackupTrigger) {
		t.Fatal("want ErrNoBackupTrigger")
	}
	h.BackupTrigger = &stubBackupTrigger{statusValue: "running"}
	if err := h.TriggerBackupRemote(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHubBackupStatusRemote(t *testing.T) {
	h := NewHub("ccu")
	_, err := h.BackupStatusRemote(context.Background())
	if !errors.Is(err, ErrNoBackupTrigger) {
		t.Fatal("want ErrNoBackupTrigger")
	}
	h.BackupTrigger = &stubBackupTrigger{statusValue: "done"}
	s, err := h.BackupStatusRemote(context.Background())
	if err != nil || s != "done" {
		t.Fatalf("status=%q err=%v", s, err)
	}
}

func TestHubTriggerFirmwareUpdateRemote(t *testing.T) {
	h := NewHub("ccu")
	if !errors.Is(h.TriggerFirmwareUpdateRemote(context.Background()), ErrNoFirmwareUpdater) {
		t.Fatal("want ErrNoFirmwareUpdater")
	}
	h.FirmwareUpdater = &stubFirmwareUpdater{}
	if err := h.TriggerFirmwareUpdateRemote(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHubAcceptInboxDeviceRemote(t *testing.T) {
	h := NewHub("ccu")
	if !errors.Is(h.AcceptInboxDeviceRemote(context.Background(), "0001"), ErrNoInboxAccepter) {
		t.Fatal("want ErrNoInboxAccepter")
	}
	h.InboxAccepter = &stubInboxAccepter{}
	if err := h.AcceptInboxDeviceRemote(context.Background(), "0001"); err != nil {
		t.Fatal(err)
	}
}

// ─── Program ────────────────────────────────────────────────────────────────

func TestProgramLastExecution(t *testing.T) {
	p := &Program{ID: "P1"}
	_, hasExec := p.LastExecution()
	if hasExec {
		t.Fatal("fresh program must have no last execution")
	}
	p.OnExecution(true, hmenum.ProgramTriggerUser)
	ts, hasExec := p.LastExecution()
	if !hasExec || ts.IsZero() {
		t.Fatalf("after OnExecution: ts=%v hasExec=%v", ts, hasExec)
	}
}

func TestProgramLastResult(t *testing.T) {
	p := &Program{ID: "P1"}
	_, obs := p.LastResult()
	if obs {
		t.Fatal("fresh program must have no last result")
	}
	p.OnExecution(false, hmenum.ProgramTriggerScheduler)
	success, obs := p.LastResult()
	if !obs || success {
		t.Fatalf("success=%v obs=%v", success, obs)
	}
}

func TestProgramSetEnabled_writerError(t *testing.T) {
	sentinel := errors.New("rega")
	p := &Program{ID: "P1", Writer: &errProgramWriter{err: sentinel}}
	if !errors.Is(p.SetEnabled(context.Background(), true), sentinel) {
		t.Fatal("expected writer error")
	}
	// Active must NOT have changed.
	_, obs := p.Active()
	if obs {
		t.Fatal("active must be unobserved after failed SetEnabled")
	}
}

func TestProgramOnUpdateUnsubscribe(t *testing.T) {
	p := &Program{ID: "P1"}
	var count atomic.Int32
	unsub := p.OnUpdate(func(ProgramEvent) { count.Add(1) })
	p.OnExecution(true, hmenum.ProgramTriggerAPI)
	unsub()
	p.OnExecution(true, hmenum.ProgramTriggerAPI)
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestProgramOnRemovedUnsubscribe(t *testing.T) {
	p := &Program{ID: "P1"}
	var count atomic.Int32
	unsub := p.OnRemoved(func() { count.Add(1) })
	unsub()
	p.NotifyRemoved()
	if count.Load() != 0 {
		t.Fatal("unsubscribed handler must not fire")
	}
}

func TestProgramNotifyRemovedClearsHandlers(t *testing.T) {
	p := &Program{ID: "P1"}
	var count atomic.Int32
	p.OnRemoved(func() { count.Add(1) })
	p.NotifyRemoved()
	p.NotifyRemoved() // second call — handlers already cleared
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestProgramConcurrentExecution(t *testing.T) {
	p := &Program{ID: "race"}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			p.OnExecution(true, hmenum.ProgramTriggerAPI)
			_, _ = p.LastExecution()
			_, _ = p.LastResult()
		})
	}
	wg.Wait()
}

// ─── Sysvar ─────────────────────────────────────────────────────────────────

func TestSysvarToWireAllKinds(t *testing.T) {
	s := &Sysvar{HubDataPoint: HubDataPoint{Name: "sv"}, Writer: &stubSysvar{}}

	cases := []struct {
		v    hmtypes.ParamValue
		want any
	}{
		{hmtypes.BoolValue(true), true},
		{hmtypes.IntValue(42), 42},
		{hmtypes.FloatValue(3.14), 3.14},
		{hmtypes.StringValue("hello"), "hello"},
		{hmtypes.ListValue([]string{"a"}), []string{"a"}},
	}
	for _, tc := range cases {
		wire, err := s.toWire(tc.v)
		if err != nil {
			t.Errorf("kind=%s: unexpected error %v", tc.v.Kind, err)
		}
		_ = wire
	}
	// NoneValue already tested in hub_test.go — also cover unsupported path
	// by using a zero-value ParamValue with a kind beyond ValueKindList.
	bad := hmtypes.ParamValue{Kind: hmtypes.ValueKind(99)}
	_, err := s.toWire(bad)
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestSysvarOnUpdateUnsubscribe(t *testing.T) {
	s := &Sysvar{HubDataPoint: HubDataPoint{Name: "sv"}, Writer: &stubSysvar{}}
	var count atomic.Int32
	unsub := s.OnUpdate(func(_, _ hmtypes.ParamValue) { count.Add(1) })
	s.OnValue(hmtypes.IntValue(1))
	unsub()
	s.OnValue(hmtypes.IntValue(2))
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestSysvarSet_writerError(t *testing.T) {
	sentinel := errors.New("rega")
	s := &Sysvar{HubDataPoint: HubDataPoint{Name: "sv"}, Writer: &errSysvarWriter{err: sentinel}}
	if !errors.Is(s.Set(context.Background(), hmtypes.IntValue(1)), sentinel) {
		t.Fatal("expected writer error")
	}
}

func TestSysvarConcurrentOnValue(t *testing.T) {
	s := &Sysvar{HubDataPoint: HubDataPoint{Name: "sv"}, Writer: &stubSysvar{}}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			s.OnValue(hmtypes.IntValue(i))
		})
	}
	wg.Wait()
}

// ─── Inbox ──────────────────────────────────────────────────────────────────

func TestInboxObserved(t *testing.T) {
	in := NewInbox()
	if in.Observed() {
		t.Fatal("fresh inbox must not be observed")
	}
	in.Replace(nil)
	if !in.Observed() {
		t.Fatal("after Replace inbox must be observed")
	}
}

func TestInboxOnUpdateUnsubscribe(t *testing.T) {
	in := NewInbox()
	var count atomic.Int32
	unsub := in.OnUpdate(func([]InboxDevice) { count.Add(1) })
	in.Replace([]InboxDevice{{Address: "0001"}})
	unsub()
	in.Replace([]InboxDevice{{Address: "0002"}})
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestSameInbox_differentValues(t *testing.T) {
	a := map[string]InboxDevice{"x": {Address: "x", Model: "A"}}
	b := map[string]InboxDevice{"x": {Address: "x", Model: "B"}}
	if sameInbox(a, b) {
		t.Fatal("different models should not be equal")
	}
}

func TestInboxConcurrentReplace(t *testing.T) {
	in := NewInbox()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			in.Replace([]InboxDevice{{Address: "0001"}})
			_ = in.Count()
			_ = in.List()
		})
	}
	wg.Wait()
}

// ─── InstallMode ─────────────────────────────────────────────────────────────

func TestInstallModeOnUpdateUnsubscribe(t *testing.T) {
	m := NewInstallMode("HmIP-RF", &stubInstall{})
	var count atomic.Int32
	unsub := m.OnUpdate(func(_ bool, _ time.Duration) { count.Add(1) })
	m.OnState(true, 30*time.Second)
	unsub()
	m.OnState(false, 0)
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestInstallModeEnable_writerError(t *testing.T) {
	sentinel := errors.New("rega")
	m := NewInstallMode("iface", &errInstallWriter{err: sentinel})
	if !errors.Is(m.Enable(context.Background(), time.Minute), sentinel) {
		t.Fatal("expected writer error")
	}
}

func TestInstallModeDisable_noWriter(t *testing.T) {
	m := NewInstallMode("iface", nil)
	if err := m.Disable(context.Background()); err == nil {
		t.Fatal("expected error when no writer")
	}
}

func TestInstallModeDisable_writerError(t *testing.T) {
	sentinel := errors.New("rega")
	m := NewInstallMode("iface", &errInstallWriter{err: sentinel})
	if !errors.Is(m.Disable(context.Background()), sentinel) {
		t.Fatal("expected writer error")
	}
}

func TestInstallModeStateAfterExpiry(t *testing.T) {
	m := NewInstallMode("iface", nil)
	// Simulate a very short window that will have expired.
	m.mu.Lock()
	m.enabled = true
	m.observed = true
	m.expiresAt = time.Now().Add(-1 * time.Second) // already past
	m.mu.Unlock()
	enabled, remain, obs := m.InstallState()
	if !enabled {
		t.Fatal("enabled flag must still be true (we didn't disable)")
	}
	if remain != 0 {
		t.Fatalf("remaining must clamp to 0 after expiry, got %v", remain)
	}
	if !obs {
		t.Fatal("must be observed")
	}
}

// ─── AlarmMessages ───────────────────────────────────────────────────────────

func TestAlarmMessagesObserved(t *testing.T) {
	a := NewAlarmMessages(&stubAck{})
	if a.Observed() {
		t.Fatal("fresh aggregate must not be observed")
	}
	a.Replace(nil)
	if !a.Observed() {
		t.Fatal("after Replace must be observed")
	}
}

func TestAlarmMessagesOnUpdateUnsubscribe(t *testing.T) {
	a := NewAlarmMessages(&stubAck{})
	var count atomic.Int32
	unsub := a.OnUpdate(func([]AlarmMessage) { count.Add(1) })
	a.Replace([]AlarmMessage{{ID: "x", Timestamp: time.Now(), Counter: 1}})
	unsub()
	a.Replace([]AlarmMessage{{ID: "y", Timestamp: time.Now(), Counter: 1}})
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestAlarmMessagesAcknowledge_noAcker(t *testing.T) {
	a := NewAlarmMessages(nil)
	a.Replace([]AlarmMessage{{ID: "x", Timestamp: time.Now()}})
	if err := a.Acknowledge(context.Background(), "x"); err == nil {
		t.Fatal("expected error without acknowledger")
	}
}

func TestAlarmMessagesAcknowledge_writerError(t *testing.T) {
	sentinel := errors.New("rega")
	a := NewAlarmMessages(&errAck{err: sentinel})
	a.Replace([]AlarmMessage{{ID: "x", Timestamp: time.Now()}})
	if !errors.Is(a.Acknowledge(context.Background(), "x"), sentinel) {
		t.Fatal("expected acker error")
	}
}

func TestAlarmMessagesAcknowledge_nonExistent(t *testing.T) {
	// Acknowledge an ID that is not in the current set — should succeed
	// without firing callbacks.
	a := NewAlarmMessages(&stubAck{})
	a.Replace([]AlarmMessage{{ID: "a", Timestamp: time.Now()}})
	var fired atomic.Int32
	a.OnUpdate(func([]AlarmMessage) { fired.Add(1) })
	if err := a.Acknowledge(context.Background(), "nonexistent"); err != nil {
		t.Fatal(err)
	}
	if fired.Load() != 0 {
		t.Fatal("callback must not fire for non-existent ID")
	}
}

func TestSameAlarmSet_timestampMismatch(t *testing.T) {
	t1 := time.Now()
	t2 := t1.Add(time.Second)
	a := map[string]AlarmMessage{"x": {ID: "x", Timestamp: t1}}
	b := map[string]AlarmMessage{"x": {ID: "x", Timestamp: t2}}
	if sameAlarmSet(a, b) {
		t.Fatal("different timestamps must not be equal")
	}
}

// ─── ServiceMessages ─────────────────────────────────────────────────────────

func TestServiceMessagesObserved(t *testing.T) {
	s := NewServiceMessages(&stubAck{})
	if s.Observed() {
		t.Fatal("fresh aggregate must not be observed")
	}
	s.Replace(nil)
	if !s.Observed() {
		t.Fatal("after Replace must be observed")
	}
}

func TestServiceMessagesOnUpdateUnsubscribe(t *testing.T) {
	s := NewServiceMessages(&stubAck{})
	var count atomic.Int32
	unsub := s.OnUpdate(func([]ServiceMessage) { count.Add(1) })
	s.Replace([]ServiceMessage{{ID: "x", Timestamp: time.Now()}})
	unsub()
	s.Replace([]ServiceMessage{{ID: "y", Timestamp: time.Now()}})
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestServiceMessagesAcknowledge_noAcker(t *testing.T) {
	s := NewServiceMessages(nil)
	s.Replace([]ServiceMessage{{ID: "x", Timestamp: time.Now()}})
	if err := s.Acknowledge(context.Background(), "x"); err == nil {
		t.Fatal("expected error without acknowledger")
	}
}

func TestServiceMessagesAcknowledge_writerError(t *testing.T) {
	sentinel := errors.New("rega")
	s := NewServiceMessages(&errAck{err: sentinel})
	s.Replace([]ServiceMessage{{ID: "x", Timestamp: time.Now()}})
	if !errors.Is(s.Acknowledge(context.Background(), "x"), sentinel) {
		t.Fatal("expected acker error")
	}
}

func TestServiceMessagesAcknowledge_nonExistent(t *testing.T) {
	s := NewServiceMessages(&stubAck{})
	s.Replace([]ServiceMessage{{ID: "a", Timestamp: time.Now()}})
	var fired atomic.Int32
	s.OnUpdate(func([]ServiceMessage) { fired.Add(1) })
	if err := s.Acknowledge(context.Background(), "ghost"); err != nil {
		t.Fatal(err)
	}
	if fired.Load() != 0 {
		t.Fatal("callback must not fire for non-existent ID")
	}
}

func TestSameServiceSet_timestampMismatch(t *testing.T) {
	t1 := time.Now()
	t2 := t1.Add(time.Second)
	a := map[string]ServiceMessage{"x": {ID: "x", Timestamp: t1}}
	b := map[string]ServiceMessage{"x": {ID: "x", Timestamp: t2}}
	if sameServiceSet(a, b) {
		t.Fatal("different timestamps must not be equal")
	}
}

func TestSameServiceSet_missingKey(t *testing.T) {
	t1 := time.Now()
	a := map[string]ServiceMessage{"x": {ID: "x", Timestamp: t1}}
	b := map[string]ServiceMessage{"y": {ID: "y", Timestamp: t1}}
	if sameServiceSet(a, b) {
		t.Fatal("different keys must not be equal")
	}
}

func TestServiceMessagesReplace_noChangeNoFire(t *testing.T) {
	s := NewServiceMessages(&stubAck{})
	ts := time.Now()
	msgs := []ServiceMessage{{ID: "a", Timestamp: ts, Counter: 1}}
	s.Replace(msgs)
	var fired atomic.Int32
	s.OnUpdate(func([]ServiceMessage) { fired.Add(1) })
	// Same content → no fire.
	s.Replace(msgs)
	if fired.Load() != 0 {
		t.Fatal("identical Replace must not fire")
	}
}

// ─── Metrics ────────────────────────────────────────────────────────────────

func TestMetricsOnUpdateUnsubscribe(t *testing.T) {
	m := NewMetrics()
	var count atomic.Int32
	unsub := m.OnUpdate(MetricSystemHealth, func(MetricSample) { count.Add(1) })
	m.Observe(MetricSystemHealth, 100)
	unsub()
	m.Observe(MetricSystemHealth, 50)
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestMetricsOnAnyUnsubscribe(t *testing.T) {
	m := NewMetrics()
	var count atomic.Int32
	unsub := m.OnAny(func(MetricSample) { count.Add(1) })
	m.Observe(MetricSystemHealth, 1)
	unsub()
	m.Observe(MetricConnectionLatMs, 2)
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestMetricsValue_unknownKind(t *testing.T) {
	m := NewMetrics()
	_, ok := m.Value("unknown_kind")
	if ok {
		t.Fatal("unknown kind should return not-ok")
	}
}

// ─── Connectivity ────────────────────────────────────────────────────────────

func TestConnectivityReachable(t *testing.T) {
	c := NewConnectivity()
	r, obs := c.Reachable("HmIP-RF")
	if obs || r {
		t.Fatal("fresh tracker must not be observed")
	}
	c.OnState("HmIP-RF", true)
	r, obs = c.Reachable("HmIP-RF")
	if !obs || !r {
		t.Fatalf("r=%v obs=%v", r, obs)
	}
}

func TestConnectivityList(t *testing.T) {
	c := NewConnectivity()
	c.OnState("B", true)
	c.OnState("A", false)
	list := c.List()
	if len(list) != 2 || list[0].InterfaceID != "A" || list[1].InterfaceID != "B" {
		t.Fatalf("list=%+v", list)
	}
}

func TestConnectivityOnUpdateUnsubscribe(t *testing.T) {
	c := NewConnectivity()
	var count atomic.Int32
	unsub := c.OnUpdate(func(InterfaceReachability) { count.Add(1) })
	c.OnState("HmIP-RF", true)
	unsub()
	c.OnState("HmIP-RF", false)
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestConnectivityAllReachable_empty(t *testing.T) {
	c := NewConnectivity()
	all, obs := c.AllReachable()
	if all || obs {
		t.Fatal("empty tracker must return (false, false)")
	}
}

func TestConnectivityAllReachable_allUp(t *testing.T) {
	c := NewConnectivity()
	c.OnState("A", true)
	c.OnState("B", true)
	all, obs := c.AllReachable()
	if !all || !obs {
		t.Fatalf("all=%v obs=%v", all, obs)
	}
}

func TestConnectivityConcurrentOnState(t *testing.T) {
	c := NewConnectivity()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.OnState("iface", i%2 == 0)
			_, _ = c.Reachable("iface")
		}(i)
	}
	wg.Wait()
}

// ─── Update ──────────────────────────────────────────────────────────────────

func TestUpdateInfo(t *testing.T) {
	u := NewUpdate()
	_, ok := u.UpdateInfo()
	if ok {
		t.Fatal("fresh Update must not be observed")
	}
	info := UpdateInfo{CurrentFirmware: "3.55", AvailableFirmware: "3.57", UpdateAvailable: true}
	u.OnInfo(info)
	got, ok := u.UpdateInfo()
	if !ok {
		t.Fatal("after OnInfo must be observed")
	}
	if got != info {
		t.Fatalf("got=%+v want=%+v", got, info)
	}
}

func TestUpdateOnUpdateUnsubscribe(t *testing.T) {
	u := NewUpdate()
	var count atomic.Int32
	unsub := u.OnUpdate(func(UpdateInfo) { count.Add(1) })
	u.OnInfo(UpdateInfo{CurrentFirmware: "1.0"})
	unsub()
	u.OnInfo(UpdateInfo{CurrentFirmware: "2.0"})
	unsub() // idempotent
	if count.Load() != 1 {
		t.Fatalf("count=%d, want 1", count.Load())
	}
}

func TestUpdateConcurrentOnInfo(t *testing.T) {
	u := NewUpdate()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u.OnInfo(UpdateInfo{CurrentFirmware: "v"})
			_, _ = u.UpdateInfo()
		}(i)
	}
	wg.Wait()
}
