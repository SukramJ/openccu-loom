// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Descriptor implements the Matter Descriptor cluster (0x001D) per
// Matter Core Specification 1.5.1 §9.5. Mandatory on every endpoint:
// it advertises the device-type list, the server cluster list, the
// client cluster list, and the parts list (sub-endpoints).
//
// The bridge wires one Descriptor per endpoint — the Root endpoint's
// PartsList enumerates every bridged endpoint; bridged endpoints
// have an empty PartsList (no sub-endpoints).
type Descriptor struct {
	deviceTypes []DeviceTypeStruct
	clientList  []uint32

	// mu guards the fields the topology assembler installs after the
	// endpoint is already serving reads. The daemon attaches the
	// PartsList / ServerList providers well after bridge.Start, so a
	// commissioner re-establishing CASE can be inside MatterRead while
	// the provider is written.
	mu                 sync.RWMutex
	serverList         []uint32
	partsList          []uint16
	partsListProvider  func() []uint16
	serverListProvider func() []uint32
}

// DeviceTypeStruct mirrors the Matter Descriptor.DeviceTypeList entry
// shape (Matter §9.5.5.1). Each entry is (DeviceTypeID, Revision).
type DeviceTypeStruct struct {
	DeviceType uint32
	Revision   uint16
}

// Cluster ID and revision per Matter §9.5.
const (
	descriptorClusterID       uint32 = 0x001D
	descriptorClusterRevision uint16 = 3 // matter.js HEAD (@matter/model 0.16.11)

	descriptorAttrDeviceTypeList uint32 = 0x0000
	descriptorAttrServerList     uint32 = 0x0001
	descriptorAttrClientList     uint32 = 0x0002
	descriptorAttrPartsList      uint32 = 0x0003
	descriptorAttrTagList        uint32 = 0x0004
)

// errDescriptorReadOnly is returned for any write attempt — the
// Descriptor cluster has no writable attributes.
var errDescriptorReadOnly = errors.New("matter: Descriptor is read-only")

// NewDescriptor returns a Descriptor with the supplied static lists.
// deviceTypes must contain at least one entry per Matter §9.5.5.1.
func NewDescriptor(deviceTypes []DeviceTypeStruct, serverList, clientList []uint32, partsList []uint16) (*Descriptor, error) {
	if len(deviceTypes) == 0 {
		return nil, errors.New("matter: Descriptor requires at least one DeviceType entry")
	}
	return &Descriptor{
		deviceTypes: append([]DeviceTypeStruct(nil), deviceTypes...),
		serverList:  append([]uint32(nil), serverList...),
		clientList:  append([]uint32(nil), clientList...),
		partsList:   append([]uint16(nil), partsList...),
	}, nil
}

// Compile-time assertion: Descriptor satisfies MatterClusterServer.
var _ interfaces.MatterClusterServer = (*Descriptor)(nil)

// MatterClusterID implements [interfaces.MatterClusterServer].
func (d *Descriptor) MatterClusterID() uint32 { return descriptorClusterID }

// MatterRead returns the static lists. PartsList is mutable when the
// topology changes — callers update it via [Descriptor.SetPartsList].
func (d *Descriptor) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case descriptorAttrDeviceTypeList:
		return append([]DeviceTypeStruct(nil), d.deviceTypes...), true
	case descriptorAttrServerList:
		// Apple Home reads ServerList post-CASE and rejects an endpoint
		// as schematically inconsistent when a Subscribe AttributeReport
		// arrives for a cluster that is NOT in ServerList — or when a
		// cluster IS in ServerList but never produces an AttributeReport.
		// Mirrors matter.js DescriptorServer.#serverList
		// (DescriptorServer.ts:236-244): always derived from the set of
		// mounted behaviors, never hardcoded. The provider closure is
		// the openccu-loom equivalent — set after endpoint composition
		// is complete so the list cannot drift from the actual mounted
		// cluster set (Bug K).
		d.mu.RLock()
		provider := d.serverListProvider
		var static []uint32
		if provider == nil {
			// Copy only on the fallback path: every assembled endpoint
			// installs a provider, so materialising the static list first
			// would allocate on every read Apple Home drives post-CASE and
			// then throw the copy away.
			static = append(static, d.serverList...)
		}
		d.mu.RUnlock()
		if provider != nil {
			return provider(), true
		}
		return static, true
	case descriptorAttrClientList:
		// Always return a non-nil slice — Apple Home's IM-decoder
		// rejects `null` for a list-typed attribute (matter.js spec
		// says default `[]`, not null) and drops the entire ClientList
		// from the topology dictionary cache, which then propagates
		// into HAPErrorDomain Code=14 "No Endpoints In Use".
		out := make([]uint32, 0, len(d.clientList))
		out = append(out, d.clientList...)
		return out, true
	case descriptorAttrPartsList:
		d.mu.RLock()
		provider := d.partsListProvider
		var out []uint16
		if provider == nil {
			// Same as ServerList above: the copy is only needed when no
			// provider answers the read.
			out = make([]uint16, 0, len(d.partsList))
			out = append(out, d.partsList...)
		}
		d.mu.RUnlock()
		if provider != nil {
			return provider(), true
		}
		return out, true
	case descriptorAttrTagList:
		// TagList (Matter 1.4 §9.5.6.5) is conformance "TAGLIST" —
		// only present when the TAGLIST feature bit is set in the
		// FeatureMap. We advertise FeatureMap=0 (no TAGLIST), so the
		// attribute MUST be reported as unsupported. Returning a
		// generic `[]uint32{}` here makes Apple's iOS Matter SDK
		// reject the whole Descriptor cluster (Apple does not yet
		// ship a `semtag` struct schema) and HAP-Service-Build
		// aborts with HAPErrorDomain Code=14. matter.js / chip-tool
		// tolerate the absence; re-enable when we surface tags.
		return nil, false
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return descriptorClusterRevision, true
	}
	return nil, false
}

