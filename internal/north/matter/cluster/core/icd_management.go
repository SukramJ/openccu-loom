// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ICDManagement implements the minimum required surface of the
// Matter Intermittently Connected Devices (ICD) cluster (0x0046)
// per Matter Core Specification 1.5.1 §9.17. The openccu-loom bridge
// is a continuously-connected device — none of the LITS / CIP / UAT
// features apply. We expose the three mandatory attributes
// (IdleModeDuration, ActiveModeDuration, ActiveModeThreshold) with
// values that signal "always-on": short Idle (1 s), maximum Active
// duration, zero threshold.
//
// chip-tool reads these during ReadCommissioningInfo and during ICD
// configuration; returning UnsupportedCluster wastes a commissioning
// round-trip, so a stub-but-valid response is the right answer for
// non-ICD bridges.
type ICDManagement struct{}

const (
	icdClusterID       uint32 = 0x0046
	icdClusterRevision uint16 = 3 // Matter 1.5.1 §9.17

	icdAttrIdleModeDuration   uint32 = 0x0000
	icdAttrActiveModeDuration uint32 = 0x0001
	icdAttrActiveModeThresh   uint32 = 0x0002
)

// NewICDManagement returns the cluster server. Stateless.
func NewICDManagement() *ICDManagement { return &ICDManagement{} }

var (
	_ interfaces.MatterClusterServer          = (*ICDManagement)(nil)
	_ interfaces.MatterClusterAttributeLister = (*ICDManagement)(nil)
)

// MatterClusterID implements [interfaces.MatterClusterServer].
func (i *ICDManagement) MatterClusterID() uint32 { return icdClusterID }

// MatterRead implements [interfaces.MatterClusterServer]. Values
// signal "always-on, no idle" so commissioners that gate ICD-related
// flows on these reads see the bridge as a non-ICD device.
func (i *ICDManagement) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case icdAttrIdleModeDuration:
		return uint32(1), true // 1 s — minimum spec value
	case icdAttrActiveModeDuration:
		// Mirrors matter.js packages/model/src/standard/elements/
		// icd-management.element.ts:31 — default 300 ms. The previous
		// 60_000 ms ("generous always-on window") was a non-spec
		// invention; ICD controllers cross-check the value against the
		// spec table and silently drop a non-compliant accessory.
		return uint32(300), true
	case icdAttrActiveModeThresh:
		// Mirrors icd-management.element.ts:35 — default 300 ms. 0 is
		// out-of-range per spec (`uint16` field, conformance `M`,
		// default 300).
		return uint16(300), true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true // no LITS/CIP/UAT features
	case cluster.AttrGlobalClusterRevision:
		return icdClusterRevision, true
	}
	return nil, false
}

// MatterWrite implements [interfaces.MatterClusterServer]. Attributes
// are read-only on the bridge.
func (i *ICDManagement) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("matter: ICDManagement attribute 0x%04X is read-only", attrID)
}

// MatterInvoke implements [interfaces.MatterClusterServer]. ICD
// commands (RegisterClient / UnregisterClient / StayActiveRequest)
// all require feature flags we don't advertise.
func (i *ICDManagement) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, im.UnsupportedCommandf("matter: ICDManagement command 0x%02X not supported", cmdID)
}

// MatterReportable lists subscribe-able attributes.
func (i *ICDManagement) MatterReportable() []uint32 {
	return []uint32{icdAttrIdleModeDuration, icdAttrActiveModeDuration, icdAttrActiveModeThresh}
}

// MatterAttributes lists every ICDManagement (0x0046) attribute the
// server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's three-attribute surface.
func (i *ICDManagement) MatterAttributes() []uint32 {
	return []uint32{icdAttrIdleModeDuration, icdAttrActiveModeDuration, icdAttrActiveModeThresh}
}
