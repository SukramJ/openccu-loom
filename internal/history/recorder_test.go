// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package history

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// openHistoryMu serialises sqlite.OpenHistory calls in this package's
// tests. The goose migration step inside OpenHistory writes package-level
// globals that are not concurrency-safe when multiple callers race.
var openHistoryMu sync.Mutex

// openStore opens a fresh file-backed measurement store for one test.
// Not safe to call in parallel goroutines — always call under openHistoryMu.
func openStore(t *testing.T) *sqlite.MeasurementStore {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "hist.db") + "?_pragma=journal_mode(WAL)"
	openHistoryMu.Lock()
	db, err := sqlite.OpenHistory(context.Background(), dsn)
	openHistoryMu.Unlock()
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewMeasurementStore(db)
}

// centralWithDevice builds a ready central.Unit + central.Registry containing
// one device with one channel. Returns (registry, unit, device, channel).
func centralWithDevice(t *testing.T, centralName, devAddr, chanAddr string) (
	*central.Registry, *central.Unit, *device.Device, *device.Channel,
) {
	t.Helper()
	u, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(u); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     devAddr,
		Model:       "HmIP-STH",
		Name:        "TestDev",
	})
	ch := d.AddChannel(chanAddr, 1, "CLIMATE", hmenum.ParamsetKeyValues)
	u.ModelRegistry.Put(d)
	return reg, u, d, ch
}

// publishValueEvent fires a DataPointValueChangedEvent on unit u's bus.
func publishValueEvent(u *central.Unit, chanAddr, param string, psk hmenum.ParamsetKey, newVal hmtypes.ParamValue) {
	events.Publish(u.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: chanAddr,
			ParamsetKey:    psk,
			Parameter:      param,
		},
		NewValue: newVal,
	})
}

// ============================================================
// allow()
// ============================================================

func TestAllow_ExcludeWinsOverInclude(t *testing.T) {
	t.Parallel()
	if allow("POWER", []string{"POWER"}, []string{"POWER"}) {
		t.Error("allow should return false when parameter is in both include and exclude (exclude wins)")
	}
}

func TestAllow_EmptyIncludeAllowsEverything(t *testing.T) {
	t.Parallel()
	if !allow("ANYTHING", nil, nil) {
		t.Error("allow with empty include and exclude should return true")
	}
	// not in exclude → allowed
	if !allow("ANYTHING", nil, []string{"OTHER"}) {
		t.Error("allow should return true when param is not matched by any exclude pattern")
	}
	// matched by exclude → denied
	if allow("OTHER", nil, []string{"OTHER"}) {
		t.Error("allow should return false when param is matched by exclude pattern")
	}
}

func TestAllow_GlobPatterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		param   string
		include []string
		want    bool
	}{
		{"ENERGY_POWER", []string{"*POWER*"}, true},
		{"POWER_FACTOR", []string{"*POWER*"}, true},
		{"ACTUAL_HUMIDITY", []string{"ACTUAL_*"}, true},
		{"ACTUAL_TEMPERATURE", []string{"ACTUAL_*"}, true},
		{"TEMPERATURE", []string{"TEMPERATURE"}, true},
		// non-matching include → rejected
		{"HUMIDITY", []string{"TEMPERATURE"}, false},
		{"OTHER", []string{"ACTUAL_*"}, false},
	}
	for _, tc := range cases {
		got := allow(tc.param, tc.include, nil)
		if got != tc.want {
			t.Errorf("allow(%q, include=%v) = %v, want %v", tc.param, tc.include, got, tc.want)
		}
	}
}

// ============================================================
// numericValue()
// ============================================================

func TestNumericValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		v       hmtypes.ParamValue
		wantVal float64
		wantOK  bool
	}{
		{"int", hmtypes.IntValue(5), 5.0, true},
		{"float", hmtypes.FloatValue(1.5), 1.5, true},
		{"bool", hmtypes.BoolValue(true), 0, false},
		{"string", hmtypes.StringValue("x"), 0, false},
		{"none", hmtypes.ParamValue{}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := numericValue(tc.v)
			if ok != tc.wantOK {
				t.Errorf("numericValue(%v) ok=%v, want %v", tc.v.Kind, ok, tc.wantOK)
			}
			if ok && got != tc.wantVal {
				t.Errorf("numericValue(%v) = %v, want %v", tc.v.Kind, got, tc.wantVal)
			}
		})
	}
}

