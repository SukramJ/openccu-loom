// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/fabrictest"
	"github.com/SukramJ/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// switchDevice returns a device with one switch channel carrying the
// switch profile, named name.
func switchDevice(addr, name string) *device.Device {
	dev := device.New(device.Config{Address: addr, Name: name})
	chAddr := addr + ":4"
	ch := dev.AddChannel(chAddr, 4, "SWITCH", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	ch.SetCustomDataPoint(switchdev.New(ch, custom.RebasedChannelGroupConfig{}))
	return dev
}

// reconcilerSite is a started node with a reconciler over its Aggregator.
type reconcilerSite struct {
	node *node.Node
	rec  *matteradapter.Reconciler
	asm  *matteradapter.Assembler
	ctx  context.Context
}

func newReconcilerSite(t *testing.T) *reconcilerSite {
	t.Helper()
	site := fabrictest.NewSite(t, fabrictest.Options{})
	pair := site.CommissionedPair(fabrictest.PairOptions{})
	n := pair.Device.Node()
	asm := matteradapter.New(matteradapter.Config{}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return &reconcilerSite{node: n, rec: matteradapter.NewReconciler(n.Aggregator(), asm, nil, slog.New(slog.DiscardHandler)), ctx: ctx, asm: asm}
}

func (s *reconcilerSite) sync(t *testing.T, snaps ...matteradapter.DeviceSnapshot) {
	t.Helper()
	if err := s.rec.Sync(s.ctx, snaps); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

// served returns the served endpoint number and name of the bridged
// device at addr, or false.
func (s *reconcilerSite) served(addr string) (number uint16, name string, ok bool) {
	for _, d := range s.node.Topology().Devices {
		if d.Key != nil && strings.Contains(d.Key.String(), addr) {
			return d.Endpoint, d.Name, true
		}
	}
	return 0, "", false
}

// TestReconcilerAddsRenamesAndRemovesUnderTheCentralsPartition pins the
// three changes a sync applies: a device the walk describes is served, a
// device renamed in the model is served under the new name with its
// endpoint number kept (a replacement under the same key), and a device
// the walk no longer describes leaves the topology.
func TestReconcilerAddsRenamesAndRemovesUnderTheCentralsPartition(t *testing.T) {
	t.Parallel()
	s := newReconcilerSite(t)
	a, b := switchDevice("SW0000001", "Lamp"), switchDevice("SW0000002", "Fan")
	s.sync(t, matteradapter.DeviceSnapshot{CentralName: "ccu1", Devices: []*device.Device{a, b}})
	number, name, ok := s.served("SW0000001")
	if !ok || !strings.HasPrefix(name, "Lamp") {
		t.Fatalf("after the first sync: SW0000001 served=%v name=%q, want served as Lamp…", ok, name)
	}
	if _, _, ok := s.served("SW0000002"); !ok {
		t.Fatal("after the first sync: SW0000002 is not served")
	}

	renamed := switchDevice("SW0000001", "Reading lamp")
	s.sync(t, matteradapter.DeviceSnapshot{CentralName: "ccu1", Devices: []*device.Device{renamed}})
	number2, name, ok := s.served("SW0000001")
	if !ok || !strings.HasPrefix(name, "Reading lamp") {
		t.Fatalf("after the rename: SW0000001 served=%v name=%q, want served as Reading lamp…", ok, name)
	}
	if number2 != number {
		t.Fatalf("the rename changed the endpoint number %d → %d; a replacement keeps the key's number", number, number2)
	}
	if _, _, ok := s.served("SW0000002"); ok {
		t.Fatal("SW0000002 is still served although the walk no longer describes it")
	}
}

// TestReconcilerKeepsAVanishedDevicesNumber pins the partition per
// central without a forget: a device the walk stops describing — unexposed,
// hidden, or a central still waiting on its CCU — comes back under the
// number it had, in every central.
func TestReconcilerKeepsAVanishedDevicesNumber(t *testing.T) {
	t.Parallel()
	s := newReconcilerSite(t)
	s.sync(t,
		matteradapter.DeviceSnapshot{CentralName: "ccu1", Devices: []*device.Device{switchDevice("SW0000011", "One")}},
		matteradapter.DeviceSnapshot{CentralName: "ccu2", Devices: []*device.Device{switchDevice("SW0000021", "Two")}})
	one, _, _ := s.served("SW0000011")
	two, _, _ := s.served("SW0000021")

	s.sync(t, matteradapter.DeviceSnapshot{CentralName: "ccu1"}, matteradapter.DeviceSnapshot{CentralName: "ccu2"})
	if _, _, ok := s.served("SW0000011"); ok {
		t.Fatal("SW0000011 is still served although the walk no longer describes it")
	}
	s.sync(t,
		matteradapter.DeviceSnapshot{CentralName: "ccu1", Devices: []*device.Device{switchDevice("SW0000011", "One")}},
		matteradapter.DeviceSnapshot{CentralName: "ccu2", Devices: []*device.Device{switchDevice("SW0000021", "Two")}})
	if n, _, _ := s.served("SW0000011"); n != one {
		t.Fatalf("ccu1's device came back as endpoint %d, want its kept %d", n, one)
	}
	if n, _, _ := s.served("SW0000021"); n != two {
		t.Fatalf("ccu2's device came back as endpoint %d, want its kept %d", n, two)
	}
}

// TestReconcilerForgetsADeletedDevice pins QueueForget: the next sync frees
// the deleted device's number, so the same address paired again is
// numbered afresh, while a sibling device of the central and a device of
// the same address under another central keep theirs.
func TestReconcilerForgetsADeletedDevice(t *testing.T) {
	t.Parallel()
	s := newReconcilerSite(t)
	full := func() []matteradapter.DeviceSnapshot {
		return []matteradapter.DeviceSnapshot{
			{CentralName: "ccu1", Devices: []*device.Device{switchDevice("SW0000051", "Deleted"), switchDevice("SW0000052", "Sibling")}},
			{CentralName: "ccu2", Devices: []*device.Device{switchDevice("SW0000051", "Same address")}},
		}
	}
	s.sync(t, full()...)
	deleted, _, _ := s.served("ccu1|SW0000051")
	sibling, _, _ := s.served("SW0000052")
	other, _, _ := s.served("ccu2|SW0000051")

	// The CCU deletes the device: the model drops it and the event queues
	// the forget ahead of the sync it triggers.
	s.rec.QueueForget("ccu1", "SW0000051")
	s.sync(t,
		matteradapter.DeviceSnapshot{CentralName: "ccu1", Devices: []*device.Device{switchDevice("SW0000052", "Sibling")}},
		matteradapter.DeviceSnapshot{CentralName: "ccu2", Devices: []*device.Device{switchDevice("SW0000051", "Same address")}})
	if _, _, ok := s.served("ccu1|SW0000051"); ok {
		t.Fatal("the deleted device is still served")
	}

	s.sync(t, full()...)
	if n, _, ok := s.served("ccu1|SW0000051"); !ok || n == deleted {
		t.Fatalf("the device paired again is endpoint %d (served=%v), want a fresh number, not %d", n, ok, deleted)
	}
	if n, _, _ := s.served("SW0000052"); n != sibling {
		t.Fatalf("the sibling moved from endpoint %d to %d", sibling, n)
	}
	if n, _, _ := s.served("ccu2|SW0000051"); n != other {
		t.Fatalf("the same address under ccu2 moved from endpoint %d to %d", other, n)
	}
}

// keysFromTopology is a DeviceKeyLister over keys recorded from a served
// topology: what the endpoint store answers for a device it holds rows of.
type keysFromTopology map[string][]matterendpoint.SourceKey

func (k keysFromTopology) DeviceKeys(_ context.Context, centralName, deviceAddress string) ([]matterendpoint.SourceKey, error) {
	return k[centralName+"|"+deviceAddress], nil
}

// TestReconcilerForgetsAStoredKeyItDoesNotHold pins the store lookup behind
// QueueForget: a reconciler that never held the deleted device — the daemon
// restarted after the device left the model — still frees its stored number
// through the DeviceKeyLister.
func TestReconcilerForgetsAStoredKeyItDoesNotHold(t *testing.T) {
	t.Parallel()
	s := newReconcilerSite(t)
	s.sync(t, matteradapter.DeviceSnapshot{CentralName: "ccu1", Devices: []*device.Device{switchDevice("SW0000061", "Deleted")}})
	deleted, _, _ := s.served("SW0000061")
	keys := keysFromTopology{}
	for _, d := range s.node.Topology().Devices {
		if k, ok := d.Key.(matterendpoint.SourceKey); ok && k.DeviceAddress == "SW0000061" {
			keys["ccu1|SW0000061"] = append(keys["ccu1|SW0000061"], k)
		}
	}
	if len(keys) == 0 {
		t.Fatal("the served topology carries no key of SW0000061")
	}
	// The device leaves the model; its number stays reserved.
	s.sync(t, matteradapter.DeviceSnapshot{CentralName: "ccu1"})

	restarted := matteradapter.NewReconciler(s.node.Aggregator(), s.asm, keys, slog.New(slog.DiscardHandler))
	restarted.QueueForget("ccu1", "SW0000061")
	if err := restarted.Sync(s.ctx, []matteradapter.DeviceSnapshot{{CentralName: "ccu1"}}); err != nil {
		t.Fatalf("Sync with the forget: %v", err)
	}
	if err := restarted.Sync(s.ctx, []matteradapter.DeviceSnapshot{{CentralName: "ccu1", Devices: []*device.Device{switchDevice("SW0000061", "Deleted")}}}); err != nil {
		t.Fatalf("Sync with the device paired again: %v", err)
	}
	if n, _, ok := s.served("SW0000061"); !ok || n == deleted {
		t.Fatalf("the device paired again is endpoint %d (served=%v), want a fresh number, not %d", n, ok, deleted)
	}
}

// TestReconcilerRetiresACentral pins Retire: the central's devices leave
// the topology, and a device of the same key added under the scope
// afterwards is numbered afresh — the retired scope's numbers were
// collected although the central never completed.
func TestReconcilerRetiresACentral(t *testing.T) {
	t.Parallel()
	s := newReconcilerSite(t)
	s.sync(t,
		matteradapter.DeviceSnapshot{CentralName: "stays", Devices: []*device.Device{switchDevice("SW0000031", "Stays")}},
		matteradapter.DeviceSnapshot{CentralName: "gone", Devices: []*device.Device{switchDevice("SW0000041", "Gone")}})
	goneNumber, _, ok := s.served("SW0000041")
	if !ok {
		t.Fatal("the central to retire is not served")
	}
	if err := s.rec.Retire(s.ctx, "gone"); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if _, _, ok := s.served("SW0000041"); ok {
		t.Fatal("the retired central's device is still served")
	}
	if _, _, ok := s.served("SW0000031"); !ok {
		t.Fatal("retiring one central dropped another central's device")
	}
	s.sync(t,
		matteradapter.DeviceSnapshot{CentralName: "stays", Devices: []*device.Device{switchDevice("SW0000031", "Stays")}},
		matteradapter.DeviceSnapshot{CentralName: "gone", Devices: []*device.Device{switchDevice("SW0000041", "Gone")}})
	if n, _, ok := s.served("SW0000041"); !ok || n == goneNumber {
		t.Fatalf("after the retirement the same key is endpoint %d (served=%v), want a fresh number", n, ok)
	}
}
