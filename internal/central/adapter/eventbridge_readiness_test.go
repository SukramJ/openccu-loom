// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestOnCentralReadinessNilWSHub verifies onCentralReadiness returns early
// without panicking when the bridge has no wsHub wired (mirrors
// TestOnCentralStateNilWSHub for the sibling handler).
func TestOnCentralReadinessNilWSHub(t *testing.T) {
	t.Parallel()
	b := &EventBridge{}
	e := hmevent.CentralReadinessChangedEvent{Base: hmevent.NewBase(), Phase: hmenum.ReadinessReady}
	b.onCentralReadiness("ccu-01", e)
}

// TestOnCentralReadinessPublishesReadyOnlyForReadyPhase verifies that
// onCentralReadiness forwards every phase to the WS hub, but the payload's
// `ready` flag is true only for hmenum.ReadinessReady — every other phase
// (including waiting_for_ccu, loading_hub, loading_devices) must publish
// ready==false so the SPA never shows "ready" mid bring-up.
func TestOnCentralReadinessPublishesReadyOnlyForReadyPhase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		phase     hmenum.ReadinessPhase
		wantReady bool
	}{
		{hmenum.ReadinessUnknown, false},
		{hmenum.ReadinessWaitingForCCU, false},
		{hmenum.ReadinessLoadingHub, false},
		{hmenum.ReadinessLoadingDevices, false},
		{hmenum.ReadinessReady, true},
	}

	for _, c := range cases {
		hub := ws.NewHub()
		b := &EventBridge{wsHub: hub}

		when := time.Now()
		e := hmevent.CentralReadinessChangedEvent{
			Base:             hmevent.NewBaseAt(when),
			CentralName:      "ccu-01",
			Phase:            c.phase,
			InterfacesLoaded: 3,
			InterfacesTotal:  5,
		}
		b.onCentralReadiness("ccu-01", e)

		result := hub.Replay(0, nil)
		if len(result.Events) != 1 {
			t.Fatalf("phase %q: got %d buffered events, want 1", c.phase, len(result.Events))
		}
		ev := result.Events[0]

		wantTopic := ws.CentralReadinessTopic("ccu-01")
		if ev.Topic != wantTopic {
			t.Errorf("phase %q: Topic = %q, want %q", c.phase, ev.Topic, wantTopic)
		}
		if ev.Type != string(hmevent.EventTypeCentralReadinessChanged) {
			t.Errorf("phase %q: Type = %q, want %q", c.phase, ev.Type, hmevent.EventTypeCentralReadinessChanged)
		}

		pl, ok := ev.Payload.(ws.CentralReadinessChangedPayload)
		if !ok {
			t.Fatalf("phase %q: Payload type = %T, want ws.CentralReadinessChangedPayload", c.phase, ev.Payload)
		}
		if pl.Central != "ccu-01" {
			t.Errorf("phase %q: Payload.Central = %q, want %q", c.phase, pl.Central, "ccu-01")
		}
		if pl.Phase != string(c.phase) {
			t.Errorf("phase %q: Payload.Phase = %q, want %q", c.phase, pl.Phase, string(c.phase))
		}
		if pl.Ready != c.wantReady {
			t.Errorf("phase %q: Payload.Ready = %v, want %v", c.phase, pl.Ready, c.wantReady)
		}
		if pl.InterfacesLoaded != 3 || pl.InterfacesTotal != 5 {
			t.Errorf("phase %q: Payload counts = (%d, %d), want (3, 5)", c.phase, pl.InterfacesLoaded, pl.InterfacesTotal)
		}
	}
}
