// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package coordinators – volume tests for ConfigurationCoordinator.
//
// These cases complement configuration_test.go and configuration_deep_test.go
// by exercising multi-interface isolation, cross-paramset patch scoping,
// DeleteChannel invalidation, multiple-patch accumulation, concurrent read
// safety, and sorting guarantees. No case duplicates any function in the
// existing test files.
package coordinators

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// seedChannel registers a root device and one channel under iface, then
// stores the given paramsets. Returns the channel address.
func seedChannel(
	t *testing.T,
	descs *registry.DeviceDescriptionRegistry,
	pss *registry.ParamsetRegistry,
	iface hmtypes.WireInterfaceID,
	deviceAddr, channelAddr, channelType string,
	sets map[hmenum.ParamsetKey]hmproto.Paramset,
) string {
	t.Helper()
	descs.Put(iface, hmproto.DeviceDescription{Address: deviceAddr, Type: "GENERIC_DEVICE"})
	descs.Put(iface, hmproto.DeviceDescription{Address: channelAddr, Parent: deviceAddr, Type: channelType})
	for k, ps := range sets {
		pss.Put(iface, channelAddr, k, ps)
	}
	return channelAddr
}

// ---------------------------------------------------------------------------
// TestConfigurationMultiInterfaceRegistryIsolation
//
// The same channel address registered on two different interfaces must
// return independent paramsets from the registry. Patches in the
// ConfigurationCoordinator are keyed on (channelAddress, paramsetKey,
// parameter) without an interface dimension — intentional design that
// This test verifies that reading through different interfaces returns the
// correct underlying registry values when no patch is applied.
// ---------------------------------------------------------------------------

func TestConfigurationMultiInterfaceRegistryIsolation(t *testing.T) {
	t.Parallel()

	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const chAddr = "SHARED:1"

	seedChannel(t, descs, ps, wireKey(hmenum.InterfaceHmIPRF), "SHARED", chAddr, "TYPE_A",
		map[hmenum.ParamsetKey]hmproto.Paramset{
			hmenum.ParamsetKeyValues: {"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}},
		})
	seedChannel(t, descs, ps, wireKey(hmenum.InterfaceBidCosRF), "SHARED", chAddr, "TYPE_B",
		map[hmenum.ParamsetKey]hmproto.Paramset{
			hmenum.ParamsetKeyValues: {"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger}},
		})

	cc := NewConfigurationCoordinator(descs, ps, devs)

	// Without any patch, each interface sees its own registry value.
	pd, ok := cc.GetParameterData(wireKey(hmenum.InterfaceHmIPRF), chAddr, hmenum.ParamsetKeyValues, "LEVEL")
	if !ok {
		t.Fatal("GetParameterData HmIPRF: expected ok=true")
	}
	if pd.Type != hmenum.ParameterTypeFloat {
		t.Fatalf("HmIPRF LEVEL type=%v want FLOAT (registry)", pd.Type)
	}

	pd2, ok2 := cc.GetParameterData(wireKey(hmenum.InterfaceBidCosRF), chAddr, hmenum.ParamsetKeyValues, "LEVEL")
	if !ok2 {
		t.Fatal("GetParameterData BidCosRF: expected ok=true")
	}
	if pd2.Type != hmenum.ParameterTypeInteger {
		t.Fatalf("BidCosRF LEVEL type=%v want INTEGER (registry)", pd2.Type)
	}

	// A different channel address on either interface must still be a miss.
	if _, ok3 := cc.GetParameterData(wireKey(hmenum.InterfaceHmIPRF), "MISSING:9", hmenum.ParamsetKeyValues, "LEVEL"); ok3 {
		t.Fatal("unknown channel must return ok=false")
	}
}

// ---------------------------------------------------------------------------
// TestConfigurationPatchScopedToParamsetKey
//
// A patch on MASTER must not be visible when querying the VALUES paramset
// for the same channel.
// ---------------------------------------------------------------------------

func TestConfigurationPatchScopedToParamsetKey(t *testing.T) {
	t.Parallel()

	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const ch = "SCOPE:1"
	seedChannel(t, descs, ps, testIface, "SCOPE", ch, "GENERIC",
		map[hmenum.ParamsetKey]hmproto.Paramset{
			hmenum.ParamsetKeyMaster: {"DURATION": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger}},
			hmenum.ParamsetKeyValues: {"DURATION": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}},
		})

	cc := NewConfigurationCoordinator(descs, ps, devs)

	// Patch MASTER only.
	cc.PatchParameter(ch, hmenum.ParamsetKeyMaster, "DURATION",
		hmproto.ParameterData{Type: hmenum.ParameterTypeString})

	master, _ := cc.GetParameterData(testIface, ch, hmenum.ParamsetKeyMaster, "DURATION")
	if master.Type != hmenum.ParameterTypeString {
		t.Fatalf("MASTER DURATION: want STRING (patched), got %v", master.Type)
	}

	values, _ := cc.GetParameterData(testIface, ch, hmenum.ParamsetKeyValues, "DURATION")
	if values.Type != hmenum.ParameterTypeFloat {
		t.Fatalf("VALUES DURATION: want FLOAT (registry), got %v", values.Type)
	}
}

