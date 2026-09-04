// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// NetworkCommissioning implements the Matter NetworkCommissioning
// cluster (0x0031) per Matter Core Specification 1.5.1 §11.9.
// Mandatory on the Root endpoint.
//
// openccu-loom ships an Ethernet-only bridge — Wi-Fi and Thread are
// outside scope. The cluster therefore advertises FeatureMap=ETH only
// and rejects every WiFi / Thread command with UnsupportedCommand.
//
// Static reporting:
//
//   - MaxNetworks = 1 (the bridge has one Ethernet interface).
//   - Networks contains a single NetworkInfoStruct entry naming the
//     local interface (default "eth0", configurable).
//   - LastNetworkingStatus is null until ConnectNetwork is invoked
//     (which never happens for Ethernet-only).
type NetworkCommissioning struct {
	mu sync.RWMutex

	// dataVersion tracks the per-cluster monotonic counter per Matter
	// §10.6.5. Bumped at construction so the initial version is a non-zero
	// sentinel (prevents DataVersionFilter=0 false-positive cache hits).
	// Satisfies [matterport.ClusterDataVersion]. Mirrors matter.js
	// NetworkCommissioning behavior layer auto-tracking and chip
	// src/app/clusters/network-commissioning/NetworkCommissioningCluster.cpp
	// ember dirty-marking mechanism.
	dataVersion cluster.DataVersionTracker

	interfaceID         []byte
	interfaceEnabled    bool
	lastNetworkingStat  *uint8 // nullable
	lastNetworkID       []byte // nullable
	lastConnectErrorVal *int32 // nullable
}

// FeatureMap bits per Matter §11.9.4.
const (
	NetworkCommFeatureWiFi     uint32 = 1 << 0
	NetworkCommFeatureThread   uint32 = 1 << 1
	NetworkCommFeatureEthernet uint32 = 1 << 2
)

// NetworkCommissioningStatusEnum values per Matter §11.9.5.5.
const (
	NetworkingStatusSuccess                uint8 = 0
	NetworkingStatusOutOfRange             uint8 = 1
	NetworkingStatusBoundsExceeded         uint8 = 2
	NetworkingStatusNetworkIDNotFound      uint8 = 3
	NetworkingStatusDuplicateNetworkID     uint8 = 4
	NetworkingStatusNetworkNotFound        uint8 = 5
	NetworkingStatusRegulatoryError        uint8 = 6
	NetworkingStatusAuthFailure            uint8 = 7
	NetworkingStatusUnsupportedSecurity    uint8 = 8
	NetworkingStatusOtherConnectionFailure uint8 = 9
	NetworkingStatusIPV6Failed             uint8 = 10
	NetworkingStatusIPBindFailed           uint8 = 11
	NetworkingStatusUnknownError           uint8 = 12
)

// Cluster ID + revision per Matter §11.9.
const (
	netcommClusterID       uint32 = 0x0031
	netcommClusterRevision uint16 = 2

	netcommAttrMaxNetworks           uint32 = 0x0000
	netcommAttrNetworks              uint32 = 0x0001
	netcommAttrScanMaxTimeSeconds    uint32 = 0x0002
	netcommAttrConnectMaxTimeSeconds uint32 = 0x0003
	netcommAttrInterfaceEnabled      uint32 = 0x0004
	netcommAttrLastNetworkingStatus  uint32 = 0x0005
	netcommAttrLastNetworkID         uint32 = 0x0006
	netcommAttrLastConnectErrorValue uint32 = 0x0007
)

// Command IDs (Matter §11.9.7), kept inline rather than as constants:
// openccu-loom rejects every one (Ethernet-only). 0x00 ScanNetworks,
// 0x01 ScanNetworksResponse, 0x02 AddOrUpdateWiFiNetwork,
// 0x03 AddOrUpdateThreadNetwork, 0x04 RemoveNetwork,
// 0x05 NetworkConfigResponse, 0x06 ConnectNetwork,
// 0x07 ConnectNetworkResponse, 0x08 ReorderNetwork.

// NetworkInfoStruct mirrors Matter §11.9.5.4.
type NetworkInfoStruct struct {
	NetworkID []byte
	Connected bool
}

// NetworkCommissioningConfig drives [NewNetworkCommissioning].
type NetworkCommissioningConfig struct {
	// InterfaceID is the byte-encoded identifier of the local
	// Ethernet interface (typically the literal "eth0" / "en0" /
	// "enp3s0" string). Defaults to "eth0" when empty.
	InterfaceID []byte
}

// NewNetworkCommissioning constructs the cluster.
func NewNetworkCommissioning(cfg NetworkCommissioningConfig) *NetworkCommissioning {
	id := cfg.InterfaceID
	if len(id) == 0 {
		id = []byte("eth0")
	}
	n := &NetworkCommissioning{
		interfaceID:      append([]byte(nil), id...),
		interfaceEnabled: true,
	}
	// Seed DataVersion at a non-zero sentinel so DataVersionFilter=0 from
	// controllers does not falsely suppress the initial cluster report.
	// Ethernet-only config is static at runtime so Bump() is only called here.
	n.dataVersion.Bump()
	return n
}

// Compile-time assertions: NetworkCommissioning satisfies
// MatterClusterServer, the attribute-lister capability, and
// MatterClusterDataVersion.
var (
	_ matterport.ClusterServer                  = (*NetworkCommissioning)(nil)
	_ matterport.ClusterAttributeLister         = (*NetworkCommissioning)(nil)
	_ matterport.ClusterDataVersion             = (*NetworkCommissioning)(nil)
	_ matterport.ClusterAttributeReadPrivilege  = (*NetworkCommissioning)(nil)
	_ matterport.ClusterAttributeWritePrivilege = (*NetworkCommissioning)(nil)
)

