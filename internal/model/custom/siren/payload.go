// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guarantees that the siren-domain custom data points
// satisfy the universal Source contract and the HA-Discovery payload
// builder contract (ADR 0010). ADR-0007 step 5.
var (
	_ payload.Source                    = (*Siren)(nil)
	_ payload.Source                    = (*SmokeSiren)(nil)
	_ payload.Source                    = (*SoundPlayer)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*Siren)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*SmokeSiren)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*SoundPlayer)(nil)
)

// --- Siren ---

// Info returns identity-level fields for a Siren.
func (s *Siren) Info() payload.InfoPayload {
	if s == nil {
		return nil
	}
	return &payload.SirenInfo{
		Address:  s.Address,
		Key:      s.key.String(),
		Category: "siren",
	}
}

// Config returns the siren capability configuration.
func (s *Siren) Config() payload.ConfigPayload {
	if s == nil {
		return nil
	}
	out := &payload.SirenConfig{
		SupportsAcoustic: s.Capabilities.SupportsAcoustic,
		SupportsOptical:  s.Capabilities.SupportsOptical,
		SupportsDuration: s.Capabilities.SupportsDuration,
	}
	if tones := s.AvailableTones(); len(tones) > 0 {
		out.AvailableTones = tones
	}
	if lights := s.AvailableLights(); len(lights) > 0 {
		out.AvailableLights = lights
	}
	return out
}

// State returns the HA-MQTT-Siren compliant state JSON.
//
// The MQTT siren platform's SIREN_PLATFORM_PAYLOAD_SCHEMA allows only
// {state, tone, duration, volume_level}; extra keys cause
// "Unable to update siren state attributes from payload". We therefore
// emit only `state` ("on" / "off"). Per-wire acoustic/optical state
// is visible through the individual Generic DPs on their own slot
// topics.
//
// `state` is always present so the value_template never logs a
// missing-key warning. Defaults to "off" until the first wire event.
func (s *Siren) State() payload.StatePayload {
	if s == nil {
		return nil
	}
	state := "off"
	if active, observed := s.IsActive(); observed && active {
		state = "on"
	}
	return &payload.SirenState{State: state}
}

// registerSirenServices wires the siren operations onto the embedded
// ServiceRegistry. Service-method names mirror
// service_method_names for siren custom DPs (turn_on, turn_off).
//
// The turn_on handler also accepts the HA MQTT siren command_topic mux:
// HA sends payload_on ("on") and payload_off ("off") to the same
// command_topic. When params["value"] == "off", the handler routes to
// TurnOff so HA's single-topic on/off multiplexing works end-to-end
// without a non-existent wire STATE parameter on the command path.
func (s *Siren) registerSirenServices() {
	s.RegisterService("turn_on", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		// Route to TurnOff when HA's command_topic mux sends payload_off.
		if v, _ := params["value"].(string); v == "off" {
			return s.TurnOff(ctx, priority)
		}
		cfg := OnConfig{}
		if d, err := payload.ParamFloat64(params, "duration"); err == nil {
			dur := time.Duration(d * float64(time.Second))
			cfg.Duration = dur
		}
		// `tone` is what Home Assistant's MQTT siren sends back for the
		// entry the operator picked out of `available_tones`. The
		// handler read only the domain name, so every tone chosen in HA
		// was dropped and the siren fired with its default — which, on
		// an HmIP-ASIR, is the label that silences it. The domain name
		// keeps precedence: it is what the REST and WebSocket surfaces
		// send, and an automation written against it must not change
		// meaning.
		if v, err := payload.ParamString(params, "acoustic_selection"); err == nil {
			cfg.AcousticSelection = &v
		} else if v, err := payload.ParamString(params, "tone"); err == nil {
			cfg.AcousticSelection = &v
		}
		if v, err := payload.ParamString(params, "optical_selection"); err == nil {
			cfg.OpticalSelection = &v
		}
		return s.TurnOn(ctx, cfg, priority)
	})
	s.RegisterService("turn_off", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return s.TurnOff(ctx, priority)
	})
}

// --- SmokeSiren ---