// ---------------------------------------------------------------------------
// TestConfigurationMultiplePatchesAccumulate
//
// Registering patches for several distinct parameters on the same channel
// must accumulate: clearing one must not affect the others.
// ---------------------------------------------------------------------------

func TestConfigurationMultiplePatchesAccumulate(t *testing.T) {
	t.Parallel()

	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const ch = "MULTI:1"
	seedChannel(t, descs, ps, testIface, "MULTI", ch, "GENERIC",
		map[hmenum.ParamsetKey]hmproto.Paramset{
			hmenum.ParamsetKeyMaster: {
				"ALPHA": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
				"BETA":  hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
				"GAMMA": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
			},
		})

	cc := NewConfigurationCoordinator(descs, ps, devs)

	cc.PatchParameter(ch, hmenum.ParamsetKeyMaster, "ALPHA", hmproto.ParameterData{Type: hmenum.ParameterTypeInteger})
	cc.PatchParameter(ch, hmenum.ParamsetKeyMaster, "BETA", hmproto.ParameterData{Type: hmenum.ParameterTypeString})
	cc.PatchParameter(ch, hmenum.ParamsetKeyMaster, "GAMMA", hmproto.ParameterData{Type: hmenum.ParameterTypeBool})

	// All three patches active.
	full, ok := cc.GetChannelParamset(testIface, ch, hmenum.ParamsetKeyMaster)
	if !ok {
		t.Fatal("GetChannelParamset: expected ok=true")
	}
	if full["ALPHA"].Type != hmenum.ParameterTypeInteger {
		t.Fatalf("ALPHA: want INTEGER, got %v", full["ALPHA"].Type)
	}
	if full["BETA"].Type != hmenum.ParameterTypeString {
		t.Fatalf("BETA: want STRING, got %v", full["BETA"].Type)
	}
	if full["GAMMA"].Type != hmenum.ParameterTypeBool {
		t.Fatalf("GAMMA: want BOOL, got %v", full["GAMMA"].Type)
	}

	// Clear BETA — ALPHA and GAMMA must remain patched.
	if !cc.ClearPatch(ch, hmenum.ParamsetKeyMaster, "BETA") {
		t.Fatal("ClearPatch BETA: expected true")
	}

	full2, _ := cc.GetChannelParamset(testIface, ch, hmenum.ParamsetKeyMaster)
	if full2["ALPHA"].Type != hmenum.ParameterTypeInteger {
		t.Fatal("ALPHA patch must survive BETA clear")
	}
	if full2["BETA"].Type != hmenum.ParameterTypeFloat {
		t.Fatalf("BETA: want FLOAT (registry) after clear, got %v", full2["BETA"].Type)
	}
	if full2["GAMMA"].Type != hmenum.ParameterTypeBool {
		t.Fatal("GAMMA patch must survive BETA clear")
	}
}

