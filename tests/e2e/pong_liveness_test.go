// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EDroppedRegistrationIsNoticedByMissedPongs pins the missed-PONG
// liveness verdict through the running daemon.
//
// A CCU that forgets our callback registration keeps answering every RPC,
// ping included — only the PONG event the ping should produce never arrives,
// and with it no device event either. godevccu's DropConnection reproduces
// exactly that: it forgets every registered client and tells nobody. The
// daemon must notice from the missing PONGs and re-register (init), which
// godevccu answers by calling listDevices on the fresh registration — the
// observable this test waits for.
//
// The keepalive runs every checkInterval here, so three missed PONGs take a
// few seconds; the callback freshness window that would otherwise be the
// only bound is fixed at 180 s, far beyond the deadline below.
//
// Production order (boot not ready, then flip) so the keepalive and its PONG
// correlation are wired against a daemon that came up the way a co-booting
// CCU makes it come up.
func TestE2EDroppedRegistrationIsNoticedByMissedPongs(t *testing.T) {
	t.Parallel()

	const checkInterval = 2 * time.Second
	h := harness.Start(t, harness.Options{
		StartCCUNotReady:        true,
		CheckConnectionInterval: checkInterval,
	})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	v := h.CCU().V()
	v.SetReady(true)

	if !waitFor(t, 45*time.Second, func() bool { return len(deviceItems(t, h)) > 0 }) {
		t.Fatal("no devices appeared after the CCU became ready — bring-up did not run")
	}
	// Steady state: the CCU delivers to our registration (PONGs and events
	// arrive as "event" callbacks, or batched in a multicall).
	if !waitFor(t, 30*time.Second, func() bool { return callbackLogged(h, "event", "system.multicall") }) {
		t.Fatal("the CCU never delivered an event callback to the daemon — no registration to drop")
	}

	if err := v.DropConnection(""); err != nil {
		t.Fatalf("DropConnection: %v", err)
	}
	v.ClearLogs()
	dropped := time.Now()

	// Missed-PONG detection plus recovery fits well inside this bound; the
	// freshness window alone would need 180 s.
	const deadline = 60 * time.Second
	if !waitFor(t, deadline, func() bool { return callbackLogged(h, "listDevices") }) {
		t.Fatalf("the daemon did not re-register within %s after the CCU dropped its registration — "+
			"the keepalive pings were answered but their PONGs never arrived, and nothing noticed", deadline)
	}
	t.Logf("re-registered %s after the registration was dropped", time.Since(dropped).Round(time.Second))
}

// TestE2EConnectionProbeAloneNoticesADroppedRegistration pins that the
// per-interface connection probe — the 15 s ticker, the most frequent ping —
// feeds the missed-PONG verdict on its own. The check_connection job is
// disabled, so no other path sends a correlated ping: if the probe pinged
// without a token, only the 180 s freshness window would be left and the
// deadline below would pass without a re-registration.
func TestE2EConnectionProbeAloneNoticesADroppedRegistration(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{
		StartCCUNotReady:        true,
		CheckConnectionInterval: -time.Second,
	})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	v := h.CCU().V()
	v.SetReady(true)

	if !waitFor(t, 45*time.Second, func() bool { return len(deviceItems(t, h)) > 0 }) {
		t.Fatal("no devices appeared after the CCU became ready — bring-up did not run")
	}
	if !waitFor(t, 30*time.Second, func() bool { return callbackLogged(h, "event", "system.multicall") }) {
		t.Fatal("the CCU never delivered an event callback to the daemon — no registration to drop")
	}

	if err := v.DropConnection(""); err != nil {
		t.Fatalf("DropConnection: %v", err)
	}
	v.ClearLogs()
	dropped := time.Now()

	// Four 15 s probe ticks (one ping in flight, three missed) plus recovery
	// stay well inside this bound; the freshness window alone needs 180 s.
	const deadline = 100 * time.Second
	if !waitFor(t, deadline, func() bool { return callbackLogged(h, "listDevices") }) {
		t.Fatalf("the daemon did not re-register within %s with only the connection probe pinging — "+
			"its pings do not feed the missed-PONG verdict", deadline)
	}
	t.Logf("re-registered %s after the registration was dropped", time.Since(dropped).Round(time.Second))
}

// callbackLogged reports whether godevccu has called the daemon's callback
// server with one of methods since its logs were last cleared.
func callbackLogged(h *harness.Harness, methods ...string) bool {
	for _, e := range h.CCU().V().CallbackLog() {
		if slices.Contains(methods, e.Method) {
			return true
		}
	}
	return false
}
