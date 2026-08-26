// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

// event_groups_test.go — pins the producer behind
// GET /devices/{addr}/channels/{no}/event-groups.
//
// The model layer for channel event groups was complete on both ends: a
// channel could hold event sources, `FinalizeInit` grouped them by kind, and
// the REST handler rendered the groups. Nothing in between ever attached a
// source, and nothing constructed one, so the route answered `[]` for every
// channel of every device — structurally, not because a fleet happened to
// lack keypress devices. Its unit tests passed throughout, because they
// attached the sources themselves.
//
// This test therefore constructs nothing. It boots the real daemon, lets the
// real device ingestion run against the simulated CCU, and asks the route the
// question an operator's client would ask.

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// expectedEventGroupKinds are the kinds the default fleet must produce.
//
// Both are asserted because they arrive by different routes through the
// classifier: keypress from an exact parameter-name match (PRESS_*), device
// error from a prefix match (ERROR*). A producer wired for one and not the
// other would still satisfy a test that only asked whether anything appeared.
//
// Impulse (SEQUENCE_OK) is deliberately absent: no device in the default
// fleet carries that parameter, so requiring it would pin a property of the
// fleet rather than of the daemon.
var expectedEventGroupKinds = []string{"device_error", "keypress"}

func TestE2EEventGroupsAreProducedDuringDeviceIngestion(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	if len(devices) == 0 {
		t.Fatalf("no devices after boot — this test would report a missing producer it cannot "+
			"distinguish from a broken harness. Fleet: %v", harness.DefaultDevices)
	}

	// Channels are addressed by the CCU's own device ids, which the
	// simulator assigns per run, so the fleet is walked rather than named.
	found := map[string][]string{}
	for _, raw := range devices {
		dev, _ := raw.(map[string]any)
		address, _ := dev["address"].(string)
		if address == "" {
			continue
		}
		for _, no := range channelNumbersOf(t, h, address) {
			path := fmt.Sprintf("/api/v1/devices/%s/channels/%d/event-groups", address, no)
			groups, err := fetchJSONArray(h, path, "")
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			for _, g := range groups {
				group, _ := g.(map[string]any)
				kind, _ := group["kind"].(string)
				found[kind] = append(found[kind], fmt.Sprintf("%s:%d", address, no))
			}
		}
	}

	if len(found) == 0 {
		t.Fatalf("every channel of every device reports an empty event-group list. The route "+
			"cannot answer anything else unless device ingestion attaches event sources, so a "+
			"keypress, an impulse or a device error never reaches a client through this "+
			"surface. Fleet: %v", harness.DefaultDevices)
	}

	for _, kind := range expectedEventGroupKinds {
		if len(found[kind]) == 0 {
			t.Errorf("no channel reports an event group of kind %q, although the fleet contains "+
				"devices whose VALUES paramset carries such parameters; kinds found: %v",
				kind, sortedKinds(found))
		}
	}
}

// channelNumbersOf lists a device's channel numbers.
func channelNumbersOf(t *testing.T, h *harness.Harness, address string) []int {
	t.Helper()
	path := fmt.Sprintf("/api/v1/devices/%s/channels", address)
	arr, err := fetchJSONArray(h, path, "")
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	out := make([]int, 0, len(arr))
	for _, raw := range arr {
		ch, _ := raw.(map[string]any)
		if no, ok := ch["number"].(float64); ok {
			out = append(out, int(no))
		}
	}
	return out
}

// sortedKinds renders the kinds found, for a failure message that says what
// the daemon did produce rather than only what it did not.
func sortedKinds(found map[string][]string) []string {
	out := make([]string, 0, len(found))
	for kind, channels := range found {
		out = append(out, fmt.Sprintf("%s (%d channels)", kind, len(channels)))
	}
	sort.Strings(out)
	return out
}

