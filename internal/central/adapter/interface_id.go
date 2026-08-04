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

// IDPrefix marks every interface_id the daemon registers with a CCU as ours.
// The CCU shows the raw interface_id in its own logs and diagnostics — an id
// built only from host and central names is indistinguishable from any other
// XML-RPC client, so an operator reading `rfd` output cannot tell which
// process a registration belongs to.
const IDPrefix = "loom"

// InitInterfaceID returns the wire-boundary interface identifier the daemon
// advertises to the CCU at init()/deinit() (XML-RPC) and registers on the
// BIN-RPC callback server (CUxD). Format:
// `loom-<instance_name>-<central_name>-<interface>`.
//
// This id is used ONLY at the CCU wire boundary, never internally. The
// `instance_name` (daemon identity) makes it unique across daemons so two
// daemons against the same CCU coexist: the CCU keys its callback registry by
// interface_id, and a duplicate value during init() would overwrite the prior
// daemon's registration. CUxD likewise routes its callbacks by interface_id,
// so the BIN-RPC callback-server registration uses this id too.
//
// The instance name is omitted when it equals the central name. Both default
// to a host-derived value, so running as the CCU's own add-on otherwise
// repeats the same name twice
// (`RM-Test-VM-96-RM-Test-VM-96-BidCos-RF`) — noise in every CCU log line
// that carries the id, with no added uniqueness: the repetition is a
// consequence of where the daemon runs, not of which daemon it is.
//
// The CCU echoes this id back in every callback envelope;
// [CanonicalInterfaceID] maps it back to the [WireInterfaceID] used by the
// stamped devices and registries.
//
// An empty centralName yields `loom-<instance_name>-<interface>`; both empty
// yields `loom-<interface>` (tests and tooling).
func InitInterfaceID(instanceName, centralName string, iface hmenum.Interface) string {
	parts := make([]string, 0, 4)
	parts = append(parts, IDPrefix)
	if instanceName != "" && instanceName != centralName {
		parts = append(parts, instanceName)
	}
	if centralName != "" {
		parts = append(parts, centralName)
	}
	return strings.Join(append(parts, string(iface)), "-")
}

// CanonicalInterfaceID maps an interface_id echoed by the CCU back to the
// canonical host-independent [WireInterfaceID] (`<central>-<interface>`),
// inverting [InitInterfaceID].
//
// An id without the [IDPrefix] is treated as one built by a pre-prefix
// release: those always carried the instance name, so a callback still in
// flight across an upgrade resolves to the same canonical id instead of
// arriving under an id no registry knows. An id belonging to neither shape is
// returned unchanged.
func CanonicalInterfaceID(instanceName, centralName, id string) string {
	if rest, ok := strings.CutPrefix(id, IDPrefix+"-"); ok {
		// The instance name is only present when it differs from the central
		// name; stripping it unconditionally would eat the central name in the
		// collapsed form.
		if instanceName != "" && instanceName != centralName {
			rest = strings.TrimPrefix(rest, instanceName+"-")
		}
		return rest
	}
	if instanceName != "" {
		return strings.TrimPrefix(id, instanceName+"-")
	}
	return id
}

// BareInterfaceFromWireID inverts [WireInterfaceID]: it strips the
// central-name prefix from a canonical `<central>-<iface>` id and returns
// the bare interface. An id without the prefix (empty central name) is
// returned unchanged.
func BareInterfaceFromWireID(centralName, wireID string) hmenum.Interface {
	if centralName != "" {
		if bare, ok := strings.CutPrefix(wireID, centralName+"-"); ok {
			return hmenum.Interface(bare)
		}
	}
	return hmenum.Interface(wireID)
}
