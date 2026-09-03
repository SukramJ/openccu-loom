// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// permanentlyOn is the VALUE_LIST sentinel for "keep LED on
// indefinitely".
const permanentlyOn = "PERMANENTLY_ON"

// ConvertFlashTimeToOnTimeList maps a flash duration in milliseconds to the
// nearest entry of the device's own ON_TIME_LIST value list.
//
// The list is not a regular ladder — a device declares
// 100MS…500MS, 700MS, 1S, 2S, 3S, 5S, 7S, 10S, 20S, 40S, 60S, PERMANENTLY_ON —
// so the entry is chosen by parsing each declared label rather than from a
// table of our own. A label the device does not declare is rejected by the
// CCU's enum conversion and fails the whole atomic turn-on put_paramset, so
// only members of valueList are ever returned.
//
// 0, a negative duration, and anything beyond the longest declared entry yield
// PERMANENTLY_ON. An empty or unparsable valueList falls back to
// [defaultOnTimeList].
func ConvertFlashTimeToOnTimeList(flashTimeMS int, valueList []string) string {
	list := valueList
	if len(list) == 0 {
		list = defaultOnTimeList
	}
	if flashTimeMS <= 0 {
		return permanentlyOn
	}
	best := ""
	bestDiff := math.MaxInt
	for _, label := range list {
		ms, ok := onTimeListLabelMS(label)
		if !ok {
			continue
		}
		diff := ms - flashTimeMS
		if diff < 0 {
			diff = -diff
		}
		if diff < bestDiff {
			bestDiff = diff
			best = label
		}
	}
	if best == "" {
		return permanentlyOn
	}
	// Beyond the longest declared duration the device cannot express the
	// request; "keep it on" is the honest reading of "longer than the longest".
	if longest, ok := onTimeListLabelMS(best); ok && flashTimeMS > longest && best == longestOnTimeLabel(list) {
		return permanentlyOn
	}
	return best
}

// onTimeListLabelMS parses an ON_TIME_LIST label into milliseconds.
// PERMANENTLY_ON carries no duration and reports (0, false).
func onTimeListLabelMS(label string) (int, bool) {
	switch {
	case strings.HasSuffix(label, "MS"):
		n, err := strconv.Atoi(strings.TrimSuffix(label, "MS"))
		if err != nil {
			return 0, false
		}
		return n, true
	case strings.HasSuffix(label, "S"):
		n, err := strconv.Atoi(strings.TrimSuffix(label, "S"))
		if err != nil {
			return 0, false
		}
		return n * 1000, true
	default:
		return 0, false
	}
}

// longestOnTimeLabel returns the label with the longest parsable duration.
func longestOnTimeLabel(list []string) string {
	best, bestMS := "", -1
	for _, label := range list {
		if ms, ok := onTimeListLabelMS(label); ok && ms > bestMS {
			bestMS, best = ms, label
		}
	}
	return best
}

// defaultOnTimeList is the value list a device declares, used only when the
// descriptor carries none.
var defaultOnTimeList = []string{
	"100MS", "200MS", "300MS", "400MS", "500MS", "700MS",
	"1S", "2S", "3S", "5S", "7S", "10S", "20S", "40S", "60S",
	permanentlyOn,
}

// LedOnConfig bundles optional arguments for [SoundPlayerLED.TurnOn].
type LedOnConfig struct {
	// Brightness is the 0–255 brightness value. 0 means "use full
	// brightness" (1.0).
	Brightness uint8
	// HSColor is the [hue, saturation] pair for colour selection, with
	// saturation HA-canonical 0..100 (fed to [HSToFixedColor]). Nil means
	// "keep current / default to WHITE".
	HSColor *[2]float64
	// OnTime is the on-duration in seconds (DURATION_VALUE/UNIT).
	// 0 means "use deferred timer or no timer".
	OnTime float64
	// RampTime is the ramp duration in seconds.
	RampTime float64
	// Repetitions is the flash cycle count:
	// 0 → NO_REPETITION (default)
	// -1 → INFINITE_REPETITIONS
	// 1..18 → REPETITIONS_NNN
	// Values outside -1..18 are treated as 0.
	Repetitions int
	// FlashTimeMS is the flash duration in ms. ≤0 or >5000 →
	// PERMANENTLY_ON.
	FlashTimeMS int
}

