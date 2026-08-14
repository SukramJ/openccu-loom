// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// P0-3: DeviceCoordinator owns the initial-pull pipeline. The diff
// against the cache must be idempotent on a stable snapshot, surface
// new devices through DeviceCreatedEvent, and tear stale ones down via
// DeviceRemovedEvent. RefreshAfterPair re-runs the pull; RefreshAfter-
// Unpair drops a single device.

type stubLister struct {
	snapshot []hmproto.DeviceDescription
	err      error
	calls    int
}

func (s *stubLister) ListDevices(_ context.Context, _ hmtypes.WireInterfaceID) ([]hmproto.DeviceDescription, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	out := make([]hmproto.DeviceDescription, len(s.snapshot))
	copy(out, s.snapshot)
	return out, nil
}

func newDC(t *testing.T) (*DeviceCoordinator, *events.Bus) {
	t.Helper()
	bus := events.NewBus()
	devs := registry.NewDeviceRegistry()
	descs := registry.NewDeviceDescriptionRegistry()
	psets := registry.NewParamsetRegistry()
	return NewDeviceCoordinator("c1", bus, devs, descs, psets, nil), bus
}

func TestInitialPullCreatesDevicesAndChannels(t *testing.T) {
	t.Parallel()
	dc, bus := newDC(t)
	created := make([]hmevent.DeviceCreatedEvent, 0, 2)
	events.Subscribe(bus, func(e hmevent.DeviceCreatedEvent) {
		created = append(created, e)
	})
	lister := &stubLister{snapshot: []hmproto.DeviceDescription{
		{Address: "0001ABCD", Type: "HmIP-STH", Children: []string{"0001ABCD:0", "0001ABCD:1"}},
		{Address: "0001ABCD:0", Type: "MAINTENANCE", Parent: "0001ABCD"},
		{Address: "0001ABCD:1", Type: "CLIMATECONTROL_RT_TRANSCEIVER", Parent: "0001ABCD"},
	}}

	rep, err := dc.InitialPull(context.Background(), lister, wireKey(hmenum.InterfaceHmIPRF))
	if err != nil {
		t.Fatalf("pull err=%v", err)
	}
	if rep.Created != 1 || rep.Updated != 0 || rep.Removed != 0 {
		t.Fatalf("rep=%+v", rep)
	}
	if len(created) != 1 || created[0].Address != "0001ABCD" {
		t.Fatalf("expected 1 DeviceCreatedEvent for 0001ABCD, got %+v", created)
	}
	if lister.calls != 1 {
		t.Fatalf("lister calls=%d", lister.calls)
	}
}

func TestInitialPullIsIdempotent(t *testing.T) {
	t.Parallel()
	dc, _ := newDC(t)
	lister := &stubLister{snapshot: []hmproto.DeviceDescription{
		{Address: "AA", Type: "HmIP-X"},
	}}
	if _, err := dc.InitialPull(context.Background(), lister, wireKey(hmenum.InterfaceHmIPRF)); err != nil {
		t.Fatal(err)
	}
	rep, err := dc.InitialPull(context.Background(), lister, wireKey(hmenum.InterfaceHmIPRF))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created+rep.Updated+rep.Removed != 0 {
		t.Fatalf("second pull must be a no-op, got %+v", rep)
	}
}

func TestInitialPullDetectsModelChange(t *testing.T) {
	t.Parallel()
	dc, _ := newDC(t)
	first := &stubLister{snapshot: []hmproto.DeviceDescription{
		{Address: "AA", Type: "HmIP-X", Firmware: "1.0"},
	}}
	if _, err := dc.InitialPull(context.Background(), first, wireKey(hmenum.InterfaceHmIPRF)); err != nil {
		t.Fatal(err)
	}
	second := &stubLister{snapshot: []hmproto.DeviceDescription{
		{Address: "AA", Type: "HmIP-X", Firmware: "1.1"},
	}}
	rep, err := dc.InitialPull(context.Background(), second, wireKey(hmenum.InterfaceHmIPRF))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated != 1 {
		t.Fatalf("firmware bump must register as Updated, got %+v", rep)
	}
}

