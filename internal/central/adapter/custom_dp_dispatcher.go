// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package adapter provides the concrete CustomDPDispatcher that bridges
// the abstract (device address, name, operation, params) tuple from the
// REST/WS layer to actual Custom-DP model method calls.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CustomDPDispatcher implements [interfaces.CustomDPWriter]. It resolves
// the target device and custom DP from the central registry, type-asserts
// to the concrete model type, parses the operation params, and invokes
// the matching method. Audit entries are emitted on every successful
// invocation.
//
// Type resolution order in [dispatch]:
//  1. Light subtypes (most-specific first: RGBWLight, EffectLight,
//     ColorTempLight, FixedColorLight, ColorLight, then plain Light).
//  2. Climate.
//  3. Cover subtypes (Blind before Cover).
//  4. Lock.
//  5. Siren.
//  6. TextDisplay.
//  7. Valve subtypes (Modulating, Irrigation).
//  8. Switch.
type CustomDPDispatcher struct {
	registry *central.Registry
	audit    audit.Recorder
}

// NewCustomDPDispatcher constructs a dispatcher backed by reg.
// Audit logging defaults to a no-op; call [SetAuditRecorder] to wire
// the production buffer.
func NewCustomDPDispatcher(reg *central.Registry) *CustomDPDispatcher {
	return &CustomDPDispatcher{
		registry: reg,
		audit:    audit.NoopRecorder(),
	}
}

// SetAuditRecorder wires an audit recorder. Passing nil reverts to the
// no-op. Returns the receiver for call-site chaining.
func (d *CustomDPDispatcher) SetAuditRecorder(rec audit.Recorder) *CustomDPDispatcher {
	if rec == nil {
		rec = audit.NoopRecorder()
	}
	d.audit = rec
	return d
}

// InvokeCustomDP implements [interfaces.CustomDPWriter].
//
// Resolution order:
//  1. Walk the central registry to find the device by address.
//  2. Walk the device's channels to find the custom DP whose
//     DataPointKey().Parameter matches name.
//  3. Type-switch on the concrete DP type and dispatch to the matching
//     per-category helper.
//  4. On success emit an audit entry tagged with source.
//
// Returns [hmapi.ErrUnknownOperation] when the operation string is
// not in the dispatch table for the DP category, and
// [hmapi.ErrBadParam] when a required parameter is absent or has the
// wrong type. Device/DP not-found returns a plain error (maps to 502 in
// the handler layer — the device should be present if the caller
// constructed a valid URL from the list endpoint).
func (d *CustomDPDispatcher) InvokeCustomDP(
	ctx context.Context,
	deviceAddress, name, operation string,
	params map[string]any,
	priority hmenum.CommandPriority,
	source string,
) error {
	// CommandPriorityCritical == 0 is a valid priority per CLAUDE.md rules.
	// The REST handler (parsePriority) maps "" → CommandPriorityHigh before
	// calling here. We pass priority through unchanged to model methods.

	dp, chNo, err := d.resolveCustomDP(deviceAddress, name)
	if err != nil {
		return err
	}

	if err := d.dispatch(ctx, dp, operation, params, priority); err != nil {
		return err
	}

	d.recordAudit(deviceAddress, chNo, name, operation, source, params)
	return nil
}

// resolveCustomDP walks the central registry to find the named custom
// data point on the device at deviceAddress.
func (d *CustomDPDispatcher) resolveCustomDP(deviceAddress, name string) (device.AttachableDataPoint, int, error) {
	if d.registry == nil {
		return nil, 0, errors.New("custom_dp: registry not configured")
	}
	for _, u := range d.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		// Accepts the bare parameter name and the channel-exact
		// `PARAM@<channel>` wire form (profile channel groups).
		if dp, chNo, found := custom.FindByWireName(dev, name); found {
			return dp, chNo, nil
		}
		// Device found but DP not found.
		return nil, 0, fmt.Errorf("custom_dp: data point %q not found on device %s", name, deviceAddress)
	}
	return nil, 0, fmt.Errorf("custom_dp: device %s not found", deviceAddress)
}

