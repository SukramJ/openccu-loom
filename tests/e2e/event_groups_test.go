// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	"testing"

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
