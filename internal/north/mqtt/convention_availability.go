// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

// hmAvailableTemplate reads the per-datapoint availability flag out of a
// status object. Under mqtt-smarthome 2.0 the flag is a project field and
// lives under this daemon's extension key `hm` (ADR 0083), so the shared
// module's own self-availability fallback — which reads the old envelope's
// top-level `available` and is dropped under the status-object encoding —
// cannot express it.
const hmAvailableTemplate = `{{ value_json.hm.available | lower }}`

// connectedAvailability is the availability entry for the instance's
// `<base>/connected` topic: available at level 2 (at least one central
// reachable), through the shared [hadiscovery.ConnectedAvailability] so the
// template is the one the bridges render.
func connectedAvailability(connected string) hadiscovery.AvailabilityEntry {
	return hadiscovery.ConnectedAvailability(connected, hadiscovery.ConnectedOperational)
}

// onlineAvailability is the availability entry for a boolean status item —
// a device's, a central's, the alarm zone's or the security plane's `online`
// item, and a program's `execute_available`.
func onlineAvailability(t string) hadiscovery.AvailabilityEntry {
	return hadiscovery.OnlineAvailability(t, hadiscovery.StatusObjectEncoding)
}

// conventionAvailability rewrites an availability list the shared render
// pipeline resolved into the vocabulary of ADR 0083.
//
// Every plane of this daemon renders its topics through a layout of its own,
// and none of them is a [hatopic.SmartHomeLayout]: that capability would also
// switch the default `value_template` of every entity to the status-object
// one, which the custom-DP and hub planes — each of whose entities names its
// own template — must not acquire as a side effect. So the planes keep their
// layouts and resolve availability exactly as before, and each one's context
// passes the list through here, which maps each entry by what it addresses:
//
//   - the instance's `connected` topic → available at ≥ 2;
//   - the old envelope's own `available` flag → the same flag under `hm`;
//   - an explicit availability binding that read the envelope's value →
//     the status object's boolean `val`;
//   - every other entry is a reachability item, which under the convention
//     is a boolean status item.
//
// Each entry carries the four keys of a list entry and nothing else, which
// is what Home Assistant accepts inside an `availability` list.
func conventionAvailability(entries []hadiscovery.AvailabilityEntry, connected string) []hadiscovery.AvailabilityEntry {
	if len(entries) == 0 {
		return entries
	}
	out := make([]hadiscovery.AvailabilityEntry, 0, len(entries))
	for _, e := range entries {
		switch {
		case e.Topic == connected:
			out = append(out, connectedAvailability(connected))
		case e.ValueTemplate == hadiscovery.AvailabilityTemplate:
			out = append(out, hadiscovery.AvailabilityEntry{
				Topic:               e.Topic,
				ValueTemplate:       hmAvailableTemplate,
				PayloadAvailable:    hadiscovery.PayloadTrue,
				PayloadNotAvailable: hadiscovery.PayloadFalse,
			})
		case e.ValueTemplate == hadiscovery.SelfAvailabilityTemplate:
			out = append(out, hadiscovery.AvailabilityEntry{
				Topic:               e.Topic,
				ValueTemplate:       hadiscovery.StatusBoolValueTemplate,
				PayloadAvailable:    hadiscovery.PayloadTrue,
				PayloadNotAvailable: hadiscovery.PayloadFalse,
			})
		default:
			out = append(out, onlineAvailability(e.Topic))
		}
	}
	return out
}
