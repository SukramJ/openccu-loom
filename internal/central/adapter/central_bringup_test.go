// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestCentralBringUp_ConcurrentReinitIsSerialized guards against two clears
// racing on the same central: without serialization a second reinit's start()
// (wg.Add) overlaps the first's teardown() (wg.Wait), which is a WaitGroup
// misuse that panics, and it leaks a bring-up generation by overwriting the
// live cancel. parentCtx is pre-cancelled so each generation's gated bring-up
// returns immediately. Run under -race.
func TestCentralBringUp_ConcurrentReinitIsSerialized(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // each start()'s gated bring-up exits at once (CCU never "ready")

	unit, err := central.New(central.Config{Name: "ccu-test"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	b := &centralBringUp{logger: slog.Default(), unit: unit, parentCtx: ctx}

	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.reinit(context.Background())
		}()
	}
	wg.Wait()
	b.shutdown() // must not panic / leave the handle inconsistent
}

// TestCentralBringUp_ClearModelClearsDescriptionAndParamsetRegistries is the
// reproducer for the resurrection gap: clearModel must drop a device's
// in-memory device-description, paramset and device-registry entries, not just
// the model object. Otherwise the re-pull's CheckAndCreateDevicesFromCache
// re-materialises a device the CCU no longer reports from its stale
// description, so a device removed on the CCU would survive a cache clear +
// re-pull — and the device registry keeps counting it on /api/v1/info.
//
// Every registry here is keyed the way the hydration pipeline keys it: by the
// canonical `<central>-<iface>` wire id the device also carries as its
// InterfaceID. A device built with the bare interface in both fields makes
// this test agree with any keying bug instead of catching it.
func TestCentralBringUp_ClearModelClearsDescriptionAndParamsetRegistries(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "ccu-test"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	wireID := WireInterfaceID(unit.Name(), hmenum.InterfaceHmIPRF)
	d := device.New(device.Config{
		InterfaceID: wireID, Interface: hmenum.InterfaceHmIPRF,
		Address: "AAAA0001", Model: "HmIP-STH", Name: "Sensor",
	})
	d.AddChannel("AAAA0001:1", 1, "MAINTENANCE", hmenum.ParamsetKeyValues)
	unit.ModelRegistry.Put(d)
	unit.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmtypes.ParseWireInterfaceID(wireID), Address: "AAAA0001", Model: "HmIP-STH",
	})
	unit.DescRegistry.Put(hmtypes.ParseWireInterfaceID(wireID), hmproto.DeviceDescription{Address: "AAAA0001"})
	unit.ParamsetReg.Add(hmtypes.ParseWireInterfaceID(wireID), "AAAA0001:1", hmenum.ParamsetKeyValues,
		hmproto.Paramset{"STATE": {Type: hmenum.ParameterTypeBool}}, "HmIP-STH")

	if unit.DescRegistry.Len() == 0 || unit.ParamsetReg.Len() == 0 || unit.DeviceRegistry.Len() == 0 {
		t.Fatalf("precondition: registries must be populated (desc=%d paramset=%d device=%d)",
			unit.DescRegistry.Len(), unit.ParamsetReg.Len(), unit.DeviceRegistry.Len())
	}

	b := &centralBringUp{logger: slog.Default(), unit: unit}
	b.clearModel()

	if got := unit.ModelRegistry.Len(); got != 0 {
		t.Fatalf("model registry not cleared: Len() = %d, want 0", got)
	}
	if got := unit.DescRegistry.Len(); got != 0 {
		t.Fatalf("description registry not cleared: Len() = %d, want 0 (stale device would be resurrected)", got)
	}
	if got := unit.ParamsetReg.Len(); got != 0 {
		t.Fatalf("paramset registry not cleared: Len() = %d, want 0", got)
	}
	if got := unit.DeviceRegistry.Len(); got != 0 {
		t.Fatalf("device registry not cleared: Len() = %d, want 0 (removed devices keep counting on /api/v1/info)", got)
	}
}

