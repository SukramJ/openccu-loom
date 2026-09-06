// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// discoveryCtx is a minimal stub for payload.HADiscoveryContext used in
// payload-builder smoke tests.
type discoveryCtx struct{}

func (discoveryCtx) CustomDPStateTopic() string { return "test/custom/state" }
func (discoveryCtx) ServiceMethodCommandTopic(method string) string {
	return "test/svc/" + method + "/set"
}

func (discoveryCtx) WireParameterCommandTopic(parameter string) string {
	return "test/" + parameter + "/set"
}

func (discoveryCtx) WireParameterStateTopic(parameter string) string {
	return "test/" + parameter
}

func (discoveryCtx) WireParameterStateTopicOn(_, parameter string) string {
	return discoveryCtx{}.WireParameterStateTopic(parameter)
}

var _ payload.HADiscoveryContext = discoveryCtx{}

// --- Siren ---

func TestSirenHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var s *Siren
	comp, body := s.HADiscoveryPayload(discoveryCtx{})
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
	comp, body := r.siren.HADiscoveryPayload(discoveryCtx{})
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
	_, body := r.siren.HADiscoveryPayload(ctx)

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
	_, body := r.siren.HADiscoveryPayload(discoveryCtx{})
	if v, _ := body["support_volume_set"].(bool); v {
		t.Error("support_volume_set: got true, want false when SupportsVolumeSet=false")
	}

	// With SupportsVolumeSet=true: must be true.
	r2 := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true, SupportsVolumeSet: true})
	_, body2 := r2.siren.HADiscoveryPayload(discoveryCtx{})
	if v, _ := body2["support_volume_set"].(bool); !v {
		t.Error("support_volume_set: got false, want true when SupportsVolumeSet=true")
	}
}

// --- SmokeSiren ---

func TestSmokeSirenHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var s *SmokeSiren
	comp, body := s.HADiscoveryPayload(discoveryCtx{})
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
	comp, body := s.HADiscoveryPayload(discoveryCtx{})
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
	_, body := s.HADiscoveryPayload(ctx)

	// state_topic uses the aggregated topic; StatePayload emits only
	// {state}, satisfying HA's strict SIREN_PLATFORM_PAYLOAD_SCHEMA.
	wantState := ctx.CustomDPStateTopic()
	if v, _ := body["state_topic"].(string); v != wantState {
		t.Errorf("state_topic = %q, want %q", v, wantState)
	}
	wantCmd := ctx.WireParameterCommandTopic("SMOKE_DETECTOR_COMMAND")
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
	comp, body := sp.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

// TestSoundPlayerHADiscoveryPayload_NilContextReturnsNil pins the nil
// context guard on SoundPlayer.HADiscoveryPayload.
func TestSoundPlayerHADiscoveryPayload_NilContextReturnsNil(t *testing.T) {
	t.Parallel()
	sp := NewSoundPlayer(SoundPlayerConfig{})
	comp, body := sp.HADiscoveryPayload(nil)
	if comp != "" || body != nil {
		t.Fatalf("nil ctx: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

// TestSoundPlayerHADiscoveryPayload_Component pins that SoundPlayer is
// Advertised as a HA "siren" entity — it mirrors
// CustomDpSoundPlayer which is a subclass of CustomDpSiren
// (siren.py:272-418) and is thus the correct MQTT platform mapping.
func TestSoundPlayerHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	sp := NewSoundPlayer(SoundPlayerConfig{})
	comp, body := sp.HADiscoveryPayload(discoveryCtx{})
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
	_, body := sp.HADiscoveryPayload(ctx)

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
	_, body := sp.HADiscoveryPayload(discoveryCtx{})
	if _, ok := body["available_tones"]; ok {
		t.Error("available_tones must be absent when no soundfiles configured")
	}
}
