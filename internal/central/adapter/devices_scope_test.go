// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDeviceLookupRefusesAnAddressSeveralCentralsShare pins that an
// address-keyed facade does not pick a CCU for the caller.
//
// The virtual-remote roots and the INT000* group devices carry the identical
// address on every CCU. Registry.List() is name-sorted, so first-match always
// answered from the alphabetically first central — a foreign installation's
// device served under a bare address, and on the write path a command
// delivered to the wrong CCU's hardware. Refusing is the honest answer: the
// caller has not said which CCU it means.
func TestDeviceLookupRefusesAnAddressSeveralCentralsShare(t *testing.T) {
	t.Parallel()

	const shared = "HmIP-RCV-1"
	reg := scopeRigTwoCentrals(t, shared)
	a := &DevicesAdapter{registry: reg}

	if d, ok := a.Device(shared); ok {
		t.Errorf("shared address resolved to %q on one arbitrary central", d.Model)
	}
}

// TestDeviceLookupStillResolvesAnUnambiguousAddress pins that the refusal is
// narrow: an address only one central holds resolves exactly as before, which
// is every address in a single-CCU installation.
func TestDeviceLookupStillResolvesAnUnambiguousAddress(t *testing.T) {
	t.Parallel()

	reg := scopeRigTwoCentrals(t, "HmIP-RCV-1")
	u, ok := reg.Get("ccu-02")
	if !ok {
		t.Fatal("ccu-02 missing")
	}
	u.ModelRegistry.Put(newScopeDevice("ONLY-ON-02", "HmIP-PSM"))

	a := &DevicesAdapter{registry: reg}
	d, ok := a.Device("ONLY-ON-02")
	if !ok {
		t.Fatal("unambiguous address must still resolve")
	}
	if d.Model != "HmIP-PSM" {
		t.Errorf("model = %q, want HmIP-PSM", d.Model)
	}
}

// TestSetValueRefusesAnAddressSeveralCentralsShare pins the same rule on the
// write path, where picking the wrong CCU actuates the wrong hardware. The
// error must name the candidates so the operator can tell which CCU to say.
func TestSetValueRefusesAnAddressSeveralCentralsShare(t *testing.T) {
	t.Parallel()

	const shared = "HmIP-RCV-1"
	reg := scopeRigTwoCentrals(t, shared)
	w := &scopeValueWriter{}
	a := &DataPointWriterAdapter{registry: reg, writer: w}

	err := a.SetValue(context.Background(), shared+":1", hmenum.ParameterPressShort, true, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("a shared address was written on one arbitrary central")
	}
	if !strings.Contains(err.Error(), "ccu-01") || !strings.Contains(err.Error(), "ccu-02") {
		t.Errorf("error should name both candidates, got %v", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("refused write still reached the wire: %d call(s)", len(w.calls))
	}
}

// scopeValueWriter records the writes a refused dispatch must not make.
type scopeValueWriter struct {
	calls []string
}

func (w *scopeValueWriter) SetValue(
	_ context.Context, centralName, _, channelAddress string,
	_ hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	w.calls = append(w.calls, centralName+"/"+channelAddress)
	return nil
}

// newScopeDevice builds a device for the scoping fixtures.
func newScopeDevice(address, model string) *device.Device {
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: address, Model: model,
	})
	d.AddChannel(address+":1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	return d
}
