// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "testing"

// TestRegistryBinarySensorClassesAgreeWithTheDomain holds every
// device-scoped binary-sensor rule in the HA-registry table equal to the
// domain's classification for the same (device, parameter) pair.
//
// This table is the third place that classification lived, and it is the one
// that reaches the wire: discovery.go sets device_class from the domain, then
// applyEntityDescription overwrites it from here. Measured when this guard was
// written: all 18 pairs agree, none conflict, and the domain classifies every
// one of them.
//
// The values are NOT removed, and the reason is worth stating because it is
// the opposite of what the duplication suggests: applyEntityDescription uses
// setOrDeleteString, so an empty DeviceClass DELETES the field the domain just
// set. Emptying these rules would strip device_class from exactly the devices
// they name. The duplication is therefore held equal rather than resolved, and
// this guard is what holds it.
func TestRegistryBinarySensorClassesAgreeWithTheDomain(t *testing.T) {
	t.Parallel()
	pairs := 0
	for _, r := range haRegistryDescriptionRules {
		if r.Category != "binary_sensor" || r.Description.DeviceClass == "" {
			continue
		}
		for _, dev := range r.Devices {
			for _, param := range r.Parameters {
				pairs++
				model := resolveBinarySensorDeviceClass(dev, param)
				if model == "" {
					t.Errorf("%s/%s: the table says %q, the domain classifies nothing — "+
						"the table is the only source, so a domain-side fix cannot reach the wire",
						dev, param, r.Description.DeviceClass)
					continue
				}
				if model != r.Description.DeviceClass {
					t.Errorf("%s/%s: the table says %q, the domain says %q — the table wins on the wire",
						dev, param, r.Description.DeviceClass, model)
				}
			}
		}
	}
	if pairs == 0 {
		t.Fatal("no device-scoped binary-sensor rules found — the guard lost its subject")
	}
	t.Logf("%d (device, parameter) pairs held equal", pairs)
}
