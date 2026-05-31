// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability_test

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func dpk(iface, ch, param string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    iface,
		ChannelAddress: ch,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      param,
	}
}

func TestInFlightTracker_StageAndClear(t *testing.T) {
	t.Parallel()
	tr := reliability.NewInFlightTracker()
	k := dpk("HmIP-RF", "VCU:1", "STATE")

	_, dup := tr.Stage(k, true)
	if dup {
		t.Fatal("first Stage returned duplicate=true, want false")
	}
	if !tr.IsInFlight(k) {
		t.Fatal("IsInFlight=false after Stage, want true")
	}
	tr.Clear(k)
	if tr.IsInFlight(k) {
		t.Fatal("IsInFlight=true after Clear, want false")
	}
	if tr.Size() != 0 {
		t.Fatalf("Size=%d after Clear, want 0", tr.Size())
	}
}

func TestInFlightTracker_StageDuplicateReturnsOldValue(t *testing.T) {
	t.Parallel()
	tr := reliability.NewInFlightTracker()
	k := dpk("HmIP-RF", "VCU:1", "LEVEL")

	tr.Stage(k, 0.5)
	prev, dup := tr.Stage(k, 1.0)
	if !dup {
		t.Fatal("second Stage returned duplicate=false, want true")
	}
	if prev != 0.5 {
		t.Fatalf("previous value = %v, want 0.5", prev)
	}
	// Current staged value should now be the second write.
	v, ok := tr.Get(k)
	if !ok {
		t.Fatal("Get returned ok=false after second Stage")
	}
	if v != 1.0 {
		t.Fatalf("Get = %v, want 1.0 (second staged value)", v)
	}
}

func TestInFlightTracker_ClearUnknownKeyIsNoop(t *testing.T) {
	t.Parallel()
	tr := reliability.NewInFlightTracker()
	k := dpk("HmIP-RF", "UNKNOWN:99", "STATE")
	tr.Clear(k) // must not panic
	if tr.Size() != 0 {
		t.Fatalf("Size=%d after Clear of unknown key, want 0", tr.Size())
	}
}

// TestInFlightTracker_ConcurrentWritesSameKey verifies the race-fix guarantee:
// two concurrent goroutines writing to the same key must each get a staging
// slot without data races. Only the last write wins the map entry; both defers
// clear it cleanly.
func TestInFlightTracker_ConcurrentWritesSameKey(t *testing.T) {
	t.Parallel()
	tr := reliability.NewInFlightTracker()
	k := dpk("HmIP-RF", "VCU:1", "STATE")

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(val int) {
			defer wg.Done()
			tr.Stage(k, val)
			defer tr.Clear(k)
		}(i)
	}
	wg.Wait()

	// After all goroutines complete, the key must be absent.
	if tr.IsInFlight(k) {
		t.Fatal("key still in-flight after all goroutines completed — Clear not called or race")
	}
}

func TestInFlightTracker_MultipleKeysStagedIndependently(t *testing.T) {
	t.Parallel()
	tr := reliability.NewInFlightTracker()
	k1 := dpk("HmIP-RF", "VCU:1", "LEVEL")
	k2 := dpk("HmIP-RF", "VCU:2", "STATE")

	tr.Stage(k1, 0.75)
	tr.Stage(k2, false)

	if tr.Size() != 2 {
		t.Fatalf("Size=%d, want 2", tr.Size())
	}
	tr.Clear(k1)
	if tr.Size() != 1 {
		t.Fatalf("Size=%d after first Clear, want 1", tr.Size())
	}
	if tr.IsInFlight(k1) {
		t.Fatal("k1 still in-flight after Clear")
	}
	if !tr.IsInFlight(k2) {
		t.Fatal("k2 not in-flight; should still be staged")
	}
}
