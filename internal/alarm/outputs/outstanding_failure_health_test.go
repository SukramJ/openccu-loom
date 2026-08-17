// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestFireFailKeepsHealthDegradedUntilOwnConditionResolves covers S7 —
// alarm health is the worst outstanding condition, not the last sample.
//
// A siren whose fire failed never sounded, so the domain is degraded.
// Health is one last-sample-wins component, so before this guard the
// next verified stop of ANY output — a different siren finishing its own
// bounded activation cleanly — flipped health back to healthy and erased
// the degradation of a siren that never fired. The failed-fire condition
// must stay degraded until it is itself resolved, never until an
// unrelated stop verifies.
func TestFireFailKeepsHealthDegradedUntilOwnConditionResolves(t *testing.T) {
	h := newHarness(t)
	// Two independent acoustic sirens on separate physical channels
	// (sirA:1, sirB:1) so their watchdogs and stops never interact.
	h.seedOutputs(
		outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120}),
		outputRow("sirB", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120}),
	)

	// sirA's activation write fails; sirB fires cleanly and is watchdogged
	// against its 120 s duration.
	h.siren("sirA").setTurnOnErr(errDeviceUnreachable)
	_ = h.mgr.FireCycle(h.ctx, "eg", newIncident(1, hmenum.AlarmModeFull),
		engine.FireOptions{Policy: noPolicy})

	// sirA's failed fire raises a degradation naming the output.
	assertUnhealthy(t, h, "sirA")

	// A verified stop of the UNRELATED output sirB: its watchdog fires at
	// the 120 s duration, the device confirms inactive, and the verify
	// pass clears sirB's activation. This must NOT clear sirA's still
	// outstanding failed-fire degradation.
	h.siren("sirB").setAcoustic(false, true)
	h.advance(120 * time.Second)  // sirB watchdog writes the stop
	h.advance(stopVerifyInterval) // sirB verify pass reads it back inactive
	if got := h.lastHealth(t); got.Healthy {
		t.Fatalf("a verified stop of the unrelated output sirB reported healthy=%v (%q) — it erased "+
			"sirA's failed-fire degradation; health must reflect the worst outstanding condition, "+
			"not the last verified stop", got.Healthy, got.Note)
	}

	// Resolving sirA's own condition — the device works again and a
	// verified stop of sirA confirms it safely inactive — clears the last
	// outstanding failure and restores health.
	h.siren("sirA").setAcoustic(false, true)
	if err := h.mgr.StopAll(h.ctx, "eg", 1); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	h.advance(stopVerifyInterval)
	if got := h.lastHealth(t); !got.Healthy {
		t.Fatalf("resolving sirA's own condition did not restore health: %+v — once no output carries "+
			"an outstanding failure the domain is healthy again", got)
	}
}

// TestVerifiedStopReportsHealthyOnlyWhenNothingElseFailed pins the
// narrow-but-load-bearing half of the rule from the driver's own state:
// with no outstanding failure a verified stop reports healthy (the
// recovery path other tests rely on), but a second output still carrying
// a failure holds the domain degraded through that same verified stop.
func TestVerifiedStopReportsHealthyOnlyWhenNothingElseFailed(t *testing.T) {
	h := newHarness(t)
	h.build(nil)

	// No outstanding failures: a verified stop of sirA reports healthy.
	h.mgr.resolveFailure("sirA")
	if got := h.lastHealth(t); !got.Healthy {
		t.Fatalf("verified stop with no outstanding failure = %+v, want healthy", got)
	}

	// A different output is now failed; a verified stop of sirA must not
	// clear it.
	h.mgr.noteFailure("sirB", "sirB failed")
	if got := h.lastHealth(t); got.Healthy {
		t.Fatalf("noteFailure(sirB) = %+v, want degraded", got)
	}
	h.mgr.resolveFailure("sirA")
	if got := h.lastHealth(t); got.Healthy {
		t.Fatalf("a verified stop of sirA reported healthy=%v while sirB is still failed", got.Healthy)
	}

	// Resolving sirB clears the last failure and reports healthy.
	h.mgr.resolveFailure("sirB")
	if got := h.lastHealth(t); !got.Healthy {
		t.Fatalf("resolving the last outstanding failure = %+v, want healthy", got)
	}
}
