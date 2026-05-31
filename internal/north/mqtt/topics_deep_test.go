// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// 11. Exact string for DataPointState.
func TestTopicBuilderDataPointStateShape(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("openccu-loom")
	got := tb.DataPointState("c1", "HmIP-RF", "0001ABCD", 3, "STATE")
	want := "openccu-loom/c1/HmIP-RF/0001ABCD/3/values/STATE"
	if got != want {
		t.Fatalf("DataPointState: got %q want %q", got, want)
	}
}

// 12. DataPointCommand appends /set to the state topic.
func TestTopicBuilderDataPointCommandHasSetSuffix(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("openccu-loom")
	state := tb.DataPointState("c1", "HmIP-RF", "0001ABCD", 3, "STATE")
	cmd := tb.DataPointCommand("c1", "HmIP-RF", "0001ABCD", 3, "STATE")
	if cmd != state+"/set" {
		t.Fatalf("DataPointCommand: got %q want %q", cmd, state+"/set")
	}
}

// 13. DataPointEvent produces .../event/{etype} shape.
func TestTopicBuilderDataPointEventShape(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("openccu-loom")
	got := tb.DataPointEvent("c1", "HmIP-RF", "0001ABCD", 3, "keypress")
	want := "openccu-loom/c1/HmIP-RF/0001ABCD/3/event/keypress"
	if got != want {
		t.Fatalf("DataPointEvent: got %q want %q", got, want)
	}
}

// 14. Hub program state + trigger produce the expected canonical shapes.
func TestNamingHubProgramStateAndTrigger(t *testing.T) {
	t.Parallel()
	state := naming.MQTTHubProgramState("openccu-loom", "c1", "MorningRoutine")
	wantState := "openccu-loom/c1/hub/programs/MorningRoutine/state"
	if state != wantState {
		t.Fatalf("MQTTHubProgramState: got %q want %q", state, wantState)
	}
	trigger := naming.MQTTHubProgramTrigger("openccu-loom", "c1", "MorningRoutine")
	wantTrigger := "openccu-loom/c1/hub/programs/MorningRoutine/trigger"
	if trigger != wantTrigger {
		t.Fatalf("MQTTHubProgramTrigger: got %q want %q", trigger, wantTrigger)
	}
}

// 15. Trailing slash in base must not produce double slashes.
func TestTopicBuilderTrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("foo/")
	got := tb.DataPointState("c1", "HmIP-RF", "0001ABCD", 3, "STATE")
	// Must start with "foo/" not "foo//".
	if strings.HasPrefix(got, "foo//") {
		t.Fatalf("double slash in topic: %q", got)
	}
	want := "foo/c1/HmIP-RF/0001ABCD/3/values/STATE"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// 16. Empty base falls back to "openccu-loom".
func TestTopicBuilderEmptyBaseUsesDefault(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("")
	if tb.Base != "openccu-loom" {
		t.Fatalf("expected default base %q, got %q", "openccu-loom", tb.Base)
	}
	got := tb.BridgeStatus()
	want := "openccu-loom/bridge/status"
	if got != want {
		t.Fatalf("BridgeStatus with empty base: got %q want %q", got, want)
	}
}

// DiscoveryConfig: default Base="openccu-loom" → same topic as the previous hardcoded form.
func TestDiscoveryConfigUsesBaseAsNode(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("openccu-loom")
	got := tb.DiscoveryConfig("switch", "openccu-loom", "0001abcd_3_state")
	want := "homeassistant/switch/openccu-loom/0001abcd_3_state/config"
	if got != want {
		t.Fatalf("DiscoveryConfig default base: got %q want %q", got, want)
	}
}

// DiscoveryConfig: custom Base → node segment changes, no collision with default.
func TestDiscoveryConfigCustomBase(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("ccu1")
	got := tb.DiscoveryConfig("switch", "ccu1", "0001abcd_3_state")
	want := "homeassistant/switch/ccu1/0001abcd_3_state/config"
	if got != want {
		t.Fatalf("DiscoveryConfig custom base: got %q want %q", got, want)
	}
}

// DiscoveryConfig: Base with MQTT-disallowed chars → safe() sanitizes the node.
func TestDiscoveryConfigSafeBase(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("my host")
	got := tb.DiscoveryConfig("switch", "my_host", "obj1")
	want := "homeassistant/switch/my_host/obj1/config"
	if got != want {
		t.Fatalf("DiscoveryConfig safe base: got %q want %q", got, want)
	}
}

