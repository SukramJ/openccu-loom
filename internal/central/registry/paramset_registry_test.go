// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package registry

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/patches"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func TestParamsetRegistry(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(wireHmIPRF, "ABC:1", hmenum.ParamsetKeyValues, ps)
	got, ok := r.Get(wireHmIPRF, "ABC:1", hmenum.ParamsetKeyValues)
	if !ok || len(got) != 1 {
		t.Fatalf("Get=%v ok=%v", got, ok)
	}
	r.DeleteChannel(wireHmIPRF, "ABC:1")
	if r.Len() != 0 {
		t.Fatal("DeleteChannel must purge all paramsets for that channel")
	}
}

func TestParamsetRegistryDeleteHit(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)
	if !r.Delete(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues) {
		t.Fatal("Delete should return true for existing entry")
	}
	if r.Len() != 0 {
		t.Fatalf("expected Len=0 after Delete, got %d", r.Len())
	}
}

func TestParamsetRegistryDeleteMiss(t *testing.T) {
	r := NewParamsetRegistry()
	if r.Delete(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues) {
		t.Fatal("Delete should return false for non-existent entry")
	}
}

func TestParamsetRegistryDeleteChannelMultiple(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"X": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(wireHmIPRF, "CH:0", hmenum.ParamsetKeyValues, ps)
	r.Put(wireHmIPRF, "CH:0", hmenum.ParamsetKeyMaster, ps)
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)
	r.DeleteChannel(wireHmIPRF, "CH:0")
	if r.Len() != 1 {
		t.Fatalf("expected 1 paramset remaining after DeleteChannel, got %d", r.Len())
	}
	_, ok := r.Get(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("entry for CH:1 should still be present")
	}
}

func TestParamsetRegistryConcurrent(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	var wg sync.WaitGroup
	const n = 20
	for range n {
		wg.Go(func() {
			r.Put(wireHmIPRF, "ADDR:1", hmenum.ParamsetKeyValues, ps)
			_, _ = r.Get(wireHmIPRF, "ADDR:1", hmenum.ParamsetKeyValues)
			_ = r.Len()
		})
	}
	wg.Wait()
}

func TestParamsetRegistryGetChannelAddressesByParamsetKey(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(wireHmIPRF, "DEV:1", hmenum.ParamsetKeyValues, hmproto.Paramset{"LEVEL": hmproto.ParameterData{}})
	r.Put(wireHmIPRF, "DEV:2", hmenum.ParamsetKeyValues, hmproto.Paramset{"LEVEL": hmproto.ParameterData{}})
	r.Put(wireHmIPRF, "DEV:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{"MODE": hmproto.ParameterData{}})

	m := r.GetChannelAddressesByParamsetKey(wireHmIPRF, "DEV")
	valAddrs := m[hmenum.ParamsetKeyValues]
	if len(valAddrs) != 2 {
		t.Fatalf("VALUES channels len=%d want 2", len(valAddrs))
	}
	masterAddrs := m[hmenum.ParamsetKeyMaster]
	if len(masterAddrs) != 1 {
		t.Fatalf("MASTER channels len=%d want 1", len(masterAddrs))
	}
}

func TestGetChannelAddressesByParamsetKey(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"X": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}

	r.Put(wireHmIPRF, "DEVXYZ:0", hmenum.ParamsetKeyValues, ps)
	r.Put(wireHmIPRF, "DEVXYZ:1", hmenum.ParamsetKeyValues, ps)
	r.Put(wireHmIPRF, "DEVXYZ:1", hmenum.ParamsetKeyMaster, ps)
	// Different device — must not appear.
	r.Put(wireHmIPRF, "OTHER:1", hmenum.ParamsetKeyValues, ps)

	result := r.GetChannelAddressesByParamsetKey(wireHmIPRF, "DEVXYZ")
	if len(result[hmenum.ParamsetKeyValues]) != 2 {
		t.Fatalf("VALUES: expected 2 channel addresses, got %v", result[hmenum.ParamsetKeyValues])
	}
	if len(result[hmenum.ParamsetKeyMaster]) != 1 {
		t.Fatalf("MASTER: expected 1 channel address, got %v", result[hmenum.ParamsetKeyMaster])
	}
	if _, found := result[hmenum.ParamsetKeyValues]; !found {
		t.Fatal("VALUES key must be present")
	}
}