// Info returns identity-level fields for a SmokeSiren.
func (s *SmokeSiren) Info() payload.InfoPayload {
	if s == nil {
		return nil
	}
	return &payload.SmokeSirenInfo{
		SirenInfo: payload.SirenInfo{
			Address:  s.Address,
			Key:      s.key.String(),
			Category: "siren",
		},
		Kind: "smoke",
	}
}

// Config returns the smoke siren static configuration.
func (s *SmokeSiren) Config() payload.ConfigPayload {
	if s == nil {
		return nil
	}
	return &payload.SmokeSirenConfig{
		Kind: "smoke",
	}
}

// State returns the HA-MQTT-Siren compliant state JSON for a SmokeSiren.
//
// HA's MQTT siren platform validates the raw state_topic JSON
// against a strict schema (`SIREN_PLATFORM_PAYLOAD_SCHEMA`): only
// {state, tone, duration, volume_level} are allowed. We emit only
// `state` ("on" / "off"); the rich
// alarm_status/is_primary_alarm/is_secondary_alarm/is_intrusion
// view stays available via the per-wire-parameter Generic DPs
// (SMOKE_DETECTOR_ALARM_STATUS publishes on its own slot topic).
//
// `state` defaults to "off" until the first wire event arrives so
// HA's value_template doesn't log warnings on retained-state
// rebroadcasts before the CCU has reported the actual state.
func (s *SmokeSiren) State() payload.StatePayload {
	if s == nil {
		return nil
	}
	state := "off"
	if st, ok := s.Status(); ok && st != SmokeStatusIdleOff && st != "" {
		state = "on"
	}
	return &payload.SmokeSirenState{State: state}
}

// HADiscoveryPayload returns the HA Siren-platform-specific payload
// skeleton. HA siren platform uses a single command_topic with
// payload_on / payload_off — both values are sent to the same topic.
//
// command_topic points at the turn_on service-method topic. HA sends
// payload_on ("on") for activation and payload_off ("off") for
// deactivation to that single topic. The turn_on service handler muxes
// on params["value"] == "off" to route the off command to TurnOff,
// avoiding a write to a non-existent STATE wire parameter that would
// produce an XML-RPC fault on every HA command.
//
// Per ADR 0010: capabilities come from ConfigPayload.
func (s *Siren) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if s == nil || ctx == nil {
		return "", nil
	}
	body = map[string]any{
		// HA siren: single command_topic; payload_on/payload_off muxed by value.
		"command_topic": ctx.ServiceMethodCommandTopic("turn_on"),
		"payload_on":    "on",
		"payload_off":   "off",
		// State from the channel's aggregated state topic — the
		// StatePayload publishes the HA-compliant minimal JSON
		// `{"state": "on"|"off"}` so HA's strict siren schema
		// (SIREN_PLATFORM_PAYLOAD_SCHEMA) accepts it.
		"state_topic":    ctx.CustomDPStateTopic(),
		"value_template": "{{ value_json.state }}",
		"state_on":       "on",
		"state_off":      "off",
		"optimistic":     false,
	}
	// Capabilities from ConfigPayload.
	cfg, _ := s.Config().(*payload.SirenConfig)
	supportDuration := false
	if cfg != nil {
		supportDuration = cfg.SupportsDuration
	}
	body["support_duration"] = supportDuration
	// SupportsVolumeSet from the capability struct, not a constant.
	body["support_volume_set"] = s.Capabilities.SupportsVolumeSet
	if cfg != nil && len(cfg.AvailableTones) > 0 {
		body["available_tones"] = cfg.AvailableTones
	}
	return "siren", body
}

