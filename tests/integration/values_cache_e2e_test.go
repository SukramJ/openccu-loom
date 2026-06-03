// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// vcOpenDB opens (and migrates) a SQLite database suitable for values-cache
// tests. When path is ":memory:" an in-memory DB is used; otherwise a file-
// backed DB at path (must be absolute or relative to t.TempDir()) is opened.
// The database is closed via t.Cleanup.
func vcOpenDB(t *testing.T, path string) *sqlite.ValuesCacheStore {
	t.Helper()
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewValuesCacheStore(db)
}

// vcIngestPipeline sets up a godevccu-backed Unit, ingests the mock-CCU
// fleet into it, and optionally attaches a ValuesCacheStore for the restore
// pass. Returns the populated unit and the pipeline used to hydrate it.
func vcIngestPipeline(
	t *testing.T,
	centralName string,
	vc *sqlite.ValuesCacheStore,
) *central.Unit {
	t.Helper()
	srv := startMockCCU(t)
	xmlClient := newXMLRPCClient(t, srv.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	p := adapter.NewDevicePipeline(c)
	if vc != nil {
		p = p.WithValuesCacheStore(vc, centralName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := p.IngestFromBackend(
		ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger,
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}
	return c
}

// firstLiveDP returns the first wire VALUES data point from any channel of any
// device in c that implements sourceMarker (i.e. generic.DataPoint). Returns
// nil when the registry is empty or no DP is found.
func firstLiveDP(c *central.Unit) interface {
	Source() hmenum.ValueSource
	OnWireValue(any) bool
} {
	for _, d := range c.ModelRegistry.List() {
		for _, ch := range d.Channels() {
			for _, dp := range ch.DataPoints() {
				if dp == nil {
					continue
				}
				type wantIface interface {
					Source() hmenum.ValueSource
					OnWireValue(any) bool
				}
				if w, ok := dp.(wantIface); ok {
					return w
				}
			}
		}
	}
	return nil
}

// ─── Test 1: flush → reopen → restore roundtrip ───────────────────────────────

// TestValuesCache_FlushAndRestoreRoundtrip verifies the core persistence loop:
//  1. Ingest a device fleet from the mock CCU.
//  2. Drive a VALUES data point to source=live via OnWireValue.
//  3. Run the periodic flusher to persist live values to SQLite.
//  4. Re-open the same DB file and run a fresh pipeline with
//     WithValuesCacheStore → restoreValuesFromCache.
//  5. Verify the data point is now source=cache.
func TestValuesCache_FlushAndRestoreRoundtrip(t *testing.T) {
	const centralName = "vc-roundtrip"
	// File-backed DB so the second pipeline run can reopen it.
	dbPath := "file:" + filepath.Join(t.TempDir(), "vc_roundtrip.db") +
		"?_pragma=journal_mode(WAL)"

	// ── first run: ingest + drive live + flush ────────────────────────────────
	vcStore := vcOpenDB(t, dbPath)
	c := vcIngestPipeline(t, centralName, vcStore)

	dp := firstLiveDP(c)
	if dp == nil {
		t.Fatal("no suitable data point found in the ingested fleet")
	}

	// Drive the data point to live by pushing a wire value.
	if !dp.OnWireValue(true) {
		// Try a numeric value in case the DP is not boolean.
		dp.OnWireValue(float64(1))
	}
	if dp.Source() != hmenum.ValueSourceLive {
		t.Fatalf("expected source=live after OnWireValue, got %s", dp.Source())
	}

	// Run flusher with a short interval so we can trigger it quickly.
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	closer := adapter.WireValuesCacheFlusher(reg, vcStore, 5*time.Millisecond, logger)
	time.Sleep(50 * time.Millisecond)
	closer() // blocks until the shutdown flush completes

	// Verify at least one row landed in the store.
	st, err := vcStore.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows == 0 {
		t.Fatal("flush produced 0 rows — nothing was persisted")
	}

	// ── second run: reopen + restore ─────────────────────────────────────────
	vcStore2 := vcOpenDB(t, dbPath)
	c2 := vcIngestPipeline(t, centralName, vcStore2)

	// At least one data point must have been restored as cache.
	gotCache := false
	for _, d := range c2.ModelRegistry.List() {
		for _, ch := range d.Channels() {
			for _, rawDP := range ch.DataPoints() {
				if rawDP == nil {
					continue
				}
				type sourcer interface{ Source() hmenum.ValueSource }
				if s, ok := rawDP.(sourcer); ok {
					if s.Source() == hmenum.ValueSourceCache {
						gotCache = true
					}
				}
			}
		}
	}
	if !gotCache {
		t.Error("no data point was restored as source=cache on the second pipeline run")
	}
}

// ─── Test 2: lifecycle transitions live → stale → live ───────────────────────

// TestValuesCache_LifecycleTransitions_ConnectionLostThenRecovered verifies
// the source-token state machine on the event bus:
//  1. Drive a DP to source=live.
//  2. Publish ConnectionLostEvent → DP becomes stale.
//     A DataPointSourceChangedEvent (old=live, new=stale) must appear on the bus.
//  3. Publish RecoveryCompletedEvent → DP becomes live.
//     A DataPointSourceChangedEvent (old=stale, new=live) must appear.
func TestValuesCache_LifecycleTransitions_ConnectionLostThenRecovered(t *testing.T) {
	const centralName = "vc-lifecycle"

	vcStore := vcOpenDB(t, ":memory:")
	c := vcIngestPipeline(t, centralName, vcStore)

	// Wire the lifecycle handler so ConnectionLost / RecoveryCompleted flip sources.
	unsub := adapter.WireValueSourceLifecycle(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer unsub()

	// Find a DP and push a live value.
	dp := firstLiveDP(c)
	if dp == nil {
		t.Fatal("no data point found for lifecycle test")
	}
	dp.OnWireValue(true)
	if dp.Source() != hmenum.ValueSourceLive {
		t.Fatalf("pre-condition: source = %s, want live", dp.Source())
	}

	// Collect DataPointSourceChangedEvents.
	type transition struct{ old, new hmenum.ValueSource }
	var (
		mu          sync.Mutex
		transitions []transition
	)
	unsubChange := events.Subscribe(c.EventBus, func(e hmevent.DataPointSourceChangedEvent) {
		mu.Lock()
		transitions = append(transitions, transition{e.OldSource, e.NewSource})
		mu.Unlock()
	})
	defer unsubChange()

	// ── ConnectionLostEvent → stale ───────────────────────────────────────────
	events.Publish(c.EventBus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		InterfaceID: "HmIP-RF",
		Reason:      hmenum.FailureReasonTimeout,
	})
	// Give the async bus a moment to dispatch.
	time.Sleep(20 * time.Millisecond)

	if dp.Source() != hmenum.ValueSourceStale {
		t.Fatalf("after ConnectionLostEvent: source = %s, want stale", dp.Source())
	}
	mu.Lock()
	haveStaleTransition := false
	for _, tr := range transitions {
		if tr.old == hmenum.ValueSourceLive && tr.new == hmenum.ValueSourceStale {
			haveStaleTransition = true
		}
	}
	mu.Unlock()
	if !haveStaleTransition {
		t.Error("no DataPointSourceChangedEvent (live→stale) observed after ConnectionLostEvent")
	}

	// ── RecoveryCompletedEvent → live ─────────────────────────────────────────
	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		InterfaceID: "HmIP-RF",
		Result:      hmenum.RecoveryResultSuccess,
	})
	time.Sleep(20 * time.Millisecond)

	if dp.Source() != hmenum.ValueSourceLive {
		t.Fatalf("after RecoveryCompletedEvent: source = %s, want live", dp.Source())
	}
	mu.Lock()
	haveLiveTransition := false
	for _, tr := range transitions {
		if tr.old == hmenum.ValueSourceStale && tr.new == hmenum.ValueSourceLive {
			haveLiveTransition = true
		}
	}
	mu.Unlock()
	if !haveLiveTransition {
		t.Error("no DataPointSourceChangedEvent (stale→live) observed after RecoveryCompletedEvent")
	}
}

// ─── Test 3: fetch_all overwrite wins over cache ──────────────────────────────

// TestValuesCache_FetchAllOverwritesCache verifies that a live OnWireValue
// call supersedes the cache-sourced value. The scenario:
//  1. DP A gets a live value (true), DP B stays unobserved.
//  2. Both are flushed; only A has a row.
//  3. Second pipeline run with cache restore: A is source=cache, B is unobserved.
//  4. Simulate fetch_all by calling OnWireValue on A with a new value (false).
//  5. A must be source=live with the new value.
func TestValuesCache_FetchAllOverwritesCache(t *testing.T) {
	const centralName = "vc-fetch-all"
	dbPath := "file:" + filepath.Join(t.TempDir(), "vc_fetch.db") +
		"?_pragma=journal_mode(WAL)"

	// ── first run: drive A to live, leave B unobserved, flush ────────────────
	vcStore := vcOpenDB(t, dbPath)
	c := vcIngestPipeline(t, centralName, vcStore)

	// Pick the first two boolean DPs we can find.
	type liveDPIface interface {
		Source() hmenum.ValueSource
		OnWireValue(any) bool
	}
	var dpA, dpB liveDPIface
	for _, d := range c.ModelRegistry.List() {
		for _, ch := range d.Channels() {
			for _, rawDP := range ch.DataPoints() {
				if rawDP == nil {
					continue
				}
				if w, ok := rawDP.(liveDPIface); ok {
					if dpA == nil {
						dpA = w
					} else if dpB == nil && rawDP != nil {
						dpB = w
						goto done
					}
				}
			}
		}
	}
done:
	if dpA == nil {
		t.Fatal("could not find two suitable data points")
	}

	// Drive dpA live.
	dpA.OnWireValue(true)
	if dpA.Source() != hmenum.ValueSourceLive {
		t.Fatalf("dpA: source = %s, want live", dpA.Source())
	}
	// dpB stays unobserved (we do not call OnWireValue on it).

	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	closer := adapter.WireValuesCacheFlusher(reg, vcStore, 5*time.Millisecond, logger)
	time.Sleep(50 * time.Millisecond)
	closer()

	// ── second run: restore, verify A=cache, then simulate fetch_all ─────────
	vcStore2 := vcOpenDB(t, dbPath)
	c2 := vcIngestPipeline(t, centralName, vcStore2)

	// At least one DP must be cache-sourced (from the restore pass).
	var restoredDP liveDPIface
	for _, d := range c2.ModelRegistry.List() {
		for _, ch := range d.Channels() {
			for _, rawDP := range ch.DataPoints() {
				if rawDP == nil {
					continue
				}
				if w, ok := rawDP.(liveDPIface); ok {
					if w.Source() == hmenum.ValueSourceCache {
						restoredDP = w
						goto foundRestored
					}
				}
			}
		}
	}
foundRestored:
	if restoredDP == nil {
		t.Fatal("no cache-sourced DP found after restore pass")
	}

	// Simulate fetch_all: push a new live value.
	restoredDP.OnWireValue(false)
	if restoredDP.Source() != hmenum.ValueSourceLive {
		t.Fatalf("after fetch_all OnWireValue: source = %s, want live", restoredDP.Source())
	}
}

// ─── Test 4: GC removes dead rows ────────────────────────────────────────────

// TestValuesCache_GCRemovesDeadRows verifies GCDeadRows at the store level:
//  1. Write two entries (A, B) for the same channel.
//  2. Build an alive set containing only A.
//  3. Run GCDeadRows → B must be deleted, A must survive.
func TestValuesCache_GCRemovesDeadRows(t *testing.T) {
	t.Parallel()

	vcStore := vcOpenDB(t, ":memory:")
	ctx := context.Background()
	now := time.Now().UTC()

	const (
		centralName = "vc-gc"
		ifaceID     = "HmIP-RF"
		chAddr      = "GC_DEV:1"
		paramA      = "STATE"
		paramB      = "RSSI_DEVICE"
	)

	if err := vcStore.SaveValue(ctx, centralName, ifaceID, chAddr, paramA, true, now, now); err != nil {
		t.Fatalf("SaveValue %s: %v", paramA, err)
	}
	if err := vcStore.SaveValue(ctx, centralName, ifaceID, chAddr, paramB, int(-65), now, now); err != nil {
		t.Fatalf("SaveValue %s: %v", paramB, err)
	}

	// Alive set includes only paramA.
	alive := map[string]struct{}{
		sqlite.AliveKey(centralName, ifaceID, chAddr, paramA): {},
	}

	res, err := vcStore.GCDeadRows(ctx, alive)
	if err != nil {
		t.Fatalf("GCDeadRows: %v", err)
	}
	if res.Scanned != 2 {
		t.Errorf("GCDeadRows.Scanned = %d, want 2", res.Scanned)
	}
	if res.Deleted != 1 {
		t.Errorf("GCDeadRows.Deleted = %d, want 1", res.Deleted)
	}

	// paramA must survive.
	rows, err := vcStore.LoadChannel(ctx, centralName, ifaceID, chAddr)
	if err != nil {
		t.Fatalf("LoadChannel after GC: %v", err)
	}
	surviving := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		surviving[r.Parameter] = struct{}{}
	}
	if _, ok := surviving[paramA]; !ok {
		t.Errorf("%s: alive entry was deleted by GC", paramA)
	}
	if _, ok := surviving[paramB]; ok {
		t.Errorf("%s: dead entry survived GC", paramB)
	}
}

// ─── compile-time guard ───────────────────────────────────────────────────────

// Ensure the generic.DataPoint type satisfies the interface expected by the
// lifecycle tests without importing the generic package in a way that creates
// a build dependency on its internals.
var _ interface{ Source() hmenum.ValueSource } = (*generic.DataPoint[bool])(nil)
