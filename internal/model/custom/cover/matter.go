// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"errors"
	"sync"

	fabriccover "github.com/SukramJ/go-fabric/cluster/cover"
	windowcoveringspec "github.com/SukramJ/go-fabric/cluster/spec/windowcovering"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterDispatchPriority is the southbound urgency every Matter-driven
// write and invoke carries. The bridge is a controller-facing
// foreground path — a tap in a Matter app must not queue behind a
// background refresh — so it dispatches at High, and the cluster
// contract no longer negotiates it per call.
//
// Spelled out as a constant rather than left to a variable: the zero
// value of [hmenum.CommandPriority] is Critical, so anything that
// reached these calls defaulted would silently escalate every bridged
// command.
const matterDispatchPriority = hmenum.CommandPriorityHigh

// Compile-time assertions: Cover, Blind, and Garage participate in the
// Matter source surface (ADR 0012). Cover and Blind project onto a
// WindowCovering (0x0102) cluster served by go-fabric's
// [fabriccover.WindowCoveringServer] since the ADR 0084 WindowCovering
// follow-up — the lift axis for a Cover, lift and tilt for a Blind —
// with the cover's CCU writes behind its [fabriccover.Mover] port and
// the CCU's confirmed position and motion fed back through the server's
// inbound setters. Garage projects a ClosureControl cluster of its own
// ([Garage.MatterClusterServers]).
var (
	_ interfaces.MatterEndpointSource     = (*Cover)(nil)
	_ interfaces.MatterEndpointSource     = (*Blind)(nil)
	_ interfaces.MatterEndpointSource     = (*Garage)(nil)
	_ interfaces.MatterClusterDataVersion = (*Garage)(nil)
	// All three implement OnMatterValueChanged explicitly below,
	// fanning every cluster state carrier (position + motion, plus
	// tilt for Blind) into one notifier. The WindowCovering attributes
	// themselves are reported by the server's own change notifier once
	// the confirmed value reaches it.
	_ interfaces.MatterChangeNotifier = (*Cover)(nil)
	_ interfaces.MatterChangeNotifier = (*Blind)(nil)
	_ interfaces.MatterChangeNotifier = (*Garage)(nil)

	_ fabriccover.Mover = wcMover{}
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

// The Matter device type of the cover projections (matter.js
// packages/node/src/devices/window-covering.ts). The WindowCovering
// cluster's ids, revision, feature bits, enums, attribute and command
// lists come from go-fabric's generated definition behind
// [fabriccover.WindowCoveringServer].
const (
	matterDeviceTypeWindowCovering uint16 = 0x0202

	// matterCoverPctMax is the Matter percent100ths saturation point —
	// 100.00% in 0.01% units.
	matterCoverPctMax uint16 = 10000

	// OperationalStatus motion codes (window-covering-cluster.element.ts
	// MovementStatus: Stopped 0, Opening 1, Closing 2) and the bitmap
	// layout the server stores verbatim: global in bits 0-1, lift in
	// bits 2-3, tilt in bits 4-5 (:128-131). The Blind's tilt bits stay
	// 00 — the model carries no tilt motion signal.
	matterWCMotionStopped uint8 = 0b00
	matterWCMotionOpening uint8 = 0b01
	matterWCMotionClosing uint8 = 0b10
	matterWCLiftShift     uint8 = 2
)

// errMatterUnknownCommand is the Garage closure projection's answer to a
// command id its cluster does not carry.
var errMatterUnknownCommand = errors.New("matter: unknown command")

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
func coverTypeFor(v CoverVariant) windowcoveringspec.TypeEnum {
	switch v {
	case VariantAwning:
		return windowcoveringspec.TypeAwning
	case VariantCurtain:
		return windowcoveringspec.TypeDrapery
	case VariantShutter, VariantWindow:
		return windowcoveringspec.TypeShutter
	case VariantShade, VariantDamper, VariantBlind, VariantGarage:
		return windowcoveringspec.TypeRollershade
	default:
		return windowcoveringspec.TypeRollershade
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
func coverEndProductTypeFor(v CoverVariant) windowcoveringspec.EndProductTypeEnum {
	switch v {
	case VariantAwning:
		return windowcoveringspec.EndProductTypeAwningTerracePatio
	case VariantCurtain:
		return windowcoveringspec.EndProductTypeCentralCurtain
	case VariantShutter, VariantWindow:
		return windowcoveringspec.EndProductTypeRollerShutter
	case VariantBlind:
		return windowcoveringspec.EndProductTypeInteriorBlind
	case VariantShade, VariantDamper, VariantGarage:
		return windowcoveringspec.EndProductTypeRollerShade
	default:
		return windowcoveringspec.EndProductTypeRollerShade
	}
}

// motionForOpeningClosing packs the (opening, closing) booleans into
// the 2-bit MovementStatus code of the OperationalStatus bitmap.
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

// matterWCHolder keeps the one WindowCovering server a cover serves
// across [interfaces.MatterEndpointSource.MatterClusterServers] calls,
// for the same reason [generic.MatterOnOffHolder] does: the assembler
// re-reads the servers on every reassembly and the bridge keeps the ones
// it already serves; a server rebuilt each time would lose its target
// and the subscription state behind it. It lives on the [Cover] so the
// Blind, which embeds one, shares it.
type matterWCHolder struct {
	mu  sync.Mutex
	srv *fabriccover.WindowCoveringServer
}

// server returns the held server, building it through build on first
// use.
func (h *matterWCHolder) server(build func() *fabriccover.WindowCoveringServer) *fabriccover.WindowCoveringServer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv == nil {
		h.srv = build()
	}
	return h.srv
}

// current returns the held server, nil while none was built.
func (h *matterWCHolder) current() *fabriccover.WindowCoveringServer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.srv
}

// wcPorts is what a cover variant hands the shared server builder: the
// FeatureMap it serves and the device operations behind the Mover. The
// Blind supplies its own open / close / stop / position methods (they
// drive both axes through one combined write) and the tilt axis; a
// plain Cover leaves the tilt fields nil.
type wcPorts struct {
	features             uint32
	open, close, stop    func(ctx context.Context, priority hmenum.CommandPriority) error
	setPosition, setTilt func(ctx context.Context, level float64, priority hmenum.CommandPriority) error
	tiltPosition         func() (custom.Position, bool)
	onTiltConfirmed      func(fn func())
}

// wcMover is the [fabriccover.Mover] of a cover: the server validates
// the command and stores the target, the Mover carries it to the CCU.
// UpOrOpen and DownOrClose arrive as one GoTo with every served axis at
// its limit and become the cover's Open / Close — the HM motor drives
// both axes in one motion; a lift target at a limit is the same call
// and goes out at once, any other target is slider-gesture debounced
// ([dispatchGoToPercentage]); StopMotion becomes Stop at Critical
// priority and drops every pending debounced write.
type wcMover struct {
	c     *Cover
	ports wcPorts
}

// GoTo implements [fabriccover.Mover].
func (m wcMover) GoTo(ctx context.Context, lift, tilt *uint16) error {
	m.c.matterCommanded.Store(true)
	if lift != nil && tilt != nil && *lift == *tilt && (*lift == 0 || *lift == matterCoverPctMax) {
		// UpOrOpen / DownOrClose on a lift+tilt server
		// (WindowCoveringServer.ts:521-535, :545-559 set both axes).
		m.c.matterGoTo.cancelAll()
		if *lift == 0 {
			return m.ports.open(ctx, matterDispatchPriority)
		}
		return m.ports.close(ctx, matterDispatchPriority)
	}
	if lift != nil {
		if err := m.goToLift(ctx, *lift); err != nil {
			return err
		}
	}
	if tilt != nil && m.ports.setTilt != nil {
		dispatchGoToPercentage(ctx, &m.c.matterGoTo, goToAxisTilt, m.c.Address(), *tilt,
			m.ports.tiltPosition, m.atPriority(m.ports.setTilt))
	}
	return nil
}

// goToLift carries a lift target: the limits are the cover's Open /
// Close (the immediate write UpOrOpen / DownOrClose always had), any
// other value the debounced SetPosition.
func (m wcMover) goToLift(ctx context.Context, pct uint16) error {
	switch pct {
	case 0:
		m.c.matterGoTo.cancel(goToAxisLift)
		return m.ports.open(ctx, matterDispatchPriority)
	case matterCoverPctMax:
		m.c.matterGoTo.cancel(goToAxisLift)
		return m.ports.close(ctx, matterDispatchPriority)
	default:
		dispatchGoToPercentage(ctx, &m.c.matterGoTo, goToAxisLift, m.c.Address(), pct,
			m.c.Position, m.atPriority(m.ports.setPosition))
		return nil
	}
}

// atPriority binds a position write to [matterDispatchPriority].
func (wcMover) atPriority(write func(ctx context.Context, level float64, priority hmenum.CommandPriority) error) func(ctx context.Context, level float64) error {
	return func(ctx context.Context, level float64) error {
		return write(ctx, level, matterDispatchPriority)
	}
}

// Stop implements [fabriccover.Mover]. A debounced GoTo write firing
// after the STOP would restart the movement the user just halted, so
// the pending writes go first; the command in effect ends with the stop
// (the server has snapped the targets to the positions).
func (m wcMover) Stop(ctx context.Context) error {
	m.c.matterGoTo.cancelAll()
	m.c.matterCommanded.Store(false)
	return m.ports.stop(ctx, matterDispatchPriority)
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion] for Garage.
// Bumped on every successful MatterInvoke.
func (g *Garage) MatterDataVersion() uint32 { return g.dataVersion.Current() }

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

// MatterDeviceType implements [interfaces.MatterEndpointSource]. All
// cover variants surface as Matter WindowCovering (0x0202); the Type
// and EndProductType attributes inside the cluster carry the variant
// distinction.
func (c *Cover) MatterDeviceType() uint16 { return matterDeviceTypeWindowCovering }

// MatterClusterServers implements [interfaces.MatterEndpointSource]:
// the lift-only WindowCovering server with the Cover's own operations
// behind the Mover.
func (c *Cover) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{c.matterWindowCoveringServer(wcPorts{
		features:    fabriccover.ServedFeatureMap,
		open:        c.Open,
		close:       c.Close,
		stop:        c.Stop,
		setPosition: c.SetPosition,
	})}
}

