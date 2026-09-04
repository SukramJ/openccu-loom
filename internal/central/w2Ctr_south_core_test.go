// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// central-core-c005 — un-ignore candidates on VALUES
// ---------------------------------------------------------------------------

// w2CtrSwitchInterfaceDevice builds an HM-SwI-3-FM channel 1 as the device
// itself declares it, not as a convenient invention: the switch-interface
// VALUES paramset at ../OpenCCU-Base/src/devicetypes/rftypes/rf_swi.xml:85-96
// carries `PRESS` with `operations="write,event"` (OPERATIONS=6) and
// `INSTALL_TEST` with `operations="event"` (OPERATIONS=4), and a recorded
// paramset description for this model reports the same two numbers on
// channels 1-3. Both are forced to Ignored, which is what our own suppression
// table does to INSTALL_TEST (internal/store/visibility/rules.go) and
// therefore what makes them un-ignore candidates in the first place.
func w2CtrSwitchInterfaceDevice(addr string) *device.Device {
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     addr,
		Model:       "HM-SwI-3-FM",
		InterfaceID: "test-BidCos-RF",
	})
	ch := d.AddChannel(addr+":1", 1, "SWITCH_INTERFACE", hmenum.ParamsetKeyValues)
	ignored := hmenum.DataPointUsageIgnored
	ch.Put(&minimalDP{
		key:         hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "PRESS"},
		forcedUsage: &ignored,
		operations:  hmenum.OperationsWrite | hmenum.OperationsEvent,
	})
	ch.Put(&minimalDP{
		key:         hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "INSTALL_TEST"},
		forcedUsage: &ignored,
		operations:  hmenum.OperationsEvent,
	})
	return d
}

// TestW2CtrUnIgnoreCandidatesAdmitEventOnlyValuesParameters pins the VALUES
// arm of operationsMatchParamset against the firmware's own event gate.
//
// The CCU emits a VALUES event on OP_EVENT alone —
// ../OpenCCU-Base/src/libhsscomm/HSSParameter.cpp:363
// `if(!(operations & OP_EVENT))return;` is the only test in
// HSSParameter::ReportEvent, and READ is never consulted; the bit values are
// ../OpenCCU-Base/src/libhsscomm/HSSParameter.h:54-58. Requiring READ *and*
// EVENT therefore strands every event-only parameter, which is not a corner
// case: an XML parse of all 142 device descriptions under
// ../OpenCCU-Base/src/devicetypes/{rftypes,hs485types} (read as latin-1,
// counting <parameter> elements whose nearest enclosing <paramset> is
// type="VALUES" and whose explicit operations attribute contains "event" and
// not "read") yields 64 declarations across 35 files — INSTALL_TEST 37 times
// in 33 files, plus PRESS / PRESS_SHORT / PRESS_LONG / PRESS_CONT /
// PRESS_LONG_RELEASE.
func TestW2CtrUnIgnoreCandidatesAdmitEventOnlyValuesParameters(t *testing.T) {
	qf := buildQFWithDevice(w2CtrSwitchInterfaceDevice("SWI00001"))

	candidates := qf.GetUnIgnoreCandidates(hmenum.ParamsetKeyValues)
	for _, want := range []string{"INSTALL_TEST", "PRESS"} {
		if !sliceContains(candidates, want) {
			t.Errorf("un-ignore candidates omit %q: an event-emitting VALUES parameter "+
				"the operator can therefore never un-ignore; the firmware gates event "+
				"delivery on OP_EVENT alone (HSSParameter.cpp:363), so READ must not be "+
				"required here; got %v", want, candidates)
		}
	}
}

// TestW2CtrUnIgnoreCandidatesStillRequireAnObservableOperation pins the other
// side of the same filter: a VALUES parameter that is neither readable nor
// event-emitting stays out. Widening READ AND EVENT to READ OR EVENT must not
// become "everything".
func TestW2CtrUnIgnoreCandidatesStillRequireAnObservableOperation(t *testing.T) {
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "SWI00002",
		Model:       "HM-SwI-3-FM",
		InterfaceID: "test-BidCos-RF",
	})
	ch := d.AddChannel("SWI00002:1", 1, "SWITCH_INTERFACE", hmenum.ParamsetKeyValues)
	ignored := hmenum.DataPointUsageIgnored
	ch.Put(&minimalDP{
		key:         hmtypes.DataPointKey{ChannelAddress: "SWI00002:1", Parameter: "WRITE_ONLY"},
		forcedUsage: &ignored,
		operations:  hmenum.OperationsWrite,
	})

	if candidates := buildQFWithDevice(d).GetUnIgnoreCandidates(hmenum.ParamsetKeyValues); sliceContains(candidates, "WRITE_ONLY") {
		t.Errorf("un-ignore candidates include a write-only VALUES parameter, which can "+
			"never be observed once un-ignored; got %v", candidates)
	}
}

