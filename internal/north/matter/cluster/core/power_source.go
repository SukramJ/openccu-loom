// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// PowerSource implements the Matter PowerSource cluster (0x002F) per
// Matter Core Specification 1.5.1 §11.7, exposing a battery
// measurement (HM LOW_BAT, OPERATING_VOLTAGE_LEVEL, etc.) as a
// read-only cluster surface.
//
// Production bridged endpoints use measurement.NewPowerSourceServer,
// which drives its attributes from a live source DP. This struct
// retains the read path + cluster constants the measurement package
// relies on for spec values.
type PowerSource struct {
	mu sync.RWMutex

	status                   uint8 // PowerSourceStatusEnum
	order                    uint8 // sort order among multiple PowerSources
	description              string
	batteryPercent           uint8 // 0..200 — half-percent units
	batteryPercentSet        bool
	batteryAlert             uint8 // BatChargeLevelEnum
	batteryReplaceable       bool  // drives FeatureMap REPLC bit
	batteryReplaceability    uint8 // BatReplaceabilityEnum (attr 0x10)
	batteryReplacementNeeded bool  // attr 0x0F: true only when explicit signal received
}

// PowerSource feature bits (Matter §11.7.4).
const (
	PowerFeatureWired        uint32 = 1 << 0
	PowerFeatureBattery      uint32 = 1 << 1
	PowerFeatureRechargeable uint32 = 1 << 2
	PowerFeatureReplaceable  uint32 = 1 << 3
)

// PowerSourceStatusEnum (Matter §11.7.5.1).
const (
	PowerStatusUnspecified uint8 = 0
	PowerStatusActive      uint8 = 1
	PowerStatusStandby     uint8 = 2
	PowerStatusUnavailable uint8 = 3
)

// BatChargeLevelEnum (Matter §11.7.5.6).
const (
	BatChargeOK       uint8 = 0
	BatChargeWarning  uint8 = 1
	BatChargeCritical uint8 = 2
)

// BatReplaceabilityEnum values per matter.js packages/model/src/standard/
// elements/power-source-cluster.element.ts:221-226 (BatReplaceabilityEnum).
const (
	BatReplaceabilityUnspecified        uint8 = 0
	BatReplaceabilityNotReplaceable     uint8 = 1
	BatReplaceabilityUserReplaceable    uint8 = 2
	BatReplaceabilityFactoryReplaceable uint8 = 3
)

// Cluster ID + revision per Matter §11.7.
const (
	powersrcClusterID       uint32 = 0x002F
	powersrcClusterRevision uint16 = 3 // matter.js HEAD (@matter/model 0.16.11)

	powersrcAttrStatus               uint32 = 0x0000
	powersrcAttrOrder                uint32 = 0x0001
	powersrcAttrDescription          uint32 = 0x0002
	powersrcAttrBatPercentRemaining  uint32 = 0x000C
	powersrcAttrBatChargeLevel       uint32 = 0x000E
	powersrcAttrBatReplacementNeeded uint32 = 0x000F // bool: battery must be replaced now
	powersrcAttrBatReplaceability    uint32 = 0x0010 // BatReplaceabilityEnum: how replaceable
	powersrcAttrEndpointList         uint32 = 0x001F
)

// Compile-time assertions: PowerSource satisfies MatterClusterServer
// and the attribute-lister capability.
var (
	_ interfaces.MatterClusterServer          = (*PowerSource)(nil)
	_ interfaces.MatterClusterAttributeLister = (*PowerSource)(nil)
)

// MatterClusterID implements [interfaces.MatterClusterServer].
func (p *PowerSource) MatterClusterID() uint32 { return powersrcClusterID }

// MatterRead implements [interfaces.MatterClusterServer].
func (p *PowerSource) MatterRead(attrID uint32) (any, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	switch attrID {
	case powersrcAttrStatus:
		return p.status, true
	case powersrcAttrOrder:
		return p.order, true
	case powersrcAttrDescription:
		return p.description, true
	case powersrcAttrBatPercentRemaining:
		if !p.batteryPercentSet {
			// Nullable — Matter encodes "unknown" as null.
			return nil, true
		}
		return p.batteryPercent, true
	case powersrcAttrBatChargeLevel:
		return p.batteryAlert, true
	case powersrcAttrBatReplacementNeeded:
		return p.batteryReplacementNeeded, true
	case powersrcAttrBatReplaceability:
		return p.batteryReplaceability, true
	case powersrcAttrEndpointList:
		// Empty — openccu-loom attaches PowerSource directly to the
		// bridged endpoint, not as a separate composite endpoint.
		return []uint16{}, true
	case cluster.AttrGlobalFeatureMap:
		feat := PowerFeatureBattery
		if p.batteryReplaceable {
			feat |= PowerFeatureReplaceable
		}
		return feat, true
	case cluster.AttrGlobalClusterRevision:
		return powersrcClusterRevision, true
	}
	return nil, false
}

// MatterWrite always rejects — PowerSource has no writable attributes.
func (p *PowerSource) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("matter: PowerSource is read-only (got attr 0x%04X)", attrID)
}

// MatterInvoke always rejects — PowerSource has no commands.
func (p *PowerSource) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("matter: PowerSource has no commands (got 0x%02X)", cmdID)
}

// MatterReportable returns the subscribe-able attributes — battery
// percent and alert level both flip when DP values change.
func (p *PowerSource) MatterReportable() []uint32 {
	return []uint32{powersrcAttrBatPercentRemaining, powersrcAttrBatChargeLevel, powersrcAttrStatus}
}

// MatterAttributes lists every PowerSource (0x002F) attribute the
// server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's three-attribute surface.
func (p *PowerSource) MatterAttributes() []uint32 {
	return []uint32{
		powersrcAttrStatus,
		powersrcAttrOrder,
		powersrcAttrDescription,
		powersrcAttrBatPercentRemaining,
		powersrcAttrBatChargeLevel,
		powersrcAttrBatReplacementNeeded,
		powersrcAttrBatReplaceability,
		powersrcAttrEndpointList,
	}
}
