// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCreateCustomDataPointsGroupAssignmentConcurrentWithChannelReaders
// reproduces the publication order the ingest pipeline actually uses:
// DevicePipeline.Ingest publishes every channel on its parent device — which
// is already in the model registry — and only afterwards does finishIngest
// walk that registry and materialise the custom data points. The channel-group
// assignment therefore lands on channels the north-bound planes can already
// reach, so it must be synchronised against them.
//
// The readers below are the production ones: Channel.Info backs the payload /
// MQTT surface and Channel.GroupNumber backs the REST channel summary and the
// sub-device discovery path. The test fails under -race while the group number
// is an unguarded exported field.
func TestCreateCustomDataPointsGroupAssignmentConcurrentWithChannelReaders(t *testing.T) {
	dev := newHmIPBwthDevice()

	registry := NewRegistry()
	profile := makeMaterializeProfile("IPThermostat", 1, 0, hmenum.DataPointCategoryClimate, nil)
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	ctor, _ := fakeCtor("IPThermostat")
	if err := registry.RegisterConstructor("IPThermostat", ctor); err != nil {
		t.Fatal(err)
	}

	// The channels are published before the materializer runs, exactly as
	// the pipeline does it.
	channels := dev.Channels()
	if len(channels) == 0 {
		t.Fatal("fixture device has no published channels")
	}

	stop := make(chan struct{})
	var (
		readers sync.WaitGroup
		running sync.WaitGroup
	)
	for range 4 {
		readers.Add(1)
		running.Add(1)
		go func() {
			defer readers.Done()
			first := true
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, ch := range channels {
					_ = ch.Info()
					_ = ch.GroupNumber()
					_ = ch.IsGroupMaster()
					_ = ch.SubDeviceName()
				}
				if first {
					// Only start the writer once every reader is actually
					// spinning; otherwise the materialiser can finish before
					// the goroutines are scheduled and the overlap the test
					// exists to exercise never happens.
					first = false
					running.Done()
				}
			}
		}()
	}
	running.Wait()

	if err := CreateCustomDataPoints(dev, registry); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}
	close(stop)
	readers.Wait()

	// The assignment itself must still be visible to a reader that arrives
	// after materialisation — a lock that drops the write is no better than
	// the race.
	var ch1 *device.Channel
	for _, ch := range channels {
		if ch.Number == 1 {
			ch1 = ch
		}
	}
	if ch1 == nil {
		t.Fatal("channel 1 missing from fixture")
	}
	if got := ch1.GroupNumber(); got != 1 {
		t.Fatalf("channel 1 GroupNumber() = %d, want 1", got)
	}
}
