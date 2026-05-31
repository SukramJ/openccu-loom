// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	r.Put(hmenum.InterfaceHmIPRF, "ABC:1", hmenum.ParamsetKeyValues, ps)
	got, ok := r.Get(hmenum.InterfaceHmIPRF, "ABC:1", hmenum.ParamsetKeyValues)
	if !ok || len(got) != 1 {
		t.Fatalf("Get=%v ok=%v", got, ok)
	}
	r.DeleteChannel(hmenum.InterfaceHmIPRF, "ABC:1")
	if r.Len() != 0 {
		t.Fatal("DeleteChannel must purge all paramsets for that channel")
	}
}

func TestParamsetRegistryDeleteHit(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)
	if !r.Delete(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues) {
		t.Fatal("Delete should return true for existing entry")
	}
	if r.Len() != 0 {
		t.Fatalf("expected Len=0 after Delete, got %d", r.Len())
	}
}

func TestParamsetRegistryDeleteMiss(t *testing.T) {
	r := NewParamsetRegistry()
	if r.Delete(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues) {
		t.Fatal("Delete should return false for non-existent entry")
	}
}

func TestParamsetRegistryDeleteChannelMultiple(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"X": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(hmenum.InterfaceHmIPRF, "CH:0", hmenum.ParamsetKeyValues, ps)
	r.Put(hmenum.InterfaceHmIPRF, "CH:0", hmenum.ParamsetKeyMaster, ps)
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)
	r.DeleteChannel(hmenum.InterfaceHmIPRF, "CH:0")
	if r.Len() != 1 {
		t.Fatalf("expected 1 paramset remaining after DeleteChannel, got %d", r.Len())
	}
	_, ok := r.Get(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("entry for CH:1 should still be present")
	}
}

func TestParamsetRegistryConcurrent(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	var wg sync.WaitGroup
	const n = 20
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Put(hmenum.InterfaceHmIPRF, "ADDR:1", hmenum.ParamsetKeyValues, ps)
			_, _ = r.Get(hmenum.InterfaceHmIPRF, "ADDR:1", hmenum.ParamsetKeyValues)
			_ = r.Len()
		}()
	}
	wg.Wait()
}

func TestParamsetRegistryGetChannelAddressesByParamsetKey(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(hmenum.InterfaceHmIPRF, "DEV:1", hmenum.ParamsetKeyValues, hmproto.Paramset{"LEVEL": hmproto.ParameterData{}})
	r.Put(hmenum.InterfaceHmIPRF, "DEV:2", hmenum.ParamsetKeyValues, hmproto.Paramset{"LEVEL": hmproto.ParameterData{}})
	r.Put(hmenum.InterfaceHmIPRF, "DEV:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{"MODE": hmproto.ParameterData{}})

	m := r.GetChannelAddressesByParamsetKey(hmenum.InterfaceHmIPRF, "DEV")
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

	r.Put(hmenum.InterfaceHmIPRF, "DEVXYZ:0", hmenum.ParamsetKeyValues, ps)
	r.Put(hmenum.InterfaceHmIPRF, "DEVXYZ:1", hmenum.ParamsetKeyValues, ps)
	r.Put(hmenum.InterfaceHmIPRF, "DEVXYZ:1", hmenum.ParamsetKeyMaster, ps)
	// Different device — must not appear.
	r.Put(hmenum.InterfaceHmIPRF, "OTHER:1", hmenum.ParamsetKeyValues, ps)

	result := r.GetChannelAddressesByParamsetKey(hmenum.InterfaceHmIPRF, "DEVXYZ")
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
	result := r.GetChannelAddressesByParamsetKey(hmenum.InterfaceHmIPRF, "GHOST")
	if len(result) != 0 {
		t.Fatalf("expected empty map, got %v", result)
	}
}

func TestParamsetRegistryGetChannelParamsetDescriptions(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, hmproto.Paramset{"A": {}})
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{"B": {}})

	descs := r.GetChannelParamsetDescriptions(hmenum.InterfaceHmIPRF, "CH:1")
	if len(descs) != 2 {
		t.Fatalf("GetChannelParamsetDescriptions len=%d want 2", len(descs))
	}
	if _, ok := descs[hmenum.ParamsetKeyValues]; !ok {
		t.Error("VALUES paramset must be in result")
	}

	// unknown channel
	empty := r.GetChannelParamsetDescriptions(hmenum.InterfaceHmIPRF, "UNKNOWN:9")
	if len(empty) != 0 {
		t.Fatal("unknown channel must return empty map")
	}
}

