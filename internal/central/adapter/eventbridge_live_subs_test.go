// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// weekProfileStateTopicSuffix is the tail of the raw-plane topic the
// week-profile pointer subscription publishes to. Counting publications on it
// is how these tests observe "the callback fired once" without reaching into
// the bridge's bookkeeping.
const weekProfileStateTopicSuffix = "/week_profile/state"

// liveSubHarness is one central carrying one ingested device, wired to an
// EventBridge with a recording MQTT client.
type liveSubHarness struct {
	unit     *central.Unit
	pipeline *DevicePipeline
	bridge   *EventBridge
	pub      *mqtt.NoopClient
}

// newLiveSubHarness ingests a device through the real [DevicePipeline] — the
// path that builds the Channel objects a reconnect and a re-ingest disagree
// about — and starts an EventBridge over it.
func newLiveSubHarness(t *testing.T) *liveSubHarness {
	t.Helper()

	u, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(u); err != nil {
		t.Fatalf("register: %v", err)
	}

	h := &liveSubHarness{unit: u, pipeline: NewDevicePipeline(u)}
	h.ingest(t)
	u.MarkSouthboundReady()

	h.pub = mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, h.pub)
	h.bridge = NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	h.bridge.Start(context.Background())
	t.Cleanup(h.bridge.Stop)
	return h
}

// ingest runs the production ingest for the fixture device and installs the
// week profile the MASTER hydration loop installs for a channel that carries
// week-program slots (see the attachWeekProfileToChannel call in
// [DevicePipeline.hydrateParamset]).
func (h *liveSubHarness) ingest(t *testing.T) {
	t.Helper()
	updatable := true
	descs := []hmproto.DeviceDescription{
		{Address: "0001ABCD", Type: "HmIP-STH", Firmware: "1.0.0", FirmwareUpdatable: &updatable},
		{Address: "0001ABCD:1", Parent: "0001ABCD", Type: "HEATING_CLIMATECONTROL_TRANSCEIVER"},
	}
	if err := h.pipeline.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, descs); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	dev := h.device(t)
	dev.AttachUpdate(nil, nil)
	attachWeekProfileToChannel(dev.Channel("0001ABCD:1"), h.unit.Name())
}

func (h *liveSubHarness) device(t *testing.T) *device.Device {
	t.Helper()
	d, ok := h.unit.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("device missing from the model registry")
	}
	return d
}

// weekProfileStatePublishes counts the raw-plane week-profile state topics the
// bridge has written so far.
func (h *liveSubHarness) weekProfileStatePublishes() int {
	n := 0
	for _, p := range h.pub.Published() {
		if strings.HasSuffix(p.Topic, weekProfileStateTopicSuffix) {
			n++
		}
	}
	return n
}

// liveSubCount reports how many live model-object subscriptions the bridge
// holds. Test-only accessor: the registry is guarded by startMu.
func (b *EventBridge) liveSubCount() int {
	b.startMu.Lock()
	defer b.startMu.Unlock()
	return len(b.liveSubs)
}

// TestRepeatedSnapshotPassesSubscribeTheSameObjectsOnlyOnce drives what a
// broker reconnect does: the MQTT lifecycle re-runs the snapshot over a model
// that did not change. Every pass used to wire another callback onto the very
// same week profile, so after N reconnects one profile-pointer change fanned
// out N times and the release list had grown by N — for the life of the
// daemon.
func TestRepeatedSnapshotPassesSubscribeTheSameObjectsOnlyOnce(t *testing.T) {
	t.Parallel()

	h := newLiveSubHarness(t)
	h.bridge.PublishInitialSnapshot(context.Background())
	first := h.bridge.liveSubCount()
	if first == 0 {
		t.Fatal("the first snapshot pass wired no live subscription at all")
	}

	const reconnects = 5
	for range reconnects {
		h.bridge.PublishInitialSnapshot(context.Background())
	}
	if got := h.bridge.liveSubCount(); got != first {
		t.Fatalf("live subscriptions grew from %d to %d across %d reconnects — "+
			"the same objects were subscribed again on every pass", first, got, reconnects)
	}

	wp := h.device(t).Channel("0001ABCD:1").WeekProfile()
	before := h.weekProfileStatePublishes()
	if err := wp.SyncProfilePointer(3); err != nil {
		t.Fatalf("sync profile pointer: %v", err)
	}
	if got := h.weekProfileStatePublishes() - before; got != 1 {
		t.Fatalf("one profile-pointer change produced %d state publishes, want 1", got)
	}
}

