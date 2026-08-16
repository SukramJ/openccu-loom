// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guarantee that *Switch satisfies the universal Source
// contract. ADR-0007 step 5.
//
// Switch inherits ServiceRegistry (and the generic turn_on / turn_off /
// set service methods) from its embedded *generic.Switch. The only
// custom-DP-level addition is turn_on_for, which bundles ON_TIME +
// STATE in one atomic put_paramset call.
var (
	_ payload.Source                    = (*Switch)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*Switch)(nil)
	_ payload.Source                    = (*AccessPermission)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*AccessPermission)(nil)
)

// HADiscoveryPayload returns the HA Switch-platform-specific payload
// skeleton. Switch maps the wire STATE parameter directly to HA's switch
// entity — toggle bool with payload_on/payload_off mirroring the
// Generic-Switch wire convention ("true"/"false").
//
// Without this method the bridge falls back to its per-parameter classifier
// which routes generic STATE → switch — but the Suppress-
// UndefinedGenericDataPoints pass marks every non-profile DP on a custom-DP
// channel as `usage=no_create`, so the Switch's STATE parameter on a
// SWITCH_VIRTUAL_RECEIVER channel never reaches HA. Implementing the builder
// here makes the custom-DP discovery authoritative — the suppression mark on
// the wire DP is bypassed because the discovery is sourced from the channel's
// custom-DP, not from the generic STATE itself.
func (s *Switch) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if s == nil || ctx == nil {
		return "", nil
	}
	body = map[string]any{
		"command_topic": ctx.WireParameterCommandTopic("STATE"),
		"payload_on":    "true",
		"payload_off":   "false",
		"state_topic":   ctx.WireParameterStateTopic("STATE"),
		// PerDPState envelope carries the value as a JSON boolean
		// (`{"value":true,...}`). Jinja's default rendering of a
		// Python boolean is `True`/`False` (capitalised) — that
		// would never match `state_on`/`state_off` ("true"/"false"),
		// leaving every switch entity stuck in `unknown`. Pipe the
		// scalar through `| lower` so the comparison is
		// case-insensitive. The defensive `value_json is defined`
		// guard catches the eviction case where HA reads an empty
		// retained payload (unobserved DPs after a register-and-
		// load-data cycle); without it HA logs `'value_json' is
		// undefined` template errors.
		"value_template": `{% if value_json is defined %}{{ value_json.value | lower }}{% endif %}`,
		"state_on":       "true",
		"state_off":      "false",
		// Explicit false prevents HA MQTT Switch from applying optimistic
		// local state updates before the CCU confirms the command via the
		// state_topic. Without it, HA defaults to optimistic=true when
		// a command_topic is present, causing the entity to flip locally
		// even if the CCU rejects or delays the write.
		"optimistic": false,
	}
	return "switch", body
}

// Info returns identity-level fields for a Switch.
func (s *Switch) Info() payload.InfoPayload {
	if s == nil {
		return nil
	}
	return &payload.SwitchInfo{
		Address:  s.Address(),
		Key:      s.DataPointKey().String(),
		Category: "switch",
	}
}

// Config returns the switch static configuration.
func (s *Switch) Config() payload.ConfigPayload {
	if s == nil {
		return nil
	}
	return &payload.SwitchConfig{
		Category: "switch",
	}
}

// State returns the live switch state.
func (s *Switch) State() payload.StatePayload {
	if s == nil {
		return nil
	}
	st := &payload.SwitchState{}
	if on, ok := s.IsOn(); ok {
		st.IsOn = &on
	}
	return st
}

// registerSwitchServices registers the switch-specific turn_on_for service
// method on top of the generic ones inherited from *generic.Switch.
func (s *Switch) registerSwitchServices() {
	s.RegisterService("turn_on_for", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		d, err := payload.ParamFloat64(params, "seconds")
		if err != nil {
			return err
		}
		return s.TurnOnFor(ctx, time.Duration(d*float64(time.Second)), priority)
	})
}
