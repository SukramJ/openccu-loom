// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
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
	// All three implement OnMatterValueChanged explicitly below,
	// fanning every cluster state carrier (position + motion, plus
	// tilt for Blind) into one notifier.
	_ interfaces.MatterChangeNotifier = (*Cover)(nil)
	_ interfaces.MatterChangeNotifier = (*Blind)(nil)
	_ interfaces.MatterChangeNotifier = (*Garage)(nil)
	// Every actuation on this cluster is a command, so the three cluster
	// servers must advertise them: the dispatcher answers
	// AcceptedCommandList from this capability and falls back to an empty
	// list without it, which reads to a controller as "nothing to invoke".
	_ interfaces.MatterClusterCommandLister = coverWCServer{}
	_ interfaces.MatterClusterCommandLister = blindWCServer{}
)

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier] for
// Cover. The WindowCovering projection has two independent state
// carriers: the lift position (the embedded LEVEL *generic.Float) and
// the motion parameter (DIRECTION / ACTIVITY_STATE) that drives
// OperationalStatus and the inferred TargetPosition. Fan both into the
// bridge's dirty-marking — a movement start updates only the motion
// parameter, so without it the Stopped→Moving transition ships no
// proactive report at all and a controller learns about the motion only
// from the eventual position echo. Mirrors the reactor set matter.js
// installs in WindowCoveringServer.ts initialize() (:147-155), which
// reacts to position and operational-status sources alike.
func (c *Cover) OnMatterValueChanged(cb func()) func() {
	if c == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		c.Float.OnMatterValueChanged(cb),
		c.directionDp.OnMatterValueChanged(cb),
	)
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier] for
// Blind: the Cover carriers (LEVEL + motion) plus the slat-tilt axis
// (LEVEL_2) feeding CurrentPositionTiltPercent100ths.
func (b *Blind) OnMatterValueChanged(cb func()) func() {
	if b == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		b.Cover.OnMatterValueChanged(cb),
		b.level2.OnMatterValueChanged(cb),
	)
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier] for
// Garage. Unlike Cover/Blind it does not embed a *generic.Float; its
// WindowCovering projection is driven by the door state (open/closed/venting)
// and the optional section DP. Fan both in so a door operated at the wall
// button or by a CCU program reaches Apple's Subscribe rather than only the
// commands Apple itself sent.
func (g *Garage) OnMatterValueChanged(cb func()) func() {
	if g == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		g.doorStateDp.OnMatterValueChanged(cb),
		g.sectionDp.OnMatterValueChanged(cb),
	)
}

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

	// Type (0x0000) enum values, verbatim from matter.js TypeEnum
	// (packages/model/src/standard/elements/window-covering-cluster.element.ts:152-162).
	matterWCTypeRollerShade   uint8 = 0 // Rollershade
	matterWCTypeDrapery       uint8 = 4 // Drapery
	matterWCTypeAwning        uint8 = 5 // Awning
	matterWCTypeShutter       uint8 = 6 // Shutter
	matterWCTypeTiltBlindLift uint8 = 8 // TiltBlindLift (lift + tilt)

	// EndProductType (0x000D) enum values, verbatim from matter.js
	// EndProductTypeEnum
	// (packages/model/src/standard/elements/window-covering-cluster.element.ts:166-192).
	// Only the values this package projects are enumerated. The two
	// enums share a numeric space with UNRELATED meanings — reusing a
	// TypeEnum code as EndProductType silently reports a different
	// product (TypeEnum Drapery=4 reads as EndProductType PleatedShade).
	matterWCEndProductRollerShade        uint8 = 0  // RollerShade
	matterWCEndProductInteriorBlind      uint8 = 10 // InteriorBlind (0x0A)
	matterWCEndProductCentralCurtain     uint8 = 16 // CentralCurtain (0x10)
	matterWCEndProductRollerShutter      uint8 = 17 // RollerShutter (0x11)
	matterWCEndProductAwningTerracePatio uint8 = 19 // AwningTerracePatio (0x13)

	// OperationalStatus motion codes (2-bit field replicated across
	// global / lift / tilt nibbles).
	matterWCMotionStopped uint8 = 0b00
	matterWCMotionOpening uint8 = 0b01
	matterWCMotionClosing uint8 = 0b10

	// ConfigStatus (0x0007) bitmap bits (window-covering-cluster.element.ts:108-117).
	// LiftMovementReversed sits at bit 2 — a WindowCovering that has never
	// received a Mode write setting MotorDirectionReversed leaves it
	// clear. LiftPositionAware / TiltPositionAware (bits 3/4) mirror the
	// position-aware features every projection in this file always
	// advertises in FeatureMap; matter.js WindowCoveringServer.ts:120-135
	// initialize() sets them from those same features.
	matterWCConfigOperational       uint8 = 1 << 0
	matterWCConfigLiftPositionAware uint8 = 1 << 3
	matterWCConfigTiltPositionAware uint8 = 1 << 4

	// matterWCConfigStatusLift / matterWCConfigStatusLiftTilt are the
	// ConfigStatus values this projection reports: Operational plus the
	// position-aware bit(s) the device type always advertises. No
	// projection here sets LiftMovementReversed — that only flips on a
	// Mode write with MotorDirectionReversed set
	// (WindowCoveringServer.ts:188), and HM covers have no motor-reversal
	// engine to drive that write's effect.
	matterWCConfigStatusLift     = matterWCConfigOperational | matterWCConfigLiftPositionAware
	matterWCConfigStatusLiftTilt = matterWCConfigOperational | matterWCConfigLiftPositionAware | matterWCConfigTiltPositionAware

	// matterWCModeMax is the Mode (0x0017) attribute's "constraint: max
	// 15" — the four ModeBitmap bits (MotorDirectionReversed,
	// CalibrationMode, MaintenanceMode, LedFeedback;
	// window-covering-cluster.element.ts:76-79,120-125) span the full
	// legal range, so any set bit above that is a reserved/unsupported
	// value and must be rejected.
	matterWCModeMax uint8 = 0x0F
	// matterWCModeDefault is the Mode attribute's spec default (0).
	matterWCModeDefault uint8 = 0
)

