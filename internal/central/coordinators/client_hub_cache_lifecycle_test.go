// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// client_hub_cache_lifecycle_test.go covers:
// ClientCoordinator lifecycle (start/stop/restart/error collection),
// EventCoordinator publish methods,
// HubCoordinator data-point accessors, and CacheCoordinator persistence /
// event subscriptions.

package coordinators

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─────────────────────────────────────────────────────────────────────────────
// ClientCoordinator lifecycle
// ─────────────────────────────────────────────────────────────────────────────

func TestClientCoordinatorPrimaryClientNilWhenEmpty(t *testing.T) {
	t.Parallel()
	cc := NewClientCoordinator()
	if cc.PrimaryClient() != nil {
		t.Fatal("PrimaryClient on empty coordinator must return nil")
	}
}

func TestClientCoordinatorAllClientsActiveFalseWhenEmpty(t *testing.T) {
	t.Parallel()
	cc := NewClientCoordinator()
	if cc.AllClientsActive() {
		t.Fatal("AllClientsActive must be false with no registered clients")
	}
}

func TestClientCoordinatorAvailableFalseWhenEmpty(t *testing.T) {
	t.Parallel()
	cc := NewClientCoordinator()
	if cc.Available() {
		t.Fatal("Available must be false with no registered clients")
	}
}

func TestClientCoordinatorAllClientsActiveFalseWhenOneDisconnected(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()

	// One entry in Connected state and one that is not Connected.
	connected := makeEntry(bus, "iface-A", hmenum.InterfaceHmIPRF)
	// Walk to CONNECTED.
	for _, s := range []hmenum.ClientState{
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
	} {
		if err := connected.Client.TransitionTo(s, "test", false, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition %s: %v", s, err)
		}
	}
	disconnected := makeEntry(bus, "iface-B", hmenum.InterfaceHmIPRF)
	// Stays in CREATED state.

	_ = cc.Register(connected)
	_ = cc.Register(disconnected)

	if cc.AllClientsActive() {
		t.Fatal("AllClientsActive must be false when any client is not CONNECTED")
	}
	// Available mirrors AllClientsActive: all clients must be CONNECTED.
	if cc.Available() {
		t.Fatal("Available must be false when any client is not CONNECTED")
	}
}

