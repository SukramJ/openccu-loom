// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func dpKey(iface, channel, param string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    iface,
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      param,
	}
}

func TestCacheSetGetRoundTrip(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	key := dpKey("iface", "A:1", "LEVEL")

	c.Set(key, hmtypes.FloatValue(0.75), "source-a")
	e, ok := c.Get(key)
	if !ok {
		t.Fatal("Get: expected ok=true")
	}
	if e.Value.Float != 0.75 {
		t.Fatalf("Value.Float=%v want 0.75", e.Value.Float)
	}
	if e.Source != "source-a" {
		t.Fatalf("Source=%q want source-a", e.Source)
	}
	if e.LastUpdated.IsZero() {
		t.Fatal("LastUpdated should be non-zero after Set")
	}
}

func TestCacheOverwriteReplacesValueAndSource(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	key := dpKey("iface", "B:2", "TEMP")

	c.Set(key, hmtypes.FloatValue(21.0), "sensor-1")
	c.Set(key, hmtypes.FloatValue(22.5), "sensor-2")

	e, ok := c.Get(key)
	if !ok {
		t.Fatal("Get: expected ok=true after second Set")
	}
	if e.Value.Float != 22.5 {
		t.Fatalf("Value.Float=%v want 22.5", e.Value.Float)
	}
	if e.Source != "sensor-2" {
		t.Fatalf("Source=%q want sensor-2 (latest write wins)", e.Source)
	}
}

func TestCacheDeleteTrueFirstFalseSecond(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	key := dpKey("iface", "C:0", "STATE")

	c.Set(key, hmtypes.BoolValue(true), "src")
	if !c.Delete(key) {
		t.Fatal("Delete: expected true on first call")
	}
	if c.Delete(key) {
		t.Fatal("Delete: expected false on second call (entry gone)")
	}
}

func TestCacheLenReflectsInsertAndDelete(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	k1 := dpKey("iface", "D:1", "PARAM1")
	k2 := dpKey("iface", "D:2", "PARAM2")

	if c.Len() != 0 {
		t.Fatalf("Len=%d want 0 on empty cache", c.Len())
	}
	c.Set(k1, hmtypes.IntValue(1), "s")
	if c.Len() != 1 {
		t.Fatalf("Len=%d want 1 after first insert", c.Len())
	}
	c.Set(k2, hmtypes.IntValue(2), "s")
	if c.Len() != 2 {
		t.Fatalf("Len=%d want 2 after second insert", c.Len())
	}
	c.Delete(k1)
	if c.Len() != 1 {
		t.Fatalf("Len=%d want 1 after delete", c.Len())
	}
}

func TestCacheGetMissingKeyReturnsFalse(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	key := dpKey("iface", "Z:9", "MISSING")

	e, ok := c.Get(key)
	if ok {
		t.Fatal("Get on missing key: expected ok=false")
	}
	// Zero-value DataCacheEntry must be returned when not found.
	if e.Source != "" || !e.LastUpdated.IsZero() || e.Value.Kind != hmtypes.ValueKindNone {
		t.Fatalf("zero-value expected for missing key, got %+v", e)
	}
}

func TestCacheConcurrentSetIsRaceFree(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	const goroutines = 20
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := dpKey("iface", "E:1", "RACE")
				c.Set(key, hmtypes.IntValue(g*iterations+i), "goroutine")
				_, _ = c.Get(key)
			}
		}()
	}
	wg.Wait()
	// Just verify the cache has exactly one entry (all goroutines wrote the same key).
	if c.Len() != 1 {
		t.Fatalf("Len=%d want 1 after concurrent writes to one key", c.Len())
	}
}
