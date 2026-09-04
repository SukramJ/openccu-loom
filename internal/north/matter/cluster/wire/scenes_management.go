// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wire

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// ScenesManagement is a minimal stub for the Matter ScenesManagement
// cluster (0x0062). Mandatory on every OnOffLight, DimmableLight, and
// OnOffPlugInUnit device-type per matter.js. HM has no scene-management
// concept; the stub advertises SceneTableSize=0 and an empty
// FabricSceneInfo list, and rejects all writes / commands.
//
// Mirrors matter.js packages/model/src/standard/elements/
// scenes-management.element.ts — ClusterRevision = 1 (HEAD
// @matter/model 0.16.11). Mandatory attributes: SceneTableSize uint16
// at 0x0001, FabricSceneInfo list at 0x0002.
type ScenesManagement struct{}

// Cluster ID + revision per matter.js HEAD.
const (
	scenesManagementClusterID       uint32 = 0x0062
	scenesManagementClusterRevision uint16 = 1

	scenesManagementAttrSceneTableSize  uint32 = 0x0001
	scenesManagementAttrFabricSceneInfo uint32 = 0x0002
)

// errScenesStub surfaces from Write / Invoke on the stub.
var errScenesStub = errors.New("matter: ScenesManagement is a read-only stub (HM has no scene management)")

// Compile-time assertions.
var (
	_ matterport.ClusterServer          = ScenesManagement{}
	_ matterport.ClusterAttributeLister = ScenesManagement{}
)

// MatterClusterID implements [matterport.ClusterServer].
func (ScenesManagement) MatterClusterID() uint32 { return scenesManagementClusterID }

// MatterRead implements [matterport.ClusterServer].
func (ScenesManagement) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case scenesManagementAttrSceneTableSize:
		// 0 = empty scene table (HM has no scene management).
		return uint16(0), true
	case scenesManagementAttrFabricSceneInfo:
		// Empty list — no scenes stored per fabric.
		return []any{}, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return scenesManagementClusterRevision, true
	}
	return nil, false
}

// MatterWrite rejects every write.
func (ScenesManagement) MatterWrite(_ context.Context, attrID uint32, _ any) error {
	return fmt.Errorf("%w: attrID 0x%04X", errScenesStub, attrID)
}

// MatterInvoke rejects every command. The bridge dispatcher maps errors
// whose message contains "no commands" to IM StatusCode UnsupportedCommand
// (0x81). Returning errScenesStub alone would fall through to StatusFailure
// (0x01); wrapping with the "no commands" sentinel ensures the controller
// receives the correct Matter status code.
// matter.js `packages/node/src/behaviors/scenes-management/` + chip
// `src/app/clusters/scenes-server/ScenesServer.cpp` both require a valid
// status-code response for unsupported commands.
func (ScenesManagement) MatterInvoke(_ context.Context, cmdID uint32, _ any) (any, error) {
	return nil, fmt.Errorf("%w: no commands supported (HM has no scene management), cmdID 0x%02X", errScenesStub, cmdID)
}

// MatterReportable returns nil — no subscribe-able attributes.
func (ScenesManagement) MatterReportable() []uint32 { return nil }

// MatterAttributes lists the mandatory ScenesManagement attributes.
func (ScenesManagement) MatterAttributes() []uint32 {
	return []uint32{scenesManagementAttrSceneTableSize, scenesManagementAttrFabricSceneInfo}
}
