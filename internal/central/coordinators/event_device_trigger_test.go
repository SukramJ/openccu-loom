// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// triggerRecorder collects the device-trigger events one raw callback path
// produces.
func triggerRecorder(t *testing.T) (*EventCoordinator, *[]hmevent.DeviceTriggerEvent) {
	t.Helper()
	bus := events.NewBus()
	ec := NewEventCoordinator(bus, NewCacheCoordinator(), nil)
	ec.SetCentralName("test-central")
	got := &[]hmevent.DeviceTriggerEvent{}
	unsub := events.Subscribe(bus, func(e hmevent.DeviceTriggerEvent) {
		*got = append(*got, e)
	})
	t.Cleanup(unsub)
	return ec, got
}

// TestRawEventPublishesDeviceTrigger is the regression tripwire for a
// broadcast that was declared, bridged and contract-tested but never fired:
// the only publisher of DeviceTriggerEvent had no production caller, so the
// raw callback path emitted a value change and nothing else. A keypress is
// not a value — no consumer can recover "a button was pressed" from a
// value-changed message.
func TestRawEventPublishesDeviceTrigger(t *testing.T) {
	t.Parallel()

	ec, got := triggerRecorder(t)
	ec.HandleRawEvent(t.Context(), "HmIP-RF", "0001ABCD:2", "PRESS_SHORT", hmtypes.BoolValue(true))

	if len(*got) != 1 {
		t.Fatalf("expected one device trigger, got %d: %+v", len(*got), *got)
	}
	e := (*got)[0]
	if e.EventType_ != hmenum.DeviceTriggerEventTypeKeypress {
		t.Fatalf("event type = %q, want keypress", e.EventType_)
	}
	if e.DeviceAddress != "0001ABCD" || e.ChannelNo != 2 {
		t.Fatalf("address split wrong: %q / %d", e.DeviceAddress, e.ChannelNo)
	}
	if e.Parameter != "PRESS_SHORT" || e.InterfaceID != "HmIP-RF" {
		t.Fatalf("event lost its coordinates: %+v", e)
	}
	if e.CentralName != "test-central" {
		t.Fatalf("event not scoped to the central: %+v", e)
	}
}

// TestRepeatedKeypressPublishesEveryEdge pins that a second identical press
// still surfaces. The value-changed dedup exempts edge-trigger parameters for
// exactly this reason; the trigger twin must inherit that, or holding a
// button down would register once.
func TestRepeatedKeypressPublishesEveryEdge(t *testing.T) {
	t.Parallel()

	ec, got := triggerRecorder(t)
	for range 3 {
		ec.HandleRawEvent(t.Context(), "HmIP-RF", "0001ABCD:2", "PRESS_SHORT", hmtypes.BoolValue(true))
	}
	if len(*got) != 3 {
		t.Fatalf("expected three triggers for three presses, got %d", len(*got))
	}
}

// TestRawEventClassifiesTriggerKinds covers the other two flavours and the
// negative case — a stateful parameter must not masquerade as an event.
func TestRawEventClassifiesTriggerKinds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		parameter string
		want      hmenum.DeviceTriggerEventType
		emits     bool
	}{
		{"PRESS_LONG", hmenum.DeviceTriggerEventTypeKeypress, true},
		{"SEQUENCE_OK", hmenum.DeviceTriggerEventTypeImpulse, true},
		{"ERROR", hmenum.DeviceTriggerEventTypeDeviceError, true},
		// Device-error matching is prefix-based, so the open-ended CCU
		// spellings resolve too.
		{"ERROR_OVERHEAT", hmenum.DeviceTriggerEventTypeDeviceError, true},
		{"SENSOR_ERROR", hmenum.DeviceTriggerEventTypeDeviceError, true},
		{"ACTUAL_TEMPERATURE", "", false},
		{"LEVEL", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.parameter, func(t *testing.T) {
			t.Parallel()
			ec, got := triggerRecorder(t)
			ec.HandleRawEvent(t.Context(), "HmIP-RF", "0001ABCD:1", tc.parameter, hmtypes.BoolValue(true))
			if !tc.emits {
				if len(*got) != 0 {
					t.Fatalf("%s is a state, not an event — got %+v", tc.parameter, *got)
				}
				return
			}
			if len(*got) != 1 {
				t.Fatalf("expected one trigger for %s, got %d", tc.parameter, len(*got))
			}
			if (*got)[0].EventType_ != tc.want {
				t.Fatalf("%s classified as %q, want %q", tc.parameter, (*got)[0].EventType_, tc.want)
			}
		})
	}
}

// TestDeviceLevelParameterKeepsChannelZero guards the address split for a
// parameter the CCU reports without a channel suffix.
func TestDeviceLevelParameterKeepsChannelZero(t *testing.T) {
	t.Parallel()

	ec, got := triggerRecorder(t)
	ec.HandleRawEvent(t.Context(), "HmIP-RF", "0001ABCD", "ERROR", hmtypes.BoolValue(true))

	if len(*got) != 1 {
		t.Fatalf("expected one trigger, got %d", len(*got))
	}
	if (*got)[0].DeviceAddress != "0001ABCD" || (*got)[0].ChannelNo != 0 {
		t.Fatalf("channel-less address mis-split: %+v", (*got)[0])
	}
}