// dispatch type-asserts dp to its concrete model type and routes
// operation to the matching per-category handler.
//
// The type hierarchy for lights is:
//
//	RGBWLight → ColorLight → Light
//	EffectLight → ColorLight → Light
//	ColorTempLight → Light
//	FixedColorLight → Light
//	ColorLight → Light
//
// More specific types must be matched first because Go's type switches
// match only the declared receiver type, not embedded fields.
func (d *CustomDPDispatcher) dispatch(
	ctx context.Context,
	dp device.AttachableDataPoint,
	operation string,
	params map[string]any,
	priority hmenum.CommandPriority,
) error {
	// --- Light family (most-specific first) ---
	if l, ok := dp.(*light.RGBWLight); ok {
		return d.dispatchRGBWLight(ctx, l, operation, params, priority)
	}
	if l, ok := dp.(*light.EffectLight); ok {
		return d.dispatchEffectLight(ctx, l, operation, params, priority)
	}
	if l, ok := dp.(*light.ColorTempLight); ok {
		return d.dispatchColorTempLight(ctx, l, operation, params, priority)
	}
	// DRGDaliLight (HmIP-DRG-DALI) composes *ColorTempLight and adds an
	// optional EFFECT surface; it is a distinct concrete type, so the
	// ColorTempLight case above never matches it. Without this case every
	// turn_on/off/brightness/color_temp/effect command errored as an
	// unsupported type even though the profile is advertised as controllable.
	if l, ok := dp.(*light.DRGDaliLight); ok {
		return d.dispatchDRGDaliLight(ctx, l, operation, params, priority)
	}
	if l, ok := dp.(*light.FixedColorLight); ok {
		return d.dispatchFixedColorLight(ctx, l, operation, params, priority)
	}
	// SoundPlayerLED (HmIP-MP3P status LED) composes *FixedColorLight; like
	// DRGDaliLight it is a distinct type the FixedColorLight case never
	// matches. Its on/off semantics (COLOR + ON_TIME_LIST_1 + REPETITIONS +
	// ON_TIME written atomically instead of LEVEL alone) live on its own
	// ServiceRegistry registrations, which the turn_on / turn_off /
	// set_level delegation in dispatchLight resolves, so the fixed-colour
	// path is the right route for every operation.
	if l, ok := dp.(*light.SoundPlayerLED); ok {
		return d.dispatchFixedColorLight(ctx, l.FixedColorLight, operation, params, priority)
	}
	if l, ok := dp.(*light.ColorLight); ok {
		return d.dispatchColorLight(ctx, l, operation, params, priority)
	}
	if l, ok := dp.(*light.Light); ok {
		return d.dispatchLight(ctx, l, operation, params, priority)
	}

	// --- Climate ---
	// `*climate.Climate` implements `device.AttachableDataPoint`
	// directly (only `DataPointKey()` is required); production
	// always passes the concrete pointer. Tests sometimes wrap it in
	// a `climateCarrier` shim so the wire DP can be a fake while
	// still exposing a real *Climate to the dispatch path.
	if c, ok := dp.(*climate.Climate); ok {
		return d.dispatchClimate(ctx, c, operation, params, priority)
	}
	if c, ok := dp.(climateCarrier); ok {
		return d.dispatchClimate(ctx, c.ClimateDP(), operation, params, priority)
	}

	// --- Cover family (Blind / Garage before Cover — both embed Cover
	// for some Methods but are concrete types themselves) ---
	if b, ok := dp.(*cover.Blind); ok {
		return d.dispatchBlind(ctx, b, operation, params, priority)
	}
	if g, ok := dp.(*cover.Garage); ok {
		return d.dispatchGarage(ctx, g, operation, params, priority)
	}
	if c, ok := dp.(*cover.Cover); ok {
		return d.dispatchCover(ctx, c, operation, params, priority)
	}

	// --- Lock ---
	if l, ok := dp.(*lock.Lock); ok {
		return d.dispatchLock(ctx, l, operation, params, priority)
	}
	if l, ok := dp.(lockCarrier); ok {
		return d.dispatchLock(ctx, l.LockDP(), operation, params, priority)
	}

	// --- Siren ---
	if s, ok := dp.(*siren.Siren); ok {
		return d.dispatchSiren(ctx, s, operation, params, priority)
	}
	if s, ok := dp.(sirenCarrier); ok {
		return d.dispatchSiren(ctx, s.SirenDP(), operation, params, priority)
	}

	// --- TextDisplay ---
	if t, ok := dp.(*textdisplay.TextDisplay); ok {
		return d.dispatchTextDisplay(ctx, t, operation, params, priority)
	}
	if t, ok := dp.(textDisplayCarrier); ok {
		return d.dispatchTextDisplay(ctx, t.TextDisplayDP(), operation, params, priority)
	}

	// --- Valve family ---
	if m, ok := dp.(*valve.Modulating); ok {
		return d.dispatchModulatingValve(ctx, m, operation, params, priority)
	}
	if iv, ok := dp.(*valve.Irrigation); ok {
		return d.dispatchIrrigation(ctx, iv, operation, params, priority)
	}

	// --- Switch ---
	if s, ok := dp.(*switchdev.Switch); ok {
		return d.dispatchSwitch(ctx, s, operation, params, priority)
	}
	if ap, ok := dp.(*switchdev.AccessPermission); ok {
		return d.dispatchAccessPermission(ctx, ap, operation, priority)
	}

	return fmt.Errorf("custom_dp: unsupported data point type %T", dp)
}

// --- Light dispatchers ---

