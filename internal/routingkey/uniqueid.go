// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package routingkey is the Go side of the cross-backend routing-key
// contract: the algorithm that turns a (central, address, parameter)
// triple into the stable Home Assistant value-change routing key.
//
// Multiple backends rebuild this key independently and MUST produce
// bit-identical output, otherwise events route to the wrong entity (or
// no entity) and Home Assistant loses every entity's history on
// cutover. The canonical artefact is the golden fixture set the
// contract test under tests/contract/ replays against these functions;
// see docs/external-clients/ha-drop-in-identity-and-scoping.md for the
// background and the owner split.
//
// This package is deliberately a faithful, dependency-light port: it
// is the algorithm-of-record on the Go side, not a convenience wrapper
// around the daemon's internal data-point identity.
package routingkey

import "strings"

// internalAddressPrefix marks CCU-internal addresses (e.g. INT0001234).
// Their key is namespaced by the central so identical internal IDs on
// two CCUs do not collide.
const internalAddressPrefix = "INT000"

// virtualRemoteRoots are the address roots whose channels are derived
// from a virtual remote bus. Their key carries the central prefix even
// though the channel address itself repeats across CCUs.
var virtualRemoteRoots = map[string]struct{}{
	"BidCoS-RF":  {},
	"BidCoS-Wir": {},
	"HmIP-RCV-1": {},
}

// Hub-level pseudo-addresses. These occupy the address slot of the routing
// key for entities that have no real CCU device address — the hub singletons,
// install-mode, programs and system variables. They are the central-slot
// fillers external clients otherwise import from the reference stack's
// constants; exporting them as named constants (surfaced through the generated
// schemas, see script/export_schemas.go) lets a wire client consume them from
// the daemon contract instead. The canonical key namespaces them by central.
const (
	// HubAddress is the pseudo-address for hub-singleton data points.
	HubAddress = "hub"
	// InstallModeAddress is the pseudo-address for the per-interface
	// install-mode entities.
	InstallModeAddress = "install_mode"
	// ProgramAddress is the pseudo-address for CCU program entities.
	ProgramAddress = "program"
	// SysvarAddress is the pseudo-address for system-variable entities.
	SysvarAddress = "sysvar"
)

// PseudoAddresses lists every hub-level pseudo-address in stable order, for
// the schema exporter and any client that needs to enumerate them.
var PseudoAddresses = []string{HubAddress, InstallModeAddress, ProgramAddress, SysvarAddress}

// centralPrefixAddresses are the hub-level pseudo-addresses whose key is
// namespaced by the central.
var centralPrefixAddresses = map[string]struct{}{
	HubAddress:         {},
	InstallModeAddress: {},
	ProgramAddress:     {},
	SysvarAddress:      {},
}

// foldAddress folds the two address separators (":" and "-") to "_".
// It is applied to the address only — a "-" inside a parameter (e.g. a
// hub slug like "aussen-temperatur") must survive into the final key.
func foldAddress(address string) string {
	folded := strings.ReplaceAll(address, ":", "_")
	return strings.ReplaceAll(folded, "-", "_")
}

// addressRoot returns the segment before the first ":" (the channel
// separator), or the whole address when it carries no channel suffix.
func addressRoot(address string) string {
	if before, _, ok := strings.Cut(address, ":"); ok {
		return before
	}
	return address
}

// NeedsCentralScope reports whether an address only becomes unique once
// the CCU's serial is prepended — the hub pseudo-addresses, the internal
// INT000* addresses, and the virtual-remote buses, all of which repeat
// verbatim on every CCU.
//
// A north-bound plane that keys entities by unique_id has to consult
// this before publishing without a serial: two CCUs would otherwise
// declare the identical id, and the consumer keeps whichever arrived
// first. Home Assistant does exactly that, and the payload is retained,
// so the loss outlives the daemon that caused it.
func NeedsCentralScope(address string) bool { return needsCentralPrefix(address) }

// needsCentralPrefix reports whether the parameter-level key for the
// given address is namespaced by the central: hub-level pseudo
// addresses, internal addresses, and virtual-remote channels.
func needsCentralPrefix(address string) bool {
	if _, ok := centralPrefixAddresses[address]; ok {
		return true
	}
	if strings.HasPrefix(address, internalAddressPrefix) {
		return true
	}
	_, ok := virtualRemoteRoots[addressRoot(address)]
	return ok
}

// GenerateUniqueID builds the parameter-level routing key.
//
// The shape is:
//
//	<folded-address>[_<parameter>]                  (normal device)
//	<prefix>_<folded-address>[_<parameter>]         (events, buttons)
//	<central>_<prefix?>_<folded-address>[_<param>]  (hub / internal / virtual remote)
//
// The address separators ":" and "-" fold to "_"; an optional parameter
// is appended verbatim (its own "-" survives); an optional prefix is
// prepended; the central prefix is prepended last for hub-level,
// internal, and virtual-remote addresses; the whole result is lowercased.
func GenerateUniqueID(centralID, address, parameter, prefix string) string {
	uid := foldAddress(address)
	if parameter != "" {
		uid = uid + "_" + parameter
	}
	if prefix != "" {
		uid = prefix + "_" + uid
	}
	if needsCentralPrefix(address) {
		uid = centralID + "_" + uid
	}
	return strings.ToLower(uid)
}

// GenerateChannelUniqueID builds the channel/device-level routing key.
// It carries no parameter and, unlike [GenerateUniqueID], prepends the
// central only for virtual-remote addresses — not for hub-level or
// internal ones.
func GenerateChannelUniqueID(centralID, address string) string {
	uid := foldAddress(address)
	if _, ok := virtualRemoteRoots[addressRoot(address)]; ok {
		uid = centralID + "_" + uid
	}
	return strings.ToLower(uid)
}