func TestGetChannelParamsetDescriptions(t *testing.T) {
	r := NewParamsetRegistry()
	psV := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	psM := hmproto.Paramset{"TEMP": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}

	r.Put(hmenum.InterfaceHmIPRF, "ABC:1", hmenum.ParamsetKeyValues, psV)
	r.Put(hmenum.InterfaceHmIPRF, "ABC:1", hmenum.ParamsetKeyMaster, psM)

	result := r.GetChannelParamsetDescriptions(hmenum.InterfaceHmIPRF, "ABC:1")
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
	result := r.GetChannelParamsetDescriptions(hmenum.InterfaceHmIPRF, "NOPE:1")
	if len(result) != 0 {
		t.Fatalf("expected empty map for unknown channel, got %v", result)
	}
}

func TestParamsetRegistryGetParameterData(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues,
		hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}})

	pd, ok := r.GetParameterData(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "LEVEL")
	if !ok {
		t.Fatal("GetParameterData: existing parameter must return ok=true")
	}
	if pd.Type != hmenum.ParameterTypeFloat {
		t.Fatalf("GetParameterData Type=%v want Float", pd.Type)
	}

	_, ok2 := r.GetParameterData(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "MISSING")
	if ok2 {
		t.Fatal("GetParameterData: absent parameter must return ok=false")
	}
}

func TestGetParameterData(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{
		"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Unit: "%"},
	}
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)

	pd, ok := r.GetParameterData(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "LEVEL")
	if !ok {
		t.Fatal("expected ok=true for existing parameter")
	}
	if pd.Unit != "%" {
		t.Fatalf("Unit=%q want %%", pd.Unit)
	}
}

func TestGetParameterDataMissing(t *testing.T) {
	r := NewParamsetRegistry()
	_, ok := r.GetParameterData(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "NOSUCH")
	if ok {
		t.Fatal("expected ok=false for missing parameter")
	}
}

func TestParamsetRegistryGetParamsetKeys(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, hmproto.Paramset{})
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{})

	keys := r.GetParamsetKeys(hmenum.InterfaceHmIPRF, "CH:1")
	if len(keys) != 2 {
		t.Fatalf("GetParamsetKeys len=%d want 2", len(keys))
	}
	// unknown channel
	noKeys := r.GetParamsetKeys(hmenum.InterfaceHmIPRF, "NONE:9")
	if len(noKeys) != 0 {
		t.Fatal("GetParamsetKeys for unknown channel must return empty slice")
	}
}

func TestGetParamsetKeys(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"X": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(hmenum.InterfaceHmIPRF, "DEV:0", hmenum.ParamsetKeyValues, ps)
	r.Put(hmenum.InterfaceHmIPRF, "DEV:0", hmenum.ParamsetKeyMaster, ps)

	keys := r.GetParamsetKeys(hmenum.InterfaceHmIPRF, "DEV:0")
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %v", keys)
	}
}

func TestGetParamsetKeysEmpty(t *testing.T) {
	r := NewParamsetRegistry()
	keys := r.GetParamsetKeys(hmenum.InterfaceHmIPRF, "GHOST:1")
	if len(keys) != 0 {
		t.Fatalf("expected no keys for unknown channel, got %v", keys)
	}
}

