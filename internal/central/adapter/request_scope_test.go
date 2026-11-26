// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// The fixture puts the same address on ccu-01 (model HmIP-RCV-50) and ccu-02
// (model HmIP-RCV-60), the shape of a virtual-remote root that repeats on
// every CCU. An unbound walk is name-ordered and reaches ccu-01 first; a
// request bound to ccu-02 must reach ccu-02's device and nothing else.
const sharedScopeAddress = "HmIP-RCV-1"

func boundTo(name string) context.Context {
	return hmreqctx.WithCentralScope(context.Background(), name)
}

func TestUnitHoldingFollowsTheRequestBinding(t *testing.T) {
	t.Parallel()
	reg := scopeRigTwoCentrals(t, sharedScopeAddress)
	ccu01, _ := reg.Get("ccu-01")
	ccu01.ModelRegistry.Put(newScopeDevice("ONLY-ON-01", "HmIP-PSM"))

	cases := []struct {
		name        string
		ctx         context.Context
		address     string
		wantCentral string
		wantModel   string
	}{
		{"unbound takes the first central by name", context.Background(), sharedScopeAddress, "ccu-01", "HmIP-RCV-50"},
		{"bound to the second central resolves there", boundTo("ccu-02"), sharedScopeAddress, "ccu-02", "HmIP-RCV-60"},
		{"bound to the first central resolves there", boundTo("ccu-01"), sharedScopeAddress, "ccu-01", "HmIP-RCV-50"},
		{"bound central lacking the address does not fall back", boundTo("ccu-02"), "ONLY-ON-01", "", ""},
		{"unknown bound central resolves nothing", boundTo("ccu-99"), sharedScopeAddress, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, dev, ok := unitHolding(tc.ctx, reg, tc.address)
			if tc.wantCentral == "" {
				if ok {
					t.Fatalf("resolved on %s, want nothing", u.Name())
				}
				return
			}
			if !ok {
				t.Fatalf("not resolved, want %s", tc.wantCentral)
			}
			if u.Name() != tc.wantCentral || dev.Model != tc.wantModel {
				t.Errorf("resolved %s/%s, want %s/%s", u.Name(), dev.Model, tc.wantCentral, tc.wantModel)
			}
		})
	}
}

func TestRequestUnitsToleratesANilRegistry(t *testing.T) {
	t.Parallel()
	if got := requestUnits(boundTo("ccu-01"), nil); got != nil {
		t.Errorf("requestUnits(nil) = %v, want nil", got)
	}
	if _, _, ok := unitHolding(context.Background(), nil, sharedScopeAddress); ok {
		t.Error("unitHolding(nil) resolved a device")
	}
}

// TestDeviceResolvesAnAmbiguousAddressOnTheBoundCentral pins that the
// refusal of an address several centrals share (see
// TestDeviceLookupRefusesAnAddressSeveralCentralsShare) lifts once the request
// names its central, and that the device and the central come from the same
// lookup.
func TestDeviceResolvesAnAmbiguousAddressOnTheBoundCentral(t *testing.T) {
	t.Parallel()
	a := &DevicesAdapter{registry: scopeRigTwoCentrals(t, sharedScopeAddress)}

	d, ok := a.Device(boundTo("ccu-02"), sharedScopeAddress)
	if !ok || d.Model != "HmIP-RCV-60" {
		t.Fatalf("Device(bound ccu-02) = %v, %v; want the HmIP-RCV-60 on ccu-02", d, ok)
	}
	d, centralName, ok := a.Resolve(boundTo("ccu-02"), sharedScopeAddress)
	if !ok || centralName != "ccu-02" || d.Model != "HmIP-RCV-60" {
		t.Errorf("Resolve(bound ccu-02) = %v, %q, %v; want ccu-02's device", d, centralName, ok)
	}
	if _, _, ok := a.Resolve(context.Background(), sharedScopeAddress); ok {
		t.Error("unbound Resolve picked one of several centrals")
	}
}

func TestChannelMetaFollowsTheRequestBinding(t *testing.T) {
	t.Parallel()
	a := &DevicesAdapter{registry: scopeRigTwoCentrals(t, sharedScopeAddress)}

	_, model, _, centralName, ok := a.ChannelMeta(boundTo("ccu-02"), sharedScopeAddress+":1")
	if !ok || centralName != "ccu-02" || model != "HmIP-RCV-60" {
		t.Errorf("bound: central=%q model=%q ok=%v; want ccu-02 / HmIP-RCV-60", centralName, model, ok)
	}
	_, model, _, centralName, ok = a.ChannelMeta(context.Background(), sharedScopeAddress+":1")
	if !ok || centralName != "ccu-01" || model != "HmIP-RCV-50" {
		t.Errorf("unbound: central=%q model=%q ok=%v; want ccu-01 / HmIP-RCV-50", centralName, model, ok)
	}
}

// TestSetValueWritesOnTheBoundCentral pins the write path: a request bound to
// ccu-02 reaches ccu-02's backend even though ccu-01 holds the same address.
func TestSetValueWritesOnTheBoundCentral(t *testing.T) {
	t.Parallel()
	w := &scopeValueWriter{}
	a := &DataPointWriterAdapter{registry: scopeRigTwoCentrals(t, sharedScopeAddress), writer: w}

	err := a.SetValue(boundTo("ccu-02"), sharedScopeAddress+":1", hmenum.ParameterPressShort, true, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetValue(bound ccu-02): %v", err)
	}
	if want := []string{"ccu-02/" + sharedScopeAddress + ":1"}; !slices.Equal(w.calls, want) {
		t.Errorf("writes = %v, want %v", w.calls, want)
	}
}

// TestParamsetsUnitsForFollowsTheRequestBinding pins the precedence on the
// paramset path: an explicit central (edit session, config import) wins over
// the request binding, an empty one follows the binding, and an unbound call
// keeps the name-ordered walk over every central.
func TestParamsetsUnitsForFollowsTheRequestBinding(t *testing.T) {
	t.Parallel()
	p := &ParamsetsDomain{registry: scopeRigTwoCentrals(t, sharedScopeAddress)}

	names := func(ctx context.Context, centralName string) []string {
		units := p.unitsFor(ctx, centralName)
		out := make([]string, 0, len(units))
		for _, u := range units {
			out = append(out, u.Name())
		}
		return out
	}
	cases := []struct {
		name     string
		ctx      context.Context
		explicit string
		want     []string
	}{
		{"unbound, no name: every central in name order", context.Background(), "", []string{"ccu-01", "ccu-02"}},
		{"bound, no name: the bound central", boundTo("ccu-02"), "", []string{"ccu-02"}},
		{"bound, explicit name: the explicit central", boundTo("ccu-02"), "ccu-01", []string{"ccu-01"}},
		{"bound to an unknown central: none", boundTo("ccu-99"), "", nil},
	}
	for _, tc := range cases {
		if got := names(tc.ctx, tc.explicit); !slices.Equal(got, tc.want) {
			t.Errorf("%s: unitsFor = %v, want %v", tc.name, got, tc.want)
		}
	}
	ch := p.resolveChannel(boundTo("ccu-02"), sharedScopeAddress+":1")
	if ch == nil {
		t.Fatal("resolveChannel(bound ccu-02) found no channel")
	}
	if model := ch.Device().Model; model != "HmIP-RCV-60" {
		t.Errorf("resolveChannel(bound ccu-02) reached the %s channel, want ccu-02's HmIP-RCV-60", model)
	}
}
