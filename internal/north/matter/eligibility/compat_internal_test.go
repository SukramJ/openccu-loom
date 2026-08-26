// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package eligibility

import "testing"

// TestCompatWarnsOnlyForCommissionedEcosystems pins the property that
// decides whether these findings get read at all: a warning about an
// ecosystem nobody paired is noise, and noise trains an operator to
// ignore the list that also carries the bridge-killer.
func TestCompatWarnsOnlyForCommissionedEcosystems(t *testing.T) {
	t.Parallel()

	valves := map[uint16]int{deviceTypeWaterValve: 2}

	appleOnly := Compat([]uint16{0x1349}, valves, 10)
	if len(appleOnly) != 0 {
		t.Errorf("an Apple-only fabric produced %d finding(s); Apple supports valves, so there is "+
			"nothing to warn about", len(appleOnly))
	}

	withGoogle := Compat([]uint16{0x1349, 0x6006}, valves, 10)
	if len(withGoogle) == 0 {
		t.Error("a Google fabric with valve endpoints must warn — Google Home omits them silently, which " +
			"is indistinguishable from a bridge fault from the operator's side")
	}
}

// TestCompatFlagsTheAlexaBridgeKiller pins the finding that costs the
// most: one unsupported device type does not merely hide its own
// endpoint on Alexa, it takes the whole bridge down with it.
func TestCompatFlagsTheAlexaBridgeKiller(t *testing.T) {
	t.Parallel()

	findings := Compat([]uint16{0x1217}, map[uint16]int{deviceTypeWaterLeakDetector: 1}, 5)
	var found *CompatFinding
	for i := range findings {
		if findings[i].Code == "device_type_breaks_bridge" {
			found = &findings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no bridge-killer finding for a leak detector on an Alexa fabric; got %+v", findings)
	}
	if found.Ecosystem != EcosystemAmazon {
		t.Errorf("ecosystem = %q, want amazon", found.Ecosystem)
	}
	if found.DeviceType != deviceTypeWaterLeakDetector {
		t.Errorf("device type = %#x, want the leak detector", found.DeviceType)
	}
}

// TestCompatWarnsNearTheAlexaEndpointCeiling covers the failure that
// arrives with fleet growth rather than with a single device, and
// therefore looks like a regression in whatever was added last.
func TestCompatWarnsNearTheAlexaEndpointCeiling(t *testing.T) {
	t.Parallel()

	if got := Compat([]uint16{0x1217}, nil, amazonEndpointCeiling); len(got) != 0 {
		t.Errorf("at the ceiling itself the bridge still works; got %+v", got)
	}
	over := Compat([]uint16{0x1217}, nil, amazonEndpointCeiling+1)
	if len(over) == 0 {
		t.Error("one endpoint past the ceiling must warn — Alexa drops devices without reporting an error, " +
			"so the symptom appears long after the exposure that caused it")
	}
}

// TestEcosystemForVendorFallsBackToUnknown pins that an unrecognised
// controller is reported as unknown rather than silently classified,
// since a wrong classification would attach warnings to the wrong
// ecosystem.
func TestEcosystemForVendorFallsBackToUnknown(t *testing.T) {
	t.Parallel()

	if got := EcosystemForVendor(0x1349); got != EcosystemApple {
		t.Errorf("0x1349 = %q, want apple", got)
	}
	if got := EcosystemForVendor(0xFFF1); got != EcosystemUnknown {
		t.Errorf("the CSA test vendor id = %q, want unknown", got)
	}
}
