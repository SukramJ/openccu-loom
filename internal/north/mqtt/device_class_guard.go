// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strings"
	"sync"

	hacatalog "github.com/SukramJ/go-ha-catalog"
)

// dropForeignDeviceClass clears a device_class the rendered platform does not
// declare.
//
// The two can disagree because they are decided in different places and on
// purpose. The description lookup is keyed on the MODEL's category
// (`ev.Category` — switch, button, action, schedule_switch …) rather than on
// the HA component, because collapsing them first would apply the button
// default to ACTION datapoints and over-emit translation_key=button_press.
// Separately, a datapoint that is not writable is downgraded: a switch the
// operator cannot drive is rendered as a binary_sensor rather than a
// non-functional switch that throws an RPC error on every toggle.
//
// Both are right, and together they produced `device_class: "switch"` on a
// binary_sensor — `switch` is a valid class for the switch platform and for
// no other. Home Assistant drops an invalid device class in silence, so the
// entity arrives with none and nothing says why. Found by running
// go-hamqtt's validator over the fleet's 9,996 retained configs.
//
// The table is Home Assistant's own generated/device_classes.json, which
// hassfest keeps current in HA's CI, read through go-ha-catalog. A second
// hand-maintained list here would be one more thing to drift.
func dropForeignDeviceClass(comp HAComponent, class string) string {
	if class == "" {
		return ""
	}
	allowed, ok := deviceClassesFor(string(comp))
	if !ok {
		// A platform the table does not describe. Leaving the class alone is
		// the conservative answer: the validator still reports it, and
		// silently dropping a class on a platform this build cannot reason
		// about would be the same invisible edit in the other direction.
		return class
	}
	if _, legal := allowed[strings.ToLower(class)]; legal {
		return class
	}
	return ""
}

var (
	deviceClassOnce  sync.Once
	deviceClassIndex map[string]map[string]struct{}
)

func deviceClassesFor(platform string) (map[string]struct{}, bool) {
	deviceClassOnce.Do(func() {
		table, err := hacatalog.LoadDeviceClasses()
		if err != nil {
			// Leave the index nil: every lookup then misses and every class
			// is kept, which is the same conservative answer as an unknown
			// platform. A catalog this build cannot read is a build problem,
			// not a reason to start editing payloads.
			return
		}
		deviceClassIndex = make(map[string]map[string]struct{}, len(table))
		for plat, classes := range table {
			set := make(map[string]struct{}, len(classes))
			for _, c := range classes {
				set[strings.ToLower(c)] = struct{}{}
			}
			deviceClassIndex[plat] = set
		}
	})
	set, ok := deviceClassIndex[platform]
	return set, ok
}
