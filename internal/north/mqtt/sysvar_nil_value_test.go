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
