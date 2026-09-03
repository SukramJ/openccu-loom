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

	// Names that separate a bare-prefix rule from an exact-or-underscore one,
	// alongside the shapes the descriptor corpus actually carries.
	params := []string{
		"ERROR",
		"ERROR_CODE",
		"ERROR_JAMMED",
		"ERROR_OVERHEAT",
		"ERROR_OVERLOAD",
		"ERROR_SMOKE_CHAMBER",
		"ERROR_NON_FLAT_POSITIONING",
		"ERROR_ALARM_TEST",
		"SENSOR_ERROR",
		"ERRORCODE",
		"ERRORS",
		"SENSOR_ERRORLIST",
		"STATE",
		"LEVEL",
		"SEQUENCE_OK",
	}
	for _, p := range params {
		suppressed := isDeviceErrorEvent(p) || isImpulseEvent(p)
		_, classified := modevent.Classify(hmenum.Parameter(p))
		if suppressed != classified {
			t.Fatalf("parameter %q: adapter suppresses the data point=%v, model/event classifies it=%v — a suppressed parameter the classifier drops reaches no plane at all",
				p, suppressed, classified)
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