// HADiscoveryPayload returns the HA Siren-platform payload for a
// SmokeSiren. HmIP-SWSD is *not* a passive sensor — it can be
// triggered by writing the SMOKE_DETECTOR_COMMAND parameter (mirrors
// turn_on / turn_off via _SirenCommand.ON / OFF). HA logs `required
// key not provided @
// data['command_topic']` when no command_topic is emitted; the fix
// is to point command_topic at SMOKE_DETECTOR_COMMAND and mux
// payload_on / payload_off onto the device's wire enum values.
//
// payload_on  = INTRUSION_ALARM       (raises the alarm sound)
// payload_off = INTRUSION_ALARM_OFF   (silences it)
//
// The value_template translates the StatePayload `is_active` boolean
// back into the same enum so HA's two-way binding works without a
// separate state DP.
func (s *SmokeSiren) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if s == nil || ctx == nil {
		return "", nil
	}
	// state_topic uses the aggregated topic; StatePayload emits the
	// HA-compliant minimal `{"state": "on"|"off"}` JSON so the strict
	// SIREN_PLATFORM_PAYLOAD_SCHEMA accepts it.
	body = map[string]any{
		"command_topic":      ctx.WireParameterCommandTopic("SMOKE_DETECTOR_COMMAND"),
		"payload_on":         "INTRUSION_ALARM",
		"payload_off":        "INTRUSION_ALARM_OFF",
		"state_topic":        ctx.CustomDPStateTopic(),
		"value_template":     "{{ value_json.state }}",
		"state_on":           "on",
		"state_off":          "off",
		"support_duration":   false,
		"support_volume_set": false,
		"optimistic":         false,
	}
	return "siren", body
}

// HADiscoveryPayload returns the HA Siren-platform-specific payload
// for a SoundPlayer. turn_on/turn_off multiplexing: command_topic points
// at the turn_on service-method topic; payload_off ("off") is routed to
// TurnOff inside the service handler, avoiding a write to a non-existent
// STATE wire parameter.
func (sp *SoundPlayer) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if sp == nil || ctx == nil {
		return "", nil
	}
	body = map[string]any{
		// HA siren: single command_topic; payload_on/payload_off muxed by value.
		"command_topic": ctx.ServiceMethodCommandTopic("turn_on"),
		"payload_on":    "on",
		"payload_off":   "off",
		// State from the aggregated topic — StatePayload emits only
		// the HA-compliant `{"state": "on"|"off"}` keys so HA's
		// strict siren schema validation accepts it.
		"state_topic":        ctx.CustomDPStateTopic(),
		"value_template":     "{{ value_json.state }}",
		"state_on":           "on",
		"state_off":          "off",
		"support_duration":   true,
		"support_volume_set": false,
		"optimistic":         false,
	}
	// Available soundfiles as tones when present.
	if cfg, _ := sp.Config().(*payload.SoundPlayerConfig); cfg != nil && len(cfg.AvailableSoundfiles) > 0 {
		body["available_tones"] = cfg.AvailableSoundfiles
	}
	return "siren", body
}

// registerSmokeSirenServices wires the smoke siren operations onto the
// embedded ServiceRegistry.
func (s *SmokeSiren) registerSmokeSirenServices() {
	s.RegisterService("turn_on", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return s.TurnOn(ctx, priority)
	})
	s.RegisterService("turn_off", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return s.TurnOff(ctx, priority)
	})
}

// --- SoundPlayer ---

// Info returns identity-level fields for a SoundPlayer.
func (sp *SoundPlayer) Info() payload.InfoPayload {
	if sp == nil {
		return nil
	}
	return &payload.SoundPlayerInfo{
		SirenInfo: payload.SirenInfo{
			Address:  sp.Address,
			Key:      sp.key.String(),
			Category: "siren",
		},
		Kind: "sound_player",
	}
}

// Config returns the sound player capability configuration.
func (sp *SoundPlayer) Config() payload.ConfigPayload {
	if sp == nil {
		return nil
	}
	out := &payload.SoundPlayerConfig{
		SirenConfig: payload.SirenConfig{
			SupportsAcoustic: sp.Capabilities.SupportsAcoustic,
			SupportsOptical:  sp.Capabilities.SupportsOptical,
			SupportsDuration: sp.Capabilities.SupportsDuration,
		},
	}
	if sf := sp.AvailableSoundfiles(); len(sf) > 0 {
		out.AvailableSoundfiles = sf
	}
	if rep := sp.AvailableRepetitions(); len(rep) > 0 {
		out.AvailableRepetitions = rep
	}
	return out
}