func TestGetChannelAddressesByParamsetKeyEmptyForUnknownDevice(t *testing.T) {
	r := NewParamsetRegistry()
	result := r.GetChannelAddressesByParamsetKey(wireHmIPRF, "GHOST")
	if len(result) != 0 {
		t.Fatalf("expected empty map, got %v", result)
	}
}

func TestParamsetRegistryGetChannelParamsetDescriptions(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, hmproto.Paramset{"A": {}})
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{"B": {}})

	descs := r.GetChannelParamsetDescriptions(wireHmIPRF, "CH:1")
	if len(descs) != 2 {
		t.Fatalf("GetChannelParamsetDescriptions len=%d want 2", len(descs))
	}
	if _, ok := descs[hmenum.ParamsetKeyValues]; !ok {
		t.Error("VALUES paramset must be in result")
	}

	// unknown channel
	empty := r.GetChannelParamsetDescriptions(wireHmIPRF, "UNKNOWN:9")
	if len(empty) != 0 {
		t.Fatal("unknown channel must return empty map")
	}
}

func TestGetChannelParamsetDescriptions(t *testing.T) {
	r := NewParamsetRegistry()
	psV := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	psM := hmproto.Paramset{"TEMP": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}

	r.Put(wireHmIPRF, "ABC:1", hmenum.ParamsetKeyValues, psV)
	r.Put(wireHmIPRF, "ABC:1", hmenum.ParamsetKeyMaster, psM)

	result := r.GetChannelParamsetDescriptions(wireHmIPRF, "ABC:1")
	if len(result) != 2 {
		t.Fatalf("expected 2 paramset keys, got %d", len(result))
	}
	if _, ok := result[hmenum.ParamsetKeyValues]; !ok {
		t.Fatal("VALUES must be present")
	}
	if _, ok := result[hmenum.ParamsetKeyMaster]; !ok {
		t.Fatal("MASTER must be present")
	}
}

func TestGetChannelParamsetDescriptionsUnknownChannel(t *testing.T) {
	r := NewParamsetRegistry()
	result := r.GetChannelParamsetDescriptions(wireHmIPRF, "NOPE:1")
	if len(result) != 0 {
		t.Fatalf("expected empty map for unknown channel, got %v", result)
	}
}

func TestParamsetRegistryGetParameterData(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues,
		hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}})

	pd, ok := r.GetParameterData(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "LEVEL")
	if !ok {
		t.Fatal("GetParameterData: existing parameter must return ok=true")
	}
	if pd.Type != hmenum.ParameterTypeFloat {
		t.Fatalf("GetParameterData Type=%v want Float", pd.Type)
	}

	_, ok2 := r.GetParameterData(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "MISSING")
	if ok2 {
		t.Fatal("GetParameterData: absent parameter must return ok=false")
	}
}

func TestGetParameterData(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{
		"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Unit: "%"},
	}
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)

	pd, ok := r.GetParameterData(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "LEVEL")
	if !ok {
		t.Fatal("expected ok=true for existing parameter")
	}
	if pd.Unit != "%" {
		t.Fatalf("Unit=%q want %%", pd.Unit)
	}
}

func TestGetParameterDataMissing(t *testing.T) {
	r := NewParamsetRegistry()
	_, ok := r.GetParameterData(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "NOSUCH")
	if ok {
		t.Fatal("expected ok=false for missing parameter")
	}
}

