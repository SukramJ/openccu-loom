// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

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
// The join itself lives in [hmtypes.NewWireInterfaceID], next to the type the
// per-central registries are keyed by: a second, private join would be the same
// rule written twice, and the copy that drifts is silent. This function stays
// because the plain string form is what device.Device.InterfaceID, the backend
// registry, ping/pong and the MQTT topics carry — reach for
// [hmtypes.NewWireInterfaceID] directly wherever a registry key is wanted, so
// the compiler keeps the two identifier spaces apart.
func WireInterfaceID(centralName string, iface hmenum.Interface) string {
	return hmtypes.NewWireInterfaceID(centralName, iface).String()
}
