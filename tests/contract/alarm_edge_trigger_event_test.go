// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestEdgeTriggerPressRepublishesOnRepeat pins the alarm keypad/remote
// contract: an edge-trigger parameter (PRESS_*, CODE_ID, CODE_STATE) must
// publish a DataPointValueChangedEvent on every emission — even a repeated
// identical value — rather than being collapsed by the event coordinator's
// value-unchanged deduplication. A WKP user who presses the same lock key
// twice, or a remote sending PRESS_SHORT again, must reach the alarm intent
// router both times; suppressing the second edge would swallow a real intent.
func TestEdgeTriggerPressRepublishesOnRepeat(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	cache := coordinators.NewCacheCoordinator()
	ec := coordinators.NewEventCoordinator(bus, cache, nil)

	var count int
	events.Subscribe(bus, func(hmevent.DataPointValueChangedEvent) {
		count++
	})

	const (
		iface = "HmIP-RF"
		ch    = "0001D3C99AB123:3"
		press = "PRESS_LOCK"
	)
	// The same true edge, delivered three times: a real keypad emits the
	// identical ACTION bool on each physical press.
	ec.HandleRawEvent(context.Background(), iface, ch, press, hmtypes.BoolValue(true))
	ec.HandleRawEvent(context.Background(), iface, ch, press, hmtypes.BoolValue(true))
	ec.HandleRawEvent(context.Background(), iface, ch, press, hmtypes.BoolValue(true))

	if count != 3 {
		t.Fatalf("edge-trigger parameter %q: got %d published events for 3 identical emissions, want 3 (dedup must be bypassed)", press, count)
	}

	// Guard the negation: an ordinary stateful parameter still deduplicates,
	// so this contract does not regress the no-op-suppression baseline.
	if hmenum.IsEdgeTriggerParameter(hmenum.ParameterState) {
		t.Fatal("STATE must not be classed as an edge-trigger parameter")
	}
	var stateCount int
	events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
		if e.Key.Parameter == "STATE" {
			stateCount++
		}
	})
	ec.HandleRawEvent(context.Background(), iface, "0001D3C99AB123:4", "STATE", hmtypes.BoolValue(true))
	ec.HandleRawEvent(context.Background(), iface, "0001D3C99AB123:4", "STATE", hmtypes.BoolValue(true))
	if stateCount != 1 {
		t.Fatalf("stateful parameter STATE: got %d events for 2 identical emissions, want 1 (dedup must still apply)", stateCount)
	}
}
