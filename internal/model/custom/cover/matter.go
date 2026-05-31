// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: Cover, Blind, and Garage participate in the
// Matter source surface (ADR 0012). Each projects onto a Matter
// WindowCovering (0x0102) cluster with type-specific FeatureMap and
// EndProductType attributes; all three return the same device type
// ID (0x0202 — WindowCovering) — the surface differences live inside
// the cluster attributes, per Matter spec.
var (
	_ interfaces.MatterEndpointSource     = (*Cover)(nil)
	_ interfaces.MatterEndpointSource     = (*Blind)(nil)
	_ interfaces.MatterEndpointSource     = (*Garage)(nil)
	_ interfaces.MatterClusterDataVersion = (*Cover)(nil)
	_ interfaces.MatterClusterDataVersion = (*Garage)(nil)
)

// Matter Device Type IDs and WindowCovering cluster IDs follow the
// Matter 1.5.1 Application Cluster Specification. They live here next
// to the projection; internal/north/matter/cluster/windowcovering/
// may later import them. Cluster revision verified against the Matter
// cluster sweep (matter.js HEAD packages/model/src/standard/elements/).
const (
	matterDeviceTypeWindowCovering uint16 = 0x0202

	matterClusterWindowCovering uint32 = 0x0102

	matterAttrType                             uint32 = 0x0000
	matterAttrConfigStatus                     uint32 = 0x0007
	matterAttrOperationalStatus                uint32 = 0x000A
	matterAttrEndProductType                   uint32 = 0x000D
	matterAttrTargetPositionLiftPercent100ths  uint32 = 0x000B
	matterAttrTargetPositionTiltPercent100ths  uint32 = 0x000C
	matterAttrCurrentPositionLiftPercent100ths uint32 = 0x000E
	matterAttrCurrentPositionTiltPercent100ths uint32 = 0x000F
	matterAttrMode                             uint32 = 0x0017
	matterAttrFeatureMap                       uint32 = 0xFFFC
	matterAttrClusterRevision                  uint32 = 0xFFFD

	matterCmdUpOrOpen           uint32 = 0x00
	matterCmdDownOrClose        uint32 = 0x01
	matterCmdStopMotion         uint32 = 0x02
	matterCmdGoToLiftPercentage uint32 = 0x05
	matterCmdGoToTiltPercentage uint32 = 0x08

	matterWindowCoveringClusterRevision uint16 = 8 // matter.js HEAD (@matter/model 0.16.11)

	// matterCoverPctMax is the Matter percent100ths saturation point —
	// 100.00% in 0.01% units.
	matterCoverPctMax uint16 = 10000

	// FeatureMap bits (Matter Application Cluster Spec 5.3.4).
	matterWCFeatureLift          uint32 = 1 << 0 // LF — supports lift control
	matterWCFeatureTilt          uint32 = 1 << 1 // TL — supports tilt control
	matterWCFeaturePositionAwLft uint32 = 1 << 2 // PA_LF — reports lift position
	// Bit 3 is NOT a WindowCovering feature in matter.js HEAD
	// (window-covering-cluster.element.ts: only LF=0, TL=1, PA_LF=2,
	// PA_TL=4). Absolute positioning is implied by PositionAware*, so the
	// previously-advertised "ABS" bit 3 was an undefined feature and is gone.
	matterWCFeaturePositionAwTlt uint32 = 1 << 4 // PA_TL — reports tilt position

	// Type / EndProductType enum values (Matter spec 5.3.6.1 / 5.3.6.4).
	matterWCTypeRollerShade           uint8 = 0
	matterWCTypeDrapery               uint8 = 4
	matterWCTypeAwning                uint8 = 5
	matterWCTypeShutter               uint8 = 6
	matterWCTypeTiltBlindLiftAndTilt  uint8 = 8
	matterWCEndProductGarageDoor      uint8 = 8
	matterWCEndProductTiltInteriorBld uint8 = 14

	// OperationalStatus motion codes (2-bit field replicated across
	// global / lift / tilt nibbles).
	matterWCMotionStopped uint8 = 0b00
	matterWCMotionOpening uint8 = 0b01
	matterWCMotionClosing uint8 = 0b10
)