// ============================================================
// deviceAddressOf()
// ============================================================

func TestDeviceAddressOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"ABC123:4", "ABC123"},
		{"ABC123", "ABC123"},
		{"DEV:0", "DEV"},
		{"LONGDEV:12", "LONGDEV"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := deviceAddressOf(tc.in)
			if got != tc.want {
				t.Errorf("deviceAddressOf(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ============================================================
// enqueue / drain / drop-oldest
// ============================================================

func TestEnqueueDropOldest(t *testing.T) {
	t.Parallel()
	r := New(nil, Options{MaxBuffer: 2})

	r.enqueue(sqlite.MeasurementSample{Value: 1.0})
	r.enqueue(sqlite.MeasurementSample{Value: 2.0})
	r.enqueue(sqlite.MeasurementSample{Value: 3.0}) // triggers drop of oldest (1.0)

	if got := r.Metrics().Dropped; got != 1 {
		t.Errorf("Dropped = %d, want 1", got)
	}
	drained := r.drain()
	if len(drained) != 2 {
		t.Fatalf("drain returned %d items, want 2", len(drained))
	}
	if drained[0].Value != 2.0 || drained[1].Value != 3.0 {
		t.Errorf("drain values = [%v, %v], want [2.0, 3.0]", drained[0].Value, drained[1].Value)
	}
}

// ============================================================
// Flush roundtrip
// ============================================================

func TestFlushRoundtrip(t *testing.T) {
	// Do not run in parallel — openStore uses openHistoryMu and calls OpenHistory.
	store := openStore(t)
	ctx := context.Background()

	r := New(store, Options{})
	now := time.Now()
	r.enqueue(sqlite.MeasurementSample{CentralName: "c1", InterfaceID: "HmIP-RF", ChannelAddress: "DEV:1", Parameter: "TEMP", TS: now, Value: 21.5})
	r.enqueue(sqlite.MeasurementSample{CentralName: "c1", InterfaceID: "HmIP-RF", ChannelAddress: "DEV:1", Parameter: "TEMP", TS: now.Add(time.Second), Value: 22.0})

	r.Flush(ctx)

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 2 {
		t.Errorf("Stats.Rows = %d, want 2", stats.Rows)
	}
	if got := r.Metrics().Recorded; got != 2 {
		t.Errorf("Metrics.Recorded = %d, want 2", got)
	}
}

// ============================================================
// Wire nil-safety
// ============================================================

func TestWireNilSafety(t *testing.T) {
	t.Parallel()
	// nil receiver
	var nilRec *Recorder
	stop := nilRec.Wire(nil)
	if stop == nil {
		t.Fatal("Wire on nil *Recorder returned nil closer")
	}
	stop() // must not panic

	// nil store
	r := New(nil, Options{})
	stop2 := r.Wire(nil)
	if stop2 == nil {
		t.Fatal("Wire with nil store returned nil closer")
	}
	stop2() // must not panic

	// WireCentral shares the guards and must tolerate the same nils.
	if unwire := nilRec.WireCentral(nil); unwire != nil {
		t.Error("WireCentral on a nil *Recorder returned a non-nil unwire")
	}
	if unwire := r.WireCentral(nil); unwire != nil {
		t.Error("WireCentral with a nil store returned a non-nil unwire")
	}
}

// TestWireCentralRecordsACentralThatAppearedAfterWire pins the entry point the
// live-adopt path uses: Wire walks the registry exactly once, so a CCU adopted
// at runtime recorded no measurement history at all and its charts stayed
// permanently empty — which reads exactly like a CCU whose data points never
// change.
func TestWireCentralRecordsACentralThatAppearedAfterWire(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	// Wire against an empty registry: the boot-time walk sees nothing.
	r := New(store, Options{})
	stop := r.Wire(central.NewRegistry())
	t.Cleanup(stop)

	_, u, _, ch := centralWithDevice(t, "adopted", "DEV009", "DEV009:1")
	dp := floatDP("DEV009:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(19.5) // source → live

	// Without the per-central attach the event below reaches no subscriber.
	publishValueEvent(u, "DEV009:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(19.5))
	if stats, err := store.Stats(ctx); err != nil {
		t.Fatalf("Stats: %v", err)
	} else if stats.Rows != 0 {
		t.Fatalf("rows before WireCentral = %d, want 0 (the assertion below would be vacuous)", stats.Rows)
	}

	unwire := r.WireCentral(u)
	if unwire == nil {
		t.Fatal("WireCentral returned a nil unwire for a recordable central")
	}

	dp.OnEvent(20.5)
	publishValueEvent(u, "DEV009:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(20.5))
	stop()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 1 {
		t.Errorf("Rows = %d, want 1 (the adopted central's value must be recorded)", stats.Rows)
	}

	unwire()
}

// ============================================================
// Provenance guard tests
// ============================================================

// floatDP creates a new float64 DataPoint and registers it on ch.
func floatDP(chanAddr, param string) *generic.DataPoint[float64] {
	return generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: chanAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// boolDP creates a new bool DataPoint for a channel address.
func boolDP(chanAddr, param string) *generic.DataPoint[bool] {
	return generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: chanAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// TestProvenanceGuard_LiveValueRecorded: DP source=live → 1 row persisted.
func TestProvenanceGuard_LiveValueRecorded(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV001", "DEV001:1")

	dp := floatDP("DEV001:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(21.5) // source → live

	r := New(store, Options{})
	stop := r.Wire(reg)

	publishValueEvent(u, "DEV001:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))

	stop()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 1 {
		t.Errorf("Rows = %d, want 1 (live value must be recorded)", stats.Rows)
	}
}

// TestProvenanceGuard_LiveZeroRecorded: a genuine live 0.0 must be stored,
// not filtered out by any value-magnitude check.
func TestProvenanceGuard_LiveZeroRecorded(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV002", "DEV002:1")

	dp := floatDP("DEV002:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(0.0) // source → live with zero value

	r := New(store, Options{})
	stop := r.Wire(reg)

	publishValueEvent(u, "DEV002:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(0.0))

	stop()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 1 {
		t.Errorf("Rows = %d, want 1 (live 0.0 must be recorded)", stats.Rows)
	}
}

// TestProvenanceGuard_CacheValueSkipped: DP source=cache (boot replay) → 0 rows.
func TestProvenanceGuard_CacheValueSkipped(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV003", "DEV003:1")

	dp := floatDP("DEV003:1", "TEMPERATURE")
	ch.Put(dp)
	dp.RestoreCachedValue(21.5, time.Now(), time.Now()) // source → cache

	r := New(store, Options{})
	stop := r.Wire(reg)

	publishValueEvent(u, "DEV003:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))

	stop()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 0 {
		t.Errorf("Rows = %d, want 0 (cache-sourced value must not be recorded)", stats.Rows)
	}
}

// TestProvenanceGuard_UnobservedValueSkipped: DP never had a value (source=unobserved) → 0 rows.
func TestProvenanceGuard_UnobservedValueSkipped(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV004", "DEV004:1")

	dp := floatDP("DEV004:1", "TEMPERATURE")
	ch.Put(dp)
	// do not call OnEvent or RestoreCachedValue → source remains unobserved

	r := New(store, Options{})
	stop := r.Wire(reg)

	publishValueEvent(u, "DEV004:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))

	stop()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 0 {
		t.Errorf("Rows = %d, want 0 (unobserved DP must not be recorded)", stats.Rows)
	}
}

// TestProvenanceGuard_BoolLiveValueSkipped: bool DP is live but not numeric → 0 rows.
func TestProvenanceGuard_BoolLiveValueSkipped(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV005", "DEV005:1")

	dp := boolDP("DEV005:1", "STATE")
	ch.Put(dp)
	dp.OnEvent(true) // source → live

	r := New(store, Options{})
	stop := r.Wire(reg)

	publishValueEvent(u, "DEV005:1", "STATE", hmenum.ParamsetKeyValues, hmtypes.BoolValue(true))

	stop()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 0 {
		t.Errorf("Rows = %d, want 0 (bool value must not be recorded)", stats.Rows)
	}
}

// TestProvenanceGuard_MasterParamsetSkipped: VALUES not used → 0 rows.
func TestProvenanceGuard_MasterParamsetSkipped(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV006", "DEV006:1")

	dp := floatDP("DEV006:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(21.5) // source → live

	r := New(store, Options{})
	stop := r.Wire(reg)

	// publish with MASTER paramset — should be ignored
	publishValueEvent(u, "DEV006:1", "TEMPERATURE", hmenum.ParamsetKeyMaster, hmtypes.FloatValue(21.5))

	stop()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 0 {
		t.Errorf("Rows = %d, want 0 (MASTER paramset must not be recorded)", stats.Rows)
	}
}

// TestProvenanceGuard_DeviceNotInRegistry: DP address unknown → 0 rows.
func TestProvenanceGuard_DeviceNotInRegistry(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, _ := centralWithDevice(t, "ccu-01", "DEV007", "DEV007:1")

	r := New(store, Options{})
	stop := r.Wire(reg)

	// publish for an address that has no device in the registry
	publishValueEvent(u, "UNKNOWN:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))

	stop()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 0 {
		t.Errorf("Rows = %d, want 0 (unknown device must not be recorded)", stats.Rows)
	}
}

// ============================================================
// rollup() — the hourly/daily fold that must run before purge()
// ============================================================

// TestRollup_FoldsRawIntoHourlyTier verifies that Recorder.rollup folds a
// raw sample older than rollupHourlyLag into the hourly rollup tier. Driven
// directly against a store with seeded raw rows (rather than through the
// ticker-based loop), which is sufficient to prove the fold happens and is
// far less flaky than asserting on wall-clock ticker timing.
func TestRollup_FoldsRawIntoHourlyTier(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	old := time.Now().Add(-2 * time.Hour)
	if err := store.SaveBatch(ctx, []sqlite.MeasurementSample{
		{CentralName: "ccu-01", InterfaceID: "HmIP-RF", ChannelAddress: "DEV008:1", Parameter: "POWER", TS: old, Value: 42},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	r := New(store, Options{})
	r.rollup(ctx)

	var hourlyRows int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM measurements_hourly`).Scan(&hourlyRows); err != nil {
		t.Fatalf("count measurements_hourly: %v", err)
	}
	if hourlyRows != 1 {
		t.Errorf("measurements_hourly rows = %d, want 1", hourlyRows)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 1 {
		t.Errorf("raw rows after rollup = %d, want 1 (rollup never deletes raw rows)", stats.Rows)
	}
}

// TestRollup_RunsBeforePurge_RawRowSurvivesFold verifies the ordering
// contract at the center of the rollup design: calling rollup(ctx) then
// purge(ctx) — the same sequence the loop's shared ticker case uses — must
// fold a raw row into the hourly tier before purge deletes it from the raw
// table. If purge ran first, the row's energy would be lost with nothing
// in the hourly tier to show for it.
func TestRollup_RunsBeforePurge_RawRowSurvivesFold(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	// Older than both the 1h rollup lag and the recorder's retention, so
	// a rollup-after-purge bug would show up as a raw row disappearing
	// with no corresponding hourly row.
	old := time.Now().Add(-2 * time.Hour)
	if err := store.SaveBatch(ctx, []sqlite.MeasurementSample{
		{CentralName: "ccu-01", InterfaceID: "HmIP-RF", ChannelAddress: "DEV009:1", Parameter: "ENERGY_COUNTER", TS: old, Value: 7},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	r := New(store, Options{Retention: time.Hour})
	r.rollup(ctx)
	r.purge(ctx)

	var hourlyRows int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM measurements_hourly`).Scan(&hourlyRows); err != nil {
		t.Fatalf("count measurements_hourly: %v", err)
	}
	if hourlyRows != 1 {
		t.Fatalf("measurements_hourly rows = %d, want 1 (rollup must have run before purge)", hourlyRows)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Rows != 0 {
		t.Errorf("raw rows after purge = %d, want 0 (purge should have removed the folded row)", stats.Rows)
	}
}

// TestRollup_DailyRetentionZero_KeepsDailyForever verifies that
// RetentionDaily <= 0 (the documented "keep forever" sentinel) skips the
// daily-tier delete: a daily row older than any plausible cutoff must
// survive a rollup() call when RetentionDaily is left at its zero value.
func TestRollup_DailyRetentionZero_KeepsDailyForever(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	veryOld := time.Now().Add(-365 * 24 * time.Hour)
	if err := store.SaveBatch(ctx, []sqlite.MeasurementSample{
		{CentralName: "ccu-01", InterfaceID: "HmIP-RF", ChannelAddress: "DEV010:1", Parameter: "POWER", TS: veryOld, Value: 1},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := store.RollupHourly(ctx, veryOld.Add(time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := store.RollupDaily(ctx, veryOld.Add(24*time.Hour)); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	r := New(store, Options{RetentionHourly: time.Hour}) // RetentionDaily left at zero
	r.rollup(ctx)

	var dailyRows int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM measurements_daily`).Scan(&dailyRows); err != nil {
		t.Fatalf("count measurements_daily: %v", err)
	}
	if dailyRows != 1 {
		t.Errorf("measurements_daily rows = %d, want 1 (RetentionDaily<=0 must never purge the daily tier)", dailyRows)
	}
}

// TestFlush_RequeuesOnErrorNoSilentDrop verifies the no-silent-drop fix: a
// flush that fails to persist re-queues the batch (bounded by maxBuffer) and
// bumps the flush-error metric instead of dropping the samples. A subsequent
// flush against a healthy store then persists them, so a transient write
// error costs a retry, not lost live samples.
func TestFlush_RequeuesOnErrorNoSilentDrop(t *testing.T) {
	// A store over a closed DB: SaveBatch fails ("database is closed").
	dsn := "file:" + filepath.Join(t.TempDir(), "hist.db") + "?_pragma=journal_mode(WAL)"
	openHistoryMu.Lock()
	db, err := sqlite.OpenHistory(context.Background(), dsn)
	openHistoryMu.Unlock()
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	store := sqlite.NewMeasurementStore(db)

	rec := New(store, Options{MaxBuffer: 8})
	rec.enqueue(sqlite.MeasurementSample{
		CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "DEV:1",
		Parameter: "TEMP", TS: time.Now(), Value: 21.5,
	})

	// Force the failure path.
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	rec.flush(context.Background())

	m := rec.Metrics()
	if m.FlushErrors != 1 {
		t.Errorf("FlushErrors = %d, want 1", m.FlushErrors)
	}
	if m.Recorded != 0 {
		t.Errorf("Recorded = %d, want 0 (nothing persisted)", m.Recorded)
	}
	if m.Dropped != 0 {
		t.Errorf("Dropped = %d, want 0 (re-queued, not dropped)", m.Dropped)
	}
	// The sample must still be buffered (re-queued), not silently lost.
	if got := rec.drain(); len(got) != 1 {
		t.Fatalf("re-queued buffer has %d samples, want 1 (batch must not be dropped)", len(got))
	}
}

// TestRequeue_BoundedByMaxBufferCountsDrops verifies that re-queuing a batch
// larger than the free buffer space drops the oldest samples and counts them
// (bounded, metered loss) rather than growing without limit.
func TestRequeue_BoundedByMaxBufferCountsDrops(t *testing.T) {
	t.Parallel()
	rec := New(nil, Options{MaxBuffer: 3})
	mk := func(v float64) sqlite.MeasurementSample {
		return sqlite.MeasurementSample{
			CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "DEV:1",
			Parameter: "TEMP", TS: time.Now(), Value: v,
		}
	}
	rec.requeue([]sqlite.MeasurementSample{mk(1), mk(2), mk(3), mk(4), mk(5)})
	if got := rec.Metrics().Dropped; got != 2 {
		t.Errorf("Dropped = %d, want 2 (5 requeued into a buffer of 3)", got)
	}
	got := rec.drain()
	if len(got) != 3 {
		t.Fatalf("buffer len = %d, want 3 (bounded by maxBuffer)", len(got))
	}
	// The two oldest were dropped; the three newest survive.
	if got[0].Value != 3 || got[2].Value != 5 {
		t.Errorf("kept values = %v..%v, want the three newest (3..5)", got[0].Value, got[2].Value)
	}
}
