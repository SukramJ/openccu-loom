// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// rehydrationOps wraps [fakeOperations] with a fixed device inventory and a
// VALUES paramset description per channel, so a callback's background
// refresh has something to re-pull after the invalidation.
type rehydrationOps struct {
	*fakeOperations
	descs []hmproto.DeviceDescription
}

func (f *rehydrationOps) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	return f.descs, nil
}

func (f *rehydrationOps) GetParamsetDescription(_ context.Context, _ string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	if key != hmenum.ParamsetKeyValues {
		return map[string]hmproto.ParameterData{}, nil
	}
	return map[string]hmproto.ParameterData{"LEVEL": {Type: "FLOAT"}}, nil
}

// rehydrationDevice returns the root + one channel description for address.
func rehydrationDevice(address string) []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{Address: address, Type: "HmIP-BROLL", Children: []string{address + ":1"}, Paramsets: []string{"MASTER"}},
		{Address: address + ":1", Type: "BLIND_CHANNEL", Parent: address, Paramsets: []string{"MASTER", "VALUES"}},
	}
}

// newRehydrationHandlers builds a central + handlers whose HmIP-RF backend
// serves the given inventory, and returns the canonical wire interface the
// callbacks key the registries by.
func newRehydrationHandlers(t *testing.T, name string, inventory []hmproto.DeviceDescription) (*central.Unit, *CallbackHandlers, hmtypes.WireInterfaceID) {
	t.Helper()
	c, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	t.Cleanup(h.Stop)
	fake := &rehydrationOps{fakeOperations: &fakeOperations{kind: backends.KindCCU}, descs: inventory}
	w := client.NewValueWriter()
	w.Register(c.Name(), hmtypes.ParseWireInterfaceID("HmIP-RF"), fake)
	h.SetWriter(w)
	return c, h, hmtypes.ParseWireInterfaceID(h.canonicalInterfaceID("HmIP-RF"))
}

// seedKnownDevice puts a device's descriptions and a stale VALUES paramset
// into the registries, the state a running daemon holds before the callback.
func seedKnownDevice(c *central.Unit, iface hmtypes.WireInterfaceID, address string) {
	descs := rehydrationDevice(address)
	for i := range descs {
		c.DescRegistry.Put(iface, descs[i])
	}
	c.DeviceRegistry.Put(registry.DeviceEntry{Interface: iface, Address: address, Model: "HmIP-BROLL"})
	c.ParamsetReg.Put(iface, address+":1", hmenum.ParamsetKeyValues, hmproto.Paramset{"LEVEL": {Type: "FLOAT"}})
}

// assertValuesRehydrated fails unless the channel's VALUES description is in
// the paramset registry — the description stream values are typed through.
func assertValuesRehydrated(t *testing.T, c *central.Unit, iface hmtypes.WireInterfaceID, channel string) {
	t.Helper()
	ps, ok := c.ParamsetReg.Get(iface, channel, hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatalf("VALUES paramset description of %s missing from the registry after the callback's background refresh", channel)
	}
	if pd, has := ps["LEVEL"]; !has || pd.Type != "FLOAT" {
		t.Fatalf("VALUES paramset description of %s lacks LEVEL/FLOAT, got %v", channel, ps)
	}
}

// TestUpdateDeviceFirmwareRehydratesParamsetDescriptions verifies that an
// updateDevice(hint=0) callback, which invalidates the device's descriptions
// and paramset descriptions, re-pulls the paramset descriptions too — not
// only the device descriptions — so stream values keep their declared type.
func TestUpdateDeviceFirmwareRehydratesParamsetDescriptions(t *testing.T) {
	t.Parallel()
	const addr = "DEVUPD0001"
	c, h, iface := newRehydrationHandlers(t, "ccu-rehydrate-upd", rehydrationDevice(addr))
	seedKnownDevice(c, iface, addr)

	if err := h.UpdateDevice(context.Background(), "HmIP-RF", addr, 0); err != nil {
		t.Fatalf("UpdateDevice: %v", err)
	}
	// Stop waits for the background refresh goroutine.
	h.Stop()

	assertValuesRehydrated(t, c, iface, addr+":1")
}

// TestReaddedDeviceRehydratesParamsetDescriptions verifies the same for every
// address of a readdedDevice callback.
func TestReaddedDeviceRehydratesParamsetDescriptions(t *testing.T) {
	t.Parallel()
	const a1, a2 = "DEVRDD0001", "DEVRDD0002"
	inventory := append(rehydrationDevice(a1), rehydrationDevice(a2)...)
	c, h, iface := newRehydrationHandlers(t, "ccu-rehydrate-rdd", inventory)
	seedKnownDevice(c, iface, a1)
	seedKnownDevice(c, iface, a2)

	if err := h.ReaddedDevice(context.Background(), "HmIP-RF", []string{a1, a2}); err != nil {
		t.Fatalf("ReaddedDevice: %v", err)
	}
	h.Stop()

	assertValuesRehydrated(t, c, iface, a1+":1")
	assertValuesRehydrated(t, c, iface, a2+":1")
}

// TestReplaceDeviceRehydratesParamsetDescriptions verifies that the
// replacement device of a replaceDevice callback gets its paramset
// descriptions, not only its device descriptions.
func TestReplaceDeviceRehydratesParamsetDescriptions(t *testing.T) {
	t.Parallel()
	const oldAddr, newAddr = "DEVOLD0001", "DEVNEW0001"
	c, h, iface := newRehydrationHandlers(t, "ccu-rehydrate-rpl", rehydrationDevice(newAddr))
	seedKnownDevice(c, iface, oldAddr)

	if err := h.ReplaceDevice(context.Background(), "HmIP-RF", oldAddr, newAddr); err != nil {
		t.Fatalf("ReplaceDevice: %v", err)
	}
	h.Stop()

	assertValuesRehydrated(t, c, iface, newAddr+":1")
	if _, ok := c.ParamsetReg.Get(iface, oldAddr+":1", hmenum.ParamsetKeyValues); ok {
		t.Fatalf("VALUES paramset description of the replaced device %s:1 survived the replacement", oldAddr)
	}
}
