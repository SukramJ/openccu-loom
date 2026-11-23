// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// reachableRecorder captures NotifyDeviceReachable-shaped forwards.
type reachableRecorder struct {
	mu    sync.Mutex
	calls []reachableCall
}

type reachableCall struct {
	central   string
	address   string
	reachable bool
}

func (r *reachableRecorder) notify(centralName, address string, reachable bool) {
	r.mu.Lock()
	r.calls = append(r.calls, reachableCall{central: centralName, address: address, reachable: reachable})
	r.mu.Unlock()
}

func (r *reachableRecorder) snapshot() []reachableCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]reachableCall(nil), r.calls...)
}

func publishAvailability(u *central.Unit, address string, available bool) {
	u.EventBus.Publish(hmevent.DeviceLifecycleEvent{
		Base:        hmevent.NewBase(),
		CentralName: u.Name(),
		Address:     address,
		Subtype:     hmenum.DeviceLifecycleSubtypeAvailabilityChanged,
		Available:   available,
	})
}

// TestMatterReachableForwardReachesBootAndAdoptedCentrals pins the forward by
// its effect over a real registry: a central registered before the forward is
// wired (boot) and one registered after it (live adopt) both forward their
// AvailabilityChanged events, a lifecycle event of another subtype forwards
// nothing, and the seam is in the registry's manifest.
func TestMatterReachableForwardReachesBootAndAdoptedCentrals(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-boot")
	rec := &reachableRecorder{}
	remove := wireMatterReachableForward(reg, rec.notify)
	t.Cleanup(remove)

	adopted, err := central.New(central.Config{Name: "ccu-adopted"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(adopted); err != nil {
		t.Fatalf("Register: %v", err)
	}
	boot, _ := reg.Get("ccu-boot")

	publishAvailability(boot, "VCU0000001", false)
	publishAvailability(adopted, "VCU0000002", true)
	adopted.EventBus.Publish(hmevent.DeviceLifecycleEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-adopted",
		Address:     "VCU0000003",
		Subtype:     hmenum.DeviceLifecycleSubtypeCreated,
	})

	got := rec.snapshot()
	want := []reachableCall{
		{central: "ccu-boot", address: "VCU0000001", reachable: false},
		{central: "ccu-adopted", address: "VCU0000002", reachable: true},
	}
	if len(got) != len(want) {
		t.Fatalf("forwards = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("forward %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if !reg.Manifest().Has("matter.reachable_forward") {
		t.Error("seam matter.reachable_forward is not declared in the registry manifest")
	}
}

// TestMatterReachableForwardRemoveStopsEveryCentral pins the teardown: after
// remove, no central forwards, whether it was there at boot or adopted.
func TestMatterReachableForwardRemoveStopsEveryCentral(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-boot")
	rec := &reachableRecorder{}
	remove := wireMatterReachableForward(reg, rec.notify)
	remove()

	adopted, err := central.New(central.Config{Name: "ccu-adopted"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(adopted); err != nil {
		t.Fatalf("Register: %v", err)
	}
	boot, _ := reg.Get("ccu-boot")
	publishAvailability(boot, "VCU0000001", false)
	publishAvailability(adopted, "VCU0000002", false)

	if got := rec.snapshot(); len(got) != 0 {
		t.Errorf("forwards after remove = %+v, want none", got)
	}
}

// TestMatterRuntimeWiresTheReachableForward pins the seam through the
// composition root: a running Matter bridge declares matter.reachable_forward
// in the registry manifest. The forward's behaviour for boot and adopted
// centrals is pinned above against the same function; this is the proof that
// wireMatterRuntime makes it happen at all.
func TestMatterRuntimeWiresTheReachableForward(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-boot", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-boot")
	ctx, cancel := context.WithCancel(context.Background())
	wiring, closers, teardown := wireMatterRuntime(
		ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, nil, nil,
		slog.New(slog.DiscardHandler), nil, nil,
	)
	t.Cleanup(func() {
		cancel()
		for _, c := range closers {
			c()
		}
		if teardown != nil {
			teardown()
		}
	})
	if wiring.nodeUI.Node == nil {
		t.Fatal("the Matter runtime did not start; without a bridge this pin asserts nothing")
	}
	if !reg.Manifest().Has("matter.reachable_forward") {
		t.Fatal("wireMatterRuntime did not declare matter.reachable_forward")
	}
}
