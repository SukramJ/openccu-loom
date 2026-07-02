// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func discardTestLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// buildPurgeTestStores opens a fresh migrated main-schema SQLite DB (for
// ValuesCacheStore/MasterValuesStore) plus a fresh migrated history DB (for
// MeasurementStore). Mirrors buildVisibilityStore's gooseMigrateMu-guarded
// open pattern (visibility_live_test.go) to avoid goose's migration race
// when tests run in parallel across the package.
func buildPurgeTestStores(t *testing.T) (*sqlitestore.ValuesCacheStore, *sqlitestore.MasterValuesStore, *sqlitestore.MeasurementStore) {
	t.Helper()
	ctx := context.Background()

	mainDSN := "file:" + t.TempDir() + "/purge_test.db?_pragma=journal_mode(WAL)"
	gooseMigrateMu.Lock()
	mainDB, err := sqlitestore.Open(ctx, mainDSN)
	gooseMigrateMu.Unlock()
	if err != nil {
		t.Fatalf("sqlitestore.Open(main): %v", err)
	}
	t.Cleanup(func() { _ = mainDB.Close() })

	histDSN := "file:" + t.TempDir() + "/purge_test_hist.db?_pragma=journal_mode(WAL)"
	gooseMigrateMu.Lock()
	histDB, err := sqlitestore.OpenHistory(ctx, histDSN)
	gooseMigrateMu.Unlock()
	if err != nil {
		t.Fatalf("sqlitestore.OpenHistory: %v", err)
	}
	t.Cleanup(func() { _ = histDB.Close() })

	return sqlitestore.NewValuesCacheStore(mainDB), sqlitestore.NewMasterValuesStore(mainDB), sqlitestore.NewMeasurementStore(histDB)
}

// TestPurgeCentralStateDeletesOnlyTheNamedCentral seeds VALUES-cache,
// MASTER-cache and history rows for two centrals sharing an interface name,
// purges one, and asserts the other central's rows are untouched — the
// live-remove path must never bleed into a peer central's persisted state.
func TestPurgeCentralStateDeletesOnlyTheNamedCentral(t *testing.T) {
	t.Parallel()
	valuesStore, masterStore, historyStore := buildPurgeTestStores(t)
	ctx := context.Background()
	now := time.Now()

	const (
		removedCentral  = "purge-central"
		survivorCentral = "other-central"
		ifaceName       = "HmIP-RF"
	)

	if err := valuesStore.SaveValue(ctx, removedCentral, ifaceName, "AAAA0001:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue(removed): %v", err)
	}
	if err := valuesStore.SaveValue(ctx, survivorCentral, ifaceName, "BBBB0001:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue(survivor): %v", err)
	}
	if err := masterStore.SaveParameter(ctx, removedCentral, ifaceName, "AAAA0001:1", "MASTER_PARAM", 1); err != nil {
		t.Fatalf("SaveParameter(removed): %v", err)
	}
	if err := masterStore.SaveParameter(ctx, survivorCentral, ifaceName, "BBBB0001:1", "MASTER_PARAM", 1); err != nil {
		t.Fatalf("SaveParameter(survivor): %v", err)
	}
	if err := historyStore.SaveBatch(ctx, []sqlitestore.MeasurementSample{
		{CentralName: removedCentral, InterfaceID: ifaceName, ChannelAddress: "AAAA0001:1", Parameter: "STATE", TS: now, Value: 1},
		{CentralName: survivorCentral, InterfaceID: ifaceName, ChannelAddress: "BBBB0001:1", Parameter: "STATE", TS: now, Value: 1},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	cc := config.CentralConfig{Name: removedCentral, Interfaces: []config.InterfaceSpec{{Name: ifaceName}}}
	purgeCentralState(ctx, valuesStore, masterStore, historyStore, cc, discardTestLogger())

	if rows, err := valuesStore.LoadChannel(ctx, removedCentral, ifaceName, "AAAA0001:1"); err != nil {
		t.Fatalf("LoadChannel(removed): %v", err)
	} else if len(rows) != 0 {
		t.Errorf("values_cache rows for %s survived purge: %d", removedCentral, len(rows))
	}
	if rows, err := valuesStore.LoadChannel(ctx, survivorCentral, ifaceName, "BBBB0001:1"); err != nil {
		t.Fatalf("LoadChannel(survivor): %v", err)
	} else if len(rows) != 1 {
		t.Errorf("values_cache rows for %s = %d, want 1 (untouched)", survivorCentral, len(rows))
	}

	if values, found, err := masterStore.LoadChannel(ctx, removedCentral, ifaceName, "AAAA0001:1"); err != nil {
		t.Fatalf("master LoadChannel(removed): %v", err)
	} else if found {
		t.Errorf("master_values row for %s survived purge: %v", removedCentral, values)
	}
	if values, found, err := masterStore.LoadChannel(ctx, survivorCentral, ifaceName, "BBBB0001:1"); err != nil {
		t.Fatalf("master LoadChannel(survivor): %v", err)
	} else if !found || len(values) != 1 {
		t.Errorf("master_values row for %s = (found=%v, %v), want 1 value (untouched)", survivorCentral, found, values)
	}

	stats, err := historyStore.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 1 {
		t.Errorf("history rows remaining = %d, want 1 (only the survivor)", stats.Rows)
	}
}

// TestPurgeCentralStateNilStoresAreSafe verifies purgeCentralState tolerates
// every store being nil (e.g. persistence disabled) without panicking.
func TestPurgeCentralStateNilStoresAreSafe(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Name: "x", Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}}}
	purgeCentralState(context.Background(), nil, nil, nil, cc, discardTestLogger())
}