func (d *CustomDPDispatcher) dispatchLight(
	ctx context.Context, l *light.Light, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	// turn_on / turn_off / set_level are defined once, on the light model's
	// ServiceRegistry, and are reached from here through Invoke. The
	// registry is shared by the whole embedded chain, so the concrete
	// subtype's own registration wins: a SoundPlayerLED's atomic
	// COLOR + ON_TIME_LIST_1 write, a ColorLight's HUE/SATURATION routing.
	// Re-implementing the HA JSON-schema ladder here is what let this plane
	// drop colour, colour temperature and effect while the MQTT command
	// topic applied them.
	case "turn_on", "turn_off", "set_level":
		// A light whose LEVEL parameter did not resolve registers no
		// service methods at all, so the operation genuinely does not
		// exist for this data point.
		if !slices.Contains(l.ServiceMethodNames(), op) {
			return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
		}
		return wrapServiceErr(l.Invoke(ctx, op, p, prio))
	case "set_brightness":
		level, err := paramFloat(p, "brightness", 1)
		if err != nil {
			return err
		}
		return l.SetLevel(ctx, level, prio)
	case "set_on_time":
		// Encodes ON_TIME_VALUE / ON_TIME_UNIT for the next on cycle. The
		// Light carries the writer + channel address it writes through.
		dur, err := requireOnTime(p)
		if err != nil {
			return err
		}
		return l.SetOnTime(ctx, l.Writer, l.Address(), dur, prio)
	case "set_color", "set_color_temperature", "set_effect":
		// Valid for the category but not for plain Light.
		return fmt.Errorf("%w: operation %q not supported by plain Light", hmapi.ErrUnknownOperation, op)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

func (d *CustomDPDispatcher) dispatchColorLight(
	ctx context.Context, l *light.ColorLight, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "set_color":
		hue, err := paramInt32(p, "hue")
		if err != nil {
			return err
		}
		// saturation is HA-canonical 0..100 (default full); SetColor scales
		// it to the wire's 0..1 SATURATION DP.
		sat, err := paramFloat(p, "saturation", 100)
		if err != nil {
			return err
		}
		return l.SetColor(ctx, hue, sat, prio)
	case "set_color_temperature", "set_effect":
		return fmt.Errorf("%w: operation %q not supported by ColorLight", hmapi.ErrUnknownOperation, op)
	default:
		return d.dispatchLight(ctx, l.Light, op, p, prio)
	}
}

func (d *CustomDPDispatcher) dispatchColorTempLight(
	ctx context.Context, l *light.ColorTempLight, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "set_color_temperature":
		kelvin, err := paramInt32(p, "kelvin")
		if err != nil {
			return err
		}
		return l.SetKelvin(ctx, kelvin, prio)
	case "set_color", "set_effect":
		return fmt.Errorf("%w: operation %q not supported by ColorTempLight", hmapi.ErrUnknownOperation, op)
	default:
		return d.dispatchLight(ctx, l.Light, op, p, prio)
	}
}

// dispatchDRGDaliLight routes the DALI light: set_effect goes to its own
// optional EFFECT surface (a no-op on fixtures without one), and every other
// operation — turn_on/off, brightness, color_temp — delegates to the embedded
// ColorTempLight path, exactly as the sibling light types are dispatched.
func (d *CustomDPDispatcher) dispatchDRGDaliLight(
	ctx context.Context, l *light.DRGDaliLight, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	if op == "set_effect" {
		label, ok := paramStringOptional(p, "label")
		if !ok {
			return fmt.Errorf("%w: set_effect requires a string label", hmapi.ErrBadParam)
		}
		return l.SetEffect(ctx, label, prio)
	}
	return d.dispatchColorTempLight(ctx, l.ColorTempLight, op, p, prio)
}

func (d *CustomDPDispatcher) dispatchFixedColorLight(
	ctx context.Context, l *light.FixedColorLight, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "set_color":
		// Accepts a "label" (the COLOR enum name), a "slot" int, or a
		// "hue"+"saturation" pair. A caller holding only the CCU descriptor
		// must use "label": the descriptor's value list is ordered by the RGB
		// bit pattern, so its index is not a [light.FixedColor].
		if labelRaw, ok := p["label"]; ok {
			label, ok2 := labelRaw.(string)
			if !ok2 {
				return fmt.Errorf("%w: label must be a string", hmapi.ErrBadParam)
			}
			return l.SetColorByName(ctx, label, prio)
		}
		if slotRaw, ok := p["slot"]; ok {
			slot, err := toInt32(slotRaw)
			if err != nil {
				return fmt.Errorf("%w: slot: %w", hmapi.ErrBadParam, err)
			}
			return l.SetColor(ctx, light.FixedColor(slot), prio)
		}
		hue, err := paramInt32(p, "hue")
		if err != nil {
			return err
		}
		sat, err := paramFloat(p, "saturation", 100)
		if err != nil {
			return err
		}
		fc := light.HSToFixedColor(hue, sat)
		return l.SetColor(ctx, fc, prio)
	case "set_color_temperature", "set_effect":
		return fmt.Errorf("%w: operation %q not supported by FixedColorLight", hmapi.ErrUnknownOperation, op)
	default:
		return d.dispatchLight(ctx, l.Light, op, p, prio)
	}
}