var (
	errMatterUnknownAttribute = errors.New("matter: unknown attribute")
	errMatterUnknownCommand   = errors.New("matter: unknown command")
	errMatterValueType        = errors.New("matter: unexpected value type")
)

// hmLevelToMatterPct100ths converts an HM domain-level position
// (0.0 = closed, 1.0 = open) into Matter's CurrentPositionLiftPercent100ths
// convention (0 = open, 10000 = closed). Note the inversion — this is
// the core encoding gotcha for window covering and lives in the model
// (rich-model principle) rather than the bridge.
func hmLevelToMatterPct100ths(hmLevel float64) uint16 {
	v := (1 - hmLevel) * float64(matterCoverPctMax)
	if v < 0 {
		return 0
	}
	if v > float64(matterCoverPctMax) {
		return matterCoverPctMax
	}
	return uint16(v + 0.5)
}

// matterPct100thsToHMLevel is the inverse of [hmLevelToMatterPct100ths].
func matterPct100thsToHMLevel(matterPct uint16) float64 {
	if matterPct >= matterCoverPctMax {
		return 0
	}
	return 1 - float64(matterPct)/float64(matterCoverPctMax)
}

// coverTypeFor maps a [CoverVariant] to the Matter WindowCovering
// Type attribute (5.3.6.1). VariantBlind never reaches here because
// blinds project via [Blind.MatterClusterServers] which sets Type
// directly to TiltBlindLiftAndTilt.
func coverTypeFor(v CoverVariant) uint8 {
	switch v {
	case VariantAwning:
		return matterWCTypeAwning
	case VariantCurtain:
		return matterWCTypeDrapery
	case VariantShutter, VariantWindow:
		return matterWCTypeShutter
	case VariantShade, VariantDamper, VariantBlind, VariantGarage:
		return matterWCTypeRollerShade
	default:
		return matterWCTypeRollerShade
	}
}

// motionForOpeningClosing packs the (opening, closing) booleans into
// the 2-bit motion code used by [matterAttrOperationalStatus] nibbles.
func motionForOpeningClosing(opening, closing bool) uint8 {
	switch {
	case opening:
		return matterWCMotionOpening
	case closing:
		return matterWCMotionClosing
	default:
		return matterWCMotionStopped
	}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion] for
// Cover (shared by coverWCServer and blindWCServer via the embedded *Cover).
// Bumped on every successful MatterInvoke so DataVersionFilter evaluation
// correctly detects cluster changes.
func (c *Cover) MatterDataVersion() uint32 { return c.dataVersion.Current() }

// MatterDataVersion implements [interfaces.MatterClusterDataVersion] for Garage.
// Bumped on every successful MatterInvoke.
func (g *Garage) MatterDataVersion() uint32 { return g.dataVersion.Current() }

// MatterDeviceType implements [interfaces.MatterEndpointSource]. All
// cover variants surface as Matter WindowCovering (0x0202); the Type
// and EndProductType attributes inside the cluster carry the variant
// distinction.
func (c *Cover) MatterDeviceType() uint16 { return matterDeviceTypeWindowCovering }

// MatterClusterServers implements [interfaces.MatterEndpointSource].
func (c *Cover) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{coverWCServer{c: c}}
}

// MatterDeviceType for Blind shadows the embedded Cover's method so
// the Blind's own projection is reachable through the source-surface
// interface.
func (b *Blind) MatterDeviceType() uint16 { return matterDeviceTypeWindowCovering }

// MatterClusterServers for Blind returns the lift+tilt projection.
// Shadows the embedded Cover's method.
func (b *Blind) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{blindWCServer{b: b}}
}

// MatterDeviceType for Garage.
func (g *Garage) MatterDeviceType() uint16 { return matterDeviceTypeWindowCovering }

// MatterClusterServers for Garage returns the discrete-state lift
// projection with EndProductType=GarageDoor.
func (g *Garage) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{garageWCServer{g: g}}
}