var (
	errMatterUnknownAttribute = errors.New("matter: unknown attribute")
	errMatterUnknownCommand   = errors.New("matter: unknown command")
	errMatterValueType        = errors.New("matter: unexpected value type")
)

// matterTargetState stores the last commanded WindowCovering target
// per axis, in Matter percent-100ths (0 = open, 10000 = closed).
// matter.js keeps TargetPosition*Percent100ths as the commanded
// destination — UpOrOpen/DownOrClose/GoTo*Percentage set it
// (WindowCoveringServer.ts:522/:546/:578/:600) and StopMotion snaps
// it back to the current position (:490-493). A nil slot means "no
// command in effect": the attribute read then mirrors
// CurrentPosition, matching both the startup initialisation
// (WindowCoveringServer.ts:142) and the post-stop snap.
type matterTargetState struct {
	mu   sync.Mutex
	lift *uint16
	tilt *uint16
}

func (t *matterTargetState) setLift(v uint16) {
	t.mu.Lock()
	t.lift = &v
	t.mu.Unlock()
}

func (t *matterTargetState) setTilt(v uint16) {
	t.mu.Lock()
	t.tilt = &v
	t.mu.Unlock()
}

// clear drops both axis targets — the attribute reads mirror
// CurrentPosition again (the StopMotion snap semantics,
// WindowCoveringServer.ts:490-493). Besides the StopMotion command
// handler, the ingest side calls this on an externally reported
// moving→stopped transition ([Cover.OnDirection] / [Garage.OnSection])
// so a stale commanded target does not outlive the motion it belonged
// to.
func (t *matterTargetState) clear() {
	t.mu.Lock()
	t.lift, t.tilt = nil, nil
	t.mu.Unlock()
}

func (t *matterTargetState) liftTarget() (uint16, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lift == nil {
		return 0, false
	}
	return *t.lift, true
}

