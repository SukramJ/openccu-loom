// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package naming

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestNewDataPointPathData_StandardInterface(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData(hmenum.InterfaceHmIPRF, "VCU1234567", 3, BucketValues, "STATE")
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

func TestNewDataPointPathData_VirtualDevicesInterface(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData(hmenum.InterfaceVirtualDevices, "INT0000001", 0, BucketValues, "PRESS_SHORT")
	if pd.SetPath != "virtdev/set/INT0000001/0/values/PRESS_SHORT" {
		t.Errorf("SetPath = %q, want %q", pd.SetPath, "virtdev/set/INT0000001/0/values/PRESS_SHORT")
	}
	if pd.StatePath != "virtdev/status/INT0000001/0/values/PRESS_SHORT" {
		t.Errorf("StatePath = %q, want %q", pd.StatePath, "virtdev/status/INT0000001/0/values/PRESS_SHORT")
	}
}

func TestNewDataPointPathData_MasterBucket(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData(hmenum.InterfaceHmIPRF, "VCU1234567", 0, BucketMaster, "ARR_TIMEOUT")
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
	pd := NewDataPointPathData(hmenum.InterfaceBidCosRF, "abcd1234", 1, BucketValues, "level")
	if pd.SetPath != "device/set/ABCD1234/1/values/LEVEL" {
		t.Errorf("SetPath = %q, want %q", pd.SetPath, "device/set/ABCD1234/1/values/LEVEL")
	}
}

func TestNewDataPointPathData_EmptyInputsReturnZero(t *testing.T) {
	t.Parallel()
	if got := NewDataPointPathData(hmenum.InterfaceHmIPRF, "", 0, BucketValues, "STATE"); !got.IsZero() {
		t.Errorf("empty address must yield zero PathData, got %+v", got)
	}
	if got := NewDataPointPathData(hmenum.InterfaceHmIPRF, "VCU1", 0, BucketValues, ""); !got.IsZero() {
		t.Errorf("empty kind must yield zero PathData, got %+v", got)
	}
}

func TestNewDataPointPathData_EmptyBucketDefaultsToValues(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData(hmenum.InterfaceHmIPRF, "VCU1", 0, "", "STATE")
	if pd.Bucket != BucketValues {
		t.Errorf("Bucket = %q, want %q (empty bucket → VALUES default)", pd.Bucket, BucketValues)
	}
	if pd.StatePath != "device/status/VCU1/0/values/STATE" {
		t.Errorf("StatePath = %q, want %q", pd.StatePath, "device/status/VCU1/0/values/STATE")
	}
}

func TestPathData_MQTTState(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData(hmenum.InterfaceHmIPRF, "VCU1234567", 1, BucketValues, "STATE")
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
	if got := NewProgramPathData("X").MQTTState("gh", "ccu"); got != "" {
		t.Errorf("non-channel PathData.MQTTState must be empty, got %q", got)
	}
}

func TestNewProgramPathData(t *testing.T) {
	t.Parallel()
	pd := NewProgramPathData("MyScript")
	if pd.SetPath != "program/set/MyScript" {
		t.Errorf("SetPath = %q, want %q", pd.SetPath, "program/set/MyScript")
	}
	if pd.StatePath != "program/status/MyScript" {
		t.Errorf("StatePath = %q, want %q", pd.StatePath, "program/status/MyScript")
	}
	if got := NewProgramPathData(""); !got.IsZero() {
		t.Errorf("empty pid must yield zero PathData, got %+v", got)
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
	pd := NewDataPointPathData(hmenum.InterfaceHmIPRF, "VCU1234567", 1, BucketValues, "STATE")
	if !strings.HasPrefix(pd.SetPath, SetPathRoot) {
		t.Errorf("SetPath = %q, want prefix %q", pd.SetPath, SetPathRoot)
	}
	if !strings.HasPrefix(pd.StatePath, StatePathRoot) {
		t.Errorf("StatePath = %q, want prefix %q", pd.StatePath, StatePathRoot)
	}
}

func TestPathData_EmptyAddressIsZero(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData(hmenum.InterfaceHmIPRF, "", 1, BucketValues, "STATE")
	if !pd.IsZero() {
		t.Errorf("empty address must yield EmptyPathData, got %+v", pd)
	}
}

func TestPathData_EmptyKindIsZero(t *testing.T) {
	t.Parallel()
	pd := NewDataPointPathData(hmenum.InterfaceHmIPRF, "VCU1234567", 1, BucketValues, "")
	if !pd.IsZero() {
		t.Errorf("empty kind must yield EmptyPathData, got %+v", pd)
	}
}