// MatterDataVersion implements [matterport.ClusterDataVersion].
// Returns the per-cluster monotonic counter seeded at construction.
// Mirrors matter.js NetworkCommissioning behavior layer DataVersion
// tracking and chip's ember dirty-marking mechanism
// (src/app/clusters/network-commissioning/NetworkCommissioningCluster.cpp).
func (n *NetworkCommissioning) MatterDataVersion() uint32 {
	return n.dataVersion.Current()
}

// MatterClusterID implements [matterport.ClusterServer].
func (n *NetworkCommissioning) MatterClusterID() uint32 { return netcommClusterID }

// MinReadPrivilege implements [matterport.ClusterAttributeReadPrivilege].
// MaxNetworks / Networks / LastNetworkingStatus / LastNetworkId /
// LastConnectErrorValue are all read-access "R A" (Administer) per Matter
// §11.9 — a merely-View subject must not read them, nor have them streamed
// via a wildcard subscribe. InterfaceEnabled is "RW VA" (View read); the
// WiFi/Thread-only ScanMaxTimeSeconds / ConnectMaxTimeSeconds are "R V".
// Mirrors matter.js
// packages/model/src/standard/elements/network-commissioning.element.ts:29-59.
func (n *NetworkCommissioning) MinReadPrivilege(attrID uint32) uint8 {
	switch attrID {
	case netcommAttrMaxNetworks,
		netcommAttrNetworks,
		netcommAttrLastNetworkingStatus,
		netcommAttrLastNetworkID,
		netcommAttrLastConnectErrorValue:
		return 5 // Administer
	default:
		return 1 // View
	}
}

// MinWritePrivilege implements [matterport.ClusterAttributeWritePrivilege].
// InterfaceEnabled (0x0004) requires Administer (5) per Matter §11.9
// (access "RW VA"). Mirrors matter.js
// packages/model/src/standard/elements/network-commissioning.element.ts:47.
func (n *NetworkCommissioning) MinWritePrivilege(attrID uint32) uint8 {
	switch attrID {
	case netcommAttrInterfaceEnabled:
		return 5 // Administer
	default:
		return 3 // Operate — standard default
	}
}

// MatterRead implements [matterport.ClusterServer].
func (n *NetworkCommissioning) MatterRead(attrID uint32) (any, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	switch attrID {
	case netcommAttrMaxNetworks:
		return uint8(1), true
	case netcommAttrNetworks:
		return []NetworkInfoStruct{{
			NetworkID: append([]byte(nil), n.interfaceID...),
			Connected: n.interfaceEnabled,
		}}, true
	case netcommAttrInterfaceEnabled:
		return n.interfaceEnabled, true
	case netcommAttrLastNetworkingStatus:
		if n.lastNetworkingStat == nil {
			return nil, true
		}
		return *n.lastNetworkingStat, true
	case netcommAttrLastNetworkID:
		if n.lastNetworkID == nil {
			return nil, true
		}
		return append([]byte(nil), n.lastNetworkID...), true
	case netcommAttrLastConnectErrorValue:
		if n.lastConnectErrorVal == nil {
			return nil, true
		}
		return *n.lastConnectErrorVal, true
	case cluster.AttrGlobalFeatureMap:
		return NetworkCommFeatureEthernet, true
	case cluster.AttrGlobalClusterRevision:
		return netcommClusterRevision, true
	}
	return nil, false
}

// MatterWrite handles InterfaceEnabled writes (Matter §11.9.6.4).
// Other attributes are read-only.
func (n *NetworkCommissioning) MatterWrite(_ context.Context, attrID uint32, value any) error {
	if attrID != netcommAttrInterfaceEnabled {
		return fmt.Errorf("matter: NetworkCommissioning attribute 0x%04X is read-only", attrID)
	}
	v, ok := value.(bool)
	if !ok {
		return fmt.Errorf("matter: InterfaceEnabled write expected bool, got %T", value)
	}
	n.mu.Lock()
	n.interfaceEnabled = v
	n.mu.Unlock()
	return nil
}

// MatterInvoke rejects every command — openccu-loom ships Ethernet-only
// and Ethernet has no commissioning surface.
func (n *NetworkCommissioning) MatterInvoke(_ context.Context, cmdID uint32, _ any) (any, error) {
	return nil, im.UnsupportedCommandf("matter: NetworkCommissioning command 0x%02X not supported (Ethernet-only)", cmdID)
}

// MatterReportable lists the subscribe-able attributes.
func (n *NetworkCommissioning) MatterReportable() []uint32 {
	return []uint32{
		netcommAttrNetworks,
		netcommAttrInterfaceEnabled,
		netcommAttrLastNetworkingStatus,
	}
}

// MatterAttributes lists the NetworkCommissioning (0x0031) attributes
// the server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's three-attribute surface.
//
// ScanMaxTimeSeconds (0x0002) and ConnectMaxTimeSeconds (0x0003) have
// conformance WI|TH in Matter §11.9 — they apply only to Wi-Fi and
// Thread interfaces. openccu-loom is Ethernet-only (FeatureMap=ETH)
// so these two attributes are excluded from the advertised set.
func (n *NetworkCommissioning) MatterAttributes() []uint32 {
	return []uint32{
		netcommAttrMaxNetworks,
		netcommAttrNetworks,
		netcommAttrInterfaceEnabled,
		netcommAttrLastNetworkingStatus,
		netcommAttrLastNetworkID,
		netcommAttrLastConnectErrorValue,
	}
}
