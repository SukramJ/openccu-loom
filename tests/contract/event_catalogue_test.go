// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestEventTypeStringsStable locks the wire-level strings the event-
// bus uses as routing keys. Renaming these breaks subscribers.
func TestEventTypeStringsStable(t *testing.T) {
	cases := map[string]hmevent.EventType{
		"central.state_changed":            hmevent.EventTypeCentralStateChanged,
		"datapoint.value_changed":          hmevent.EventTypeDataPointValueChanged,
		"datapoint.optimistic_rolled_back": hmevent.EventTypeDataPointOptimisticRolled,
		"device.created":                   hmevent.EventTypeDeviceCreated,
		"device.trigger":                   hmevent.EventTypeDeviceTrigger,
		"recovery.started":                 hmevent.EventTypeRecoveryStarted,
		"hub.service_message":              hmevent.EventTypeServiceMessage,
		"hub.alarm_message":                hmevent.EventTypeAlarmMessage,
		"incident.recorded":                hmevent.EventTypeIncidentRecorded,
	}
	for want, got := range cases {
		if string(got) != want {
			t.Errorf("event type tag=%q, want %q", string(got), want)
		}
	}
}