func TestParamsetRegistryGetParamsetKeys(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, hmproto.Paramset{})
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{})

	keys := r.GetParamsetKeys(wireHmIPRF, "CH:1")
	if len(keys) != 2 {
		t.Fatalf("GetParamsetKeys len=%d want 2", len(keys))
	}
	// unknown channel
	noKeys := r.GetParamsetKeys(wireHmIPRF, "NONE:9")
	if len(noKeys) != 0 {
		t.Fatal("GetParamsetKeys for unknown channel must return empty slice")
	}
}

func TestGetParamsetKeys(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"X": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(wireHmIPRF, "DEV:0", hmenum.ParamsetKeyValues, ps)
	r.Put(wireHmIPRF, "DEV:0", hmenum.ParamsetKeyMaster, ps)

	keys := r.GetParamsetKeys(wireHmIPRF, "DEV:0")
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %v", keys)
	}
}

func TestGetParamsetKeysEmpty(t *testing.T) {
	r := NewParamsetRegistry()
	keys := r.GetParamsetKeys(wireHmIPRF, "GHOST:1")
	if len(keys) != 0 {
		t.Fatalf("expected no keys for unknown channel, got %v", keys)
	}
}

func TestParamsetRegistryHasInterfaceID(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	if r.HasInterfaceID(wireHmIPRF) {
		t.Fatal("HasInterfaceID must return false for empty registry")
	}
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, hmproto.Paramset{})
	if !r.HasInterfaceID(wireHmIPRF) {
		t.Fatal("HasInterfaceID must return true after Put")
	}
	if r.HasInterfaceID(wireBidCosRF) {
		t.Fatal("HasInterfaceID must return false for interface with no entries")
	}
}

func TestHasInterfaceID(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"Y": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)

	if !r.HasInterfaceID(wireHmIPRF) {
		t.Fatal("HasInterfaceID must return true for known interface")
	}
	if r.HasInterfaceID(wireBidCosRF) {
		t.Fatal("HasInterfaceID must return false for unknown interface")
	}
}

func TestParamsetRegistryHasParameter(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues,
		hmproto.Paramset{"STATE": {}})

	if !r.HasParameter(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "STATE") {
		t.Fatal("HasParameter must return true for existing parameter")
	}
	if r.HasParameter(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "NOPE") {
		t.Fatal("HasParameter must return false for missing parameter")
	}
}

func TestHasParameter(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)

	if !r.HasParameter(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("HasParameter must return true for existing parameter")
	}
	if r.HasParameter(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "NOPE") {
		t.Fatal("HasParameter must return false for absent parameter")
	}
}

func TestParamsetRegistryAddAppliesPatch(t *testing.T) {
	// Build a registry that has a patch correcting ENERGY_COUNTER unit.
	pr := patches.NewRegistry()
	r := NewParamsetRegistryWithPatches(pr)

	ps := hmproto.Paramset{
		"ENERGY_COUNTER": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Unit: ""},
	}
	r.Add(wireHmIPRF, "VCU001:1", hmenum.ParamsetKeyValues, ps, "HM-ES-PMSw1-Pl")

	got, ok := r.Get(wireHmIPRF, "VCU001:1", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("Add: paramset not found after Add")
	}
	pd, ok := got["ENERGY_COUNTER"]
	if !ok {
		t.Fatal("Add: ENERGY_COUNTER parameter missing")
	}
	if pd.Unit != "Wh" {
		t.Fatalf("Add: expected Unit=Wh (patch applied), got %q", pd.Unit)
	}
}

// contract: known broken device (HM-CC-VG-1 SET_TEMPERATURE bounds).
func TestParamsetRegistryAddPatchesHMCCVG1(t *testing.T) {
	pr := patches.NewRegistry()
	r := NewParamsetRegistryWithPatches(pr)

	// Simulate the broken paramset as returned by the CCU.
	badMin, _ := json.Marshal(0.0)
	badMax, _ := json.Marshal(0.0)
	ps := hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat,
			Min:  json.RawMessage(badMin),
			Max:  json.RawMessage(badMax),
		},
	}
	// Channel 1 of HM-CC-VG-1 — must trigger the channel_no=1 scoped patch.
	r.Add(wireHmIPRF, "VCU0000001:1", hmenum.ParamsetKeyValues, ps, "HM-CC-VG-1")

	got, ok := r.Get(wireHmIPRF, "VCU0000001:1", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("paramset not found")
	}
	pd := got["SET_TEMPERATURE"]
	if string(pd.Min) != "4.5" {
		t.Fatalf("MIN=%s want 4.5", pd.Min)
	}
	if string(pd.Max) != "30.5" {
		t.Fatalf("MAX=%s want 30.5", pd.Max)
	}
}