// SoundPlayerLED is the HmIP-MP3P channel-6 status LED. It is a fixed-colour
// light with two extra controls: ON_TIME_LIST (flash timing) and REPETITIONS
// (how many flash cycles).
type SoundPlayerLED struct {
	*FixedColorLight

	// ON_TIME_LIST_1 and REPETITIONS are write-only ENUMs (OPERATIONS=2)
	// whose VALUE_LIST carries the labels; the resolver builds an
	// ActionSelect for each.
	onTimeList  *generic.ActionSelect
	repetitions *generic.ActionSelect

	// direction is DIRECTION — ACTIVITY_STATE on the IPSoundPlayerLed
	// schema. The sibling IPSoundPlayer profile on channel 2 resolves the
	// same parameter into siren.SoundPlayer.direction; the LED channel
	// carries it too and is bound the same way.
	direction *generic.Sensor[int32]

	availableOnTimes     []string
	availableRepetitions []string
}

// NewSoundPlayerLED constructs the LED light.
func NewSoundPlayerLED(cfg Config) *SoundPlayerLED {
	fc := NewFixedColorLight(cfg)
	led := &SoundPlayerLED{FixedColorLight: fc}
	if cfg.Channel != nil {
		if dp := cfg.Channel.Parameter(hmenum.ParameterOnTimeList1); dp != nil {
			led.availableOnTimes = append([]string(nil), dp.ParameterData().ValueList...)
		}
		if dp := cfg.Channel.Parameter(hmenum.ParameterRepetitions); dp != nil {
			led.availableRepetitions = append([]string(nil), dp.ParameterData().ValueList...)
		}
		led.onTimeList = custom.ActionSelectField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldOnTimeList, hmenum.ParameterOnTimeList1))
		led.repetitions = custom.ActionSelectField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldRepetitions, hmenum.ParameterRepetitions))
		led.direction = custom.EnumSensorField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldDirection, hmenum.ParameterDirection))
	}
	if led.onTimeList != nil {
		_ = led.onTimeList.OnConfirmedUpdate(func(_, _ int32) { led.dataVersion.Bump() })
	}
	if led.repetitions != nil {
		_ = led.repetitions.OnConfirmedUpdate(func(_, _ int32) { led.dataVersion.Bump() })
	}
	if led.direction != nil {
		_ = led.direction.OnConfirmedUpdate(func(_, _ int32) { led.dataVersion.Bump() })
	}
	// The embedded chain registers turn_on / turn_off / set_level only
	// when the LEVEL Float resolved (see [New]); without it there is
	// nothing to override and the LED has no write path either.
	if led.Float != nil {
		led.registerSoundPlayerLEDServices()
	}
	return led
}

// registerSoundPlayerLEDServices substitutes the LED's own atomic writes
// for the three inherited on/off operations.
//
// The plain [Light.TurnOn] the embedded FixedColorLight would run writes
// LEVEL only, which leaves COLOR at BLACK — the LED stays dark — and lets
// a previously commanded flash pattern (ON_TIME_LIST_1 / REPETITIONS)
// survive a turn-off. [SoundPlayerLED.TurnOn] / [SoundPlayerLED.TurnOff]
// bundle COLOR, ON_TIME_LIST_1, REPETITIONS and ON_TIME into one
// put_paramset instead.
//
// OverrideService rather than RegisterService: the whole embedded chain
// shares one ServiceRegistry, so re-registering an inherited name would
// trip its duplicate-registration panic at device-discovery time.
func (l *SoundPlayerLED) registerSoundPlayerLEDServices() {
	l.OverrideService("turn_on", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		return l.TurnOn(ctx, ledOnConfigFromParams(params), l.Writer, l.Address(), priority)
	})
	l.OverrideService("turn_off", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return l.TurnOff(ctx, l.Writer, l.Address(), priority)
	})
	l.OverrideService("set_level", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		if state, ok := params["state"].(string); ok {
			switch strings.ToUpper(state) {
			case "OFF":
				return l.TurnOff(ctx, l.Writer, l.Address(), priority)
			case "ON":
				if err := l.TurnOn(ctx, ledOnConfigFromParams(params), l.Writer, l.Address(), priority); err != nil {
					return err
				}
				// `color` is consumed by the atomic write above; the
				// remaining HA attributes are not axes a fixed-colour
				// LED advertises, so route them and let the registry
				// answer for them.
				return l.applyHAColorTempAndEffect(ctx, params, priority)
			}
		}
		// Every other shape (brightness-only, the legacy scalar level)
		// is a plain LEVEL adjust and keeps the inherited semantics.
		return l.applyHASetLevel(ctx, params, priority)
	})
}