func (d *CustomDPDispatcher) dispatchEffectLight(
	ctx context.Context, l *light.EffectLight, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "set_effect":
		// Accept either "index" (int) or "label" (string).
		if labelRaw, ok := p["label"]; ok {
			label, ok2 := labelRaw.(string)
			if !ok2 {
				return fmt.Errorf("%w: label must be a string", hmapi.ErrBadParam)
			}
			return l.SetEffectByLabel(ctx, label, prio)
		}
		idx, err := paramInt32(p, "index")
		if err != nil {
			return err
		}
		return l.SetEffect(ctx, idx, prio)
	case "set_color":
		hue, err := paramInt32(p, "hue")
		if err != nil {
			return err
		}
		sat, err := paramFloat(p, "saturation", 100)
		if err != nil {
			return err
		}
		return l.SetColor(ctx, hue, sat, prio)
	case "set_color_temperature":
		return fmt.Errorf("%w: operation %q not supported by EffectLight", hmapi.ErrUnknownOperation, op)
	default:
		return d.dispatchLight(ctx, l.Light, op, p, prio)
	}
}

func (d *CustomDPDispatcher) dispatchRGBWLight(
	ctx context.Context, l *light.RGBWLight, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "set_color":
		hue, err := paramInt32(p, "hue")
		if err != nil {
			return err
		}
		sat, err := paramFloat(p, "saturation", 100)
		if err != nil {
			return err
		}
		return l.SetColor(ctx, hue, sat, prio)
	case "set_color_temperature":
		kelvin, err := paramInt32(p, "kelvin")
		if err != nil {
			return err
		}
		return l.SetKelvin(ctx, kelvin, prio)
	case "set_effect":
		if labelRaw, ok := p["label"]; ok {
			label, ok2 := labelRaw.(string)
			if !ok2 {
				return fmt.Errorf("%w: label must be a string", hmapi.ErrBadParam)
			}
			if err := l.SetEffect(ctx, label, prio); err != nil {
				return fmt.Errorf("%w: %w", hmapi.ErrBadParam, err)
			}
			return nil
		}
		// Index-based fallback: look up the label at the given index so the
		// wire always receives the string label the CCU expects.
		idx, err := paramInt32(p, "index")
		if err != nil {
			return err
		}
		effects := l.Effects()
		if idx < 0 || int(idx) >= len(effects) {
			return fmt.Errorf("%w: effect index %d out of range", hmapi.ErrBadParam, idx)
		}
		return l.SetEffect(ctx, effects[idx], prio)
	default:
		return d.dispatchLight(ctx, l.Light, op, p, prio)
	}
}

// --- Climate dispatcher ---