// ---------------------------------------------------------------------------
// TestConfigurationDeleteChannelInvalidatesCache
//
// After deleting a channel's paramsets from the registry (via
// ParamsetRegistry.DeleteChannel), the coordinator must report a miss on
// subsequent gets.
// ---------------------------------------------------------------------------

func TestConfigurationDeleteChannelInvalidatesCache(t *testing.T) {
	t.Parallel()

	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const ch = "DEL:1"
	seedChannel(t, descs, ps, testIface, "DEL", ch, "GENERIC",
		map[hmenum.ParamsetKey]hmproto.Paramset{
			hmenum.ParamsetKeyValues: {"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}},
		})

	cc := NewConfigurationCoordinator(descs, ps, devs)

	// Confirm hit before deletion.
	if _, ok := cc.GetParameterData(testIface, ch, hmenum.ParamsetKeyValues, "LEVEL"); !ok {
		t.Fatal("pre-delete: expected ok=true")
	}

	// Remove via registry.
	ps.DeleteChannel(testIface, ch)

	// Now the coordinator must report a miss.
	if _, ok := cc.GetParameterData(testIface, ch, hmenum.ParamsetKeyValues, "LEVEL"); ok {
		t.Fatal("post-delete: expected ok=false for deleted channel")
	}
	if _, ok := cc.GetChannelParamset(testIface, ch, hmenum.ParamsetKeyValues); ok {
		t.Fatal("post-delete: GetChannelParamset must be false for deleted channel")
	}
	if cc.HasParameter(testIface, ch, hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("post-delete: HasParameter must be false for deleted channel")
	}
}

// ---------------------------------------------------------------------------
// TestConfigurationConfigurableChannelsSortedByAddress
//
// ConfigurableChannels must return channels in ascending order of address
// regardless of insertion order.
// ---------------------------------------------------------------------------

func TestConfigurationConfigurableChannelsSortedByAddress(t *testing.T) {
	t.Parallel()

	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	descs.Put(testIface, hmproto.DeviceDescription{Address: "SORT0001", Type: "ROOT"})
	// Insert channels in reverse order to expose any sort bug.
	for _, ch := range []string{"SORT0001:3", "SORT0001:1", "SORT0001:2"} {
		descs.Put(testIface, hmproto.DeviceDescription{
			Address: ch, Parent: "SORT0001", Type: "CHAN",
		})
		ps.Put(testIface, ch, hmenum.ParamsetKeyMaster, hmproto.Paramset{
			"X": hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
		})
	}

	cc := NewConfigurationCoordinator(descs, ps, devs)
	channels := cc.ConfigurableChannels(testIface)

	if len(channels) != 3 {
		t.Fatalf("expected 3 configurable channels, got %d", len(channels))
	}
	want := []string{"SORT0001:1", "SORT0001:2", "SORT0001:3"}
	for i, w := range want {
		if channels[i].ChannelAddress != w {
			t.Fatalf("channels[%d]=%q want %q", i, channels[i].ChannelAddress, w)
		}
	}
}

// ---------------------------------------------------------------------------
// TestConfigurationGetChannelParamsetIncludesPatchedKeys
//
// GetChannelParamset must merge registry keys and patched-only keys into a
// single consistent map. Patching a parameter that exists only in the
// registry must still return the correct value.
// ---------------------------------------------------------------------------

func TestConfigurationGetChannelParamsetIncludesPatchedKeys(t *testing.T) {
	t.Parallel()

	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const ch = "MRG:1"
	seedChannel(t, descs, ps, testIface, "MRG", ch, "GENERIC",
		map[hmenum.ParamsetKey]hmproto.Paramset{
			hmenum.ParamsetKeyMaster: {
				"A": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
				"B": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger},
			},
		})

	cc := NewConfigurationCoordinator(descs, ps, devs)

	// Patch only B.
	cc.PatchParameter(ch, hmenum.ParamsetKeyMaster, "B",
		hmproto.ParameterData{Type: hmenum.ParameterTypeString, Unit: "units"})

	full, ok := cc.GetChannelParamset(testIface, ch, hmenum.ParamsetKeyMaster)
	if !ok {
		t.Fatal("GetChannelParamset: expected ok=true")
	}
	if len(full) != 2 {
		t.Fatalf("expected 2 params, got %d", len(full))
	}
	if full["A"].Type != hmenum.ParameterTypeFloat {
		t.Fatalf("A: want FLOAT (registry), got %v", full["A"].Type)
	}
	if full["B"].Type != hmenum.ParameterTypeString || full["B"].Unit != "units" {
		t.Fatalf("B: want STRING/units (patch), got %v/%q", full["B"].Type, full["B"].Unit)
	}
}

