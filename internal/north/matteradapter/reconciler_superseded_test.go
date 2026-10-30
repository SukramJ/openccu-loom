// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// rawEndpoint hands a spec to the Aggregator as it is.
type rawEndpoint struct{ spec endpoint.Spec }

func (e rawEndpoint) Spec() endpoint.Spec { return e.spec }

// countingKeys is a DeviceKeyLister that counts its lookups.
type countingKeys struct {
	keysFromTopology
	calls atomic.Int32
}

func (c *countingKeys) DeviceKeys(ctx context.Context, centralName, deviceAddress string) ([]matterendpoint.SourceKey, error) {
	c.calls.Add(1)
	return c.keysFromTopology.DeviceKeys(ctx, centralName, deviceAddress)
}

// reserve serves donor under key in ccu1's partition and removes it again,
// so key keeps a stored number the way a row an earlier composition wrote
// does, and returns that number.
func (s *reconcilerSite) reserve(t *testing.T, key matterendpoint.SourceKey, donor endpoint.Spec) uint16 {
	t.Helper()
	spec := donor
	spec.StableKey = key
	part := s.node.Aggregator().Partition("ccu1")
	if err := part.Batch(s.ctx, func(b *node.Batch) error { return b.Add(rawEndpoint{spec: spec}) }); err != nil {
		t.Fatalf("add %s: %v", key, err)
	}
	var number uint16
	for _, d := range s.node.Topology().Devices {
		if d.Key != nil && d.Key.String() == key.String() {
			number = d.Endpoint
		}
	}
	if number == 0 {
		t.Fatalf("%s is not served after the add", key)
	}
	if err := part.Batch(s.ctx, func(b *node.Batch) error { return b.Remove(key) }); err != nil {
		t.Fatalf("remove %s: %v", key, err)
	}
	return number
}

// TestReconcilerForgetsSupersededPerPressRows pins the superseded-key
// register: a per-press row a release before the button consolidation
// stored for a button channel is forgotten once the walk describes the
// channel, so re-adding its key draws a fresh number. A stored key of the
// same device that the walk only does not describe — an unexposed
// endpoint — keeps its number, and the store is asked once, not on every
// sync.
func TestReconcilerForgetsSupersededPerPressRows(t *testing.T) {
	t.Parallel()
	s := newReconcilerSite(t)
	described, err := s.asm.DescribeDevices(s.ctx, []matteradapter.DeviceSnapshot{
		{CentralName: "ccu1", Devices: []*device.Device{switchDevice("SW0000071", "Donor")}},
	})
	if err != nil || len(described) != 1 || len(described[0].Endpoints) == 0 {
		t.Fatalf("describe the donor: %v", err)
	}
	donor := described[0].Endpoints[0]

	legacy := matterendpoint.SourceKey{
		CentralName: "ccu1", DeviceAddress: "BTN0001", ChannelNo: 1,
		DPKind: matterendpoint.DPKindGeneric, DPKey: string(hmenum.ParameterPressShort),
	}
	unexposed := matterendpoint.SourceKey{
		CentralName: "ccu1", DeviceAddress: "BTN0001", ChannelNo: 2,
		DPKind: matterendpoint.DPKindCustom, DPKey: "LEVEL",
	}
	legacyNumber := s.reserve(t, legacy, donor)
	unexposedNumber := s.reserve(t, unexposed, donor)

	keys := &countingKeys{keysFromTopology: keysFromTopology{"ccu1|BTN0001": {legacy, unexposed}}}
	rec := matteradapter.NewReconciler(s.node.Aggregator(), s.asm, keys, slog.New(slog.DiscardHandler))
	button := newDevice("BTN0001", "Taster")
	ch := addChannel(button, "BTN0001:1", 1)
	ch.Put(pressButtonDP("BTN0001:1", hmenum.ParameterPressShort))
	snap := []matteradapter.DeviceSnapshot{{CentralName: "ccu1", Devices: []*device.Device{button}}}
	for range 2 {
		if err := rec.Sync(s.ctx, snap); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}
	if got := keys.calls.Load(); got != 1 {
		t.Errorf("DeviceKeys asked %d times over two syncs, want 1", got)
	}

	if n := s.reserve(t, legacy, donor); n == legacyNumber {
		t.Errorf("the superseded per-press key kept endpoint %d, want a fresh number", n)
	}
	if n := s.reserve(t, unexposed, donor); n != unexposedNumber {
		t.Errorf("the unexposed key moved from endpoint %d to %d, want its number kept", unexposedNumber, n)
	}
}
