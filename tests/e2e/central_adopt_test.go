// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// adoptedCentralName is the CCU this file adds at runtime. It must not
// collide with the harness's boot central (`ccu-e2e`).
const adoptedCentralName = "adopted-e2e"

// TestE2EAdoptedCentralKeepsItsSchedulerRunningAfterTheRequestCompletes
// asserts that a CCU adopted at runtime still runs its periodic jobs once
// the HTTP request that adopted it has finished.
//
// The defect it guards against is a context-lifetime one. The adopt path
// starts the new central's scheduler, and the scheduler makes the context it
// is handed the parent of every job goroutine. Every caller of that path is
// an HTTP handler — POST/PUT /api/v1/centrals and the first-run setup wizard
// — and net/http cancels the request context the instant the response is
// written. Handed that context, every one of the central's jobs exits
// before the operator's browser has rendered the 201: the health heartbeat,
// the hub program / sysvar / inbox / service-message / alarm-message /
// system-update / install-mode refreshes, the firmware checks and the
// reconcile pass. Nothing restarts them — Scheduler.Start refuses a second
// call and the boot-time StartAll has long since run — so only a daemon
// restart recovers, and live adopt is explicitly a restart-free operation.
//
// Nothing about it looks broken from the outside. The south-bound bring-up
// runs on the bring-up manager's own context, so the CCU connects, its
// devices load and its push events keep arriving. What stops is every
// scheduled read: the hub data freezes at its bring-up values and the
// central's health decays to unknown roughly ninety seconds later.
//
// It is a black-box test on purpose, and that is the load-bearing part of
// the setup rather than extra realism. The context is chosen by the
// composition root, so any test that calls the adopt path itself gets to
// pick the context and will pick the working one — the twelve in-process
// adopt tests all pass a test-lifetime context and stayed green throughout.
// Only a real HTTP request against the built binary reproduces the
// cancellation that production performs.
//
// The evidence is the per-central `scheduler` health component, which
// nothing but the `central.health_heartbeat` job creates and which
// therefore cannot exist unless that job has ticked at least once after
// the response was read. The boot central is checked first as the control:
// it proves the component name and the diagnostics surface are right, so a
// failure below can only mean the adopted central's jobs are dead.
func TestE2EAdoptedCentralKeepsItsSchedulerRunningAfterTheRequestCompletes(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	// A second simulator, so the adopted central owns its own CCU rather
	// than sharing callback state with the boot one.
	ccu2 := harness.StartCCU(t, nil)

	row := map[string]any{
		"name":          adoptedCentralName,
		"host":          "127.0.0.1",
		"port":          ccu2.XMLRPCPort(),
		"json_rpc_port": ccu2.JSONRPCPort(),
		"username":      "Admin",
		"interfaces":    []map[string]any{{"name": "HmIP-RF"}},
		"enabled":       true,
	}
	body, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal central row: %v", err)
	}
	req, err := h.REST().NewRequest(http.MethodPost, "/api/v1/centrals", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build adopt request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/centrals: %v", err)
	}
	respBody, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read adopt response: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close adopt response: %v", closeErr)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/v1/centrals: status %d, body %s", resp.StatusCode, respBody)
	}
	// The response is fully read and the connection released: on the daemon
	// side ServeHTTP has returned and the request context is cancelled. Every
	// assertion below is about what survives that.

	// Control: the boot central, whose scheduler runs on the daemon context.
	// If this is missing the failure is in the harness or the component name,
	// not in the adopt path.
	awaitHealthComponent(t, h, "ccu-e2e/scheduler",
		"the BOOT central has no scheduler health component either, so this test is not "+
			"measuring what it claims — check the diagnostics surface and the component name "+
			"before reading the adopted-central failure below")

	awaitHealthComponent(t, h, adoptedCentralName+"/scheduler",
		"the adopted central's health heartbeat never ticked after the adopting request "+
			"completed. Its scheduler was started on the request context, so every periodic "+
			"job for that CCU died when the response was written: hub programs, system "+
			"variables, the inbox, service and alarm messages, the system-update state and "+
			"install mode all stop being refreshed, the firmware and reconcile passes never "+
			"run, and the CCU's health decays to unknown — until the daemon is restarted")
}

// awaitHealthComponent polls the diagnostics dump until a health component
// named `name` appears, and fails with `symptom` when it never does.
//
// The heartbeat that creates the per-central scheduler component runs once a
// minute and deliberately not at start, so the wait is bounded well above one
// interval: a shorter budget would make this a race that passes on an idle
// machine and fails under parallel CI load, which is worse than no test.
func awaitHealthComponent(t *testing.T, h *harness.Harness, name, symptom string) {
	t.Helper()
	const budget = 120 * time.Second
	deadline := time.Now().Add(budget)
	var lastErr error
	for {
		names, err := healthComponentNames(h)
		if err == nil {
			for _, got := range names {
				if got == name {
					return
				}
			}
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("GET /api/v1/diagnostics: %v", lastErr)
	}
	t.Fatalf("health component %q did not appear within %s — %s", name, budget, symptom)
}

// healthComponentNames returns every component name in the diagnostics
// dump. Per-central components are scoped as `<central>/<component>`.
func healthComponentNames(h *harness.Harness) ([]string, error) {
	req, err := h.REST().NewRequest(http.MethodGet, "/api/v1/diagnostics", nil)
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
	var env struct {
		Health struct {
			Components []struct {
				Name string `json:"name"`
			} `json:"components"`
		} `json:"health"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode diagnostics envelope: %w", err)
	}
	out := make([]string, 0, len(env.Health.Components))
	for _, c := range env.Health.Components {
		out = append(out, c.Name)
	}
	return out, nil
}
