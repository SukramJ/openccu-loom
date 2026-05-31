// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package naming

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// helpers shared by the MQTT-topic tests.
const (
	testBase    = "openccu-loom"
	testCentral = "ccu1"
)

func newChannelPD() PathData {
	return NewChannelPathData(hmenum.InterfaceHmIPRF, "VCU1234567", 2)
}

func newDevicePD() PathData {
	return NewDevicePathData(hmenum.InterfaceHmIPRF, "VCU1234567")
}

func newCustomPD() PathData {
	return NewCustomDPPathData(hmenum.InterfaceHmIPRF, "VCU1234567", 2, "climate")
}

// --- NewChannelPathData ---

func TestNewChannelPathData_Empty(t *testing.T) {
	t.Parallel()
	if got := NewChannelPathData(hmenum.InterfaceHmIPRF, "", 0); !got.IsZero() {
		t.Errorf("empty address must yield zero, got %+v", got)
	}
}

func TestNewChannelPathData_Populated(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	if pd.Address != "VCU1234567" || pd.ChannelNo != 2 || pd.Interface != hmenum.InterfaceHmIPRF {
		t.Errorf("unexpected fields: %+v", pd)
	}
	if pd.Bucket != "" || pd.Kind != "" {
		t.Errorf("bucket/kind must be empty for channel PathData, got bucket=%q kind=%q", pd.Bucket, pd.Kind)
	}
}

// --- NewDevicePathData ---

func TestNewDevicePathData_Empty(t *testing.T) {
	t.Parallel()
	if got := NewDevicePathData(hmenum.InterfaceHmIPRF, ""); !got.IsZero() {
		t.Errorf("empty address must yield zero, got %+v", got)
	}
}

func TestNewDevicePathData_Populated(t *testing.T) {
	t.Parallel()
	pd := newDevicePD()
	if pd.Address != "VCU1234567" || pd.ChannelNo != 0 {
		t.Errorf("unexpected fields: %+v", pd)
	}
}

// --- NewCustomDPPathData ---

func TestNewCustomDPPathData_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := NewCustomDPPathData(hmenum.InterfaceHmIPRF, "", 0, "climate"); !got.IsZero() {
		t.Errorf("empty address must yield zero, got %+v", got)
	}
}

func TestNewCustomDPPathData_EmptyKind(t *testing.T) {
	t.Parallel()
	if got := NewCustomDPPathData(hmenum.InterfaceHmIPRF, "VCU1", 0, ""); !got.IsZero() {
		t.Errorf("empty kind must yield zero, got %+v", got)
	}
}

func TestNewCustomDPPathData_BucketForced(t *testing.T) {
	t.Parallel()
	pd := newCustomPD()
	if pd.Bucket != BucketCustom {
		t.Errorf("Bucket = %q, want BucketCustom", pd.Bucket)
	}
	if pd.Kind != "climate" {
		t.Errorf("Kind = %q, want climate", pd.Kind)
	}
}

// --- MQTTChannelAggregateState ---

