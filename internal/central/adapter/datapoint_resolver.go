// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"github.com/SukramJ/openccu-loom/internal/model/device"
	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// resolveDataPoint maps a paramset entry onto a concrete
// [device.ParameterDataPoint]. The decision tree:
//
//	writable ACTION → Button / ActionSelect / Action / Switch
//	writable non-ACTION, write-only → ActionSelect / ActionFloat / …
//	writable read+write → Switch / Select / Float / Integer / Text
//	readonly → BinarySensor / (typed) Sensor / nil for click events
//
// Returns nil when the parameter is a click event (handled as a
// separate event stream), or when the (type, operations) tuple has
// no meaningful data-point analogue (e.g. ENUM readonly without a
// value list).
func resolveDataPoint(cfg generic.Spec) device.ParameterDataPoint {
	return resolveDataPointWithUnIgnore(cfg, false)
}

// resolveDataPointWithUnIgnore is the un-ignore-aware variant. When the
// caller already knows from the visibility decider that an ERROR-prefixed
// parameter is un-ignored for this device model (e.g. HM-Sec-Key HM-Sec-Win
// ERROR or HmIP-DLD / HmIP-DLP ERROR_JAMMED — see
// `unIgnoreParametersByDevice` in `internal/store/visibility/rules.go`), the
// DEVICE_ERROR suppression is bypassed and the DP is created.
//
// if parameter not in IMPULSE_EVENTS and ( not
// parameter.startswith(DEVICE_ERROR_EVENTS) or parameter_is_un_ignored ):
// create_data_point_and_append_to_channel(...)
//
// IMPULSE_EVENTS (SEQUENCE_OK) is suppressed regardless because it has no DP
// equivalent on either side.
func resolveDataPointWithUnIgnore(cfg generic.Spec, parameterIsUnIgnored bool) device.ParameterDataPoint {
	pd := cfg.Descriptor
	param := hmenum.Parameter(cfg.Key.Parameter)

	if isImpulseEvent(string(param)) {
		return nil
	}
	// DEVICE_ERROR_EVENTS prefix gate — mirrors
	// `model/__init__.py:163` filter: ERROR/SENSOR_ERROR parameters
	// are NOT created as standalone data points unless the visibility
	// decider has un-ignored them for this device model. Without the
	// un-ignore exception 4 lock-family devices lose their
	// ERROR/ERROR_JAMMED DP — measured against
	// snapshot baseline ( only_py finding).
	if isDeviceErrorEvent(string(param)) && !parameterIsUnIgnored {
		return nil
	}

	if pd.Operations.IsWritable() {
		return resolveWritable(cfg, param, pd)
	}
	return resolveReadonly(cfg, param, pd)
}

// ImpulseEvents is the set of parameter names treated as impulse events.
// Parameters in this set are not created as standalone data points; they
// surface as device-level events instead.
//
// Derived from [modevent.Sources], not restated: the membership belongs to
// the classifier that also has to emit the event. Restating it here meant
// SEQUENCE_OK was declared twice, and the two copies were free to drift —
// a name added on this side alone suppresses a data point for an event
// nothing emits, and one added on the classifier's side alone emits an event
// beside a data point that should not exist.
var ImpulseEvents = impulseEventNames()

// impulseEventNames projects the classifier's impulse parameters onto the
// plain-string keys this package resolves parameters by.
func impulseEventNames() map[string]struct{} {
	params := modevent.Sources(modevent.KindImpulse)
	out := make(map[string]struct{}, len(params))
	for _, p := range params {
		out[string(p)] = struct{}{}
	}
	return out
}

// isImpulseEvent reports whether the parameter name is in [ImpulseEvents],
// which is the classifier's own impulse set projected onto strings — so this
// answers the same question [modevent.Classify] does, for the same reason
// [isDeviceErrorEvent] delegates: suppressing the data point is only safe
// while the classifier keeps the parameter.
func isImpulseEvent(parameter string) bool {
	_, ok := ImpulseEvents[parameter]
	return ok
}

// isDeviceErrorEvent reports whether the parameter is a device-error
// parameter, which is suppressed as a stateful data point and surfaces as a
// device-trigger event instead.
//
// The verdict is asked of [modevent.Classify] rather than restated here.
// Suppressing the data point is only safe because the classifier keeps the
// parameter: a name this side suppressed but the classifier did not know
// reached no plane at all — no data point, no device-trigger event, no
// broadcast. A bare HasPrefix here answered true for /^ERROR[^_]/ names
// (ERRORCODE, ERRORS) that the classifier's exact-or-underscore rule rejects,
// so the two rules were the same rule in name only.
func isDeviceErrorEvent(parameter string) bool {
	k, ok := modevent.Classify(hmenum.Parameter(parameter))
	return ok && k == modevent.KindDeviceError
}

// buttonActionParameters are write-only ACTION parameters that are rendered
// as a stateless button.
var buttonActionParameters = map[hmenum.Parameter]struct{}{
	hmenum.ParameterResetMotion:   {},
	hmenum.ParameterResetPresence: {},
}

