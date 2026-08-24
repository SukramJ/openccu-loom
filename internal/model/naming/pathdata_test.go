// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package naming

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Wire ids of a central without a name: the wire form collapses onto the bare
// interface there, which keeps the path/topic expectations below readable.
// The named-central form is exercised where it actually changes an outcome.
var (
	wireHmIPRF   = hmtypes.NewWireInterfaceID("", hmenum.InterfaceHmIPRF)
	wireBidCosRF = hmtypes.NewWireInterfaceID("", hmenum.InterfaceBidCosRF)
)

func TestNewDataPointPathData_StandardInterface(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData("", wireHmIPRF, "VCU1234567", 3, BucketValues, "STATE")
	if pd.SetPath != "device/set/VCU1234567/3/values/STATE" {
		t.Errorf("SetPath = %q, want %q", pd.SetPath, "device/set/VCU1234567/3/values/STATE")
	}
	if pd.StatePath != "device/status/VCU1234567/3/values/STATE" {
		t.Errorf("StatePath = %q, want %q", pd.StatePath, "device/status/VCU1234567/3/values/STATE")
	}
	if pd.Bucket != BucketValues {
		t.Errorf("Bucket = %q, want %q", pd.Bucket, BucketValues)
	}
	if pd.Address != "VCU1234567" || pd.ChannelNo != 3 || pd.Kind != "STATE" {
		t.Errorf("structured fields not populated correctly: %+v", pd)
	}
}

// TestNewDataPointPathData_VirtualDevicesInterface pins the `virtdev/` path
// roots for a virtual-remote data point on BOTH shapes the interface reaches
// this constructor in: the bare id an unnamed central produces and the
// `<central>-VirtualDevices` wire id every named central produces. Only the
// first was ever covered, and only the first ever matched — a named central
// took the `device/` roots for every one of its virtual-remote data points.
func TestNewDataPointPathData_VirtualDevicesInterface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		central string
	}{
		{name: "unnamed central", central: ""},
		{name: "named central", central: "ccu-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			iface := hmtypes.NewWireInterfaceID(tc.central, hmenum.InterfaceVirtualDevices)
			pd := NewDataPointPathData(tc.central, iface, "INT0000001", 0, BucketValues, "PRESS_SHORT")
			if pd.SetPath != "virtdev/set/INT0000001/0/values/PRESS_SHORT" {
				t.Errorf("SetPath = %q, want %q", pd.SetPath, "virtdev/set/INT0000001/0/values/PRESS_SHORT")
			}
			if pd.StatePath != "virtdev/status/INT0000001/0/values/PRESS_SHORT" {
				t.Errorf("StatePath = %q, want %q", pd.StatePath, "virtdev/status/INT0000001/0/values/PRESS_SHORT")
			}
		})
	}
}

func TestNewDataPointPathData_MasterBucket(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData("", wireHmIPRF, "VCU1234567", 0, BucketMaster, "ARR_TIMEOUT")
	if pd.StatePath != "device/status/VCU1234567/0/master/ARR_TIMEOUT" {
		t.Errorf("StatePath = %q, want %q", pd.StatePath, "device/status/VCU1234567/0/master/ARR_TIMEOUT")
	}
	if pd.Bucket != BucketMaster {
		t.Errorf("Bucket = %q, want %q", pd.Bucket, BucketMaster)
	}
}

func TestNewDataPointPathData_LowerCaseAddressUpper(t *testing.T) {
	t.Parallel()
	// Address and kind must be upper-cased on the way through.
	pd := NewDataPointPathData("", wireBidCosRF, "abcd1234", 1, BucketValues, "level")
	if pd.SetPath != "device/set/ABCD1234/1/values/LEVEL" {
		t.Errorf("SetPath = %q, want %q", pd.SetPath, "device/set/ABCD1234/1/values/LEVEL")
	}
}