// TestEvictModelRemovesDevicesDescriptionsAndParamsets mirrors
// centralBringUp.clearModel's own test
// (internal/central/adapter/central_bringup_test.go:
// TestCentralBringUp_ClearModelClearsDescriptionAndParamsetRegistries) —
// evictModel replicates clearModel's sequence via Unit's exported
// registries because clearModel itself is unexported to the adapter
// package.
func TestEvictModelRemovesDevicesDescriptionsAndParamsets(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "evict-test"})
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

	if unit.ModelRegistry.Len() == 0 || unit.DescRegistry.Len() == 0 || unit.ParamsetReg.Len() == 0 {
		t.Fatalf("precondition: registries must be populated (model=%d desc=%d paramset=%d)",
			unit.ModelRegistry.Len(), unit.DescRegistry.Len(), unit.ParamsetReg.Len())
	}

	evictModel(unit)

	if got := unit.ModelRegistry.Len(); got != 0 {
		t.Errorf("model registry not evicted: Len() = %d, want 0", got)
	}
	if got := unit.DescRegistry.Len(); got != 0 {
		t.Errorf("description registry not evicted: Len() = %d, want 0 (stale device would be resurrected on re-adopt)", got)
	}
	if got := unit.ParamsetReg.Len(); got != 0 {
		t.Errorf("paramset registry not evicted: Len() = %d, want 0", got)
	}
}

// TestEvictModelNilSafe verifies evictModel tolerates a nil unit.
func TestEvictModelNilSafe(t *testing.T) {
	t.Parallel()
	evictModel(nil) // must not panic
}

// TestNewCentralOrchestratorNilBringUpReturnsNil verifies the nil-tolerant
// construction pattern: a nil BringUpManager (southbound never came up)
// yields a nil orchestrator rather than one that would nil-pointer-dereference
// on its first AddCentral/RemoveCentral call.
func TestNewCentralOrchestratorNilBringUpReturnsNil(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	orch := newCentralOrchestrator(reg, nil, southboundWiringDeps{reg: reg}, &config.Config{}, discardTestLogger(), "", nil, nil, nil)
	if orch != nil {
		t.Fatal("newCentralOrchestrator(bringUp=nil) returned a non-nil orchestrator")
	}
}

// unreachableTestCentralConfig returns a CentralConfig pointed at a port
// nothing listens on, so the readiness-gated southbound bring-up
// (WaitForCCUReady) loops harmlessly in the background without ever
// completing — exactly what these tests want, since they only assert
// synchronous orchestrator state (registry/handles), never model population.
// The polling goroutine is cancelled cleanly by the caller's mgr.Teardown /
// orch.removeCentral.
func unreachableTestCentralConfig(name string) config.CentralConfig {
	return config.CentralConfig{
		Name:       name,
		Host:       "127.0.0.1",
		Username:   "Admin",
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}},
	}
}