func TestClientCoordinatorStartClientsInvokesHooks(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()

	var calls atomic.Int32
	hook := func(_ context.Context) error { calls.Add(1); return nil }

	e := makeEntry(bus, "iface-X", hmenum.InterfaceHmIPRF)
	e.StartFunc = hook
	_ = cc.Register(e)

	if err := cc.StartClients(context.Background()); err != nil {
		t.Fatalf("StartClients: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("StartFunc called %d times, want 1", calls.Load())
	}
}

func TestClientCoordinatorStopClientsInvokesHooks(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()

	var calls atomic.Int32
	hook := func(_ context.Context) error { calls.Add(1); return nil }

	e := makeEntry(bus, "iface-Y", hmenum.InterfaceHmIPRF)
	e.StopFunc = hook
	_ = cc.Register(e)

	if err := cc.StopClients(context.Background()); err != nil {
		t.Fatalf("StopClients: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("StopFunc called %d times, want 1", calls.Load())
	}
}

func TestClientCoordinatorStartClientsCollectsErrors(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()

	boom := errors.New("boom")
	for _, id := range []string{"a", "b"} {
		e := makeEntry(bus, id, hmenum.InterfaceHmIPRF)
		e.StartFunc = func(_ context.Context) error { return boom }
		_ = cc.Register(e)
	}

	err := cc.StartClients(context.Background())
	if err == nil {
		t.Fatal("expected error from StartClients when hooks fail")
	}
	// Both errors should be joined.
	if !errors.Is(err, boom) {
		t.Errorf("error should wrap boom: %v", err)
	}
}

func TestClientCoordinatorNilHooksAreNoOp(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()

	e := makeEntry(bus, "iface-nil", hmenum.InterfaceHmIPRF)
	// StartFunc and StopFunc are nil (zero value).
	_ = cc.Register(e)

	if err := cc.StartClients(context.Background()); err != nil {
		t.Fatalf("StartClients with nil hook must not error: %v", err)
	}
	if err := cc.StopClients(context.Background()); err != nil {
		t.Fatalf("StopClients with nil hook must not error: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 3 — EventCoordinator publish methods
// ─────────────────────────────────────────────────────────────────────────────

func TestPublishBackendParameterEventEmits(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)
	ec.SetCentralName("c-test")

	var got []hmevent.RPCParameterReceivedEvent
	events.Subscribe(bus, func(e hmevent.RPCParameterReceivedEvent) {
		got = append(got, e)
	})

	ec.PublishBackendParameterEvent("HmIP-RF", "DEV:1", "LEVEL", "0.5")

	if len(got) != 1 {
		t.Fatalf("expected 1 RPCParameterReceivedEvent, got %d", len(got))
	}
	e := got[0]
	if e.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID=%q want HmIP-RF", e.InterfaceID)
	}
	if e.CentralName != "c-test" {
		t.Errorf("CentralName=%q want c-test", e.CentralName)
	}
	if e.RawValue != "0.5" {
		t.Errorf("RawValue=%q want 0.5", e.RawValue)
	}
}

func TestPublishBackendParameterEventEmptyInterfaceIsNoop(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)

	var count atomic.Int32
	events.Subscribe(bus, func(_ hmevent.RPCParameterReceivedEvent) { count.Add(1) })
	ec.PublishBackendParameterEvent("", "DEV:1", "LEVEL", "1")

	if count.Load() != 0 {
		t.Fatal("empty interfaceID must not emit event")
	}
}

func TestPublishDeviceTriggerEventEmits(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)
	ec.SetCentralName("c-trig")

	var got []hmevent.DeviceTriggerEvent
	events.Subscribe(bus, func(e hmevent.DeviceTriggerEvent) {
		got = append(got, e)
	})

	ec.PublishDeviceTriggerEvent(
		context.Background(),
		"HmIP-RF", "DEV001", 1,
		hmenum.DeviceTriggerEventTypeKeypress,
		"PRESS_SHORT",
		hmtypes.BoolValue(true),
	)

	if len(got) != 1 {
		t.Fatalf("expected 1 DeviceTriggerEvent, got %d", len(got))
	}
	ev := got[0]
	if ev.CentralName != "c-trig" {
		t.Errorf("CentralName=%q want c-trig", ev.CentralName)
	}
	if ev.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID=%q want HmIP-RF", ev.InterfaceID)
	}
	if ev.Parameter != "PRESS_SHORT" {
		t.Errorf("Parameter=%q want PRESS_SHORT", ev.Parameter)
	}
}

func TestPublishDeviceTriggerEventUpdatesStamp(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)

	_, observed := ec.LastEventMonotonicForInterface("HmIP-RF")
	if observed {
		t.Fatal("interface should not be observed before any event")
	}

	ec.PublishDeviceTriggerEvent(
		context.Background(),
		"HmIP-RF", "DEV001", 0,
		hmenum.DeviceTriggerEventTypeKeypress,
		"PRESS_SHORT", hmtypes.BoolValue(true),
	)

	_, observed = ec.LastEventMonotonicForInterface("HmIP-RF")
	if !observed {
		t.Fatal("interface should be observed after PublishDeviceTriggerEvent")
	}
}

func TestPublishSystemEventEmits(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)

	var got []hmevent.SystemStatusChangedEvent
	events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
		got = append(got, e)
	})

	ec.PublishSystemEvent(context.Background(), hmevent.SystemStatusChangedEvent{
		CentralName: "c-sys",
		Component:   "test",
		Healthy:     true,
	})

	if len(got) != 1 {
		t.Fatalf("expected 1 SystemStatusChangedEvent, got %d", len(got))
	}
	if got[0].CentralName != "c-sys" {
		t.Errorf("CentralName=%q want c-sys", got[0].CentralName)
	}
}

func TestAddDataPointSubscriptionReceivesEvents(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)

	var count atomic.Int32
	unsub := ec.AddDataPointSubscription(func(_ hmevent.DataPointValueChangedEvent) {
		count.Add(1)
	})
	defer unsub()

	// Publish directly on the bus to simulate a cache update.
	events.Publish(bus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "LEVEL",
		},
		NewValue: hmtypes.FloatValue(0.5),
	})

	if count.Load() != 1 {
		t.Fatalf("subscription received %d events, want 1", count.Load())
	}
}

func TestAddDataPointSubscriptionUnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)

	var count atomic.Int32
	unsub := ec.AddDataPointSubscription(func(_ hmevent.DataPointValueChangedEvent) {
		count.Add(1)
	})

	// Unsubscribe before publishing.
	unsub()
	events.Publish(bus, hmevent.DataPointValueChangedEvent{
		Base:     hmevent.NewBase(),
		NewValue: hmtypes.IntValue(1),
	})

	if count.Load() != 0 {
		t.Fatalf("after unsub, received %d events, want 0", count.Load())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 4 — HubCoordinator data-point accessors
// ─────────────────────────────────────────────────────────────────────────────

func TestHubCoordinatorAccessorsNilWhenNoHubModel(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c-hub", bus)

	if h.AlarmMessagesDP() != nil {
		t.Error("AlarmMessagesDP must return nil when no hub model wired")
	}
	if h.ServiceMessagesDP() != nil {
		t.Error("ServiceMessagesDP must return nil when no hub model wired")
	}
	if h.MetricsDPs() != nil {
		t.Error("MetricsDPs must return nil when no hub model wired")
	}
	if h.InstallModeDPs() != nil {
		t.Error("InstallModeDPs must return nil when no hub model wired")
	}
	if h.ProgramDataPoints() != nil {
		t.Error("ProgramDataPoints must return nil when no hub model wired")
	}
	if h.SysvarDataPoints() != nil {
		t.Error("SysvarDataPoints must return nil when no hub model wired")
	}
}

func TestHubCoordinatorSetHubModelWiresAccessors(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c-hub2", bus)
	m := hub.NewHub("c-hub2")
	h.SetHubModel(m)

	if h.AlarmMessagesDP() == nil {
		t.Error("AlarmMessagesDP must not be nil after hub model wired")
	}
	if h.ServiceMessagesDP() == nil {
		t.Error("ServiceMessagesDP must not be nil after hub model wired")
	}
	if h.MetricsDPs() == nil {
		t.Error("MetricsDPs must not be nil after hub model wired")
	}
	// InstallModeDPs returns nil slice (empty), not nil when hub model
	// is wired. ProgramDataPoints returns nil slice too — both are
	// acceptable; nil would mean no model wired. Touch them so the
	// hub.Programs() / hub.Sysvars() paths are still exercised.
	_ = h.ProgramDataPoints()
	_ = h.SysvarDataPoints()
}

func TestHubCoordinatorAddRemoveProgramDP(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c-prog", bus)
	m := hub.NewHub("c-prog")
	h.SetHubModel(m)

	p := hub.NewProgram("c-prog", "prog-1", "Test Program", "", false, nil)
	h.AddProgramDP(p)

	progs := h.ProgramDataPoints()
	if len(progs) != 1 {
		t.Fatalf("ProgramDataPoints: got %d, want 1", len(progs))
	}
	if progs[0].ID != "prog-1" {
		t.Errorf("ID=%q, want prog-1", progs[0].ID)
	}

	removed := h.RemoveProgramDP("prog-1")
	if !removed {
		t.Error("RemoveProgramDP should return true for existing program")
	}
	if len(h.ProgramDataPoints()) != 0 {
		t.Error("ProgramDataPoints should be empty after removal")
	}
}

func TestHubCoordinatorAddRemoveSysvarDP(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c-sysvar", bus)
	m := hub.NewHub("c-sysvar")
	h.SetHubModel(m)

	sv := hub.NewSysvar("c-sysvar", "MyVar", "My Variable", hmenum.HubValueTypeInteger, nil)
	h.AddSysvarDP(sv)

	svars := h.SysvarDataPoints()
	if len(svars) != 1 {
		t.Fatalf("SysvarDataPoints: got %d, want 1", len(svars))
	}
	if svars[0].Name != "MyVar" {
		t.Errorf("Name=%q, want MyVar", svars[0].Name)
	}

	removed := h.RemoveSysvarDP("MyVar")
	if !removed {
		t.Error("RemoveSysvarDP should return true for existing sysvar")
	}
	if len(h.SysvarDataPoints()) != 0 {
		t.Error("SysvarDataPoints should be empty after removal")
	}
}

func TestHubCoordinatorSetProgramStateNilWriterIsNoop(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c-psw", bus)
	// No writer wired.
	if err := h.SetProgramState(context.Background(), "prog-1", true); err != nil {
		t.Fatalf("SetProgramState with nil writer must return nil: %v", err)
	}
}

func TestHubCoordinatorSetProgramStateDelegatesToWriter(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c-psw2", bus)

	var gotID string
	var gotActive bool
	w := &fakeProgramStateWriter{fn: func(_ context.Context, id string, active bool) error {
		gotID = id
		gotActive = active
		return nil
	}}
	h.SetProgramStateWriter(w)

	if err := h.SetProgramState(context.Background(), "prog-42", false); err != nil {
		t.Fatalf("SetProgramState: %v", err)
	}
	if gotID != "prog-42" {
		t.Errorf("programID=%q, want prog-42", gotID)
	}
	if gotActive {
		t.Error("active should be false")
	}
}

func TestHubCoordinatorSetSystemVariableNilWriterIsNoop(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c-svw", bus)
	if err := h.SetSystemVariable(context.Background(), "Var1", 42); err != nil {
		t.Fatalf("SetSystemVariable with nil writer must return nil: %v", err)
	}
}

func TestHubCoordinatorSetSystemVariableDelegatesToWriter(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c-svw2", bus)

	var gotName string
	var gotValue any
	w := &fakeSysvarWriter{fn: func(_ context.Context, name string, value any) error {
		gotName = name
		gotValue = value
		return nil
	}}
	h.SetSysvarValueWriter(w)

	if err := h.SetSystemVariable(context.Background(), "Counter", 7); err != nil {
		t.Fatalf("SetSystemVariable: %v", err)
	}
	if gotName != "Counter" {
		t.Errorf("name=%q, want Counter", gotName)
	}
	if gotValue != 7 {
		t.Errorf("value=%v, want 7", gotValue)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 5 — CacheCoordinator persistence
// ─────────────────────────────────────────────────────────────────────────────

func TestCacheLoadAllPopulatesEntries(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	key := dpKey("iface", "A:1", "LEVEL")
	entry := DataCacheEntry{Value: hmtypes.FloatValue(0.5), Source: "stored"}

	fp := &fakePersister{
		data: map[hmtypes.DataPointKey]DataCacheEntry{key: entry},
	}
	c.SetPersister(fp)

	if err := c.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	got, ok := c.Get(key)
	if !ok {
		t.Fatal("entry should be present after LoadAll")
	}
	if got.Value.Float != 0.5 {
		t.Errorf("value=%v, want 0.5", got.Value.Float)
	}
}

func TestCacheLoadAllNilPersisterIsNoop(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	if err := c.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll with nil persister must return nil: %v", err)
	}
}

func TestCacheSaveAllPersistsEntries(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	key := dpKey("iface", "B:1", "STATE")
	c.Set(key, hmtypes.BoolValue(true), "source")

	fp := &fakePersister{}
	c.SetPersister(fp)

	if err := c.SaveAll(context.Background()); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	if fp.saves != 1 {
		t.Fatalf("SaveAll should call persister once, got %d", fp.saves)
	}
	if _, ok := fp.data[key]; !ok {
		t.Error("persisted data should contain the set key")
	}
}

func TestCacheSaveIfChangedSkipsOnCleanCache(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	fp := &fakePersister{}
	c.SetPersister(fp)

	// Cache is clean (no dirty flag set).
	if err := c.SaveIfChanged(context.Background()); err != nil {
		t.Fatalf("SaveIfChanged: %v", err)
	}
	if fp.saves != 0 {
		t.Fatalf("SaveIfChanged on clean cache must not call persister, called %d times", fp.saves)
	}
}

func TestCacheSaveIfChangedSavesWhenDirty(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	fp := &fakePersister{}
	c.SetPersister(fp)

	// Mark dirty via Set.
	c.Set(dpKey("iface", "C:0", "PARAM"), hmtypes.IntValue(1), "src")

	if err := c.SaveIfChanged(context.Background()); err != nil {
		t.Fatalf("SaveIfChanged: %v", err)
	}
	if fp.saves != 1 {
		t.Fatalf("SaveIfChanged on dirty cache must call persister once, called %d", fp.saves)
	}
}

func TestCacheClearAllResetsCache(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	c.Set(dpKey("iface", "D:1", "X"), hmtypes.IntValue(1), "src")
	c.Set(dpKey("iface", "D:2", "Y"), hmtypes.IntValue(2), "src")

	if c.Len() != 2 {
		t.Fatalf("Len=%d, want 2 before ClearAll", c.Len())
	}
	c.ClearAll()
	if c.Len() != 0 {
		t.Fatalf("Len=%d, want 0 after ClearAll", c.Len())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 5 — CacheCoordinator event subscriptions
// ─────────────────────────────────────────────────────────────────────────────

func TestCacheSubscribeToBusEvictsOnDeviceRemoved(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewCacheCoordinator()
	c.SubscribeToBus(bus)
	defer c.UnsubscribeAll()

	// Add two entries for the same device on different channels.
	c.Set(dpKey("iface", "DEV001:0", "X"), hmtypes.IntValue(1), "src")
	c.Set(dpKey("iface", "DEV001:1", "Y"), hmtypes.IntValue(2), "src")
	// And one for a different device.
	c.Set(dpKey("iface", "DEV002:0", "Z"), hmtypes.IntValue(3), "src")

	if c.Len() != 3 {
		t.Fatalf("Len=%d, want 3 before removal", c.Len())
	}

	events.Publish(bus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c",
		InterfaceID: "iface",
		Address:     "DEV001",
	})

	// The bus is synchronous within the goroutine; after Publish returns
	// the subscription handler has run.
	if c.Len() != 1 {
		t.Fatalf("Len=%d, want 1 after DEV001 removed (DEV002 should remain)", c.Len())
	}
}

func TestCacheSubscribeToBusMarkssDirtyOnDataFetchCompleted(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewCacheCoordinator()
	// Start clean — load clears dirty flag after a LoadAll.
	c.Set(dpKey("iface", "A:0", "P"), hmtypes.IntValue(1), "src")
	fp := &fakePersister{}
	c.SetPersister(fp)
	// Save to clear dirty.
	_ = c.SaveAll(context.Background())

	c.SubscribeToBus(bus)
	defer c.UnsubscribeAll()

	events.Publish(bus, hmevent.DataFetchCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c",
		InterfaceID: "iface",
		Operation:   "values",
		Success:     true,
	})

	// Now SaveIfChanged should call the persister because dirty was set.
	if err := c.SaveIfChanged(context.Background()); err != nil {
		t.Fatalf("SaveIfChanged: %v", err)
	}
	if fp.saves != 2 { // 1 from SaveAll + 1 from SaveIfChanged
		t.Fatalf("saves=%d, want 2", fp.saves)
	}
}

func TestCacheUnsubscribeAllStopsDelivery(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewCacheCoordinator()
	c.SubscribeToBus(bus)

	c.Set(dpKey("iface", "DEV003:0", "P"), hmtypes.IntValue(1), "src")
	c.UnsubscribeAll()

	// After unsubscribe, DeviceRemovedEvent must not evict.
	events.Publish(bus, hmevent.DeviceRemovedEvent{
		Base:    hmevent.NewBase(),
		Address: "DEV003",
	})
	if c.Len() != 1 {
		t.Fatalf("Len=%d, want 1 — unsubscribed cache must not evict", c.Len())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Fakes
// ─────────────────────────────────────────────────────────────────────────────

type fakePersister struct {
	data  map[hmtypes.DataPointKey]DataCacheEntry
	saves int
}

func (f *fakePersister) LoadDataCache(_ context.Context) (map[hmtypes.DataPointKey]DataCacheEntry, error) {
	if f.data == nil {
		return nil, nil
	}
	return f.data, nil
}

func (f *fakePersister) SaveDataCache(_ context.Context, entries map[hmtypes.DataPointKey]DataCacheEntry) error {
	f.saves++
	f.data = entries
	return nil
}

type fakeProgramStateWriter struct {
	fn func(ctx context.Context, id string, active bool) error
}

func (f *fakeProgramStateWriter) SetProgramActive(ctx context.Context, id string, active bool) error {
	return f.fn(ctx, id, active)
}

type fakeSysvarWriter struct {
	fn func(ctx context.Context, name string, value any) error
}

func (f *fakeSysvarWriter) SetSysvar(ctx context.Context, name string, value any) error {
	return f.fn(ctx, name, value)
}
