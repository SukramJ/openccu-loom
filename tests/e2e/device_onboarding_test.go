// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// hotPlugModel is the device type paired while the daemon runs. It is not
// part of harness.DefaultDevices, so its arrival is unambiguous.
const hotPlugModel = "HmIP-eTRV-2"

// TestE2EDeviceOnboardingHoldsNewDevicesByDefault pins the first-start
// guarantee of the default-on new-device hold (ADR 0082) on a fresh
// installation with no behaviour configured: the fleet the system already
// has is built and never presented as waiting, while a device paired
// afterwards waits to be accepted and then to be released.
//
// The simulated system boots NOT ready, so the daemon's north-bound surface
// is up and its model empty when the devices arrive — the production order.
// The first half is the dangerous one: the daemon answers listDevices with
// an empty list, so a CCU re-announces its whole inventory after every
// init, and a hold that did not recognise that announcement as known
// devices would park the entire fleet on the first start after an upgrade.
func TestE2EDeviceOnboardingHoldsNewDevicesByDefault(t *testing.T) {
	t.Parallel()
	for _, backend := range []harness.Backend{harness.BackendCCU, harness.BackendOpenCCULite} {
		t.Run(backend.String(), func(t *testing.T) {
			t.Parallel()
			h := bootOnboardingDaemon(t, backend, nil)

			addr := hotPlugAndAwaitPending(t, h, backend)

			// Waiting to be accepted: not built, so no device to list.
			if dev := deviceByAddress(t, h, addr); dev != nil {
				t.Fatalf("%s is in GET /devices while it waits to be accepted: %v", addr, dev)
			}
			// The fleet stays unheld after the hot-plug, too.
			assertNoFleetDeviceHeld(t, h)

			if status := postDeviceAction(t, h, addr, "accept"); status != http.StatusAccepted {
				t.Fatalf("POST /devices/%s/accept: status %d, want %d", addr, status, http.StatusAccepted)
			}

			// Waiting to be released: built and listed for configuration,
			// flagged unreleased, and still on the inbox as a different ask.
			if !waitFor(t, 30*time.Second, func() bool {
				dev := deviceByAddress(t, h, addr)
				return dev != nil && dev["released"] == false
			}) {
				t.Fatalf("%s did not reach the model as unreleased after the accept; got %v",
					addr, deviceByAddress(t, h, addr))
			}
			entry := inboxEntry(t, h, addr)
			if entry == nil || entry["awaiting_release"] != true || entry["pending_creation"] == true {
				t.Fatalf("inbox entry for %s after the accept = %v, want awaiting_release without pending_creation",
					addr, entry)
			}
			if listed := releasedOnlyAddresses(t, h); listed[addr] {
				t.Fatalf("%s is listed by GET /devices?released_only=true before it was released", addr)
			}

			if status := postDeviceAction(t, h, addr, "release"); status != http.StatusNoContent {
				t.Fatalf("POST /devices/%s/release: status %d, want %d", addr, status, http.StatusNoContent)
			}
			if !waitFor(t, 15*time.Second, func() bool { return inboxEntry(t, h, addr) == nil }) {
				t.Fatalf("%s is still on the inbox after the release: %v", addr, inboxEntry(t, h, addr))
			}
			if dev := deviceByAddress(t, h, addr); dev == nil || dev["released"] != true {
				t.Fatalf("%s after the release = %v, want a released device", addr, dev)
			}
		})
	}
}