// ledOnConfigFromParams builds a [LedOnConfig] from a service method's
// params. Every field is optional, matching LedOnConfig's own documented
// zero-value defaults (0 brightness → full, nil HSColor → keep the
// current colour, 0 on/ramp time → no timer, 0 repetitions → none, 0
// flash time → PERMANENTLY_ON); a param that is present but the wrong
// type is ignored rather than rejected, so a caller that only wants a
// plain turn-on can omit all of them.
//
// Both colour spellings are read: the flat `hue` / `saturation` pair a
// REST or SPA caller sends, and the `color:{"h":…,"s":…}` object of an HA
// JSON-schema light command. Reading only the flat pair would drop the
// colour of every HA colour pick on this LED.
func ledOnConfigFromParams(p map[string]any) LedOnConfig {
	var cfg LedOnConfig
	if raw, ok := p["brightness"]; ok {
		if f, err := toNumber(raw); err == nil {
			cfg.Brightness = uint8(min(max(f, 0), 255))
		}
	}
	if hueRaw, ok := p["hue"]; ok {
		if hue, err := toNumber(hueRaw); err == nil {
			sat := 100.0
			if satRaw, ok2 := p["saturation"]; ok2 {
				if s, err2 := toNumber(satRaw); err2 == nil {
					sat = s
				}
			}
			cfg.HSColor = &[2]float64{hue, sat}
		}
	} else if c, ok := p["color"]; ok {
		if hue, sat, valid := haColorHS(c); valid {
			cfg.HSColor = &[2]float64{float64(hue), sat}
		}
	}
	if raw, ok := p["on_time"]; ok {
		if f, err := toNumber(raw); err == nil {
			cfg.OnTime = f
		}
	}
	if raw, ok := p["ramp_time"]; ok {
		if f, err := toNumber(raw); err == nil {
			cfg.RampTime = f
		}
	}
	if raw, ok := p["repetitions"]; ok {
		if n, err := toNumber(raw); err == nil {
			cfg.Repetitions = int(n)
		}
	}
	if raw, ok := p["flash_time_ms"]; ok {
		if n, err := toNumber(raw); err == nil {
			cfg.FlashTimeMS = int(n)
		}
	}
	return cfg
}

// ActivityState returns the LED channel's current DIRECTION /
// ACTIVITY_STATE label and whether it has been observed. nil-safe: reports
// ("", false) when the schema mapped no such parameter onto this channel.
func (l *SoundPlayerLED) ActivityState() (string, bool) {
	return custom.EnumLabelValue(l.direction)
}

// AvailableOnTimes returns the labels of accepted ON_TIME_LIST_1
// values (e.g. "100MS", "200MS", "500MS", "1S", "2S", "5S",
// "PERMANENTLY_ON").
func (l *SoundPlayerLED) AvailableOnTimes() []string {
	return append([]string(nil), l.availableOnTimes...)
}

// AvailableRepetitions returns the labels of accepted REPETITIONS
// values (e.g. "NO_REP", "REPETITIONS_2", "REPETITIONS_5", "INFINITE").
func (l *SoundPlayerLED) AvailableRepetitions() []string {
	return append([]string(nil), l.availableRepetitions...)
}

// TurnOff stops the LED. The CCU expects COLOR=BLACK + ON_TIME=0 in a single
// put_paramset so the ON_TIME timer that may still be running is cleared
// atomically with the colour change.
func (l *SoundPlayerLED) TurnOff(ctx context.Context, w custom.Writer, addr string, priority hmenum.CommandPriority) error {
	if w == nil {
		return errors.New("soundplayer-led: writer required")
	}
	ctx = custom.EnsureContext(ctx)
	coll := generic.NewCollector(generic.WriterAsBackend(w), generic.WithPriority(priority))
	ctx = generic.ContextWithCollector(ctx, coll)
	// Anything staged on the collector only reaches the wire in the
	// flush, so its error is part of this command's result.
	return generic.FlushCollector(ctx, coll,
		custom.PutOrSet(ctx, w, addr, hmenum.ParamsetKeyValues, map[hmenum.Parameter]any{
			hmenum.ParameterColor:  fixedColorNames[FixedColorBlack],
			hmenum.ParameterOnTime: 0.0,
		}, priority))
}

