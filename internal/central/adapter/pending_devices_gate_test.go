// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// memorySink is an in-test [coordinators.PendingDeviceSink]. It stands in
// for SQLite so a test can simulate a restart — build a fresh coordinator
// over the same sink — without a database.
type memorySink struct {
	rows    map[string]map[string]struct{}
	addErr  error
	loadErr error
}

func newMemorySink() *memorySink {
	return &memorySink{rows: map[string]map[string]struct{}{}}
}

func (s *memorySink) Load(context.Context) (map[string][]string, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	out := map[string][]string{}
	for iface, set := range s.rows {
		for a := range set {
			out[iface] = append(out[iface], a)
		}
	}
	return out, nil
}

func (s *memorySink) Add(_ context.Context, interfaceID, address, _ string) error {
	if s.addErr != nil {
		return s.addErr
	}
	if s.rows[interfaceID] == nil {
		s.rows[interfaceID] = map[string]struct{}{}
	}
	s.rows[interfaceID][address] = struct{}{}
	return nil
}

func (s *memorySink) Remove(_ context.Context, interfaceID, address string) error {
	delete(s.rows[interfaceID], address)
	return nil
}

func (s *memorySink) Clear(context.Context) error {
	s.rows = map[string]map[string]struct{}{}
	return nil
}

func (s *memorySink) count() int {
	n := 0
	for _, set := range s.rows {
		n += len(set)
	}
	return n
}

func gateDescs() []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{Address: "GATE0001", Type: "HmIP-STH"},
		{Address: "GATE0001:1", Type: "HEATING_CLIMATECONTROL_TRANSCEIVER", Parent: "GATE0001"},
		{Address: "GATE0002", Type: "HmIP-PS"},
		{Address: "GATE0002:1", Type: "SWITCH_VIRTUAL_RECEIVER", Parent: "GATE0002"},
	}
}

