// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"strconv"
	"strings"
	"time"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guarantee that *Climate satisfies the universal Source
// contract (ADR 0007 step 4) and the HA-Discovery payload builder
// contract (ADR 0010).
var (
	_ payload.Source                   = (*Climate)(nil)
	_ payload.HADiscoveryEntityBuilder = (*Climate)(nil)
)

// The roles a climate entity binds. They are the platform's own field names:
// a composite binds one datapoint per part of itself, and naming the parts
// after the keys they feed is what lets [climateEntity.BuildDiscovery] resolve
// a topic without a second table mapping roles onto keys.
const (
	roleCurrentTemperature = "current_temperature"
	roleTargetTemperature  = "temperature"
	roleCurrentHumidity    = "current_humidity"
)

// climateEntity is a thermostat on the shared model plus the HA climate
// platform's own vocabulary.
//
// Climate declares no `state_topic` and no `command_topic` at all: it names a
// topic per role — mode_state_topic, temperature_command_topic,
// action_topic — which is exactly the case [hadiscovery.Builder] exists for.
// The bindings carry the coordinates and this resolves them, so the topics
// come out of the same layout every other plane renders through.
type climateEntity struct {
	payload.CustomEntity

	// fields is the vocabulary that needs no topic: bounds, unit, and the
	// mode and preset lists.
	fields hadiscovery.ClimateFields
	// presets is non-empty when the thermostat advertises a selectable
	// profile, which is what brings the whole preset surface with it.
	presets []string
	// action is true when the thermostat has an activity source; see
	// [Climate.HasActivitySource].
	action bool
}

// BuildDiscovery implements [hadiscovery.Builder].
func (e *climateEntity) BuildDiscovery(ctx hadiscovery.Context, comp *hadiscovery.Component) error {
	fields := e.fields
	aggregate := e.stateTopic(ctx, hamodel.RoleState)

	// Direct wire values → per-parameter topics with `value_json.value`. Each
	// carries its own envelope from the bridge's slot-state publish, so Home
	// Assistant renders a fresh value as soon as the matching wire DP is
	// observed and a missing DP stays unavailable per field instead of forcing
	// the whole climate card to wait on the slowest constituent.
	fields.CurrentTemperatureTopic = e.stateTopic(ctx, roleCurrentTemperature)
	fields.CurrentTemperatureTemplate = wireValueTemplate
	fields.TemperatureStateTopic = e.stateTopic(ctx, roleTargetTemperature)
	fields.TemperatureStateTemplate = wireValueTemplate
	fields.TemperatureCommandTopic = e.MethodTopic(ctx, "set_temperature")

	// Derived fields → the aggregate, which carries the curated,
	// model-computed document rather than every wire value.
	fields.ModeStateTopic = aggregate
	fields.ModeStateTemplate = "{{ value_json.hvac_mode }}"
	fields.ModeCommandTopic = e.MethodTopic(ctx, "set_mode")

	if len(e.presets) > 0 {
		fields.PresetModes = e.presets
		fields.PresetModeStateTopic = aggregate
		fields.PresetModeValueTemplate = "{{ value_json.preset_mode }}"
		fields.PresetModeCommandTopic = e.MethodTopic(ctx, "set_profile")
	}
	if topic := e.stateTopic(ctx, roleCurrentHumidity); topic != "" {
		fields.CurrentHumidityTopic = topic
		fields.CurrentHumidityTemplate = wireValueTemplate
	}
	// The action surface is only advertised when the thermostat has an
	// activity source. For display-only thermostats the aggregate never
	// carries an `action` key, and HA must not subscribe an action_topic that
	// would render as "unknown" where the reference stack shows no hvac_action
	// at all.
	//
	// A peer-only source (activity wired late via
	// [Climate.RefreshLinkPeerActivitySources]) converges on its own: the first
	// peer push feeds OnActivity, the next channel event rebuilds this payload
	// with the action surface included, and the bridge's diff-gated discovery
	// cache re-publishes the changed bytes.
	if e.action {
		fields.ActionTopic = aggregate
		fields.ActionTemplate = "{{ value_json.action }}"
	}
	comp.Fields = fields

	// The json-attributes pair is [hamodel.Description] vocabulary since
	// go-hamqtt v0.24.0, but its topic is this entity's own aggregate and only
	// the render context can resolve a topic — so it is written here, after
	// the projection that would otherwise have carried it.
	comp.JSONAttributesTopic = aggregate
	comp.JSONAttributesTemplate = climateJSONAttributesTemplate
	return nil
}