// TestCentralBringUp_CloserOrderAndReRunnable verifies that teardown runs
// per-generation closers in reverse registration order and that a second
// teardown runs nothing (the stack was emptied on the first run).
func TestCentralBringUp_CloserOrderAndReRunnable(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var called []int

	b := &centralBringUp{logger: slog.Default()}
	for i := range 3 {
		idx := i
		b.addCloser(func() {
			mu.Lock()
			called = append(called, idx)
			mu.Unlock()
		})
	}

	b.teardown()

	mu.Lock()
	got := make([]int, len(called))
	copy(got, called)
	mu.Unlock()

	want := []int{2, 1, 0}
	if len(got) != len(want) {
		t.Fatalf("teardown: got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("teardown order wrong: got %v, want %v", got, want)
		}
	}

	// Second teardown must run nothing.
	mu.Lock()
	called = nil
	mu.Unlock()

	b.teardown()

	mu.Lock()
	afterSecond := len(called)
	mu.Unlock()

	if afterSecond != 0 {
		t.Fatalf("second teardown ran %d closers, want 0", afterSecond)
	}
}

// TestCentralBringUp_PermanentClosersRunOnlyOnShutdown verifies that permanent
// closers survive teardown and only fire on shutdown.
func TestCentralBringUp_PermanentClosersRunOnlyOnShutdown(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var genCalled, permCalled int

	b := &centralBringUp{logger: slog.Default()}
	b.addCloser(func() {
		mu.Lock()
		genCalled++
		mu.Unlock()
	})
	b.addPermanentCloser(func() {
		mu.Lock()
		permCalled++
		mu.Unlock()
	})

	b.teardown()

	mu.Lock()
	gc, pc := genCalled, permCalled
	mu.Unlock()

	if gc != 1 {
		t.Fatalf("teardown: gen closer ran %d times, want 1", gc)
	}
	if pc != 0 {
		t.Fatalf("teardown: permanent closer ran %d times before shutdown, want 0", pc)
	}

	b.shutdown()

	mu.Lock()
	gc2, pc2 := genCalled, permCalled
	mu.Unlock()

	// shutdown calls teardown first (gen closer stack is empty now) then runs perm.
	if gc2 != 1 {
		t.Fatalf("shutdown: gen closer total %d, want 1 (already emptied)", gc2)
	}
	if pc2 != 1 {
		t.Fatalf("shutdown: permanent closer ran %d times, want 1", pc2)
	}
}

// TestCentralBringUp_NilClosersIgnored verifies that nil closers passed to
// addCloser and addPermanentCloser are silently dropped and cause no panic.
func TestCentralBringUp_NilClosersIgnored(t *testing.T) {
	t.Parallel()

	b := &centralBringUp{logger: slog.Default()}
	b.addCloser(nil)
	b.addPermanentCloser(nil)

	// Must not panic.
	b.teardown()
	b.shutdown()
}

// TestCentralBringUp_ClearModelRemovesDevices verifies that clearModel removes
// every device from the unit's ModelRegistry so the registry is empty afterwards.
func TestCentralBringUp_ClearModelRemovesDevices(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "ccu-test"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	d1 := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "AAAA0001", Model: "HmIP-STH", Name: "Sensor 1",
	})
	d2 := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "AAAA0002", Model: "HmIP-STH", Name: "Sensor 2",
	})
	unit.ModelRegistry.Put(d1)
	unit.ModelRegistry.Put(d2)

	if got := unit.ModelRegistry.Len(); got != 2 {
		t.Fatalf("pre-clear: expected 2 devices, got %d", got)
	}

	b := &centralBringUp{
		logger: slog.Default(),
		unit:   unit,
	}
	b.clearModel()

	if got := unit.ModelRegistry.Len(); got != 0 {
		t.Fatalf("post-clear: expected 0 devices, got %d", got)
	}
}

