// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// ws_broadcast_roundtrip_test.go — a running daemon actually emits its
// declared WS broadcasts.
//
// TestWSBroadcastsHaveProductionEmitter (ws_broadcast_emitter_test.go) pins
// the declaration half: every broadcast in assets/wsapi.json names a
// production file and a set of literal tokens. That guard is satisfied by
// the tokens existing in the source text — it never runs the code, so a
// subscription that is registered but silently no-ops (a stale event type,
// a nil guard that always fires, a condition that never lets the publish
// happen) leaves it green. The MQTT planes already close this gap with a
// `Test*PlaneTopicsRoundTrip` per plane (rule 4, root CLAUDE.md); the WS
// plane had no equivalent.
//
// This test drives the real composition path — [central.New], a real
// [central.Registry], and [adapter.NewEventBridge] wired exactly like
// cmd/openccu-loom's daemon_north.go wires it — and transitions the
// central's own state machine, the same call [*central.Unit.Start] makes.
// It then asserts the broadcast actually lands in the real [ws.Hub]'s
// replay buffer, the same buffer a reconnecting client resumes from. If the
// EventBridge's subscription to CentralStateChangedEvent is ever removed or
// broken, the buffer stays empty and this test goes red — no production
// code is called from the test to fabricate the event; the transition goes
// through the same state machine [*central.Unit.Start] uses at boot.
import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestWSCentralStateBroadcastReachesHub is the round-trip half of the WS
// broadcast contract for "central.state_changed": a real central state
// transition, driven through the real state machine, must produce a real
// entry in the hub's replay buffer — not merely a token in source text.
func TestWSCentralStateBroadcastReachesHub(t *testing.T) {
	t.Parallel()

	const centralName = "wsroundtrip"

	u, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	reg := central.NewRegistry()
	if err := reg.Register(u); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}

	hub := ws.NewHub()
	hub.SetReplayCapacity(64)

	bridge := adapter.NewEventBridge(reg, hub, nil)
	bridge.Start(context.Background())

	// Drive the transition through the production state machine — the
	// same call sequence *central.Unit.Start makes — rather than
	// publishing hmevent.CentralStateChangedEvent onto the bus by hand.
	// STARTING -> INITIALIZING is the first edge a real boot takes.
	if err := u.StateMachine.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("TransitionTo(Initializing): %v", err)
	}

	// The fan-out onto the hub happens off the calling goroutine (the
	// event bus dispatches asynchronously), so poll the replay buffer
	// briefly instead of asserting immediately.
	deadline := time.Now().Add(2 * time.Second)
	var found *ws.Event
	for time.Now().Before(deadline) {
		res := hub.Replay(0, nil)
		for i := range res.Events {
			e := res.Events[i]
			if e.Type == string(hmevent.EventTypeCentralStateChanged) {
				found = &e
				break
			}
		}
		if found != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if found == nil {
		t.Fatal("central.state_changed never reached the ws.Hub replay buffer after a real state transition — " +
			"the EventBridge subscription for hmevent.CentralStateChangedEvent is not wired, or the state " +
			"machine transition never reached the bus")
	}
	payload, ok := found.Payload.(ws.CentralStateChangedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want ws.CentralStateChangedPayload", found.Payload)
	}
	if payload.Central != centralName {
		t.Errorf("payload.Central = %q, want %q", payload.Central, centralName)
	}
	if payload.NewState != string(hmenum.CentralStateInitializing) {
		t.Errorf("payload.NewState = %q, want %q", payload.NewState, hmenum.CentralStateInitializing)
	}
}