// stateTopic resolves one bound role's state topic, or the empty string for a
// role this thermostat does not bind — a humidity reading it has no sensor
// for, say, whose key must then stay off the payload entirely.
func (e *climateEntity) stateTopic(ctx hadiscovery.Context, role string) string {
	b, ok := hamodel.Bind(e, role)
	if !ok {
		return ""
	}
	return ctx.StateTopic(b.Slot)
}

// wireValueTemplate reads the scalar out of a per-parameter state envelope.
const wireValueTemplate = "{{ value_json.value }}"

// HADiscoveryEntity describes the thermostat on the shared model.
//
// **ADR 0011 — per-DP topology:** every wire-backed field names the channel
// and parameter it actually resolved to (see [Config.Group]); on the HmIP and
// classic-RF families that is the custom DP's own channel, on HM-CC-TC it is
// not. Derived fields (hvac_mode, preset_mode, action) come from the custom-DP
// aggregate, which is no longer a sink for every wire value.
//
// Write side: the named actions of ADR 0009 + ADR 0011's
// `…/custom/climate/set/<method>`.
func (c *Climate) HADiscoveryEntity() hamodel.Entity {
	if c == nil {
		return nil
	}
	// HA's temperature_unit expects "C" or "F", not the unit with its degree
	// sign; [Climate.TemperatureUnit] answers "°C" / "°F" by default.
	fields := hadiscovery.ClimateFields{
		TemperatureUnit: strings.TrimPrefix(c.TemperatureUnit(), "°"),
		MinTemp:         hadiscovery.Ptr(c.MinTemp()),
		MaxTemp:         hadiscovery.Ptr(c.MaxTemp()),
		TempStep:        hadiscovery.Ptr(c.TemperatureStep()),
	}
	if modes := c.Modes(); len(modes) > 0 {
		ms := make([]string, len(modes))
		for i, m := range modes {
			ms[i] = string(m)
		}
		fields.Modes = ms
	}

	var presets []string
	if profiles := c.Profiles(); len(profiles) > 0 {
		presets = make([]string, 0, len(profiles))
		for _, p := range profiles {
			// HA's MQTT Climate schema reserves "none" as the implicit unset
			// preset and rejects it as a selectable mode in preset_modes
			// (`preset_modes must not include preset mode 'none'`).
			// Domain-side Profiles() keeps ProfileNone for state reporting;
			// the discovery payload must filter it.
			if p == ProfileNone {
				continue
			}
			presets = append(presets, string(p))
		}
	}

	binds := []hamodel.Binding{
		{
			Role: hamodel.RoleState, Mode: hamodel.Read,
			Slot: payload.CustomSlot(c.TopicSlot()),
		},
		{
			Role: roleCurrentTemperature, Mode: hamodel.Read,
			Slot: payload.WireSlotOn(c.temperatureSlot.ChannelAddress, string(c.temperatureSlot.Parameter)),
		},
		{
			Role: roleTargetTemperature, Mode: hamodel.Read,
			Slot: payload.WireSlotOn(c.setpointSlot.ChannelAddress, string(c.setpointSlot.Parameter)),
		},
	}
	if c.HasHumidity() {
		binds = append(binds, hamodel.Binding{
			Role: roleCurrentHumidity, Mode: hamodel.Read,
			Slot: payload.WireSlotOn(c.humiditySlot.ChannelAddress, string(c.humiditySlot.Parameter)),
		})
	}

	return &climateEntity{
		CustomEntity: payload.CustomEntity{
			Basic: hamodel.Basic{
				EntityKey:      c.TopicSlot().Parameter,
				EntityPlatform: hacatalog.PlatformClimate,
				Description: hamodel.Description{
					// HA derives slider granularity from `temp_step` alone;
					// `precision` is an HA-MQTT-only display-rounding hint with
					// no counterpart in the native integration, which uses
					// _attr_target_temperature_step. Emitting both drifts from
					// the native integration without any behavioural benefit.
					//
					// optimistic=false: HA must not apply setpoint changes
					// locally before the CCU echoes them back; a wrong
					// displayed setpoint during connection issues would
					// mislead the operator.
					Optimistic: hamodel.Ptr(false),
				},
				Binds: binds,
			},
		},
		fields:  fields,
		presets: presets,
		action:  c.HasActivitySource(),
	}
}