// safe() replaces MQTT-disallowed chars in all components. PathData
// (the source of truth for the topic shape) also upper-cases address
// And kind to mirror
// real CCU wire data is uppercase by convention; the synthetic
// lower-case input here exercises that normalisation.
func TestTopicBuilderSafeReplacesAllDisallowedChars(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	// address with slash, iface with +, parameter with # and space
	got := tb.DataPointState("c1", "If+ace", "A/B", 0, "STA#TE me")
	// Each disallowed char → underscore; address + kind upper-cased.
	want := "gh/c1/If_ace/A_B/0/values/STA_TE_ME"
	if got != want {
		t.Fatalf("safe replacement: got %q want %q", got, want)
	}
}

// Connectivity topic shape.
func TestNamingHubConnectivityShape(t *testing.T) {
	t.Parallel()
	got := naming.MQTTHubConnectivity("openccu-loom", "c1", "HmIP-RF")
	want := "openccu-loom/c1/hub/connectivity/HmIP-RF"
	if got != want {
		t.Fatalf("MQTTHubConnectivity: got %q want %q", got, want)
	}
}

// InstallMode topic shape.
func TestNamingHubInstallModeShape(t *testing.T) {
	t.Parallel()
	got := naming.MQTTHubInstallMode("openccu-loom", "c1")
	want := "openccu-loom/c1/hub/install_mode"
	if got != want {
		t.Fatalf("MQTTHubInstallMode: got %q want %q", got, want)
	}
}

// Sysvar state + /set topic.
func TestNamingHubSysvarAndCommand(t *testing.T) {
	t.Parallel()
	sv := naming.MQTTHubSysvarState("openccu-loom", "c1", "PartyMode")
	wantSV := "openccu-loom/c1/hub/sysvars/PartyMode/state"
	if sv != wantSV {
		t.Fatalf("MQTTHubSysvarState: got %q want %q", sv, wantSV)
	}
	cmd := naming.MQTTHubSysvarCommand("openccu-loom", "c1", "PartyMode")
	wantCmd := "openccu-loom/c1/hub/sysvars/PartyMode/set"
	if cmd != wantCmd {
		t.Fatalf("MQTTHubSysvarCommand: got %q want %q", cmd, wantCmd)
	}
}

// CustomDPInvoke topic shape.
func TestTopicBuilderCustomDPInvokeShape(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("openccu-loom")
	got := tb.CustomDPInvoke("c1", "0001ABCD", "light_dp", "turn_on")
	want := "openccu-loom/c1/devices/0001ABCD/cdps/light_dp/turn_on/invoke"
	if got != want {
		t.Fatalf("CustomDPInvoke: got %q want %q", got, want)
	}
}

// CustomDPInvoke sanitises disallowed characters in all path components.
func TestTopicBuilderCustomDPInvokeSafeComponents(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("openccu-loom")
	got := tb.CustomDPInvoke("c1", "A/B+C", "dp#1", "set value")
	// safe() replaces /, +, #, space → underscore; verify the exact sanitised form.
	want := "openccu-loom/c1/devices/A_B_C/cdps/dp_1/set_value/invoke"
	if got != want {
		t.Fatalf("CustomDPInvoke safe: got %q want %q", got, want)
	}
	// Disallowed raw chars must not appear as literal topic-level chars.
	for _, bad := range []string{"+", "#"} {
		if strings.Contains(got, bad) {
			t.Fatalf("disallowed char %q found in topic: %q", bad, got)
		}
	}
	// last segment must still be "invoke"
	if !strings.HasSuffix(got, "/invoke") {
		t.Fatalf("missing /invoke suffix: %q", got)
	}
}

// AlarmMessages canonical hub topic.
func TestNamingHubAlarmMessagesShape(t *testing.T) {
	t.Parallel()
	got := naming.MQTTHubAlarmMessages("openccu-loom", "c1")
	want := "openccu-loom/c1/hub/alarm_messages"
	if got != want {
		t.Fatalf("MQTTHubAlarmMessages: got %q want %q", got, want)
	}
}

// ServiceMessages canonical hub topic.
func TestNamingHubServiceMessagesShape(t *testing.T) {
	t.Parallel()
	got := naming.MQTTHubServiceMessages("openccu-loom", "c1")
	want := "openccu-loom/c1/hub/service_messages"
	if got != want {
		t.Fatalf("MQTTHubServiceMessages: got %q want %q", got, want)
	}
}