// TestCentralBringUp_ClearModelNilSafe verifies that clearModel is a safe
// no-op when the unit or ModelRegistry is nil.
func TestCentralBringUp_ClearModelNilSafe(t *testing.T) {
	t.Parallel()

	// nil unit
	b := &centralBringUp{logger: slog.Default()}
	b.clearModel() // must not panic

	// unit with nil ModelRegistry cannot be constructed via central.New (it
	// always sets ModelRegistry), so verify the guard in clearModel directly
	// via the nil-unit path above — the production guard covers both.
}

// TestBringUpManager_CentralsInInsertionOrder verifies that Centrals returns
// names in the order they were added.
func TestBringUpManager_CentralsInInsertionOrder(t *testing.T) {
	t.Parallel()

	m := newBringUpManager()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		m.add(&centralBringUp{
			cc:     config.CentralConfig{Name: name},
			logger: slog.Default(),
		})
	}

	got := m.Centrals()
	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("Centrals() = %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("Centrals()[%d] = %q, want %q", i, got[i], v)
		}
	}
}

// TestBringUpManager_ReinitUnknownCentralReturnsFalse verifies that
// ReinitCentral returns false for a name that was never added.
func TestBringUpManager_ReinitUnknownCentralReturnsFalse(t *testing.T) {
	t.Parallel()

	m := newBringUpManager()
	if m.ReinitCentral(context.Background(), "does-not-exist") {
		t.Fatal("expected false for unknown central")
	}
}

