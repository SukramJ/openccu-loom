// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wire

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// GenericSwitch implements Matter cluster 0x003B (Switch) per Matter
// Application Cluster Specification 1.5.1 §1.13. The bridge surfaces
// it for HM Button / Action DPs that fire press events.
//
// Attributes:
//
//   - 0x0000 NumberOfPositions    (uint8, mandatory)
//   - 0x0001 CurrentPosition       (uint8, mandatory; readable)
//   - 0x0002 MultiPressMax         (uint8, optional)
//   - 0xFFFC FeatureMap            (uint32)
//   - 0xFFFD ClusterRevision       (uint16)
//
// Events (cluster-emitted via [interfaces.MatterEventEmitter]):
//
//   - 0x00 SwitchLatched          (LS feature)
//   - 0x01 InitialPress           (MS / MSL / MSR / AS feature)
//   - 0x02 LongPress              (MSL feature)
//   - 0x03 ShortRelease           (MSR feature)
//   - 0x04 LongRelease            (MSL feature)
//   - 0x05 MultiPressOngoing      (MSM feature)
//   - 0x06 MultiPressComplete     (MSM feature)
//
// Bridge default features: MS (Momentary Switch) + MSR (Momentary
// Switch Release) — covers HM short-press handling. MSL (Long
// Press) flips on when the source DP advertises long-press
// recognition. AS / MSM / LS are off in v1.1.
//
// The cluster is event-driven: attribute reads return spec-mandated
// constants (CurrentPosition additionally reads live through the
// optional [GenericSwitchPositionSource] capability), and HM events
// arrive via [GenericSwitch.Fire*] methods fed from the model layer;
// the cluster forwards them to the bridge-injected
// [interfaces.MatterEventEmitter].
const (
	matterClusterGenericSwitch uint32 = 0x003B

	matterAttrSwitchNumberOfPositions uint32 = 0x0000
	matterAttrSwitchCurrentPosition   uint32 = 0x0001
	matterAttrSwitchMultiPressMax     uint32 = 0x0002
	matterAttrSwitchFeatureMap        uint32 = 0xFFFC
	matterAttrSwitchClusterRevision   uint32 = 0xFFFD

	// MatterEventSwitchLatched fires when a latching switch flips
	// position (LS feature).
	MatterEventSwitchLatched uint32 = 0x00
	// MatterEventInitialPress fires on the first press (MS / AS).
	MatterEventInitialPress uint32 = 0x01
	// MatterEventLongPress fires once the long-press threshold is
	// crossed (MSL feature).
	MatterEventLongPress uint32 = 0x02
	// MatterEventShortRelease fires when a short press ends (MSR).
	MatterEventShortRelease uint32 = 0x03
	// MatterEventLongRelease fires when a long press ends (MSL).
	MatterEventLongRelease uint32 = 0x04
	// MatterEventMultiPressOngoing fires during multi-press capture
	// (MSM feature).
	MatterEventMultiPressOngoing uint32 = 0x05
	// MatterEventMultiPressComplete fires once the multi-press window
	// closes (MSM feature).
	MatterEventMultiPressComplete uint32 = 0x06

	// SwitchClusterRevision per matter.js HEAD (@matter/model 0.16.11)
	// — Switch cluster bumped 1→2 in Matter 1.4 with the action-button
	// (AS) feature codification.
	switchClusterRevision uint16 = 2

	// FeatureMap bits (Matter §1.13.4):
	//   bit 0 = LS  (Latching Switch)
	//   bit 1 = MS  (Momentary Switch)
	//   bit 2 = MSR (Momentary Switch Release)
	//   bit 3 = MSL (Momentary Switch Long Press)
	//   bit 4 = MSM (Momentary Switch Multi Press)
	//   bit 5 = AS  (Action Switch)
	switchFeatureMS  uint32 = 1 << 1
	switchFeatureMSR uint32 = 1 << 2
	switchFeatureMSL uint32 = 1 << 3
	switchFeatureMSM uint32 = 1 << 4
)

// GenericSwitchSource is the model-side surface a HM button source
// (the per-channel press group, or a lone Button / Action DP) exposes.
// The cluster server reads `NumberOfPositions` once at construction
// and supports long-press recognition opt-in via `SupportsLongPress`.
type GenericSwitchSource interface {
	// MatterSwitchPositions returns the static NumberOfPositions
	// (Matter §1.13.5.1). Typical: 2 for a single button (idle +
	// pressed). 0 → cluster falls back to 2.
	MatterSwitchPositions() uint8
	// MatterSwitchSupportsLongPress reports whether the source can
	// distinguish long press from short press. When true, the
	// FeatureMap exposes MSL and the cluster forwards LongPress /
	// LongRelease events when the source fires them.
	MatterSwitchSupportsLongPress() bool
}