// coverWCServer projects a [Cover] (lift only, no tilt) onto the
// WindowCovering cluster.
type coverWCServer struct{ c *Cover }

func (s coverWCServer) MatterClusterID() uint32 { return matterClusterWindowCovering }

func (s coverWCServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrType:
		return coverTypeFor(s.c.Variant), true
	case matterAttrEndProductType:
		return coverTypeFor(s.c.Variant), true
	case matterAttrConfigStatus:
		// 0x05 = Operational | LiftPositionAware. Mirrors a typical
		// powered-cover descriptor.
		return uint8(0x05), true
	case matterAttrOperationalStatus:
		motion := motionForOpeningClosing(s.c.IsOpening(), s.c.IsClosing())
		return motion | (motion << 2), true
	case matterAttrCurrentPositionLiftPercent100ths:
		// Value temporarily unavailable (e.g. CCU circuit-breaker open): return
		// (nil, true) so the dispatcher encodes TLV null + Success. See
		// climate/matter.go for the full rationale.
		pos, ok := s.c.Position()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(pos.Level()), true
	case matterAttrTargetPositionLiftPercent100ths:
		// HM covers do not maintain a separate target; mirror CurrentPosition.
		// Conformance LF & PA_LF makes this attribute mandatory given the
		// advertised FeatureMap. Mirrors matter.js WindowCoveringServer.ts
		// fallback when no explicit target was set.
		pos, ok := s.c.Position()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(pos.Level()), true
	case matterAttrMode:
		return uint8(0), true
	case matterAttrFeatureMap:
		return matterWCFeatureLift | matterWCFeaturePositionAwLft, true
	case matterAttrClusterRevision:
		return matterWindowCoveringClusterRevision, true
	default:
		return nil, false
	}
}

func (s coverWCServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s coverWCServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdUpOrOpen:
		err = s.c.Open(ctx, priority)
	case matterCmdDownOrClose:
		err = s.c.Close(ctx, priority)
	case matterCmdStopMotion:
		err = s.c.Stop(ctx, priority)
	case matterCmdGoToLiftPercentage:
		pct, e := extractGoToPercentage(fields)
		if e != nil {
			return nil, e
		}
		err = s.c.SetPosition(ctx, matterPct100thsToHMLevel(pct), priority)
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.c.dataVersion.Bump()
	return nil, nil
}

func (s coverWCServer) MatterReportable() []uint32 {
	return []uint32{matterAttrCurrentPositionLiftPercent100ths, matterAttrOperationalStatus}
}

// MatterAttributes lists every WindowCovering (0x0102) attribute the
// cover server implements via MatterRead. Apple Home's HAP service
// rebuild reads the full attribute set; without this the dispatcher
// falls back to MatterReportable's two-attribute surface.
func (s coverWCServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrType,
		matterAttrConfigStatus,
		matterAttrOperationalStatus,
		matterAttrEndProductType,
		matterAttrTargetPositionLiftPercent100ths,
		matterAttrCurrentPositionLiftPercent100ths,
		matterAttrMode,
	}
}

// blindWCServer projects a [Blind] (lift + tilt) onto the
// WindowCovering cluster.
type blindWCServer struct{ b *Blind }

func (s blindWCServer) MatterClusterID() uint32 { return matterClusterWindowCovering }

