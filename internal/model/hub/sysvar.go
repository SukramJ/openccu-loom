// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Sysvar is a CCU system variable. Its Go surface is typed through
// [hmtypes.ParamValue], which carries Bool / Int / Float / String
// List / None flavours. The [ValueType] field records the CCU's
// declared wire type so writers can coerce values on the way out.
//
// Sysvar embeds [HubDataPoint] and therefore satisfies [HubDataPointer]
// and [datapoint.BaseDataPoint]. The promoted Name and Description
// fields retain their previous call sites unchanged.
//
// Use [NewSysvar] to construct a properly wired instance.
type Sysvar struct {
	HubDataPoint // embeds Name, Description, EnabledDefault, BaseDataPointFields, StateUncertain
	Unit         string
	ValueList    []string
	ValueType    hmenum.HubValueType
	Writer       SysvarWriter

	// Min / Max hold the declared lower and upper bound of the sysvar value
	// range as provided by the CCU's Rega `getSystemVariables` response. The
	// bounds are optional — the CCU may omit them for boolean or list variables.
	Min *hmtypes.ParamValue
	Max *hmtypes.ParamValue

	// Vid is the CCU-internal numeric variable ID. Used by some Rega write paths
	// that reference variables by ID rather than name.
	Vid int

	// IsExtended indicates whether the sysvar was registered as an
	// "extended" variable in the CCU (class-level constant
	// `_is_extended`, hub/data_point.py:139). Extended
	// sysvars support additional operations and are reported separately
	// from plain system variables.
	IsExtended bool

	// ServiceRegistry implements the write-half of [payload.Source].
	// Each Sysvar instance gets its own registry so service methods are
	// registered per-instance without cross-instance double-registration.
	payload.ServiceRegistry

	mu               sync.RWMutex
	value            hmtypes.ParamValue  // confirmed value from CCU
	previousValue    *hmtypes.ParamValue // value before last confirmed write
	unconfirmedValue *hmtypes.ParamValue // optimistic write not yet confirmed
	refreshedAt      time.Time           // timestamp of last confirmed OnValue
	unconfirmedAt    time.Time           // timestamp of last WriteUnconfirmedValue
	observed         bool
	callbacks        []func(old, next hmtypes.ParamValue)
}

// NewSysvar constructs a [Sysvar] with a fully initialised
// [datapoint.BaseDataPointFields] embedded in the [HubDataPoint] base.
//
// - central — the Unit name for UniqueID scoping (multi-CCU safe).
// - name — the CCU sysvar name (used as both Name field and KeyName).
// - description — optional human-readable description.
// - valueType — the CCU-declared wire type.
// - writer — the write backend; nil creates a read-only sysvar.
func NewSysvar(centralName, name, description string, valueType hmenum.HubValueType, writer SysvarWriter) *Sysvar {
	s := &Sysvar{
		HubDataPoint: NewHubDataPoint(centralName, name, description, true),
		ValueType:    valueType,
		Writer:       writer,
	}
	if writer != nil {
		s.registerSysvarServices()
	}
	return s
}

// registerSysvarServices wires Sysvar operations onto the embedded ServiceRegistry.
func (s *Sysvar) registerSysvarServices() {
	s.RegisterService("set_value", func(ctx context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		raw, ok := params["value"]
		if !ok {
			return fmt.Errorf("%w: %q", payload.ErrServiceMissingParam, "value")
		}
		pv, err := sysvarParamValue(s.ValueType, raw)
		if err != nil {
			return err
		}
		return s.Set(ctx, pv)
	})
}

// excludedSysvarMarkers is the Go equivalent of Python's _EXCLUDED list
// In.py:95-98). Any sysvar whose legacy name
// contains one of these substrings is a CCU-internal variable that should
// not be surfaced to north-bound adapters or home-automation platforms.
//
// Current exclusions mirror
// - "OldVal" — internal change-detection helper created by the CCU.
// - "pcCCUID" — internal CCU device-ID variable.
var excludedSysvarMarkers = []string{
	"OldVal",
	"pcCCUID",
}