// MatterWrite always rejects — Descriptor has no writable attributes.
func (d *Descriptor) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errDescriptorReadOnly, attrID)
}

// MatterInvoke always rejects — Descriptor has no commands.
func (d *Descriptor) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, im.UnsupportedCommandf("matter: Descriptor has no commands (got 0x%02X)", cmdID)
}

// MatterReportable returns the attributes that subscribe-able. PartsList
// changes when the bridge adds or removes a bridged endpoint.
func (d *Descriptor) MatterReportable() []uint32 {
	return []uint32{descriptorAttrPartsList}
}

// MatterAttributes implements [interfaces.MatterClusterAttributeLister]
// so wildcard subscribe / read enumerates the full Descriptor surface.
// Apple Home reads DeviceTypeList + ServerList + PartsList on every
// endpoint to build the HAP topology — missing entries trigger
// "no enumeration/topology dictionary found" and pairing fails.
//
// FeatureMap (0xFFFC) and ClusterRevision (0xFFFD) are included explicitly
// here in addition to being prepended by the dispatcher's global-attribute
// expansion (dispatcher.go:265). The explicit listing ensures that any
// Subscribe wildcard on a Descriptor cluster sees these attributes even if
// the dispatcher's global prepend behaviour changes, and closes the Apple
// cache-drop for EP14/EP28 Descriptor.FeatureMap / ClusterRevision.
// Mirrors chip src/app/clusters/descriptor/descriptor.cpp which serves
// FeatureMap=0 and ClusterRevision via the standard
// AttributeAccessInterface global-attribute mechanism on every
// endpoint, including bridged ones.
func (d *Descriptor) MatterAttributes() []uint32 {
	// TagList (0x0004) intentionally omitted: it is gated behind the
	// TAGLIST FeatureMap bit (Matter 1.4 §9.5.4.1) which we do not
	// advertise. Apple's iOS Matter SDK does not yet ship the
	// `semtag` struct schema and rejects the whole Descriptor cluster
	// when TagList shows up in the wildcard expansion → HAP build
	// fails with Code=14.
	return []uint32{
		descriptorAttrDeviceTypeList,
		descriptorAttrServerList,
		descriptorAttrClientList,
		descriptorAttrPartsList,
		cluster.AttrGlobalFeatureMap,
		cluster.AttrGlobalClusterRevision,
	}
}

// SetPartsList replaces the parts list under a copy. Used by the
// endpoint assembler when the topology changes — typically only
// relevant on the Root endpoint's Descriptor.
func (d *Descriptor) SetPartsList(partsList []uint16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.partsList = append([]uint16(nil), partsList...)
}

// SetPartsListProvider installs a closure consulted on every
// PartsList read; it overrides the static list set by [NewDescriptor]
// and [SetPartsList]. The Root endpoint's Descriptor uses this so
// the bridge's dynamic endpoint registry (populated post-Reassemble)
// surfaces directly through `0:0x001D:0x0003` reads. Apple Home reads
// PartsList right after the initial subscribe; an empty list makes
// the bridge look "empty" and Apple's iCloud-Heim sync rejects the
// commissioning with a generic add-failed error.
//
// Pass nil to revert to the static list.
func (d *Descriptor) SetPartsListProvider(p func() []uint16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.partsListProvider = p
}

// SetServerListProvider installs a closure consulted on every
// ServerList read; it overrides the static list set by [NewDescriptor].
// The Root endpoint's Descriptor uses this so the actual mounted
// MatterClusterServer set (assembled in daemon.go after the full
// endpoint composition completes) is the single source of truth and
// the advertised ServerList cannot drift from it.
//
// Mirrors matter.js DescriptorServer.#serverList
// (packages/node/src/behaviors/descriptor/DescriptorServer.ts:236-244):
// always derived from `endpoint.behaviors.supported`, never hardcoded.
//
// Pass nil to revert to the static list (only used by unit tests that
// pre-populate a synthetic ServerList).
func (d *Descriptor) SetServerListProvider(p func() []uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.serverListProvider = p
}