// TestE2EEventGroupRecordsTheTriggerThatFired pins the other half: a trigger
// the CCU pushes must land on the channel's event group, which is what
// `last_triggered_event` reports.
//
// The two halves fail independently and look alike from the route. Without
// the producer every group is missing; without the feed every group is
// present and permanently reports no trigger — a fleet whose buttons nobody
// has pressed. Only pressing one tells them apart, so this test presses one.
func TestE2EEventGroupRecordsTheTriggerThatFired(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	address, channelNo, parameter, path := findKeypressSource(t, h, devices)

	// Before: the group exists and has never fired. Asserting this is what
	// keeps the check below from passing on a value that was already there.
	if got := lastTriggeredParameter(t, h, path); got != "" {
		t.Fatalf("%s already reports last_triggered_event %q before anything was pressed",
			path, got)
	}

	channelAddress := fmt.Sprintf("%s:%d", address, channelNo)
	if err := h.CCU().V().SimulateDeviceEvent(channelAddress, parameter, true); err != nil {
		t.Fatalf("SimulateDeviceEvent %s %s: %v", channelAddress, parameter, err)
	}

	// The push travels CCU → callback server → event coordinator → bus →
	// feed → model, so it is observed rather than awaited.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if got := lastTriggeredParameter(t, h, path); got != "" {
			// The summary reports the lowercased event-type token, matching
			// its `event_types` sibling, while `parameters` carries the
			// upper-case CCU names — so the comparison is case-insensitive
			// by design, not by convenience.
			if !strings.EqualFold(got, parameter) {
				t.Fatalf("%s recorded %q, want the parameter that fired (%q)", path, got, parameter)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still reports no last_triggered_event after %s fired on %s — the trigger "+
				"reaches every north-bound surface, but nothing feeds it back into the model, so "+
				"a client can enumerate the event entities and never learn one was pressed",
				path, parameter, channelAddress)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// findKeypressSource returns a device address, channel, keypress parameter
// and the event-groups route of the first keypress group in the fleet.
func findKeypressSource(
	t *testing.T, h *harness.Harness, devices []any,
) (address string, channelNo int, parameter, path string) {
	t.Helper()
	for _, raw := range devices {
		dev, _ := raw.(map[string]any)
		addr, _ := dev["address"].(string)
		if addr == "" {
			continue
		}
		for _, no := range channelNumbersOf(t, h, addr) {
			p := fmt.Sprintf("/api/v1/devices/%s/channels/%d/event-groups", addr, no)
			groups, err := fetchJSONArray(h, p, "")
			if err != nil {
				t.Fatalf("GET %s: %v", p, err)
			}
			for _, g := range groups {
				group, _ := g.(map[string]any)
				if kind, _ := group["kind"].(string); kind != "keypress" {
					continue
				}
				params, _ := group["parameters"].([]any)
				if len(params) == 0 {
					continue
				}
				name, _ := params[0].(string)
				if name == "" {
					continue
				}
				return addr, no, name, p
			}
		}
	}
	t.Fatalf("no keypress event group in the fleet — the producer is gone, which "+
		"TestE2EEventGroupsAreProducedDuringDeviceIngestion covers. Fleet: %v",
		harness.DefaultDevices)
	return "", 0, "", ""
}

// lastTriggeredParameter returns the parameter of the group's recorded
// trigger, or "" when none has fired yet.
func lastTriggeredParameter(t *testing.T, h *harness.Harness, path string) string {
	t.Helper()
	groups, err := fetchJSONArray(h, path, "")
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	for _, g := range groups {
		group, _ := g.(map[string]any)
		last, ok := group["last_triggered_event"].(map[string]any)
		if !ok {
			continue
		}
		if p, _ := last["parameter"].(string); p != "" {
			return p
		}
	}
	return ""
}

// TestE2EDeviceErrorTriggerReachesItsEventGroup is the same check for the
// device-error kind, which travels a different route than a keypress.
//
// A keypress parameter is writable, so device ingestion gives it a data
// point. An ERROR* parameter deliberately gets none — the resolver drops it,
// mirroring the reference, because it is an event and not a state. The
// callback path then has to carry it anyway, and that is the part worth
// pinning: everything else about the two kinds looks identical from the
// model, so a delivery gap that affects only one of them is invisible unless
// a test names it.
func TestE2EDeviceErrorTriggerReachesItsEventGroup(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	address, channelNo, path := findDeviceErrorSource(t, h, devices)

	if got := lastTriggeredParameter(t, h, path); got != "" {
		t.Fatalf("%s already reports last_triggered_event %q before any fault was reported",
			path, got)
	}

	// ERROR_CODE is an INTEGER on every fleet device that has it; a non-zero
	// value is an active fault, which is what the source's transition gate
	// reacts to.
	channelAddress := fmt.Sprintf("%s:%d", address, channelNo)
	if err := h.CCU().V().SimulateDeviceEvent(channelAddress, deviceErrorParameter, 5); err != nil {
		t.Fatalf("SimulateDeviceEvent %s %s: %v", channelAddress, deviceErrorParameter, err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		if got := lastTriggeredParameter(t, h, path); got != "" {
			if !strings.EqualFold(got, deviceErrorParameter) {
				t.Fatalf("%s recorded %q, want %q", path, got, deviceErrorParameter)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s reports no last_triggered_event after %s=5 on %s — a device fault "+
				"reaches no north-bound surface at all: the parameter has no data point by "+
				"design, and the callback path drops an event whose parameter has none",
				path, deviceErrorParameter, channelAddress)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// deviceErrorParameter is the fault parameter the pin drives. Every
// device-error group in the default fleet carries it.
const deviceErrorParameter = "ERROR_CODE"

// findDeviceErrorSource returns the first channel carrying a device-error
// group that includes [deviceErrorParameter], plus its event-groups route.
func findDeviceErrorSource(
	t *testing.T, h *harness.Harness, devices []any,
) (address string, channelNo int, path string) {
	t.Helper()
	for _, raw := range devices {
		dev, _ := raw.(map[string]any)
		addr, _ := dev["address"].(string)
		if addr == "" {
			continue
		}
		for _, no := range channelNumbersOf(t, h, addr) {
			p := fmt.Sprintf("/api/v1/devices/%s/channels/%d/event-groups", addr, no)
			groups, err := fetchJSONArray(h, p, "")
			if err != nil {
				t.Fatalf("GET %s: %v", p, err)
			}
			for _, g := range groups {
				group, _ := g.(map[string]any)
				if kind, _ := group["kind"].(string); kind != "device_error" {
					continue
				}
				params, _ := group["parameters"].([]any)
				for _, raw := range params {
					if name, _ := raw.(string); name == deviceErrorParameter {
						return addr, no, p
					}
				}
			}
		}
	}
	t.Fatalf("no device-error group carrying %s in the fleet: %v",
		deviceErrorParameter, harness.DefaultDevices)
	return "", 0, ""
}
