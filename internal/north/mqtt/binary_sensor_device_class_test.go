// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"sort"
	"testing"
)

// TestEveryDeviceParamRuleHasADomainDeviceClass pins every (model, parameter)
// binary-sensor rule to a device_class the domain can answer.
//
// The rules used to carry their own device_class and, being applied second in
// the discovery builder, overwrote the domain's. A correction in
// internal/parameter therefore reached REST, WebSocket and Matter while MQTT
// kept publishing the stale class for every model the table named — a
// divergence with no failing test anywhere, because each side was internally
// consistent.
//
// Now the rules carry only what this plane knows on its own, so a rule whose
// model the domain cannot classify would publish no device_class at all. That
// is what this guard catches.
func TestEveryDeviceParamRuleHasADomainDeviceClass(t *testing.T) {
	t.Parallel()
	keys := make([]devParam, 0, len(binarySensorRulesByDeviceAndParam))
	for k := range binarySensorRulesByDeviceAndParam {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		t.Fatal("no device+parameter rules — the guard lost its subject")
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].devicePrefix < keys[j].devicePrefix })
	for _, k := range keys {
		if got := resolveBinarySensorDeviceClass(k.devicePrefix, k.parameter); got == "" {
			t.Errorf("%s/%s: the domain classifies nothing, so the entity would publish no device_class",
				k.devicePrefix, k.parameter)
		}
	}
}
