// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import (
	"testing"
)

// --- MQTTCommand / MQTTConfig empty-address paths ---

func TestMQTTCommand_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTCommand(testBase, testCentral); got != "" {
		t.Errorf("empty address: MQTTCommand = %q, want empty", got)
	}
}

func TestMQTTConfig_EmptyAddress(t *testing.T) {
	t.Parallel()
	if got := EmptyPathData.MQTTConfig(testBase, testCentral); got != "" {
		t.Errorf("empty address: MQTTConfig = %q, want empty", got)
	}
}

// --- ADR-0011 hub free functions ---

func TestMQTTHubStatus(t *testing.T) {
	t.Parallel()
	got := MQTTHubStatus(testBase, testCentral)
	want := "openccu-loom/ccu1/hub/status"
	if got != want {
		t.Errorf("MQTTHubStatus = %q, want %q", got, want)
	}
}

func TestMQTTHubInfo(t *testing.T) {
	t.Parallel()
	got := MQTTHubInfo(testBase, testCentral)
	want := "openccu-loom/ccu1/hub/info"
	if got != want {
		t.Errorf("MQTTHubInfo = %q, want %q", got, want)
	}
}

func TestMQTTHubDiagnostics(t *testing.T) {
	t.Parallel()
	got := MQTTHubDiagnostics(testBase, testCentral)
	want := "openccu-loom/ccu1/hub/diagnostics"
	if got != want {
		t.Errorf("MQTTHubDiagnostics = %q, want %q", got, want)
	}
}

func TestMQTTHubSysvarState(t *testing.T) {
	t.Parallel()
	got := MQTTHubSysvarState(testBase, testCentral, "presence")
	want := "openccu-loom/ccu1/hub/sysvars/presence/state"
	if got != want {
		t.Errorf("MQTTHubSysvarState = %q, want %q", got, want)
	}
}

func TestMQTTHubSysvarState_EmptyName(t *testing.T) {
	t.Parallel()
	if got := MQTTHubSysvarState(testBase, testCentral, ""); got != "" {
		t.Errorf("empty name must return empty, got %q", got)
	}
}

func TestMQTTHubSysvarCommand(t *testing.T) {
	t.Parallel()
	got := MQTTHubSysvarCommand(testBase, testCentral, "presence")
	want := "openccu-loom/ccu1/hub/sysvars/presence/set"
	if got != want {
		t.Errorf("MQTTHubSysvarCommand = %q, want %q", got, want)
	}
}

func TestMQTTHubSysvarCommand_EmptyName(t *testing.T) {
	t.Parallel()
	if got := MQTTHubSysvarCommand(testBase, testCentral, ""); got != "" {
		t.Errorf("empty name must return empty, got %q", got)
	}
}

func TestMQTTHubProgramTrigger(t *testing.T) {
	t.Parallel()
	got := MQTTHubProgramTrigger(testBase, testCentral, "my_prog")
	want := "openccu-loom/ccu1/hub/programs/my_prog/trigger"
	if got != want {
		t.Errorf("MQTTHubProgramTrigger = %q, want %q", got, want)
	}
}

func TestMQTTHubProgramTrigger_EmptyID(t *testing.T) {
	t.Parallel()
	if got := MQTTHubProgramTrigger(testBase, testCentral, ""); got != "" {
		t.Errorf("empty id must return empty, got %q", got)
	}
}

func TestMQTTSystemStatus(t *testing.T) {
	t.Parallel()
	got := MQTTSystemStatus(testBase, testCentral)
	want := "openccu-loom/ccu1/system/status"
	if got != want {
		t.Errorf("MQTTSystemStatus = %q, want %q", got, want)
	}
}

func TestMQTTHubConnectivity(t *testing.T) {
	t.Parallel()
	got := MQTTHubConnectivity(testBase, testCentral, "HmIP-RF")
	want := "openccu-loom/ccu1/hub/connectivity/HmIP-RF"
	if got != want {
		t.Errorf("MQTTHubConnectivity = %q, want %q", got, want)
	}
}

func TestMQTTHubInstallMode(t *testing.T) {
	t.Parallel()
	got := MQTTHubInstallMode(testBase, testCentral)
	want := "openccu-loom/ccu1/hub/install_mode"
	if got != want {
		t.Errorf("MQTTHubInstallMode = %q, want %q", got, want)
	}
}

func TestMQTTHubInstallModeForInterface(t *testing.T) {
	t.Parallel()
	got := MQTTHubInstallModeForInterface(testBase, testCentral, "HmIP-RF")
	want := "openccu-loom/ccu1/hub/install_mode/HmIP-RF"
	if got != want {
		t.Errorf("MQTTHubInstallModeForInterface = %q, want %q", got, want)
	}
}

func TestMQTTHubInstallModeCommand(t *testing.T) {
	t.Parallel()
	got := MQTTHubInstallModeCommand(testBase, testCentral, "BidCos-RF")
	want := "openccu-loom/ccu1/hub/install_mode/BidCos-RF/set"
	if got != want {
		t.Errorf("MQTTHubInstallModeCommand = %q, want %q", got, want)
	}
}

func TestMQTTHubAlarmMessages(t *testing.T) {
	t.Parallel()
	got := MQTTHubAlarmMessages(testBase, testCentral)
	want := "openccu-loom/ccu1/hub/alarm_messages"
	if got != want {
		t.Errorf("MQTTHubAlarmMessages = %q, want %q", got, want)
	}
}

func TestMQTTHubServiceMessages(t *testing.T) {
	t.Parallel()
	got := MQTTHubServiceMessages(testBase, testCentral)
	want := "openccu-loom/ccu1/hub/service_messages"
	if got != want {
		t.Errorf("MQTTHubServiceMessages = %q, want %q", got, want)
	}
}

func TestMQTTCustomDPInvoke(t *testing.T) {
	t.Parallel()
	got := MQTTCustomDPInvoke(testBase, testCentral, "VCU1234567", "climate", "set_mode")
	want := "openccu-loom/ccu1/devices/VCU1234567/cdps/climate/set_mode/invoke"
	if got != want {
		t.Errorf("MQTTCustomDPInvoke = %q, want %q", got, want)
	}
}

func TestMQTTCustomDPInvoke_EmptyFields(t *testing.T) {
	t.Parallel()
	if got := MQTTCustomDPInvoke(testBase, testCentral, "", "climate", "set_mode"); got != "" {
		t.Errorf("empty address must return empty, got %q", got)
	}
	if got := MQTTCustomDPInvoke(testBase, testCentral, "VCU1", "", "set_mode"); got != "" {
		t.Errorf("empty name must return empty, got %q", got)
	}
	if got := MQTTCustomDPInvoke(testBase, testCentral, "VCU1", "climate", ""); got != "" {
		t.Errorf("empty operation must return empty, got %q", got)
	}
}