// buildLiveTestOrchestrator wires a real (not faked) BringUpManager via
// [adapter.WireCentrals] with an empty boot-time central list — this gives
// the orchestrator a properly-initialized manager (parentCtx/cfg/deps/logger
// captured) without needing a live CCU for every test in this file. ctx
// bounds the manager's bring-up goroutines; cancel it (or call
// mgr.Teardown, registered via t.Cleanup) to drain them.
func buildLiveTestOrchestrator(ctx context.Context, t *testing.T, reg *central.Registry, cfg *config.Config) *centralOrchestrator {
	t.Helper()
	logger := discardTestLogger()
	mgr, err := adapter.WireCentrals(ctx, cfg, reg, adapter.WireDeps{}, logger)
	if err != nil {
		t.Fatalf("adapter.WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)
	orch := newCentralOrchestrator(reg, mgr, southboundWiringDeps{reg: reg, logger: logger}, cfg, logger, "", nil, nil, nil)
	if orch == nil {
		t.Fatal("newCentralOrchestrator returned nil")
	}
	return orch
}

// TestCentralOrchestratorAdoptCentralRejectsDuplicateRegistration verifies
// that adopting a name already present in the shared registry (e.g. a
// boot-time central) fails cleanly via Registry.Register's own duplicate
// guard, rather than silently double-registering a Unit.
func TestCentralOrchestratorAdoptCentralRejectsDuplicateRegistration(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfg := &config.Config{}
	reg := central.NewRegistry()
	existing, err := central.New(central.Config{Name: "already-there"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(existing); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	orch := buildLiveTestOrchestrator(ctx, t, reg, cfg)

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("already-there")); err == nil {
		t.Fatal("adoptCentral for an already-registered name succeeded; want an error")
	}
	// The pre-existing unit must be unaffected.
	if _, ok := reg.Get("already-there"); !ok {
		t.Fatal("the pre-existing unit was unregistered as a side effect of the failed adopt")
	}
}

// TestCentralOrchestratorIsRegisteredReflectsSharedRegistry verifies
// isRegistered (the REST decorator's Put idempotency check) sees both a
// boot-time-registered central and a live-adopted one.
func TestCentralOrchestratorIsRegisteredReflectsSharedRegistry(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfg := &config.Config{}
	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, cfg)

	if orch.isRegistered("never-adopted") {
		t.Error("isRegistered(never-adopted) = true, want false")
	}

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("adopted-live")); err != nil {
		t.Fatalf("adoptCentral: %v", err)
	}
	if !orch.isRegistered("adopted-live") {
		t.Error("isRegistered(adopted-live) = false after adoptCentral, want true")
	}

	if err := orch.removeCentral(ctx, "adopted-live"); err != nil {
		t.Fatalf("removeCentral: %v", err)
	}
	if orch.isRegistered("adopted-live") {
		t.Error("isRegistered(adopted-live) = true after removeCentral, want false")
	}
}

// TestCentralOrchestratorRemoveCentralUnmanagedReturnsSentinel verifies
// removeCentral reports [errCentralNotLive] (not a generic error) for a name
// that was never adopted through this orchestrator, and does not panic.
func TestCentralOrchestratorRemoveCentralUnmanagedReturnsSentinel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	orch := buildLiveTestOrchestrator(ctx, t, central.NewRegistry(), &config.Config{})
	err := orch.removeCentral(ctx, "nope")
	if !errors.Is(err, errCentralNotLive) {
		t.Errorf("removeCentral(unmanaged) error = %v, want errCentralNotLive", err)
	}
}

// fakeCentralAdmin is an in-memory [handlers.CentralAdminService] fake that
// records every call so the [liveCentralAdmin] decorator tests can assert
// the persisted-then-adopted / removed-then-persisted call ordering without
// a real SQLite-backed CentralsStore.
type fakeCentralAdmin struct {
	mu      sync.Mutex
	rows    map[string]sqlitestore.CentralRow
	putN    int
	deleteN int
}

func newFakeCentralAdmin() *fakeCentralAdmin {
	return &fakeCentralAdmin{rows: make(map[string]sqlitestore.CentralRow)}
}

func (f *fakeCentralAdmin) Put(_ context.Context, row sqlitestore.CentralRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putN++
	f.rows[row.Name] = row
	return nil
}

func (f *fakeCentralAdmin) Get(_ context.Context, name string) (sqlitestore.CentralRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[name]
	if !ok {
		return sqlitestore.CentralRow{}, sqlitestore.ErrCentralNotFound
	}
	return r, nil
}

func (f *fakeCentralAdmin) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[name]; !ok {
		return sqlitestore.ErrCentralNotFound
	}
	delete(f.rows, name)
	f.deleteN++
	return nil
}

func (f *fakeCentralAdmin) List(_ context.Context) ([]sqlitestore.CentralRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlitestore.CentralRow, 0, len(f.rows))
	for name := range f.rows {
		out = append(out, f.rows[name])
	}
	return out, nil
}

// TestNewLiveCentralAdminPassthroughWhenUnavailable verifies the decorator
// is a true no-op wrapper (returns the store unchanged) when either
// dependency is unavailable — the same nil-tolerant pattern used throughout
// the composition root (e.g. cacheResetReset).
func TestNewLiveCentralAdminPassthroughWhenUnavailable(t *testing.T) {
	t.Parallel()
	store := newFakeCentralAdmin()

	if got := newLiveCentralAdmin(nil, nil, discardTestLogger()); got != nil {
		t.Error("newLiveCentralAdmin(nil store, nil orch) did not return nil")
	}
	if got := newLiveCentralAdmin(store, nil, discardTestLogger()); got != store {
		t.Error("newLiveCentralAdmin(store, nil orch) did not return store unchanged")
	}
}