func TestMQTTChannelAggregateState(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	got := pd.MQTTChannelAggregateState(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/2/state"
	if got != want {
		t.Errorf("MQTTChannelAggregateState = %q, want %q", got, want)
	}
}

func TestMQTTChannelAggregateState_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTChannelAggregateState(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- MQTTChannelEvent ---

func TestMQTTChannelEvent(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	got := pd.MQTTChannelEvent(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/2/event"
	if got != want {
		t.Errorf("MQTTChannelEvent = %q, want %q", got, want)
	}
}

func TestMQTTChannelEvent_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTChannelEvent(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- MQTTDataPointEvent ---

func TestMQTTDataPointEvent(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	got := pd.MQTTDataPointEvent(testBase, testCentral, "long_press")
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/2/event/long_press"
	if got != want {
		t.Errorf("MQTTDataPointEvent = %q, want %q", got, want)
	}
}

func TestMQTTDataPointEvent_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTDataPointEvent(testBase, testCentral, "x"); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- MQTTDeviceAvailability ---

func TestMQTTDeviceAvailability(t *testing.T) {
	t.Parallel()
	pd := newDevicePD()
	got := pd.MQTTDeviceAvailability(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/availability"
	if got != want {
		t.Errorf("MQTTDeviceAvailability = %q, want %q", got, want)
	}
}

func TestMQTTDeviceAvailability_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTDeviceAvailability(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- MQTTDeviceInfo ---

func TestMQTTDeviceInfo(t *testing.T) {
	t.Parallel()
	pd := newDevicePD()
	got := pd.MQTTDeviceInfo(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/info"
	if got != want {
		t.Errorf("MQTTDeviceInfo = %q, want %q", got, want)
	}
}

func TestMQTTDeviceInfo_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTDeviceInfo(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- MQTTDeviceDiagnostics ---

func TestMQTTDeviceDiagnostics(t *testing.T) {
	t.Parallel()
	pd := newDevicePD()
	got := pd.MQTTDeviceDiagnostics(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/diagnostics"
	if got != want {
		t.Errorf("MQTTDeviceDiagnostics = %q, want %q", got, want)
	}
}

func TestMQTTDeviceDiagnostics_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTDeviceDiagnostics(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- MQTTDeviceUpdateState / MQTTDeviceUpdateCommand ---

func TestMQTTDeviceUpdateState(t *testing.T) {
	t.Parallel()
	pd := newDevicePD()
	got := pd.MQTTDeviceUpdateState(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/update"
	if got != want {
		t.Errorf("MQTTDeviceUpdateState = %q, want %q", got, want)
	}
}

func TestMQTTDeviceUpdateState_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTDeviceUpdateState(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

func TestMQTTDeviceUpdateCommand(t *testing.T) {
	t.Parallel()
	pd := newDevicePD()
	got := pd.MQTTDeviceUpdateCommand(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/update/set"
	if got != want {
		t.Errorf("MQTTDeviceUpdateCommand = %q, want %q", got, want)
	}
}

func TestMQTTDeviceUpdateCommand_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTDeviceUpdateCommand(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- MQTTWeekProfileState / MQTTWeekProfileCommand ---

func TestMQTTWeekProfileState(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	got := pd.MQTTWeekProfileState(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/2/week_profile/state"
	if got != want {
		t.Errorf("MQTTWeekProfileState = %q, want %q", got, want)
	}
}

func TestMQTTWeekProfileState_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTWeekProfileState(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

func TestMQTTWeekProfileCommand(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	got := pd.MQTTWeekProfileCommand(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/2/week_profile/set"
	if got != want {
		t.Errorf("MQTTWeekProfileCommand = %q, want %q", got, want)
	}
}

func TestMQTTWeekProfileCommand_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTWeekProfileCommand(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- MQTTCustomDPState / MQTTCustomDPConfig / MQTTCustomDPServiceMethod ---

func TestMQTTCustomDPState(t *testing.T) {
	t.Parallel()
	pd := newCustomPD()
	got := pd.MQTTCustomDPState(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/2/custom/climate"
	if got != want {
		t.Errorf("MQTTCustomDPState = %q, want %q", got, want)
	}
}

func TestMQTTCustomDPState_WrongBucket(t *testing.T) {
	t.Parallel()
	// PathData with BucketValues — not custom — must return empty.
	pd := NewDataPointPathData(hmenum.InterfaceHmIPRF, "VCU1234567", 2, BucketValues, "climate")
	if got := pd.MQTTCustomDPState(testBase, testCentral); got != "" {
		t.Errorf("non-custom bucket must return empty, got %q", got)
	}
}

func TestMQTTCustomDPState_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTCustomDPState(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

func TestMQTTCustomDPConfig(t *testing.T) {
	t.Parallel()
	pd := newCustomPD()
	got := pd.MQTTCustomDPConfig(testBase, testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/2/custom/climate/config"
	if got != want {
		t.Errorf("MQTTCustomDPConfig = %q, want %q", got, want)
	}
}

func TestMQTTCustomDPConfig_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTCustomDPConfig(testBase, testCentral); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

func TestMQTTCustomDPServiceMethod(t *testing.T) {
	t.Parallel()
	pd := newCustomPD()
	got := pd.MQTTCustomDPServiceMethod(testBase, testCentral, "set_mode")
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/2/custom/climate/set/set_mode"
	if got != want {
		t.Errorf("MQTTCustomDPServiceMethod = %q, want %q", got, want)
	}
}

func TestMQTTCustomDPServiceMethod_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTCustomDPServiceMethod(testBase, testCentral, "open"); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- DiscoveryNodeID ---

func TestDiscoveryNodeID_WithCentral(t *testing.T) {
	t.Parallel()
	pd := newDevicePD()
	got := pd.DiscoveryNodeID("MyCCU")
	want := "myccu_vcu1234567"
	if got != want {
		t.Errorf("DiscoveryNodeID = %q, want %q", got, want)
	}
}

func TestDiscoveryNodeID_NoCentral(t *testing.T) {
	t.Parallel()
	pd := newDevicePD()
	got := pd.DiscoveryNodeID("")
	want := "vcu1234567"
	if got != want {
		t.Errorf("DiscoveryNodeID (no central) = %q, want %q", got, want)
	}
}

func TestDiscoveryNodeID_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.DiscoveryNodeID("ccu1"); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

// --- DiscoveryObjectID ---

func TestDiscoveryObjectID(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	got := pd.DiscoveryObjectID("STATE")
	want := "2_state"
	if got != want {
		t.Errorf("DiscoveryObjectID = %q, want %q", got, want)
	}
}

func TestDiscoveryObjectID_EmptySuffix(t *testing.T) {
	t.Parallel()
	if got := newChannelPD().DiscoveryObjectID(""); got != "" {
		t.Errorf("empty suffix must return empty, got %q", got)
	}
}

// --- DiscoveryUniqueID ---

func TestDiscoveryUniqueID_Full(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	got := pd.DiscoveryUniqueID("openccu-loom", "ccu1", "STATE")
	want := "openccu-loom_ccu1_vcu1234567_2_state"
	if got != want {
		t.Errorf("DiscoveryUniqueID = %q, want %q", got, want)
	}
}

func TestDiscoveryUniqueID_NoCentral(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	got := pd.DiscoveryUniqueID("openccu-loom", "", "STATE")
	want := "openccu-loom_vcu1234567_2_state"
	if got != want {
		t.Errorf("DiscoveryUniqueID (no central) = %q, want %q", got, want)
	}
}

func TestDiscoveryUniqueID_EmptyPrefix(t *testing.T) {
	t.Parallel()
	pd := newChannelPD()
	got := pd.DiscoveryUniqueID("", "ccu1", "STATE")
	// empty prefix defaults to "openccu-loom"
	want := "openccu-loom_ccu1_vcu1234567_2_state"
	if got != want {
		t.Errorf("DiscoveryUniqueID (empty prefix) = %q, want %q", got, want)
	}
}

func TestDiscoveryUniqueID_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.DiscoveryUniqueID("gh", "ccu1", "STATE"); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
}

func TestDiscoveryUniqueID_EmptySuffix(t *testing.T) {
	t.Parallel()
	if got := newChannelPD().DiscoveryUniqueID("gh", "ccu1", ""); got != "" {
		t.Errorf("empty suffix must return empty, got %q", got)
	}
}

// --- DiscoveryConfigTopic ---

func TestDiscoveryConfigTopic(t *testing.T) {
	t.Parallel()
	got := DiscoveryConfigTopic("lock", "ccu1_vcu1234567", "2_state")
	want := "homeassistant/lock/ccu1_vcu1234567/2_state/config"
	if got != want {
		t.Errorf("DiscoveryConfigTopic = %q, want %q", got, want)
	}
}

// --- base trimming ---

func TestMQTTTopics_BaseSlashTrimmed(t *testing.T) {
	t.Parallel()
	pd := newDevicePD()
	// Leading/trailing slashes on the base must be stripped.
	got := pd.MQTTDeviceInfo("/openccu-loom/", testCentral)
	want := "openccu-loom/ccu1/HmIP-RF/VCU1234567/info"
	if got != want {
		t.Errorf("MQTTDeviceInfo (slash base) = %q, want %q", got, want)
	}
}
