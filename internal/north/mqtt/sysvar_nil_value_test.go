// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPublishSysvarRefusesNilValue pins the separation between an
// accidental and a deliberate retraction.
//
// A retained publish with a zero-length payload is MQTT's mechanism for
// deleting a retained message. Rendering a nil value as zero bytes
// therefore made a sysvar the CCU reports as nil delete its own state
// topic — the entity disappears from every consumer, and nothing in the
// log says why. The nil is refused instead, so the last known reading
// stays on the broker.
func TestPublishSysvarRefusesNilValue(t *testing.T) {
	t.Parallel()

	b, pub := newTestBridge(t)
	sv := hub.NewSysvar("ccu-01", "Anwesenheit", "", hmenum.HubValueTypeLogic, nil)

	err := b.PublishSysvar(context.Background(), "ccu-01", sv, nil)
	if !errors.Is(err, ErrNilValue) {
		t.Fatalf("PublishSysvar(nil) error = %v, want ErrNilValue", err)
	}
	for _, s := range pub.sent {
		if s.payload == "" && s.retain {
			t.Fatalf("nil value produced a retained empty payload on %q — that retracts the entity", s.topic)
		}
	}
	if len(pub.sent) != 0 {
		t.Fatalf("expected no publish at all for a nil value, got %v", pub.sent)
	}
}

// TestRetractSysvarStateStaysAvailable guards the other half: refusing
// the accidental retraction must not take the deliberate one away. A
// central leaving the registry still has to clear its sysvar topics,
// and that path does not go through the value renderer.
func TestRetractSysvarStateStaysAvailable(t *testing.T) {
	t.Parallel()

	b, pub := newTestBridge(t)
	sv := hub.NewSysvar("ccu-01", "Anwesenheit", "", hmenum.HubValueTypeLogic, nil)

	if err := b.RetractSysvarState(context.Background(), "ccu-01", sv); err != nil {
		t.Fatalf("RetractSysvarState: %v", err)
	}
	if len(pub.sent) != 1 {
		t.Fatalf("want exactly one publish, got %v", pub.sent)
	}
	if got := pub.sent[0]; got.payload != "" || !got.retain {
		t.Fatalf("retraction = %+v, want empty retained payload", got)
	}
}

// TestRenderValueRejectsNil states the contract at the renderer, where
// both publishers pick it up.
func TestRenderValueRejectsNil(t *testing.T) {
	t.Parallel()

	body, err := renderValue(nil)
	if !errors.Is(err, ErrNilValue) {
		t.Fatalf("renderValue(nil) error = %v, want ErrNilValue", err)
	}
	if body != nil {
		t.Fatalf("renderValue(nil) body = %q, want nil", body)
	}
}

// TestRenderValueFloatPrecision pins the shortest round-tripping decimal
// form for floats.
//
// The renderer used to format with `%f` and trim trailing zeros, which
// caps at six fractional digits: every value below 5e-7 was published as
// a flat "0". That is not a rounding artefact a consumer can compensate
// for — a genuine zero and an erased reading arrive as the same byte.
func TestRenderValueFloatPrecision(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   any
		want string
	}{
		// The measured case: seven decimals, six-digit %f rendered "0".
		{"seven decimals", 0.0000001, "0.0000001"},
		{"twelve decimals", 0.000000000001, "0.000000000001"},
		// Beyond six digits in the middle of the fraction, too — %f
		// rounded this to "1.234568".
		{"more than six digits", 1.23456789, "1.23456789"},
		// A genuine zero must stay distinguishable from an erased one.
		{"zero", 0.0, "0"},
		// Regressions on the forms that already worked.
		{"trailing zeros trimmed", 3.0, "3"},
		{"two decimals", 2.75, "2.75"},
		{"negative", -0.125, "-0.125"},
		// float32 renders at float32 width, not the widening artefact.
		{"float32", float32(0.1), "0.1"},
		{"float32 half", float32(1.5), "1.5"},
	}
	for _, c := range cases {
		got, err := renderValue(c.in)
		if err != nil {
			t.Errorf("%s: renderValue(%v): %v", c.name, c.in, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s: renderValue(%v) = %q, want %q", c.name, c.in, string(got), c.want)
		}
	}
}
