// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package event

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Kind enumerates the three CCU event flavours.
type Kind string

// Kind values mirror DeviceTriggerEventType.
const (
	KindKeypress    Kind = "homematic.keypress"
	KindImpulse     Kind = "homematic.impulse"
	KindDeviceError Kind = "homematic.device_error"
)

// Event is one dispatched notification.
type Event struct {
	Kind           Kind
	ChannelAddress string
	Parameter      hmenum.Parameter
	Value          any
	ReceivedAt     time.Time
}

// isDeviceErrorParam reports whether p matches a device-error prefix. via a
// tuple prefix-search. (M27a — v8 §4)
func isDeviceErrorParam(p hmenum.Parameter) bool {
	s := string(p)
	for _, pfx := range errorPrefixes {
		// Accept exact match (e.g. "ERROR") or prefix followed by "_"
		// (e.g. "ERROR_OVERHEAT") to avoid false positives on unrelated
		// parameters whose names happen to start with the same letters.
		if s == pfx || strings.HasPrefix(s, pfx+"_") {
			return true
		}
	}
	return false
}

// Classify returns the Kind a parameter name maps to, plus true when
// the parameter is a known event-bearing parameter.
//
// Device-error parameters are matched by prefix (M27a), so ERROR,
// SENSOR_ERROR, ERROR_OVERHEAT, ERROR_REDUCED, etc. all resolve to
// [KindDeviceError] — mirroring
func Classify(p hmenum.Parameter) (Kind, bool) {
	if _, ok := clickParams[p]; ok {
		return KindKeypress, true
	}
	if _, ok := impulseParams[p]; ok {
		return KindImpulse, true
	}
	if isDeviceErrorParam(p) {
		return KindDeviceError, true
	}
	return "", false
}

// Sources returns the parameter set for a kind in sorted order.
// Useful for introspection and tests.
//
// For [KindDeviceError] the returned slice contains the known error
// prefix roots (e.g. "ERROR", "SENSOR_ERROR") rather than an
// exhaustive list, because device-error matching is prefix-based and
// the full set of matching CCU parameter names is open-ended.
func Sources(k Kind) []hmenum.Parameter {
	switch k {
	case KindKeypress:
		out := make([]hmenum.Parameter, 0, len(clickParams))
		for p := range clickParams {
			out = append(out, p)
		}
		slices.Sort(out)
		return out
	case KindImpulse:
		out := make([]hmenum.Parameter, 0, len(impulseParams))
		for p := range impulseParams {
			out = append(out, p)
		}
		slices.Sort(out)
		return out
	case KindDeviceError:
		out := make([]hmenum.Parameter, 0, len(errorPrefixes))
		for _, pfx := range errorPrefixes {
			out = append(out, hmenum.Parameter(pfx))
		}
		slices.Sort(out)
		return out
	default:
		return nil
	}
}

// Source is one bound (channel, parameter) event emitter. It holds
// the classification, timestamp of the last fire, and subscribers.
//
// [DeviceError] sources apply a value-change gate: events only fire
// on transitions to an active state (true / > 0).
//
// [enabledByChannelOperationMode] carries the tri-state gate set by the
// device pipeline's `applyChannelOperationModeGating` pass. Nil means
// "no CHANNEL_OPERATION_MODE constraint observed" (mirrors
// `if cop is None: return None` branch). False means the current operation
// mode excludes this parameter — [Usage] returns [hmenum.DataPointUsageNoCreate]
// in that case. True means the mode explicitly includes it — [Usage] returns
// [hmenum.DataPointUsageEvent].
type Source struct {
	ChannelAddress string
	Parameter      hmenum.Parameter
	Kind           Kind

	mu        sync.RWMutex
	lastAt    time.Time
	lastValue any
	hasLast   bool
	callbacks []func(Event)

	// enabledByChannelOperationMode is the tri-state gate written by
	// applyChannelOperationModeGating. Protected by mu.
	enabledByChannelOperationMode *bool
}

