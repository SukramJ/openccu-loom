// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fakeCalcDP is a minimal calculated-DP stub satisfying the contracts
// the initial-snapshot calc loop and buildPublishEvent read:
// DataPointKey (AttachableDataPoint), Parameter + RawValue (snapshot
// loop), and Category (discovery routing).
type fakeCalcDP struct {
	key      hmtypes.DataPointKey
	category hmenum.DataPointCategory
	value    any
	observed bool
}

func (f *fakeCalcDP) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeCalcDP) Parameter() hmenum.Parameter        { return hmenum.Parameter(f.key.Parameter) }
func (f *fakeCalcDP) RawValue() (any, bool)              { return f.value, f.observed }
func (f *fakeCalcDP) Category() hmenum.DataPointCategory { return f.category }

// TestPublishInitialSnapshotRegistersUnobservedCalcBinarySensor is the
// regression tripwire for the parity gap where calculated binary_sensors
// (SMOKE_ALARM / INTRUSION_ALARM / WINDOW_OPEN) never reached HA
// discovery because they start unobserved. The reference stack registers
// them as entities at setup regardless of observation; the initial
// snapshot must publish a binary_sensor discovery config plus an
// `unknown` (null) slot state on the calculated bucket.
func TestPublishInitialSnapshotRegistersUnobservedCalcBinarySensor(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	ch.AttachCalculatedDataPoint(&fakeCalcDP{
		key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "SMOKE_ALARM",
		},
		category: hmenum.DataPointCategoryBinarySensor,
		observed: false,
	})

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "ccu-01",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	var (
		discovery int
		slotState int
	)
	for _, p := range pub.Published() {
		if strings.HasPrefix(p.Topic, "homeassistant/binary_sensor/") &&
			strings.HasSuffix(p.Topic, "/config") &&
			strings.Contains(p.Topic, "smoke_alarm") {
			discovery++
			// binary_sensor uses the lowercase bool on/off contract.
			if !strings.Contains(string(p.Payload), `"payload_on":"true"`) {
				t.Errorf("calc binary_sensor discovery missing bool payload contract: %s", p.Payload)
			}
		}
		if strings.Contains(p.Topic, "/calculated/SMOKE_ALARM") {
			slotState++
		}
	}
	if discovery != 1 {
		t.Fatalf("expected 1 calc binary_sensor discovery config, got %d", discovery)
	}
	if slotState != 1 {
		t.Fatalf("expected 1 calc binary_sensor slot-state publish, got %d", slotState)
	}
}
