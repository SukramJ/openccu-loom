// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "strings"

// Backend names the kind of backend a central talks to.
type Backend string

// Backend values.
const (
	BackendCCU      Backend = "CCU"
	BackendHomegear Backend = "Homegear"
	BackendPyDevCCU Backend = "PyDevCCU"
)

// String returns the wire representation.
func (b Backend) String() string { return string(b) }

// CCUType discriminates between classic CCU, OpenCCU, and unknown.
type CCUType string

// CCUType values.
const (
	CCUTypeCCU     CCUType = "CCU"
	CCUTypeOpenCCU CCUType = "OpenCCU"
	CCUTypeUnknown CCUType = "Unknown"
)

// String returns the wire representation.
func (c CCUType) String() string { return string(c) }

// ProductGroup identifies a physical product family. Wire-level token
// matches the interface string for the home/wired flavours.
type ProductGroup string

// ProductGroup values.
const (
	ProductGroupHM      ProductGroup = "BidCos-RF"
	ProductGroupHmIP    ProductGroup = "HmIP-RF"
	ProductGroupHmIPW   ProductGroup = "HmIP-Wired" //nolint:gosec // G101 false positive: not a credential
	ProductGroupHmW     ProductGroup = "BidCos-Wired"
	ProductGroupVirtual ProductGroup = "VirtualDevices"
	ProductGroupUnknown ProductGroup = "unknown"
)

// String returns the wire representation.
func (p ProductGroup) String() string { return string(p) }

// ProductGroupForModel classifies a device into its [ProductGroup]
// based on the lower-cased model-name prefix, falling back to the
// CCU interface the device sits on when the prefix is unknown.
//
// The prefix list is authoritative for HmIP-Wired vs HmIP-RF because
// the CCU exposes both flavours under a single HmIP-RF service —
// only the model name (`hmipw-*` vs `hmip-*`) tells them apart. The
// interface fallback covers devices whose model name does not start
// with one of the canonical prefixes. See ADR 0023.
func ProductGroupForModel(model string, iface Interface) ProductGroup {
	l := strings.ToLower(model)
	switch {
	case strings.HasPrefix(l, "hmipw-"):
		return ProductGroupHmIPW
	case strings.HasPrefix(l, "hmip-"):
		return ProductGroupHmIP
	case strings.HasPrefix(l, "hmw-"):
		return ProductGroupHmW
	case strings.HasPrefix(l, "hm-"):
		return ProductGroupHM
	}
	switch iface { //nolint:exhaustive // CUxD shares the unknown fallback
	case InterfaceHmIPRF:
		return ProductGroupHmIP
	case InterfaceBidCosRF:
		return ProductGroupHM
	case InterfaceBidCosWired:
		return ProductGroupHmW
	case InterfaceVirtualDevices:
		return ProductGroupVirtual
	}
	return ProductGroupUnknown
}

// Manufacturer identifies the device vendor.
type Manufacturer string

// Manufacturer values.
const (
	ManufacturerEQ3         Manufacturer = "eQ-3"
	ManufacturerHB          Manufacturer = "Homebrew"
	ManufacturerMoehlenhoff Manufacturer = "Möhlenhoff"
)

// String returns the wire representation.
func (m Manufacturer) String() string { return string(m) }