// NewSource constructs a Source after classifying parameter p. Panics
// when p is not a known event parameter — callers should use
// [Classify] first.
func NewSource(channelAddress string, p hmenum.Parameter) *Source {
	kind, ok := Classify(p)
	if !ok {
		return nil
	}
	return &Source{ChannelAddress: channelAddress, Parameter: p, Kind: kind}
}

// DataPointKey returns the composite identifier for this event source.
// The key uses ParamsetKeyValues as a stable namespace token; the
// InterfaceID is intentionally empty for event sources (they are not
// tied to a single interface — the CCU may route them over any registered
// path). Consumers that need the interface must look it up on the parent
// device via [Device.Interface].
func (s *Source) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		ChannelAddress: s.ChannelAddress,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(s.Parameter),
	}
}

// EventKind returns the string representation of the source's [Kind].
// Satisfies the [device.AttachableEvent] interface so Sources can be
// stored in Channel.genericEvents without a separate wrapper type.
func (s *Source) EventKind() string { return string(s.Kind) }

// EventParameter returns the CCU parameter name for this event source.
// Satisfies the [device.GenericEvent] interface (model/device/aggregate.go)
// so GetGenericEvent can match Sources by parameter.
func (s *Source) EventParameter() hmenum.Parameter { return s.Parameter }

// Fire dispatches an event with the given value. Returns true when
// the event was published to subscribers; false when the event was
// suppressed (device_error transition gate).
func (s *Source) Fire(value any) bool {
	return s.FireAt(value, time.Now())
}

// FireAt is Fire with an explicit timestamp.
func (s *Source) FireAt(value any, at time.Time) bool {
	if s.Kind == KindDeviceError && !deviceErrorActive(s, value) {
		return false
	}
	s.mu.Lock()
	s.lastAt = at
	s.lastValue = value
	s.hasLast = true
	cbs := make([]func(Event), len(s.callbacks))
	copy(cbs, s.callbacks)
	s.mu.Unlock()

	ev := Event{
		Kind:           s.Kind,
		ChannelAddress: s.ChannelAddress,
		Parameter:      s.Parameter,
		Value:          value,
		ReceivedAt:     at,
	}
	for _, cb := range cbs {
		if cb != nil {
			cb(ev)
		}
	}
	return true
}

// LastFire returns the timestamp and value of the most recent event,
// plus whether one has been fired at all.
func (s *Source) LastFire() (time.Time, any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastAt, s.lastValue, s.hasLast
}

// OnFire subscribes a handler and returns an idempotent unsubscribe.
func (s *Source) OnFire(fn func(Event)) func() {
	s.mu.Lock()
	s.callbacks = append(s.callbacks, fn)
	idx := len(s.callbacks) - 1
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if idx < len(s.callbacks) {
				s.callbacks[idx] = nil
			}
		})
	}
}

// deviceErrorActive implements the transition gate: a device error
// fires only on moves from "inactive" to "active" or between
// distinct active values.
func deviceErrorActive(s *Source, next any) bool {
	s.mu.RLock()
	prev := s.lastValue
	had := s.hasLast
	s.mu.RUnlock()

	switch v := next.(type) {
	case bool:
		if !had {
			return v
		}
		prevBool, _ := prev.(bool)
		return prevBool != v
	case int:
		if !had {
			return v > 0
		}
		prevInt, _ := prev.(int)
		return prevInt != v
	case int32:
		if !had {
			return v > 0
		}
		prevInt, _ := prev.(int32)
		return prevInt != v
	case int64:
		if !had {
			return v > 0
		}
		prevInt, _ := prev.(int64)
		return prevInt != v
	}
	return false
}

var clickParams = map[hmenum.Parameter]struct{}{
	hmenum.ParameterPress:            {},
	hmenum.ParameterPressCont:        {},
	hmenum.ParameterPressLock:        {},
	hmenum.ParameterPressLong:        {},
	hmenum.ParameterPressLongRelease: {},
	hmenum.ParameterPressLongStart:   {},
	hmenum.ParameterPressShort:       {},
	hmenum.ParameterPressUnlock:      {},
}

