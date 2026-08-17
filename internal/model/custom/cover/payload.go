// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Compile-time guarantees that the cover-domain custom data points
// satisfy the universal Source contract. Cover and Blind inherit
// the write half (ServiceRegistry) by promotion through their
// *generic.Float embed; Garage embeds payload.ServiceRegistry directly.
// HADiscoveryPayloadBuilder is also satisfied by all three types.
var (
	_ payload.Source                    = (*Cover)(nil)
	_ payload.Source                    = (*Blind)(nil)
	_ payload.Source                    = (*Garage)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*Cover)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*Blind)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*Garage)(nil)
)

// --- Cover ---

// Info returns identity-level fields for a Cover.
func (c *Cover) Info() payload.InfoPayload {
	if c == nil {
		return nil
	}
	out := &payload.CoverInfo{
		Address:   c.address,
		Category:  "cover",
		SubDPKeys: subDPKeysAsStrings(c.SubDataPointKeys()),
	}
	if c.Float != nil {
		out.Key = c.DataPointKey().String()
	}
	return out
}

// Config returns the cover capability configuration.
func (c *Cover) Config() payload.ConfigPayload {
	if c == nil {
		return nil
	}
	return &payload.CoverConfig{
		InvertedControl: c.Capabilities.InvertedControl,
		SupportsStop:    c.Capabilities.SupportsStop,
		SupportsTilt:    c.Capabilities.SupportsTilt,
	}
}

// State returns the live cover state in HA-friendly semantic keys:
// current_position (0..100 %), direction (when observable), and state
// (open/closed/opening/closing). The per-device availability flag rides on
// its own MQTT topic (eventbridge.markAvailability); a parallel `available`
// field in the state JSON would be redundant.
//
// `current_position` is emitted unconditionally when the cover declares
// SupportsPosition — HA's `position_template` references
// `value_json.current_position` and logs a warning on every retained-state
// rebroadcast where the key is missing, before the CCU has reported the
// actual level. Defaults to 0 (closed) until the first wire event arrives.
func (c *Cover) State() payload.StatePayload {
	if c == nil {
		return nil
	}
	out := &payload.CoverState{
		State: coverStateString(c.IsClosed(), c.IsOpening(), c.IsClosing()),
	}
	if pos, ok := c.Position(); ok {
		// OpenFraction rounds. Truncating LEVEL × 100 reports 29 %, 57 %
		// and 58 % one percent low — those three levels are just below an
		// exact hundredth in binary64 — so the HA slider snapped back one
		// step below the position the operator had just commanded, on
		// every retained-state read.
		v := pos.OpenFraction()
		out.CurrentPosition = &v
		lv := pos.Level()
		out.Level = &lv
	} else if c.Capabilities.SupportsPosition {
		v := 0
		out.CurrentPosition = &v
		lv := 0.0
		out.Level = &lv
	}
	if dir, ok := c.Direction(); ok {
		out.Direction = directionString(dir)
	}
	return out
}

// serviceCoverCommand is the service method that carries the motion
// command Home Assistant multiplexes onto a cover entity's single
// `command_topic`. An MQTT cover has exactly one command topic for the
// Open / Close / Stop buttons and distinguishes them by payload alone,
// so no per-operation topic can serve them and no wire parameter can
// either: LEVEL cannot express STOP and STOP cannot express a position.
const serviceCoverCommand = "cover_command"

// argCoverCommand is the scalar-argument key [serviceCoverCommand]
// expects. A bare MQTT payload is wrapped under it by the bridge before
// the invoke reaches the handler.
const argCoverCommand = "command"

// The command tokens advertised as payload_open / payload_close /
// payload_stop. Spelled as words rather than as wire values so a payload
// that lands on the wrong topic cannot be mistaken for a level or a
// boolean.
const (
	commandTokenOpen  = "OPEN"
	commandTokenClose = "CLOSE"
	commandTokenStop  = "STOP"
)

// registerCoverServices wires the cover operations onto the
// ServiceRegistry promoted via *generic.Float.
func (c *Cover) registerCoverServices() {
	c.RegisterService("open", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return c.Open(ctx, priority)
	})
	c.RegisterService("close", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return c.Close(ctx, priority)
	})
	c.RegisterService("stop", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return c.Stop(ctx, priority)
	})
	c.RegisterServiceWithArg("set_position", "position", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := payload.ParamFloat64(params, "position")
		if err != nil {
			return err
		}
		return c.SetPosition(ctx, v, priority)
	})
	c.RegisterServiceWithArg(serviceCoverCommand, argCoverCommand, c.invokeCoverCommand)
}