// TestBringUpManager_TeardownRunsAllHandlesAndParentCancel verifies that
// Teardown fires every handle's permanent + per-gen closers and then calls
// parentCancel.
func TestBringUpManager_TeardownRunsAllHandlesAndParentCancel(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var log []string

	record := func(label string) func() {
		return func() {
			mu.Lock()
			log = append(log, label)
			mu.Unlock()
		}
	}

	m := newBringUpManager()
	m.parentCancel = record("parentCancel")

	// Two handles; each has one per-gen and one permanent closer.
	for _, name := range []string{"first", "second"} {
		b := &centralBringUp{
			cc:     config.CentralConfig{Name: name},
			logger: slog.Default(),
		}
		b.addCloser(record(name + ".gen"))
		b.addPermanentCloser(record(name + ".perm"))
		m.add(b)
	}

	m.Teardown()

	mu.Lock()
	got := make([]string, len(log))
	copy(got, log)
	mu.Unlock()

	// parentCancel must be the last entry.
	if len(got) == 0 {
		t.Fatal("no closers ran")
	}
	if got[len(got)-1] != "parentCancel" {
		t.Fatalf("parentCancel was not last: log=%v", got)
	}

	// All per-gen and permanent closers must have fired.
	contains := func(s string) bool {
		for _, v := range got {
			if v == s {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"first", "second"} {
		if !contains(name + ".gen") {
			t.Errorf("missing %s.gen in log %v", name, got)
		}
		if !contains(name + ".perm") {
			t.Errorf("missing %s.perm in log %v", name, got)
		}
	}
}

// TestBringUpManager_RemoveCentral_Unmanaged returns false for a name the
// manager does not hold and does not panic.
func TestBringUpManager_RemoveCentral_Unmanaged(t *testing.T) {
	m := newBringUpManager()
	if m.RemoveCentral("nope") {
		t.Fatal("RemoveCentral on an unmanaged name returned true")
	}
}

// TestBringUpManager_AddCentral_DuplicateRejected rejects a second add for a
// name already managed, without building a new handle (so no real deps are
// needed — the guard short-circuits before buildAndStart).
func TestBringUpManager_AddCentral_DuplicateRejected(t *testing.T) {
	m := newBringUpManager()
	m.add(&centralBringUp{logger: slog.Default(), cc: config.CentralConfig{Name: "ccu1"}})

	if m.AddCentral(&config.CentralConfig{Name: "ccu1"}, nil) {
		t.Fatal("AddCentral for an already-managed name returned true")
	}
	if got := m.Centrals(); len(got) != 1 {
		t.Fatalf("Centrals() = %v, want exactly the pre-existing entry", got)
	}
}

// TestBringUpManager_RemoveCentral_DropsHandleAndRunsShutdown removes a managed
// handle from the manager's map + order and runs its shutdown (permanent
// closers). The handle has no started generation, so shutdown is a clean no-op
// teardown plus the permanent-closer run.
func TestBringUpManager_RemoveCentral_DropsHandleAndRunsShutdown(t *testing.T) {
	m := newBringUpManager()
	b := &centralBringUp{logger: slog.Default(), cc: config.CentralConfig{Name: "ccu1"}}
	shutdownRan := false
	b.addPermanentCloser(func() { shutdownRan = true })
	m.add(b)

	if !m.RemoveCentral("ccu1") {
		t.Fatal("RemoveCentral on a managed name returned false")
	}
	if !shutdownRan {
		t.Fatal("RemoveCentral did not run the handle's permanent closer (shutdown)")
	}
	if got := m.Centrals(); len(got) != 0 {
		t.Fatalf("Centrals() = %v after remove, want empty", got)
	}
	// Idempotent: a second remove is a no-op false.
	if m.RemoveCentral("ccu1") {
		t.Fatal("second RemoveCentral returned true")
	}
}

// TestBringUpManager_TeardownSnapshotsHandlesUnderLock hammers Teardown
// concurrently with AddCentral/RemoveCentral. Teardown must snapshot
// m.byCentral under the lock (maps.Clone) before iterating it; aliasing the
// live map and reading handles[name] after the unlock races the mutator's
// map writes — a fatal concurrent map read/write that the -race detector (and
// Go's own built-in concurrent-map guard) trips. The handles carry no started
// generation, so every shutdown/teardown is a clean no-op. Run under -race.
func TestBringUpManager_TeardownSnapshotsHandlesUnderLock(t *testing.T) {
	m := newBringUpManager()
	// Seed a permanent handle so Teardown's iteration always has an entry to
	// read while the mutator churns the map.
	m.add(&centralBringUp{logger: slog.Default(), cc: config.CentralConfig{Name: "seed"}})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			name := fmt.Sprintf("churn-%d", i%16)
			m.add(&centralBringUp{logger: slog.Default(), cc: config.CentralConfig{Name: name}})
			m.RemoveCentral(name)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 500 {
			m.Teardown()
		}
		close(stop)
	}()

	wg.Wait()
}

// TestCentralBringUp_ReinitAfterShutdownDoesNotRestart pins that a handle
// retired by shutdown stays retired.
//
// RemoveCentral deletes the handle from the manager and calls shutdown;
// ReinitCentral reads the handle under the lock and then releases it before
// calling reinit. A cache clear (POST /admin/cache/clear) racing a central
// removal therefore ran teardown — a no-op by then — followed by clearModel
// and start, launching a fresh bring-up generation on the still-live parent
// context for a central nothing manages any more. That generation re-announced
// itself to the CCU and was unreachable from Teardown or a second
// RemoveCentral: a goroutine plus a live CCU callback registration surviving
// until the daemon exits.
func TestCentralBringUp_ReinitAfterShutdownDoesNotRestart(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "ccu-retired"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// A live (uncancelled) parent context: a resurrected generation would
	// keep running on it, which is exactly the leak under test.
	b := &centralBringUp{logger: slog.Default(), unit: unit, parentCtx: context.Background()}

	b.shutdown()
	b.reinit(context.Background())

	b.mu.Lock()
	cancel := b.cancel
	b.mu.Unlock()
	if cancel != nil {
		t.Fatal("reinit after shutdown started a new bring-up generation; " +
			"it outlives every teardown path because the handle is no longer managed")
	}
}

// TestBringUpManagerAddCentralWiresDescriptorPersistence pins the persistent
// descriptor cache onto the runtime-adopt path. The manager is built through
// [WireCentrals] — the real composition root — and the central is added the
// way the REST adopt handler adds it, so nothing in the test attaches the
// sinks itself. Both effects are asserted: the pre-existing rows reach the
// fresh Unit's registries (hydration) and a later registry mutation reaches
// SQLite (mirroring). Wiring this only at the boot call site left an adopted
// central without a cache, so every daemon restart re-inventoried it over the
// radio instead of reading the rows it should have written.
func TestBringUpManagerAddCentralWiresDescriptorPersistence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db, err := sqlite.Open(ctx, sqlite.FileDSN(filepath.Join(t.TempDir(), "adopt-descriptors.db")))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stores := DescriptorStores{
		Devices:   sqlite.NewDeviceStore(db),
		Paramsets: sqlite.NewParamsetStore(db),
	}

	// A row written by an earlier daemon run of the same central. It is
	// seeded through the sink so the record carries the production hash.
	seed, err := central.New(central.Config{Name: "ccu-adopted"})
	if err != nil {
		t.Fatalf("central.New (seed): %v", err)
	}
	WireDescriptorPersistence(ctx, seed, stores, nil)
	seed.DescRegistry.Put(wireHmIPRF, hmproto.DeviceDescription{
		Address:  "VCU7",
		Type:     "HmIP-PS",
		Children: []string{"VCU7:1"},
	})

	// The manager the daemon hands to the adopt orchestrator: built by
	// WireCentrals with no boot centrals, so only the adopt path can wire
	// anything for "ccu-adopted".
	reg := central.NewRegistry()
	mgr, err := WireCentrals(ctx, &config.Config{}, reg, WireDeps{Descriptors: stores}, slog.Default())
	if err != nil {
		t.Fatalf("WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)

	unit, err := central.New(central.Config{Name: "ccu-adopted"})
	if err != nil {
		t.Fatalf("central.New (adopted): %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Registry.Register: %v", err)
	}
	cc := config.CentralConfig{
		Name:       "ccu-adopted",
		Host:       "127.0.0.1",
		Username:   "Admin",
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}},
	}
	if !mgr.AddCentral(&cc, unit) {
		t.Fatal("AddCentral returned false for a name the manager does not hold")
	}

	// Effect 1 — hydration: the adopted Unit starts warm.
	if desc, ok := unit.DescRegistry.Get(wireHmIPRF, "VCU7"); !ok || desc.Type != "HmIP-PS" {
		t.Fatalf("adopted central did not hydrate its description registry: got %+v ok=%v; "+
			"it will re-inventory the whole CCU over the radio on every restart", desc, ok)
	}

	// Effect 2 — mirroring: what the adopted central learns is persisted.
	unit.ParamsetReg.Add(wireHmIPRF, "VCU7:1", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"STATE": {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}, "HmIP-PS")
	rec, err := stores.Paramsets.Get(ctx, "ccu-adopted", "HmIP-RF", "VCU7:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("adopted central's paramset never reached SQLite: %v", err)
	}
	if _, ok := rec.Paramset["STATE"]; !ok {
		t.Errorf("persisted paramset missing STATE: %+v", rec.Paramset)
	}
}

