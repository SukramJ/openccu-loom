// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// WireInterfaceID returns the canonical, host-independent interface
// identifier used EVERYWHERE inside the daemon: on
// [hmtypes.DataPointKey], the value-writer key, the Clients registry,
// the backend registry, the state-changed bus, ping/pong, the master
// poller, device stamping + matching, MQTT topics, and the REST/WS/SPA
// surfaces. Format: `<central_name>-<interface>` (ADR-0024 option B).
//
// The `central_name` keeps the id distinct per CCU so addresses that
// repeat across CCUs (BidCoS-RF:1, INT000*, VCU virtual remotes) do not
// collide on the InterfaceID — [hmtypes.DataPointKey] carries no
// separate central field, so per-CCU scoping rides entirely on this
// identifier. The daemon hostname (instance name) is deliberately NOT
// part of this id: it would leak into MQTT topics and the external API
// and make the surface host-dependent.
//
// An empty centralName yields the bare interface (used by tests and
// tooling).
//
// The rule lives in the central domain rather than in the southbound
// adapter because domains above the adapter need it too: anything that
// receives a bare CCU interface name (the connectivity probe reports
// "BidCos-RF") and has to reconcile it with wire-keyed state must spell
// the id the same way. A second, private join would be the same rule
// written twice, and the copy that drifts is silent.
func WireInterfaceID(centralName string, iface hmenum.Interface) string {
	if centralName == "" {
		return string(iface)
	}
	return centralName + "-" + string(iface)
}
