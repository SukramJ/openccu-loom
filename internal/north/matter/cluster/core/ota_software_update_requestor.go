// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// OTASoftwareUpdateRequestor implements the Matter cluster (0x002A)
// per Matter Core Specification 1.5.1 §11.20. Mandatory on the Root
// endpoint when the device claims to support OTA updates.
//
// openccu-loom v1.1 ships an explicit stub: OTA is delivered through
// the daemon's existing release channel (Docker / GoReleaser),
// not through Matter. We expose the cluster shape with empty default
// providers and a static UpdateState=Idle so commissioners that
// inspect the cluster don't see a missing-cluster error.
//
// Bumping out of stub status (i.e. accepting Matter-OTA images)
// requires a dedicated ADR — the Matter image format is BDX-over-
// TCP/UDP, which openccu-loom does not currently implement.
type OTASoftwareUpdateRequestor struct{}

// Cluster ID + revision per Matter §11.20.
//
// Mirrors matter.js packages/model/src/standard/elements/
// ota-software-update-requestor.element.ts:20 — id 0x002A. The
// previous value 0x0029 is matter.js's `OtaSoftwareUpdateProvider`
// — a different cluster — and would silently collide if a Provider
// surface is ever added. The latency in this fix is harmless because
// we never list 0x0029 in any endpoint's ServerList today
// (`cmd/openccu-loom/daemon_matter.go::buildRootClusters` does not mount
// this cluster); the constant is read only by the parity-snapshot test
// which now resolves the correct Requestor entry.
const (
	otaRequestorClusterID       uint32 = 0x002A
	otaRequestorClusterRevision uint16 = 1

	otaRequestorAttrDefaultOTAProviders uint32 = 0x0000
	otaRequestorAttrUpdatePossible      uint32 = 0x0001
	otaRequestorAttrUpdateState         uint32 = 0x0002
	otaRequestorAttrUpdateStateProgress uint32 = 0x0003

	otaRequestorCmdAnnounceOTAProvider uint32 = 0x00
)

// UpdateStateEnum values (Matter §11.20.5.1).
const (
	OTAUpdateStateUnknown      uint8 = 0
	OTAUpdateStateIdle         uint8 = 1
	OTAUpdateStateQuerying     uint8 = 2
	OTAUpdateStateDelayedQuery uint8 = 3
	OTAUpdateStateDownloading  uint8 = 4
	OTAUpdateStateApplying     uint8 = 5
	OTAUpdateStateDelayedApply uint8 = 6
	OTAUpdateStateRollingBack  uint8 = 7
)

// NewOTASoftwareUpdateRequestor returns the stub cluster server.
func NewOTASoftwareUpdateRequestor() *OTASoftwareUpdateRequestor {
	return &OTASoftwareUpdateRequestor{}
}

// Compile-time assertions: OTASoftwareUpdateRequestor satisfies
// MatterClusterServer and the attribute-lister capability.
var (
	_ matterport.ClusterServer          = (*OTASoftwareUpdateRequestor)(nil)
	_ matterport.ClusterAttributeLister = (*OTASoftwareUpdateRequestor)(nil)
)

// MatterClusterID implements [matterport.ClusterServer].
func (o *OTASoftwareUpdateRequestor) MatterClusterID() uint32 { return otaRequestorClusterID }

// MatterRead implements [matterport.ClusterServer].
func (o *OTASoftwareUpdateRequestor) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case otaRequestorAttrDefaultOTAProviders:
		return []any{}, true
	case otaRequestorAttrUpdatePossible:
		// false — Matter OTA is not the openccu-loom update path.
		return false, true
	case otaRequestorAttrUpdateState:
		return OTAUpdateStateIdle, true
	case otaRequestorAttrUpdateStateProgress:
		// nullable uint8 — return nil to indicate "no update in flight".
		return nil, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return otaRequestorClusterRevision, true
	}
	return nil, false
}

// MatterWrite accepts DefaultOTAProviders writes and silently
// discards them — the bridge does not act on Matter OTA providers
// in v1.1.
func (o *OTASoftwareUpdateRequestor) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	if attrID != otaRequestorAttrDefaultOTAProviders {
		return fmt.Errorf("matter: OTASoftwareUpdateRequestor is read-only (got attr 0x%04X)", attrID)
	}
	return nil
}

// MatterInvoke handles AnnounceOTAProvider. The stub accepts the
// announcement but takes no action.
func (o *OTASoftwareUpdateRequestor) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	if cmdID != otaRequestorCmdAnnounceOTAProvider {
		return nil, im.UnsupportedCommandf("matter: OTASoftwareUpdateRequestor command 0x%02X not supported", cmdID)
	}
	return nil, nil
}

// MatterReportable returns the subscribe-able attributes.
func (o *OTASoftwareUpdateRequestor) MatterReportable() []uint32 {
	return []uint32{otaRequestorAttrUpdateState}
}

// MatterAttributes lists every OTASoftwareUpdateRequestor (0x002A)
// attribute the server implements via MatterRead. Apple Home's HAP
// service rebuild reads the full attribute set; without this the
// dispatcher falls back to MatterReportable's single attribute.
func (o *OTASoftwareUpdateRequestor) MatterAttributes() []uint32 {
	return []uint32{
		otaRequestorAttrDefaultOTAProviders,
		otaRequestorAttrUpdatePossible,
		otaRequestorAttrUpdateState,
		otaRequestorAttrUpdateStateProgress,
	}
}