// channel_no=1 scoped patch must NOT fire on channel 2.
func TestParamsetRegistryAddPatchChannelNoScopingSkipsOtherChannel(t *testing.T) {
	pr := patches.NewRegistry()
	r := NewParamsetRegistryWithPatches(pr)

	badMin, _ := json.Marshal(0.0)
	badMax, _ := json.Marshal(0.0)
	ps := hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat,
			Min:  json.RawMessage(badMin),
			Max:  json.RawMessage(badMax),
		},
	}
	// Channel 2 — the patch is scoped to channel 1, so it must not fire.
	r.Add(wireHmIPRF, "VCU0000001:2", hmenum.ParamsetKeyValues, ps, "HM-CC-VG-1")

	got, ok := r.Get(wireHmIPRF, "VCU0000001:2", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("paramset not found")
	}
	pd := got["SET_TEMPERATURE"]
	if string(pd.Min) != "0" {
		t.Fatalf("MIN=%s want 0 (patch must not fire for channel 2)", pd.Min)
	}
}

// device_type pre-filter — an unrelated device type must not be patched.
func TestParamsetRegistryAddDeviceTypePreFilter(t *testing.T) {
	pr := patches.NewRegistry()
	r := NewParamsetRegistryWithPatches(pr)

	badMin, _ := json.Marshal(0.0)
	badMax, _ := json.Marshal(0.0)
	ps := hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat,
			Min:  json.RawMessage(badMin),
			Max:  json.RawMessage(badMax),
		},
	}
	// HM-CC-RT-DN is a different device — no patch must fire.
	r.Add(wireHmIPRF, "VCU0000001:1", hmenum.ParamsetKeyValues, ps, "HM-CC-RT-DN")

	got, ok := r.Get(wireHmIPRF, "VCU0000001:1", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("paramset not found")
	}
	pd := got["SET_TEMPERATURE"]
	if string(pd.Min) != "0" {
		t.Fatalf("MIN=%s want 0 for unrelated device type", pd.Min)
	}
}

// Add without patch registry uses the old normalise-only path (backward compat).
func TestParamsetRegistryAddNoPatchRegistry(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{
		"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Unit: "  % "},
	}
	r.Add(wireHmIPRF, "VCU002:1", hmenum.ParamsetKeyValues, ps, "SomeModel")

	got, ok := r.Get(wireHmIPRF, "VCU002:1", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("paramset not found")
	}
	if got["LEVEL"].Unit != "%" {
		t.Fatalf("unit not normalised: %q", got["LEVEL"].Unit)
	}
}

// TestParamsetRegistryConcurrentAdd exercises the registry's write path
// from many goroutines at once. It is the race detector's entry point
// into Add, so it stays after the dead secondary index it also touched
// was removed.
func TestParamsetRegistryConcurrentAdd(t *testing.T) {
	pr := patches.NewRegistry()
	r := NewParamsetRegistryWithPatches(pr)
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}

	var wg sync.WaitGroup
	const n = 20
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			addr := "CONCURRENT:1"
			if i%2 == 0 {
				addr = "CONCURRENT:2"
			}
			r.Add(wireHmIPRF, addr, hmenum.ParamsetKeyValues, ps, "SomeModel")
			_, _ = r.Get(wireHmIPRF, addr, hmenum.ParamsetKeyValues)
		}(i)
	}
	wg.Wait()
}
