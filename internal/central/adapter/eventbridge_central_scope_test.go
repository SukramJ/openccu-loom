// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// scopeRigTwoCentrals registers two centrals that both carry a device at the
// same address, which is the ordinary case rather than an exotic one: the
// virtual remote, the BidCoS pseudo devices and the INT000* internal devices
// carry the identical address on every CCU. The models differ so a lookup
// that drops the central is visible in the result.
func scopeRigTwoCentrals(t *testing.T, addr string) *central.Registry {
	t.Helper()
	reg := central.NewRegistry()
	for _, spec := range []struct{ name, model string }{
		{"ccu-01", "HmIP-RCV-50"},
		{"ccu-02", "HmIP-RCV-60"},
	} {
		c, err := central.New(central.Config{Name: spec.name})
		if err != nil {
			t.Fatalf("central.New(%s): %v", spec.name, err)
		}
		if err := reg.Register(c); err != nil {
			t.Fatalf("register(%s): %v", spec.name, err)
		}
		d := device.New(device.Config{
			InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
			Address: addr, Model: spec.model,
		})
		d.AddChannel(addr+":1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
		c.ModelRegistry.Put(d)
		c.MarkSouthboundReady()
	}
	return reg
}

// TestLookupsResolveWithinTheNamedCentral pins that a device lookup answers
// from the central the event came from.
//
// Registry.List() is name-sorted, so an unscoped first-match walk always
// answers from the alphabetically first central. Every consumer downstream
// then reads another installation's device: the model and name published
// under this central's MQTT topic, the channel type driving discovery, and
// the availability that feeds both the retained availability topic and the
// device-lifecycle event.
func TestLookupsResolveWithinTheNamedCentral(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-RCV-1"
	reg := scopeRigTwoCentrals(t, addr)

	for _, tc := range []struct{ central, wantModel string }{
		{"ccu-01", "HmIP-RCV-50"},
		{"ccu-02", "HmIP-RCV-60"},
	} {
		if d := lookupDeviceObject(reg, tc.central, addr); d == nil {
			t.Errorf("%s: device not found", tc.central)
		} else if d.Model != tc.wantModel {
			t.Errorf("%s: lookupDeviceObject model = %q, want %q", tc.central, d.Model, tc.wantModel)
		}
		if model, _ := lookupDevice(reg, tc.central, addr); model != tc.wantModel {
			t.Errorf("%s: lookupDevice model = %q, want %q", tc.central, model, tc.wantModel)
		}
		if ch := lookupChannel(reg, tc.central, addr, 1); ch == nil {
			t.Errorf("%s: channel not found", tc.central)
		}
	}
}

// TestLookupsDoNotFallBackToAnotherCentral pins that an address absent from
// the named central resolves to nothing, rather than to a namesake on a
// different CCU. Falling back is worse than answering nothing: it publishes a
// foreign installation's data under this central's identity.
func TestLookupsDoNotFallBackToAnotherCentral(t *testing.T) {
	t.Parallel()

	reg := scopeRigTwoCentrals(t, "HmIP-RCV-1")

	if d := lookupDeviceObject(reg, "ccu-03", "HmIP-RCV-1"); d != nil {
		t.Errorf("unknown central resolved to %q on another CCU", d.Model)
	}
	if model, name := lookupDevice(reg, "ccu-03", "HmIP-RCV-1"); model != "" || name != "" {
		t.Errorf("unknown central resolved to (%q, %q)", model, name)
	}
	if ch := lookupChannel(reg, "ccu-03", "HmIP-RCV-1", 1); ch != nil {
		t.Error("unknown central resolved a channel on another CCU")
	}
}