// TestParkedDeviceSurvivesARestartAndIsWithheldByThePull is the guard for
// the whole gate.
//
// Before it, `delay_new_device_creation` was a notice, not a gate: the
// queue lived only in memory and the boot pull never consulted it, so a
// device the operator had not accepted was materialised by the next
// restart and its inbox entry vanished with the process. The operator's
// pending decision disappeared without a trace.
func TestParkedDeviceSurvivesARestartAndIsWithheldByThePull(t *testing.T) {
	t.Parallel()
	sink := newMemorySink()
	iface := hmtypes.ParseWireInterfaceID("ccu-gate-HmIP-RF")

	// Run 1: a device is paired and parked.
	c1, err := central.New(central.Config{Name: "ccu-gate"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c1.Devices.SetPendingDeviceSink(context.Background(), sink)
	c1.Devices.StoreDelayedDeviceDescriptions(context.Background(), iface, gateDescs()[:2])
	if sink.count() != 1 {
		t.Fatalf("sink holds %d row(s) after parking one device, want 1", sink.count())
	}

	// Run 2: a fresh process over the same durable queue.
	c2, err := central.New(central.Config{Name: "ccu-gate"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c2.Devices.SetPendingDeviceSink(context.Background(), sink)
	if !c2.Devices.IsParked(iface, "GATE0001") {
		t.Fatal("the parked device did not survive the restart — the decision is gone and the pull will materialise it")
	}

	// Run 2's boot pull, driven through the real entry point. Calling the
	// gate helper directly would prove only that it CAN withhold, never
	// that IngestFromBackend actually asks it — the bracketing defect this
	// repository has paid for before.
	p := NewDevicePipeline(c2)
	b := &paramsetFakeOps{
		listDevicesFn: func(context.Context) ([]hmproto.DeviceDescription, error) {
			return gateDescs(), nil
		},
	}
	if err := p.IngestFromBackend(
		context.Background(), string(iface), hmenum.InterfaceHmIPRF,
		b, nil, nil, slog.New(slog.DiscardHandler),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	if _, ok := c2.ModelRegistry.Get("GATE0001"); ok {
		t.Error("the held-back device was materialised by the pull — it is usable without an accept")
	}
	if _, ok := c2.ModelRegistry.Get("GATE0002"); !ok {
		t.Error("the unparked device was NOT materialised — the gate swallowed more than it should")
	}
	// It is on the inbox surface instead, described by this pull rather
	// than by anything the queue stored.
	listed := c2.HubModel.Inbox.List()
	if len(listed) != 1 || listed[0].Address != "GATE0001" {
		t.Errorf("inbox = %+v, want exactly the held-back GATE0001", listed)
	}
}

// TestUnparkedFleetIsNotWithheld is the negative control. An empty queue
// must let everything through: the gate honours the parked set, it never
// decides on its own that a pull result looks new. Getting that wrong
// would park an entire installation on the first boot after an upgrade,
// which reads exactly like a hundred simultaneous pairings.
func TestUnparkedFleetIsNotWithheld(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-nogate"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.Devices.SetPendingDeviceSink(context.Background(), newMemorySink())

	p := NewDevicePipeline(c)
	keep, held := p.withholdParked(context.Background(), "ccu-nogate-HmIP-RF", gateDescs(), nil)
	if len(held) != 0 {
		t.Errorf("withheld %d description(s) with an empty queue, want 0", len(held))
	}
	if len(keep) != len(gateDescs()) {
		t.Errorf("kept %d of %d descriptions", len(keep), len(gateDescs()))
	}
}

// TestAcceptClearsTheDurableDecision pins that accepting a device ends
// the hold for good. A queue that keeps the row after an accept parks the
// device again on the next restart — the operator would accept the same
// device forever.
func TestAcceptClearsTheDurableDecision(t *testing.T) {
	t.Parallel()
	sink := newMemorySink()
	iface := hmtypes.ParseWireInterfaceID("ccu-accept-HmIP-RF")
	c, err := central.New(central.Config{Name: "ccu-accept"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.Devices.SetPendingDeviceSink(context.Background(), sink)
	c.Devices.StoreDelayedDeviceDescriptions(context.Background(), iface, gateDescs()[:2])

	if got := c.Devices.TakeDelayedDeviceDescriptions(context.Background(), iface, "GATE0001"); len(got) != 2 {
		t.Fatalf("took %d description(s), want 2", len(got))
	}
	if c.Devices.IsParked(iface, "GATE0001") {
		t.Error("device is still parked in memory after the accept")
	}
	if sink.count() != 0 {
		t.Errorf("sink still holds %d row(s) after the accept — the next restart re-parks the device", sink.count())
	}
}

// TestTurningTheToggleOffReleasesTheQueue pins the off-switch. The
// setting means "ask me about new devices"; turning it off means "stop
// asking". Leaving rows behind would strand devices in a state whose only
// explanation is a setting that is no longer on, and that an operator
// could clear only through the database.
func TestTurningTheToggleOffReleasesTheQueue(t *testing.T) {
	t.Parallel()
	sink := newMemorySink()
	iface := hmtypes.ParseWireInterfaceID("ccu-toggle-HmIP-RF")
	c, err := central.New(central.Config{Name: "ccu-toggle"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.Devices.SetPendingDeviceSink(context.Background(), sink)
	c.Devices.StoreDelayedDeviceDescriptions(context.Background(), iface, gateDescs())
	if sink.count() != 2 {
		t.Fatalf("sink holds %d row(s), want 2", sink.count())
	}

	// This is what WirePendingDevices does when the toggle reads off.
	WirePendingDevices(context.Background(), c, PendingDeviceStores{}, false, nil)
	if sink.count() != 2 {
		t.Fatal("a nil store must leave the queue alone rather than silently release it")
	}

	if freed := c.Devices.ReleaseAllParked(context.Background()); freed != 2 {
		t.Errorf("released %d device(s), want 2", freed)
	}
	if c.Devices.IsParked(iface, "GATE0001") {
		t.Error("device still parked after the release")
	}
	if sink.count() != 0 {
		t.Errorf("sink still holds %d row(s) after the release", sink.count())
	}
}

// TestParkedDeviceGoneFromTheCCUIsSwept pins the collection of rows the
// pull can never fill. A parked row carries no descriptions, so a device
// unpaired while the daemon was down would sit on the inbox surface
// forever naming a device that does not exist — and an operator accepting
// it would get nothing.
func TestParkedDeviceGoneFromTheCCUIsSwept(t *testing.T) {
	t.Parallel()
	sink := newMemorySink()
	iface := hmtypes.ParseWireInterfaceID("ccu-sweep-HmIP-RF")
	c, err := central.New(central.Config{Name: "ccu-sweep"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.Devices.SetPendingDeviceSink(context.Background(), sink)
	c.Devices.StoreDelayedDeviceDescriptions(context.Background(), iface, gateDescs())

	// The next pull reports only GATE0002 — GATE0001 was unpaired on the
	// CCU while the daemon was down.
	remaining := []hmproto.DeviceDescription{
		{Address: "GATE0002", Type: "HmIP-PS"},
		{Address: "GATE0002:1", Type: "SWITCH_VIRTUAL_RECEIVER", Parent: "GATE0002"},
	}
	p := NewDevicePipeline(c)
	_, held := p.withholdParked(context.Background(), string(iface), remaining, nil)

	if c.Devices.IsParked(iface, "GATE0001") {
		t.Error("a device the CCU no longer reports is still parked")
	}
	if sink.count() != 1 {
		t.Errorf("sink holds %d row(s) after the sweep, want 1", sink.count())
	}
	if len(held) != 2 {
		t.Errorf("withheld %d description(s) of the surviving parked device, want 2", len(held))
	}
}

// TestAStoreFailureHoldsTheDeviceBackAnyway pins the safe direction of a
// failing database: the device stays parked in memory for this run.
//
// The opposite — dropping the decision because it could not be written —
// materialises a device the operator never accepted, and does it silently.
// A run that forgets across a restart is recoverable; a device that
// appears without approval is the failure the gate exists to prevent.
func TestAStoreFailureHoldsTheDeviceBackAnyway(t *testing.T) {
	t.Parallel()
	sink := newMemorySink()
	sink.addErr = context.DeadlineExceeded
	iface := hmtypes.ParseWireInterfaceID("ccu-dberr-HmIP-RF")
	c, err := central.New(central.Config{Name: "ccu-dberr"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.Devices.SetPendingDeviceSink(context.Background(), sink)
	c.Devices.StoreDelayedDeviceDescriptions(context.Background(), iface, gateDescs()[:2])

	if !c.Devices.IsParked(iface, "GATE0001") {
		t.Fatal("a store failure dropped the hold; the device would be materialised without an accept")
	}
}

// TestALoadFailureHoldsNothingBack pins the opposite direction for the
// restore, and the asymmetry is deliberate: a database hiccup that
// presented the whole installation as pending is the failure an operator
// cannot tell from a real defect, so a failed load degrades to an open
// gate rather than a closed one.
func TestALoadFailureHoldsNothingBack(t *testing.T) {
	t.Parallel()
	sink := newMemorySink()
	sink.rows["ccu-loaderr-HmIP-RF"] = map[string]struct{}{"GATE0001": {}}
	sink.loadErr = context.DeadlineExceeded

	c, err := central.New(central.Config{Name: "ccu-loaderr"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.Devices.SetPendingDeviceSink(context.Background(), sink)
	if c.Devices.IsParked(hmtypes.ParseWireInterfaceID("ccu-loaderr-HmIP-RF"), "GATE0001") {
		t.Error("a failed load must not hold anything back")
	}
}

// Compile-time assertion: the in-test sink satisfies the port the
// coordinator declares, so this file breaks if the port changes shape.
var _ coordinators.PendingDeviceSink = (*memorySink)(nil)
