// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func TestCommandTrackerAddSetValue(t *testing.T) {
	tr := reliability.NewCommandTracker("HmIP-RF", reliability.CommandTrackerConfig{})
	dpk, ok := tr.AddSetValue("VCU123:1", hmenum.ParameterLevel, hmenum.ParamsetKeyValues, 0.5)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if dpk.ChannelAddress != "VCU123:1" {
		t.Fatalf("unexpected channel: %s", dpk.ChannelAddress)
	}

	val, found := tr.GetLastSentValue(dpk)
	if !found {
		t.Fatal("expected to find sent value")
	}
	if val != 0.5 {
		t.Fatalf("unexpected value: %v", val)
	}
}

func TestCommandTrackerAddPutParamset(t *testing.T) {
	tr := reliability.NewCommandTracker("HmIP-RF", reliability.CommandTrackerConfig{})
	values := map[string]any{"LEVEL": 0.3, "ON_TIME": 60}
	keys := tr.AddPutParamset("VCU456:1", hmenum.ParamsetKeyValues, values)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}

	// Check both DPKs are tracked.
	for _, dpk := range keys {
		_, ok := tr.GetLastSentValue(dpk)
		if !ok {
			t.Fatalf("expected sent value for %s", dpk.Parameter)
		}
	}
}

func TestCommandTrackerClearForKey(t *testing.T) {
	tr := reliability.NewCommandTracker("HmIP-RF", reliability.CommandTrackerConfig{})
	dpk, _ := tr.AddSetValue("VCU123:1", hmenum.ParameterLevel, hmenum.ParamsetKeyValues, 0.5)

	tr.ClearForKey(dpk)

	_, found := tr.GetLastSentValue(dpk)
	if found {
		t.Fatal("expected value to be cleared")
	}
}

func TestCommandTrackerHasInFlight(t *testing.T) {
	tr := reliability.NewCommandTracker("HmIP-RF", reliability.CommandTrackerConfig{})
	dpk, _ := tr.AddSetValue("VCU123:1", hmenum.ParameterLevel, hmenum.ParamsetKeyValues, true)

	if !tr.HasInFlight(dpk) {
		t.Fatal("expected HasInFlight=true")
	}
	tr.ClearForKey(dpk)
	if tr.HasInFlight(dpk) {
		t.Fatal("expected HasInFlight=false after clear")
	}
}

func TestCommandTrackerTTLExpiry(t *testing.T) {
	cfg := reliability.CommandTrackerConfig{TTL: 1 * time.Millisecond}
	tr := reliability.NewCommandTracker("HmIP-RF", cfg)
	dpk, _ := tr.AddSetValue("VCU123:1", hmenum.ParameterLevel, hmenum.ParamsetKeyValues, 1.0)

	time.Sleep(5 * time.Millisecond)

	_, found := tr.GetLastSentValue(dpk)
	if found {
		t.Fatal("expected expired entry to return not-found")
	}
}

func TestCommandTrackerCleanupExpired(t *testing.T) {
	cfg := reliability.CommandTrackerConfig{TTL: 1 * time.Millisecond}
	tr := reliability.NewCommandTracker("HmIP-RF", cfg)
	tr.AddSetValue("VCU123:1", hmenum.ParameterLevel, hmenum.ParamsetKeyValues, 1.0)
	tr.AddSetValue("VCU123:2", hmenum.ParameterLevel, hmenum.ParamsetKeyValues, 2.0)

	time.Sleep(5 * time.Millisecond)
	removed := tr.CleanupExpired()
	if removed != 2 {
		t.Fatalf("expected 2 removed, got %d", removed)
	}
	if tr.Size() != 0 {
		t.Fatalf("expected size=0 after cleanup, got %d", tr.Size())
	}
}

func TestCommandTrackerSizeLimitEviction(t *testing.T) {
	cfg := reliability.CommandTrackerConfig{
		MaxSize:          5,
		WarningThreshold: 4,
		CleanupThreshold: 100, // disable lazy cleanup
		TTL:              time.Hour,
	}
	tr := reliability.NewCommandTracker("HmIP-RF", cfg)
	for i := range 7 {
		dpk := hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "VCU:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(rune('A' + i)),
		}
		// Add directly via AddPutParamset to get predictable keys.
		tr.AddPutParamset(dpk.ChannelAddress, dpk.ParamsetKey, map[string]any{dpk.Parameter: i})
	}
	// After adding 7 entries with MaxSize=5, eviction must have fired.
	// Size must be ≤ 5 after eviction.
	if tr.Size() > 5 {
		t.Fatalf("expected size ≤ 5, got %d", tr.Size())
	}
}

func TestCommandTrackerClear(t *testing.T) {
	tr := reliability.NewCommandTracker("HmIP-RF", reliability.CommandTrackerConfig{})
	tr.AddSetValue("VCU:1", hmenum.ParameterLevel, hmenum.ParamsetKeyValues, 1.0)
	tr.AddSetValue("VCU:2", hmenum.ParameterLevel, hmenum.ParamsetKeyValues, 2.0)
	tr.Clear()
	if tr.Size() != 0 {
		t.Fatalf("expected empty after Clear, got %d", tr.Size())
	}
}

func TestCommandTrackerAddCombinedParameterHmIP(t *testing.T) {
	tr := reliability.NewCommandTracker("HmIP-RF", reliability.CommandTrackerConfig{})
	// COMBINED_PARAMETER wire string: "L=100,L2=50" → LEVEL=1.0, LEVEL_2=0.5
	keys := tr.AddCombinedParameter("VCU789:1", "COMBINED_PARAMETER", "L=100,L2=50")
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys from COMBINED_PARAMETER, got %d", len(keys))
	}
	var levelKey, level2Key hmtypes.DataPointKey
	for _, dpk := range keys {
		switch dpk.Parameter {
		case "LEVEL":
			levelKey = dpk
		case "LEVEL_2":
			level2Key = dpk
		}
	}
	if levelKey.ChannelAddress == "" {
		t.Fatal("LEVEL key missing from combined parameter result")
	}
	if level2Key.ChannelAddress == "" {
		t.Fatal("LEVEL_2 key missing from combined parameter result")
	}
	val, ok := tr.GetLastSentValue(levelKey)
	if !ok {
		t.Fatal("LEVEL not tracked after AddCombinedParameter")
	}
	if val != 1.0 {
		t.Errorf("LEVEL = %v; want 1.0", val)
	}
}

func TestCommandTrackerAddCombinedParameterInvalid(t *testing.T) {
	tr := reliability.NewCommandTracker("HmIP-RF", reliability.CommandTrackerConfig{})
	// Unparseable wire value should return nil, not panic.
	keys := tr.AddCombinedParameter("VCU789:1", "COMBINED_PARAMETER", "")
	if keys != nil {
		t.Fatalf("expected nil for unparseable combined value, got %v", keys)
	}
	if tr.Size() != 0 {
		t.Fatalf("expected empty tracker, got size=%d", tr.Size())
	}
}
