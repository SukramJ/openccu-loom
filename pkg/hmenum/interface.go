// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hmenum collects the Homematic-domain enumerations shared
// across the daemon. Wire strings are stable because recorded sessions,
// paramset patches, and generated device profiles all refer to them.
package hmenum

// Interface is the identifier of a CCU interface process. Its String()
// value is exactly the token used in XML-RPC / JSON-RPC URLs, logs, and
// config files; do not change casing.
type Interface string

// Interface values. The string form is the wire token used in CCU URLs
// and config files — do not change casing.
//
// HmIP-Wired is deliberately absent: the CCU exposes a single HmIP-RF
// XML-RPC service that hosts both RF and Wired devices. The
// HmIP-Wired flavour is a [ProductGroup] (ProductGroupHmIPW), derived
// from the device model-name prefix via [ProductGroupForModel].
const (
	InterfaceHmIPRF         Interface = "HmIP-RF"
	InterfaceBidCosRF       Interface = "BidCos-RF"
	InterfaceBidCosWired    Interface = "BidCos-Wired"
	InterfaceVirtualDevices Interface = "VirtualDevices"
	InterfaceCUxD           Interface = "CUxD"
)

// String returns the wire representation.
func (i Interface) String() string { return string(i) }

// IsXMLRPC reports whether the interface speaks XML-RPC.
func (i Interface) IsXMLRPC() bool { _, ok := XMLRPCInterfaces[i]; return ok }

// IsBINRPC reports whether the interface speaks BIN-RPC (CUxD).
func (i Interface) IsBINRPC() bool { _, ok := BINRPCInterfaces[i]; return ok }

// IsJSONRPCOnly reports whether the interface is pull-only via JSON-RPC.
// Empty — CCU-Jack ist gestrichen, alle ausgelieferten Interfaces unter-
// stützen Push.
func (i Interface) IsJSONRPCOnly() bool { _, ok := JSONRPCOnlyInterfaces[i]; return ok }

// SupportsRPCCallback reports whether the CCU can push events to us for
// this interface (XML-RPC callback for HTTP interfaces, BIN-RPC for CUxD).
func (i Interface) SupportsRPCCallback() bool {
	_, ok := InterfacesSupportingRPCCallback[i]
	return ok
}

// SupportsFirmwareUpdates reports whether firmware updates can be
// triggered for devices on this interface.
func (i Interface) SupportsFirmwareUpdates() bool {
	_, ok := InterfacesSupportingFirmwareUpdates[i]
	return ok
}

// PushesConfigPending reports whether this interface — taken on its
// own — delivers reliable CONFIG_PENDING events on MASTER writes.
// True for HmIP-*; false for BidCos-* (CONFIG_PENDING is unreliable;
// `interface_client.put_paramset` polls MASTER on BidCos for that
// reason), CUxD (synchronous), and the bulk of the VirtualDevices
// interface.
//
// VirtualDevices is mixed: it hosts HmIP-flavored virtual devices
// (e.g. HmIP-HEATING groups) where CONFIG_PENDING does work, plus
// BidCos-flavored ones where it doesn't. The interface itself can't
// answer that — callers should additionally consult
// [PushesConfigPendingFor], which factors in the device's product
// group / model name and falls back to this interface check.
func (i Interface) PushesConfigPending() bool {
	_, ok := InterfacesPushingConfigPending[i]
	return ok
}

// PushesConfigPendingFor reports whether MASTER paramset writes for a
// *device* described by (interface, productGroup) emit reliable
// CONFIG_PENDING events. The product group wins over the interface
// classification — an HmIP-HEATING virtual device sits on the VirtualDevices
// interface but behaves like an HmIP-RF device for the purposes of
// CONFIG_PENDING.
func PushesConfigPendingFor(iface Interface, group ProductGroup) bool {
	switch group { //nolint:exhaustive // Virtual and Unknown groups fall through to the interface-level PushesConfigPending check
	case ProductGroupHmIP, ProductGroupHmIPW:
		return true
	case ProductGroupHM, ProductGroupHmW:
		return false
	}
	return iface.PushesConfigPending()
}

