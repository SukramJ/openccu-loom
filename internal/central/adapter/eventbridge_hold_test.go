// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

const holdDevAddr = "0001ABCD"

// holdEnv is a ready central with one device carrying a discovery-producing
// data point, published once, behind a bridge with HA discovery on.
type holdEnv struct {
	eb   *EventBridge
	u    *central.Unit
	dev  *device.Device
	pub  *mqtt.NoopClient
	mqtt *mqtt.Bridge
}

func newHoldEnv(t *testing.T) *holdEnv {
	t.Helper()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel(holdDevAddr+":1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID: "HmIP-RF", ChannelAddress: holdDevAddr + ":1",
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "OPERATING_VOLTAGE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	// Discovery names an entity from its naming data, which the pipeline
	// sets in production.
	dp.SetNameData(device.BuildDataPointName(ch, "OPERATING_VOLTAGE", ""))
	dp.SetPathData(naming.NewDataPointPathData(
		"", hmtypes.NewWireInterfaceID("", hmenum.InterfaceHmIPRF), ch.Address, ch.Number, payload.BucketValues, "OPERATING_VOLTAGE",
	))
	dp.OnEvent(30)
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true, HADiscoveryEnabled: true,
		DiscoveryBuilder: mqtt.NewDefaultDiscoveryBuilder(mqtt.NewTopicBuilder("openccu-loom"), "ccu-01"),
	}, pub)
	// Every discovery unique_id is gated on the CCU serial.
	bridge.SetHubInfoFor("ccu-01", mqtt.HubInfo{Name: "ccu-01", Serial: "HOLD0001"})
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	t.Cleanup(eb.Stop)
	u, _ := reg.Get("ccu-01")
	eb.PublishCentralSnapshot(context.Background(), "ccu-01")
	eb.Flush()
	env := &holdEnv{eb: eb, u: u, dev: dev, pub: pub, mqtt: bridge}
	// The snapshot carries the raw plane; the discovery config is declared
	// directly under the device's node id, which is all the hold matches on.
	if err := bridge.PublishHubDiscovery(context.Background(), mqtt.DiscoveryItem{
		Component: "sensor",
		NodeID:    naming.DiscoverySlug("ccu-01") + "_" + strings.ToLower(holdDevAddr),
		ObjectID:  "operating_voltage",
		Payload:   []byte(`{"name":"Voltage","unique_id":"hold_test_voltage"}`),
		OK:        true,
	}); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if env.configs() == 0 {
		t.Fatal("precondition: the snapshot declared no discovery config for the device")
	}
	return env
}

// configs counts the retained discovery configs the device has declared.
func (e *holdEnv) configs() int {
	n := 0
	for _, p := range e.pub.Published() {
		if strings.HasPrefix(p.Topic, "homeassistant/") && strings.Contains(p.Topic, strings.ToLower(holdDevAddr)) && len(p.Payload) > 0 {
			n++
		}
	}
	return n
}

// empties counts the empty publishes to the device's discovery configs and
// raw topics.
func (e *holdEnv) empties() int {
	n := 0
	for _, p := range e.pub.Published() {
		if len(p.Payload) == 0 && strings.Contains(strings.ToLower(p.Topic), strings.ToLower(holdDevAddr)) {
			n++
		}
	}
	return n
}

// rawEmpties counts the empty publishes to the device's raw-plane topics.
func (e *holdEnv) rawEmpties() int {
	n := 0
	for _, p := range e.pub.Published() {
		if len(p.Payload) == 0 && strings.HasPrefix(p.Topic, "openccu-loom/") && strings.Contains(p.Topic, holdDevAddr) {
			n++
		}
	}
	return n
}

// tearDown removes the device as a restart or re-pull does and waits until
// the bridge handled the removal.
func (e *holdEnv) tearDown(t *testing.T) {
	t.Helper()
	e.u.BeginModelTeardown()
	e.u.RemoveDeviceForTeardown(holdDevAddr)
	waitFor(t, func() bool { return len(e.mqtt.HeldDiscoveryGroups("ccu-01")) == 1 })
	e.eb.Flush()
}

