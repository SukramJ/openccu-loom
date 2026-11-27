// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package payload

import (
	"fmt"
	"time"

	hapayload "sukramj.dev/go-hamqtt/payload"
)

// ErrServiceMissingParam is returned by the Param* decoders when a
// required key is absent from the service-method request body.
// Wrapped with the offending key name for diagnostics.
//
// It is an alias of [hapayload.ErrMissingParam], not a second sentinel:
// the coercions moved into the shared module (ADR 0070's move-up
// measurement, step C) and the error identity moved with them, so an
// `errors.Is` written against either name matches an error produced by
// either side. The daemon-local name stays because it is this package's
// error vocabulary -- twenty-odd call sites across internal/model wrap
// it with their own message, and the custom-DP dispatcher matches it to
// turn a bad service call into a client error rather than an internal
// fault -- and renaming those would be churn on log text for no gain.
var ErrServiceMissingParam = hapayload.ErrMissingParam

// ErrServiceInvalidParam is returned when a key is present but its
// value cannot be coerced to the expected Go type. An alias of
// [hapayload.ErrInvalidParam]; see [ErrServiceMissingParam].
var ErrServiceInvalidParam = hapayload.ErrInvalidParam

// ParamBool decodes a required bool param.
//
// The implementation is [hapayload.ParamBool]. Its coercions are not
// arbitrary and its doc comment carries the reasons: Home Assistant's
// templating decides what Go type reaches the wire, so a `{{ value }}`
// template sends the string "42" where the author meant a number and
// `payload_on` sends whatever the platform's default spelling is.
//
// Note in particular what this decoder does NOT accept, because the
// asymmetry is deliberate: its spelling list is exact and excludes
// "yes"/"no", while internal/parameter's CCU-side `asBool` is
// case-insensitive and does accept them. The two coerce different
// boundaries -- north-bound service-call JSON here, CCU wire values
// against a parameter descriptor there -- and are not meant to
// converge. See the comment on `asBool` in internal/parameter/coerce.go.
func ParamBool(params map[string]any, key string) (bool, error) {
	return hapayload.ParamBool(params, key)
}

// ParamFloat64 decodes a required float64 param. The implementation is
// [hapayload.ParamFloat64], which parses a numeric string strictly, so
// "42xyz" is an error rather than 42.
func ParamFloat64(params map[string]any, key string) (float64, error) {
	return hapayload.ParamFloat64(params, key)
}

// ParamInt32 decodes a required int32 param. The implementation is
// [hapayload.ParamInt32], which treats an out-of-range input as an error
// rather than truncating it -- truncation would arrive as a write to the
// wrong thing rather than as a rejected command. Note that a fractional
// float64 IS truncated, which the shared doc comment now says outright.
func ParamInt32(params map[string]any, key string) (int32, error) {
	return hapayload.ParamInt32(params, key)
}

// TransitionKey is the key under which every custom data point's service
// methods take a transition time: the light's turn_on and set_level, the
// sound player's turn_on. It is the key Home Assistant's JSON-schema light
// sends on the MQTT command topic
// (homeassistant/components/mqtt/light/schema_json.py), so one name serves
// every plane.
const TransitionKey = "transition"

// ParamTransition decodes the optional transition of a service call, in
// seconds. Absent or zero is no transition — Home Assistant clamps the value
// to 0..6553 and sends 0 for an instant change
// (homeassistant/components/light/const.py, VALID_TRANSITION). A negative or
// non-numeric value is [ErrServiceInvalidParam]: a command that names a
// transition the device would not run is refused rather than half carried
// out.
func ParamTransition(params map[string]any) (*time.Duration, error) {
	if _, ok := params[TransitionKey]; !ok {
		return nil, nil //nolint:nilnil // absent is a valid, distinct answer: no transition
	}
	secs, err := ParamFloat64(params, TransitionKey)
	if err != nil {
		return nil, err
	}
	if secs < 0 {
		return nil, fmt.Errorf("%w: %q must not be negative, got %v", ErrServiceInvalidParam, TransitionKey, secs)
	}
	if secs == 0 {
		return nil, nil //nolint:nilnil // zero is an instant change: no transition
	}
	d := time.Duration(secs * float64(time.Second))
	return &d, nil
}

// ParamString decodes a required string param. The implementation is
// [hapayload.ParamString], which formats a numeric or bool value to its
// canonical Go string form.
func ParamString(params map[string]any, key string) (string, error) {
	return hapayload.ParamString(params, key)
}
