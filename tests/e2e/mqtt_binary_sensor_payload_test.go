// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// valueLowerTemplate is the extractor the daemon declares for every
// binary_sensor. The test compares the declared payload tokens against
// `strings.ToLower` of the published value, which is only a faithful stand-in
// for Home Assistant's Jinja rendering while this exact template is declared —
// so the test asserts the template too, rather than assuming it.
const valueLowerTemplate = `{% if value_json is defined and value_json.value is not none %}{{ value_json.value | lower }}{% endif %}`

// TestE2EMQTTBinarySensorPayloadsMatchPublishedState drives a door/window
// contact through the built daemon and compares what the state plane PUBLISHES
// against what HA-Discovery DECLARES for the same entity.
//
// The two halves are produced by different code paths and nothing inside the
// daemon compares them: an ENUM data point publishes its VALUE_LIST label
// ("OPEN"), while the binary_sensor declaration used to hard-code
// payload_on "true" / payload_off "false". Home Assistant matches those by
// exact string and, on a miss, logs at INFO and leaves the state untouched —
// so the contact stays *available* and `unknown` for as long as it is paired,
// every automation keyed on it silently never fires, and no surface in the
// daemon reports anything wrong.
func TestE2EMQTTBinarySensorPayloadsMatchPublishedState(t *testing.T) {
	t.Parallel()

	// HMIP-SWDO is the canonical ENUM binary sensor: channel 1 STATE is
	// TYPE=ENUM with VALUE_LIST [CLOSED, OPEN]. HmIP-SWSD contributes plain
	// BOOL binary sensors, so both descriptor shapes are exercised.
	h := harness.Start(t, harness.Options{
		EnableMQTT: true,
		Devices:    []string{"HMIP-SWDO", "HmIP-SWSD"},
	})
	broker := h.MQTT()
	if broker == nil {
		t.Fatal("MQTT broker not started")
	}

	var mu sync.Mutex
	configs := map[string][]byte{}
	raw := map[string][]byte{}

	if err := broker.Subscribe("homeassistant/binary_sensor/+/+/config", func(topic string, payload []byte, _ bool) {
		mu.Lock()
		defer mu.Unlock()
		if len(payload) == 0 {
			delete(configs, topic)
			return
		}
		configs[topic] = append([]byte(nil), payload...)
	}); err != nil {
		t.Fatalf("subscribe discovery: %v", err)
	}
	if err := broker.Subscribe("openccu-loom/#", func(topic string, payload []byte, _ bool) {
		mu.Lock()
		defer mu.Unlock()
		raw[topic] = append([]byte(nil), payload...)
	}); err != nil {
		t.Fatalf("subscribe raw plane: %v", err)
	}

	// Wait for a binary_sensor whose descriptor companion carries a
	// VALUE_LIST — that is the entity class this test exists for. The
	// descriptor comes off the wire, so the test names no device model.
	var (
		stateTopic string
		declared   map[string]any
	)
	waitFor(t, 60*time.Second, "a binary_sensor with a VALUE_LIST descriptor", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, payload := range configs {
			body := decodeJSON(t, payload)
			companion, _ := body["json_attributes_topic"].(string)
			if companion == "" {
				continue
			}
			desc, ok := raw[companion]
			if !ok {
				continue
			}
			if list, _ := decodeJSON(t, desc)["value_list"].([]any); len(list) == 0 {
				continue
			}
			stateTopic, _ = body["state_topic"].(string)
			declared = body
			return stateTopic != ""
		}
		return false
	})

	// `<base>/<central>/<iface>/<address>/<channel>/<bucket>/<parameter>`
	parts := strings.Split(stateTopic, "/")
	if len(parts) != 7 {
		t.Fatalf("unexpected state topic shape %q", stateTopic)
	}
	channelAddress := parts[3] + ":" + parts[4]
	parameter := parts[6]

	// Push the second VALUE_LIST entry (the "on" side of the pair) so the
	// state topic carries a real value instead of the boot-time null.
	if err := h.CCU().V().SimulateDeviceEvent(channelAddress, parameter, 1); err != nil {
		t.Fatalf("SimulateDeviceEvent(%s, %s): %v", channelAddress, parameter, err)
	}

	var published any
	waitFor(t, 30*time.Second, "a non-null value on "+stateTopic, func() bool {
		mu.Lock()
		defer mu.Unlock()
		payload, ok := raw[stateTopic]
		if !ok {
			return false
		}
		published, ok = decodeJSON(t, payload)["value"]
		return ok && published != nil
	})

	if tpl, _ := declared["value_template"].(string); tpl != valueLowerTemplate {
		t.Fatalf("binary_sensor declares value_template %q; this test only models %q",
			tpl, valueLowerTemplate)
	}
	on, _ := declared["payload_on"].(string)
	off, _ := declared["payload_off"].(string)
	rendered := strings.ToLower(fmt.Sprint(published))
	if rendered != on && rendered != off {
		t.Fatalf("%s publishes %q, which the discovery config declares as neither payload_on (%q) nor payload_off (%q) — "+
			"Home Assistant matches these by exact string, so the entity stays available and `unknown` forever",
			stateTopic, rendered, on, off)
	}
}

// waitFor polls until done reports true or the deadline expires.
func waitFor(t *testing.T, within time.Duration, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if done() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", within, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func decodeJSON(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return map[string]any{}
	}
	return body
}