// State returns the HA-MQTT-Siren compliant state JSON for a SoundPlayer.
//
// HA's MQTT siren platform validates the raw state_topic JSON
// against a strict schema (`SIREN_PLATFORM_PAYLOAD_SCHEMA`): only
// {state, tone, duration, volume_level} are allowed. We emit only
// `state` ("on" / "off"); current_soundfile and is_playing are
// available via the per-wire-parameter Generic DPs.
//
// Defaults to "off" until the first wire event arrives.
func (sp *SoundPlayer) State() payload.StatePayload {
	if sp == nil {
		return nil
	}
	state := "off"
	if playing, observed := sp.IsPlaying(); observed && playing {
		state = "on"
	}
	return &payload.SoundPlayerState{State: state}
}

// registerSoundPlayerServices wires the sound player operations onto the
// embedded ServiceRegistry.
//
// The turn_on handler also accepts the HA MQTT siren command_topic mux:
// when params["value"] == "off", the handler routes to TurnOff so HA's
// single-topic on/off multiplexing works without a non-existent STATE wire
// parameter on the command path.
func (sp *SoundPlayer) registerSoundPlayerServices() {
	sp.RegisterService("turn_on", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		// Route to TurnOff when HA's command_topic mux sends payload_off.
		if v, _ := params["value"].(string); v == "off" {
			return sp.TurnOff(ctx, priority)
		}
		cfg := PlayConfig{RepetitionsIndex: RepetitionsIndexNotSet}
		if idx, err := payload.ParamInt32(params, "soundfile_index"); err == nil {
			cfg.SoundfileIndex = int(idx)
		} else if label, err := payload.ParamString(params, "tone"); err == nil {
			// The player advertises its soundfiles as `available_tones`,
			// so HA returns the chosen one by label while the handler
			// expected an index — the choice never reached the device.
			// The label travels verbatim: it is the only form the
			// device's non-numbered entries (INTERNAL_SOUNDFILE,
			// RANDOM_SOUNDFILE) have, and PlaySound rejects a label the
			// device does not offer rather than dropping the parameter
			// and playing whatever was selected before.
			cfg.SoundfileLabel = label
		}
		if v, err := payload.ParamFloat64(params, "volume"); err == nil {
			cfg.Volume = v
		} else if v, err := payload.ParamFloat64(params, "volume_level"); err == nil {
			// HA's name for the same thing, sent whenever
			// support_volume_set is advertised.
			cfg.Volume = v
		}
		if d, err := payload.ParamFloat64(params, "duration"); err == nil {
			cfg.Duration = time.Duration(d * float64(time.Second))
		}
		if d, err := payload.ParamFloat64(params, "ramp_time"); err == nil {
			cfg.RampTime = time.Duration(d * float64(time.Second))
		}
		if idx, err := payload.ParamInt32(params, "repetitions_index"); err == nil {
			cfg.RepetitionsIndex = int(idx)
		}
		return sp.PlaySound(ctx, cfg, priority)
	})
	sp.RegisterService("turn_off", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return sp.TurnOff(ctx, priority)
	})
}

// LocalisableSelections implements [payload.LocalisableSelections]: the
// tone list a siren advertises is the VALUE_LIST of its acoustic-alarm
// selection, and Home Assistant returns the operator's pick as `tone`.
func (s *Siren) LocalisableSelections() []payload.LocalisableSelection {
	if s == nil || len(s.AvailableTones()) == 0 {
		return nil
	}
	return []payload.LocalisableSelection{{
		BodyKey:   "available_tones",
		ArgKey:    "tone",
		Parameter: string(hmenum.ParameterAcousticAlarmSelection),
	}}
}

// LocalisableSelections implements [payload.LocalisableSelections]. The
// player advertises its soundfiles as tones, so the same field carries a
// different parameter's VALUE_LIST.
func (sp *SoundPlayer) LocalisableSelections() []payload.LocalisableSelection {
	if sp == nil || len(sp.AvailableSoundfiles()) == 0 {
		return nil
	}
	return []payload.LocalisableSelection{{
		BodyKey:   "available_tones",
		ArgKey:    "tone",
		Parameter: string(hmenum.ParameterSoundfile),
	}}
}
