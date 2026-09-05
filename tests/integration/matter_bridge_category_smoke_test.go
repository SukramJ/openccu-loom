// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

// matter_bridge_category_smoke_test.go — non-OnOff category bridge smoke tests.
//
// The OnOff / TemperatureSensor smoke cases in matter_bridge_smoke_test.go drive
// the assembler with single-cluster STUB sources. The non-OnOff device
// categories (Thermostat, DoorLock, WindowCovering, ColorControl, SmokeCoAlarm)
// were otherwise only exercised by the frozen unit-parity tests in
// internal/model/custom/<cat>/matter_test.go, which never run the projection
// through the real endpoint assembler. This file closes that gap: it attaches
// the REAL internal/model/custom projections as channel Custom-DPs and asserts
// the full multi-cluster surface materialises on the bridged endpoint — proving
// the assembler's mandatory-cluster attachment (BDBI + Descriptor) composes
// correctly on top of a real multi-cluster device, not just a single-cluster
// stub.
//
// Reuses buildSmokeTopology / clusterIDsOf / hasClusterID from
// matter_bridge_smoke_test.go (same package + build tag).
package integration

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"

	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── matter device-type + cluster constants (non-OnOff categories) ────────────

const (
	// matter.js packages/node/src/devices/thermostat.ts ThermostatDevice.deviceType
	catDevTypeThermostat uint16 = 0x0301
	// matter.js packages/node/src/devices/door-lock.ts DoorLockDevice.deviceType
	catDevTypeDoorLock uint16 = 0x000A
	// matter.js packages/node/src/devices/window-covering.ts WindowCoveringDevice.deviceType
	catDevTypeWindowCovering uint16 = 0x0202
	// matter.js packages/node/src/devices/extended-color-light.ts ExtendedColorLightDevice.deviceType
	catDevTypeExtendedColorLight uint16 = 0x010D
	// matter.js packages/node/src/devices/smoke-co-alarm.ts SmokeCoAlarmDevice.deviceType
	catDevTypeSmokeCoAlarm uint16 = 0x0076

	// Assembler always attaches these two mandatory clusters to every bridged
	// endpoint regardless of the projection's own cluster set.
	catClusterDescriptor uint32 = 0x001D
	catClusterBDBI       uint32 = 0x0039
)

// ─── real-projection device builders ──────────────────────────────────────────