// TestE2EDeviceOnboardingHoldOffBuildsNewDevicesImmediately is the other
// value of the same setting: with the hold switched off a hot-plugged
// device is built and released at once and never listed as waiting.
func TestE2EDeviceOnboardingHoldOffBuildsNewDevicesImmediately(t *testing.T) {
	t.Parallel()
	h := bootOnboardingDaemon(t, harness.BackendCCU, new(false))

	hotPlug(t, h, harness.BackendCCU)

	var addr string
	if !waitFor(t, 30*time.Second, func() bool {
		addr = deviceAddressOfModel(t, h, hotPlugModel)
		return addr != ""
	}) {
		t.Fatalf("the hot-plugged %s was not built with the hold off", hotPlugModel)
	}
	if dev := deviceByAddress(t, h, addr); dev["released"] != true {
		t.Fatalf("%s with the hold off = %v, want released", addr, dev)
	}
	// The device is built, so a pending entry would have been recorded by
	// now; give a late one a moment before concluding there is none.
	time.Sleep(2 * time.Second)
	for _, e := range inboxEntries(t, h) {
		if e["address"] == addr || e["pending_creation"] == true || e["awaiting_release"] == true {
			t.Fatalf("inbox lists %v with the hold off", e)
		}
	}
}

// bootOnboardingDaemon starts a daemon on a fresh data dir against a system
// that is still booting, flips the system ready, and returns once the whole
// fleet is in the model. hold nil leaves the setting unconfigured.
func bootOnboardingDaemon(t *testing.T, backend harness.Backend, hold *bool) *harness.Harness {
	t.Helper()
	h := harness.Start(t, harness.Options{
		StartCCUNotReady:       true,
		Backend:                backend,
		DelayNewDeviceCreation: hold,
	})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	if items := getJSONArrayOnce(t, h, "/api/v1/devices", "items"); len(items) != 0 {
		t.Fatalf("expected 0 devices while the system is still booting, got %d — the readiness "+
			"gate did not hold, so this test no longer reproduces the production order", len(items))
	}
	h.SetCCUReady(true)

	if !waitFor(t, 45*time.Second, func() bool { return missingFleetModels(t, h) == nil }) {
		t.Fatalf("fleet models missing from GET /devices after bring-up: %v", missingFleetModels(t, h))
	}
	if backend == harness.BackendCCU {
		// The CCU's post-init inventory announcement is the event that
		// would park the fleet. Wait until the daemon has handled it, or
		// an empty inbox below would only mean "not yet".
		if !waitFor(t, 30*time.Second, func() bool {
			return strings.Contains(h.Logs(), `"msg":"callback.new_devices",`)
		}) {
			t.Fatal("the CCU's post-init newDevices announcement never reached the daemon")
		}
		time.Sleep(time.Second)
	} else if !waitFor(t, 30*time.Second, func() bool { return h.Lite().OpenStreams() > 0 }) {
		// An openccu-lite box announces a pairing on its event stream, and
		// the stream attaches on its own retry schedule after the box
		// becomes ready. A hot-plug before it attached would be a test of
		// the retry timing, not of the hold.
		t.Fatal("the daemon never attached to the openccu-lite event stream")
	}
	assertNoFleetDeviceHeld(t, h)
	return h
}

// assertNoFleetDeviceHeld fails when any inbox entry is held by the daemon.
// Before a hot-plug every entry would be a fleet device; afterwards only
// the hot-plugged model may be held.
func assertNoFleetDeviceHeld(t *testing.T, h *harness.Harness) {
	t.Helper()
	for _, e := range inboxEntries(t, h) {
		if model, _ := e["model"].(string); strings.EqualFold(model, hotPlugModel) {
			continue
		}
		if e["pending_creation"] == true || e["awaiting_release"] == true {
			t.Fatalf("a device the system already had is listed as held on GET /inbox: %v", e)
		}
	}
}

// hotPlug pairs hotPlugModel on the running simulator, which pushes the
// announcement to the daemon as a real system would.
func hotPlug(t *testing.T, h *harness.Harness, backend harness.Backend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	if backend == harness.BackendOpenCCULite {
		err = h.Lite().V().InterfaceRPC("HmIP-RF").AddDevices(ctx, []string{hotPlugModel})
	} else {
		err = h.CCU().V().RPC().AddDevices(ctx, []string{hotPlugModel})
	}
	if err != nil {
		t.Fatalf("AddDevices(%s): %v", hotPlugModel, err)
	}
}

