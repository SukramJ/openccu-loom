// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHmAdpSuppressionAgreesWithEventClassification couples the two halves of
// one domain rule: this parameter is a device error (or an impulse), so it
// gets a trigger event instead of a stateful data point.
//
// The adapter decides the suppression, [modevent.Classify] decides the event
// kind, and the halves are wired into one path — callback_handlers' data-point-
// less event forwarding only runs because the data point is absent, and
// suppressing the data point is only safe because Classify keeps the parameter.
// A parameter suppressed here but unknown to Classify reaches no plane at all:
// no data point, no device-trigger event, no WebSocket broadcast.
func TestHmAdpSuppressionAgreesWithEventClassification(t *testing.T) {
	t.Parallel()

	for _, p := range hmAdpSuppressionCorpus() {
		suppressed := isDeviceErrorEvent(p) || isImpulseEvent(p)
		// Keypress parameters are classified too, but keep their data point:
		// the click surface is a separate path (Parameter.IsClickEvent), so
		// only the error and impulse kinds are the ones this resolver drops.
		kind, classified := modevent.Classify(hmenum.Parameter(p))
		want := classified && (kind == modevent.KindDeviceError || kind == modevent.KindImpulse)
		if suppressed != want {
			t.Fatalf("parameter %q: adapter suppresses the data point=%v, model/event classifies it as %q (suppressible=%v) — a suppressed parameter the classifier drops reaches no plane at all",
				p, suppressed, kind, want)
		}
	}
}

// TestHmAdpDeviceErrorRuleMatchesTheClassifier states the error half directly:
// exact match on a prefix root, or the root followed by "_".
func TestHmAdpDeviceErrorRuleMatchesTheClassifier(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		param string
		want  bool
	}{
		{"ERROR", true},
		{"ERROR_OVERHEAT", true},
		{"SENSOR_ERROR", true},
		{"ERRORCODE", false},
		{"ERRORS", false},
		{"SENSOR_ERRORLIST", false},
		{"STATE", false},
	} {
		if got := isDeviceErrorEvent(tc.param); got != tc.want {
			t.Fatalf("isDeviceErrorEvent(%q) = %v, want %v", tc.param, got, tc.want)
		}
	}
}

// hmAdpSuppressionCorpus builds the parameter names the coupling is checked
// over from the two sides themselves rather than from a remembered list, so a
// name added to either side extends the corpus instead of leaving a hole:
// every parameter [modevent.Sources] reports for each kind, every key of
// [ImpulseEvents], and per device-error root the four shapes that separate an
// exact-or-underscore rule from a bare prefix.
//
// The trailing literals are ordinary parameters, needed because neither side
// enumerates what it rejects. They are not invented: every one of them is a
// real CCU parameter name, measured over the reference paramset-description
// corpus checked out beside this repository by parsing all 399 files as
// latin-1 JSON and collecting every map key containing "ERROR" — CLEAR_ERROR
// appears in 2 files, LOAD_ERROR_CALIB in 9, VALVE_ERROR_POSITION in 2,
// STATUS_FLAG_ERROR in 1. The same measurement found no ERRORCODE, ERRORS or
// SENSOR_ERRORLIST anywhere, so those enter below as derived shapes, which is
// what they are — probes for the rule, not device parameters.
func hmAdpSuppressionCorpus() []string {
	out := []string{"STATE", "LEVEL", "CLEAR_ERROR", "LOAD_ERROR_CALIB", "VALVE_ERROR_POSITION", "STATUS_FLAG_ERROR"}
	for _, k := range []modevent.Kind{modevent.KindKeypress, modevent.KindImpulse, modevent.KindDeviceError} {
		for _, p := range modevent.Sources(k) {
			root := string(p)
			out = append(out, root)
			if k != modevent.KindDeviceError {
				continue
			}
			// root+"_X" must be a device error; the three glued shapes must
			// not — that is the whole difference between the classifier's
			// rule and the bare HasPrefix this side used to run.
			out = append(out, root+"_OVERHEAT", root+"CODE", root+"S", root+"LIST")
		}
	}
	for k := range ImpulseEvents {
		out = append(out, k)
	}
	return out
}

// TestHmAdpImpulseEventsMirrorsTheClassifier pins the de-duplication itself:
// [ImpulseEvents] is a projection of the classifier's impulse sources, not a
// second declaration of them. SEQUENCE_OK used to be written out in both
// places, and nothing compared the two — a name added to one side alone
// either suppressed a data point for an event nothing emits, or emitted an
// event beside a data point that should not exist.
func TestHmAdpImpulseEventsMirrorsTheClassifier(t *testing.T) {
	t.Parallel()

	want := make(map[string]struct{})
	for _, p := range modevent.Sources(modevent.KindImpulse) {
		want[string(p)] = struct{}{}
	}
	if len(want) == 0 {
		t.Fatal("the classifier reports no impulse parameters — this test would pass vacuously")
	}
	for name := range want {
		if _, ok := ImpulseEvents[name]; !ok {
			t.Fatalf("the classifier treats %q as an impulse event, ImpulseEvents does not — the two sets have drifted", name)
		}
		if !isImpulseEvent(name) {
			t.Fatalf("isImpulseEvent(%q) = false while the classifier reports an impulse event", name)
		}
	}
	for name := range ImpulseEvents {
		if _, ok := want[name]; !ok {
			t.Fatalf("ImpulseEvents carries %q, the classifier does not — a data point suppressed for an event nothing emits", name)
		}
	}
}
