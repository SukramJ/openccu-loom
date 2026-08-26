// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// BenchmarkCacheGetParallel measures the read-only fast path under
// contention. Audit R7: with the previous Lock-based Get every reader
// queued behind every other reader; now the path is RLock + atomic
// counter, so concurrent reads scale.
//
// Run with `go test -bench BenchmarkCacheGet -benchtime=2s -cpu 1,4,8`
// against the pre-fix HEAD to compare.
func BenchmarkCacheGetParallel(b *testing.B) {
	c := NewCacheCoordinator()
	const n = 10_000
	keys := make([]hmtypes.DataPointKey, n)
	for i := range n {
		k := hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DEV" + strconv.Itoa(i) + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		}
		keys[i] = k
		c.Set(k, hmtypes.BoolValue(true), "bench")
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Get(keys[i%n])
			i++
		}
	})
}

// BenchmarkCacheGetWriteContention measures the steady-state mix of
// Get + Set under contention. With atomic counters the reader path
// no longer fights the writer for the same lock; the Sets still
// serialize against each other through the write lock.
func BenchmarkCacheGetWriteContention(b *testing.B) {
	c := NewCacheCoordinator()
	const n = 1_000
	keys := make([]hmtypes.DataPointKey, n)
	for i := range n {
		keys[i] = hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DEV" + strconv.Itoa(i) + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		}
		c.Set(keys[i], hmtypes.BoolValue(true), "bench")
	}

	b.ResetTimer()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range b.N {
			c.Set(keys[i%n], hmtypes.BoolValue(i%2 == 0), "bench")
		}
	}()
	go func() {
		defer wg.Done()
		for i := range b.N * 4 {
			c.Get(keys[i%n])
		}
	}()
	wg.Wait()
}
