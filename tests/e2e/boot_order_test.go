// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EDaemonLevelSubsystemsReportNonEmptyStateAfterBoot asserts that
// every daemon-level subsystem derived from the device model holds real
// state once the daemon has booted against a CCU that answers.
//
// It is a black-box test on purpose. The defect it guards against is a
// boot-ordering one, and boot order is a property of the composition
// root: the daemon launches the south-bound bring-up and continues, the
// bring-up returns immediately, and the device model is filled by a
// goroutine that only finishes once the CCU has replied. A subsystem
// that reads the model once, at its own Start, therefore reads nothing —
// permanently. Any test that assembles the collaborators itself gets to
// choose the order and will choose the working one.
//
// That is not hypothetical. The Security & Safety domain shipped with a
// classification index built exactly once, in Start, against a registry
// that was still empty: every wire event was discarded at the first
// lookup, no class ever became active, no fault ever opened, and Start
// logged success throughout. Seven green PRs and an integration test
// that registered a fully loaded central *before* Start — the exact
// inverse of production — kept it invisible for months.
//
// Liveness is not the property under test. `TestE2EDaemonBoot` already
// asserts the daemon answers; an inert subsystem answers too, with 200
// and an empty payload. The assertion here is that the payload has
// something in it.
//
// The CCU deliberately boots NOT ready, and this is the load-bearing
// part of the setup rather than extra realism. Against a CCU that
// answers instantly the daemon happens to finish the south-bound
// bring-up before the domain services start, so every subsystem reads a
// populated model and the test passes no matter how the wiring is
// broken — verified by removing the rebuild trigger and watching it stay
// green. Gating the CCU restores the real order: the services start
// against an empty registry, the devices arrive afterwards, and only a
// subsystem that reacts to that arrival ends up with state.
func TestE2EDaemonLevelSubsystemsReportNonEmptyStateAfterBoot(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{StartCCUNotReady: true, EnableMQTT: true})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	// The services are up and the registry is empty — the exact state in
	// which the Security & Safety domain built its one and only index.
	if items := getJSONArrayOnce(t, h, "/api/v1/devices", "items"); len(items) != 0 {
		t.Fatalf("expected 0 devices while the CCU is still booting, got %d — the readiness gate "+
			"did not hold, so this test is no longer reproducing the production order", len(items))
	}

	// The CCU finishes booting. Everything below must follow from this.
	h.CCU().V().SetReady(true)

	// The anchor runs first and separately. Every other subsystem in the
	// table derives its state from the device model, so an empty model
	// would make all of them fail for one reason — and, worse, a model
	// that silently stopped loading would turn this whole file into a
	// test of the harness rather than of the daemon.
	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	if len(devices) == 0 {
		t.Fatalf("the device model is empty after boot; every subsystem below derives from it, "+
			"so this test would report a daemon-wide defect it cannot distinguish from a broken "+
			"harness. Fleet: %v", harness.DefaultDevices)
	}

	// MQTT discovery is checked here rather than in the table: it is the
	// one subsystem whose state is not readable over REST, and it is the
	// one an operator notices first — an empty discovery plane means Home
	// Assistant shows no entities at all.
	t.Run("mqtt_discovery", func(t *testing.T) {
		t.Parallel()
		if h.MQTT() == nil {
			t.Fatal("MQTT broker not started although EnableMQTT was set")
		}
		seen := make(chan string, 1)
		if err := h.MQTT().Subscribe("homeassistant/+/+/+/config", func(topic string, _ []byte, _ bool) {
			select {
			case seen <- topic:
			default:
			}
		}); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		select {
		case <-seen:
		case <-time.After(45 * time.Second):
			t.Fatal("no HA discovery config was published after the CCU became ready — the " +
				"discovery publisher read the device model once, before it was populated, so " +
				"Home Assistant is shown an installation with no entities at all")
		}
	})

	for _, sub := range bootOrderSubsystems {
		t.Run(sub.name, func(t *testing.T) {
			t.Parallel()
			got := getJSONArray(t, h, sub.path, sub.field)
			if len(got) == 0 {
				t.Fatalf("%s: %s%s is empty after boot — %s",
					sub.name, sub.path, fieldSuffix(sub.field), sub.symptom)
			}
		})
	}
}

