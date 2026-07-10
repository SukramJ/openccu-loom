// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: Generic Switch is the OnOff-aktor surface
// for HM channels that have no [model/custom/switch.Switch] wrapper —
// e.g. a no-frills HmIP-PSM with just a STATE writable. The endpoint
// assembler materialises one OnOffPlugInUnit endpoint per such DP.
var (
	_ interfaces.MatterEndpointSource = (*Switch)(nil)
	_ interfaces.MatterClusterServer  = (*Switch)(nil)
	_ interfaces.MatterChangeNotifier = (*Switch)(nil)
)

// Matter Device Type IDs and OnOff cluster IDs follow the Matter 1.5.1
// Application Cluster Specification. The constants mirror those in
// `model/custom/switch/matter.go` (the rich Switch projection); the
// generic projection here is intentionally a subset (no
// power/energy hosting).
const (
	matterGenericSwitchDeviceType uint16 = 0x010A // OnOffPlugInUnit

	matterGenericSwitchClusterOnOff uint32 = 0x0006

	matterGenericSwitchAttrOnOff           uint32 = 0x0000
	matterGenericSwitchAttrFeatureMap      uint32 = 0xFFFC
	matterGenericSwitchAttrClusterRevision uint32 = 0xFFFD

	matterGenericSwitchCmdOff    uint32 = 0x00
	matterGenericSwitchCmdOn     uint32 = 0x01
	matterGenericSwitchCmdToggle uint32 = 0x02

	matterGenericOnOffClusterRevision uint16 = 6
)

var (
	errMatterGenericSwitchAttribute = errors.New("matter: unknown attribute")
	errMatterGenericSwitchCommand   = errors.New("matter: unknown command")
	errMatterGenericSwitchValueType = errors.New("matter: unexpected value type")
)

// matterGenericSwitchEligible reports whether the underlying parameter
// makes the Generic.Switch a credible OnOff source. Only STATE
// (the canonical HM switch parameter) qualifies; other booleans
// (e.g. ENABLE_TIMER, RESET) opt out by returning false so they
// don't pollute the topology with phantom OnOff endpoints.
func matterGenericSwitchEligible(p hmenum.Parameter) bool {
	return p == hmenum.ParameterState
}

// MatterDeviceType implements [interfaces.MatterEndpointSource].
// Generic OnOff actors map to OnOffPlugInUnit (0x010A); a richer
// classification (Light vs. Plug) requires the Custom-DP layer.
func (s *Switch) MatterDeviceType() uint16 { return matterGenericSwitchDeviceType }

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// Returns nil when the underlying parameter is not the canonical
// STATE so the assembler skips the projection — see
// [matterGenericSwitchEligible].
func (s *Switch) MatterClusterServers() []interfaces.MatterClusterServer {
	if s == nil || !matterGenericSwitchEligible(s.Parameter()) {
		return nil
	}
	return []interfaces.MatterClusterServer{s}
}

// MatterClusterID implements [interfaces.MatterClusterServer].
func (s *Switch) MatterClusterID() uint32 { return matterGenericSwitchClusterOnOff }

// MatterRead implements [interfaces.MatterClusterServer]. Returns
// (nil, false) when the underlying STATE wire DP has not been
// observed yet — the bridge maps that to a stale-data status.
func (s *Switch) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterGenericSwitchAttrOnOff:
		on, observed := s.Value()
		if !observed {
			return nil, false
		}
		return on, true
	case matterGenericSwitchAttrFeatureMap:
		return uint32(0), true // No optional features (Lighting feature off).
	case matterGenericSwitchAttrClusterRevision:
		return matterGenericOnOffClusterRevision, true
	default:
		return nil, false
	}
}

// MatterWrite implements [interfaces.MatterClusterServer]. The OnOff
// cluster's only writable attribute is OnOff itself.
func (s *Switch) MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error {
	if attrID != matterGenericSwitchAttrOnOff {
		return fmt.Errorf("%w: 0x%04X", errMatterGenericSwitchAttribute, attrID)
	}
	on, ok := value.(bool)
	if !ok {
		return fmt.Errorf("%w: OnOff write expected bool, got %T", errMatterGenericSwitchValueType, value)
	}
	return s.Set(ctx, on, priority)
}

// MatterInvoke implements [interfaces.MatterClusterServer]. Implements
// Off / On / Toggle. OnWithTimedOff is unimplemented in the generic
// projection — clients that need it talk to the Custom-DP wrapper.
func (s *Switch) MatterInvoke(ctx context.Context, cmdID uint32, _ any, priority hmenum.CommandPriority) (any, error) {
	switch cmdID {
	case matterGenericSwitchCmdOff:
		return nil, s.Set(ctx, false, priority)
	case matterGenericSwitchCmdOn:
		return nil, s.TurnOn(ctx, priority)
	case matterGenericSwitchCmdToggle:
		cur, observed := s.Value()
		if !observed || !cur {
			return nil, s.TurnOn(ctx, priority)
		}
		return nil, s.Set(ctx, false, priority)
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterGenericSwitchCommand, cmdID)
	}
}

// MatterReportable implements [interfaces.MatterClusterServer]. Only
// the OnOff attribute fires Matter reports; FeatureMap / Revision
// are static.
func (s *Switch) MatterReportable() []uint32 {
	return []uint32{matterGenericSwitchAttrOnOff}
}

// MatterAttributes implements
// [interfaces.MatterClusterAttributeLister] — wildcard reads expand
// to OnOff; the dispatcher merges in the universal globals
// (FeatureMap + ClusterRevision) on top.
func (s *Switch) MatterAttributes() []uint32 {
	return []uint32{matterGenericSwitchAttrOnOff}
}