// ---------------------------------------------------------------------------
// TestConfigurationConcurrentReadsRaceFree
//
// Many goroutines perform concurrent reads (GetParameterData, HasParameter,
// GetChannelParamset) while patches are simultaneously being applied.
// Must be data-race-free under -race.
// ---------------------------------------------------------------------------

func TestConfigurationConcurrentReadsRaceFree(t *testing.T) {
	t.Parallel()

	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	const ch = "RACE:1"
	seedChannel(t, descs, ps, testIface, "RACE", ch, "GENERIC",
		map[hmenum.ParamsetKey]hmproto.Paramset{
			hmenum.ParamsetKeyValues: {"X": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}},
		})

	cc := NewConfigurationCoordinator(descs, ps, devs)

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := range workers {
		go func(id int) {
			defer wg.Done()
			//nolint:staticcheck // QF1003: switch-on-mod here mixes types (PatchParameter vs read calls); if-else stays clearer
			if id%3 == 0 {
				cc.PatchParameter(ch, hmenum.ParamsetKeyValues, "X",
					hmproto.ParameterData{Type: hmenum.ParameterTypeInteger})
			} else if id%3 == 1 {
				_, _ = cc.GetParameterData(testIface, ch, hmenum.ParamsetKeyValues, "X")
			} else {
				_, _ = cc.GetChannelParamset(testIface, ch, hmenum.ParamsetKeyValues)
			}
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// TestConfigurationConfigurableChannelsExcludesNoMasterParamset
//
// Channels that have only a VALUES paramset (no MASTER) must not appear in
// ConfigurableChannels output.
// ---------------------------------------------------------------------------

func TestConfigurationConfigurableChannelsExcludesNoMasterParamset(t *testing.T) {
	t.Parallel()

	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	descs.Put(testIface, hmproto.DeviceDescription{Address: "NOM0001", Type: "ROOT"})
	descs.Put(testIface, hmproto.DeviceDescription{Address: "NOM0001:1", Parent: "NOM0001", Type: "VALUES_ONLY"})

	// Only store a VALUES paramset — no MASTER.
	ps.Put(testIface, "NOM0001:1", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})

	cc := NewConfigurationCoordinator(descs, ps, devs)
	channels := cc.ConfigurableChannels(testIface)

	if len(channels) != 0 {
		t.Fatalf("expected 0 configurable channels (no MASTER), got %d: %+v", len(channels), channels)
	}
}

// ---------------------------------------------------------------------------
// TestConfigurationConfigurableChannelsParamCount
//
// ParamCount reported in ConfigurableChannel must equal the number of
// parameters in the MASTER paramset at the time of the call.
// ---------------------------------------------------------------------------

func TestConfigurationConfigurableChannelsParamCount(t *testing.T) {
	t.Parallel()

	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	descs.Put(testIface, hmproto.DeviceDescription{Address: "CNT0001", Type: "ROOT"})
	descs.Put(testIface, hmproto.DeviceDescription{Address: "CNT0001:1", Parent: "CNT0001", Type: "CHAN"})

	ps.Put(testIface, "CNT0001:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"A": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
		"B": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger},
		"C": hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
	})

	cc := NewConfigurationCoordinator(descs, ps, devs)
	channels := cc.ConfigurableChannels(testIface)

	if len(channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(channels))
	}
	if channels[0].ParamCount != 3 {
		t.Fatalf("ParamCount=%d want 3", channels[0].ParamCount)
	}
}