// TurnOn turns on the LED with optional colour, brightness, flash
// timing, and repetitions. The parameters are bundled into a single
// atomic put_paramset when the writer supports it — mirrors
// turn_on (light.py:839-880).
//
// Parameter resolution:
// - LEVEL: cfg.Brightness/255.0, or 1.0 when Brightness==0.
// - COLOR: resolved from cfg.HSColor via [HSToFixedColor], or
// kept as current color (defaulting to WHITE if unset/black).
// - ON_TIME_LIST_1: [ConvertFlashTimeToOnTimeList](cfg.FlashTimeMS).
// - REPETITIONS: [custom.RepetitionsLabel](cfg.Repetitions).
// - RAMP_TIME: cfg.RampTime seconds.
// - ON_TIME: cfg.OnTime seconds (or deferred timer via
// [Light.SetTimerOnTime]).
func (l *SoundPlayerLED) TurnOn(ctx context.Context, cfg LedOnConfig, w custom.Writer, addr string, priority hmenum.CommandPriority) error {
	if w == nil {
		return errors.New("soundplayer-led: writer required")
	}
	ctx = custom.EnsureContext(ctx)

	// Brightness: 0-255 → 0.0-1.0; 0 means full brightness.
	var brightness float64
	if cfg.Brightness == 0 {
		brightness = 1.0
	} else {
		brightness = float64(cfg.Brightness) / 255.0
	}

	// Colour: resolve from hs_color or default to WHITE.
	color := l.currentColorOrWhite()
	if cfg.HSColor != nil {
		color = HSToFixedColor(int32(cfg.HSColor[0]), cfg.HSColor[1])
	}

	// Flash time → ON_TIME_LIST label.
	// The device's own list decides which durations are expressible; ours
	// would invent labels it rejects.
	var onTimeListValues []string
	if l.onTimeList != nil {
		onTimeListValues = l.onTimeList.Descriptor.ValueList
	}
	flashValue := ConvertFlashTimeToOnTimeList(cfg.FlashTimeMS, onTimeListValues)

	// Repetitions → label. A count the grammar cannot express is operator
	// input the device would reject; failing the whole command surfaces it
	// instead of quietly turning the LED on with a different repeat count.
	repValue, err := custom.RepetitionsLabel(cfg.Repetitions)
	if err != nil {
		return fmt.Errorf("soundplayer-led: %w", err)
	}

	// OnTime from deferred timer or cfg.OnTime.
	onTime := cfg.OnTime
	if l.FixedColorLight != nil && l.Light != nil {
		l.timerMu.Lock()
		if l.pendingOn != nil {
			onTime = l.pendingOn.Seconds()
			l.pendingOn = nil
		}
		l.timerMu.Unlock()
	}

	colorLabel := fixedColorNames[color]
	// RAMP_TIME and ON_TIME are always sent, even when 0. Sending 0 resets any
	// previously active timer on the device — skipping the fields leaves the old
	// timer running.
	params := map[hmenum.Parameter]any{
		hmenum.ParameterLevel:       brightness,
		hmenum.ParameterColor:       colorLabel,
		hmenum.ParameterOnTimeList1: flashValue,
		hmenum.ParameterRepetitions: repValue,
		hmenum.ParameterRampTime:    cfg.RampTime,
		hmenum.ParameterOnTime:      onTime,
	}

	coll := generic.NewCollector(generic.WriterAsBackend(w), generic.WithPriority(priority))
	ctx = generic.ContextWithCollector(ctx, coll)
	// Anything staged on the collector only reaches the wire in the
	// flush, so its error is part of this command's result.
	return generic.FlushCollector(ctx, coll,
		custom.PutOrSet(ctx, w, addr, hmenum.ParamsetKeyValues, params, priority))
}

// currentColorOrWhite returns the last observed COLOR sensor value, or
// FixedColorWhite when no colour has been set or the current value is
// FixedColorBlack. The method uses [FixedColorLight.Color] to read the
// embedded sensor.
func (l *SoundPlayerLED) currentColorOrWhite() FixedColor {
	if l.FixedColorLight == nil {
		return FixedColorWhite
	}
	c, ok := l.Color()
	if !ok || c == FixedColorBlack {
		return FixedColorWhite
	}
	return c
}

// Flash emits one or more flashes. `colour` selects the LED colour,
// `onTimeIdx` indexes into [AvailableOnTimes] (0 = shortest, last =
// PERMANENTLY_ON), `repIdx` indexes into [AvailableRepetitions]
// (0 = NO_REP, last = INFINITE).
func (l *SoundPlayerLED) Flash(ctx context.Context, c FixedColor, onTimeIdx, repIdx int, w custom.Writer, addr string, priority hmenum.CommandPriority) error {
	ctx = custom.EnsureContext(ctx)
	if onTimeIdx < 0 || onTimeIdx >= len(l.availableOnTimes) {
		return fmt.Errorf("soundplayer-led: on-time index %d out of range", onTimeIdx)
	}
	if repIdx < 0 || repIdx >= len(l.availableRepetitions) {
		return fmt.Errorf("soundplayer-led: repetitions index %d out of range", repIdx)
	}
	if w == nil {
		return errors.New("soundplayer-led: writer required")
	}
	if err := w.SetValue(ctx, addr, hmenum.ParameterOnTimeList1, l.availableOnTimes[onTimeIdx], priority); err != nil {
		return fmt.Errorf("soundplayer-led: ON_TIME_LIST_1: %w", err)
	}
	if err := w.SetValue(ctx, addr, hmenum.ParameterRepetitions, l.availableRepetitions[repIdx], priority); err != nil {
		return fmt.Errorf("soundplayer-led: REPETITIONS: %w", err)
	}
	return l.SetColor(ctx, c, priority)
}