// climateJSONAttributesTemplate is the Jinja template HA's MQTT
// integration applies to the aggregated state JSON to produce the
// Climate entity's state_attributes dict. Picks only
// extra_state_attributes keys; entity-property keys (hvac_mode,
// preset_mode, action, state_uncertain) are excluded so they aren't
// surfaced both as primary properties and under "more attributes".
//
// Each key is wrapped in `| default(none)` so HA's Jinja produces
// `null` for missing fields instead of `LoggingUndefined`, which
// otherwise crashes `tojson` with
// `TypeError: Object of type LoggingUndefined is not JSON serializable`.
// The `true` second arg also coerces empty-string / zero values to
// `null` — consistent
// where unset fields are absent rather than zero-valued.
//
// `tojson` round-trips through Jinja's JSON serializer, which handles
// nested dicts (schedule_data) and arrays (available_profiles)
// natively. The climate card omits the row when the value renders as
// null instead of showing the literal "null" string.
const climateJSONAttributesTemplate = `{
  "schedule_data": {{ (value_json.schedule_data | default(none, true)) | tojson }},
  "temperature_offset": {{ (value_json.temperature_offset | default(none, true)) | tojson }},
  "optimum_start_stop": {{ (value_json.optimum_start_stop | default(none, true)) | tojson }},
  "available_profiles": {{ (value_json.available_profiles | default(none, true)) | tojson }},
  "current_schedule_profile": {{ (value_json.current_schedule_profile | default(none, true)) | tojson }},
  "device_active_profile_index": {{ (value_json.device_active_profile_index | default(none, true)) | tojson }},
  "schedule_api_version": {{ (value_json.schedule_api_version | default(none, true)) | tojson }},
  "value_state": {{ ("uncertain" if value_json.state_uncertain else "valid") | tojson }}
}`

// Info returns identity-level fields. Stable across the thermostat's
// lifetime — the Kind, Capabilities, and Address pin the thermostat
// shape that downstream UIs render.
func (c *Climate) Info() payload.InfoPayload {
	if c == nil {
		return nil
	}
	return &payload.ClimateInfo{
		Address:   c.Address,
		Key:       c.key.String(),
		Kind:      kindName(c.Kind),
		Category:  "climate",
		SubDPKeys: c.SubDataPointKeysAsStrings(),
	}
}

// kindName maps the internal Kind enum to a wire-stable string label.
func kindName(k Kind) string {
	switch k {
	case KindSimpleRF:
		return "simple_rf"
	case KindRF:
		return "rf"
	case KindIP:
		return "ip"
	}
	return "unknown"
}

// SubDataPointKeysAsStrings returns the wire identifiers of every
// wire-level slot as plain strings. Convenience wrapper for the
// payload method — keeps the JSON shape stable (string list) without
// leaking the hmtypes.DataPointKey type to consumers.
func (c *Climate) SubDataPointKeysAsStrings() []string {
	keys := c.SubDataPointKeys()
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.String()
	}
	return out
}

// Config returns the configuration-level fields a UI adapter needs to
// lay out the climate widget: capability flags, the resolved temperature
// bounds, the step, and the unit.
//
// The preset list always exposes every available week-program slot
// (in addition to BOOST / COMFORT / ECO) regardless of the current
// mode. Selecting a week_program_* preset implicitly switches the
// thermostat to AUTO on the CCU, so the SPA Tile must be able to
// offer the slot even while the device is currently in MANU/OFF.
// The mode-aware filter in [Climate.Profiles] (used by the MQTT
// discovery topic) stays untouched.
func (c *Climate) Config() payload.ConfigPayload {
	if c == nil {
		return nil
	}
	out := &payload.ClimateConfig{
		MinTemp:         c.MinTemp(),
		MaxTemp:         c.MaxTemp(),
		TempStep:        c.TemperatureStep(),
		TemperatureUnit: c.TemperatureUnit(),
	}
	if modes := c.Modes(); len(modes) > 0 {
		ms := make([]string, len(modes))
		for i, m := range modes {
			ms[i] = string(m)
		}
		out.HVACModes = ms
	}
	if c.Capabilities.SupportsProfile {
		ps := make([]string, 0, 9)
		if c.Capabilities.SupportsBoost {
			ps = append(ps, string(ProfileBoost))
		}
		if c.Capabilities.SupportsComfort {
			ps = append(ps, string(ProfileComfort))
		}
		if c.Capabilities.SupportsEco {
			ps = append(ps, string(ProfileEco))
		}
		count := c.numWeekPrograms()
		programs := []Profile{
			ProfileWeekProgram1,
			ProfileWeekProgram2,
			ProfileWeekProgram3,
			ProfileWeekProgram4,
			ProfileWeekProgram5,
			ProfileWeekProgram6,
		}
		if count > len(programs) {
			count = len(programs)
		}
		for _, p := range programs[:count] {
			ps = append(ps, string(p))
		}
		if len(ps) > 0 {
			out.PresetModes = ps
		}
	}
	return out
}