// GenericSwitchPositionSource is the optional source capability for a
// live CurrentPosition (Matter §1.13.5.2). Sources that run a press-
// cycle state machine report 1 (pressed) while a hold is open and 0
// (idle) otherwise, mirroring matter.js
// packages/node/src/behaviors/switch/SwitchServer.ts, where
// `state.currentPosition` moves to the pressed position for the
// duration of the press and returns to `momentaryNeutralPosition`
// (default 0) on release. Sources without the capability read as the
// constant neutral position.
type GenericSwitchPositionSource interface {
	MatterSwitchCurrentPosition() uint8
}

// GenericSwitch is the cluster-server. Implements
// [interfaces.MatterClusterServer] (read/write/invoke/reportable) and
// [interfaces.MatterEventReceiver] (bridge injects emitter at
// topology assembly).
type GenericSwitch struct {
	src      GenericSwitchSource
	endpoint uint16
	emitter  interfaces.MatterEventEmitter
}

// NewGenericSwitch wires the cluster-server against a model-side
// source on the given endpoint. The endpoint is captured so events
// can be addressed without the source needing to know its own ID.
func NewGenericSwitch(endpoint uint16, src GenericSwitchSource) *GenericSwitch {
	return &GenericSwitch{src: src, endpoint: endpoint}
}

// MatterClusterID identifies the Switch cluster (0x003B).
func (s *GenericSwitch) MatterClusterID() uint32 { return matterClusterGenericSwitch }

// SetMatterEventEmitter implements [interfaces.MatterEventReceiver].
// Called by the bridge during topology assembly so the cluster can
// fire events outside the request/response cycle.
func (s *GenericSwitch) SetMatterEventEmitter(emitter interfaces.MatterEventEmitter) {
	s.emitter = emitter
}

// MatterRead resolves attribute reads.
func (s *GenericSwitch) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrSwitchNumberOfPositions:
		n := s.src.MatterSwitchPositions()
		if n == 0 {
			n = 2
		}
		return n, true
	case matterAttrSwitchCurrentPosition:
		// Live position when the source tracks its press cycle
		// (see [GenericSwitchPositionSource]): 1 while a long press
		// is held, back to the neutral 0 after release. Short presses
		// arrive as a single CCU event (press + release in one
		// dispatch), so reads only observe 1 during a held long press.
		// Sources without press-cycle tracking stay at the constant
		// idle position.
		if pos, ok := s.src.(GenericSwitchPositionSource); ok {
			return pos.MatterSwitchCurrentPosition(), true
		}
		return uint8(0), true
	case matterAttrSwitchMultiPressMax:
		// MSM feature is off — return 0 (no multi-press support
		// advertised). MultiPressMax carries the constraint "min 2"
		// (matter.js packages/model/src/standard/elements/
		// Switch.element.ts), so a bridge without multi-press
		// recognition must drop the MSM feature instead of
		// advertising max=1; the attribute is likewise omitted from
		// MatterAttributes below. Reading it is optional unless MSM
		// is in the FeatureMap; answering the read keeps controllers
		// that probe optional attributes happy.
		return uint8(0), true
	case matterAttrSwitchFeatureMap:
		return s.featureMap(), true
	case matterAttrSwitchClusterRevision:
		return switchClusterRevision, true
	default:
		return nil, false
	}
}

// MatterWrite rejects all attribute writes — every Switch attribute is
// read-only per spec.
func (s *GenericSwitch) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("matter: GenericSwitch attribute 0x%04X is read-only", attrID)
}

// MatterInvoke rejects all commands — Switch cluster has no commands.
func (s *GenericSwitch) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, im.UnsupportedCommandf("matter: GenericSwitch has no command 0x%02X", cmdID)
}

// MatterReportable lists the attributes that emit reports.
// CurrentPosition is the only value-bearing attribute; FeatureMap +
// ClusterRevision are static.
func (s *GenericSwitch) MatterReportable() []uint32 {
	return []uint32{matterAttrSwitchCurrentPosition}
}

// MatterAttributes lists the Switch (0x003B) attributes the server
// implements via MatterRead. Apple Home's HAP service rebuild reads
// the full attribute set; without this the dispatcher falls back to
// MatterReportable's single attribute.
//
// MultiPressMax (0x0002) has conformance MSM — it is only advertised
// when the MSM feature bit is set in FeatureMap. Current bridge
// sources do not expose MSM, so the attribute is omitted here.
func (s *GenericSwitch) MatterAttributes() []uint32 {
	attrs := []uint32{
		matterAttrSwitchNumberOfPositions,
		matterAttrSwitchCurrentPosition,
	}
	if s.featureMap()&switchFeatureMSM != 0 {
		attrs = append(attrs, matterAttrSwitchMultiPressMax)
	}
	return attrs
}

