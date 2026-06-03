// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// WireInterfaceID returns the canonical, host-independent interface
// identifier used EVERYWHERE inside the daemon: on [hmtypes.DataPointKey],
// the [client.ValueWriter] key, the Clients registry, the backend registry,
// the state-changed bus, ping/pong, the master poller, device stamping +
// matching, MQTT topics, and the REST/WS/SPA surfaces. Format:
// `<central_name>-<interface>` (ADR-0024 option B).
//
// The `central_name` keeps the id distinct per CCU so addresses that repeat
// across CCUs (BidCoS-RF:1, INT000*, VCU virtual remotes) do not collide on
// the InterfaceID — [hmtypes.DataPointKey] carries no separate central field,
// so per-CCU scoping rides entirely on this identifier. The daemon hostname
// (instance name) is deliberately NOT part of this id: it would leak into MQTT
// topics and the external API and make the surface host-dependent.
//
// An empty centralName yields the bare interface (used by tests and tooling).
func WireInterfaceID(centralName string, iface hmenum.Interface) string {
	if centralName == "" {
		return string(iface)
	}
	return centralName + "-" + string(iface)
}

// InitInterfaceID returns the wire-boundary interface identifier the daemon
// advertises to the CCU at init()/deinit() (XML-RPC) and registers on the
// BIN-RPC callback server (CUxD). Format:
// `<instance_name>-<central_name>-<interface>` (ADR-0024 option B).
//
// This triple is used ONLY at the CCU wire boundary, never internally. The
// `instance_name` (daemon identity) makes the id unique across daemons so two
// daemons against the same CCU coexist: the CCU keys its callback registry by
// interface_id, and a duplicate value during init() would overwrite the prior
// daemon's registration. CUxD likewise routes its callbacks by interface_id,
// so the BIN-RPC callback-server registration uses this triple too.
//
// The CCU echoes this triple back in every callback envelope; the inbound
// callback handler strips the `<instance_name>-` prefix with [StripInstance]
// to recover the canonical [WireInterfaceID] used by the stamped devices and
// registries.
//
// An empty instanceName falls back to the two-part [WireInterfaceID] form
// (single-daemon setups and tests); an empty centralName yields the bare
// interface.
func InitInterfaceID(instanceName, centralName string, iface hmenum.Interface) string {
	if instanceName == "" {
		return WireInterfaceID(centralName, iface)
	}
	return instanceName + "-" + WireInterfaceID(centralName, iface)
}

// StripInstance removes the leading `<instanceName>-` prefix from an
// interface_id echoed by the CCU, mapping an [InitInterfaceID] triple back to
// the canonical two-part [WireInterfaceID]. It is a no-op when instanceName is
// empty or the id does not carry the prefix.
func StripInstance(instanceName, id string) string {
	if instanceName == "" {
		return id
	}
	return strings.TrimPrefix(id, instanceName+"-")
}