// complete latches the reloaded model ready and publishes its snapshot.
func (e *holdEnv) complete() {
	e.u.MarkSouthboundReady()
	e.eb.PublishCentralSnapshot(context.Background(), "ccu-01")
	e.eb.Flush()
}

// TestAModelTeardownHoldsTheDeviceInsteadOfRetracting: a removal while the
// generation is incomplete leaves every topic of the device in place and
// writes its availability offline.
func TestAModelTeardownHoldsTheDeviceInsteadOfRetracting(t *testing.T) {
	t.Parallel()
	e := newHoldEnv(t)
	e.tearDown(t)
	if n := e.empties(); n != 0 {
		t.Fatalf("%d empty publishes during the teardown, want 0", n)
	}
	var offline bool
	for _, p := range e.pub.Published() {
		if statusSuffix(p.Topic, "/"+holdDevAddr+"/online") && strings.Contains(string(p.Payload), "false") {
			offline = true
		}
	}
	if !offline {
		t.Error("the held device's availability was not written offline")
	}
}

// TestADeviceTheReloadedModelPublishesKeepsItsConfigs: the device comes back
// and the snapshot republishes it. Its raw topics are never emptied; of its
// configs only the one the reloaded model no longer declares is retracted
// (the fixture's config is declared outside the snapshot, so it is that one).
func TestADeviceTheReloadedModelPublishesKeepsItsConfigs(t *testing.T) {
	t.Parallel()
	e := newHoldEnv(t)
	e.tearDown(t)
	e.u.ModelRegistry.Put(e.dev)
	e.complete()
	if n := e.rawEmpties(); n != 0 {
		t.Fatalf("%d empty raw publishes for a device that came back, want 0", n)
	}
	if n := e.empties(); n != 1 {
		t.Fatalf("%d empty publishes, want exactly the undeclared config", n)
	}
	if g := e.mqtt.HeldDiscoveryGroups("ccu-01"); len(g) != 0 {
		t.Errorf("hold not settled: %v", g)
	}
}

// TestADeviceTheReloadedModelLacksIsRetractedAfterTheSnapshot: a device that
// does not come back on an interface the central no longer configures is
// retracted, once the model is complete and not before.
func TestADeviceTheReloadedModelLacksIsRetractedAfterTheSnapshot(t *testing.T) {
	t.Parallel()
	e := newHoldEnv(t)
	e.tearDown(t)
	// The latch is never cleared, so the snapshot of a reconnect runs during
	// the reload: it must not settle the hold against the partial model.
	e.eb.PublishCentralSnapshot(context.Background(), "ccu-01")
	e.eb.Flush()
	if n := e.empties(); n != 0 {
		t.Fatalf("%d empty publishes before the model was complete, want 0", n)
	}
	e.complete()
	if n := e.empties(); n == 0 || e.rawEmpties() == 0 {
		t.Fatalf("the device the reloaded model lacks was not retracted on both planes (%d empties)", n)
	}
}

// TestADeviceOfAnInterfaceThatLoadedNothingStaysHeld: an interface that is
// configured but carries no device after the reload failed its ingest, so
// its absent devices prove nothing and stay declared.
func TestADeviceOfAnInterfaceThatLoadedNothingStaysHeld(t *testing.T) {
	t.Parallel()
	e := newHoldEnv(t)
	if err := e.u.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceHmIPRF),
		Interface:   hmenum.InterfaceHmIPRF,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	e.tearDown(t)
	e.complete()
	if n := e.empties(); n != 0 {
		t.Fatalf("%d empty publishes for a device of an interface that loaded nothing, want 0", n)
	}
	if g := e.mqtt.HeldDiscoveryGroups("ccu-01"); len(g) != 1 {
		t.Errorf("held groups = %v, want the device still held", g)
	}
}