// IsExcludedSysvar reports whether name contains any of the
// [excludedSysvarMarkers] substrings.
func IsExcludedSysvar(name string) bool {
	for _, marker := range excludedSysvarMarkers {
		if len(name) >= len(marker) {
			for i := 0; i <= len(name)-len(marker); i++ {
				if name[i:i+len(marker)] == marker {
					return true
				}
			}
		}
	}
	return false
}

// CleanSysvarNames filters a slice of sysvar names, removing any that match
// [IsExcludedSysvar].
//
// The function allocates a new slice; the input is not modified.
func CleanSysvarNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !IsExcludedSysvar(n) {
			out = append(out, n)
		}
	}
	return out
}

// sysvarParamValue converts the raw JSON-decoded param value to the
// [hmtypes.ParamValue] kind expected for the given [hmenum.HubValueType].
func sysvarParamValue(vt hmenum.HubValueType, raw any) (hmtypes.ParamValue, error) { //nolint:gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
	switch vt {
	case hmenum.HubValueTypeLogic:
		switch v := raw.(type) {
		case bool:
			return hmtypes.BoolValue(v), nil
		case float64:
			return hmtypes.BoolValue(v != 0), nil
		case string:
			switch v {
			case "true", "True", "TRUE", "1", "on", "ON":
				return hmtypes.BoolValue(true), nil
			case "false", "False", "FALSE", "0", "off", "OFF":
				return hmtypes.BoolValue(false), nil
			}
		}
		return hmtypes.ParamValue{}, fmt.Errorf("%w: %q expects boolean", payload.ErrServiceInvalidParam, "value")
	case hmenum.HubValueTypeFloat:
		switch v := raw.(type) {
		case float64:
			return hmtypes.FloatValue(v), nil
		case float32:
			return hmtypes.FloatValue(float64(v)), nil
		case int:
			return hmtypes.FloatValue(float64(v)), nil
		case int64:
			return hmtypes.FloatValue(float64(v)), nil
		case string:
			var f float64
			if _, err := fmt.Sscanf(v, "%f", &f); err == nil {
				return hmtypes.FloatValue(f), nil
			}
		}
		return hmtypes.ParamValue{}, fmt.Errorf("%w: %q expects float", payload.ErrServiceInvalidParam, "value")
	case hmenum.HubValueTypeInteger:
		switch v := raw.(type) {
		case float64:
			return hmtypes.IntValue(int(v)), nil
		case int:
			return hmtypes.IntValue(v), nil
		case int64:
			return hmtypes.IntValue(int(v)), nil
		case string:
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				return hmtypes.IntValue(n), nil
			}
		}
		return hmtypes.ParamValue{}, fmt.Errorf("%w: %q expects integer", payload.ErrServiceInvalidParam, "value")
	case hmenum.HubValueTypeString:
		switch v := raw.(type) {
		case string:
			return hmtypes.StringValue(v), nil
		case bool, int, float64:
			return hmtypes.StringValue(fmt.Sprintf("%v", v)), nil
		}
		return hmtypes.ParamValue{}, fmt.Errorf("%w: %q expects string", payload.ErrServiceInvalidParam, "value")
	case hmenum.HubValueTypeList:
		// LIST sysvars hold a []string of the selected option(s).
		// JSON decodes arrays as []any; we accept either form.
		switch v := raw.(type) {
		case []string:
			return hmtypes.ListValue(v), nil
		case []any:
			ss := make([]string, len(v))
			for i, el := range v {
				ss[i] = fmt.Sprintf("%v", el)
			}
			return hmtypes.ListValue(ss), nil
		case string:
			return hmtypes.ListValue([]string{v}), nil
		}
		return hmtypes.ParamValue{}, fmt.Errorf("%w: %q expects list (array of strings)", payload.ErrServiceInvalidParam, "value")
	default:
		// Fallback: infer the type from the raw Go value when the HubValueType
		// is unknown or zero. This handles CCU sysvars whose type descriptor
		// has not been populated yet, mirroring the old_value-based type inference
		// in _convert_value (hub/data_point.py:231-247).
		switch v := raw.(type) {
		case bool:
			return hmtypes.BoolValue(v), nil
		case float64:
			return hmtypes.FloatValue(v), nil
		case int:
			return hmtypes.IntValue(v), nil
		case string:
			return hmtypes.StringValue(v), nil
		}
		return hmtypes.ParamValue{}, fmt.Errorf("%w: %q unsupported value type", payload.ErrServiceInvalidParam, "value")
	}
}

