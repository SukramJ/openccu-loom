// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// This file drives the intent router (intents.go) through a real
// alarm.Service — a real engine, a real journal, real SQLite stores —
// with only the CodeSource faked, so the tests exercise the same
// wiring intents_test's sibling production code runs on (WKP
// CODE_ID/CODE_STATE correlation and remote-key bindings,
// notes/concepts/alarm-concept.md §11 and notes/reference/alarm-assumptions.md Q4).

// intentsTestStart is the harness wall-clock origin (after the engine's
// plausibility epoch).
var intentsTestStart = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

// fakeCodeSource returns a fixed set of parsed code rows, wired via
// Service.SetCodeSource. Only identity + binding matter to keypad/
// remote intent routing, never the secret, so bypassing the real codes
// facade (argon2 hashing, DB rows) keeps these tests focused.
type fakeCodeSource struct {
	rows []CodeRow
	err  error
}

func (f *fakeCodeSource) Rows(context.Context) ([]CodeRow, error) { return f.rows, f.err }

// intentsHarness bundles a real SQLite-backed alarm.Service around an
// empty central registry (the router is fed synthetic wire events
// directly — no godevccu needed) and a fake CodeSource.
type intentsHarness struct {
	t   *testing.T
	ctx context.Context
	clk *clock.Fake
	svc *Service
}

// newIntentsHarness opens a fresh temp-file SQLite database, builds the
// alarm.Service on it, and wires src as the code source (nil keeps
// hardware-code routing inert).
func newIntentsHarness(t *testing.T, src CodeSource) *intentsHarness {
	t.Helper()
	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-intents.db"))
	db, err := sqlitestore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	clk := clock.NewFake(intentsTestStart)
	svc, err := NewService(Deps{
		Settings: Settings{Enabled: true},
		Registry: central.NewRegistry(),
		Stores:   NewStores(db),
		Clock:    clk,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetCodeSource(src)
	return &intentsHarness{t: t, ctx: context.Background(), clk: clk, svc: svc}
}

// seedZone persists a minimal armable zone: mode "full" with no exit
// delay, so Arm completes synchronously and dispatchArm's default mode
// resolves.
func (h *intentsHarness) seedZone(id, name string) {
	h.t.Helper()
	cfg := engine.ZoneConfig{Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}}}
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal zone config: %v", err)
	}
	now := intentsTestStart.UnixMilli()
	if err := h.svc.Stores().Zones.Upsert(h.ctx, sqlitestore.AlarmZoneRow{
		ID: id, Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed zone: %v", err)
	}
}

// start starts the service (loads config, subscribes — a no-op here
// since the registry is empty — and runs the S4 reconciliation pass
// over the empty output set) and registers cleanup.
func (h *intentsHarness) start() {
	h.t.Helper()
	if err := h.svc.Start(h.ctx); err != nil {
		h.t.Fatalf("service start: %v", err)
	}
	h.t.Cleanup(func() { _ = h.svc.Stop(context.Background()) })
}

// zoneState reads the engine's current state for id, failing if unknown.
func (h *intentsHarness) zoneState(id string) hmenum.AlarmZoneState {
	h.t.Helper()
	snap, ok := h.svc.Engine().Zone(id)
	if !ok {
		h.t.Fatalf("unknown zone %q", id)
	}
	return snap.State
}

// journalEvents returns every journal event name recorded so far
// (hidden entries included, since duress tests need to see them).
func (h *intentsHarness) journalEvents() []string {
	h.t.Helper()
	entries, err := h.svc.Stores().Journal.Query(h.ctx, sqlitestore.AlarmJournalFilter{IncludeHidden: true})
	if err != nil {
		h.t.Fatalf("query journal: %v", err)
	}
	out := make([]string, len(entries))
	for i := range entries {
		e := &entries[i]
		out[i] = e.Event
	}
	return out
}

func (h *intentsHarness) wantJournalEvent(event string) {
	h.t.Helper()
	for _, e := range h.journalEvents() {
		if e == event {
			return
		}
	}
	h.t.Fatalf("missing %q journal entry; got %v", event, h.journalEvents())
}

func (h *intentsHarness) wantNoJournalEvent(event string) {
	h.t.Helper()
	for _, e := range h.journalEvents() {
		if e == event {
			h.t.Fatalf("unexpected %q journal entry; got %v", event, h.journalEvents())
		}
	}
}

// wkpEvent builds a synthetic data-point value-changed event addressed
// at channelAddress/parameter, the shape the intent router consumes
// (intents.go's onEvent).
func wkpEvent(channelAddress string, param hmenum.Parameter, val hmtypes.ParamValue) hmevent.DataPointValueChangedEvent {
	return hmevent.DataPointValueChangedEvent{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: channelAddress,
			Parameter:      string(param),
		},
		NewValue: val,
	}
}

const intentsTestCentral = "ccu1"

// --- WKP keypad correlation ---