// bootOrderSubsystem is one daemon-level subsystem whose state is
// derived from the device model and must therefore be populated once
// the CCU has answered.
type bootOrderSubsystem struct {
	// name is the subsystem, not the endpoint.
	name string
	// path is the REST route that exposes its state.
	path string
	// field names the array inside an object response; empty means the
	// response is the array itself.
	field string
	// symptom is what an operator experiences when this is empty. It is
	// the part of the failure message worth reading.
	symptom string
}

// bootOrderSubsystems lists the subsystems whose emptiness is a defect.
//
// Membership has one rule: the subsystem's state must follow from the
// device model alone. A subsystem whose state also needs operator
// configuration is deliberately absent, because "empty" is its correct
// answer on a fresh boot and asserting otherwise would be a test that
// lies. Those are named below the table rather than silently omitted.
var bootOrderSubsystems = []bootOrderSubsystem{
	{
		name:  "security_inventory",
		path:  "/api/v1/security/sources",
		field: "",
		symptom: "the classification index was built against an empty registry and never rebuilt, " +
			"so every wire event is discarded at the first lookup and the whole classification " +
			"half of the domain is inert while Start logs success",
	},
	{
		name:  "security_classes",
		path:  "/api/v1/security",
		field: "classes",
		symptom: "no hazard class is known although the fleet contains a smoke detector — the " +
			"domain would report `ok` through a fire",
	},
	{
		name:  "hub_data_points",
		path:  "/api/v1/hub/data-points",
		field: "",
		symptom: "the hub coordinator holds no model, which is how the hub notifiers went dead " +
			"in 0.52.12: SetHubModel had no production caller and every hub push was lost",
	},
}

// Deliberately absent, and why — an empty answer is correct for these on
// a fresh boot, so asserting non-emptiness would pin a lie:
//
//   - /api/v1/alarm/zones — a zone exists only once an operator creates
//     one. The alarm engine's own boot order is covered by its
//     integration tests.
//   - /api/v1/security/faults — a fault stands only when something is
//     actually wrong; the simulated fleet is healthy.
//   - /api/v1/security → zones — populated from the alarm engine, which
//     is disabled in this harness.
//   - Matter — off by default (cfg.North.Matter.Enabled), so there is no
//     bridge to report state. Covering it needs the harness to enable it,
//     which is its own piece of work.
//
// Adding a subsystem here whose state needs configuration means seeding
// that configuration first, in the production order, rather than
// relaxing the assertion.

// getJSONArray fetches path and returns the array at field, or the whole
// body when field is empty.
func getJSONArray(t *testing.T, h *harness.Harness, path, field string) []any {
	t.Helper()
	// The south-bound bring-up is asynchronous by design, so a subsystem
	// may legitimately still be filling when the north-bound surface
	// answers. Polling distinguishes "not yet" from "never" — without it
	// this test would be a race that fails on a slow runner and passes on
	// a fast one, which is worse than no test.
	deadline := time.Now().Add(45 * time.Second)
	var last []any
	var lastErr error
	for {
		arr, err := fetchJSONArray(h, path, field)
		if err == nil && len(arr) > 0 {
			return arr
		}
		last, lastErr = arr, err
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("GET %s: %v", path, lastErr)
	}
	return last
}

// getJSONArrayOnce reads once, without polling. Used for the
// precondition, where waiting would defeat the assertion.
func getJSONArrayOnce(t *testing.T, h *harness.Harness, path, field string) []any {
	t.Helper()
	arr, err := fetchJSONArray(h, path, field)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return arr
}

func fetchJSONArray(h *harness.Harness, path, field string) ([]any, error) {
	req, err := h.REST().NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	if field == "" {
		var arr []any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("body is not a JSON array: %w", err)
		}
		return arr, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("body is not a JSON object: %w", err)
	}
	arr, ok := obj[field].([]any)
	if !ok {
		return nil, fmt.Errorf("field %q is missing or not an array in %s", field, body)
	}
	return arr, nil
}

func fieldSuffix(field string) string {
	if field == "" {
		return ""
	}
	return " → " + field
}
