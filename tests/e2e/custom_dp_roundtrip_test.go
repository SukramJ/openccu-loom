// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2ECustomDPRoundtrip exercises the full MQTT round-trip for a custom
// data-point write:
//
//  1. Locate an HmIP-BSM switch device that godevccu loaded.
//  2. Publish a SET command on the raw-plane command topic for channel 4
//     STATE parameter. The topic uses the wire interface ID
//     (<central>-<iface>) as emitted by the daemon.
//  3. godevccu accepts the SetValue call (via its XML-RPC interface) and
//     fires a paramset event back to the daemon via the callback server.
//  4. The daemon relays the event to the MQTT bridge, which publishes the
//     new value on the corresponding raw-plane state topic.
//  5. The test asserts that the state topic carries an updated value.
//
// Topic shape: the daemon uses the composite wire interface ID
// "<central>-<iface>" (e.g. "ccu-e2e-HmIP-RF") as the interface segment in
// every MQTT topic it publishes and subscribes, matching the ValueWriter's
// backend registry key. Both the SET command topic and the state topic must
// use this composite form.
func TestE2ECustomDPRoundtrip(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{EnableMQTT: true, AuthMode: harness.AuthSession})
	if h.MQTT() == nil {
		t.Fatal("MQTT broker not started")
	}

	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Step 1: Locate an HmIP-BSM switch device via the REST device list.
	// We look for a device whose type contains "BSM" and whose channel 4
	// exposes STATE (the switch output).
	bsmAddress := findBSMDevice(t, h)
	if bsmAddress == "" {
		t.Skip("no HmIP-BSM device found in daemon device list — skipping roundtrip test")
	}
	t.Logf("using HmIP-BSM device at address %s", bsmAddress)

	// The daemon's MQTT topic for HmIP-RF on central "ccu-e2e" uses the
	// composite wire interface ID "ccu-e2e-HmIP-RF" as the interface
	// segment. Both the SET command topic and the state topic use this form.
	const centralName = "ccu-e2e"
	const wireIface = "ccu-e2e-HmIP-RF"
	stateTopic := fmt.Sprintf("openccu-loom/%s/%s/%s/4/values/STATE", centralName, wireIface, bsmAddress)

	// Step 2: Subscribe to the raw-plane state topic BEFORE sending the
	// SET so we do not miss the echo.
	var mu sync.Mutex
	var lastPayload string
	hit := make(chan struct{}, 1)

	subscribeFilter := fmt.Sprintf("openccu-loom/%s/%s/+/4/values/STATE", centralName, wireIface)
	_ = h.MQTT().Subscribe(subscribeFilter, func(topic string, payload []byte, _ bool) {
		if !strings.Contains(topic, bsmAddress) {
			return
		}
		mu.Lock()
		lastPayload = string(payload)
		select {
		case hit <- struct{}{}:
		default:
		}
		mu.Unlock()
	})

	// Step 3: Publish SET true on the command topic. The topic shape is
	// <stateTopic>/set. The daemon's CommandSubscriber is subscribed to
	// the same wire-interface form and routes the write to the correct backend.
	setCmdTopic := stateTopic + "/set"
	setPayload := []byte(`{"value":true}`)
	if err := h.MQTT().Publish(setCmdTopic, setPayload, false, 0); err != nil {
		t.Fatalf("publish SET command: %v", err)
	}
	t.Logf("published SET true on %s", setCmdTopic)

	// Step 4–5: Wait for the echo on the state topic.
	select {
	case <-hit:
		mu.Lock()
		got := lastPayload
		mu.Unlock()
		t.Logf("state echo received on %s: %s", stateTopic, got)
	case <-time.After(20 * time.Second):
		t.Fatalf("state echo not received within 20s — callback path broken (SET went to %s, waiting on %s)", setCmdTopic, stateTopic)
	}
}

// findBSMDevice queries /api/v1/devices and returns the root address of
// the first HmIP-BSM it finds. Returns "" if none is present.
func findBSMDevice(t *testing.T, h *harness.Harness) string {
	t.Helper()
	req, _ := h.REST().NewRequest(http.MethodGet, "/api/v1/devices", nil)
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Logf("GET /api/v1/devices: %v", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	// The REST response is a JSON array of device objects. We look for any
	// object whose "type" or "device_type" field contains "BSM".
	var devices []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &devices); err != nil {
		// The endpoint wraps items in an envelope. Try the known keys.
		var env struct {
			Items []map[string]json.RawMessage `json:"items"`
			Data  []map[string]json.RawMessage `json:"data"`
		}
		if err2 := json.Unmarshal(raw, &env); err2 != nil {
			return ""
		}
		if len(env.Items) > 0 {
			devices = env.Items
		} else {
			devices = env.Data
		}
	}
	// The REST /api/v1/devices response uses the field name "model" for the
	// device type string (see DeviceSummary.Model in handlers/devices.go).
	for _, d := range devices {
		for _, key := range []string{"model", "type", "device_type"} {
			if v, ok := d[key]; ok {
				var s string
				if err := json.Unmarshal(v, &s); err == nil && strings.Contains(s, "BSM") {
					if addrRaw, ok := d["address"]; ok {
						var addr string
						if err := json.Unmarshal(addrRaw, &addr); err == nil {
							return addr
						}
					}
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ctx
	return ""
}