// TestCentralBringUp_ClearModelDoesNotEvictPersistedValuesCache reproduces
// one half of the teardown's split contract. A device- or interface-scoped cache clear
// (cachereset.Service.Clear) deletes only the scoped persisted VALUES rows
// itself, then calls BringUpManager.ReinitCentral, which tears the whole
// central's in-memory model down via clearModel before re-pulling.
// clearModel used to remove every device through Unit.RemoveDevice, which
// fires hmevent.DeviceRemovedEvent unconditionally — and the persistent
// values-cache evictor (WireValuesCacheEviction) deletes on that event for
// every device it fires for, not just the one the operator scoped. The net
// effect was that clearModel's blast radius silently widened any scoped
// clear into a whole-central wipe of the persisted VALUES cache, exactly
// what docs/caching.md says cannot happen.
//
// The reproducer never asks for device B's cache to be cleared (no explicit
// store delete for it, mirroring what cachereset.Service.Clear does for an
// out-of-scope device) — only clearModel is invoked, exactly as reinit()
// calls it. Device B's rows must survive.
func TestCentralBringUp_ClearModelDoesNotEvictPersistedValuesCache(t *testing.T) {
	t.Parallel()

	store := freshValuesCacheStoreForAdapter(t)
	reg, unit := registryWithEventBus(t)

	const (
		centralName = "ccu-test"
		ifaceID     = "HmIP-RF"
		devA        = "DEVICE-A" // in the requested clear scope
		devB        = "DEVICE-B" // NOT in the requested clear scope
	)

	saveRowsForDevice(t, store, centralName, ifaceID, devA, []string{"STATE"})
	saveRowsForDevice(t, store, centralName, ifaceID, devB, []string{"TEMPERATURE", "HUMIDITY"})

	evictor := WireValuesCacheEviction(reg, store, nil)
	t.Cleanup(evictor.Stop)

	unit.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: ifaceID, Interface: hmenum.InterfaceHmIPRF,
		Address: devA, Model: "HmIP-PS",
	}))
	unit.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: ifaceID, Interface: hmenum.InterfaceHmIPRF,
		Address: devB, Model: "HmIP-STH",
	}))

	b := &centralBringUp{logger: slog.Default(), unit: unit}
	b.clearModel()

	if n := rowCountForDevice(t, store, centralName, ifaceID, devB); n != 2 {
		t.Errorf("device B was outside the requested clear scope, but clearModel dropped its "+
			"persisted VALUES cache rows anyway: expected 2 rows intact, got %d", n)
	}
}

