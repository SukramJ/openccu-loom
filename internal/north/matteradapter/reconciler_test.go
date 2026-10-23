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
	return &reconcilerSite{node: n, rec: matteradapter.NewReconciler(n.Aggregator(), asm, slog.New(slog.DiscardHandler)), ctx: ctx}
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

// TestReconcilerCollectsOnlyACompleteCentralsVanishedNumbers pins the
// partition per central: a device removed from a central whose model is
// complete gives its endpoint number up (a later add of the same key is
// numbered afresh), while a device removed from a central still loading
// keeps it (the same key comes back under the same number) — whatever the
// other central's completeness.
func TestReconcilerCollectsOnlyACompleteCentralsVanishedNumbers(t *testing.T) {
	t.Parallel()
	s := newReconcilerSite(t)
	done, loading := switchDevice("SW0000011", "Done"), switchDevice("SW0000021", "Loading")
	snapDone := matteradapter.DeviceSnapshot{CentralName: "done", Devices: []*device.Device{done}, ModelComplete: true}
	snapLoading := matteradapter.DeviceSnapshot{CentralName: "loading", Devices: []*device.Device{loading}}
	s.sync(t, snapDone, snapLoading)
	doneNumber, _, _ := s.served("SW0000011")
	loadingNumber, _, _ := s.served("SW0000021")

	// Both devices vanish from their walks.
	s.sync(t,
		matteradapter.DeviceSnapshot{CentralName: "done", ModelComplete: true},
		matteradapter.DeviceSnapshot{CentralName: "loading"})
	// Both come back; a fresh key's number tells whether the old one
	// was collected: the next number is above every number in use.
	s.sync(t,
		matteradapter.DeviceSnapshot{CentralName: "done", Devices: []*device.Device{switchDevice("SW0000011", "Done")}, ModelComplete: true},
		matteradapter.DeviceSnapshot{CentralName: "loading", Devices: []*device.Device{switchDevice("SW0000021", "Loading")}})
	if n, _, _ := s.served("SW0000021"); n != loadingNumber {
		t.Fatalf("the loading central's device came back as endpoint %d, want its retained %d", n, loadingNumber)
	}
	if n, _, _ := s.served("SW0000011"); n == doneNumber {
		t.Fatalf("the complete central's device came back as endpoint %d: its vanished number was not collected", n)
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
