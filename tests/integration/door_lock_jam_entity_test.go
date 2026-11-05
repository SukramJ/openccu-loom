//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package integration

import (
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDoorLockJamIsAnEntity pins the ERROR_JAMMED data point of both HmIP door
// locks as an entity of its own, on the channel the recorded device carries it:
// channel 0 of the HmIP-DLD, the lock channel 12 (DOOR_LOCK_TRANSCEIVER) of the
// HmIP-DLP (eQ-3 device documentation crRFD 1.60.1, DLP_V2, channel 12 state
// parameters). The DLP registered it as an additional data point on channel 0,
// which carries no such parameter, so on the lock channel it stayed a hidden
// field of the lock.
func TestDoorLockJamIsAnEntity(t *testing.T) {
	type usageReader interface {
		Usage() hmenum.DataPointUsage
	}
	for model, channel := range map[string]int{"HmIP-DLD": 0, "HmIP-DLP": 12} {
		t.Run(model, func(t *testing.T) {
			dev := ingestFixtureDevice(t, model)
			ch := dev.Channel(dev.Address + ":" + strconv.Itoa(channel))
			if ch == nil {
				t.Fatalf("channel %d missing", channel)
			}
			dp := ch.Parameter(hmenum.ParameterErrorJammed)
			if dp == nil {
				t.Fatalf("channel %d carries no ERROR_JAMMED data point", channel)
			}
			u, ok := dp.(usageReader)
			if !ok {
				t.Fatalf("ERROR_JAMMED data point %T reports no usage", dp)
			}
			if got := u.Usage(); got != hmenum.DataPointUsageDataPoint {
				t.Errorf("ERROR_JAMMED on channel %d has usage %q, want %q", channel, got, hmenum.DataPointUsageDataPoint)
			}
		})
	}
}