func (t *matterTargetState) tiltTarget() (uint16, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tilt == nil {
		return 0, false
	}
	return *t.tilt, true
}

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
// directly to TiltBlindLift.
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

// coverEndProductTypeFor maps a [CoverVariant] to the Matter
// WindowCovering EndProductType attribute (0x000D). EndProductType
// names the finished product the motor drives (matter.js
// window-covering-cluster.element.ts:166-192), so it does NOT reuse
// the TypeEnum code from [coverTypeFor]. CentralCurtain is the
// neutral pick among the three curtain geometries (LateralLeft /
// LateralRight / Central) — the HM channel does not report which one
// the actuator drives. VariantBlind normally projects via
// [Blind.MatterClusterServers]; the mapping here keeps a plain Cover
// carrying that variant consistent.
func coverEndProductTypeFor(v CoverVariant) uint8 {
	switch v {
	case VariantAwning:
		return matterWCEndProductAwningTerracePatio
	case VariantCurtain:
		return matterWCEndProductCentralCurtain
	case VariantShutter, VariantWindow:
		return matterWCEndProductRollerShutter
	case VariantBlind:
		return matterWCEndProductInteriorBlind
	case VariantShade, VariantDamper, VariantGarage:
		return matterWCEndProductRollerShade
	default:
		return matterWCEndProductRollerShade
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

// liftTargetRead computes the TargetPositionLiftPercent100ths (0x000B)
// read for a WindowCovering projection, reconciling the last commanded
// target with the externally observed motion state.
//
// matter.js only ever has to derive in the opposite direction: its
// WindowCoveringServer computes OperationalStatus FROM target-vs-current
// (#handleLiftTargetPositionChanging → #computeOperationalState,
// WindowCoveringServer.ts:215-222, :271-281) because on a native device
// every movement starts with a Matter command that first sets the
// target. A bridged HM cover moves the other way round: a wall button
// or CCU program starts motion that only surfaces as a DIRECTION /
// ACTIVITY_STATE push, with no Matter command updating the stored
// target. Reporting the stale commanded target then makes controllers
// that derive the motion arrow from target-vs-current (Apple Home)
// render the wrong or no direction, so the target is inferred from the
// motion instead:
//
//   - opening (percent100ths decreasing toward 0): keep the commanded
//     target only when it lies strictly ahead of the current position
//     (target < current); otherwise report the direction limit 0.
//   - closing: mirror image — keep target > current, else 10000.
//   - stopped: report the commanded target while one is in effect,
//     otherwise mirror CurrentPosition (the startup initialisation at
//     WindowCoveringServer.ts:142 and the StopMotion snap at :490).
//
// The moving→stopped snap that clears a stale commanded target lives on
// the ingest side ([Cover.OnDirection] / [Garage.OnSection]) so a
// commanded-but-not-yet-moving target survives reads.
//
// Returns follow the MatterRead contract: (nil, true) when the value is
// transiently unobservable.
func liftTargetRead(t *matterTargetState, position func() (custom.Position, bool), opening, closing bool) (any, bool) {
	commanded, hasCommanded := t.liftTarget()
	pos, hasPos := position()
	var current uint16
	if hasPos {
		current = hmLevelToMatterPct100ths(pos.Level())
	}
	switch {
	case opening:
		if hasCommanded && hasPos && commanded < current {
			return commanded, true
		}
		return uint16(0), true
	case closing:
		if hasCommanded && hasPos && commanded > current {
			return commanded, true
		}
		return matterCoverPctMax, true
	case hasCommanded:
		return commanded, true
	case !hasPos:
		return nil, true
	default:
		return current, true
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

// MatterDeviceType for Garage is Closure (0x0230), not WindowCovering.
//
// A garage drive's travel has named stops, and WindowCovering has only a
// lift percentage: the ventilation stop had to be expressed as a position
// near the middle, which no controller can label and no read can tell
// apart from a door that happens to be halfway. The Closure device type
// requires ClosureControl and forbids WindowCovering outright (matter.js
// closure-device.element.ts:20, conformance "X"), so the drive projects
// one cluster, not two.
func (g *Garage) MatterDeviceType() uint16 { return matterDeviceTypeClosure }

// MatterClusterServers for Garage returns the ClosureControl projection.
func (g *Garage) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{g.closure.get(g)}
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
		return coverEndProductTypeFor(s.c.Variant), true
	case matterAttrConfigStatus:
		// Operational | LiftPositionAware — this projection always
		// advertises PA_LF in FeatureMap. See matterWCConfigStatusLift.
		return matterWCConfigStatusLift, true
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
		// TargetPosition carries the last commanded destination while a
		// command is in effect (WindowCoveringServer.ts:522/:546/:578);
		// with none it mirrors CurrentPosition, matching the matter.js
		// startup initialisation (:142) and the StopMotion snap (:490).
		// External movement overrides a stale commanded target — see
		// [liftTargetRead].
		return liftTargetRead(&s.c.matterTarget, s.c.Position, s.c.IsOpening(), s.c.IsClosing())
	case matterAttrMode:
		return matterWCModeDefault, true
	case matterAttrFeatureMap:
		return matterWCFeatureLift | matterWCFeaturePositionAwLft, true
	case matterAttrClusterRevision:
		return matterWindowCoveringClusterRevision, true
	default:
		return nil, false
	}
}

// MatterWrite implements [interfaces.MatterClusterServer]. Mode
// (0x0017) is the only writable attribute — window-covering-cluster.
// element.ts:76-79 declares it "RW VM" with constraint "max 15". See
// [validateWindowCoveringMode] for the accept-but-inert rationale.
func (s coverWCServer) MatterWrite(_ context.Context, attrID uint32, value any, _ hmenum.CommandPriority) error {
	if attrID != matterAttrMode {
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
	return validateWindowCoveringMode(value)
}

// MinWritePrivilege implements
// [interfaces.MatterClusterAttributeWritePrivilege]: Mode is RW VM
// (Manage) per window-covering-cluster.element.ts:76-79.
func (s coverWCServer) MinWritePrivilege(_ uint32) uint8 { return 4 }

func (s coverWCServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdUpOrOpen:
		// A pending debounced GoTo write is stale intent once a
		// full-open command lands — drop it before the immediate write.
		s.c.matterGoTo.cancel(goToAxisLift)
		// Target lift = fully open (0). WindowCoveringServer.ts:522.
		err = s.c.Open(ctx, priority)
		if err == nil {
			s.c.matterTarget.setLift(0)
		}
	case matterCmdDownOrClose:
		s.c.matterGoTo.cancel(goToAxisLift)
		// Target lift = fully closed (10000). WindowCoveringServer.ts:546.
		err = s.c.Close(ctx, priority)
		if err == nil {
			s.c.matterTarget.setLift(matterCoverPctMax)
		}
	case matterCmdStopMotion:
		// Stop pre-empts queued motion: a debounced GoTo write firing
		// after the STOP would restart the movement the user just
		// halted.
		s.c.matterGoTo.cancelAll()
		// Snap the target back to the current position.
		// WindowCoveringServer.ts:490 handleStopMovement.
		err = s.c.Stop(ctx, priority)
		if err == nil {
			s.c.matterTarget.clear()
		}
	case matterCmdGoToLiftPercentage:
		pct, e := extractGoToPercentage(fields)
		if e != nil {
			return nil, e
		}
		// Target lift = requested value, stored immediately
		// (WindowCoveringServer.ts:578); the CCU write itself is
		// debounced — see [dispatchGoToPercentage].
		dispatchGoToPercentage(ctx, &s.c.matterGoTo, goToAxisLift, s.c.Address(), pct,
			s.c.Position, s.c.matterTarget.setLift,
			func(ctx context.Context, hmLevel float64) error {
				return s.c.SetPosition(ctx, hmLevel, priority)
			})
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.c.dataVersion.Bump()
	return nil, nil
}

// MatterReportable lists the attributes that ship proactive reports on
// a state-carrier push. TargetPositionLift must be in the set: Apple
// Home derives the displayed motion arrow from target-vs-current, and
// the inferred target ([liftTargetRead]) changes on motion transitions
// without any Matter command touching the stored value — leaving it out
// keeps the arrow stale or absent during externally started movement.
func (s coverWCServer) MatterReportable() []uint32 {
	return []uint32{
		matterAttrOperationalStatus,
		matterAttrTargetPositionLiftPercent100ths,
		matterAttrCurrentPositionLiftPercent100ths,
	}
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

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister].
// UpOrOpen / DownOrClose / StopMotion are conformance "M" and
// GoToLiftPercentage is "LF & PA_LF", the feature pair this projection
// advertises (window-covering-cluster.element.ts:85-96). Mode (0x0017) is
// the cluster's only writable attribute, so without this list a controller
// sees no way to actuate the cover at all and renders it read-only.
func (s coverWCServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		matterCmdUpOrOpen,
		matterCmdDownOrClose,
		matterCmdStopMotion,
		matterCmdGoToLiftPercentage,
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister].
// Every WindowCovering command answers with a plain status
// (`response: "status"`, window-covering-cluster.element.ts:85-105).
func (s coverWCServer) MatterGeneratedCommands() []uint32 { return nil }

// blindWCServer projects a [Blind] (lift + tilt) onto the
// WindowCovering cluster.
type blindWCServer struct{ b *Blind }

func (s blindWCServer) MatterClusterID() uint32 { return matterClusterWindowCovering }

func (s blindWCServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrType:
		return matterWCTypeTiltBlindLift, true
	case matterAttrEndProductType:
		// InteriorBlind (10) — the lift+tilt interior product in
		// EndProductTypeEnum (window-covering-cluster.element.ts:177).
		// TiltOnlyInteriorBlind (9) would drop the lift axis a Blind
		// always carries via the embedded Cover's LEVEL position.
		return matterWCEndProductInteriorBlind, true
	case matterAttrConfigStatus:
		// Operational | LiftPositionAware | TiltPositionAware — this
		// projection always advertises PA_LF and PA_TL in FeatureMap.
		// See matterWCConfigStatusLiftTilt.
		return matterWCConfigStatusLiftTilt, true
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
		// Last commanded lift destination; mirrors CurrentPosition when
		// no command is in effect, and external movement overrides a
		// stale commanded target. See coverWCServer.MatterRead and
		// [liftTargetRead]. The tilt axis below has no motion signal in
		// the model, so no inference applies there.
		return liftTargetRead(&s.b.matterTarget, s.b.Position, s.b.IsOpening(), s.b.IsClosing())
	case matterAttrCurrentPositionTiltPercent100ths:
		tilt, ok := s.b.TiltPosition()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(tilt.Level()), true
	case matterAttrTargetPositionTiltPercent100ths:
		// Last commanded tilt destination; mirrors CurrentPositionTilt
		// when no command is in effect (WindowCoveringServer.ts:600 sets
		// it on GoToTiltPercentage, :524/:548 on UpOrOpen/DownOrClose).
		if v, ok := s.b.matterTarget.tiltTarget(); ok {
			return v, true
		}
		tilt, ok := s.b.TiltPosition()
		if !ok {
			return nil, true
		}
		return hmLevelToMatterPct100ths(tilt.Level()), true
	case matterAttrMode:
		return matterWCModeDefault, true
	case matterAttrFeatureMap:
		return matterWCFeatureLift | matterWCFeatureTilt |
			matterWCFeaturePositionAwLft | matterWCFeaturePositionAwTlt, true
	case matterAttrClusterRevision:
		return matterWindowCoveringClusterRevision, true
	default:
		return nil, false
	}
}

// MatterWrite implements [interfaces.MatterClusterServer]. See
// [coverWCServer.MatterWrite] — the Mode attribute has the same
// accept-valid-writes contract on every WindowCovering variant.
func (s blindWCServer) MatterWrite(_ context.Context, attrID uint32, value any, _ hmenum.CommandPriority) error {
	if attrID != matterAttrMode {
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
	return validateWindowCoveringMode(value)
}

// MinWritePrivilege implements
// [interfaces.MatterClusterAttributeWritePrivilege]: Mode is RW VM
// (Manage) per window-covering-cluster.element.ts:76-79.
func (s blindWCServer) MinWritePrivilege(_ uint32) uint8 { return 4 }

func (s blindWCServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdUpOrOpen:
		// The HM motor drives both axes in one motion — a full-open
		// supersedes pending debounced GoTo writes on either axis.
		s.b.matterGoTo.cancelAll()
		// Both position-aware axes target fully open (0).
		// WindowCoveringServer.ts:522-525.
		err = s.b.Open(ctx, priority)
		if err == nil {
			s.b.matterTarget.setLift(0)
			s.b.matterTarget.setTilt(0)
		}
	case matterCmdDownOrClose:
		s.b.matterGoTo.cancelAll()
		// Both position-aware axes target fully closed (10000).
		// WindowCoveringServer.ts:546-549.
		err = s.b.Close(ctx, priority)
		if err == nil {
			s.b.matterTarget.setLift(matterCoverPctMax)
			s.b.matterTarget.setTilt(matterCoverPctMax)
		}
	case matterCmdStopMotion:
		// Stop pre-empts queued motion — see coverWCServer.MatterInvoke.
		s.b.matterGoTo.cancelAll()
		// Snap both targets back to the current positions.
		// WindowCoveringServer.ts:490-493 handleStopMovement.
		err = s.b.Stop(ctx, priority)
		if err == nil {
			s.b.matterTarget.clear()
		}
	case matterCmdGoToLiftPercentage:
		pct, e := extractGoToPercentage(fields)
		if e != nil {
			return nil, e
		}
		// Target stored immediately (WindowCoveringServer.ts:578); the
		// combined-parameter CCU write is debounced per axis — see
		// [dispatchGoToPercentage].
		dispatchGoToPercentage(ctx, &s.b.matterGoTo, goToAxisLift, s.b.Address(), pct,
			s.b.Position, s.b.matterTarget.setLift,
			func(ctx context.Context, hmLevel float64) error {
				return s.b.SetPosition(ctx, hmLevel, priority)
			})
	case matterCmdGoToTiltPercentage:
		pct, e := extractGoToPercentage(fields)
		if e != nil {
			return nil, e
		}
		// Target stored immediately (WindowCoveringServer.ts:600).
		dispatchGoToPercentage(ctx, &s.b.matterGoTo, goToAxisTilt, s.b.Address(), pct,
			s.b.TiltPosition, s.b.matterTarget.setTilt,
			func(ctx context.Context, hmLevel float64) error {
				return s.b.SetTilt(ctx, hmLevel, priority)
			})
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.b.dataVersion.Bump()
	return nil, nil
}

// MatterReportable includes both axes' Target attributes — see
// [coverWCServer.MatterReportable] for the target-vs-current rationale.
func (s blindWCServer) MatterReportable() []uint32 {
	return []uint32{
		matterAttrOperationalStatus,
		matterAttrTargetPositionLiftPercent100ths,
		matterAttrTargetPositionTiltPercent100ths,
		matterAttrCurrentPositionLiftPercent100ths,
		matterAttrCurrentPositionTiltPercent100ths,
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

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister].
// The blind is the one projection that advertises TL & PA_TL, so it adds
// GoToTiltPercentage (conformance "TL & PA_TL") to the lift surface — see
// [coverWCServer.MatterAcceptedCommands] and
// window-covering-cluster.element.ts:85-105.
func (s blindWCServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		matterCmdUpOrOpen,
		matterCmdDownOrClose,
		matterCmdStopMotion,
		matterCmdGoToLiftPercentage,
		matterCmdGoToTiltPercentage,
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister].
func (s blindWCServer) MatterGeneratedCommands() []uint32 { return nil }

// extractGoToPercentage pulls the LiftPercent100thsValue /
// TiltPercent100thsValue (context tag 0) out of a GoToLiftPercentage /
// GoToTiltPercentage request. GoTo*Percentage has no typed decoder in
// the bridge, so the real wire path lands here as the tag-keyed
// map[uint8]any that decodeGenericTagMap produces (unsigned ints as
// uint64) — see internal/north/matter/bridge/fields_reader.go. The
// bare-uint16 and string-keyed shapes are kept for the in-package
// tests.
//
// The value is clamped to 10000 (Percent100ths max, Matter §5.3):
// matter.js WindowCoveringServer rejects an out-of-range percentage via
// the schema constraint, and an unclamped value would otherwise convert
// to an HM domain level above 1.0.
//
// This deliberately ignores the variants that carry an `OptionsMask`
// and `OptionsOverride` field — those are the v1.0 GoToLiftPercentage
// shape; Matter 1.3+ (incl. 1.5.1) uses a single Percent100ths field.
func extractGoToPercentage(fields any) (uint16, error) {
	switch v := fields.(type) {
	case uint16:
		return clampPercent100ths(v), nil
	case map[uint8]any:
		raw, ok := v[0]
		if !ok {
			return 0, fmt.Errorf("%w: GoTo*Percentage missing percent field (tag 0)", errMatterValueType)
		}
		pct, ok := wireUint16(raw)
		if !ok {
			return 0, fmt.Errorf("%w: GoTo*Percentage percent expected integer, got %T", errMatterValueType, raw)
		}
		return clampPercent100ths(pct), nil
	case map[string]any:
		raw, ok := v["percent"]
		if !ok {
			return 0, fmt.Errorf("%w: GoTo*Percentage missing percent field", errMatterValueType)
		}
		pct, ok := raw.(uint16)
		if !ok {
			return 0, fmt.Errorf("%w: GoTo*Percentage percent expected uint16, got %T", errMatterValueType, raw)
		}
		return clampPercent100ths(pct), nil
	default:
		return 0, fmt.Errorf("%w: GoTo*Percentage expected uint16 or map[uint8]any, got %T", errMatterValueType, fields)
	}
}

// clampPercent100ths bounds a Percent100ths value to its spec maximum of
// 10000 (100.00 %).
func clampPercent100ths(pct uint16) uint16 {
	if pct > 10000 {
		return 10000
	}
	return pct
}

// wireUint16 reads an unsigned integer out of a value decoded from the
// generic tag-keyed fields map, where decodeGenericTagMap stores
// unsigned ints as uint64. The uint16/uint8 cases keep the helper usable
// from tests that pass narrower Go types directly.
func wireUint16(raw any) (uint16, bool) {
	switch n := raw.(type) {
	case uint64:
		return uint16(n & 0xFFFF), true
	case uint16:
		return n, true
	case uint8:
		return uint16(n), true
	default:
		return 0, false
	}
}

// wireUint8 is the 8-bit sibling of [wireUint16].
func wireUint8(raw any) (uint8, bool) {
	switch n := raw.(type) {
	case uint64:
		return uint8(n & 0xFF), true
	case uint8:
		return n, true
	default:
		return 0, false
	}
}

// validateWindowCoveringMode accepts a Mode (0x0017) attribute write
// when the value is within the ModeBitmap's legal range (constraint
// "max 15": MotorDirectionReversed / CalibrationMode / MaintenanceMode /
// LedFeedback, window-covering-cluster.element.ts:76-79,120-125) and
// rejects anything wider. HM covers have no maintenance / calibration /
// motor-reversal engine to react to the write, so a constraint-valid
// value is accepted without changing behaviour — the same "accepted but
// inert" treatment this projection already gives capability-absent
// commands (e.g. Stop on a cover with no in-flight ramp).
func validateWindowCoveringMode(value any) error {
	m, ok := wireUint8(value)
	if !ok {
		return fmt.Errorf("%w: Mode write expected uint8, got %T", errMatterValueType, value)
	}
	if m > matterWCModeMax {
		return fmt.Errorf("%w: Mode constraint max 15, got %d", errMatterValueType, m)
	}
	return nil
}
