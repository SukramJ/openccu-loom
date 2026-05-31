// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	for i := 0; i < benchParamCount; i++ {
		ps[benchParamName(i)] = hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	}
	return ps
}

// seedBenchStore inserts benchDeviceCount × benchChannelsPerDev paramset
// rows so that every parameter appears in multiple channels. Returns the
// store and a pre-allocated slice of (channelAddr, param) probe pairs.
type benchProbe struct{ channelAddr, param string }

func seedBenchStore(b *testing.B, s *ParamsetStore) []benchProbe {
	b.Helper()
	ctx := context.Background()
	ps := buildBenchParamset()

	for d := 0; d < benchDeviceCount; d++ {
		for ch := 0; ch < benchChannelsPerDev; ch++ {
			if err := s.Upsert(ctx, ParamsetRecord{
				CentralName:    "bench",
				InterfaceID:    "HmIP-RF",
				ChannelAddress: benchChannelAddr(d, ch),
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Hash:           fmt.Sprintf("h-%d-%d", d, ch),
				Paramset:       ps,
			}); err != nil {
				b.Fatalf("seed Upsert: %v", err)
			}
		}
	}

	// Build a mixed probe set: pick the middle parameter for each device's
	// first channel — these all appear in multiple channels (ch 0, 1, 2).
	probes := make([]benchProbe, benchDeviceCount)
	for d := 0; d < benchDeviceCount; d++ {
		probes[d] = benchProbe{
			channelAddr: benchChannelAddr(d, 0),
			param:       benchParamName(benchParamCount / 2),
		}
	}
	return probes
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

func BenchmarkIsInMultipleChannels_WithCache(b *testing.B) {
	s := freshBenchStore(b)
	probes := seedBenchStore(b, s)

	ctx := context.Background()
	if err := s.WarmCache(ctx); err != nil {
		b.Fatalf("WarmCache: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p := probes[i%len(probes)]
		if _, err := s.IsInMultipleChannels(ctx, "bench", "HmIP-RF", p.channelAddr, p.param); err != nil {
			b.Fatalf("IsInMultipleChannels: %v", err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// BenchmarkIsInMultipleChannels_NoCache
//
// Measures the slow SQL json_extract fallback path. Each iteration clears the
// in-memory cache so that every call goes to the database.
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkIsInMultipleChannels_NoCache(b *testing.B) {
	s := freshBenchStore(b)
	probes := seedBenchStore(b, s)
	ctx := context.Background()

	// Helper to evict the cache for a single device without touching the DB.
	evictDevice := func(deviceAddr string) {
		s.cacheMu.Lock()
		delete(s.cache, deviceAddr)
		s.cacheMu.Unlock()
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p := probes[i%len(probes)]
		// Evict this device from the cache to force the SQL fallback.
		dev, _, _ := splitChannelAddress(p.channelAddr)
		evictDevice(dev)
		if _, err := s.IsInMultipleChannels(ctx, "bench", "HmIP-RF", p.channelAddr, p.param); err != nil {
			b.Fatalf("IsInMultipleChannels (no cache): %v", err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// BenchmarkUpsert_WithCache
//
// Measures Upsert when the cache is already warm (simulates steady-state
// incremental updates). Seeds the store, warms the cache, then benchmarks
// repeated upserts of the same set of rows.
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkUpsert_WithCache(b *testing.B) {
	s := freshBenchStore(b)
	_ = seedBenchStore(b, s)
	ctx := context.Background()
	if err := s.WarmCache(ctx); err != nil {
		b.Fatalf("WarmCache: %v", err)
	}

	ps := buildBenchParamset()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d := i % benchDeviceCount
		ch := i % benchChannelsPerDev
		if err := s.Upsert(ctx, ParamsetRecord{
			CentralName:    "bench",
			InterfaceID:    "HmIP-RF",
			ChannelAddress: benchChannelAddr(d, ch),
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Hash:           fmt.Sprintf("h-%d-%d-%d", d, ch, i),
			Paramset:       ps,
		}); err != nil {
			b.Fatalf("Upsert: %v", err)
		}
	}
}

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
	for i := 0; i < b.N; i++ {
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
