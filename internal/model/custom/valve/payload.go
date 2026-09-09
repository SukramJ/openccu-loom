// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package valve

import (
	"context"
	"time"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guarantees that the valve-domain custom data points
// satisfy the universal Source contract and the HA-Discovery payload
// builder contract (ADR 0010). ADR-0007 step 5.
//
// Irrigation inherits the ServiceRegistry write-half from its embedded
// *generic.Switch (which promotes turn_on, turn_off, set). Modulating
// inherits from its embedded *generic.Float (which promotes set_value).
// Both types register additional service methods below.
var (
	_ payload.Source                      = (*Irrigation)(nil)
	_ payload.Source                      = (*Modulating)(nil)
	_ payload.HADiscoveryComponentBuilder = (*Irrigation)(nil)
	_ payload.HADiscoveryComponentBuilder = (*Modulating)(nil)
)

// --- Irrigation ---

// Info returns identity-level fields for an Irrigation valve.
func (v *Irrigation) Info() payload.InfoPayload {
	if v == nil {
		return nil
	}
	return &payload.IrrigationValveInfo{
		Address:  v.Address(),
		Key:      v.DataPointKey().String(),
		Category: "valve",
		Kind:     "irrigation",
	}
}

// Config returns the irrigation valve static configuration.
func (v *Irrigation) Config() payload.ConfigPayload {
	if v == nil {
		return nil
	}
	return &payload.IrrigationValveConfig{
		Kind: "irrigation",
	}
}

// State returns the live irrigation valve state.
//
// `is_open` is emitted unconditionally — HA's
// `value_template={{ value_json.is_open }}` filter logs a warning on
// every retained-state rebroadcast where the key is missing, before
// the CCU has reported the actual state. Defaults to `false`
// (closed) until the first wire event arrives.
func (v *Irrigation) State() payload.StatePayload {
	if v == nil {
		return nil
	}
	open, observed := v.IsOpen()
	if !observed {
		open = false
	}
	return &payload.IrrigationValveState{IsOpen: open}
}

// registerIrrigationServices registers the irrigation-specific service
// methods on top of the generic ones inherited from *generic.Switch.
func (v *Irrigation) registerIrrigationServices() {
	v.RegisterService("open", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		var dur time.Duration
		if d, err := payload.ParamFloat64(params, "duration"); err == nil {
			dur = time.Duration(d * float64(time.Second))
		}
		return v.Open(ctx, dur, priority)
	})
	v.RegisterService("close", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return v.Close(ctx, priority)
	})
}

// --- Modulating ---

// Info returns identity-level fields for a Modulating valve.
func (v *Modulating) Info() payload.InfoPayload {
	if v == nil {
		return nil
	}
	return &payload.ModulatingValveInfo{
		Address:  v.Address(),
		Key:      v.DataPointKey().String(),
		Category: "valve",
		Kind:     "modulating",
	}
}

// Config returns the modulating valve static configuration.
func (v *Modulating) Config() payload.ConfigPayload {
	if v == nil {
		return nil
	}
	return &payload.ModulatingValveConfig{
		Kind: "modulating",
	}
}

// State returns the live modulating valve state.
//
// `current_level_pct` is emitted unconditionally — HA's
// `value_template={{ value_json.current_level_pct }}` filter logs a
// warning on every retained-state rebroadcast where the key is
// missing, before the CCU has reported the actual level. Defaults
// to 0 (closed) until the first wire event arrives.
func (v *Modulating) State() payload.StatePayload {
	if v == nil {
		return nil
	}
	st := &payload.ModulatingValveState{}
	if pos, ok := v.Level(); ok {
		lvl := pos.Level()
		st.CurrentLevel = &lvl
		st.CurrentLevelPct = lvl * 100
	} else {
		st.CurrentLevel = nil
		st.CurrentLevelPct = 0.0
	}
	return st
}

// HADiscoveryComponent returns the HA Valve-platform-specific payload for an
// Irrigation valve. Irrigation is a binary open/close device. The open/close
// service methods are distinct — HA valve's command_topic however sends a
// single topic with payload_open/payload_close. Because the two payloads
// cannot be routed to separate service-method topics, we fall back to the
// wire-parameter command topic for STATE (boolean). State from
// value_json.is_open (bool).
//
// Per ADR 0010: open/close multiplexing on one HA command_topic →
// wire-parameter fallback (STATE). reports_position = false for irrigation.
func (v *Irrigation) HADiscoveryComponent(ctx payload.HADiscoveryContext) hadiscovery.Component {
	if v == nil || ctx == nil {
		return hadiscovery.Component{}
	}
	stateTopic := ctx.CustomDPStateTopic()
	return hadiscovery.Component{
		Platform: hacatalog.PlatformValve,
		// HA valve: command_topic with payload_open/payload_close.
		// Uses STATE parameter (boolean) matching STATE.
		CommandTopic: ctx.WireParameterCommandTopic("STATE"),
		// State from aggregated topic — is_open (bool). Render
		// HA-canonical state strings ("open" / "closed") directly via
		// value_template; the bare `{{ value_json.is_open }}` form
		// returns Python's `True`/`False` (capitalised) and doesn't
		// match any state_open / state_closed permutation. HA logs
		// `Payload received ... is not one of [open, closed,
		// opening, closing], got: False` until the explicit branch
		// emits a matching string.
		StateTopic:    stateTopic,
		ValueTemplate: "{% if value_json.is_open %}open{% else %}closed{% endif %}",
		// device_class drives the HA icon (water-droplet) and semantic
		// classification.
		DeviceClass: "water",
		Optimistic:  hadiscovery.Ptr(false),
		Fields: hadiscovery.ValveFields{
			PayloadOpen:  "true",
			PayloadClose: "false",
			// Irrigation is binary — no position reporting.
			ReportsPosition: hadiscovery.Ptr(false),
			StateOpen:       "open",
			StateClosed:     "closed",
		},
	}
}

// HADiscoveryComponent returns the HA Valve-platform-specific payload
// for a Modulating valve. set_level is a distinct service method →
// service-method command topic. State from value_json.current_level_pct
// (0..100). reports_position = true for modulating valves.
//
// Per ADR 0010: set_level is unambiguous → service-method topic.
func (v *Modulating) HADiscoveryComponent(ctx payload.HADiscoveryContext) hadiscovery.Component {
	if v == nil || ctx == nil {
		return hadiscovery.Component{}
	}
	stateTopic := ctx.CustomDPStateTopic()
	return hadiscovery.Component{
		Platform: hacatalog.PlatformValve,
		// set_level is a 1:1 service method → service-method command topic.
		CommandTopic:    ctx.ServiceMethodCommandTopic("set_level"),
		CommandTemplate: "{{ (value | float / 100) }}",
		// State from aggregated topic — current_level_pct (0..100).
		StateTopic:    stateTopic,
		ValueTemplate: "{{ value_json.current_level_pct }}",
		// device_class drives the HA icon — water-droplet for
		// irrigation valves; modulating water-flow regulators
		// inherit the same classification.
		DeviceClass: "water",
		Optimistic:  hadiscovery.Ptr(false),
		Fields: hadiscovery.ValveFields{
			// Modulating valves report position.
			ReportsPosition: hadiscovery.Ptr(true),
		},
	}
}

// registerModulatingServices registers the modulating valve service
// methods on top of the generic ones inherited from *generic.Float.
func (v *Modulating) registerModulatingServices() {
	v.RegisterServiceWithArg("set_level", "level", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		f, err := payload.ParamFloat64(params, "level")
		if err != nil {
			return err
		}
		return v.SetLevel(ctx, f, priority)
	})
}
