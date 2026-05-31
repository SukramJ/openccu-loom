// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for ChannelNo field scoping, ApplyParamset behaviour,
// device-type pre-filter, and built-in HM-CC-VG-1 patch correctness.

package patches

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ---------- ChannelNo field ----------

func TestChannelNoFieldNilMatchesAllChannels(t *testing.T) {
	r := &Registry{}
	r.Register(Patch{
		Model:     "TestModel",
		Parameter: hmenum.ParameterLevel,
		ChannelNo: nil, // nil = any channel
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "%"
			return true
		},
	})

	for _, addr := range []string{"VCU:0", "VCU:1", "VCU:5"} {
		pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
		changes := r.ApplyParamset("TestModel", addr, hmenum.ParamsetKeyValues,
			hmproto.Paramset{"LEVEL": *pd})
		if changes == 0 {
			t.Errorf("ChannelNo=nil patch must fire for %s", addr)
		}
	}
}

func TestChannelNoFieldScopedToSpecificChannel(t *testing.T) {
	ch1 := 1
	r := &Registry{}
	r.Register(Patch{
		Model:     "TestDevice",
		Parameter: hmenum.ParameterSetTemperature,
		ChannelNo: &ch1,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Min = json.RawMessage(`4.5`)
			return true
		},
	})

	// Channel 1 → must fire.
	ps1 := hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	}
	c1 := r.ApplyParamset("TestDevice", "VCU001:1", hmenum.ParamsetKeyValues, ps1)
	if c1 == 0 {
		t.Fatal("patch must fire for channel 1")
	}
	if string(ps1["SET_TEMPERATURE"].Min) != "4.5" {
		t.Fatalf("Min=%s want 4.5", ps1["SET_TEMPERATURE"].Min)
	}

	// Channel 2 → must not fire.
	ps2 := hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	}
	c2 := r.ApplyParamset("TestDevice", "VCU001:2", hmenum.ParamsetKeyValues, ps2)
	if c2 != 0 {
		t.Fatalf("patch scoped to channel 1 must not fire for channel 2, got %d changes", c2)
	}
}

// ---------- device_type pre-filter ----------

func TestApplyParamsetSkipsUnrelatedDeviceType(t *testing.T) {
	r := NewRegistry() // built-ins include HM-CC-VG-1 patch

	ps := hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat,
			Min:  json.RawMessage(`0`),
			Max:  json.RawMessage(`0`),
		},
	}
	// Pass an unrelated device type — the HM-CC-VG-1 patch must NOT fire.
	c := r.ApplyParamset("HM-CC-RT-DN", "VCU:1", hmenum.ParamsetKeyValues, ps)
	if c != 0 {
		t.Fatalf("unrelated device type: expected 0 changes, got %d", c)
	}
}

func TestApplyParamsetCaseInsensitiveModelMatch(t *testing.T) {
	r := &Registry{}
	r.Register(Patch{
		Model:     "HmIP-RGBW",
		Parameter: hmenum.ParameterSaturation,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "deg"
			return true
		},
	})

	// Lower-case model name — must still match (case-insensitive).
	ps := hmproto.Paramset{
		"SATURATION": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	}
	c := r.ApplyParamset("hmip-rgbw", "VCU:1", hmenum.ParamsetKeyValues, ps)
	if c == 0 {
		t.Fatal("case-insensitive model match must fire")
	}
}

// ---------- ApplyParamset iterates all parameters ----------

func TestApplyParamsetPatches(t *testing.T) {
	r := &Registry{}
	r.Register(Patch{
		Model:     "MultiParam",
		Parameter: hmenum.ParameterLevel,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "%"
			return true
		},
	})
	r.Register(Patch{
		Model:     "MultiParam",
		Parameter: hmenum.ParameterTemperature,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "°C"
			return true
		},
	})

	ps := hmproto.Paramset{
		"LEVEL":       hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
		"TEMPERATURE": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
		"OTHER":       hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	}
	c := r.ApplyParamset("MultiParam", "VCU:1", hmenum.ParamsetKeyValues, ps)
	if c != 2 {
		t.Fatalf("expected 2 patches applied, got %d", c)
	}
	if ps["LEVEL"].Unit != "%" {
		t.Fatalf("LEVEL Unit=%q want %%", ps["LEVEL"].Unit)
	}
	if ps["TEMPERATURE"].Unit != "°C" {
		t.Fatalf("TEMPERATURE Unit=%q want °C", ps["TEMPERATURE"].Unit)
	}
	if ps["OTHER"].Unit != "" {
		t.Fatalf("OTHER Unit=%q want empty", ps["OTHER"].Unit)
	}
}

func TestApplyParamsetEmptyParamsetReturnsZero(t *testing.T) {
	r := NewRegistry()
	c := r.ApplyParamset("HmIP-RGBW", "VCU:1", hmenum.ParamsetKeyValues, hmproto.Paramset{})
	if c != 0 {
		t.Fatalf("empty paramset must return 0, got %d", c)
	}
}

// ---------- HM-CC-VG-1 built-in patch correctness ----------

func TestBuiltInHMCCVG1Patch(t *testing.T) {
	r := NewRegistry()

	badMin, _ := json.Marshal(0)
	badMax, _ := json.Marshal(0)
	ps := hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat,
			Min:  json.RawMessage(badMin),
			Max:  json.RawMessage(badMax),
		},
	}
	c := r.ApplyParamset("HM-CC-VG-1", "VCU0000001:1", hmenum.ParamsetKeyValues, ps)
	if c == 0 {
		t.Fatal("HM-CC-VG-1 patch must fire")
	}
	pd := ps["SET_TEMPERATURE"]
	if string(pd.Min) != "4.5" {
		t.Fatalf("MIN=%s want 4.5", pd.Min)
	}
	if string(pd.Max) != "30.5" {
		t.Fatalf("MAX=%s want 30.5", pd.Max)
	}
}

func TestBuiltInHMCCVG1PatchIdempotent(t *testing.T) {
	r := NewRegistry()
	goodMin, _ := json.Marshal(4.5)
	goodMax, _ := json.Marshal(30.5)
	ps := hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat,
			Min:  json.RawMessage(goodMin),
			Max:  json.RawMessage(goodMax),
		},
	}
	c := r.ApplyParamset("HM-CC-VG-1", "VCU0000001:1", hmenum.ParamsetKeyValues, ps)
	if c != 0 {
		t.Fatalf("second apply must be no-op (idempotent), got %d changes", c)
	}
}

// ---------- Reason field is observable ----------

func TestPatchReasonFieldSet(t *testing.T) {
	r := &Registry{}
	r.Register(Patch{
		Model:     "TestModel",
		Parameter: hmenum.ParameterLevel,
		Reason:    "test reason",
		Apply:     func(_ *hmproto.ParameterData) bool { return false },
	})
	r.mu.RLock()
	p := r.patches[0]
	r.mu.RUnlock()
	if p.Reason != "test reason" {
		t.Fatalf("Reason=%q want \"test reason\"", p.Reason)
	}
}

// ---------- Concurrent ApplyParamset ----------

func TestApplyParamsetConcurrent(t *testing.T) {
	r := NewRegistry()
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			ps := hmproto.Paramset{
				"ENERGY_COUNTER": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
			}
			_ = r.ApplyParamset("HM-ES-PMSw1-Pl", "VCU:1", hmenum.ParamsetKeyValues, ps)
		}()
	}
	wg.Wait()
}
