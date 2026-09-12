// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"context"
	"time"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

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
	_ payload.Source                   = (*Switch)(nil)
	_ payload.HADiscoveryEntityBuilder = (*Switch)(nil)
	_ payload.Source                   = (*AccessPermission)(nil)
	_ payload.HADiscoveryEntityBuilder = (*AccessPermission)(nil)
)

// switchValueTemplate reads the STATE envelope's scalar as a lower-cased
// string.
//
// The PerDPState envelope carries the value as a JSON boolean
// (`{"value":true,…}`). Jinja's default rendering of a Python boolean is
// `True`/`False` (capitalised) — that would never match `state_on`/`state_off`
// ("true"/"false"), leaving every switch entity stuck in `unknown`. Piping the
// scalar through `| lower` makes the comparison case-insensitive. The
// defensive `value_json is defined` guard catches the eviction case where Home
// Assistant reads an empty retained payload (unobserved DPs after a
// register-and-load-data cycle); without it it logs `'value_json' is
// undefined` template errors. The `value is not none` clause covers the
// registered-but-unobserved DP, whose envelope is
// `{"value":null,"available":true}` — `none | lower` renders the literal
// string "none", which matches neither `state_on` nor `state_off` and leaves
// the entity in a wrong state rather than in "unknown".
//
// This is the same rule the per-parameter discovery plane applies
// (valueJSONValueLowerTemplate in internal/north/mqtt/discovery.go) to the
// same envelope on the same topic; the two spellings must not drift.
const switchValueTemplate = `{% if value_json is defined and value_json.value is not none %}{{ value_json.value | lower }}{% endif %}`

// switchFields is the HA switch platform's own payload vocabulary, shared by
// the plain switch and the access permission: both mirror the Generic-Switch
// wire convention ("true"/"false") in both directions.
func switchFields() hadiscovery.SwitchFields {
	return hadiscovery.SwitchFields{
		PayloadOn:  "true",
		PayloadOff: "false",
		StateOn:    "true",
		StateOff:   "false",
	}
}

// HADiscoveryEntity describes the switch on the shared model. Switch maps the
// wire STATE parameter directly onto Home Assistant's switch entity — a
// toggle read and written on the same parameter.
//
// Without it the bridge falls back to its per-parameter classifier which
// routes generic STATE → switch — but the SuppressUndefinedGenericDataPoints
// pass marks every non-profile DP on a custom-DP channel as `usage=no_create`,
// so the Switch's STATE parameter on a SWITCH_VIRTUAL_RECEIVER channel never
// reaches HA. Declaring the entity here makes the custom-DP discovery
// authoritative — the suppression mark on the wire DP is bypassed because the
// entity is sourced from the channel's custom DP, not from the generic STATE
// itself.
func (s *Switch) HADiscoveryEntity() hamodel.Entity {
	if s == nil {
		return nil
	}
	// One slot, bound twice. The switch is read on the state topic and written
	// on the command topic, and the render pipeline resolves the two roles
	// separately, so a single ReadWrite binding on one role would project only
	// one of the two topics.
	slot := payload.WireSlot(string(hmenum.ParameterState))
	return &payload.CustomEntity{
		Basic: hamodel.Basic{
			EntityKey:      s.TopicSlot().Parameter,
			EntityPlatform: hacatalog.PlatformSwitch,
			Description: hamodel.Description{
				ValueTemplate: switchValueTemplate,
				// Explicit false prevents HA MQTT Switch from applying
				// optimistic local state updates before the CCU confirms the
				// command via the state topic. Without it, HA defaults to
				// optimistic=true when a command_topic is present, causing the
				// entity to flip locally even if the CCU rejects or delays the
				// write.
				Optimistic: hamodel.Ptr(false),
			},
			Binds: []hamodel.Binding{
				{Role: hamodel.RoleState, Mode: hamodel.Read, Slot: slot},
				{Role: hamodel.RoleCommand, Mode: hamodel.Write, Slot: slot},
			},
		},
		Fields: switchFields(),
	}
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
