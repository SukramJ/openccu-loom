// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fabricdevice "thingfabric.dev/go-fabric/device"
	"thingfabric.dev/go-fabric/surface"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// togglingSwitch is a custom data point whose device the kit refuses while
// refuse is set and builds through a real generic switch otherwise.
type togglingSwitch struct {
	key    hmtypes.DataPointKey
	refuse atomic.Bool
	inner  *generic.Switch
}

func (s *togglingSwitch) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *togglingSwitch) Name() string                       { return "toggling" }
func (s *togglingSwitch) MatterDeviceType() uint16           { return s.inner.MatterDeviceType() }

func (s *togglingSwitch) MatterDevice(req interfaces.MatterDeviceRequest) (*fabricdevice.Device, error) {
	if s.refuse.Load() {
		return nil, errors.New("refused on purpose")
	}
	return s.inner.MatterDevice(req)
}

// togglingDevice returns a device whose channel 1 carries a togglingSwitch
// that starts out refused.
func togglingDevice(addr string) (*device.Device, *togglingSwitch) {
	dev := newDevice(addr, "Toggling")
	chAddr := addr + ":1"
	ch := dev.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	key := hmtypes.DataPointKey{ChannelAddress: chAddr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE"}
	src := &togglingSwitch{key: key, inner: generic.NewSwitch(generic.Spec{
		Key: key,
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})}
	src.refuse.Store(true)
	ch.SetCustomDataPoint(src)
	return dev, src
}

// refusingDevice returns a device whose one channel the kit refuses.
func refusingDevice(addr string) *device.Device {
	dev := newDevice(addr, "Refusing")
	ch := dev.AddChannel(addr+":1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(&refusingSwitch{key: hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "STATE"}})
	return dev
}

// findingsSite is an assembler with a fixed clock and a captured log.
type findingsSite struct {
	asm *matteradapter.Assembler
	log *bytes.Buffer
	at  time.Time
}

func newFindingsSite(cfg matteradapter.Config) *findingsSite {
	s := &findingsSite{log: &bytes.Buffer{}, at: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	cfg.Now = func() time.Time { return s.at }
	s.asm = matteradapter.New(cfg, slog.New(slog.NewTextHandler(s.log, nil)))
	return s
}

func (s *findingsSite) describe(t *testing.T, snaps ...matteradapter.DeviceSnapshot) {
	t.Helper()
	if _, err := s.asm.DescribeDevices(context.Background(), snaps); err != nil {
		t.Fatalf("DescribeDevices: %v", err)
	}
}

// TestARefusedDeviceIsOneFindingWithItsFields pins the finding a refusal
// leaves: one per key, carrying the source's coordinates, its first-seen
// time unchanged over later walks, and logged once.
func TestARefusedDeviceIsOneFindingWithItsFields(t *testing.T) {
	t.Parallel()
	const addr = "VCU0000301"
	s := newFindingsSite(validConfig())
	snap := matteradapter.DeviceSnapshot{CentralName: "c", Devices: []*device.Device{refusingDevice(addr)}}
	first := s.at
	s.describe(t, snap)
	s.at = first.Add(time.Hour)
	s.describe(t, snap)

	got := s.asm.Findings()
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want one", got)
	}
	f := got[0]
	if f.Code != matteradapter.FindingDeviceRefused || f.DeviceType != 0x010A ||
		f.DeviceAddress != addr || f.ChannelAddress != addr+":1" ||
		!strings.Contains(f.Key, addr) || f.Err != "refused on purpose" || f.Name == "" {
		t.Fatalf("finding = %+v, want device_refused for %s:1 of type 0x010A", f, addr)
	}
	if !f.Since.Equal(first) {
		t.Fatalf("Since = %v, want the first-seen %v unchanged by a later walk", f.Since, first)
	}
	if n := strings.Count(s.log.String(), "matter.assembler.device_refused"); n != 1 {
		t.Fatalf("the refusal was logged %d times over two walks, want once:\n%s", n, s.log.String())
	}
}

// TestASourceThatBuildsClearsItsFinding pins that a source the kit refused
// once and builds later is no longer a finding. The build is re-run through
// the decision walk, which never prunes, so only the clear on a successful
// build can remove it.
func TestASourceThatBuildsClearsItsFinding(t *testing.T) {
	t.Parallel()
	dev, src := togglingDevice("VCU0000302")
	s := newFindingsSite(validConfig())
	s.describe(t, matteradapter.DeviceSnapshot{CentralName: "c", Devices: []*device.Device{dev}})
	if got := s.asm.Findings(); len(got) != 1 || got[0].Code != matteradapter.FindingDeviceRefused {
		t.Fatalf("findings after the refusal = %+v, want one device_refused", got)
	}
	src.refuse.Store(false)
	if _, err := s.asm.DecideDevice(context.Background(), "c", []*device.Device{dev}, dev); err != nil {
		t.Fatalf("DecideDevice: %v", err)
	}
	if got := s.asm.Findings(); len(got) != 0 {
		t.Fatalf("findings after a successful build = %+v, want none", got)
	}
}

// TestASpecLevelWithholdIsADeviceWithheldFinding pins that a source the
// specification level withholds is recorded under its own code, at info.
func TestASpecLevelWithholdIsADeviceWithheldFinding(t *testing.T) {
	t.Parallel()
	dev := newDevice("GAR0002", "Garage")
	ch := addChannel(dev, "GAR0002:1", 1)
	ch.SetCustomDataPoint(&stubEndpointSource{
		key:        dpKey("GAR0002:1", "DOOR_COMMAND"),
		deviceType: uint16(fabricdevice.ClosureID),
	})
	cfg := validConfig()
	cfg.Build.Surface = surface.Policies{Level: "1.4.2"}
	s := newFindingsSite(cfg)
	s.describe(t, matteradapter.DeviceSnapshot{CentralName: "c", Devices: []*device.Device{dev}})
	got := s.asm.Findings()
	if len(got) != 1 || got[0].Code != matteradapter.FindingDeviceWithheld || got[0].DeviceType != uint16(fabricdevice.ClosureID) {
		t.Fatalf("findings = %+v, want one device_withheld of Closure", got)
	}
	if n := strings.Count(s.log.String(), "matter.assembler.device_withheld"); n != 1 {
		t.Fatalf("the withhold was logged %d times, want once:\n%s", n, s.log.String())
	}
}

// TestAFindingLeavesWithItsSource pins pruning: a completed walk of a
// central drops the findings of sources it no longer met, and leaves the
// findings of every other central alone.
func TestAFindingLeavesWithItsSource(t *testing.T) {
	t.Parallel()
	s := newFindingsSite(validConfig())
	s.describe(t,
		matteradapter.DeviceSnapshot{CentralName: "a", Devices: []*device.Device{refusingDevice("VCU0000303")}},
		matteradapter.DeviceSnapshot{CentralName: "b", Devices: []*device.Device{refusingDevice("VCU0000304")}})
	if got := s.asm.Findings(); len(got) != 2 {
		t.Fatalf("findings = %+v, want two", got)
	}
	s.describe(t, matteradapter.DeviceSnapshot{CentralName: "a"})
	got := s.asm.Findings()
	if len(got) != 1 || got[0].DeviceAddress != "VCU0000304" {
		t.Fatalf("findings after central a lost its device = %+v, want only central b's", got)
	}
}

// TestARetiredCentralLeavesNoFindings pins that retiring a central drops
// its findings: no later walk names the scope, so none would prune them.
func TestARetiredCentralLeavesNoFindings(t *testing.T) {
	t.Parallel()
	s := newReconcilerSite(t)
	s.sync(t,
		matteradapter.DeviceSnapshot{CentralName: "stays", Devices: []*device.Device{refusingDevice("VCU0000305")}},
		matteradapter.DeviceSnapshot{CentralName: "gone", Devices: []*device.Device{refusingDevice("VCU0000306")}})
	if got := s.asm.Findings(); len(got) != 2 {
		t.Fatalf("findings = %+v, want two", got)
	}
	if err := s.rec.Retire(s.ctx, "gone"); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if got := s.asm.Findings(); len(got) != 1 || got[0].DeviceAddress != "VCU0000305" {
		t.Fatalf("findings after retiring gone = %+v, want only the staying central's", got)
	}
}

// TestFindingsAreOrderedByCodeThenKey pins the snapshot order, which the
// map behind it does not have.
func TestFindingsAreOrderedByCodeThenKey(t *testing.T) {
	t.Parallel()
	closure := newDevice("GAR0003", "Garage")
	addChannel(closure, "GAR0003:1", 1).SetCustomDataPoint(&stubEndpointSource{
		key:        dpKey("GAR0003:1", "DOOR_COMMAND"),
		deviceType: uint16(fabricdevice.ClosureID),
	})
	cfg := validConfig()
	cfg.Build.Surface = surface.Policies{Level: "1.4.2"}
	s := newFindingsSite(cfg)
	s.describe(t, matteradapter.DeviceSnapshot{CentralName: "c", Devices: []*device.Device{
		refusingDevice("VCU0000309"), closure, refusingDevice("VCU0000307"), refusingDevice("VCU0000308"),
	}})
	got := s.asm.Findings()
	want := []string{"VCU0000307", "VCU0000308", "VCU0000309", "GAR0003"}
	if len(got) != len(want) {
		t.Fatalf("findings = %+v, want %d", got, len(want))
	}
	for i, f := range got {
		if f.DeviceAddress != want[i] {
			t.Fatalf("finding %d is %s (%s), want %s; order = %+v", i, f.DeviceAddress, f.Code, want[i], got)
		}
	}
}