func (s blindWCServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrType:
		return matterWCTypeTiltBlindLiftAndTilt, true
	case matterAttrEndProductType:
		// EndProductType=14 (TiltOnlyInteriorBlind) when the device
		// reports no lift capability; otherwise mirror Type. Blind
		// always carries a lift axis (the position from the embedded
		// Cover), so report the Type code directly.
		return matterWCTypeTiltBlindLiftAndTilt, true
	case matterAttrConfigStatus:
		return uint8(0x05), true
	case matterAttrOperationalStatus:
		// Lift motion mirrors Cover.IsOpening / IsClosing. Tilt motion
		// is not exposed by the model in 0.1.0 — bits remain 00.
		// Tilt-motion wiring requires generic Direction handling
		// that distinguishes lift from tilt; not yet implemented.
		liftMotion := motionForOpeningClosing(s.b.IsOpening(), s.b.IsClosing())
		return liftMotion | (liftMotion << 2), true
	case matterAttrCurrentPositionLiftPercent100ths:
		// Value temporarily unavailable — return (nil, true); see coverWCServer.MatterRead.
		pos, ok := s.b.Position()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(pos.Level()), true
	case matterAttrTargetPositionLiftPercent100ths:
		// HM blinds do not maintain a separate target; mirror CurrentPosition.
		// Conformance LF & PA_LF makes this attribute mandatory given the
		// advertised FeatureMap. Mirrors matter.js WindowCoveringServer.ts
		// fallback when no explicit target was set.
		pos, ok := s.b.Position()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(pos.Level()), true
	case matterAttrCurrentPositionTiltPercent100ths:
		tilt, ok := s.b.TiltPosition()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(tilt.Level()), true
	case matterAttrTargetPositionTiltPercent100ths:
		// HM blinds do not maintain a separate tilt target; mirror CurrentPositionTilt.
		// Conformance TL & PA_TL makes this attribute mandatory given the
		// advertised FeatureMap. Mirrors matter.js WindowCoveringServer.ts
		// fallback when no explicit target was set.
		tilt, ok := s.b.TiltPosition()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(tilt.Level()), true
	case matterAttrMode:
		return uint8(0), true
	case matterAttrFeatureMap:
		return matterWCFeatureLift | matterWCFeatureTilt |
			matterWCFeaturePositionAwLft | matterWCFeaturePositionAwTlt, true
	case matterAttrClusterRevision:
		return matterWindowCoveringClusterRevision, true
	default:
		return nil, false
	}
}

func (s blindWCServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s blindWCServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdUpOrOpen:
		err = s.b.Open(ctx, priority)
	case matterCmdDownOrClose:
		err = s.b.Close(ctx, priority)
	case matterCmdStopMotion:
		err = s.b.Stop(ctx, priority)
	case matterCmdGoToLiftPercentage:
		pct, e := extractGoToPercentage(fields)
		if e != nil {
			return nil, e
		}
		err = s.b.SetPosition(ctx, matterPct100thsToHMLevel(pct), priority)
	case matterCmdGoToTiltPercentage:
		pct, e := extractGoToPercentage(fields)
		if e != nil {
			return nil, e
		}
		err = s.b.SetTilt(ctx, matterPct100thsToHMLevel(pct), priority)
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.b.dataVersion.Bump()
	return nil, nil
}

func (s blindWCServer) MatterReportable() []uint32 {
	return []uint32{
		matterAttrCurrentPositionLiftPercent100ths,
		matterAttrCurrentPositionTiltPercent100ths,
		matterAttrOperationalStatus,
	}
}

// MatterAttributes lists every WindowCovering (0x0102) attribute the
// blind server implements via MatterRead. Apple Home's HAP service
// rebuild reads the full attribute set; without this the dispatcher
// falls back to MatterReportable's three-attribute surface.
func (s blindWCServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrType,
		matterAttrConfigStatus,
		matterAttrOperationalStatus,
		matterAttrEndProductType,
		matterAttrTargetPositionLiftPercent100ths,
		matterAttrTargetPositionTiltPercent100ths,
		matterAttrCurrentPositionLiftPercent100ths,
		matterAttrCurrentPositionTiltPercent100ths,
		matterAttrMode,
	}
}

// garageWCServer projects a [Garage] onto the WindowCovering cluster.
// Position is derived from the discrete door state — Open=1.0,
// Ventilation=0.5, Closed=0.0 — which the [Garage.Position] method
// already exposes. EndProductType is GarageDoor.
type garageWCServer struct{ g *Garage }

func (s garageWCServer) MatterClusterID() uint32 { return matterClusterWindowCovering }