// State returns the derived thermostat state — fields that cannot be
// sourced from a single per-DP topic. Direct wire values
// (current_temperature, target_temperature, current_humidity,
// temperature_offset) are NOT duplicated here; the discovery references
// the per-DP slot topics for those.
//
// Per ADR 0011 — the aggregate state topic carries only:
//
// - hvac_mode ← Mode() (computed from setpoint/control_mode) - preset_mode ←
// Profile() (computed, mode-aware) - action ← Activity()
// (heating/cooling/idle aggregate) - state_uncertain (diagnostic; pings any
// optimistic-update window)
//
// **Bootstrap defaults**: every key is ALWAYS present in the payload.
// Pre-observation values use safe fallbacks (`hvac_mode="off"`,
// `preset_mode="none"`, `action="idle"`) rather than omission. HA's MQTT
// Climate platform reads `value_json.<field>` via templates; missing keys
// render to empty strings which HA either ignores (current state preserved,
// never set) or maps to `unknown`. Either way the climate card stays unbound
// until the first push event. Stamping defaults gives HA a coherent initial
// state instantly and keeps the card interactive even before the CCU has
// reported CONTROL_MODE.
func (c *Climate) State() payload.StatePayload {
	if c == nil {
		return nil
	}
	out := &payload.ClimateState{
		StateUncertain: c.StateUncertain(),
	}
	currentMode := ModeOff
	if m, ok := c.Mode(); ok {
		currentMode = m
		out.HVACMode = string(m)
	} else {
		// Default to ModeOff — the safest assumption for a thermostat
		// that has not yet reported CONTROL_MODE. HA renders the card
		// as "off"; the user can still issue commands via
		// mode_command_topic which the CCU answers.
		out.HVACMode = string(ModeOff)
	}
	if p, ok := c.Profile(); ok {
		out.PresetMode = string(p)
	} else {
		out.PresetMode = string(ProfileNone)
	}
	// The action field is only stamped when the thermostat actually has
	// an activity source (LEVEL / STATE / VALVE_STATE / linked peers).
	// Display-only thermostats (HmIP-STHD) carry none — the reference
	// stack reports `hvac_action = None` for them, so the aggregate
	// state omits the key instead of inventing "idle".
	if c.HasActivitySource() {
		// When the thermostat is in OFF mode the action must be "off", not
		// "idle". The internal activity field is not updated when the user
		// switches off the thermostat (the CCU only pushes VALVE_STATE /
		// LEVEL), so we must override it here based on the current mode.
		if currentMode == ModeOff {
			out.Action = string(ActivityOff)
		} else if a, ok := c.Activity(); ok {
			out.Action = string(a)
		} else {
			// "idle" matches HA's MQTT Climate `action_topic` schema and
			// renders the card as not-currently-heating/cooling until the
			// first source value arrives.
			out.Action = string(ActivityIdle)
		}
	}

	// Measurement fields from the channel's field DPs — observed-only
	// (omitted until the CCU reported them), so a climate card can be
	// populated from the aggregate state alone.
	if v, ok := c.CurrentTemperature(); ok {
		out.CurrentTemperature = &v
	}
	if v, ok := c.Setpoint(); ok {
		out.SetTemperature = &v
	}
	if v, ok := c.Humidity(); ok {
		out.CurrentHumidity = &v
	}

	// State attributes — mirror extra_state_attributes for HA-native
	// parity. HA's MQTT Climate platform exposes them via
	// `json_attributes_topic` + `json_attributes_template` (see
	// HADiscoveryPayload). Each is only set when the underlying CCU
	// value is observed; nil/zero-value fields are omitted by omitempty.
	if v, ok := c.TemperatureOffset(); ok {
		out.TemperatureOffset = &v
	}
	if v, ok := c.OptimumStartStop(); ok {
		out.OptimumStartStop = v
	}
	// Schedule-profile metadata from the channel-attached week-profile
	// DP. When no week-profile is attached (SimpleRF, edge cases),
	// these fields stay at their zero values and are omitted.
	if c.channelRef != nil {
		if wp := c.channelRef.WeekProfile(); wp != nil {
			if profiles := wp.AvailableProfiles(); len(profiles) > 0 {
				ap := make([]any, len(profiles))
				for i, p := range profiles {
					ap[i] = p
				}
				out.AvailableProfiles = ap
			}
			if cur := wp.CurrentProfile(); cur != "" {
				out.CurrentScheduleProfile = cur
				if idx, err := strconv.Atoi(strings.TrimPrefix(cur, "P")); err == nil {
					out.DeviceActiveProfileIndex = &idx
				}
			}
			out.ScheduleAPIVersion = "v2.0"
			if sched := buildScheduleData(wp, wp.CurrentProfile()); sched != nil {
				out.ScheduleData = sched
			}
		}
	}
	// Two fields deliberately absent from the state payload, both because
	// they belong to a different layer: the address is identity, carried
	// by the info payload and the discovery `device.identifiers`; and
	// ValueState is a pure restatement of StateUncertain, which the
	// discovery `json_attributes_template` derives from
	// value_json.state_uncertain. Repeating either on the state topic
	// would put the same fact on the wire twice, with two chances to
	// disagree.
	return out
}