// MatterDeviceType for Blind shadows the embedded Cover's method so
// the Blind's own projection is reachable through the source-surface
// interface.
func (b *Blind) MatterDeviceType() uint16 { return matterDeviceTypeWindowCovering }

// MatterClusterServers for Blind returns the lift+tilt projection, with
// the Blind's combined-write operations behind the Mover. Shadows the
// embedded Cover's method; the server is the one held on the embedded
// Cover, so whichever method the assembler reaches first builds it.
func (b *Blind) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{b.matterWindowCoveringServer(wcPorts{
		features:     fabriccover.ServedLiftTiltFeatureMap,
		open:         b.Open,
		close:        b.Close,
		stop:         b.Stop,
		setPosition:  b.SetPosition,
		setTilt:      b.SetTilt,
		tiltPosition: b.TiltPosition,
		onTiltConfirmed: func(fn func()) {
			if b.level2 != nil {
				_ = b.level2.OnConfirmedUpdate(func(_, _ float64) { fn() })
			}
		},
	})}
}

// matterWindowCoveringServer builds the cover's WindowCovering server
// once: go-fabric's server with the variant's Type / EndProductType and
// FeatureMap, the position the CCU last reported as the initial value
// (open until the first report), the cover's operations as its Mover,
// and the LEVEL (and LEVEL_2) data points' confirmed-update hooks
// feeding the CCU's values back through the inbound setters — the path
// that stores without asking the Mover, so a position set at the wall
// or by a CCU program reaches a subscribed controller and a confirmed
// command does not echo into a second write. Motion reaches the server
// through [Cover.OnDirection] → [Cover.syncMatterMotion].
func (c *Cover) matterWindowCoveringServer(ports wcPorts) *fabriccover.WindowCoveringServer {
	return c.matterWC.server(func() *fabriccover.WindowCoveringServer {
		c.matterPorts = ports
		wcType := coverTypeFor(c.Variant)
		endProduct := coverEndProductTypeFor(c.Variant)
		if ports.tiltPosition != nil {
			// The lift+tilt interior product (window-covering-cluster.
			// element.ts:177); TiltOnlyInteriorBlind (9) would drop the
			// lift axis a Blind always carries.
			wcType = windowcoveringspec.TypeTiltBlindLift
			endProduct = windowcoveringspec.EndProductTypeInteriorBlind
		}
		cfg := fabriccover.Config{
			Type:           uint8(wcType),
			EndProductType: uint8(endProduct),
			FeatureMap:     ports.features,
			Mover:          wcMover{c: c, ports: ports},
		}
		if pct, ok := c.matterLiftPosition(); ok {
			cfg.InitialPositionPercent100ths = pct
		}
		if pct, ok := c.matterTiltPosition(); ok {
			cfg.InitialTiltPositionPercent100ths = pct
		}
		srv := fabriccover.NewWindowCoveringServer(cfg)
		// The hooks close over srv rather than going through the holder:
		// the holder's lock is held while this builder runs, and the
		// first sync below runs inside it.
		if c.Float != nil {
			_ = c.OnConfirmedUpdate(func(_, _ float64) { c.syncMatterLiftTo(srv) })
		}
		if ports.onTiltConfirmed != nil {
			ports.onTiltConfirmed(func() { c.syncMatterTiltTo(srv) })
		}
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current values are stored
		// again, which is a no-op when nothing changed.
		c.syncMatterLiftTo(srv)
		c.syncMatterTiltTo(srv)
		c.syncMatterMotionTo(srv, false, c.moving())
		return srv
	})
}