// ---------------------------------------------------------------------------
// central-core-c007 — Registry.Released across colliding addresses
// ---------------------------------------------------------------------------

// w2CtrBidCosCentralDescs describes the BidCoS central device every rfd
// announces. Its serial is a compile-time literal, so the address is
// identical on every CCU: ../OpenCCU-Base/src/rfd/RFCentral.cpp:36-37
// (`type="HM-RCV-50";` / `serial="BidCoS-RF";`).
func w2CtrBidCosCentralDescs() []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{Address: "BidCoS-RF", Type: "HM-RCV-50"},
		{Address: "BidCoS-RF:1", Type: "VIRTUAL_KEY", Parent: "BidCoS-RF"},
	}
}

// w2CtrRegisterBidCosCentral registers a central under name that holds the
// BidCoS-RF device in its model, and withholds it from the ecosystems when
// held is true. The hold is set through the production path — a stored
// delayed description taken by the accept — not by reaching into the
// coordinator's state.
func w2CtrRegisterBidCosCentral(t *testing.T, r *Registry, name string, held bool) {
	t.Helper()
	u, err := New(Config{Name: name})
	if err != nil {
		t.Fatalf("New(%q): %v", name, err)
	}
	ifaceID := name + "-BidCos-RF"
	u.ModelRegistry.Put(device.New(device.Config{
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "BidCoS-RF",
		Model:       "HM-RCV-50",
		InterfaceID: ifaceID,
	}))
	if held {
		iface := hmtypes.ParseWireInterfaceID(ifaceID)
		ctx := context.Background()
		u.Devices.StoreDelayedDeviceDescriptions(ctx, iface, w2CtrBidCosCentralDescs())
		if got := u.Devices.TakeDelayedDeviceDescriptions(ctx, iface, "BidCoS-RF"); len(got) == 0 {
			t.Fatalf("fixture: no delayed descriptions taken for %q, so no hold was set", name)
		}
		if u.Devices.IsReleased(iface, "BidCoS-RF") {
			t.Fatalf("fixture: %q reports BidCoS-RF released although it was just withheld", name)
		}
	}
	if err := r.Register(u); err != nil {
		t.Fatalf("Register(%q): %v", name, err)
	}
}

// TestW2CtrReleasedCombinesEveryHolderOfARepeatedAddress pins Registry.Released
// against the address spaces the firmware mints per CCU rather than globally.
//
// "BidCoS-RF" is a compile-time literal in every rfd build
// (../OpenCCU-Base/src/rfd/RFCentral.cpp:37), so two CCUs in one daemon both
// hold a device at that address — the case interface_id.go:19-21 already names,
// and internal/routingkey/uniqueid.go:40-44 already compensates for. An
// address-only lookup that returns the first holder in Registry.List order
// (sorted by central name) therefore answers from whichever central happens to
// sort first, and renaming a central silently changes the verdict. A device
// held anywhere is not released, whatever the names are.
func TestW2CtrReleasedCombinesEveryHolderOfARepeatedAddress(t *testing.T) {
	for _, tc := range []struct{ name, holder string }{
		{"hold on the last-sorting central", "ccu-zulu"},
		{"hold on the first-sorting central", "ccu-alpha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			for _, n := range []string{"ccu-alpha", "ccu-zulu"} {
				w2CtrRegisterBidCosCentral(t, r, n, n == tc.holder)
			}
			if r.Released("BidCoS-RF") {
				t.Errorf("Released(%q) = true although %q withholds it: the verdict came "+
					"from the first central in name order, so a device still in the "+
					"onboarding wizard on one CCU reaches the ecosystems because another "+
					"CCU sorts earlier", "BidCoS-RF", tc.holder)
			}
		})
	}
}

// TestW2CtrReleasedReportsReleasedWhenNoHolderWithholds keeps the widened rule
// from collapsing into "always held": with no hold anywhere the answer is
// still released, as is the answer for an address no central knows.
func TestW2CtrReleasedReportsReleasedWhenNoHolderWithholds(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"ccu-alpha", "ccu-zulu"} {
		w2CtrRegisterBidCosCentral(t, r, n, false)
	}
	if !r.Released("BidCoS-RF") {
		t.Error("Released = false although no central withholds BidCoS-RF")
	}
	if !r.Released("VCU0000277") {
		t.Error("Released = false for an address no central holds; absence of a hold must read as released")
	}
}