// TranslationKey returns "sysvar" as the HA entity translation key.
// The base HubDataPoint stub returns "" — Sysvar overrides it so
// platform adapters can look up a human-readable display name without
// hard-coding the entity kind.
func (s *Sysvar) TranslationKey() string { return "sysvar" }

// Extended reports whether this sysvar was created as an "extended"
// (editable) variable. In Go the flag is set by the coordinator when the wire
// data marks the variable as extended; WrapSysvar uses this flag to select
// the correct wrapper type.
//
// Mirrors the Python reference hub/sysvar.py — is_extended property.
func (s *Sysvar) Extended() bool {
	return s.IsExtended
}

// Value returns the effective sysvar value. When an unconfirmed (optimistic)
// value was written after the last confirmed value arrived, the unconfirmed
// value is returned instead.
func (s *Sysvar) Value() (hmtypes.ParamValue, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.unconfirmedValue != nil && !s.unconfirmedAt.Before(s.refreshedAt) {
		return *s.unconfirmedValue, true
	}
	return s.value, s.observed
}

// ConfirmedValue returns the last confirmed (non-optimistic) value
// from the CCU, ignoring any pending unconfirmed write.
func (s *Sysvar) ConfirmedValue() (hmtypes.ParamValue, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value, s.observed
}

// OnValue records an incoming sysvar change from the CCU.
// Clears [HubDataPoint.StateUncertain] on first observation, mirroring
// Write_value behaviour.
// When the value changes the previous value is stored and accessible
// via [PreviousValue]. Also resets any pending unconfirmed value so
// The confirmed CCU state wins.
// (hub/data_point.py:176) and write_value (hub/data_point.py:216-229).
func (s *Sysvar) OnValue(v hmtypes.ParamValue) {
	now := time.Now()
	s.mu.Lock()
	prev := s.value
	was := s.observed
	if was {
		cp := prev
		s.previousValue = &cp
	}
	s.value = v
	s.observed = true
	s.refreshedAt = now
	s.unconfirmedValue = nil // confirmed value clears optimistic write
	cbs := make([]func(old, next hmtypes.ParamValue), len(s.callbacks))
	copy(cbs, s.callbacks)
	s.mu.Unlock()
	s.markCertain()
	if was && prev.Equal(v) {
		return
	}
	for _, cb := range cbs {
		if cb != nil {
			cb(prev, v)
		}
	}
}

// PreviousValue returns the sysvar value that was observed before the last
// [OnValue] call, together with a boolean that is false when no prior value
// exists (first observation).
func (s *Sysvar) PreviousValue() (hmtypes.ParamValue, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.previousValue == nil {
		return hmtypes.ParamValue{}, false
	}
	return *s.previousValue, true
}

// WriteUnconfirmedValue stores an optimistic value that will shadow the
// confirmed value until the next [OnValue] call from the CCU confirms or
// overrides it. Marks [HubDataPoint.StateUncertain] to indicate an in- flight
// write.
func (s *Sysvar) WriteUnconfirmedValue(v hmtypes.ParamValue) {
	now := time.Now()
	s.mu.Lock()
	s.unconfirmedValue = &v
	s.unconfirmedAt = now
	s.mu.Unlock()
	s.markUncertain()
}

// ResetUnconfirmedValue clears the optimistic unconfirmed value slot. Called
// by [OnValue] automatically; also available for callers that want to cancel
// a pending optimistic write.
func (s *Sysvar) ResetUnconfirmedValue() {
	s.mu.Lock()
	s.unconfirmedValue = nil
	s.unconfirmedAt = time.Time{}
	s.mu.Unlock()
}

// Set writes a new sysvar value. The value is coerced based on the variable's
// [ValueType] when possible, then delegated to the writer. The optimistic
// (unconfirmed) value is stored only after the CCU call succeeds, so a failed
// write never causes a transient cache flash for readers.
func (s *Sysvar) Set(ctx context.Context, v hmtypes.ParamValue) error {
	if s.Writer == nil {
		return fmt.Errorf("sysvar %q: no writer configured", s.Name)
	}
	wireValue, err := s.toWire(v)
	if err != nil {
		return err
	}
	if err := s.Writer.SetSysvar(ctx, s.Name, wireValue); err != nil {
		return err
	}
	// Apply optimistic value only after a successful backend write.
	s.WriteUnconfirmedValue(v)
	return nil
}