// buildScheduleData renders the week-profile's currently-active
// ClimateProfile (selected by `currentProfileKey` — typically "P1")
// As the per-day map
//
//	{
//	 "MONDAY": {"base_temperature": 19, "periods": [{...}]},
//	 "TUESDAY": {"base_temperature": 19, "periods": []},
//	 ...
//	}
//
// Returns nil when no climate schedule has been loaded yet, or the
// current-profile slot has not been populated. The CCU schedule
// fetch is asynchronous; the StatePayload caller skips the key in
// that case.
func buildScheduleData(wp *weekprofile.ProfileDataPoint, currentProfileKey string) map[string]any {
	if wp == nil {
		return nil
	}
	cp := wp.Climate()
	if cp == nil {
		return nil
	}
	current, err := cp.Current()
	if err != nil || current == nil || len(current.Profiles) == 0 {
		return nil
	}
	profile, ok := current.Profiles[currentProfileKey]
	if !ok || profile == nil {
		return nil
	}
	out := make(map[string]any, len(profile.Days))
	for day, weekday := range profile.Days {
		periods := make([]map[string]any, 0, len(weekday.Periods))
		for _, p := range weekday.Periods {
			periods = append(periods, map[string]any{
				"starttime":   p.StartTime,
				"endtime":     p.EndTime,
				"temperature": p.Temperature,
			})
		}
		out[string(day)] = map[string]any{
			"base_temperature": weekday.BaseTemperature,
			"periods":          periods,
		}
	}
	return out
}

// registerServices wires the Climate Set* methods onto the embedded
// ServiceRegistry. Service-method names mirror
// service_method_names for thermostat custom DPs (see
// Set_temperature, set_mode
// set_profile, enable_away_*).
func (c *Climate) registerServices() {
	c.RegisterServiceWithArg("set_temperature", "temperature", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := payload.ParamFloat64(params, "temperature")
		if err != nil {
			return err
		}
		return c.SetTemperature(ctx, v, priority)
	})
	c.RegisterServiceWithArg("set_mode", "mode", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		s, err := payload.ParamString(params, "mode")
		if err != nil {
			return err
		}
		return c.SetMode(ctx, Mode(s), priority)
	})
	c.RegisterServiceWithArg("set_profile", "profile", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		s, err := payload.ParamString(params, "profile")
		if err != nil {
			return err
		}
		return c.SetProfile(ctx, Profile(s), priority)
	})
	c.RegisterServiceWithArg("set_temperature_offset", "offset", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := payload.ParamString(params, "offset")
		if err != nil {
			return err
		}
		return c.SetTemperatureOffset(ctx, v, priority)
	})
	c.RegisterService("enable_boost", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return c.EnableBoost(ctx, priority)
	})
	c.RegisterService("disable_boost", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return c.DisableBoost(ctx, priority)
	})
	c.RegisterService("set_away", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		untilStr, err := payload.ParamString(params, "until")
		if err != nil {
			return err
		}
		until, err := time.Parse(time.RFC3339, untilStr)
		if err != nil {
			return err
		}
		away, err := payload.ParamFloat64(params, "away_temperature")
		if err != nil {
			return err
		}
		return c.SetAway(ctx, until, away, priority)
	})
	c.RegisterService("set_away_for_duration", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		hours, err := payload.ParamFloat64(params, "hours")
		if err != nil {
			return err
		}
		away, err := payload.ParamFloat64(params, "away_temperature")
		if err != nil {
			return err
		}
		return c.SetAwayForDuration(ctx, time.Duration(hours*float64(time.Hour)), away, priority)
	})
	c.RegisterService("disable_away", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return c.DisableAway(ctx, priority)
	})
}