func TestIntentsWKP_MatchedLockArmsTheBoundZone(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "c1", Name: "Alice", Kind: CodeKindKeypadSlot, Enabled: true,
		Perms:   CodePerms{Arm: true, Disarm: true},
		Binding: CodeBinding{Central: intentsTestCentral, DeviceAddress: "WKP0001", Slot: 1, ArmMode: "full", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))
	// Pair 1's lock channel is the odd member of the pair: channel 1.
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:1", hmenum.ParameterPressLock, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateArmed {
		t.Fatalf("zone state = %s, want armed", got)
	}
	entries, err := h.svc.Stores().Journal.Query(h.ctx, sqlitestore.AlarmJournalFilter{})
	if err != nil {
		t.Fatalf("query journal: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Event == "armed" {
			found = true
			if e.Actor != "Alice" {
				t.Fatalf("armed entry actor = %q, want Alice", e.Actor)
			}
		}
	}
	if !found {
		t.Fatal("missing armed journal entry")
	}
}

func TestIntentsWKP_MatchedUnlockDisarmsTheBoundZone(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "c1", Name: "Alice", Kind: CodeKindKeypadSlot, Enabled: true,
		Perms:   CodePerms{Arm: true, Disarm: true},
		Binding: CodeBinding{Central: intentsTestCentral, DeviceAddress: "WKP0001", Slot: 1, ArmMode: "full", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	if _, err := h.svc.Engine().Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))
	// Pair 1's unlock channel is the even member of the pair: channel 2.
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:2", hmenum.ParameterPressUnlock, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed", got)
	}
}

func TestIntentsWKP_PressOutsideTheCorrelationWindowIsUnmatched(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "c1", Name: "Alice", Kind: CodeKindKeypadSlot, Enabled: true,
		Perms:   CodePerms{Arm: true, Disarm: true},
		Binding: CodeBinding{Central: intentsTestCentral, DeviceAddress: "WKP0001", Slot: 1, ArmMode: "full", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))

	h.clk.Advance(3 * time.Second) // beyond the 2s correlation window

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:1", hmenum.ParameterPressLock, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed (a stale scan must not correlate)", got)
	}
	h.wantJournalEvent("keypad_press_unmatched")
}

func TestIntentsWKP_OutOfRangeCodeIDNeverCorrelates(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "c1", Name: "Alice", Kind: CodeKindKeypadSlot, Enabled: true,
		Perms:   CodePerms{Arm: true, Disarm: true},
		Binding: CodeBinding{Central: intentsTestCentral, DeviceAddress: "WKP0001", Slot: 1, ArmMode: "full", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	// The documented idle-sentinel CODE_ID value (notes/reference/alarm-assumptions.md
	// Q4): a "known" scan reporting a slot outside the declared 1..8
	// range must never correlate, however coincidental the timing.
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(32)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:1", hmenum.ParameterPressLock, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed", got)
	}
	h.wantJournalEvent("keypad_press_unmatched")
}

func TestIntentsWKP_MatchedButUnboundSlotIsUnmatched(t *testing.T) {
	h := newIntentsHarness(t, &fakeCodeSource{}) // no code rows at all
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:1", hmenum.ParameterPressLock, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed", got)
	}
	h.wantJournalEvent("keypad_press_unmatched")
}

func TestIntentsWKP_LockWithoutArmPermissionIsDenied(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "c1", Name: "Guest", Kind: CodeKindKeypadSlot, Enabled: true,
		Perms:   CodePerms{Arm: false, Disarm: true},
		Binding: CodeBinding{Central: intentsTestCentral, DeviceAddress: "WKP0001", Slot: 1, ArmMode: "full", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:1", hmenum.ParameterPressLock, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed", got)
	}
	h.wantJournalEvent("code_permission_denied")
}

func TestIntents_NoCodeSourceWiredIsInert(t *testing.T) {
	h := newIntentsHarness(t, nil) // overrides the default facade adapter
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))
	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:1", hmenum.ParameterPressLock, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed", got)
	}
	if entries := h.journalEvents(); len(entries) != 0 {
		t.Fatalf("expected no journal entries with no code source wired, got %v", entries)
	}
}

// --- remote-key bindings ---

