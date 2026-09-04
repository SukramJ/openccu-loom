// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"fmt"
	"time"
)

// ParseOnDuration reads the duration of a siren turn_on out of a service
// payload, for every plane that can carry one.
//
// It exists because the two planes disagreed on the same key. The invoke
// plane (REST, WebSocket, the MQTT cdp-invoke topic) read a bare `duration`
// number as milliseconds; the siren's own service handler, which is what the
// per-service MQTT topic reaches, read it as seconds. `{"duration": 30}` wrote
// DURATION_VALUE=0 through one and DURATION_VALUE=30 through the other — same
// key, same device, two wire values, and no error on either path.
//
// Seconds is the unit, because that is what Home Assistant's MQTT siren sends
// back for `duration` and what the service handler has always done. The
// canonical key is `seconds`, the shape every other timed operation uses; the
// two aliases are kept so existing clients keep working.
//
// Returns (0, false, nil) when no key is present — a caller that makes the
// duration optional relies on the bool. A value that is present and
// unreadable is an error rather than a silent fall-through to the device's
// default: the service handler used to drop `{"duration": "5s"}` without
// telling anyone.
func ParseOnDuration(params map[string]any) (time.Duration, bool, error) {
	for _, key := range []string{"seconds", "duration_seconds"} {
		raw, ok := params[key]
		if !ok {
			continue
		}
		secs, err := durationNumber(raw)
		if err != nil {
			return 0, true, fmt.Errorf("param %q: %w", key, err)
		}
		return time.Duration(secs * float64(time.Second)), true, nil
	}
	raw, ok := params["duration"]
	if !ok {
		return 0, false, nil
	}
	if s, isString := raw.(string); isString {
		dur, err := time.ParseDuration(s)
		if err != nil {
			return 0, true, fmt.Errorf("param %q: cannot parse %q as a duration: %w", "duration", s, err)
		}
		return dur, true, nil
	}
	secs, err := durationNumber(raw)
	if err != nil {
		return 0, true, fmt.Errorf("param %q: %w", "duration", err)
	}
	return time.Duration(secs * float64(time.Second)), true, nil
}

// durationNumber narrows the JSON scalar types a duration can arrive as.
func durationNumber(raw any) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("cannot read %T as a number of seconds", raw)
	}
}