// invokeCoverCommand routes one of the [commandTokenOpen] /
// [commandTokenClose] / [commandTokenStop] tokens onto the matching
// operation.
//
// Dispatch goes back through the registry instead of calling
// c.Open / c.Close / c.Stop directly: a [Blind] shares this registry and
// replaces those three entries with handlers that drive both axes
// through the combined parameter. Calling the Cover methods here would
// silently pin every HA cover button to the LEVEL-only path.
func (c *Cover) invokeCoverCommand(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
	raw, err := payload.ParamString(params, argCoverCommand)
	if err != nil {
		return err
	}
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case commandTokenOpen:
		return c.Invoke(ctx, "open", nil, priority)
	case commandTokenClose:
		return c.Invoke(ctx, "close", nil, priority)
	case commandTokenStop:
		return c.Invoke(ctx, "stop", nil, priority)
	}
	return fmt.Errorf("%w: %s=%q", payload.ErrServiceInvalidParam, argCoverCommand, raw)
}

// --- Blind ---

// Info returns identity-level fields for a Blind.
func (b *Blind) Info() payload.InfoPayload {
	if b == nil {
		return nil
	}
	out := &payload.BlindInfo{
		Address:  b.Address(),
		Category: "cover",
		Kind:     "blind",
	}
	if b.Float != nil {
		out.Key = b.DataPointKey().String()
	}
	return out
}

// Config returns blind capabilities — tilt always present.
func (b *Blind) Config() payload.ConfigPayload {
	if b == nil {
		return nil
	}
	return &payload.BlindConfig{
		InvertedControl: b.Capabilities.InvertedControl,
		SupportsStop:    b.Capabilities.SupportsStop,
		SupportsTilt:    true,
	}
}

// State returns the blind state — position + tilt + state string.
// Availability rides on its own MQTT topic (eventbridge.markAvailability).
// `current_position` and `current_tilt_position` are emitted
// unconditionally so that the position_template / tilt_status_template
// references never log a missing-key warning. Defaults to 0 until the
// first wire event arrives.
func (b *Blind) State() payload.StatePayload {
	if b == nil {
		return nil
	}
	out := &payload.BlindState{
		State: coverStateString(b.IsClosed(), b.IsOpening(), b.IsClosing()),
	}
	if pos, ok := b.Position(); ok {
		// Rounded for the same reason as [Cover.State].
		out.CurrentPosition = pos.OpenFraction()
		lv := pos.Level()
		out.Level = &lv
	} else if b.Capabilities.SupportsPosition {
		out.CurrentPosition = 0
	}
	if tilt, ok := b.TiltPosition(); ok {
		out.CurrentTiltPosition = tilt.OpenFraction()
		out.TiltLevel = tilt.Level()
	} else {
		out.CurrentTiltPosition = 0
		out.TiltLevel = 0.0
	}
	if dir, ok := b.Direction(); ok {
		out.Direction = directionString(dir)
	}
	return out
}

// registerBlindServices adds the tilt-aware blind operations on top
// of the cover service set and overrides open / close / set_position
// so they route through [Blind.SetPosition] — the inherited
// Cover.SetPosition writes LEVEL on its own, bypassing the combined-
// parameter wire shape (COMBINED_PARAMETER for IP blinds,
// LEVEL_COMBINED for HM blinds).
func (b *Blind) registerBlindServices() {
	b.OverrideService("open", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return b.Open(ctx, priority)
	})
	b.OverrideService("close", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return b.Close(ctx, priority)
	})
	b.OverrideService("set_position", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := payload.ParamFloat64(params, "position")
		if err != nil {
			return err
		}
		return b.SetPosition(ctx, v, priority)
	})
	b.OverrideService("stop", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return b.Stop(ctx, priority)
	})
	b.RegisterServiceWithArg("set_tilt", "tilt", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := payload.ParamFloat64(params, "tilt")
		if err != nil {
			return err
		}
		return b.SetTilt(ctx, v, priority)
	})
	b.RegisterService("open_tilt", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return b.OpenTilt(ctx, priority)
	})
	b.RegisterService("close_tilt", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return b.CloseTilt(ctx, priority)
	})
	b.RegisterService("stop_tilt", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return b.StopTilt(ctx, priority)
	})
}

// --- Garage ---

// Info returns identity-level fields for a Garage door.
func (g *Garage) Info() payload.InfoPayload {
	if g == nil {
		return nil
	}
	return &payload.GarageInfo{
		Address:  g.Address,
		Category: "cover",
		Kind:     "garage",
		Key:      g.DataPointKey().String(),
	}
}

