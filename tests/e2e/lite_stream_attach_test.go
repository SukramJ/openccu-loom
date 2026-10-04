// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2ELiteDevicePairedBeforeStreamAttachesIsHeld pins that an
// openccu-lite central sees a device paired while the daemon runs even
// when the pairing falls between the bring-up's inventory pull and the
// first attach of the box's event stream.
//
// The window is real in production: while the box boots, the stream
// reader backs off on the box's 503, and the readiness gate that starts
// the bring-up polls on its own schedule, so the inventory is pulled
// before the stream is back. A box replays nothing to a stream that
// attaches without a Last-Event-ID, so the pairing announcement of that
// window is never delivered; only a reconciliation against the box's
// inventory after the attach can see the device. Without one the device
// stays unknown until the next reconnect or restart — and is then built
// without the hold although the daemon was running when it was paired.
func TestE2ELiteDevicePairedBeforeStreamAttachesIsHeld(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{
		StartCCUNotReady: true,
		Backend:          harness.BackendOpenCCULite,
	})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	if items := getJSONArrayOnce(t, h, "/api/v1/devices", "items"); len(items) != 0 {
		t.Fatalf("expected 0 devices while the box is still booting, got %d — the readiness "+
			"gate did not hold, so this test no longer reproduces the production order", len(items))
	}
	// The stream reader has been refused at least once, so it is waiting
	// out its backoff when the box comes up — the production state.
	if !waitFor(t, 30*time.Second, func() bool {
		return strings.Contains(h.Logs(), `"msg":"lite.stream.dropped"`)
	}) {
		t.Fatal("the event stream was never refused by the booting box")
	}
	h.SetCCUReady(true)

	// Pair the device the moment the bring-up has pulled the HmIP-RF
	// inventory: that pull cannot have seen it.
	if !pollFast(30*time.Second, func() bool { return hmipIngested(h.Logs()) }) {
		t.Fatalf("the HmIP-RF inventory was never pulled; logs:\n%s", liteAttachLogLines(h.Logs()))
	}
	streamsAtPairing := h.Lite().OpenStreams()
	hotPlug(t, h, harness.BackendOpenCCULite)
	t.Logf("paired %s with %d open event stream(s)", hotPlugModel, streamsAtPairing)
	if streamsAtPairing != 0 {
		t.Fatalf("the event stream was already attached when the device was paired, so this " +
			"test no longer reproduces the window between the inventory pull and the first attach")
	}

	var outcome string
	waitFor(t, 45*time.Second, func() bool {
		for _, e := range inboxEntries(t, h) {
			if model, _ := e["model"].(string); strings.EqualFold(model, hotPlugModel) && e["pending_creation"] == true {
				outcome = "held"
				return true
			}
		}
		if deviceAddressOfModel(t, h, hotPlugModel) != "" {
			outcome = "built"
			return true
		}
		return false
	})
	switch outcome {
	case "held":
		t.Logf("held; daemon log:\n%s", liteAttachLogLines(h.Logs()))
		if addr := deviceAddressOfModel(t, h, hotPlugModel); addr != "" {
			t.Fatalf("%s is held and built at once (%s)", hotPlugModel, addr)
		}
		// The reconciliation lists the whole inventory; the fleet the
		// bring-up built must not be parked by it.
		assertNoFleetDeviceHeld(t, h)
		if missing := missingFleetModels(t, h); missing != nil {
			t.Fatalf("fleet models missing from GET /devices after the reconciliation: %v", missing)
		}
	case "built":
		t.Fatalf("%s paired while the daemon ran was built without the hold; logs:\n%s",
			hotPlugModel, liteAttachLogLines(h.Logs()))
	default:
		t.Fatalf("%s paired before the event stream attached is neither held on GET /inbox nor "+
			"built (open streams now %d); logs:\n%s",
			hotPlugModel, h.Lite().OpenStreams(), liteAttachLogLines(h.Logs()))
	}
}

// pollFast polls cond every few milliseconds: the window under test is
// shorter than [waitFor]'s interval.
func pollFast(deadline time.Duration, cond func() bool) bool {
	until := time.Now().Add(deadline)
	for !cond() {
		if time.Now().After(until) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

func hmipIngested(logs string) bool {
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, `"msg":"wire.ingest.ok"`) && strings.Contains(line, "HmIP-RF") {
			return true
		}
	}
	return false
}

// liteAttachLogLines extracts the daemon's log lines about the stream,
// the bring-up's ingest and device announcements.
func liteAttachLogLines(logs string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(logs, "\n") {
		for _, k := range []string{"lite.stream", "lite.reconcile", "callback.new_devices", "wire.ingest.ok", "wire.init"} {
			if strings.Contains(line, k) {
				b.WriteString(line)
				b.WriteByte('\n')
				break
			}
		}
	}
	return b.String()
}
