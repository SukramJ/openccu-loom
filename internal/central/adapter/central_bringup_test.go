// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
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
// in-memory device-description and paramset entries, not just the model object.
// Otherwise the re-pull's CheckAndCreateDevicesFromCache re-materialises a
// device the CCU no longer reports from its stale description, so a device
// removed on the CCU would survive a cache clear + re-pull.
func TestCentralBringUp_ClearModelClearsDescriptionAndParamsetRegistries(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "ccu-test"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "AAAA0001", Model: "HmIP-STH", Name: "Sensor",
	})
	d.AddChannel("AAAA0001:1", 1, "MAINTENANCE", hmenum.ParamsetKeyValues)
	unit.ModelRegistry.Put(d)
	unit.DescRegistry.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "AAAA0001"})
	unit.ParamsetReg.Add(hmenum.InterfaceHmIPRF, "AAAA0001:1", hmenum.ParamsetKeyValues,
		hmproto.Paramset{"STATE": {Type: hmenum.ParameterTypeBool}}, "HmIP-STH")

	if unit.DescRegistry.Len() == 0 || unit.ParamsetReg.Len() == 0 {
		t.Fatalf("precondition: registries must be populated (desc=%d paramset=%d)",
			unit.DescRegistry.Len(), unit.ParamsetReg.Len())
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
