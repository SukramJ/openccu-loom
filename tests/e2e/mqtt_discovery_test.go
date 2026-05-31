// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EMQTTDiscoveryAllComponentsEmitted asserts that after a full boot the
// daemon publishes HA-Discovery config payloads for at least the four canonical
// component families expected from the DefaultDevices fleet:
//
//   - binary_sensor (HmIP-SWSD smoke detector STATE channel)
//   - climate       (HmIP-BWTH wall thermostat)
//   - sensor        (HmIP-BSM power-meter POWER/ENERGY channels)
//   - cover         (HmIP-BROLL roller shutter LEVEL channel)
//
// It does NOT assert all 15 HA components listed in the plan — many require
// devices that are not in the DefaultDevices fleet and would need a broader
// fixture list, which is intentionally deferred. This test proves the
// discovery pipeline is wired for the core four.
func TestE2EMQTTDiscoveryAllComponentsEmitted(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{EnableMQTT: true})
	if h.MQTT() == nil {
		t.Fatal("MQTT broker not started")
	}

	wantComponents := map[string]bool{
		"binary_sensor": false,
		"climate":       false,
		"sensor":        false,
		"cover":         false,
	}

	var mu sync.Mutex
	hit := make(chan struct{}, 1)
	allFound := func() bool {
		for _, found := range wantComponents {
			if !found {
				return false
			}
		}
		return true
	}

	if err := h.MQTT().Subscribe("homeassistant/+/+/+/config", func(topic string, _ []byte, _ bool) {
		// Topic shape: homeassistant/<component>/<node_id>/<object_id>/config
		parts := strings.Split(topic, "/")
		if len(parts) < 2 {
			return
		}
		component := parts[1]
		mu.Lock()
		if _, known := wantComponents[component]; known {
			wantComponents[component] = true
		}
		if allFound() {
			select {
			case hit <- struct{}{}:
			default:
			}
		}
		mu.Unlock()
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	deadline := 30 * time.Second
	select {
	case <-hit:
	case <-time.After(deadline):
	}

	mu.Lock()
	defer mu.Unlock()
	var missing []string
	for comp, found := range wantComponents {
		if !found {
			missing = append(missing, comp)
		}
	}
	if len(missing) > 0 {
		t.Errorf("missing HA-Discovery config for components %v within %s", missing, deadline)
	}
}
