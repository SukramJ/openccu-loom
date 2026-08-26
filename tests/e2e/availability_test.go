// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// availabilityCheckInterval is the check_connection cadence for this test.
// The client leaves CONNECTED on the second consecutive failed probe, so the
// outage is visible after roughly two intervals.
const availabilityCheckInterval = 2 * time.Second

// TestE2EAvailabilityFollowsTheCCUAndTheShutdown walks one device's
// availability from the wire to every north-bound surface, across the three
// events that make a daemon lie about it:
//
//  1. A single device the CCU reports as UNREACH. UNREACH, STICKY_UNREACH and
//     CONFIG_PENDING are suppressed by the default visibility rules, and the
//     availability announcement used to sit behind that gate — so the one
//     parameter that carries reachability was the one parameter whose
//     announcement was dropped. Nothing outside the raw value plane learned
//     that a device had died.
//
//  2. The whole CCU disappearing. Nothing moves a client that reached
//     CONNECTED off that state while the CCU is silent — the reconnect path
//     can only run once the CCU answers again — so REST, MQTT and Matter kept
//     reporting every device online for as long as the daemon ran.
//
//  3. A graceful shutdown. A broker discards the Last Will of a client that
//     disconnects cleanly, so `<base>/bridge/status` — the first availability
//     source of every discovery payload the daemon emits — stayed retained at
//     `online` after a normal stop, while a SIGKILL reported it correctly.
//
// Black-box on purpose: all three are properties of how the daemon is
// assembled, and a test that assembles the collaborators itself gets to
// choose a wiring that works.
func TestE2EAvailabilityFollowsTheCCUAndTheShutdown(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{
		AuthMode:                harness.AuthSession,
		EnableMQTT:              true,
		CheckConnectionInterval: availabilityCheckInterval,
	})
	if h.MQTT() == nil {
		t.Fatal("MQTT broker not started")
	}
	// Session auth: the polling below would otherwise pay a bcrypt
	// verification per request.
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Phase 0 — the daemon is up and reports the fleet as reachable.
	if awaitTopic(t, h.MQTT(), "openccu-loom/bridge/status", 45*time.Second, func(_ string, payload []byte) bool {
		return strings.TrimSpace(string(payload)) == "online"
	}) == "" {
		t.Fatal("bridge/status never went online")
	}
	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	addr := firstAvailableDevice(t, devices)
	t.Logf("device under test: %s", addr)

	availFilter := "openccu-loom/ccu-e2e/+/" + addr + "/availability"

	// Phase 1 — one device goes unreachable. The stimulus is the CCU's own
	// UNREACH report on the maintenance channel, i.e. exactly the suppressed
	// parameter.
	if err := h.CCU().V().RPC().SetDeviceUnreachable(addr, true); err != nil {
		t.Fatalf("simulate UNREACH on %s: %v", addr, err)
	}
	if !waitFor(t, 30*time.Second, func() bool { return !deviceAvailable(t, h, addr) }) {
		t.Fatalf("REST still reports %s available after UNREACH", addr)
	}
	if awaitTopic(t, h.MQTT(), availFilter, 30*time.Second, func(_ string, payload []byte) bool {
		return strings.TrimSpace(string(payload)) == "offline"
	}) == "" {
		t.Fatalf("retained availability topic for %s never went offline after UNREACH", addr)
	}

	// …and back. A device that recovers has to be usable again, so the same
	// path must carry the rising edge too.
	if err := h.CCU().V().RPC().SetDeviceUnreachable(addr, false); err != nil {
		t.Fatalf("clear UNREACH on %s: %v", addr, err)
	}
	if !waitFor(t, 30*time.Second, func() bool { return deviceAvailable(t, h, addr) }) {
		t.Fatalf("REST still reports %s unavailable after UNREACH cleared", addr)
	}
	if awaitTopic(t, h.MQTT(), availFilter, 30*time.Second, func(_ string, payload []byte) bool {
		return strings.TrimSpace(string(payload)) == "online"
	}) == "" {
		t.Fatalf("retained availability topic for %s never went back online", addr)
	}

	// Phase 2 — the CCU itself disappears. No further wire traffic arrives,
	// so nothing rides a value change: the interface's own liveness probe is
	// the only thing that can notice.
	if err := h.CCU().Stop(); err != nil {
		t.Fatalf("stop mock CCU: %v", err)
	}
	t.Log("mock CCU stopped")

	outage := 20*availabilityCheckInterval + 20*time.Second
	if !waitFor(t, outage, func() bool { return !anyInterfaceConnected(t, h) }) {
		t.Fatalf("GET /api/v1/interfaces still reports connected=true %s after the CCU died", outage)
	}
	if !waitFor(t, 30*time.Second, func() bool { return !deviceAvailable(t, h, addr) }) {
		t.Fatalf("GET /api/v1/devices still reports %s available after the CCU died", addr)
	}
	if awaitTopic(t, h.MQTT(), availFilter, 30*time.Second, func(_ string, payload []byte) bool {
		return strings.TrimSpace(string(payload)) == "offline"
	}) == "" {
		t.Fatalf("retained availability topic for %s never went offline after the CCU died", addr)
	}

	// Phase 3 — a graceful stop. Harness.Stop signals SIGTERM, which is a
	// clean MQTT DISCONNECT: the broker drops the will, so the daemon has to
	// retract the marker itself.
	offline := make(chan struct{}, 1)
	if err := h.MQTT().Subscribe("openccu-loom/bridge/status", func(_ string, payload []byte, _ bool) {
		if strings.TrimSpace(string(payload)) == "offline" {
			select {
			case offline <- struct{}{}:
			default:
			}
		}
	}); err != nil {
		t.Fatalf("subscribe bridge/status: %v", err)
	}
	h.Stop()
	select {
	case <-offline:
	case <-time.After(20 * time.Second):
		t.Fatal("bridge/status never went offline after a graceful shutdown")
	}
}

// firstAvailableDevice returns the address of a device the daemon currently
// reports as reachable. Without one the phases below would assert a
// transition that has nothing to transition from.
func firstAvailableDevice(t *testing.T, devices []any) string {
	t.Helper()
	for _, item := range devices {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		address, _ := entry["address"].(string)
		available, _ := entry["available"].(bool)
		if address != "" && available {
			return address
		}
	}
	t.Fatal("no device reported as available after boot")
	return ""
}

// deviceAvailable reads one device's `available` flag off GET /devices/{addr}.
func deviceAvailable(t *testing.T, h *harness.Harness, address string) bool {
	t.Helper()
	body, status, err := getBody(h, "/api/v1/devices/"+address)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /api/v1/devices/%s: status=%d err=%v", address, status, err)
	}
	var payload struct {
		Available bool `json:"available"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode device %s: %v", address, err)
	}
	return payload.Available
}

// anyInterfaceConnected reports whether GET /interfaces still claims any
// interface is connected.
func anyInterfaceConnected(t *testing.T, h *harness.Harness) bool {
	t.Helper()
	body, status, err := getBody(h, "/api/v1/interfaces")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /api/v1/interfaces: status=%d err=%v", status, err)
	}
	var entries []struct {
		ID        string `json:"id"`
		Connected bool   `json:"connected"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("decode interfaces: %v", err)
	}
	for _, e := range entries {
		if e.Connected {
			return true
		}
	}
	return false
}

func getBody(h *harness.Harness, path string) ([]byte, int, error) {
	req, err := h.REST().NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, deadline time.Duration, cond func() bool) bool {
	t.Helper()
	until := time.Now().Add(deadline)
	for {
		if cond() {
			return true
		}
		if time.Now().After(until) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}