func TestParamsetRegistryHasInterfaceID(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	if r.HasInterfaceID(hmenum.InterfaceHmIPRF) {
		t.Fatal("HasInterfaceID must return false for empty registry")
	}
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, hmproto.Paramset{})
	if !r.HasInterfaceID(hmenum.InterfaceHmIPRF) {
		t.Fatal("HasInterfaceID must return true after Put")
	}
	if r.HasInterfaceID(hmenum.InterfaceBidCosRF) {
		t.Fatal("HasInterfaceID must return false for interface with no entries")
	}
}

func TestHasInterfaceID(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"Y": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)

	if !r.HasInterfaceID(hmenum.InterfaceHmIPRF) {
		t.Fatal("HasInterfaceID must return true for known interface")
	}
	if r.HasInterfaceID(hmenum.InterfaceBidCosRF) {
		t.Fatal("HasInterfaceID must return false for unknown interface")
	}
}

func TestParamsetRegistryHasParameter(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues,
		hmproto.Paramset{"STATE": {}})

	if !r.HasParameter(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "STATE") {
		t.Fatal("HasParameter must return true for existing parameter")
	}
	if r.HasParameter(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "NOPE") {
		t.Fatal("HasParameter must return false for missing parameter")
	}
}

func TestHasParameter(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, ps)

	if !r.HasParameter(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("HasParameter must return true for existing parameter")
	}
	if r.HasParameter(hmenum.InterfaceHmIPRF, "CH:1", hmenum.ParamsetKeyValues, "NOPE") {
		t.Fatal("HasParameter must return false for absent parameter")
	}
}

func TestParamsetRegistryIsInMultipleChannels(t *testing.T) {
	t.Parallel()
	r := NewParamsetRegistry()
	r.Put(hmenum.InterfaceHmIPRF, "DEV:1", hmenum.ParamsetKeyValues, hmproto.Paramset{"LEVEL": {}})
	r.Put(hmenum.InterfaceHmIPRF, "DEV:2", hmenum.ParamsetKeyValues, hmproto.Paramset{"LEVEL": {}})

	if !r.IsInMultipleChannels("DEV:1", "LEVEL") {
		t.Fatal("IsInMultipleChannels must return true when parameter appears in 2 channels")
	}

	// single channel
	r.Put(hmenum.InterfaceHmIPRF, "DEV2:1", hmenum.ParamsetKeyValues, hmproto.Paramset{"ONLY": {}})
	if r.IsInMultipleChannels("DEV2:1", "ONLY") {
		t.Fatal("IsInMultipleChannels must return false for parameter in only 1 channel")
	}

	// invalid address
	if r.IsInMultipleChannels("NOCODON", "LEVEL") {
		t.Fatal("IsInMultipleChannels must return false for address without channel separator")
	}
}

func TestIsInMultipleChannels(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}

	// LEVEL on two different channels of the same device.
	r.Put(hmenum.InterfaceHmIPRF, "DEV001:1", hmenum.ParamsetKeyValues, ps)
	r.Put(hmenum.InterfaceHmIPRF, "DEV001:2", hmenum.ParamsetKeyValues, ps)

	if !r.IsInMultipleChannels("DEV001:1", "LEVEL") {
		t.Fatal("LEVEL should be detected as in multiple channels")
	}
}

func TestIsInMultipleChannelsFalseForSingleChannel(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(hmenum.InterfaceHmIPRF, "DEV002:1", hmenum.ParamsetKeyValues, ps)

	if r.IsInMultipleChannels("DEV002:1", "LEVEL") {
		t.Fatal("LEVEL should NOT be in multiple channels when only one channel has it")
	}
}

func TestIsInMultipleChannelsFalseForDeviceAddress(t *testing.T) {
	// Plain device address (no colon) → always false.
	r := NewParamsetRegistry()
	if r.IsInMultipleChannels("DEV003", "LEVEL") {
		t.Fatal("plain device address must return false")
	}
}