func TestIntentsRemote_ArmBindingArmsTheBoundZone(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "r1", Name: "Living Room Remote", Kind: CodeKindRemoteKey, Enabled: true,
		Perms:   CodePerms{Arm: true},
		Binding: CodeBinding{Central: intentsTestCentral, ChannelAddress: "REMOTE01:1", Parameter: "PRESS_SHORT", Action: "arm:full", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("REMOTE01:1", hmenum.ParameterPressShort, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateArmed {
		t.Fatalf("zone state = %s, want armed", got)
	}
}

func TestIntentsRemote_DisarmBindingDisarmsTheBoundZone(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "r1", Name: "Remote", Kind: CodeKindRemoteKey, Enabled: true,
		Perms:   CodePerms{Disarm: true},
		Binding: CodeBinding{Central: intentsTestCentral, ChannelAddress: "REMOTE01:1", Parameter: "PRESS_LONG", Action: "disarm", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()
	if _, err := h.svc.Engine().Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("REMOTE01:1", hmenum.ParameterPressLong, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed", got)
	}
}

func TestIntentsRemote_SilenceBindingDispatchesWithoutAFault(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "r1", Name: "Remote", Kind: CodeKindRemoteKey, Enabled: true,
		Perms:   CodePerms{Silence: true},
		Binding: CodeBinding{Central: intentsTestCentral, ChannelAddress: "REMOTE01:1", Parameter: "PRESS_SHORT", Action: "silence", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("REMOTE01:1", hmenum.ParameterPressShort, hmtypes.BoolValue(true)))

	// Silence never fails on state; with no configured outputs there is
	// nothing else to observe, so the assertion is that dispatch did not
	// fault.
	h.wantNoJournalEvent("code_action_failed")
	h.wantNoJournalEvent("code_permission_denied")
}

// TestIntentsRemote_PanicBindingTriggersTheBoundZone pins the panic
// key end to end: a hold-up or medical key is bound to a zone, the
// operator presses it, and the zone enters triggered independently of
// its arm state (notes/concepts/alarm-concept.md §7).
//
// The verb is called directly rather than through an optional port.
// Discovering it by interface assertion made a signature drift invisible
// — the assertion simply failed at runtime, and every bound panic key
// journaled "engine has no panic path" instead of raising an alarm, on
// every installation.
func TestIntentsRemote_PanicBindingTriggersTheBoundZone(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "r1", Name: "Remote", Kind: CodeKindRemoteKey, Enabled: true,
		Binding: CodeBinding{Central: intentsTestCentral, ChannelAddress: "REMOTE01:1", Parameter: "PRESS_LONG", Action: "panic", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("REMOTE01:1", hmenum.ParameterPressLong, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateTriggered {
		t.Fatalf("zone state = %s, want triggered — a bound panic key must reach the engine's always-on path", got)
	}
	h.wantNoJournalEvent("code_action_failed")
	// The trigger is attributed to the key's identity, not to the engine.
	entries, err := h.svc.Stores().Journal.Query(h.ctx, sqlitestore.AlarmJournalFilter{})
	if err != nil {
		t.Fatalf("query journal: %v", err)
	}
	for i := range entries {
		if entries[i].Event == "triggered" {
			if entries[i].Actor != "Remote" || entries[i].Source != "remote" {
				t.Fatalf("panic trigger attributed to actor=%q source=%q, want Remote/remote",
					entries[i].Actor, entries[i].Source)
			}
			return
		}
	}
	t.Fatalf("no triggered journal entry; got %v", h.journalEvents())
}

// TestIntentsRemote_PanicBindingWithoutAZoneJournalsAFault pins the one
// remaining fault branch of the panic path: a binding that names no
// zone cannot address the engine, and must say so visibly (S7).
func TestIntentsRemote_PanicBindingWithoutAZoneJournalsAFault(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "r1", Name: "Remote", Kind: CodeKindRemoteKey, Enabled: true,
		Binding: CodeBinding{Central: intentsTestCentral, ChannelAddress: "REMOTE01:1", Parameter: "PRESS_LONG", Action: "panic"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("REMOTE01:1", hmenum.ParameterPressLong, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed", got)
	}
	h.wantJournalEvent("code_action_failed")
}

func TestIntentsRemote_UnboundPressIsSilentNotAFault(t *testing.T) {
	h := newIntentsHarness(t, &fakeCodeSource{}) // no bindings at all
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("REMOTE99:1", hmenum.ParameterPressShort, hmtypes.BoolValue(true)))

	if entries := h.journalEvents(); len(entries) != 0 {
		t.Fatalf("expected no journal entries for an unbound remote press, got %v", entries)
	}
}

func TestIntentsRemote_ActionWithoutPermissionIsDenied(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "r1", Name: "Remote", Kind: CodeKindRemoteKey, Enabled: true,
		Perms:   CodePerms{Arm: false},
		Binding: CodeBinding{Central: intentsTestCentral, ChannelAddress: "REMOTE01:1", Parameter: "PRESS_SHORT", Action: "arm:full", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("REMOTE01:1", hmenum.ParameterPressShort, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed", got)
	}
	h.wantJournalEvent("code_permission_denied")
}

func TestIntentsRemote_UnknownActionJournalsAFault(t *testing.T) {
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "r1", Name: "Remote", Kind: CodeKindRemoteKey, Enabled: true,
		Perms:   CodePerms{Arm: true, Disarm: true, Silence: true},
		Binding: CodeBinding{Central: intentsTestCentral, ChannelAddress: "REMOTE01:1", Parameter: "PRESS_SHORT", Action: "flashlights", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()

	h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("REMOTE01:1", hmenum.ParameterPressShort, hmtypes.BoolValue(true)))

	if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state = %s, want disarmed", got)
	}
	h.wantJournalEvent("code_action_failed")
}
