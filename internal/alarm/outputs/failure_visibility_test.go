// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// errDeviceUnreachable stands in for the wire failure an output driver
// reports when the actuator behind it cannot be driven.
var errDeviceUnreachable = errors.New("device unreachable")

// TestAFailedOutputCommandSurfacesOnEverySignal covers S7 — fail-visible.
//
// S7 exists because the worst failure mode of a DIY alarm is not a loud
// one. An alarm that has quietly been non-functional for weeks reports
// nothing, so every failed output command has to surface as a journal
// entry and a health signal, not only as an error returned to whoever
// happened to be watching.
//
// Two paths were silent, both measured against a real CCU.
//
// A failed test fire returned HTTP 502 to the operator who pressed the
// button and left nothing behind: no journal entry, no health signal.
// Successful tests were journalled, so the record of a siren sweep
// showed only the outputs that worked — precisely inverted from what a
// technician needs.
//
// A failed activation during a real incident journalled
// `output_fire_failed` but never touched health, so `/api/v1/health`
// kept reporting the alarm domain healthy while a siren had not gone
// off.
func TestAFailedOutputCommandSurfacesOnEverySignal(t *testing.T) {
	t.Parallel()

	t.Run("test fire", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.seedStandardZone()
		failingActuator(t, h, "plug:1").boundedErr = errDeviceUnreachable

		if err := h.mgr.TestFire(h.ctx, "plug", false); err == nil {
			t.Fatal("TestFire reported success although the device write failed")
		}

		if !h.journal.hasForOutput("output_test_failed", "plug") {
			t.Errorf("no output_test_failed journal entry; got events %v. A siren sweep whose "+
				"failures leave no trace records only the outputs that worked",
				journalEvents(h))
		}
		assertUnhealthy(t, h, "plug")
	})

	t.Run("incident activation", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.seedStandardZone()
		failingActuator(t, h, "plug:1").boundedErr = errDeviceUnreachable

		// Fire the whole zone; the plug is the one output that fails.
		_ = h.mgr.FireCycle(h.ctx, "eg", newIncident(1, hmenum.AlarmModeFull),
			engine.FireOptions{Policy: noPolicy})

		if !h.journal.hasForOutput("output_fire_failed", "plug") {
			t.Fatalf("no output_fire_failed journal entry; got events %v", journalEvents(h))
		}
		assertUnhealthy(t, h, "plug")
	})
}

// TestASuccessfulTestFireIsNotFiledAsAFault pins the journal class of a
// test that worked.
//
// Every test fire was written with class `fault`, so an operator
// filtering the journal for faults — the one filter S7 makes them rely
// on — saw a fault per successful siren test and had to read each entry
// to tell them apart. The enum has carried a `test` class for this the
// whole time.
func TestASuccessfulTestFireIsNotFiledAsAFault(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.seedStandardZone()

	if err := h.mgr.TestFire(h.ctx, "plug", false); err != nil {
		t.Fatalf("TestFire: %v", err)
	}

	fired := h.journal.entriesFor("output_test_fired")
	if len(fired) != 1 {
		t.Fatalf("got %d output_test_fired entries, want 1", len(fired))
	}
	if got := fired[0].Class; got != hmenum.AlarmJournalClassTest {
		t.Errorf("class = %q, want %q — a test that worked is not a fault, and filing it as one "+
			"buries the failures the fault filter exists to surface", got, hmenum.AlarmJournalClassTest)
	}
	if h.healthCallCount() != 0 {
		t.Errorf("a successful test fire recorded health %v; it must not move the domain's health "+
			"either way", h.healthCalls)
	}
}

// assertUnhealthy fails unless the last health sample is a degradation
// naming the output.
func assertUnhealthy(t *testing.T, h *harness, outputID string) {
	t.Helper()
	if h.healthCallCount() == 0 {
		t.Fatalf("no health signal for the failed output %q — /api/v1/health keeps reporting the "+
			"alarm domain healthy while an output cannot be driven", outputID)
	}
	last := h.lastHealth(t)
	if last.Healthy {
		t.Errorf("last health sample is healthy=%v (%q), want a degradation", last.Healthy, last.Note)
	}
	if !strings.Contains(last.Note, outputID) {
		t.Errorf("health note %q does not name the failing output %q; an operator cannot act on a "+
			"degradation that does not say which device it is", last.Note, outputID)
	}
}

// failingActuator returns the fake actuator behind a channel so a test
// can make its next write fail.
func failingActuator(t *testing.T, h *harness, channelAddress string) *fakeActuator {
	t.Helper()
	dev, err := h.resolver.Actuator(testCentral, channelAddress)
	if err != nil {
		t.Fatalf("resolve actuator %s: %v", channelAddress, err)
	}
	a, ok := dev.(*fakeActuator)
	if !ok {
		t.Fatalf("actuator %s is a %T, not the fake", channelAddress, dev)
	}
	return a
}

// journalEvents lists the event names the journal holds, for failure
// messages that say what did land instead.
func journalEvents(h *harness) []string {
	h.journal.mu.Lock()
	defer h.journal.mu.Unlock()
	out := make([]string, 0, len(h.journal.entries))
	for _, e := range h.journal.entries {
		out = append(out, e.Event)
	}
	return out
}

// TestARefusedTestFireIsNotReportedAsADegradation guards the other side
// of the rule above.
//
// A smoke-detector sounder has no safe test path: each commanded
// activation costs irreplaceable battery life and fans out to the whole
// detector group, so the manager refuses. That refusal is a design
// decision, not a device that failed, and reporting it as a health
// degradation would put a false fault on the one signal S7 makes an
// operator trust — which is how a signal stops being read.
func TestARefusedTestFireIsNotReportedAsADegradation(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.seedStandardZone()

	if err := h.mgr.TestFire(h.ctx, "smoke", false); !errors.Is(err, ErrTestFireUnsupported) {
		t.Fatalf("TestFire on a smoke sounder = %v, want ErrTestFireUnsupported", err)
	}
	if h.healthCallCount() != 0 {
		t.Errorf("a refused test fire recorded health %v — a refusal by design is not a "+
			"degradation", h.healthCalls)
	}
	if len(h.journal.entriesFor("output_test_failed")) != 0 {
		t.Error("a refused test fire was journalled as a failed one")
	}
}