// Config returns the garage door's static configuration.
func (g *Garage) Config() payload.ConfigPayload {
	if g == nil {
		return nil
	}
	return &payload.GarageConfig{
		SupportsStop: true,
		SupportsVent: g.Capabilities.SupportsVent,
	}
}

// State returns the garage door state (open/closed/ventilation) using
// lowercase HA-canonical state strings consumed by value_template
// "{{ value_json.state }}".
//
// `current_position` is emitted unconditionally — HA's
// `position_template` references `value_json.current_position` and
// logs a warning on every retained-state rebroadcast where the key
// is missing. Defaults to 0 (closed) until the first wire event
// arrives.
func (g *Garage) State() payload.StatePayload {
	if g == nil {
		return nil
	}
	out := &payload.GarageState{
		State: coverStateString(g.IsClosed(), g.IsOpening(), g.IsClosing()),
	}
	if s, ok := g.DoorState(); ok {
		out.DoorState = string(s)
	}
	if pos, ok := g.Position(); ok {
		out.CurrentPosition = pos.OpenFraction()
	}
	return out
}

// registerGarageServices wires the garage door commands.
func (g *Garage) registerGarageServices() {
	g.RegisterService("open", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return g.Open(ctx, priority)
	})
	g.RegisterService("close", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return g.Close(ctx, priority)
	})
	g.RegisterService("stop", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return g.Stop(ctx, priority)
	})
	g.RegisterService("ventilate", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return g.Vent(ctx, priority)
	})
}

// --- helpers ---

func directionString(d CoverDirection) string {
	switch d {
	case DirectionUp:
		return "opening"
	case DirectionDown:
		return "closing"
	case DirectionUnknown:
		return "unknown"
	case DirectionNone:
		return "stopped"
	}
	return "stopped"
}

// coverStateString derives the HA-canonical state string from the three
// Boolean accessors. Priority matches
// (platforms/cover.py:74-83): opening > closing > closed > open.
// "stopped" is not emitted here — direction-based STOP is expressed via
// directionString; the state field represents the cover's gross position.
func coverStateString(isClosed, isOpening, isClosing bool) string {
	if isOpening {
		return "opening"
	}
	if isClosing {
		return "closing"
	}
	if isClosed {
		return "closed"
	}
	return "open"
}

func subDPKeysAsStrings(keys []hmtypes.DataPointKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.String()
	}
	return out
}

// --- HADiscoveryPayload implementations ---

// HADiscoveryPayload returns the HA Cover-platform-specific payload
// skeleton. Position reads come from the aggregated state topic via
// value_json.current_position. set_position uses the service-method
// command topic (unique, 1:1 with the set_position service).
//
// HA's cover platform publishes payload_open / payload_close /
// payload_stop to one shared command_topic, so that topic points at
// [serviceCoverCommand] — the service method that multiplexes the three
// tokens back onto Open / Close / Stop. No wire parameter can carry all
// three: LEVEL cannot express a stop, and STOP is a fire-once boolean
// action that turns an "open" payload into a halt and swallows a "close"
// payload entirely.
//
// Per ADR 0010: service-method topics for calls that reduce to one
// domain operation.
func (c *Cover) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if c == nil || ctx == nil {
		return "", nil
	}
	stateTopic := ctx.CustomDPStateTopic()
	body = map[string]any{
		"device_class":  VariantString(c.Variant),
		"optimistic":    false,
		"command_topic": ctx.ServiceMethodCommandTopic(serviceCoverCommand),
		"payload_open":  commandTokenOpen,
		"payload_close": commandTokenClose,
		"payload_stop":  commandTokenStop,
		// State reads from aggregated topic via value_json.state.
		"state_topic":    stateTopic,
		"value_template": "{{ value_json.state }}",
		"state_open":     "open",
		"state_closed":   "closed",
		"state_opening":  "opening",
		"state_closing":  "closing",
		"state_stopped":  "stopped",
	}
	// Only emit position topics when the capability is set. HA's MQTT
	// cover platform treats the presence of position_topic as "device
	// supports position" — on devices with no LEVEL write support (e.g.
	// only STOP) HA would incorrectly render a position slider.
	if c.Capabilities.SupportsPosition {
		body["position_open"] = 100
		body["position_closed"] = 0
		body["set_position_topic"] = ctx.ServiceMethodCommandTopic("set_position")
		body["set_position_template"] = "{{ (value | float / 100) }}"
		body["position_topic"] = stateTopic
		body["position_template"] = "{{ value_json.current_position }}"
	}
	return "cover", body
}