// OnUpdate registers a change handler and returns an idempotent
// unsubscribe closure.
func (s *Sysvar) OnUpdate(fn func(old, next hmtypes.ParamValue)) func() {
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

// PathData returns the set/state routing paths for this sysvar keyed by its
// numeric Vid.
func (s *Sysvar) PathData() naming.PathData {
	if s.Vid == 0 {
		return naming.EmptyPathData
	}
	return naming.NewSysvarPathData(strconv.Itoa(s.Vid))
}

// MQTTTopics implements [payload.MQTTAddressable] — the canonical
// ADR-0011 sysvar topology. The model owns the topic decision; the
// bridge is a pass-through that only fills in `base` and `central`.
// Legacy mirror topics are a bridge-operations detail and live
// behind LegacyAliasConfig in the north/mqtt package.
func (s *Sysvar) MQTTTopics(base, centralName string) payload.MQTTTopicSet {
	if s.Name == "" {
		return payload.MQTTTopicSet{}
	}
	set := payload.MQTTTopicSet{
		State: naming.MQTTHubSysvarState(base, centralName, s.Name),
	}
	if s.Writer != nil {
		set.Set = naming.MQTTHubSysvarCommand(base, centralName, s.Name)
	}
	return set
}

// toWire renders v as a bare Go value the writer can serialise.
// Validation beyond the type tag is intentionally minimal — the CCU
// itself rejects out-of-range values, and range bounds are not
// exposed on the sysvar descriptor.
//
// HubValueTypeList carries a symmetry constraint: the publish path
// renders the integer CCU index as a label string (see
// adapter/hub_mqtt_publisher.go::sysvarStateForMQTT), so HA's `select`
// entity round-trips that label back on the command topic. Without
// the inverse mapping here CCU receives the raw string and the Rega
// `setSystemVariable` call silently fails. Mirrors Python
// `SysvarDpSelect.send_variable` (model/hub/select.py:34-42).
func (s *Sysvar) toWire(v hmtypes.ParamValue) (any, error) {
	if s.ValueType == hmenum.HubValueTypeList && len(s.ValueList) > 0 {
		if idx, ok := s.resolveListIndex(v); ok {
			return idx, nil
		}
		return nil, fmt.Errorf("sysvar %q: value %s not in value list", s.Name, v.AsString())
	}
	switch v.Kind {
	case hmtypes.ValueKindBool:
		return v.Bool, nil
	case hmtypes.ValueKindInt:
		return v.Int, nil
	case hmtypes.ValueKindFloat:
		return v.Float, nil
	case hmtypes.ValueKindString:
		return v.String, nil
	case hmtypes.ValueKindList:
		return v.List, nil
	case hmtypes.ValueKindNone:
		return nil, fmt.Errorf("sysvar %q: cannot write NoneValue", s.Name)
	}
	return nil, fmt.Errorf("sysvar %q: unsupported value kind %s", s.Name, v.Kind)
}

// resolveListIndex maps a write-side value onto a zero-based index
// into [Sysvar.ValueList]. Accepts integer indices directly and
// looks up string labels case-sensitively. Returns (idx, true) on a
// valid match; (0, false) otherwise (caller emits a descriptive
// error). Float values coerce to int.
func (s *Sysvar) resolveListIndex(v hmtypes.ParamValue) (int, bool) {
	switch v.Kind {
	case hmtypes.ValueKindInt:
		if v.Int >= 0 && v.Int < len(s.ValueList) {
			return v.Int, true
		}
	case hmtypes.ValueKindFloat:
		// Bound the float to the valid index range before converting so a
		// large/NaN float cannot overflow the int conversion.
		if v.Float >= 0 && v.Float < float64(len(s.ValueList)) {
			return int(v.Float), true
		}
	case hmtypes.ValueKindString:
		for i, label := range s.ValueList {
			if label == v.String {
				return i, true
			}
		}
	case hmtypes.ValueKindNone, hmtypes.ValueKindBool, hmtypes.ValueKindList:
		// These kinds do not map to a list index.
	}
	return 0, false
}
