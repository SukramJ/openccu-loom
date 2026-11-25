// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	bdbidef "thingfabric.dev/go-fabric/cluster/spec/bridgeddevicebasicinformation"
	psdef "thingfabric.dev/go-fabric/cluster/spec/powersource"
	"thingfabric.dev/go-fabric/fabrictest"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// pairedSite is a started, commissioned node with a reconciler over its
// Aggregator and the controller pair that reads what the node serves.
type pairedSite struct {
	site *fabrictest.Site
	pair *fabrictest.Pair
	rec  *matteradapter.Reconciler
	ctx  context.Context
}

func newPairedSite(t *testing.T) *pairedSite {
	t.Helper()
	site := fabrictest.NewSite(t, fabrictest.Options{})
	pair := site.CommissionedPair(fabrictest.PairOptions{})
	asm := matteradapter.New(matteradapter.Config{}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	rec := matteradapter.NewReconciler(pair.Device.Node().Aggregator(), asm, nil, slog.New(slog.DiscardHandler))
	return &pairedSite{site: site, pair: pair, rec: rec, ctx: ctx}
}

func (s *pairedSite) sync(t *testing.T, dev *device.Device) {
	t.Helper()
	if err := s.rec.Sync(s.ctx, []matteradapter.DeviceSnapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

// endpointOf returns the served endpoint number of the device at addr.
func (s *pairedSite) endpointOf(t *testing.T, addr string) uint16 {
	t.Helper()
	devices := s.pair.Device.Node().Topology().Devices
	for i := range devices {
		if d := &devices[i]; d.Key != nil && strings.Contains(d.Key.String(), addr) {
			return d.Endpoint
		}
	}
	t.Fatalf("%s is not served", addr)
	return 0
}

func (s *pairedSite) bridgedInfo(t *testing.T, addr string) bdbidef.State {
	t.Helper()
	v, err := fabrictest.ReadCluster(s.ctx, s.pair, s.endpointOf(t, addr), bdbidef.ClusterID)
	if err != nil {
		t.Fatalf("read BridgedDeviceBasicInformation: %v", err)
	}
	st, _ := bdbidef.StateFrom(v)
	return st
}

// TestReconcilerServesANewFirmwareAndRaisesTheConfigurationVersion pins that
// a firmware update reaches the controller: the served
// SoftwareVersionString is the new one and the device's ConfigurationVersion
// rose by exactly one, so a controller re-reads the device. A sync without a
// change raises nothing.
func TestReconcilerServesANewFirmwareAndRaisesTheConfigurationVersion(t *testing.T) {
	t.Parallel()
	s := newPairedSite(t)
	const addr = "SW0000101"
	dev := switchDevice(addr, "Lamp")
	dev.Model = "HmIP-PS"
	dev.Firmware().Set(device.FirmwareInfo{Current: "1.0.0"})

	s.sync(t, dev)
	first := s.bridgedInfo(t, addr)
	if first.SoftwareVersionString != "1.0.0" {
		t.Fatalf("first sync: SoftwareVersionString=%q, want 1.0.0", first.SoftwareVersionString)
	}

	s.sync(t, dev)
	if got := s.bridgedInfo(t, addr).ConfigurationVersion; got != first.ConfigurationVersion {
		t.Fatalf("an unchanged sync moved ConfigurationVersion %d -> %d", first.ConfigurationVersion, got)
	}

	dev.Firmware().Set(device.FirmwareInfo{Current: "1.1.0"})
	s.sync(t, dev)
	after := s.bridgedInfo(t, addr)
	if after.SoftwareVersionString != "1.1.0" {
		t.Errorf("after the update: SoftwareVersionString=%q, want 1.1.0", after.SoftwareVersionString)
	}
	if after.ConfigurationVersion != first.ConfigurationVersion+1 {
		t.Errorf("ConfigurationVersion %d -> %d, want a rise of exactly 1", first.ConfigurationVersion, after.ConfigurationVersion)
	}

	s.sync(t, dev)
	if got := s.bridgedInfo(t, addr).ConfigurationVersion; got != after.ConfigurationVersion {
		t.Errorf("a sync after the update moved ConfigurationVersion %d -> %d", after.ConfigurationVersion, got)
	}
}

// TestBatteryPowerSourceServesUnspecifiedReplaceability pins what a
// controller reads of the LOWBAT battery: the CCU does not say whether the
// cell is replaceable, so BatReplaceability is Unspecified rather than the
// node's user-replaceable default for a source that states nothing.
func TestBatteryPowerSourceServesUnspecifiedReplaceability(t *testing.T) {
	t.Parallel()
	s := newPairedSite(t)
	const addr = "SW0000102"
	dev := switchDevice(addr, "Lamp")
	ch0 := dev.AddChannel(addr+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch0.Put(generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch0.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLowBat),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))
	s.sync(t, dev)

	v, err := fabrictest.ReadCluster(s.ctx, s.pair, s.endpointOf(t, addr), psdef.ClusterID)
	if err != nil {
		t.Fatalf("read PowerSource: %v", err)
	}
	st, absent := psdef.StateFrom(v)
	for _, a := range absent {
		if a == psdef.AttrBatReplaceability {
			t.Fatal("BatReplaceability is not served on the battery's endpoint")
		}
	}
	if st.BatReplaceability != psdef.BatReplaceabilityUnspecified {
		t.Errorf("BatReplaceability=%v, want Unspecified", st.BatReplaceability)
	}
}