func (d *CustomDPDispatcher) dispatchClimate(
	ctx context.Context, c *climate.Climate, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "set_temperature":
		temp, err := paramFloat(p, "temperature", 0)
		if err != nil {
			return err
		}
		return c.SetTemperature(ctx, temp, prio)
	case "enable_boost":
		return c.EnableBoost(ctx, prio)
	case "disable_boost":
		return c.DisableBoost(ctx, prio)
	case "set_mode":
		modeStr, err := paramString(p, "mode")
		if err != nil {
			return err
		}
		return c.SetMode(ctx, climate.Mode(modeStr), prio)
	case "set_profile":
		profileStr, err := paramString(p, "profile")
		if err != nil {
			return err
		}
		return c.SetProfile(ctx, climate.Profile(profileStr), prio)
	case "enable_away":
		// Params: "until" (RFC3339 string or "+<duration>") and optional "temperature".
		until, err := paramTime(p, "until")
		if err != nil {
			return err
		}
		awayTemp := 0.0
		if _, ok := p["temperature"]; ok {
			if awayTemp, err = paramFloat(p, "temperature", 0); err != nil {
				return err
			}
		}
		return c.SetAway(ctx, until, awayTemp, prio)
	case "enable_away_by_calendar":
		// Params: "end" (RFC3339 string or "+<duration>") and "away_temperature".
		until, err := paramTime(p, "end")
		if err != nil {
			return err
		}
		awayTemp, err := paramFloat(p, "away_temperature", 0)
		if err != nil {
			return err
		}
		return c.SetAway(ctx, until, awayTemp, prio)
	case "enable_away_by_duration":
		// Params: a duration ("hours" or "duration_seconds") and "away_temperature".
		dur, err := awayDuration(p)
		if err != nil {
			return err
		}
		awayTemp, err := paramFloat(p, "away_temperature", 0)
		if err != nil {
			return err
		}
		return c.SetAwayForDuration(ctx, dur, awayTemp, prio)
	case "disable_away":
		return c.DisableAway(ctx, prio)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

// --- Cover dispatchers ---

func (d *CustomDPDispatcher) dispatchCover(
	ctx context.Context, c *cover.Cover, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "open":
		return c.Open(ctx, prio)
	case "close":
		return c.Close(ctx, prio)
	case "set_position":
		pos, err := paramFloat(p, "position", 1)
		if err != nil {
			return err
		}
		return c.SetPosition(ctx, pos, prio)
	case "stop":
		return c.Stop(ctx, prio)
	case "set_tilt":
		return fmt.Errorf("%w: set_tilt requires a Blind, not a plain Cover", hmapi.ErrUnknownOperation)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

// dispatchGarage handles `*cover.Garage` operations (open / close /
// stop / ventilate). The garage drive doesn't expose a LEVEL slider,
// so `set_position` returns ErrUnknownOperation.
func (d *CustomDPDispatcher) dispatchGarage(
	ctx context.Context, g *cover.Garage, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	_ = p
	switch op {
	case "open":
		return g.Open(ctx, prio)
	case "close":
		return g.Close(ctx, prio)
	case "stop":
		return g.Stop(ctx, prio)
	case "ventilate":
		return g.Vent(ctx, prio)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

func (d *CustomDPDispatcher) dispatchBlind(
	ctx context.Context, b *cover.Blind, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "open":
		return b.Open(ctx, prio)
	case "close":
		return b.Close(ctx, prio)
	case "set_position":
		pos, err := paramFloat(p, "position", 1)
		if err != nil {
			return err
		}
		return b.SetPosition(ctx, pos, prio)
	case "stop":
		return b.Stop(ctx, prio)
	case "set_tilt":
		tilt, err := paramFloat(p, "tilt", 1)
		if err != nil {
			return err
		}
		return b.SetTilt(ctx, tilt, prio)
	case "set_combined":
		// Drives both axes in a single CCU paramset write. level and tilt
		// are 0..1 floats.
		level, err := paramFloat(p, "level", 1)
		if err != nil {
			return err
		}
		tilt, err := paramFloat(p, "tilt", 1)
		if err != nil {
			return err
		}
		return b.SetCombined(ctx, level, tilt, prio)
	case "open_tilt":
		return b.OpenTilt(ctx, prio)
	case "close_tilt":
		return b.CloseTilt(ctx, prio)
	case "stop_tilt":
		return b.StopTilt(ctx, prio)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

// --- Lock dispatcher ---

func (d *CustomDPDispatcher) dispatchLock(
	ctx context.Context, l *lock.Lock, op string, _ map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "lock":
		return l.Lock(ctx, prio)
	case "unlock":
		return l.Unlock(ctx, prio)
	case "open":
		return l.Open(ctx, prio)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

// --- Siren dispatcher ---

func (d *CustomDPDispatcher) dispatchSiren(
	ctx context.Context, s *siren.Siren, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "turn_on":
		cfg := siren.OnConfig{}
		// The siren's own reader, shared with its service handler so the two
		// planes cannot disagree about the same key again — this branch used
		// to read a bare number as milliseconds while the handler read
		// seconds, and it accepted neither the canonical "seconds" key nor
		// the shared helper every other timed operation here uses.
		dur, ok, err := siren.ParseOnDuration(p)
		if err != nil {
			return fmt.Errorf("%w: %w", hmapi.ErrBadParam, err)
		}
		if ok {
			cfg.Duration = dur
		}
		if acoustic, ok := p["acoustic"]; ok {
			v, err := toString(acoustic)
			if err != nil {
				return fmt.Errorf("%w: acoustic: %w", hmapi.ErrBadParam, err)
			}
			cfg.AcousticSelection = &v
		}
		if optical, ok := p["optical"]; ok {
			v, err := toString(optical)
			if err != nil {
				return fmt.Errorf("%w: optical: %w", hmapi.ErrBadParam, err)
			}
			cfg.OpticalSelection = &v
		}
		return s.TurnOn(ctx, cfg, prio)
	case "turn_off", "stop":
		return s.TurnOff(ctx, prio)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

// --- TextDisplay dispatcher ---

func (d *CustomDPDispatcher) dispatchTextDisplay(
	ctx context.Context, t *textdisplay.TextDisplay, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "write", "send_text":
		row, err := extractRow(p)
		if err != nil {
			return err
		}
		// If sound options are present, use WriteWithSound.
		if _, hasSound := p["sound"]; hasSound {
			opts, err := extractSoundOptions(p)
			if err != nil {
				return err
			}
			return t.WriteWithSound(ctx, row, opts, prio)
		}
		return t.Write(ctx, row, prio)
	case "clear", "clear_text":
		id, err := paramInt32(p, "id")
		if err != nil {
			return err
		}
		return t.Clear(ctx, id, prio)
	case "commit":
		// Flushes a previously-prepared row to the physical display.
		return t.Commit(ctx, prio)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

// --- Valve dispatchers ---

func (d *CustomDPDispatcher) dispatchIrrigation(
	ctx context.Context, v *valve.Irrigation, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "open":
		// Optional timed open: with no duration the valve opens indefinitely.
		dur, _, err := onTimeParam(p)
		if err != nil {
			return err
		}
		return v.Open(ctx, dur, prio)
	case "set_on_time":
		// Timed open: ON_TIME + STATE are bundled into one atomic
		// put_paramset. A duration is required here (unlike "open").
		dur, err := requireOnTime(p)
		if err != nil {
			return err
		}
		return v.Open(ctx, dur, prio)
	case "close":
		return v.Close(ctx, prio)
	case "set_level":
		return fmt.Errorf("%w: set_level not supported by Irrigation valve", hmapi.ErrUnknownOperation)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

func (d *CustomDPDispatcher) dispatchModulatingValve(
	ctx context.Context, v *valve.Modulating, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "set_level":
		level, err := paramFloat(p, "level", 1)
		if err != nil {
			return err
		}
		return v.SetLevel(ctx, level, prio)
	case "open":
		return v.SetLevel(ctx, 1.0, prio)
	case "close":
		return v.SetLevel(ctx, 0.0, prio)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

// --- Switch dispatcher ---

func (d *CustomDPDispatcher) dispatchSwitch(
	ctx context.Context, s *switchdev.Switch, op string, p map[string]any, prio hmenum.CommandPriority,
) error {
	switch op {
	case "turn_on":
		return s.Set(ctx, true, prio)
	case "turn_off":
		return s.Set(ctx, false, prio)
	case "turn_on_for", "set_on_time":
		dur, err := requireOnTime(p)
		if err != nil {
			return err
		}
		return s.TurnOnFor(ctx, dur, prio)
	case "toggle":
		on, _ := s.IsOn()
		return s.Set(ctx, !on, prio)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

// dispatchAccessPermission serves the access-permission switch of the
// HomematicIP access-control devices. It reports the SWITCH category, so
// every switch surface (SPA tile, REST, WS) sends the switch operations at
// it; without this case they answered "unsupported data point type".
//
// There is no bounded-on operation: the permission is written through the
// write-only ACCESS_AUTHORIZATION control, which carries no on-time.
func (d *CustomDPDispatcher) dispatchAccessPermission(
	ctx context.Context, ap *switchdev.AccessPermission, op string, prio hmenum.CommandPriority,
) error {
	switch op {
	case "turn_on":
		return ap.TurnOn(ctx, prio)
	case "turn_off":
		return ap.TurnOff(ctx, prio)
	case "toggle":
		on, _ := ap.IsOn()
		if on {
			return ap.TurnOff(ctx, prio)
		}
		return ap.TurnOn(ctx, prio)
	default:
		return fmt.Errorf("%w: %s", hmapi.ErrUnknownOperation, op)
	}
}

// --- Audit ---

func (d *CustomDPDispatcher) recordAudit(
	deviceAddress string,
	chNo int,
	name, operation, source string,
	params map[string]any,
) {
	// Record only the NAMES of the written parameters, never their raw
	// values: a write payload can carry secrets (e.g. a lock PIN) that
	// must not be persisted into the append-only audit log.
	note := fmt.Sprintf("source=%s op=%s", source, operation)
	if len(params) > 0 {
		note += " params=" + strings.Join(slices.Sorted(maps.Keys(params)), ",")
	}
	d.audit.Record(audit.Entry{
		Action:        audit.ActionDataPointWrite,
		DeviceAddress: deviceAddress,
		ChannelNo:     chNo,
		Parameter:     name,
		Note:          note,
	})
}

// ============================================================
// Carrier interfaces — fallback path for test wrappers that need to
// expose a real `*climate.Climate` (etc.) wrapped behind a fake
// `DataPointKey()` implementation. Production code passes the
// concrete pointer directly and never hits these.
// ============================================================

type climateCarrier interface {
	device.AttachableDataPoint
	ClimateDP() *climate.Climate
}
type lockCarrier interface {
	device.AttachableDataPoint
	LockDP() *lock.Lock
}
type sirenCarrier interface {
	device.AttachableDataPoint
	SirenDP() *siren.Siren
}
type textDisplayCarrier interface {
	device.AttachableDataPoint
	TextDisplayDP() *textdisplay.TextDisplay
}

// ============================================================
// Param-parsing helpers
// ============================================================

// wrapServiceErr translates the light model's service-registry error
// sentinels into the hmapi sentinels the REST and WebSocket layers
// classify on. Without it every malformed payload on those planes would
// degrade from 422 Unprocessable Entity to a 502 upstream failure,
// because they match on [hmapi.ErrBadParam] alone.
//
// An unknown *method* reaching this function is never the dispatched
// operation itself — the caller checks that the operation is registered
// before invoking. It is the nested routing of an HA JSON-schema
// attribute (`color`, `color_temp_kelvin`, `effect`) to a light type that
// advertises no such axis, which is a bad parameter for the operation the
// caller did send, not an unknown operation.
func wrapServiceErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, payload.ErrServiceMissingParam),
		errors.Is(err, payload.ErrServiceInvalidParam):
		return fmt.Errorf("%w: %w", hmapi.ErrBadParam, err)
	case errors.Is(err, payload.ErrUnknownServiceMethod):
		return fmt.Errorf("%w: this data point supports no such attribute: %w", hmapi.ErrBadParam, err)
	default:
		return err
	}
}

// paramFloat extracts a float64 from params[key]. When hi > 0, the
// value is validated within [0, hi]. When hi == 0, no bounds check
// is applied (useful for temperature which has an arbitrary range).
// Returns [hmapi.ErrBadParam] wrapped with a useful message when
// the key is absent or the value is the wrong type.
func paramFloat(params map[string]any, key string, hi float64) (float64, error) {
	v, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: missing required param %q", hmapi.ErrBadParam, key)
	}
	f, err := toFloat64(v)
	if err != nil {
		return 0, fmt.Errorf("%w: param %q: %w", hmapi.ErrBadParam, key, err)
	}
	if hi > 0 && (f < 0 || f > hi) {
		return 0, fmt.Errorf("%w: param %q: value %v out of range [0, %v]", hmapi.ErrBadParam, key, f, hi)
	}
	return f, nil
}

// paramInt32 extracts an int32 from params[key].
func paramInt32(params map[string]any, key string) (int32, error) {
	v, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: missing required param %q", hmapi.ErrBadParam, key)
	}
	n, err := toInt32(v)
	if err != nil {
		return 0, fmt.Errorf("%w: param %q: %w", hmapi.ErrBadParam, key, err)
	}
	return n, nil
}

// paramString extracts a string from params[key].
func paramString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%w: missing required param %q", hmapi.ErrBadParam, key)
	}
	s, ok2 := v.(string)
	if !ok2 {
		return "", fmt.Errorf("%w: param %q must be a string, got %T", hmapi.ErrBadParam, key, v)
	}
	return s, nil
}

// paramStringOptional reads an optional string parameter. Returns
// (value, true) when the key is present AND the value is a string;
// (zero, false) when missing or wrong type. Useful for HA-Discovery
// JSON payloads where the same command_topic accepts several
// shapes (e.g. mqtt-light schema=json: {state, brightness} OR just
// {brightness}).
func paramStringOptional(params map[string]any, key string) (string, bool) { //nolint:unparam // key is designed for multiple call sites; currently only "state" is used but the function is intentionally generic
	v, ok := params[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// onTimeParam resolves a timed-action duration from a service payload. The
// canonical key is "seconds" (a JSON number of seconds) — the shape every SPA
// CDP widget emits for turn_on_for / timed open. "duration" is accepted as a
// backward-compatible alias for API/MQTT clients: a string is parsed by
// [time.ParseDuration] ("30s", "1m30s"), a bare number is treated as
// milliseconds (the legacy shape). Returns (dur, true, nil) when a value was
// supplied, (0, false, nil) when neither key is present (callers that make the
// duration optional rely on the bool), and a wrapped [hmapi.ErrBadParam] when a
// supplied value cannot be parsed.
func onTimeParam(params map[string]any) (time.Duration, bool, error) {
	if raw, ok := params["seconds"]; ok {
		secs, err := toFloat64(raw)
		if err != nil {
			return 0, true, fmt.Errorf("%w: param %q: %w", hmapi.ErrBadParam, "seconds", err)
		}
		return time.Duration(secs * float64(time.Second)), true, nil
	}
	if raw, ok := params["duration"]; ok {
		dur, err := anyToDuration(raw)
		if err != nil {
			return 0, true, fmt.Errorf("%w: param %q: %w", hmapi.ErrBadParam, "duration", err)
		}
		return dur, true, nil
	}
	return 0, false, nil
}

// requireOnTime is [onTimeParam] for operations where the duration is
// mandatory; it returns [hmapi.ErrBadParam] naming the canonical "seconds" key
// when neither "seconds" nor the "duration" alias is present.
func requireOnTime(params map[string]any) (time.Duration, error) {
	dur, ok, err := onTimeParam(params)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("%w: missing required param %q", hmapi.ErrBadParam, "seconds")
	}
	return dur, nil
}

// awayDuration extracts an away-mode duration from either "hours"
// (a float number of hours) or "duration_seconds" (a float number of
// seconds). "hours" takes precedence when both are present. Returns
// [hmapi.ErrBadParam] when neither key is present or the value has the
// wrong type.
func awayDuration(params map[string]any) (time.Duration, error) {
	if raw, ok := params["hours"]; ok {
		hours, err := toFloat64(raw)
		if err != nil {
			return 0, fmt.Errorf("%w: param %q: %w", hmapi.ErrBadParam, "hours", err)
		}
		return time.Duration(hours * float64(time.Hour)), nil
	}
	if raw, ok := params["duration_seconds"]; ok {
		sec, err := toFloat64(raw)
		if err != nil {
			return 0, fmt.Errorf("%w: param %q: %w", hmapi.ErrBadParam, "duration_seconds", err)
		}
		return time.Duration(sec * float64(time.Second)), nil
	}
	return 0, fmt.Errorf("%w: missing required param %q or %q", hmapi.ErrBadParam, "hours", "duration_seconds")
}

// paramTime extracts a [time.Time] from params[key]. Accepts:
//   - an RFC 3339 string
//   - a relative duration string ("+5h30m")
func paramTime(params map[string]any, key string) (time.Time, error) {
	v, ok := params[key]
	if !ok {
		return time.Time{}, fmt.Errorf("%w: missing required param %q", hmapi.ErrBadParam, key)
	}
	s, ok2 := v.(string)
	if !ok2 {
		return time.Time{}, fmt.Errorf("%w: param %q must be a string, got %T", hmapi.ErrBadParam, key, v)
	}
	// Try RFC3339 first.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try "+<duration>" relative to now.
	if strings.HasPrefix(s, "+") {
		if dur, err := time.ParseDuration(s[1:]); err == nil {
			return time.Now().Add(dur), nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: param %q: cannot parse %q as RFC3339 or +<duration>", hmapi.ErrBadParam, key, s)
}

// anyToDuration converts a raw JSON value to a [time.Duration].
//   - string: forwarded to [time.ParseDuration]
//   - number (float64, int, …): treated as milliseconds
func anyToDuration(v any) (time.Duration, error) {
	if s, ok := v.(string); ok {
		dur, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("cannot parse %q as duration: %w", s, err)
		}
		return dur, nil
	}
	ms, err := toFloat64(v)
	if err != nil {
		return 0, fmt.Errorf("cannot convert %T to duration: %w", v, err)
	}
	return time.Duration(ms * float64(time.Millisecond)), nil
}

// toFloat64 converts common JSON scalar types to float64.
func toFloat64(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case float32:
		return float64(x), nil
	case int:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case json.Number:
		return x.Float64()
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, fmt.Errorf("cannot parse %q as float", x)
		}
		return f, nil
	}
	return 0, fmt.Errorf("unsupported type %T for float conversion", v)
}

// toInt32 converts common JSON scalar types to int32.
func toInt32(v any) (int32, error) {
	switch x := v.(type) {
	case float64:
		return int32(x), nil //nolint:gosec // deliberate narrowing from JSON number; see #20
	case float32:
		return int32(x), nil //nolint:gosec // deliberate narrowing; caller validates range; see #20
	case int:
		return int32(x), nil //nolint:gosec // deliberate narrowing; caller validates range; see #20
	case int32:
		return x, nil
	case int64:
		return int32(x), nil //nolint:gosec // deliberate narrowing; caller validates range; see #20
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, fmt.Errorf("cannot parse %q as int", x)
		}
		return int32(n), nil //nolint:gosec // deliberate narrowing; caller validates range; see #20
	case string:
		n, err := strconv.ParseInt(x, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("cannot parse %q as int", x)
		}
		return int32(n), nil //nolint:gosec // ParseInt bit size 32 guarantees the range; see #20
	}
	return 0, fmt.Errorf("unsupported type %T for int conversion", v)
}

// toString converts common JSON scalar types to string.
func toString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	}
	return "", fmt.Errorf("unsupported type %T for string conversion", v)
}

// extractRow builds a [textdisplay.Row] from the params map.
func extractRow(p map[string]any) (textdisplay.Row, error) {
	id, err := paramInt32(p, "id")
	if err != nil {
		return textdisplay.Row{}, err
	}
	row := textdisplay.Row{ID: id}
	if v, ok := p["text"]; ok {
		s, ok2 := v.(string)
		if !ok2 {
			return textdisplay.Row{}, fmt.Errorf("%w: param \"text\" must be a string", hmapi.ErrBadParam)
		}
		row.Text = s
	}
	if v, ok := p["icon"]; ok {
		s, ok2 := v.(string)
		if !ok2 {
			return textdisplay.Row{}, fmt.Errorf("%w: param \"icon\" must be a string", hmapi.ErrBadParam)
		}
		row.Icon = s
	}
	if v, ok := p["alignment"]; ok {
		s, ok2 := v.(string)
		if !ok2 {
			return textdisplay.Row{}, fmt.Errorf("%w: param \"alignment\" must be a string label", hmapi.ErrBadParam)
		}
		row.Alignment = &s
	}
	if v, ok := p["text_color"]; ok {
		s, ok2 := v.(string)
		if !ok2 {
			return textdisplay.Row{}, fmt.Errorf("%w: param \"text_color\" must be a string label", hmapi.ErrBadParam)
		}
		row.TextColor = &s
	}
	if v, ok := p["background_color"]; ok {
		s, ok2 := v.(string)
		if !ok2 {
			return textdisplay.Row{}, fmt.Errorf("%w: param \"background_color\" must be a string label", hmapi.ErrBadParam)
		}
		row.BackgroundColor = &s
	}
	return row, nil
}

// extractSoundOptions builds a [textdisplay.SoundOptions] from the params map.
func extractSoundOptions(p map[string]any) (textdisplay.SoundOptions, error) {
	var opts textdisplay.SoundOptions
	if v, ok := p["sound"]; ok {
		s, ok2 := v.(string)
		if !ok2 {
			return opts, fmt.Errorf("%w: param \"sound\" must be a string", hmapi.ErrBadParam)
		}
		opts.Sound = s
	}
	if v, ok := p["repetitions"]; ok {
		s, ok2 := v.(string)
		if !ok2 {
			return opts, fmt.Errorf("%w: param \"repetitions\" must be a string", hmapi.ErrBadParam)
		}
		opts.Repetitions = s
	}
	if v, ok := p["interval"]; ok {
		s, ok2 := v.(string)
		if !ok2 {
			return opts, fmt.Errorf("%w: param \"interval\" must be a string", hmapi.ErrBadParam)
		}
		opts.Interval = s
	}
	return opts, nil
}