// TestReingestSubscribesTheReplacementObjectsAndReleasesTheOld pins the case
// that forbids keying the subscription on the address: a mid-life re-ingest
// rebuilds the Channel and everything hanging off it, and the replacements are
// what the model serves from then on. Skipping them because "that address is
// already subscribed" would lose every later change silently.
func TestReingestSubscribesTheReplacementObjectsAndReleasesTheOld(t *testing.T) {
	t.Parallel()

	h := newLiveSubHarness(t)
	h.bridge.PublishInitialSnapshot(context.Background())
	first := h.bridge.liveSubCount()

	oldChannel := h.device(t).Channel("0001ABCD:1")
	oldProfile := oldChannel.WeekProfile()

	// The real ingest path, run a second time exactly as a reconnect runs it.
	h.ingest(t)

	newChannel := h.device(t).Channel("0001ABCD:1")
	if newChannel == oldChannel {
		t.Fatal("re-ingest reused the channel object — this test no longer covers the replacement case")
	}
	newProfile := newChannel.WeekProfile()
	if newProfile == oldProfile {
		t.Fatal("re-ingest reused the week profile — this test no longer covers the replacement case")
	}

	h.bridge.PublishCentralSnapshot(context.Background(), "ccu-01")
	if got := h.bridge.liveSubCount(); got != first {
		t.Fatalf("live subscriptions = %d after the re-ingest pass, want %d — "+
			"the replacement objects must take the slots over, not add to them", got, first)
	}

	before := h.weekProfileStatePublishes()
	if err := oldProfile.SyncProfilePointer(4); err != nil {
		t.Fatalf("sync replaced profile: %v", err)
	}
	if got := h.weekProfileStatePublishes() - before; got != 0 {
		t.Fatalf("the replaced week profile still published %d times — its callback was never released", got)
	}

	before = h.weekProfileStatePublishes()
	if err := newProfile.SyncProfilePointer(5); err != nil {
		t.Fatalf("sync replacement profile: %v", err)
	}
	if got := h.weekProfileStatePublishes() - before; got != 1 {
		t.Fatalf("the replacement week profile published %d times, want 1 — "+
			"an address-keyed skip would leave it unsubscribed", got)
	}
}

// TestRemovedDeviceReleasesItsLiveSubscriptions covers the one path no later
// pass can reach: once the device is out of the model, nothing walks its
// objects again, so the callbacks have to be released when the removal is
// announced. Driven through the bus subscription the bridge installs in
// Start, not by calling the handler.
func TestRemovedDeviceReleasesItsLiveSubscriptions(t *testing.T) {
	t.Parallel()

	h := newLiveSubHarness(t)
	h.bridge.PublishInitialSnapshot(context.Background())
	if h.bridge.liveSubCount() == 0 {
		t.Fatal("no live subscription to release")
	}
	wp := h.device(t).Channel("0001ABCD:1").WeekProfile()

	publishDeviceRemoved(h.unit, "0001ABCD")
	h.bridge.Flush()

	if got := h.bridge.liveSubCount(); got != 0 {
		t.Fatalf("removed device left %d live subscriptions installed", got)
	}
	before := h.weekProfileStatePublishes()
	if err := wp.SyncProfilePointer(2); err != nil {
		t.Fatalf("sync profile pointer: %v", err)
	}
	if got := h.weekProfileStatePublishes() - before; got != 0 {
		t.Fatalf("a removed device's week profile still published %d times", got)
	}
}

// publishDeviceRemoved announces a device removal on the central's own bus,
// the way the device coordinator does.
func publishDeviceRemoved(u *central.Unit, address string) {
	events.Publish(u.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: u.Name(),
		InterfaceID: "HmIP-RF",
		Address:     address,
	})
}