// HADiscoveryPayload returns the HA Cover-platform-specific payload
// for a Blind — extends Cover with tilt. set_tilt is a distinct
// service-method and gets its own service-method topic. Tilt reads
// come from value_json.current_tilt_position.
func (b *Blind) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if b == nil || ctx == nil {
		return "", nil
	}
	// Start from Cover's payload; override device_class for blind.
	// The Blind's variant is propagated through Cover.Variant so
	// subtypes like VariantShade (HmIP-HDM) surface the correct class.
	// When no explicit variant was set (zero value = VariantShutter),
	// blinds always emit "blind" — a plain Blind is never a shutter.
	_, body = b.Cover.HADiscoveryPayload(ctx)
	if body == nil {
		body = map[string]any{}
	}
	variant := b.Variant
	if variant == VariantShutter {
		variant = VariantBlind
	}
	body["device_class"] = VariantString(variant)
	stateTopic := ctx.CustomDPStateTopic()
	// Tilt: set_tilt is a distinct service method (unique write call).
	// Tilt_opened_value / tilt_closed_value mirror
	// (platforms/cover.py:63-64).
	body["tilt_status_topic"] = stateTopic
	body["tilt_status_template"] = "{{ value_json.current_tilt_position }}"
	body["tilt_command_topic"] = ctx.ServiceMethodCommandTopic("set_tilt")
	body["tilt_command_template"] = "{{ (value | float / 100) }}"
	body["tilt_min"] = 0
	body["tilt_max"] = 100
	body["tilt_opened_value"] = 100
	body["tilt_closed_value"] = 0
	return "cover", body
}

// HADiscoveryPayload returns the HA Cover-platform-specific payload
// for a Garage door. open/close/stop/ventilate are all distinct
// service methods and each gets its own service-method command topic.
// HA's cover command_topic however sends a single topic with
// payload_open/payload_close/payload_stop; to keep compatibility we
// route command_topic to the open service-method and handle the rest
// via open/close/stop service-method topics where HA supports it.
// The most pragmatic approach: use set_position_topic (which HA sends
// a numeric 0..100 to) mapped to set_position; open/close/stop are
// then the service-method topics. HA cover doesn't support separate
// open/close/stop topics, so command_topic carries the stop wire
// parameter path, with payload_open routed to open service and
// payload_close to close service via set_position_topic template.
// Simplest correct approach: command_topic = wire DOOR_COMMAND param
// (carries string values), state from value_json.door_state.
func (g *Garage) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if g == nil || ctx == nil {
		return "", nil
	}
	stateTopic := ctx.CustomDPStateTopic()
	body = map[string]any{
		"device_class": "garage",
		"optimistic":   false,
		// HA cover: command_topic with string payloads. Garage uses
		// DOOR_COMMAND wire parameter (OPEN/CLOSE/STOP/PARTIAL_OPEN).
		// No single service-method maps to all three HA payloads, so
		// wire-parameter command topic is the correct fallback per spec.
		"command_topic": ctx.WireParameterCommandTopic("DOOR_COMMAND"),
		"payload_open":  "OPEN",
		"payload_close": "CLOSE",
		"payload_stop":  "STOP",
		// Position reads from aggregated topic (virtual 0/50/100 from door_state).
		"position_topic":    stateTopic,
		"position_template": "{{ value_json.current_position }}",
		"position_open":     100,
		"position_closed":   0,
		// State from aggregated topic via value_json.state (lowercase HA-canonical
		// strings). Previously used door_state (CCU-raw uppercase) which did not
		// match HA's expected lowercase state strings.
		"state_topic":    stateTopic,
		"value_template": "{{ value_json.state }}",
		"state_open":     "open",
		"state_closed":   "closed",
		"state_opening":  "opening",
		"state_closing":  "closing",
		"state_stopped":  "stopped",
	}
	// SupportsVent (PARTIAL_OPEN) exposes the garage drive's intermediate "vent"
	// position. HA's MQTT Cover platform validates entity_document against a
	// closed key schema (extra=vol.REMOVE_EXTRA in homeassistant/components/
	// mqtt/cover.py) and has no vent_command_topic field, so this key is
	// silently dropped on the HA side — no button or other entity currently
	// reads it. The ventilate service itself stays reachable through REST,
	// WebSocket, MCP and the SPA cover tile; only the HA surface lacks a
	// dedicated control for it. Kept as documentation of the intended wire
	// shape for a future HA `button` discovery entity.
	if g.Capabilities.SupportsVent {
		body["vent_command_topic"] = ctx.ServiceMethodCommandTopic("ventilate")
	}
	return "cover", body
}