func (s garageWCServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrType:
		return matterWCTypeRollerShade, true // No "garage" Type code; EndProductType carries the distinction.
	case matterAttrEndProductType:
		return matterWCEndProductGarageDoor, true
	case matterAttrConfigStatus:
		return uint8(0x05), true
	case matterAttrOperationalStatus:
		motion := motionForOpeningClosing(s.g.IsOpening(), s.g.IsClosing())
		return motion | (motion << 2), true
	case matterAttrCurrentPositionLiftPercent100ths:
		// Value temporarily unavailable — return (nil, true); see coverWCServer.MatterRead.
		pos, ok := s.g.Position()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(pos.Level()), true
	case matterAttrTargetPositionLiftPercent100ths:
		// HM garage doors do not maintain a separate target; mirror CurrentPosition.
		// Conformance LF & PA_LF makes this attribute mandatory given the
		// advertised FeatureMap. Mirrors matter.js WindowCoveringServer.ts
		// fallback when no explicit target was set.
		pos, ok := s.g.Position()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(pos.Level()), true
	case matterAttrMode:
		return uint8(0), true
	case matterAttrFeatureMap:
		return matterWCFeatureLift | matterWCFeaturePositionAwLft, true
	case matterAttrClusterRevision:
		return matterWindowCoveringClusterRevision, true
	default:
		return nil, false
	}
}

func (s garageWCServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s garageWCServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdUpOrOpen:
		err = s.g.Open(ctx, priority)
	case matterCmdDownOrClose:
		err = s.g.Close(ctx, priority)
	case matterCmdStopMotion:
		err = s.g.Stop(ctx, priority)
	case matterCmdGoToLiftPercentage:
		pct, e := extractGoToPercentage(fields)
		if e != nil {
			return nil, e
		}
		err = s.g.SetPosition(ctx, matterPct100thsToHMLevel(pct), priority)
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.g.dataVersion.Bump()
	return nil, nil
}

func (s garageWCServer) MatterReportable() []uint32 {
	return []uint32{matterAttrCurrentPositionLiftPercent100ths, matterAttrOperationalStatus}
}

// MatterAttributes lists every WindowCovering (0x0102) attribute the
// garage server implements via MatterRead. Apple Home's HAP service
// rebuild reads the full attribute set; without this the dispatcher
// falls back to MatterReportable's two-attribute surface.
func (s garageWCServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrType,
		matterAttrConfigStatus,
		matterAttrOperationalStatus,
		matterAttrEndProductType,
		matterAttrTargetPositionLiftPercent100ths,
		matterAttrCurrentPositionLiftPercent100ths,
		matterAttrMode,
	}
}

// extractGoToPercentage pulls a uint16 LiftPercent100thsValue or
// TiltPercent100thsValue out of the request payload. The bridge has
// already TLV-decoded the payload; we accept either a bare uint16 (the
// minimal "percent only" shape) or a map carrying a "percent" key.
// A typed request struct from
// internal/north/matter/cluster/windowcovering/ may replace this once
// that package exists.
//
// This deliberately ignores the variants that carry an `OptionsMask`
// and `OptionsOverride` field — those are the v1.0 GoToLiftPercentage
// shape; Matter 1.3+ (incl. 1.5.1) uses a single Percent100ths field.
//
// Unused parameter `fields` is named so the linter knows it is the
// dispatch input.
func extractGoToPercentage(fields any) (uint16, error) {
	switch v := fields.(type) {
	case uint16:
		return v, nil
	case map[string]any:
		raw, ok := v["percent"]
		if !ok {
			return 0, fmt.Errorf("%w: GoTo*Percentage missing percent field", errMatterValueType)
		}
		pct, ok := raw.(uint16)
		if !ok {
			return 0, fmt.Errorf("%w: GoTo*Percentage percent expected uint16, got %T", errMatterValueType, raw)
		}
		return pct, nil
	default:
		return 0, fmt.Errorf("%w: GoTo*Percentage expected uint16 or map[string]any, got %T", errMatterValueType, fields)
	}
}