// MatterEvents returns the event IDs this cluster may emit, enabling the
// dispatcher to populate EventList (0xFFFA) for wildcard reads. The set
// is feature-gated: InitialPress (0x01) requires MS; ShortRelease (0x03)
// requires MSR; LongPress (0x02) and LongRelease (0x04) require MSL;
// MultiPressOngoing (0x05) and MultiPressComplete (0x06) require MSM.
func (s *GenericSwitch) MatterEvents() []uint32 {
	fm := s.featureMap()
	ids := []uint32{MatterEventInitialPress} // MS always present
	if fm&switchFeatureMSR != 0 {
		ids = append(ids, MatterEventShortRelease)
	}
	if fm&switchFeatureMSL != 0 {
		ids = append(ids, MatterEventLongPress, MatterEventLongRelease)
	}
	if fm&switchFeatureMSM != 0 {
		ids = append(ids, MatterEventMultiPressOngoing, MatterEventMultiPressComplete)
	}
	return ids
}

func (s *GenericSwitch) featureMap() uint32 {
	fm := switchFeatureMS | switchFeatureMSR
	if s.src.MatterSwitchSupportsLongPress() {
		fm |= switchFeatureMSL
	}
	return fm
}

// FireInitialPress emits the Matter §1.13.6.1 InitialPress event.
// Called by the model layer when the source DP fires PRESS_SHORT or
// the equivalent first-press signal. Priority INFO per matter.js HEAD
// packages/model/src/standard/elements/switch.element.ts:48 — every
// event of the Switch cluster carries "info" there, which is also what
// the sibling ShortRelease / LongRelease emitters below already use.
func (s *GenericSwitch) FireInitialPress(newPosition uint8) {
	if s.emitter == nil {
		return
	}
	s.emitter.MatterEmitEvent(s.endpoint, matterClusterGenericSwitch, MatterEventInitialPress,
		switchInitialPressEvent{NewPosition: newPosition},
		interfaces.MatterEventPriorityInfo)
}

// FireShortRelease emits the §1.13.6.3 ShortRelease event.
func (s *GenericSwitch) FireShortRelease(previousPosition uint8) {
	if s.emitter == nil {
		return
	}
	s.emitter.MatterEmitEvent(s.endpoint, matterClusterGenericSwitch, MatterEventShortRelease,
		switchShortReleaseEvent{PreviousPosition: previousPosition},
		interfaces.MatterEventPriorityInfo)
}

// FireLongPress emits the §1.13.6.2 LongPress event. No-op when the
// source does not advertise long-press support.
func (s *GenericSwitch) FireLongPress(newPosition uint8) {
	if s.emitter == nil || !s.src.MatterSwitchSupportsLongPress() {
		return
	}
	// Priority INFO per matter.js HEAD switch.element.ts:52.
	s.emitter.MatterEmitEvent(s.endpoint, matterClusterGenericSwitch, MatterEventLongPress,
		switchLongPressEvent{NewPosition: newPosition},
		interfaces.MatterEventPriorityInfo)
}

// FireLongRelease emits the §1.13.6.4 LongRelease event.
func (s *GenericSwitch) FireLongRelease(previousPosition uint8) {
	if s.emitter == nil || !s.src.MatterSwitchSupportsLongPress() {
		return
	}
	s.emitter.MatterEmitEvent(s.endpoint, matterClusterGenericSwitch, MatterEventLongRelease,
		switchLongReleaseEvent{PreviousPosition: previousPosition},
		interfaces.MatterEventPriorityInfo)
}

// switch{event}Event are the cluster-native event payload structs.
// The bridge serialises these via the value writer when encoding the
// EventReportIB. Each field carries a context tag matching the
// Matter spec's event field numbers.
type switchInitialPressEvent struct {
	NewPosition uint8 // ContextTag(0)
}

type switchLongPressEvent struct {
	NewPosition uint8 // ContextTag(0)
}

type switchShortReleaseEvent struct {
	PreviousPosition uint8 // ContextTag(0)
}

type switchLongReleaseEvent struct {
	PreviousPosition uint8 // ContextTag(0)
}

// Compile-time assertions: GenericSwitch satisfies the bridge-side
// dispatch interfaces and the attribute-lister and event-lister capabilities.
var (
	_ interfaces.MatterClusterServer          = (*GenericSwitch)(nil)
	_ interfaces.MatterEventReceiver          = (*GenericSwitch)(nil)
	_ interfaces.MatterClusterAttributeLister = (*GenericSwitch)(nil)
	_ interfaces.MatterClusterEventLister     = (*GenericSwitch)(nil)
)
