// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmtypes

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// WireInterfaceID is the canonical, host-independent interface identifier
// `<central_name>-<interface>` that every per-central registry is keyed by and
// that the CCU echoes back on every callback.
//
// It is a distinct type — not [hmenum.Interface] — because the daemon carries
// two identifier spaces that are both "the interface": the bare interface name
// (`HmIP-RF`, the operator-facing form, carried by device.Device.Interface) and
// this wire id (`ccu1-HmIP-RF`, carried by device.Device.InterfaceID). While
// both were spelled `hmenum.Interface`, a lookup with the wrong one compiled,
// ran, and silently found nothing — an empty team-candidate list served with
// HTTP 200, firmware fields frozen for the life of the daemon, descriptions
// that survive an unpair and resurrect the device on the next boot. Giving the
// wire id its own type turns each of those into a compile error.
//
// Build one with [NewWireInterfaceID]; adopt one that is already in wire form
// with [ParseWireInterfaceID]. Do not convert a bare interface name into this
// type directly — that is the mistake the type exists to prevent.
type WireInterfaceID string

// NewWireInterfaceID joins a central name and a bare interface into the
// canonical wire id (ADR-0024 option B).
//
// The central name keeps the id distinct per CCU so addresses that repeat
// across CCUs (BidCoS-RF:1, INT000*, VCU virtual remotes) do not collide —
// [DataPointKey] carries no separate central field, so per-CCU scoping rides
// entirely on this identifier. The daemon hostname is deliberately NOT part of
// it: it would leak into MQTT topics and the external API and make the surface
// host-dependent.
//
// An empty centralName yields the bare interface (used by tests and tooling).
func NewWireInterfaceID(centralName string, iface hmenum.Interface) WireInterfaceID {
	if centralName == "" {
		return WireInterfaceID(iface)
	}
	return WireInterfaceID(centralName + "-" + string(iface))
}

// ParseWireInterfaceID adopts an id that is already in wire form — one read
// back from the CCU callback path, from a persisted descriptor row, or from
// device.Device.InterfaceID. It performs no validation: those producers are
// the authority on the format, and rejecting an unrecognised id here would
// drop a device the CCU does report.
func ParseWireInterfaceID(id string) WireInterfaceID {
	return WireInterfaceID(id)
}

// String returns the id in its wire form.
func (w WireInterfaceID) String() string { return string(w) }

// Bare strips the leading central-name prefix and returns the interface
// itself. The central name has to be supplied because the separator is an
// ordinary hyphen and two interface tokens (BidCos-Wired, HmIP-Wired) contain
// one themselves, so the split is not recoverable from the id alone. An id
// without the prefix is returned unchanged.
//
// Use it where a decision is about the radio technology (product-group
// classification, capability gates) rather than about a registry key.
func (w WireInterfaceID) Bare(centralName string) hmenum.Interface {
	if centralName != "" {
		if bare, ok := strings.CutPrefix(string(w), centralName+"-"); ok {
			return hmenum.Interface(bare)
		}
	}
	return hmenum.Interface(w)
}