// hotPlugAndAwaitPending pairs a device and returns its address once the
// inbox lists it as waiting to be accepted.
func hotPlugAndAwaitPending(t *testing.T, h *harness.Harness, backend harness.Backend) string {
	t.Helper()
	if addr := deviceAddressOfModel(t, h, hotPlugModel); addr != "" {
		t.Fatalf("%s is already in the model before it was paired (%s)", hotPlugModel, addr)
	}
	hotPlug(t, h, backend)
	var entry map[string]any
	if !waitFor(t, 30*time.Second, func() bool {
		for _, e := range inboxEntries(t, h) {
			if model, _ := e["model"].(string); strings.EqualFold(model, hotPlugModel) && e["pending_creation"] == true {
				entry = e
				return true
			}
		}
		return false
	}) {
		t.Fatalf("the hot-plugged %s is not listed on GET /inbox as pending_creation; inbox: %v",
			hotPlugModel, inboxEntries(t, h))
	}
	addr, _ := entry["address"].(string)
	if addr == "" {
		t.Fatalf("pending inbox entry carries no address: %v", entry)
	}
	return addr
}

func inboxEntries(t *testing.T, h *harness.Harness) []map[string]any {
	t.Helper()
	body, status, err := getBody(h, "/api/v1/inbox")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /inbox: status %d err %v body %s", status, err, body)
	}
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode inbox: %v (%s)", err, body)
	}
	return out
}

func inboxEntry(t *testing.T, h *harness.Harness, addr string) map[string]any {
	t.Helper()
	for _, e := range inboxEntries(t, h) {
		if a, _ := e["address"].(string); strings.EqualFold(a, addr) {
			return e
		}
	}
	return nil
}

// allDevices reads the full device list. per_page is set so a growing
// fleet cannot push the device under test onto a page this never reads.
func allDevices(t *testing.T, h *harness.Harness, query string) []map[string]any {
	t.Helper()
	body, status, err := getBody(h, "/api/v1/devices?per_page=500"+query)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /devices: status %d err %v body %s", status, err, body)
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode devices: %v (%s)", err, body)
	}
	return out.Items
}

func deviceByAddress(t *testing.T, h *harness.Harness, addr string) map[string]any {
	t.Helper()
	for _, d := range allDevices(t, h, "") {
		if a, _ := d["address"].(string); strings.EqualFold(a, addr) {
			return d
		}
	}
	return nil
}

func deviceAddressOfModel(t *testing.T, h *harness.Harness, model string) string {
	t.Helper()
	for _, d := range allDevices(t, h, "") {
		if m, _ := d["model"].(string); strings.EqualFold(m, model) {
			a, _ := d["address"].(string)
			return a
		}
	}
	return ""
}

func releasedOnlyAddresses(t *testing.T, h *harness.Harness) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, d := range allDevices(t, h, "&released_only=true") {
		if a, _ := d["address"].(string); a != "" {
			out[a] = true
		}
	}
	return out
}

// missingFleetModels returns the harness fleet models not yet in the model.
func missingFleetModels(t *testing.T, h *harness.Harness) []string {
	t.Helper()
	have := map[string]bool{}
	for _, d := range allDevices(t, h, "") {
		if m, _ := d["model"].(string); m != "" {
			have[strings.ToLower(m)] = true
		}
	}
	var missing []string
	for _, m := range harness.DefaultDevices {
		if !have[strings.ToLower(m)] {
			missing = append(missing, m)
		}
	}
	return missing
}

// postDeviceAction calls POST /api/v1/devices/{addr}/{action} with no body.
func postDeviceAction(t *testing.T, h *harness.Harness, addr, action string) int {
	t.Helper()
	path := fmt.Sprintf("/api/v1/devices/%s/%s", url.PathEscape(addr), action)
	req, err := h.REST().NewRequest(http.MethodPost, path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