// Classification sets that drive protocol selection and capability
// computation. Keep in sync with SPECIFICATION.md §5.1 — the contract
// tests in tests/contract/ assert exactly these memberships.
var (
	// XMLRPCInterfaces speak XML-RPC over HTTP with an HTTP callback.
	XMLRPCInterfaces = map[Interface]struct{}{
		InterfaceHmIPRF:         {},
		InterfaceBidCosRF:       {},
		InterfaceBidCosWired:    {},
		InterfaceVirtualDevices: {},
	}

	// BINRPCInterfaces speak BIN-RPC over raw TCP with a BIN-RPC callback.
	BINRPCInterfaces = map[Interface]struct{}{
		InterfaceCUxD: {},
	}

	// JSONRPCOnlyInterfaces speak only JSON-RPC (no push). Permanent
	// EMPTY — CCU-Jack ist gestrichen.
	JSONRPCOnlyInterfaces = map[Interface]struct{}{}

	// InterfacesSupportingRPCCallback is the union of push-capable
	// interfaces. Equals every configurable interface.
	InterfacesSupportingRPCCallback = unionInterfaces(XMLRPCInterfaces, BINRPCInterfaces)

	// InterfacesRequiringPeriodicRefresh is an alias of JSONRPCOnlyInterfaces
	// — leer, da alle Interfaces pushen.
	InterfacesRequiringPeriodicRefresh = JSONRPCOnlyInterfaces

	// InterfacesSupportingFirmwareUpdates lists the interfaces for which
	// firmware update triggers are defined.
	InterfacesSupportingFirmwareUpdates = map[Interface]struct{}{
		InterfaceBidCosRF:    {},
		InterfaceBidCosWired: {},
		InterfaceHmIPRF:      {},
	}

	// LinkableInterfaces is an alias for the firmware-updatable set;
	// the CCU uses the same membership for both concepts.
	LinkableInterfaces = InterfacesSupportingFirmwareUpdates

	// InterfacesPushingConfigPending lists the interfaces that emit
	// reliable CONFIG_PENDING events on a MASTER write. HmIP devices
	// Do; BidCos devices do not (
	// post-write polling pass in interface_client.py:964-971, citing
	// "CONFIG_PENDING unreliable" in model/device.py:856).
	// VirtualDevices and CUxD are synchronous and don't participate
	// in the CONFIG_PENDING flow either.
	//
	// The SPA reads the per-device projection of this set
	// (DeviceSummary.master_pushes_config_pending) to choose its
	// post-save refresh strategy.
	InterfacesPushingConfigPending = map[Interface]struct{}{
		InterfaceHmIPRF: {},
	}

	// PrimaryClientCandidateInterfaces is the preferred set of interfaces used
	// when selecting a primary InterfaceClient for operations that require only
	// one client (e.g. JSON-RPC facade, device-info queries).
	PrimaryClientCandidateInterfaces = map[Interface]struct{}{
		InterfaceHmIPRF:      {},
		InterfaceBidCosRF:    {},
		InterfaceBidCosWired: {},
	}
)

// InterfaceRPCServerType maps each Interface to the [RPCServerType] that
// handles its callbacks. The mapping drives the dispatcher inside the
// callback servers — unknown interfaces default to [RPCServerTypeNone].
//
// Note: CUxD maps to [RPCServerTypeNone] here because openccu-loom routes
// CUxD through a dedicated BIN-RPC callback server, not the XML-RPC
// server. Callers that need to distinguish the BIN-RPC path should check
// [Interface.IsBINRPC] in addition to consulting this map.
var InterfaceRPCServerType = map[Interface]RPCServerType{
	InterfaceBidCosRF:       RPCServerTypeXMLRPC,
	InterfaceBidCosWired:    RPCServerTypeXMLRPC,
	InterfaceHmIPRF:         RPCServerTypeXMLRPC,
	InterfaceVirtualDevices: RPCServerTypeXMLRPC,
	InterfaceCUxD:           RPCServerTypeNone,
}

func unionInterfaces(sets ...map[Interface]struct{}) map[Interface]struct{} {
	out := make(map[Interface]struct{})
	for _, s := range sets {
		for k := range s {
			out[k] = struct{}{}
		}
	}
	return out
}