// matterLiftPosition is the CCU's last reported lift position in
// percent100ths, false while unobserved.
func (c *Cover) matterLiftPosition() (uint16, bool) {
	pos, ok := c.Position()
	if !ok {
		return 0, false
	}
	return hmLevelToMatterPct100ths(pos.Level()), true
}

// matterTiltPosition is the CCU's last reported tilt position in
// percent100ths, false while unobserved or on a cover without a tilt
// axis.
func (c *Cover) matterTiltPosition() (uint16, bool) {
	if c.matterPorts.tiltPosition == nil {
		return 0, false
	}
	pos, ok := c.matterPorts.tiltPosition()
	if !ok {
		return 0, false
	}
	return hmLevelToMatterPct100ths(pos.Level()), true
}

// moving reports whether the cached motion state is an active movement
// in either direction.
func (c *Cover) moving() bool { return c.IsOpening() || c.IsClosing() }

// syncMatterLiftTo stores the confirmed lift position on srv. At rest
// with no command in effect the target follows it, so a cover moved
// without a Matter command (a wall button on a device that reports no
// motion parameter, a CCU program) reports a target equal to its
// position, as matter.js initialises the target from the position
// (WindowCoveringServer.ts:142) and the StopMotion snap leaves it
// (:490). A commanded target survives until the motion it started ends
// — [Cover.syncMatterMotion] snaps it on the moving→stopped transition.
func (c *Cover) syncMatterLiftTo(srv *fabriccover.WindowCoveringServer) {
	pct, ok := c.matterLiftPosition()
	if !ok {
		return
	}
	srv.SetCurrentPosition(&pct, nil)
	if !c.moving() && !c.matterCommanded.Load() {
		srv.SetTargetPosition(&pct, nil)
	}
}