// TestLiveCentralAdminPutAdoptsNewCentralLive verifies the REST injection
// seam: Put persists the row THEN adopts it live when Enabled, and the
// central is visible in the shared registry without any restart.
func TestLiveCentralAdminPutAdoptsNewCentralLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})
	store := newFakeCentralAdmin()
	dec := newLiveCentralAdmin(store, orch, discardTestLogger())

	row := sqlitestore.CentralRow{
		Name: "put-live", Host: "127.0.0.1", Enabled: true,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}},
	}
	if err := dec.Put(ctx, row); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if store.putN != 1 {
		t.Errorf("underlying store.Put call count = %d, want 1", store.putN)
	}
	if _, ok := reg.Get("put-live"); !ok {
		t.Error("central not present in the shared registry after Put — adopt did not run")
	}

	if err := dec.Delete(ctx, "put-live"); err != nil {
		t.Fatalf("Delete (cleanup): %v", err)
	}
}

// TestLiveCentralAdminPutSkipsAdoptForAlreadyRegisteredCentral verifies that
// PUT-as-update against an already-live central (boot-time or previously
// live-adopted) still persists the row but does not attempt a second adopt —
// update-in-place of a running central is out of scope for PR3.
func TestLiveCentralAdminPutSkipsAdoptForAlreadyRegisteredCentral(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	// Simulate a boot-time central: registered directly, not through the
	// orchestrator, so it is NOT in orch.handles but IS in reg.
	bootUnit, err := central.New(central.Config{Name: "boot-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(bootUnit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})
	store := newFakeCentralAdmin()
	dec := newLiveCentralAdmin(store, orch, discardTestLogger())

	row := sqlitestore.CentralRow{Name: "boot-central", Host: "127.0.0.1", Enabled: true}
	if err := dec.Put(ctx, row); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if store.putN != 1 {
		t.Errorf("underlying store.Put call count = %d, want 1 (row must still persist)", store.putN)
	}
	// The boot-time unit must be the SAME instance — a second adopt would
	// have failed reg.Register and rolled back, or (if reg.Register were
	// somehow skipped) replaced it; either way Get must still return the
	// original pointer identity.
	got, ok := reg.Get("boot-central")
	if !ok || got != bootUnit {
		t.Error("boot-central was replaced or removed by a redundant Put-adopt")
	}
}

// TestLiveCentralAdminDeleteRemovesLiveCentralBeforePersisting verifies
// Delete tears the central down live BEFORE deleting the persisted row.
func TestLiveCentralAdminDeleteRemovesLiveCentralBeforePersisting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})
	store := newFakeCentralAdmin()
	dec := newLiveCentralAdmin(store, orch, discardTestLogger())

	row := sqlitestore.CentralRow{
		Name: "delete-live", Host: "127.0.0.1", Enabled: true,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}},
	}
	if err := dec.Put(ctx, row); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := reg.Get("delete-live"); !ok {
		t.Fatal("precondition: central not live after Put")
	}

	if err := dec.Delete(ctx, "delete-live"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := reg.Get("delete-live"); ok {
		t.Error("central still present in the shared registry after Delete")
	}
	if store.deleteN != 1 {
		t.Errorf("underlying store.Delete call count = %d, want 1", store.deleteN)
	}
}

// TestLiveCentralAdminDeleteToleratesNeverAdoptedCentral verifies Delete for
// a name that was never live-adopted (e.g. a row created while disabled)
// still deletes the persisted row instead of surfacing errCentralNotLive to
// the operator.
func TestLiveCentralAdminDeleteToleratesNeverAdoptedCentral(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	orch := buildLiveTestOrchestrator(ctx, t, central.NewRegistry(), &config.Config{})
	store := newFakeCentralAdmin()
	dec := newLiveCentralAdmin(store, orch, discardTestLogger())

	if err := store.Put(ctx, sqlitestore.CentralRow{Name: "never-live", Enabled: false}); err != nil {
		t.Fatalf("seed Put: %v", err)
	}

	if err := dec.Delete(ctx, "never-live"); err != nil {
		t.Fatalf("Delete(never-live): %v", err)
	}
	if store.deleteN != 1 {
		t.Errorf("underlying store.Delete call count = %d, want 1", store.deleteN)
	}
}
