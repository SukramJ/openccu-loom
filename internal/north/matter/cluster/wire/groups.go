// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Groups is a minimal stub for the Matter Groups cluster (0x0004).
// Mandatory on every OnOffLight, DimmableLight, and OnOffPlugInUnit
// device-type per matter.js packages/node/src/devices/. HM has no
// group-management concept; this stub advertises NameSupport=0 and
// rejects all writes / commands.
//
// Mirrors matter.js packages/model/src/standard/elements/
// groups.element.ts — ClusterRevision = 4 (HEAD @matter/model 0.16.11).
type Groups struct{}

// Cluster ID + revision per matter.js HEAD.
const (
	groupsClusterID       uint32 = 0x0004
	groupsClusterRevision uint16 = 4

	groupsAttrNameSupport uint32 = 0x0000
)

// errGroupsReadOnly surfaces from Write / Invoke on the Groups stub.
var errGroupsReadOnly = errors.New("matter: Groups cluster is a read-only stub")

// Compile-time assertions.
var (
	_ interfaces.MatterClusterServer          = Groups{}
	_ interfaces.MatterClusterAttributeLister = Groups{}
)

// MatterClusterID implements [interfaces.MatterClusterServer].
func (Groups) MatterClusterID() uint32 { return groupsClusterID }

// MatterRead implements [interfaces.MatterClusterServer]. Only the
// mandatory NameSupport bitmap8 + the global FeatureMap /
// ClusterRevision are exposed.
func (Groups) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case groupsAttrNameSupport:
		// NameSupport bitmap8 bit 7 (0x80) mandates GroupNames support.
		// matter.js groups.element.ts:31 declares the field with default
		// bit 7 set under M conformance; chip's GroupsCluster.cpp:228
		// advertises 0x80 unconditionally and its test asserts the bit.
		return uint8(0x80), true
	case cluster.AttrGlobalFeatureMap:
		// FeatureMap bit 0 = GN (GroupNames). matter.js
		// groups.element.ts:23 declares the bit with M conformance and
		// default 1; chip's GroupsCluster.cpp:223 encodes
		// Feature::kGroupNames unconditionally.
		return uint32(1), true
	case cluster.AttrGlobalClusterRevision:
		return groupsClusterRevision, true
	}
	return nil, false
}

// MatterWrite rejects every write — Groups is a read-only stub.
func (Groups) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: attrID 0x%04X", errGroupsReadOnly, attrID)
}

// MatterInvoke rejects every command.
func (Groups) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w: cmdID 0x%02X", errGroupsReadOnly, cmdID)
}

// MatterReportable returns nil — no subscribe-able attributes.
func (Groups) MatterReportable() []uint32 { return nil }

// MatterAttributes lists the mandatory Groups attributes.
func (Groups) MatterAttributes() []uint32 {
	return []uint32{groupsAttrNameSupport}
}
