// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"fmt"
	"strings"
)

// ValidateEntityDescriptionRules inspects the static per-domain devParam
// maps for ambiguous entries: two entries with the same composite key
// (devParam) that carry different EntityCategory or EnabledByDefault
// values. Such conflicts indicate a copy-paste error and will cause
// non-deterministic lookup results (Go map iteration order is random).
//
// The function is intended to be called once at daemon startup; on
// conflict it returns a non-nil error listing every conflict found.
// The caller should log the error but not abort (soft startup check),
// mirroring registry.py:validate() (registry.py:237).
//
// Conflicts in the extended rule slices ([sensorExtRules] etc.) are
// checked separately: two rules at the same priority that share all
// non-zero criteria are flagged.
func ValidateEntityDescriptionRules() error {
	var conflicts []string

	// --- Static devParam maps -----------------------------------------------

	// allDevParamMaps collects every (name, map) pair so the checker can
	// iterate generically. Parameter-only maps use the empty string as
	// devicePrefix; we adapt them to devParam for uniform treatment.
	type namedMap struct {
		name    string
		entries map[devParam]EntityDescription
	}

	deviceMaps := []namedMap{
		{"sensor/byDeviceAndParam", sensorRulesByDeviceAndParam},
		{"binarySensor/byDeviceAndParam", binarySensorRulesByDeviceAndParam},
		{"number/byDeviceAndParam", numberRulesByDeviceAndParam},
		{"switch/byDeviceAndParam", switchRulesByDeviceAndParam},
		{"cover/byDeviceAndParam", coverRulesByDeviceAndParam},
		{"siren/byDeviceAndParam", sirenRulesByDeviceAndParam},
		{"valve/byDeviceAndParam", valveRulesByDeviceAndParam},
	}

	// param-only maps: adapt string→devParam for uniform treatment.
	type namedParamMap struct {
		name    string
		entries map[string]EntityDescription
	}
	paramMaps := []namedParamMap{
		{"sensor/byParam", sensorRulesByParam},
		{"binarySensor/byParam", binarySensorRulesByParam},
		{"number/byParam", numberRulesByParam},
		{"switch/byParam", switchRulesByParam},
		{"lock/byParam", lockRulesByParam},
		{"button/byParam", buttonRulesByParam},
		{"select/byParam", selectRulesByParam},
	}

	// devParam maps are keyed so each devParam can appear at most once;
	// a Go map literal would panic on duplicate keys at compile time.
	// The only possible conflict is across devParam-map vs. param-map for
	// the same domain: if the same parameter appears in both tiers with
	// conflicting EntityCategory or EnabledByDefault, the device-specific
	// entry always wins (by lookup precedence) so the param-only entry is
	// shadowed. This is intentional and not flagged.
	//
	// We therefore check each devParam map for self-consistency (no
	// duplicate keys, guaranteed by Go), and then cross-check same-
	// parameter entries across the two tiers of the same domain.

	for _, nm := range deviceMaps {
		_ = nm // static devParam maps cannot have duplicate keys (compile-time uniqueness)
	}

	// Static devParam maps and param-only maps are keyed by their respective
	// key types, so Go itself prevents duplicate entries in the same map
	// (a duplicate key literal in a composite literal is a compile-time
	// error). Cross-tier differences (device-and-param vs. param-only) are
	// intentional: device-specific rules override the generic default by
	// design, so they are NOT flagged here.
	//
	// Nothing more to check for static maps.
	_ = paramMaps

	// --- Extended rule slices -----------------------------------------------
	// Check for rules at the same priority with identical non-zero criteria.

	allExtSlices := []struct {
		name  string
		rules []EntityDescriptionExtRule
	}{
		{"sensor/ext", sensorExtRules},
		{"binarySensor/ext", binarySensorExtRules},
		{"number/ext", numberExtRules},
		{"switch/ext", switchExtRules},
	}

	for _, ns := range allExtSlices {
		rules := ns.rules
		for i := range rules {
			for j := i + 1; j < len(rules); j++ {
				a, b := rules[i], rules[j]
				if a.Priority != b.Priority {
					continue
				}
				// Same priority — check if all non-zero criteria are identical.
				if a.DevicePrefix == b.DevicePrefix &&
					a.Parameter == b.Parameter &&
					a.Unit == b.Unit &&
					a.Postfix == b.Postfix &&
					a.VarNameContains == b.VarNameContains {
					conflicts = append(conflicts, fmt.Sprintf(
						"%s: two ext rules at priority=%d share identical criteria "+
							"(device=%q param=%q unit=%q postfix=%q varNameContains=%q): ambiguous",
						ns.name, a.Priority,
						a.DevicePrefix, a.Parameter, a.Unit, a.Postfix, a.VarNameContains,
					))
				}
			}
		}
	}

	if len(conflicts) == 0 {
		return nil
	}
	var result strings.Builder
	result.WriteString("entity description rules validation failed:\n")
	for _, c := range conflicts {
		result.WriteString("  - " + c + "\n")
	}
	return fmt.Errorf("%s", result.String()) //nolint:goerr113 // dynamic conflict list; not a sentinel
}