func TestParamsetRegistryAddAppliesPatch(t *testing.T) {
	// Build a registry that has a patch correcting ENERGY_COUNTER unit.
	pr := patches.NewRegistry()
	r := NewParamsetRegistryWithPatches(pr)

	ps := hmproto.Paramset{
		"ENERGY_COUNTER": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Unit: ""},
	}
	r.Add(hmenum.InterfaceHmIPRF, "VCU001:1", hmenum.ParamsetKeyValues, ps, "HM-ES-PMSw1-Pl")

	got, ok := r.Get(hmenum.InterfaceHmIPRF, "VCU001:1", hmenum.ParamsetKeyValues)
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
	r.Add(hmenum.InterfaceHmIPRF, "VCU0000001:1", hmenum.ParamsetKeyValues, ps, "HM-CC-VG-1")

	got, ok := r.Get(hmenum.InterfaceHmIPRF, "VCU0000001:1", hmenum.ParamsetKeyValues)
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
	r.Add(hmenum.InterfaceHmIPRF, "VCU0000001:2", hmenum.ParamsetKeyValues, ps, "HM-CC-VG-1")

	got, ok := r.Get(hmenum.InterfaceHmIPRF, "VCU0000001:2", hmenum.ParamsetKeyValues)
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
	r.Add(hmenum.InterfaceHmIPRF, "VCU0000001:1", hmenum.ParamsetKeyValues, ps, "HM-CC-RT-DN")

	got, ok := r.Get(hmenum.InterfaceHmIPRF, "VCU0000001:1", hmenum.ParamsetKeyValues)
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
	r.Add(hmenum.InterfaceHmIPRF, "VCU002:1", hmenum.ParamsetKeyValues, ps, "SomeModel")

	got, ok := r.Get(hmenum.InterfaceHmIPRF, "VCU002:1", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("paramset not found")
	}
	if got["LEVEL"].Unit != "%" {
		t.Fatalf("unit not normalised: %q", got["LEVEL"].Unit)
	}
}

func TestRegisterAdditionalParameter(t *testing.T) {
	r := NewParamsetRegistry()
	// Only one paramset stored for DEV004:1 initially.
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(hmenum.InterfaceHmIPRF, "DEV004:1", hmenum.ParamsetKeyValues, ps)

	// Register an additional parameter on channel 2 (e.g. for a calculated DP).
	r.RegisterAdditionalParameter("DEV004:2", "LEVEL")

	// Now LEVEL appears on both channel 1 and channel 2.
	if !r.IsInMultipleChannels("DEV004:1", "LEVEL") {
		t.Fatal("LEVEL should be in multiple channels after RegisterAdditionalParameter")
	}
}

func TestRegisterAdditionalParameterNoopForDeviceAddress(t *testing.T) {
	r := NewParamsetRegistry()
	// Should not panic or mutate anything for a device address without a channel.
	r.RegisterAdditionalParameter("DEV005", "LEVEL")
	if r.IsInMultipleChannels("DEV005", "LEVEL") {
		t.Fatal("plain device address should not be indexed")
	}
}

// Secondary index survives DeleteChannel.
func TestAddressParamCacheCleanedOnDeleteChannel(t *testing.T) {
	r := NewParamsetRegistry()
	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.Put(hmenum.InterfaceHmIPRF, "DEVCLEAN:1", hmenum.ParamsetKeyValues, ps)
	r.Put(hmenum.InterfaceHmIPRF, "DEVCLEAN:2", hmenum.ParamsetKeyValues, ps)

	// LEVEL is in two channels → multiple.
	if !r.IsInMultipleChannels("DEVCLEAN:1", "LEVEL") {
		t.Fatal("pre-condition: LEVEL must be in multiple channels")
	}
	r.DeleteChannel(hmenum.InterfaceHmIPRF, "DEVCLEAN:2")
	// After removing channel 2, only channel 1 remains → not multiple.
	if r.IsInMultipleChannels("DEVCLEAN:1", "LEVEL") {
		t.Fatal("after DeleteChannel, LEVEL must not be in multiple channels")
	}
}

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
			r.Add(hmenum.InterfaceHmIPRF, addr, hmenum.ParamsetKeyValues, ps, "SomeModel")
			_ = r.IsInMultipleChannels("CONCURRENT:1", "LEVEL")
		}(i)
	}
	wg.Wait()
}