// syncMatterTiltTo is [Cover.syncMatterLiftTo] for the tilt axis.
func (c *Cover) syncMatterTiltTo(srv *fabriccover.WindowCoveringServer) {
	pct, ok := c.matterTiltPosition()
	if !ok {
		return
	}
	srv.SetCurrentPosition(nil, &pct)
	if !c.moving() && !c.matterCommanded.Load() {
		srv.SetTargetPosition(nil, &pct)
	}
}

// syncMatterMotion stores the cover's motion on the WindowCovering
// server, if one was built; [Cover.OnDirection] calls it after every
// motion update with the moving state before and after it.
func (c *Cover) syncMatterMotion(wasMoving, nowMoving bool) {
	if srv := c.matterWC.current(); srv != nil {
		c.syncMatterMotionTo(srv, wasMoving, nowMoving)
	}
}

// syncMatterMotionTo stores OperationalStatus from the cached motion
// state and infers the target from it (by_design
// BD-Matter-WindowCovering-InferredTarget). matter.js derives
// OperationalStatus FROM target-vs-current (#handleLiftTargetPositionChanging
// → #computeOperationalState, WindowCoveringServer.ts:215-222, :271-281)
// because on a native device every movement starts with a Matter command
// that first sets the target. A bridged HM cover moves the other way
// round: a wall button or CCU program starts motion that only surfaces as
// a DIRECTION / ACTIVITY_STATE push, with no Matter command updating the
// stored target. Reporting the stale commanded target then makes
// controllers that derive the motion arrow from target-vs-current (Apple
// Home) render the wrong or no direction, so the target is inferred from
// the motion instead:
//
//   - opening (percent100ths decreasing toward 0): keep the stored
//     target only when it lies strictly ahead of the current position
//     (target < current); otherwise store the direction limit 0.
//   - closing: mirror image — keep target > current, else 10000.
//   - moving → stopped: snap every served axis's target to its position
//     (the handleStopMovement semantics, :485-493, applied to a stop the
//     CCU reports on its own — end position reached, wall button,
//     obstruction) and end the command in effect, so a commanded target
//     does not outlive the motion it belonged to.
//
// An unobserved position reports the direction limit while moving and
// leaves the target alone at the stop.
func (c *Cover) syncMatterMotionTo(srv *fabriccover.WindowCoveringServer, wasMoving, nowMoving bool) {
	opening, closing := c.IsOpening(), c.IsClosing()
	motion := motionForOpeningClosing(opening, closing)
	srv.SetOperationalStatus(motion | motion<<matterWCLiftShift)

	lift, hasLift := c.matterLiftPosition()
	switch {
	case opening:
		if target, ok := storedLiftTarget(srv); !ok || !hasLift || target >= lift {
			limit := uint16(0)
			srv.SetTargetPosition(&limit, nil)
		}
	case closing:
		if target, ok := storedLiftTarget(srv); !ok || !hasLift || target <= lift {
			limit := matterCoverPctMax
			srv.SetTargetPosition(&limit, nil)
		}
	case wasMoving && !nowMoving:
		c.matterCommanded.Store(false)
		var liftTarget, tiltTarget *uint16
		if hasLift {
			liftTarget = &lift
		}
		if tilt, ok := c.matterTiltPosition(); ok {
			tiltTarget = &tilt
		}
		srv.SetTargetPosition(liftTarget, tiltTarget)
	}
}

// storedLiftTarget reads the server's TargetPositionLiftPercent100ths.
func storedLiftTarget(srv *fabriccover.WindowCoveringServer) (uint16, bool) {
	v, ok := srv.MatterRead(windowcoveringspec.AttrTargetPositionLiftPercent100ths)
	if !ok {
		return 0, false
	}
	target, ok := v.(uint16)
	return target, ok
}
