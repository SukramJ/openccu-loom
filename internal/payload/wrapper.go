// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package payload

import "time"

// PerDPState is what the MQTT bridge renders into the status object of one
// data point's status item (`<base>/status/…/<bucket>/<param>`, ADR 0083):
//
//	{"val": 21.6, "ts": 1730385720123, "lc": 1730385720123,
//	 "hm": {"available": true, "additional_information": {…}}}
//
// It carries only live state — descriptor metadata (unit, type, min, max,
// default, value_list, source) lives on the retained `meta` companion and is
// not duplicated here on every value event.
//
// `Value` is `any` because a parameter can be a bool, int, float, string, or
// string-list depending on its descriptor; the bridge marshals it via
// `encoding/json` directly.
type PerDPState struct {
	// Value is the parameter's current observed value, the status object's
	// `val`. nil when the parameter has never been observed (rare — bridge
	// gates such publishes upstream); it renders as `null`.
	Value any

	// Available reports whether the bridge currently considers the
	// parameter readable — `hm.available`.
	Available bool

	// ObservedAt is when the CCU reported the value — the status object's
	// `ts`. Zero means the time of the publish. `lc`, the time the value
	// last changed, is tracked by the bridge per topic.
	ObservedAt time.Time

	// AdditionalInformation carries enriched model metadata (e.g. battery
	// type / quantity / low-voltage limits for a battery-backed device)
	// when the data point provides it — `hm.additional_information`. nil
	// for plain scalar data points, and then omitted.
	AdditionalInformation map[string]any
}
