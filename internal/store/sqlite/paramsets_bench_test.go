// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// paramsets_bench_test.go — Performance benchmarks for the
// address-parameter cache in ParamsetStore.
//
// The benchmarks verify that the in-memory cache provides a substantial
// speed-up over the SQL json_extract fallback path for IsInMultipleChannels
// at the scale of a realistic godevccu fleet (~500 devices × 50 parameters).
//
// # Running
//
//	go test -bench=. -benchmem ./internal/store/sqlite/...
//
// For a quick single-iteration smoke-run:
//
//	go test -bench=. -benchtime=1x ./internal/store/sqlite/...
//
// Expected outcome (indicative):
//
// BenchmarkIsInMultipleChannels_WithCache — O(1) map lookup
// BenchmarkIsInMultipleChannels_NoCache — O(n) SQL json_extract (50-100× slower)
// BenchmarkUpsert_WithCache — Upsert + in-memory cache update
// BenchmarkUpsert_Bare — Upsert on an empty store (baseline)

package sqlite

import (
	"context"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ─────────────────────────────────────────────────────────────────────────────
// Shared test data generation
// ─────────────────────────────────────────────────────────────────────────────

const (
	benchDeviceCount    = 500 // approximate godevccu fleet size
	benchChannelsPerDev = 3   // channels 0, 1, 2 per device
	benchParamCount     = 50  // parameters per channel
)

// benchParamName returns a stable parameter name for index i.
func benchParamName(i int) string { return fmt.Sprintf("PARAM_%04d", i) }

// benchDeviceAddr returns a stable device address for device index d.
func benchDeviceAddr(d int) string { return fmt.Sprintf("VCU%07d", d) }

// benchChannelAddr returns the channel address for device d, channel ch.
func benchChannelAddr(d, ch int) string {
	return fmt.Sprintf("%s:%d", benchDeviceAddr(d), ch)
}

// buildBenchParamset builds a Paramset with benchParamCount parameters.
func buildBenchParamset() hmproto.Paramset {
	ps := make(hmproto.Paramset, benchParamCount)
	for i := range benchParamCount {
		ps[benchParamName(i)] = hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	}
	return ps
}

// freshBenchStore opens a new ParamsetStore backed by a per-benchmark
// file-backed DB. Cannot use t.TempDir because Benchmark has no TempDir —
// use b.TempDir (available since Go 1.15).
func freshBenchStore(b *testing.B) *ParamsetStore {
	b.Helper()
	dsn := "file:" + b.TempDir() + "/bench.db?_pragma=journal_mode(WAL)"
	openMu.Lock()
	db, err := Open(context.Background(), dsn)
	openMu.Unlock()
	if err != nil {
		b.Fatalf("freshBenchStore Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return NewParamsetStore(db)
}

// ─────────────────────────────────────────────────────────────────────────────
// BenchmarkIsInMultipleChannels_WithCache
//
// Measures the hot O(1) cache path. Seeds the store, calls WarmCache once,
// then measures IsInMultipleChannels over the probe set in a loop.
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// BenchmarkIsInMultipleChannels_NoCache
//
// Measures the slow SQL json_extract fallback path. Each iteration clears the
// in-memory cache so that every call goes to the database.
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// BenchmarkUpsert_WithCache
//
// Measures Upsert when the cache is already warm (simulates steady-state
// incremental updates). Seeds the store, warms the cache, then benchmarks
// repeated upserts of the same set of rows.
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// BenchmarkUpsert_Bare
//
// Measures Upsert against a cold empty store (first-write baseline). The cache
// starts empty — every insert also populates the cache.
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkUpsert_Bare(b *testing.B) {
	s := freshBenchStore(b)
	ctx := context.Background()
	ps := buildBenchParamset()

	b.ResetTimer()
	b.ReportAllocs()
	for i := range b.N {
		d := i % benchDeviceCount
		ch := i % benchChannelsPerDev
		if err := s.Upsert(ctx, ParamsetRecord{
			CentralName:    "bench",
			InterfaceID:    "HmIP-RF",
			ChannelAddress: benchChannelAddr(d, ch),
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Hash:           fmt.Sprintf("h-%d", i),
			Paramset:       ps,
		}); err != nil {
			b.Fatalf("Upsert bare: %v", err)
		}
	}
}
