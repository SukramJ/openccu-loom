// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// countingClimateLoader stands in for the CCU-backed schedule read the
// snapshot pass warms up in the background. It counts every invocation before
// honouring the context, so a warm-up goroutine that should never have been
// spawned is still observed. The delay keeps a goroutine in flight while a
// concurrent Stop() drains.
type countingClimateLoader struct {
	calls atomic.Int32
	delay time.Duration
}

func (l *countingClimateLoader) Load(ctx context.Context) (*schedule.Climate, error) {
	l.calls.Add(1)
	if l.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(l.delay):
		}
	}
	return schedule.NewClimate(), nil
}

// climateBridgeFixture builds a registry holding one device with a climate
// week profile — the shape that makes a snapshot pass spawn a background
// schedule warm-up — plus an EventBridge wired to a noop broker.
func climateBridgeFixture(t *testing.T, loader *countingClimateLoader) *EventBridge {
	t.Helper()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel(dev.Address+":1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-01",
		ChannelAddress: ch.Address,
		ScheduleType:   weekprofile.ScheduleTypeClimate,
	})
	wp.AttachClimateProfile(weekprofile.NewClimate(loader, nil))
	ch.AttachWeekProfile(wp)

	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, mqtt.NewNoopClient())
	return NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
}

// TestSnapshotAfterStopSpawnsNoBackgroundLoad pins the teardown half of the
// background-load contract.
//
// A snapshot pass runs on the MQTT lifecycle's reconnect goroutine and on the
// fan-out worker; Stop() stops neither before it waits on the warm-up wait
// group. Adding to that wait group straight from the pass let a warm-up
// goroutine start after Stop had drained — it then loaded a schedule and
// published it into a torn-down bridge — and, when the Add landed on the zero
// counter Wait() was parked on, aborted the daemon with "sync: WaitGroup
// misuse: Add called concurrently with Wait".
//
// The assertion is the effect: once the bridge is stopped, no further warm-up
// is started at all.
func TestSnapshotAfterStopSpawnsNoBackgroundLoad(t *testing.T) {
	t.Parallel()

	loader := &countingClimateLoader{}
	eb := climateBridgeFixture(t, loader)

	eb.Start(context.Background())
	eb.PublishInitialSnapshot(context.Background())
	eb.Stop()

	before := loader.calls.Load()
	if before == 0 {
		t.Fatal("precondition: the running bridge must warm the climate schedule up")
	}

	// A broker reconnect racing the teardown: the pass lands after Stop.
	eb.PublishInitialSnapshot(context.Background())
	time.Sleep(50 * time.Millisecond) // let a wrongly-spawned goroutine surface

	if got := loader.calls.Load(); got != before {
		t.Fatalf("schedule warm-up ran %d more time(s) after Stop; a goroutine outlived the "+
			"bridge and its Add raced goroutineWG.Wait()", got-before)
	}
}

// TestSnapshotConcurrentWithStopIsRaceFree drives the same interleaving from
// two goroutines instead of in sequence: repeated snapshot passes against a
// Stop(). Run under -race; the assertion is that the process survives and
// every Stop returns.
func TestSnapshotConcurrentWithStopIsRaceFree(t *testing.T) {
	t.Parallel()

	loader := &countingClimateLoader{delay: time.Millisecond}
	for range 20 {
		eb := climateBridgeFixture(t, loader)
		eb.Start(context.Background())

		var wg sync.WaitGroup
		wg.Go(func() {
			for range 5 {
				eb.PublishInitialSnapshot(context.Background())
			}
		})
		wg.Go(eb.Stop)
		wg.Wait()

		// The gate stays closed, so a second Stop has nothing left to drain.
		eb.Stop()
	}
}

// TestPublishInitialSnapshotWarmsSchedulesWithoutStart pins why the gate keys
// on "stopping" and not on "started": PublishInitialSnapshot is a supported
// call on a bridge that was never started, and must still warm schedules up.
func TestPublishInitialSnapshotWarmsSchedulesWithoutStart(t *testing.T) {
	t.Parallel()

	loader := &countingClimateLoader{}
	eb := climateBridgeFixture(t, loader)

	eb.PublishInitialSnapshot(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for loader.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if loader.calls.Load() == 0 {
		t.Fatal("schedule warm-up never ran for a snapshot published without Start")
	}
}
