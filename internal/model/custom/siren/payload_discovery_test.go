// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// discoveryCtx is a minimal stub for [payload.HADiscoveryTopics] used in
// entity-builder smoke tests. It returns stable, testable topic strings.
type discoveryCtx struct{}

func (discoveryCtx) CustomDPStateTopic() string   { return "test/custom/state" }
func (discoveryCtx) CustomDPCommandTopic() string { return "test/custom/state/set" }

// ServiceMethodCommandTopic is what the render context builds on its own out
// of [discoveryCtx.CustomDPCommandTopic]; it is spelled here so an assertion
// can name a method topic without repeating the join.
func (c discoveryCtx) ServiceMethodCommandTopic(method string) string {
	return c.CustomDPCommandTopic() + "/" + method
}

func (discoveryCtx) WireParameterCommandTopic(channelAddress, parameter string) string {
	if channelAddress == "" {
		return "test/" + parameter + "/set"
	}
	return "test/" + channelAddress + "/" + parameter + "/set"
}

func (discoveryCtx) WireParameterStateTopic(channelAddress, parameter string) string {
	if channelAddress == "" {
		return "test/" + parameter
	}
	return "test/" + channelAddress + "/" + parameter
}

func (discoveryCtx) DeviceAvailabilityTopic() string { return "test/availability" }
func (discoveryCtx) BridgeStatusTopic() string       { return "test/bridge/status" }

// compile-time check: discoveryCtx satisfies payload.HADiscoveryTopics.
var _ payload.HADiscoveryTopics = discoveryCtx{}

// --- Siren ---

func TestSirenHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var s *Siren
	comp, body := haEntity(t, s.HADiscoveryEntity(), discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestSirenHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
		SupportsDuration: true,
	})
	comp, body := haEntity(t, r.siren.HADiscoveryEntity(), discoveryCtx{})
	if comp != "siren" {
		t.Fatalf("component = %q, want %q", comp, "siren")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestSirenHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{
		SupportsAcoustic: true,
	})
	ctx := discoveryCtx{}
	_, body := haEntity(t, r.siren.HADiscoveryEntity(), ctx)

	for _, key := range []string{
		"state_topic",
		"command_topic",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required key %q", key)
		}
	}

	// state_topic uses the aggregated topic; StatePayload emits the
	// HA-compliant minimal `{"state": "on"|"off"}` JSON so HA's
	// strict siren-schema validator (SIREN_PLATFORM_PAYLOAD_SCHEMA)
	// accepts it. Extras like acoustic_active / optical_active stay
	// out of the StatePayload — per-wire Generic DPs continue to
	// expose them on their own slot topics for REST + UI consumers.
	wantState := ctx.CustomDPStateTopic()
	if v, _ := body["state_topic"].(string); v != wantState {
		t.Errorf("state_topic = %q, want %q", v, wantState)
	}
	// command_topic must point at the turn_on service-method topic, not a
	// non-existent STATE wire parameter. HmIP siren devices have no STATE
	// parameter — writing it would produce an XML-RPC fault on every HA command.
	wantCmd := ctx.ServiceMethodCommandTopic("turn_on")
	if v, _ := body["command_topic"].(string); v != wantCmd {
		t.Errorf("command_topic = %q, want %q (must use service-method topic, not wire STATE)", v, wantCmd)
	}
}

// TestSirenSupportVolumeSetReadsCapability verifies that support_volume_set
// in HA discovery is driven by Capabilities.SupportsVolumeSet, not a
// hardcoded false.
func TestSirenSupportVolumeSetReadsCapability(t *testing.T) {
	t.Parallel()

	// With SupportsVolumeSet=false: must be false.
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true, SupportsVolumeSet: false})
	_, body := haEntity(t, r.siren.HADiscoveryEntity(), discoveryCtx{})
	if v, _ := body["support_volume_set"].(bool); v {
		t.Error("support_volume_set: got true, want false when SupportsVolumeSet=false")
	}

	// With SupportsVolumeSet=true: must be true.
	r2 := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true, SupportsVolumeSet: true})
	_, body2 := haEntity(t, r2.siren.HADiscoveryEntity(), discoveryCtx{})
	if v, _ := body2["support_volume_set"].(bool); !v {
		t.Error("support_volume_set: got false, want true when SupportsVolumeSet=true")
	}
}

// --- SmokeSiren ---

func TestSmokeSirenHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var s *SmokeSiren
	comp, body := haEntity(t, s.HADiscoveryEntity(), discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

// TestSmokeSirenHADiscoveryPayload_Component pins the siren
// Classification. HmIP-SWSD is *not* passive
// CustomDpIpSirenSmoke implements turn_on / turn_off by writing
// SMOKE_DETECTOR_COMMAND with _SirenCommand.ON / OFF (==
// "INTRUSION_ALARM" / "INTRUSION_ALARM_OFF"). The HA-correct mapping
// is therefore the Siren platform with command_topic pointing at the
// wire-parameter — earlier discoveries that omitted command_topic
// were rejected by HA's schema (`required key not provided @
// data['command_topic']` in the broker log).
func TestSmokeSirenHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	s := NewSmokeSiren(SmokeSirenConfig{})
	comp, body := haEntity(t, s.HADiscoveryEntity(), discoveryCtx{})
	if comp != "siren" {
		t.Fatalf("component = %q, want %q", comp, "siren")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestSmokeSirenHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	s := NewSmokeSiren(SmokeSirenConfig{})
	ctx := discoveryCtx{}
	_, body := haEntity(t, s.HADiscoveryEntity(), ctx)

	// state_topic uses the aggregated topic; StatePayload emits only
	// {state}, satisfying HA's strict SIREN_PLATFORM_PAYLOAD_SCHEMA.
	wantState := ctx.CustomDPStateTopic()
	if v, _ := body["state_topic"].(string); v != wantState {
		t.Errorf("state_topic = %q, want %q", v, wantState)
	}
	wantCmd := ctx.WireParameterCommandTopic("", "SMOKE_DETECTOR_COMMAND")
	if v, _ := body["command_topic"].(string); v != wantCmd {
		t.Errorf("command_topic = %q, want %q (must point at the wire param the daemon writes for turn_on/turn_off)", v, wantCmd)
	}
	if v, _ := body["payload_on"].(string); v != "INTRUSION_ALARM" {
		t.Errorf("payload_on = %q, want %q (mirrors aiohomematic _SirenCommand.ON)", v, "INTRUSION_ALARM")
	}
	if v, _ := body["payload_off"].(string); v != "INTRUSION_ALARM_OFF" {
		t.Errorf("payload_off = %q, want %q (mirrors aiohomematic _SirenCommand.OFF)", v, "INTRUSION_ALARM_OFF")
	}
}

// --- SoundPlayer ---

// TestSoundPlayerHADiscoveryPayload_NilReceiverReturnsNil pins the nil
// guard on SoundPlayer.HADiscoveryPayload.
func TestSoundPlayerHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var sp *SoundPlayer
	comp, body := haEntity(t, sp.HADiscoveryEntity(), discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

// TestSoundPlayerHADiscoveryPayload_WithoutTopicsStillDescribes pins what a missing transport does to
// an entity: nothing. The entity is its description and its bindings, and it
// is complete before any topic is resolved — a renderer with no layout simply
// produces no topic strings.
func TestSoundPlayerHADiscoveryPayload_WithoutTopicsStillDescribes(t *testing.T) {
	t.Parallel()
	sp := NewSoundPlayer(SoundPlayerConfig{})
	comp, body := haEntity(t, sp.HADiscoveryEntity(), nil)
	if comp != "siren" {
		t.Fatalf("component = %q, want siren", comp)
	}
	for key := range body {
		if strings.HasSuffix(key, "_topic") {
			t.Errorf("%s present with no topic layout: %v", key, body[key])
		}
	}
}

// TestSoundPlayerHADiscoveryPayload_Component pins that SoundPlayer is
// Advertised as a HA "siren" entity — it mirrors
// CustomDpSoundPlayer which is a subclass of CustomDpSiren
// (siren.py:272-418) and is thus the correct MQTT platform mapping.
func TestSoundPlayerHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	sp := NewSoundPlayer(SoundPlayerConfig{})
	comp, body := haEntity(t, sp.HADiscoveryEntity(), discoveryCtx{})
	if comp != "siren" {
		t.Fatalf("component = %q, want %q", comp, "siren")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

// TestSoundPlayerHADiscoveryPayload_RequiredKeys pins the mandatory
// HA siren schema fields for a SoundPlayer: state_topic,
// command_topic, support_duration (must be true — SoundPlayer carries
// DURATION_VALUE / DURATION_UNIT), and state_on/state_off.
func TestSoundPlayerHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	sp := NewSoundPlayer(SoundPlayerConfig{})
	ctx := discoveryCtx{}
	_, body := haEntity(t, sp.HADiscoveryEntity(), ctx)

	for _, key := range []string{
		"state_topic",
		"command_topic",
		"support_duration",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required key %q in SoundPlayer discovery body", key)
		}
	}

	// state_topic uses the aggregated topic; StatePayload emits the
	// HA-compliant `{"state": "on"|"off"}` shape so HA's strict
	// siren-schema validator accepts it.
	wantState := ctx.CustomDPStateTopic()
	if v, _ := body["state_topic"].(string); v != wantState {
		t.Errorf("state_topic = %q, want %q", v, wantState)
	}
	if v, _ := body["support_duration"].(bool); !v {
		t.Error("support_duration = false, want true (SoundPlayer always carries DURATION_VALUE/UNIT)")
	}
	if v, _ := body["optimistic"].(bool); v {
		t.Error("optimistic = true, want false")
	}
}

// TestSoundPlayerHADiscoveryPayload_AvailableTones pins that when
// soundfiles are configured on the channel, the discovery payload
// Exposes them as available_tones — mirrors
// SirenCapabilities.soundfiles + available_tones field.
// With no channel (nil) the available_tones field is absent.
func TestSoundPlayerHADiscoveryPayload_AvailableTonesAbsentWhenNone(t *testing.T) {
	t.Parallel()
	sp := NewSoundPlayer(SoundPlayerConfig{}) // no channel → no soundfiles
	_, body := haEntity(t, sp.HADiscoveryEntity(), discoveryCtx{})
	if _, ok := body["available_tones"]; ok {
		t.Error("available_tones must be absent when no soundfiles configured")
	}
}
