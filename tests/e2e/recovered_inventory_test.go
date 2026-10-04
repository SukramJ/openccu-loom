// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EFleetOfAnInterfaceRecoveredAfterAFailedBootPullIsBuilt pins that
// the new-device hold never parks a fleet the daemon has not yet taken
// stock of. On a fresh installation the boot pull of the interface's
// inventory fails for its whole retry budget; the interface is left to
// the recovery pipeline, which reconnects without pulling the inventory
// again — the devices then arrive only as the system's re-announcement
// (a CCU's newDevices after init, an openccu-lite box's reconciliation
// after the announcement). With the hold on by default, that announcement
// must build the fleet, not list it as waiting to be accepted.
func TestE2EFleetOfAnInterfaceRecoveredAfterAFailedBootPullIsBuilt(t *testing.T) {
	t.Parallel()
	for _, backend := range []harness.Backend{harness.BackendCCU, harness.BackendOpenCCULite} {
		t.Run(backend.String(), func(t *testing.T) {
			t.Parallel()
			h := harness.Start(t, harness.Options{StartCCUNotReady: true, Backend: backend})
			if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
				t.Fatalf("login: %v", err)
			}
			breakInventory(t, h, backend, true)
			h.SetCCUReady(true)

			if !waitFor(t, 120*time.Second, func() bool {
				return strings.Contains(h.Logs(), `"msg":"wire.interface.ingest_failed"`)
			}) {
				t.Fatalf("the boot pull did not exhaust its retries; logs:\n%s", recoveryLogLines(h.Logs()))
			}
			if items := getJSONArrayOnce(t, h, "/api/v1/devices", "items"); len(items) != 0 {
				t.Fatalf("expected no devices after the failed boot pull, got %d", len(items))
			}
			breakInventory(t, h, backend, false)

			var parked []map[string]any
			if !waitFor(t, 120*time.Second, func() bool {
				parked = parked[:0]
				for _, e := range inboxEntries(t, h) {
					if e["pending_creation"] == true || e["awaiting_release"] == true {
						parked = append(parked, e)
					}
				}
				return len(parked) > 0 || missingFleetModels(t, h) == nil
			}) {
				t.Fatalf("the interface never recovered: fleet missing %v, inbox %v; logs:\n%s",
					missingFleetModels(t, h), inboxEntries(t, h), recoveryLogLines(h.Logs()))
			}
			if len(parked) > 0 {
				t.Fatalf("the recovered fleet is listed as waiting to be accepted instead of built: %v; logs:\n%s",
					parked, recoveryLogLines(h.Logs()))
			}
			// Built; a late park would still be a defect.
			time.Sleep(2 * time.Second)
			assertNoFleetDeviceHeld(t, h)

			// Building the fleet took stock of the interface, so the hold
			// applies again: a device paired now waits to be accepted.
			addr := hotPlugAndAwaitPending(t, h, backend)
			if dev := deviceByAddress(t, h, addr); dev != nil {
				t.Fatalf("%s paired after the recovery is built although it is held: %v", addr, dev)
			}
			assertNoFleetDeviceHeld(t, h)
		})
	}
}

// breakInventory makes the system answer the inventory read the boot
// pull makes with a fault, or serve it again. An openccu-lite box forwards
// the call to the same simulated interface process, so one rule covers
// both backends while the box itself stays ready.
func breakInventory(t *testing.T, h *harness.Harness, backend harness.Backend, broken bool) {
	t.Helper()
	v := h.CCU().V
	if backend == harness.BackendOpenCCULite {
		v = h.Lite().V
	}
	var err error
	if broken {
		err = v().InjectFault("*", godevccu.FaultRule{
			Method: "listDevices", Times: -1,
			Fault: &godevccu.Fault{Code: -1, Message: "inventory unavailable"},
		})
	} else {
		err = v().ClearFaults("*")
	}
	if err != nil {
		t.Fatalf("break inventory (%v): %v", broken, err)
	}
}

func recoveryLogLines(logs string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(logs, "\n") {
		for _, k := range []string{"wire.interface", "wire.ingest", "wire.init", "recovery.", "callback.new_devices", "lite.", "pending"} {
			if strings.Contains(line, k) {
				b.WriteString(line)
				b.WriteByte('\n')
				break
			}
		}
	}
	return b.String()
}