func TestNewDataPointPathData_EmptyInputsReturnZero(t *testing.T) {
	t.Parallel()
	if got := NewDataPointPathData("", wireHmIPRF, "", 0, BucketValues, "STATE"); !got.IsZero() {
		t.Errorf("empty address must yield zero PathData, got %+v", got)
	}
	if got := NewDataPointPathData("", wireHmIPRF, "VCU1", 0, BucketValues, ""); !got.IsZero() {
		t.Errorf("empty kind must yield zero PathData, got %+v", got)
	}
}

func TestNewDataPointPathData_EmptyBucketDefaultsToValues(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData("", wireHmIPRF, "VCU1", 0, "", "STATE")
	if pd.Bucket != BucketValues {
		t.Errorf("Bucket = %q, want %q (empty bucket → VALUES default)", pd.Bucket, BucketValues)
	}
	if pd.StatePath != "device/status/VCU1/0/values/STATE" {
		t.Errorf("StatePath = %q, want %q", pd.StatePath, "device/status/VCU1/0/values/STATE")
	}
}

func TestPathData_MQTTState(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData("", wireHmIPRF, "VCU1234567", 1, BucketValues, "STATE")
	got := pd.MQTTState("openccu-loom", "ccu-1")
	want := "openccu-loom/ccu-1/HmIP-RF/VCU1234567/1/values/STATE"
	if got != want {
		t.Errorf("MQTTState = %q, want %q", got, want)
	}
	if cmd := pd.MQTTCommand("openccu-loom", "ccu-1"); cmd != want+"/set" {
		t.Errorf("MQTTCommand = %q, want %q", cmd, want+"/set")
	}
	if cfg := pd.MQTTConfig("openccu-loom", "ccu-1"); cfg != want+"/config" {
		t.Errorf("MQTTConfig = %q, want %q", cfg, want+"/config")
	}
}

func TestPathData_MQTTStateNonChannelEmpty(t *testing.T) {
	t.Parallel()
	// A PathData with no channel address renders no MQTT state topic.
	if got := (PathData{SetPath: "x/set", StatePath: "x/status", Kind: "X"}).MQTTState("gh", "ccu"); got != "" {
		t.Errorf("non-channel PathData.MQTTState must be empty, got %q", got)
	}
}

func TestNewSysvarPathData(t *testing.T) {
	t.Parallel()
	pd := NewSysvarPathData("MyVar")
	if pd.SetPath != "sysvar/set/MyVar" {
		t.Errorf("SetPath = %q, want %q", pd.SetPath, "sysvar/set/MyVar")
	}
	if pd.StatePath != "sysvar/status/MyVar" {
		t.Errorf("StatePath = %q, want %q", pd.StatePath, "sysvar/status/MyVar")
	}
	if got := NewSysvarPathData(""); !got.IsZero() {
		t.Errorf("empty vid must yield zero PathData, got %+v", got)
	}
}

func TestPathData_DeviceSetStateRoots(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData("", wireHmIPRF, "VCU1234567", 1, BucketValues, "STATE")
	if !strings.HasPrefix(pd.SetPath, SetPathRoot) {
		t.Errorf("SetPath = %q, want prefix %q", pd.SetPath, SetPathRoot)
	}
	if !strings.HasPrefix(pd.StatePath, StatePathRoot) {
		t.Errorf("StatePath = %q, want prefix %q", pd.StatePath, StatePathRoot)
	}
}

func TestPathData_EmptyAddressIsZero(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData("", wireHmIPRF, "", 1, BucketValues, "STATE")
	if !pd.IsZero() {
		t.Errorf("empty address must yield EmptyPathData, got %+v", pd)
	}
}

func TestPathData_EmptyKindIsZero(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData("", wireHmIPRF, "VCU1234567", 1, BucketValues, "")
	if !pd.IsZero() {
		t.Errorf("empty kind must yield EmptyPathData, got %+v", pd)
	}
}