// TestCentralBringUp_ClearModelStillAnnouncesRemoval pins the other half.
//
// The cache evictors must stand down on a model teardown, but every other
// consumer must not: the north-bound planes learn that a device is gone only
// from this event. Silencing it wholesale — the first attempt at the fix
// above — meant a device the CCU had genuinely dropped kept its MQTT
// discovery config, its WebSocket presence and its live subscriptions until
// the next daemon boot, because the re-pull that follows a teardown re-creates
// only the devices the CCU still reports.
//
// So: the event fires, and it carries ModelTeardown so the two evictors can
// tell the two situations apart.
func TestCentralBringUp_ClearModelStillAnnouncesRemoval(t *testing.T) {
	t.Parallel()

	reg, unit := registryWithEventBus(t)
	_ = reg

	const (
		ifaceID = "HmIP-RF"
		addr    = "DEVICE-GONE"
	)

	var mu sync.Mutex
	var seen []hmevent.DeviceRemovedEvent
	unsub := events.Subscribe(unit.EventBus, func(e hmevent.DeviceRemovedEvent) {
		mu.Lock()
		seen = append(seen, e)
		mu.Unlock()
	})
	t.Cleanup(unsub)

	unit.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: ifaceID, Interface: hmenum.InterfaceHmIPRF,
		Address: addr, Model: "HmIP-PS",
	}))

	b := &centralBringUp{logger: slog.Default(), unit: unit}
	b.clearModel()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatalf("clearModel removed %s without announcing it: every north-bound plane "+
			"(MQTT retraction, the WebSocket device-lifecycle push, the Matter bridge) "+
			"learns of a vanished device only from this event", addr)
	}
	if got := seen[0].Address; got != addr {
		t.Errorf("removal event carried address %q, want %q", got, addr)
	}
	if !seen[0].ModelTeardown {
		t.Error("the removal event must carry ModelTeardown so the persistent " +
			"VALUES/MASTER cache evictors can stand down; without it a scoped " +
			"cache clear widens into a whole-central wipe")
	}
}