// TestReleasingAHoldRetractsTheHeldDevices: a removal of the central (or a
// failed adopt after a restart) releases the hold, and everything held goes.
func TestReleasingAHoldRetractsTheHeldDevices(t *testing.T) {
	t.Parallel()
	e := newHoldEnv(t)
	e.tearDown(t)
	e.eb.ReleaseModelHold("ccu-01")
	e.eb.Flush()
	if n := e.empties(); n == 0 {
		t.Fatal("releasing the hold retracted nothing")
	}
}

// TestARemovalOutsideAModelReloadRetractsAtOnce: a device the CCU deletes
// while the model is complete is gone for good, and holding it would leave
// its entities in Home Assistant until the next reload.
func TestARemovalOutsideAModelReloadRetractsAtOnce(t *testing.T) {
	t.Parallel()
	e := newHoldEnv(t)
	e.u.RemoveDevice(holdDevAddr)
	waitFor(t, func() bool { return e.empties() > 0 })
	if g := e.mqtt.HeldDiscoveryGroups("ccu-01"); len(g) != 0 {
		t.Errorf("a removal outside a reload was held: %v", g)
	}
}

// TestARemovalDispatchedDuringAReloadIsHeldEvenIfTheJobRunsLate: the
// fan-out worker can still be behind other jobs when the reloaded model
// completes. The removal belongs to the reload it was dispatched in, so it
// is held however late its job runs.
func TestARemovalDispatchedDuringAReloadIsHeldEvenIfTheJobRunsLate(t *testing.T) {
	t.Parallel()
	e := newHoldEnv(t)
	release := make(chan struct{})
	e.eb.enqueueDurable(func(context.Context) { <-release })
	e.u.BeginModelTeardown()
	e.u.RemoveDeviceForTeardown(holdDevAddr)
	// The reload completes while the removal's job is still queued.
	e.u.MarkSouthboundReady()
	close(release)
	e.eb.Flush()
	if n := e.empties(); n != 0 {
		t.Fatalf("%d empty publishes for a removal of a reload whose job ran late, want 0", n)
	}
	if g := e.mqtt.HeldDiscoveryGroups("ccu-01"); len(g) != 1 {
		t.Errorf("held groups = %v, want the device held", g)
	}
}

// TestATouchFromAReplacedGenerationProvesNothing: a reconnect snapshot of a
// reload that is torn down again published the device, but the next
// generation lacks it — it is gone, and both planes are retracted.
func TestATouchFromAReplacedGenerationProvesNothing(t *testing.T) {
	t.Parallel()
	e := newHoldEnv(t)
	e.tearDown(t)
	e.u.ModelRegistry.Put(e.dev)
	// A reconnect snapshot during the reload (the latch is never cleared).
	e.eb.PublishCentralSnapshot(context.Background(), "ccu-01")
	e.eb.Flush()
	// A second reload begins and the device does not come back.
	e.u.BeginModelTeardown()
	e.u.RemoveDeviceForTeardown(holdDevAddr)
	e.eb.Flush()
	e.complete()
	if e.rawEmpties() == 0 {
		t.Fatal("a device the completed generation lacks kept its raw topics because an earlier generation published it")
	}
}

// TestAPublishOfADeviceTheModelNoLongerHoldsIsNoTouch: a snapshot pass that
// walks a device list taken before the removal publishes the removed device
// once more; that does not make it part of the reloaded model.
func TestAPublishOfADeviceTheModelNoLongerHoldsIsNoTouch(t *testing.T) {
	t.Parallel()
	e := newHoldEnv(t)
	e.tearDown(t)
	e.eb.publishDeviceSnapshot(context.Background(), "ccu-01", e.dev)
	e.complete()
	if e.rawEmpties() == 0 {
		t.Fatal("a device published from a stale list counted as republished and was never retracted")
	}
}