func TestInitialPullEmitsRemovedForVanishedDevices(t *testing.T) {
	t.Parallel()
	dc, bus := newDC(t)
	removed := make([]hmevent.DeviceRemovedEvent, 0, 1)
	events.Subscribe(bus, func(e hmevent.DeviceRemovedEvent) {
		removed = append(removed, e)
	})
	full := &stubLister{snapshot: []hmproto.DeviceDescription{
		{Address: "AA", Type: "HmIP-X"},
		{Address: "BB", Type: "HmIP-Y"},
	}}
	if _, err := dc.InitialPull(context.Background(), full, wireKey(hmenum.InterfaceHmIPRF)); err != nil {
		t.Fatal(err)
	}
	shrunk := &stubLister{snapshot: []hmproto.DeviceDescription{
		{Address: "AA", Type: "HmIP-X"},
	}}
	rep, err := dc.InitialPull(context.Background(), shrunk, wireKey(hmenum.InterfaceHmIPRF))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Removed != 1 {
		t.Fatalf("expected 1 removal, got %+v", rep)
	}
	if len(removed) != 1 || removed[0].Address != "BB" {
		t.Fatalf("missing DeviceRemovedEvent: %+v", removed)
	}
}

func TestInitialPullSurfacesListerError(t *testing.T) {
	t.Parallel()
	dc, _ := newDC(t)
	lister := &stubLister{err: errors.New("offline")}
	if _, err := dc.InitialPull(context.Background(), lister, wireKey(hmenum.InterfaceHmIPRF)); err == nil {
		t.Fatal("expected error from lister")
	}
}

func TestInitialPullNilLister(t *testing.T) {
	t.Parallel()
	dc, _ := newDC(t)
	if _, err := dc.InitialPull(context.Background(), nil, wireKey(hmenum.InterfaceHmIPRF)); err == nil {
		t.Fatal("expected error for nil lister")
	}
}

func TestRefreshAfterPairReusesPullPath(t *testing.T) {
	t.Parallel()
	dc, _ := newDC(t)
	lister := &stubLister{snapshot: []hmproto.DeviceDescription{
		{Address: "AA", Type: "HmIP-X"},
	}}
	rep, err := dc.RefreshAfterPair(context.Background(), lister, wireKey(hmenum.InterfaceHmIPRF))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != 1 {
		t.Fatalf("rep=%+v", rep)
	}
	if lister.calls != 1 {
		t.Fatalf("lister calls=%d", lister.calls)
	}
}

func TestRefreshAfterUnpairDropsDevice(t *testing.T) {
	t.Parallel()
	dc, bus := newDC(t)
	removed := make([]hmevent.DeviceRemovedEvent, 0, 1)
	events.Subscribe(bus, func(e hmevent.DeviceRemovedEvent) {
		removed = append(removed, e)
	})
	lister := &stubLister{snapshot: []hmproto.DeviceDescription{
		{Address: "AA", Type: "HmIP-X"},
	}}
	if _, err := dc.InitialPull(context.Background(), lister, wireKey(hmenum.InterfaceHmIPRF)); err != nil {
		t.Fatal(err)
	}
	if !dc.RefreshAfterUnpair(context.Background(), wireKey(hmenum.InterfaceHmIPRF), "AA") {
		t.Fatal("expected removal=true")
	}
	if dc.RefreshAfterUnpair(context.Background(), wireKey(hmenum.InterfaceHmIPRF), "AA") {
		t.Fatal("second unpair must be a no-op (return false)")
	}
	if len(removed) != 1 || removed[0].Address != "AA" {
		t.Fatalf("missing event: %+v", removed)
	}
}

func TestSameDescriptionEdgeCases(t *testing.T) {
	t.Parallel()
	a := hmproto.DeviceDescription{Address: "X", Type: "T", Firmware: "1.0", Children: []string{"a", "b"}}
	b := a
	if !sameDescription(a, b) {
		t.Fatal("identical descriptions must compare equal")
	}
	c := a
	c.Children = []string{"a"}
	if sameDescription(a, c) {
		t.Fatal("child-list change must register as different")
	}
	d := a
	d.Firmware = "1.1"
	if sameDescription(a, d) {
		t.Fatal("firmware change must register as different")
	}
}
