// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"fmt"
	"strings"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Compile-time guarantees that the cover-domain custom data points
// satisfy the universal Source contract. Cover and Blind inherit
// the write half (ServiceRegistry) by promotion through their
// *generic.Float embed; Garage embeds payload.ServiceRegistry directly.
// HADiscoveryEntityBuilder is also satisfied by all three types.
var (
	_ payload.Source                   = (*Cover)(nil)
	_ payload.Source                   = (*Blind)(nil)
	_ payload.Source                   = (*Garage)(nil)
	_ payload.HADiscoveryEntityBuilder = (*Cover)(nil)
	_ payload.HADiscoveryEntityBuilder = (*Blind)(nil)
	_ payload.HADiscoveryEntityBuilder = (*Garage)(nil)
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

// --- shared-model entities ---

// coverEntity is a cover on the shared model plus the keys the model has no
// field for: HA's cover platform names a topic per operation
// (`set_position_topic`, `tilt_command_topic`) and multiplexes open, close and
// stop onto one command_topic — which no single writable datapoint can carry,
// and which the render pipeline cannot wire up on its own because a cover
// declares more than one named action.
type coverEntity struct {
	payload.CustomEntity

	// fields is the platform vocabulary that needs no topic.
	fields hadiscovery.CoverFields
	// method is the named action the command topic points at, empty for a
	// cover whose command is a wire-parameter binding.
	method string
	// position adds the read side of the position surface: HA's MQTT cover
	// platform treats the presence of position_topic as "device supports
	// position", so a drive with no LEVEL write (only STOP) must not carry one
	// or HA renders a slider whose every command is refused.
	position bool
	// setPosition adds the write side. It is a second flag rather than the
	// same one because a garage door reports a virtual 0/50/100 position it
	// cannot be commanded to: it travels fully open or fully shut, and a
	// set_position topic would offer the operator a slider whose intermediate
	// values the drive silently rounds away.
	setPosition bool
	// tilt adds the slat surface, which only a blind has.
	tilt bool
}

// BuildDiscovery implements [hadiscovery.Builder].
//
// The position and tilt surfaces read from the same aggregate the entity's
// state binding names, so they are taken from the component the pipeline
// already filled rather than resolved a second time.
func (e *coverEntity) BuildDiscovery(ctx hadiscovery.Context, comp *hadiscovery.Component) error {
	fields := e.fields
	if e.method != "" {
		comp.CommandTopic = e.MethodTopic(ctx, e.method)
	}
	if e.position {
		fields.PositionOpen = hadiscovery.Ptr(100)
		fields.PositionClosed = hadiscovery.Ptr(0)
		fields.PositionTopic = comp.StateTopic
		fields.PositionTemplate = "{{ value_json.current_position }}"
	}
	if e.setPosition {
		fields.SetPositionTopic = e.MethodTopic(ctx, "set_position")
		fields.SetPositionTemplate = "{{ (value | float / 100) }}"
	}
	if e.tilt {
		// set_tilt is a distinct named action. tilt_opened_value /
		// tilt_closed_value mirror the reference stack (platforms/cover.py).
		fields.TiltStatusTopic = comp.StateTopic
		fields.TiltStatusTemplate = "{{ value_json.current_tilt_position }}"
		fields.TiltCommandTopic = e.MethodTopic(ctx, "set_tilt")
		fields.TiltCommandTemplate = "{{ (value | float / 100) }}"
		fields.TiltMin = hadiscovery.Ptr(0)
		fields.TiltMax = hadiscovery.Ptr(100)
		fields.TiltOpenedValue = hadiscovery.Ptr(100)
		fields.TiltClosedValue = hadiscovery.Ptr(0)
	}
	comp.Fields = fields
	return nil
}

// coverStateFields are the lifecycle tokens every cover on this plane
// reports, whatever drives it.
func coverStateFields() hadiscovery.CoverFields {
	return hadiscovery.CoverFields{
		StateOpen:    "open",
		StateClosed:  "closed",
		StateOpening: "opening",
		StateClosing: "closing",
		StateStopped: "stopped",
	}
}

// HADiscoveryEntity describes the cover on the shared model. Position reads
// come from the aggregate; set_position is a named action, 1:1 with the
// service method of that name.
//
// HA's cover platform publishes payload_open / payload_close / payload_stop to
// one shared command_topic, so that topic is [serviceCoverCommand] — the
// method that multiplexes the three tokens back onto Open / Close / Stop. No
// wire parameter can carry all three: LEVEL cannot express a stop, and STOP is
// a fire-once boolean action that turns an "open" payload into a halt and
// swallows a "close" payload entirely.
//
// Per ADR 0010: method topics for calls that reduce to one domain operation.
func (c *Cover) HADiscoveryEntity() hamodel.Entity {
	if c == nil {
		return nil
	}
	fields := coverStateFields()
	fields.PayloadOpen = commandTokenOpen
	fields.PayloadClose = commandTokenClose
	fields.PayloadStop = commandTokenStop
	return &coverEntity{
		CustomEntity: payload.CustomEntity{
			Basic: hamodel.Basic{
				EntityKey:      c.TopicSlot().Parameter,
				EntityPlatform: hacatalog.PlatformCover,
				Description: hamodel.Description{
					DeviceClass:   hamodel.DeviceClass(VariantString(c.Variant)),
					ValueTemplate: "{{ value_json.state }}",
					Optimistic:    hamodel.Ptr(false),
				},
				Binds: []hamodel.Binding{{
					Role: hamodel.RoleState, Mode: hamodel.Read,
					Slot: payload.CustomSlot(c.TopicSlot()),
				}},
			},
		},
		fields:      fields,
		method:      serviceCoverCommand,
		position:    c.Capabilities.SupportsPosition,
		setPosition: c.Capabilities.SupportsPosition,
	}
}

// HADiscoveryEntity describes a Blind — the cover plus tilt.
//
// The Blind's variant is propagated through Cover.Variant so subtypes like
// VariantShade (HmIP-HDM) surface the correct class. When no explicit variant
// was set (zero value = VariantShutter) a blind always emits "blind": a plain
// Blind is never a shutter.
func (b *Blind) HADiscoveryEntity() hamodel.Entity {
	if b == nil {
		return nil
	}
	entity, ok := b.Cover.HADiscoveryEntity().(*coverEntity)
	if !ok {
		return nil
	}
	variant := b.Variant
	if variant == VariantShutter {
		variant = VariantBlind
	}
	entity.Description.DeviceClass = hamodel.DeviceClass(VariantString(variant))
	entity.tilt = true
	return entity
}

// HADiscoveryEntity describes a Garage door.
//
// HA's cover platform sends payload_open / payload_close / payload_stop to one
// command_topic and no single named action maps to all three, so the command
// binds to the DOOR_COMMAND wire parameter, which accepts exactly those
// tokens. State reads the aggregate's lowercase HA-canonical strings.
//
// The ventilation position deliberately does not appear. HA's MQTT cover
// platform validates the discovery body against a closed key schema and has no
// field for a vent command, so a key invented for it is dropped before any
// entity sees it. A vent-capable drive gets a separate `select` entity
// instead — see Garage.attachDoorMode — which HA renders as a real control and
// can read back.
func (g *Garage) HADiscoveryEntity() hamodel.Entity {
	if g == nil {
		return nil
	}
	fields := coverStateFields()
	fields.PayloadOpen = "OPEN"
	fields.PayloadClose = "CLOSE"
	fields.PayloadStop = "STOP"
	return &coverEntity{
		CustomEntity: payload.CustomEntity{
			Basic: hamodel.Basic{
				EntityKey:      g.TopicSlot().Parameter,
				EntityPlatform: hacatalog.PlatformCover,
				Description: hamodel.Description{
					DeviceClass:   "garage",
					ValueTemplate: "{{ value_json.state }}",
					Optimistic:    hamodel.Ptr(false),
				},
				Binds: []hamodel.Binding{
					{
						Role: hamodel.RoleState, Mode: hamodel.Read,
						Slot: payload.CustomSlot(g.TopicSlot()),
					},
					{
						Role: hamodel.RoleCommand, Mode: hamodel.Write,
						Slot: payload.WireSlot("DOOR_COMMAND"),
					},
				},
			},
		},
		fields: fields,
		// The virtual 0/50/100 position derived from door_state is always
		// reported, and never commanded — see [coverEntity.setPosition].
		position: true,
	}
}