// makeSmokeClimateDevice attaches a real climate.Climate (Thermostat 0x0301)
// projection. matter.js packages/node/src/devices/thermostat.ts requires the
// Thermostat (0x0201) server; loom additionally projects
// ThermostatUserInterfaceConfiguration (0x0204) and TemperatureMeasurement
// (0x0402). Mirrors internal/model/custom/climate/matter.go MatterClusterServers.
func makeSmokeClimateDevice(addr, name string) *device.Device {
	dev := device.New(device.Config{Address: addr, Name: name})
	ch := dev.AddChannel(addr+":1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(climate.New(climate.Config{Channel: ch, Kind: climate.KindIP}))
	return dev
}

// makeSmokeLockDevice attaches a real lock.Lock (DoorLock 0x000A) projection.
// Mirrors internal/model/custom/lock/matter.go MatterClusterServers, which wires
// the DoorLock (0x0101) server per matter.js packages/node/src/devices/door-lock.ts.
func makeSmokeLockDevice(addr, name string) *device.Device {
	dev := device.New(device.Config{Address: addr, Name: name})
	ch := dev.AddChannel(addr+":1", 1, "DOOR_LOCK", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(lock.New(lock.Config{Channel: ch, Kind: lock.KindIP}))
	return dev
}

// makeSmokeCoverDevice attaches a real cover.Cover (WindowCovering 0x0202)
// projection. Mirrors internal/model/custom/cover/matter.go MatterClusterServers,
// which wires the WindowCovering (0x0102) server per matter.js
// packages/node/src/devices/window-covering.ts.
func makeSmokeCoverDevice(addr, name string) *device.Device {
	dev := device.New(device.Config{Address: addr, Name: name})
	ch := dev.AddChannel(addr+":1", 1, "BLIND", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(cover.New(cover.Config{Channel: ch}))
	return dev
}

// makeSmokeColorLightDevice attaches a real light.ColorLight (ExtendedColorLight
// 0x010D) projection. Mirrors internal/model/custom/light/matter_color.go, which
// appends ColorControl (0x0300) — matter.js
// packages/node/src/behaviors/color-control/ColorControlServer.ts — on top of the
// base OnOff (0x0006) + Groups (0x0004) + ScenesManagement (0x0062) set from
// packages/node/src/devices/on-off-light.ts.
func makeSmokeColorLightDevice(addr, name string) *device.Device {
	dev := device.New(device.Config{Address: addr, Name: name})
	ch := dev.AddChannel(addr+":1", 1, "RGBW", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(light.NewColorLight(light.Config{Channel: ch}))
	return dev
}

// makeSmokeSmokeSirenDevice attaches a real siren.SmokeSiren (SmokeCoAlarm 0x0076)
// projection. Mirrors internal/model/custom/siren/smoke.go MatterClusterServers,
// which wires the SmokeCoAlarm (0x005C) server per matter.js
// packages/node/src/devices/smoke-co-alarm.ts.
func makeSmokeSmokeSirenDevice(addr, name string) *device.Device {
	dev := device.New(device.Config{Address: addr, Name: name})
	ch := dev.AddChannel(addr+":1", 1, "SMOKE_DETECTOR", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(siren.NewSmokeSiren(siren.SmokeSirenConfig{Channel: ch}))
	return dev
}

// ─── shared category assertion ────────────────────────────────────────────────

// assertCategoryBridgedEndpoint builds a one-device topology, resolves the single
// bridged endpoint, and asserts its DeviceType and full cluster surface (the
// projection's clusters plus the assembler-mandatory Descriptor + BDBI).
func assertCategoryBridgedEndpoint(t *testing.T, dev *device.Device, wantDeviceType uint16, wantClusters []uint32) {
	t.Helper()

	top := buildSmokeTopology(t, []matteradapter.DeviceSnapshot{
		{CentralName: "ccu1", Devices: []*device.Device{dev}},
	})

	bridged := top.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("Bridged()=%d, want 1", len(bridged))
	}
	ep := bridged[0]

	if ep.DeviceType != wantDeviceType {
		t.Errorf("EP %d: DeviceType=0x%04X, want 0x%04X", ep.ID, ep.DeviceType, wantDeviceType)
	}

	ids := clusterIDsOf(ep)
	// Every category endpoint must carry the projection clusters plus the two
	// assembler-mandatory clusters.
	want := append([]uint32{catClusterDescriptor, catClusterBDBI}, wantClusters...)
	for _, id := range want {
		if !hasClusterID(ids, id) {
			t.Errorf("EP %d: cluster 0x%04X missing from cluster surface %v", ep.ID, id, ids)
		}
	}
}

// ─── Test: climate -> Thermostat (0x0301) ─────────────────────────────────────

// TestMatterBridgeSmoke_ClimateClusterSurface materialises a real Climate and
// asserts the Thermostat cluster surface survives the endpoint assembler.
func TestMatterBridgeSmoke_ClimateClusterSurface(t *testing.T) {
	t.Parallel()
	assertCategoryBridgedEndpoint(
		t,
		makeSmokeClimateDevice("CATCLIM01", "Bridged Thermostat"),
		catDevTypeThermostat,
		// Thermostat + ThermostatUserInterfaceConfiguration only. The Device
		// Library names TemperatureMeasurement (0x0402) for 0x0301 as
		// element=clientCluster, so a thermostat endpoint must not serve it;
		// the channel's ACTUAL_TEMPERATURE reaches controllers as its own
		// TemperatureSensor endpoint instead.
		[]uint32{0x0201, 0x0204},
	)
}

// ─── Test: lock -> DoorLock (0x000A) ──────────────────────────────────────────

// TestMatterBridgeSmoke_LockClusterSurface materialises a real Lock and asserts
// the DoorLock cluster (0x0101) surfaces on the bridged endpoint.
func TestMatterBridgeSmoke_LockClusterSurface(t *testing.T) {
	t.Parallel()
	assertCategoryBridgedEndpoint(
		t,
		makeSmokeLockDevice("CATLOCK01", "Bridged Lock"),
		catDevTypeDoorLock,
		[]uint32{0x0101}, // DoorLock
	)
}

// ─── Test: cover -> WindowCovering (0x0202) ───────────────────────────────────

// TestMatterBridgeSmoke_CoverClusterSurface materialises a real Cover and asserts
// the WindowCovering cluster (0x0102) surfaces on the bridged endpoint.
func TestMatterBridgeSmoke_CoverClusterSurface(t *testing.T) {
	t.Parallel()
	assertCategoryBridgedEndpoint(
		t,
		makeSmokeCoverDevice("CATCOVR01", "Bridged Cover"),
		catDevTypeWindowCovering,
		[]uint32{0x0102}, // WindowCovering
	)
}

// ─── Test: color light -> ColorControl (0x0300) ───────────────────────────────

// TestMatterBridgeSmoke_ColorLightClusterSurface materialises a real ColorLight
// and asserts ColorControl (0x0300) surfaces alongside the base OnOffLight
// cluster set on the bridged endpoint.
func TestMatterBridgeSmoke_ColorLightClusterSurface(t *testing.T) {
	t.Parallel()
	assertCategoryBridgedEndpoint(
		t,
		makeSmokeColorLightDevice("CATCOLR01", "Bridged Color Light"),
		catDevTypeExtendedColorLight,
		[]uint32{0x0006, 0x0004, 0x0062, 0x0300}, // OnOff, Groups, ScenesManagement, ColorControl
	)
}

// ─── Test: siren -> SmokeCoAlarm (0x0076) ─────────────────────────────────────

// TestMatterBridgeSmoke_SmokeSirenClusterSurface materialises a real SmokeSiren
// and asserts the SmokeCoAlarm cluster (0x005C) surfaces on the bridged endpoint.
func TestMatterBridgeSmoke_SmokeSirenClusterSurface(t *testing.T) {
	t.Parallel()
	assertCategoryBridgedEndpoint(
		t,
		makeSmokeSmokeSirenDevice("CATSIRN01", "Bridged Smoke Alarm"),
		catDevTypeSmokeCoAlarm,
		[]uint32{0x005C}, // SmokeCoAlarm
	)
}