// binarySensorValueLists enumerates the VALUE_LIST shapes that
// The paired string is the "true" value.
var binarySensorValueLists = map[[2]string]string{
	{"CLOSED", "OPEN"}:       "OPEN",
	{"DRY", "RAIN"}:          "RAIN",
	{"STABLE", "NOT_STABLE"}: "NOT_STABLE",
}

func isBinarySensor(pd hmproto.ParameterData) bool {
	if pd.Type == hmenum.ParameterTypeBool {
		return true
	}
	if len(pd.ValueList) == 2 {
		key := [2]string{pd.ValueList[0], pd.ValueList[1]}
		if _, ok := binarySensorValueLists[key]; ok {
			return true
		}
	}
	return false
}

// withKind returns a copy of cfg with the resolved [generic.ResolvedKind]
// stamped onto Config.Kind. The pipeline does not pre-set Config.Kind
// (it does not know which constructor will fire), so the resolver
// stamps it here — without it `(*generic.DataPoint).Category()` would
// return [hmenum.DataPointCategoryUndefined] for every non-custom DP.
func withKind(cfg generic.Spec, k generic.ResolvedKind) generic.Spec {
	cfg.Kind = k
	return cfg
}

func resolveWritable(cfg generic.Spec, param hmenum.Parameter, pd hmproto.ParameterData) device.ParameterDataPoint {
	if pd.Type == hmenum.ParameterTypeAction {
		return resolveAction(cfg, param, pd)
	}
	// Write-only (non-ACTION).
	if pd.Operations == hmenum.OperationsWrite {
		if len(pd.ValueList) > 0 {
			return generic.NewActionSelect(withKind(cfg, generic.KindActionSelect))
		}
		switch pd.Type { //nolint:exhaustive // ACTION handled above; other types fall through to generic Action
		case hmenum.ParameterTypeFloat:
			return generic.NewActionFloat(withKind(cfg, generic.KindActionFloat))
		case hmenum.ParameterTypeInteger:
			return generic.NewActionInteger(withKind(cfg, generic.KindActionInteger))
		case hmenum.ParameterTypeBool:
			return generic.NewActionBoolean(withKind(cfg, generic.KindActionBoolean))
		case hmenum.ParameterTypeString:
			return generic.NewActionString(withKind(cfg, generic.KindActionString))
		}
		return generic.NewAction(withKind(cfg, generic.KindAction))
	}
	// Read + write.
	switch pd.Type { //nolint:exhaustive // ACTION + DUMMY + empty fall through to nil
	case hmenum.ParameterTypeBool:
		return generic.NewSwitch(withKind(cfg, generic.KindSwitch))
	case hmenum.ParameterTypeEnum:
		return generic.NewSelect(withKind(cfg, generic.KindSelect))
	case hmenum.ParameterTypeFloat:
		return generic.NewFloat(withKind(cfg, generic.KindNumberFloat))
	case hmenum.ParameterTypeInteger:
		return generic.NewInteger(withKind(cfg, generic.KindNumberInteger))
	case hmenum.ParameterTypeString:
		return generic.NewText(withKind(cfg, generic.KindText))
	}
	return nil
}

func resolveAction(cfg generic.Spec, param hmenum.Parameter, pd hmproto.ParameterData) device.ParameterDataPoint {
	if pd.Operations == hmenum.OperationsWrite {
		if _, ok := buttonActionParameters[param]; ok {
			return generic.NewButton(withKind(cfg, generic.KindButton))
		}
		if len(pd.ValueList) > 0 {
			return generic.NewActionSelect(withKind(cfg, generic.KindActionSelect))
		}
		return generic.NewAction(withKind(cfg, generic.KindAction))
	}
	if param.IsClickEvent() {
		return generic.NewButton(withKind(cfg, generic.KindButton))
	}
	// Read+write ACTION → switch.
	return generic.NewSwitch(withKind(cfg, generic.KindSwitch))
}

func resolveReadonly(cfg generic.Spec, param hmenum.Parameter, pd hmproto.ParameterData) device.ParameterDataPoint {
	if param.IsClickEvent() {
		// Click-event parameters (PRESS_SHORT / PRESS_LONG / …) are EVENT-only on
		// the wire (OPERATIONS bit 4, no READ, no WRITE). Returning nil here meant
		// no DP was attached to the channel, so [ChannelInspector.HasParameter]
		// reported false and the HA `event` discovery never fired HmIP-BSM /
		// HmIP-WRC / HmIP-MOD-RC8 buttons never showed up as entities.
		return generic.NewButton(withKind(cfg, generic.KindButton))
	}
	if isBinarySensor(pd) {
		return generic.NewBinarySensor(withKind(cfg, generic.KindBinarySensor))
	}
	switch pd.Type { //nolint:exhaustive // ACTION + BOOL handled above; DUMMY + empty fall through to nil
	case hmenum.ParameterTypeFloat:
		return generic.NewFloatSensor(withKind(cfg, generic.KindSensor))
	case hmenum.ParameterTypeInteger, hmenum.ParameterTypeEnum:
		return generic.NewIntegerSensor(withKind(cfg, generic.KindSensor))
	case hmenum.ParameterTypeString:
		return generic.NewStringSensor(withKind(cfg, generic.KindSensor))
	}
	return nil
}
