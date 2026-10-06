// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strings"

	hatopic "github.com/SukramJ/go-hamqtt/topic"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// The pre-convention layout (before ADR 0083) put every item directly below
// the base — `<base>/<central>/…`, `<base>/bridge/…`, `<base>/alarm/…`,
// `<base>/security/…`, `<base>/system/addon_update/…` — with the function as
// a suffix (`…/state`, `…/set`, `…/config`). The retained sweep clears what
// that layout left on the broker, under three rules:
//
//   - It subscribes the old trees only: one filter per configured central and
//     one per daemon-level tree. A filter over the whole base would overlap
//     the daemon's own `set` routes, and a `set` arriving during the window
//     would then be delivered twice (found in go-mtec2mqtt's migration).
//   - A topic whose first level below the base is a function name is new and
//     never touched. The reserved-name guard in the config refuses a central
//     named like a function, which is what makes this test sound.
//   - Every other topic is cleared only when it matches an exact old shape
//     for an identifier this daemon owns: a configured central, or one of its
//     own daemon-level trees. Never a prefix match — a sibling daemon on the
//     same broker keeps every topic (ADR 0070's measurement of a prefix rule
//     deleting 510 live components of a sibling instance).
//
// The sweep is idempotent and stays for the life of this major: a second run
// finds nothing, and a rollback followed by a re-upgrade is cleaned again.

// legacyLayoutTrees are the daemon-level literal trees of the old layout.
var legacyLayoutTrees = []string{"bridge", alarmTree, securityTree, "system"}

// legacyLayoutFilters are the subscription filters of the migration sweep:
// one per configured central's old tree and one per daemon-level tree, each
// disjoint from every `<base>/set/…` command route and from the new
// `status`/`meta` trees, because a central can never be named like a function.
func legacyLayoutFilters(base string, centralNames []string) []string {
	base = strings.Trim(base, "/")
	seen := map[string]bool{}
	out := make([]string, 0, len(centralNames)+len(legacyLayoutTrees))
	add := func(level string) {
		if level == "" || hatopic.IsFunction(level) || seen[level] {
			return
		}
		seen[level] = true
		out = append(out, base+"/"+level+"/#")
	}
	for _, name := range centralNames {
		add(naming.TopicSafe(name))
	}
	for _, tree := range legacyLayoutTrees {
		add(tree)
	}
	return out
}

// LegacyLayoutMatcher reports whether topic is one of the exact shapes the
// pre-ADR-0083 layout published or subscribed below base, for a configured
// central (centrals holds their escaped topic segments) or for one of the
// daemon's own literal trees. A topic whose first level below the base is a
// function name is new and never matches.
//
// Exported for the sweep's tests; production consumes it through
// [RetainCleanup.collect].
func LegacyLayoutMatcher(base string, centrals map[string]bool, topic string) bool {
	base = strings.Trim(base, "/")
	rest, ok := strings.CutPrefix(topic, base+"/")
	if !ok || rest == "" {
		return false
	}
	p := strings.Split(rest, "/")
	if slicesContainsEmpty(p) || hatopic.IsFunction(p[0]) {
		return false
	}
	switch p[0] {
	case "bridge":
		return len(p) == 2 && (p[1] == "status" || p[1] == "health")
	case alarmTree:
		return len(p) == 3 && oneOf(p[2], "state", "availability", "event", "set", "triggered-motion")
	case securityTree:
		return legacySecurityShape(p[1:])
	case "system":
		return len(p) == 3 && p[1] == "addon_update" && oneOf(p[2], "state", "set")
	}
	if !centrals[p[0]] {
		return false
	}
	return legacyCentralShape(p[1:])
}

// legacySecurityShape matches the old `<base>/security/…` items.
func legacySecurityShape(p []string) bool {
	switch len(p) {
	case 1:
		return oneOf(p[0], "state", "alarm", "problem", "health", "last_alarm", "last_fault",
			"event", "fault", "availability")
	case 2:
		return p[0] == "class" || p[0] == "zone"
	}
	return false
}

// legacyCentralShape matches the old shapes below `<base>/<central>/`: the
// hub and system items, the custom-DP operation invoke, and the device tree.
func legacyCentralShape(p []string) bool {
	if len(p) == 0 {
		return false
	}
	switch p[0] {
	case "hub":
		return legacyHubShape(p[1:])
	case "system":
		return len(p) == 2 && oneOf(p[1], "health_score", "latency", "last_event_age", "status")
	case "devices":
		// devices/<addr>/cdps/<name>/<op>/invoke
		return len(p) == 6 && p[2] == "cdps" && p[5] == "invoke"
	}
	return legacyDeviceShape(p)
}

// legacyHubShape matches the old `<central>/hub/…` items.
func legacyHubShape(p []string) bool {
	switch len(p) {
	case 1:
		return oneOf(p[0], "status", "info", "diagnostics", "update",
			"alarm_messages", "service_messages", "inbox")
	case 2:
		return oneOf(p[0], "connectivity", "install_mode")
	case 3:
		switch p[0] {
		case "sysvars":
			return oneOf(p[2], "state", "set")
		case "programs":
			return oneOf(p[2], "state", "set", "trigger", "execute_available")
		case "install_mode":
			return p[2] == "set"
		}
	}
	return false
}

// legacyDeviceShape matches the old `<central>/<iface>/<addr>/…` items: the
// device-scope leaves, and the channel items below a numeric channel.
func legacyDeviceShape(p []string) bool {
	if len(p) < 3 {
		return false
	}
	if len(p) == 3 {
		return oneOf(p[2], "availability", "info", "diagnostics", "update")
	}
	if len(p) == 4 && p[2] == "update" {
		return p[3] == "set"
	}
	if !numeric(p[2]) {
		return false
	}
	return legacyChannelShape(p[3:])
}

// legacyChannelShape matches the old items below `<…>/<addr>/<ch>/`.
func legacyChannelShape(p []string) bool {
	switch len(p) {
	case 1:
		return oneOf(p[0], "event", "impulse", "device_error")
	case 2:
		switch p[0] {
		case "values", "master", "calculated", "custom", "combined":
			return true
		case "event":
			return true // the dropped per-type pulse `event/<type>`
		case "week_profile":
			return oneOf(p[1], "state", "set")
		case segSchedule:
			return oneOf(p[1], "state", "attrs")
		}
	case 3:
		switch p[0] {
		case "values", "master", "calculated", "custom":
			return oneOf(p[2], "config", "set")
		case "combined":
			return p[2] == "set"
		case segSchedule:
			return oneOf(p[2], "state", "set")
		}
	case 4:
		// custom/<kind>/set/<method>
		return p[0] == "custom" && p[2] == "set"
	}
	return false
}

func oneOf(s string, options ...string) bool {
	for _, o := range options {
		if s == o {
			return true
		}
	}
	return false
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func slicesContainsEmpty(p []string) bool {
	for _, s := range p {
		if s == "" {
			return true
		}
	}
	return false
}