var impulseParams = map[hmenum.Parameter]struct{}{
	hmenum.ParameterSequenceOK: {},
}

// errorPrefixes holds the string prefixes that identify device-error
// parameters. openccu-loom previously used an exact-match set (`errorParams`)
// which missed parameters like `ERROR_OVERHEAT`, `ERROR_REDUCED`, and other
// CCU-model-specific error suffixes. (M27a — v8 §4)
//
// Legacy exact-match entries (`ERROR`, `SENSOR_ERROR`) are preserved as
// prefixes so they continue to match without a trailing underscore.
var errorPrefixes = []string{
	"ERROR",
	"SENSOR_ERROR",
}

// SetOperationModeAllowed records the tri-state gate value set by the device
// pipeline's `applyChannelOperationModeGating` pass. Passing true marks the
// source as explicitly enabled by the current CHANNEL_OPERATION_MODE; false
// marks it as excluded (Usage returns NoCreate).
func (s *Source) SetOperationModeAllowed(allowed bool) {
	s.mu.Lock()
	s.enabledByChannelOperationMode = &allowed
	s.mu.Unlock()
}

// Usage returns the canonical [hmenum.DataPointUsage] for an event source.
//
// - When `_enabled_by_channel_operation_mode` is None (nil) → EVENT
// - When true → EVENT - When false → IGNORED
//
// The channel-operation-mode mask is a visibility-gate path; ADR 0015
// pins those onto Ignored so the un-ignore feature can offer the
// affected sources to the operator. North-bound adapters that
// iterate over data points can rely on this method to render event
// entities consistently with other DP families.
func (s *Source) Usage() hmenum.DataPointUsage {
	s.mu.RLock()
	enabled := s.enabledByChannelOperationMode
	s.mu.RUnlock()
	if enabled != nil && !*enabled {
		return hmenum.DataPointUsageIgnored
	}
	return hmenum.DataPointUsageEvent
}

// EnabledByDefault reports whether the event source should be
// surfaced in default UI listings. Mirrors
// `BaseDataPoint.enabled_default` for `EVENT`-usage:
//
//	return self.usage in (CDP_PRIMARY, CDP_VISIBLE, DATA_POINT, EVENT)
//
// EVENT is in that set, so the answer is always true for a Source.
func (s *Source) EnabledByDefault() bool { return true }

// Visible reports whether the event surface should be exposed by
// north-bound adapters. Always true for an event source — events
// have no NoCreate equivalent.
func (s *Source) Visible() bool { return true }

// Category returns the [hmenum.DataPointCategory] this event source surfaces
// under.
func (s *Source) Category() hmenum.DataPointCategory { return hmenum.DataPointCategoryEvent }

// TranslationKey returns the slugified translation key for this event source.
//
// return generate_translation_key(self.event_type)
//
// The key is derived by lower-casing the Kind string and replacing the
// "homematic." prefix with an empty string, leaving only the event-type name
// (e.g. "keypress", "impulse", "device_error"). North-bound adapters (HA
// EventPlatform, WS push) use this as the i18n lookup key for the event
// entity's display name.
func (s *Source) TranslationKey() string {
	return GenerateTranslationKey(s.Kind)
}

// GenerateTranslationKey returns the slugified i18n key for a given event
// Kind.
//
// return event_type.value.removeprefix("homematic.").replace(".", "_")
//
// "homematic.keypress" → "keypress" "homematic.impulse" → "impulse"
// "homematic.device_error" → "device_error"
func GenerateTranslationKey(k Kind) string {
	s := string(k)
	const prefix = "homematic."
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		s = s[len(prefix):]
	}
	// Replace remaining dots (none in current kinds, but future-proof).
	out := make([]byte, 0, len(s))
	for i := range len(s) {
		if s[i] == '.' {
			out = append(out, '_')
		} else {
			out = append(out, s[i])
		}
	}
	return string(out)
}
