// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"

	"github.com/SukramJ/go-fabric/cluster/modeselect"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: a read/write ENUM parameter is projected as a
// ModeSelect (0x0027) endpoint whose single mandatory cluster is served by
// the library's ModeSelect server over this data point as its narrow port.
//
// Only [Select] takes part. The two neighbouring ENUM shapes deliberately
// do not:
//
//   - [ActionSelect] is write-only (the resolver picks it exactly when the
//     parameter is neither readable nor an event source). ModeSelect's
//     CurrentMode is conformance M and carries no X quality
//     (matter.js mode-select-cluster.element.ts:41-42), so a controller is
//     entitled to a mode that means something. A write-only parameter can
//     only ever report what this daemon last sent, which is a claim about
//     the device that nothing confirmed.
//   - A read-only ENUM surfaces as [Sensor] of int32, and ChangeToMode is
//     conformance M on the cluster. Mounting the cluster over a parameter
//     that cannot be written would advertise a command that always fails.
var (
	_ interfaces.MatterEndpointSource = (*Select)(nil)
	_ interfaces.MatterChangeNotifier = (*Select)(nil)
	_ modeselect.ModeSource           = (*Select)(nil)
)

// matterSelectDeviceType is the Matter Device Type ID for ModeSelect.
// Its only mandatory server cluster is ModeSelect (0x0050) — matter.js
// packages/model/src/standard/elements/mode-select-device.element.ts:12-19
// lists Descriptor (supplied by the bridge) and that one requirement, so
// unlike the OnOffPlugInUnit projection this endpoint mounts no stubs.
const matterSelectDeviceType uint16 = 0x0027

// matterSelectMaxModes is the number of VALUE_LIST entries this projection
// can carry. SupportedModes has constraint "max 255"
// (mode-select-cluster.element.ts:37) and the encoder trims a longer list
// at encode time, which would leave modes that CurrentMode can still name
// but ChangeToMode would reject as unsupported. A parameter with more
// entries than that opts out of Matter entirely rather than being
// half-projected.
const matterSelectMaxModes = modeselect.SupportedModesMaxEntries

// matterSelectEligible reports whether this data point can honestly carry
// a ModeSelect cluster.
//
// The cluster needs a mode list to select from, a readable current mode,
// and a way to change it. The resolver only ever produces a [Select] for a
// writable, readable-or-event ENUM, so the checks are not expected to fire
// in the assembled model — they are stated here because [Select] is also
// constructed directly (tests, synthetic data points), and an empty
// VALUE_LIST would otherwise reach a controller as a mode chooser with no
// modes.
func matterSelectEligible(s *Select) bool {
	if s == nil || s.DataPoint == nil {
		return false
	}
	n := len(s.Descriptor.ValueList)
	if n == 0 || n > matterSelectMaxModes {
		return false
	}
	return s.IsWritable() && (s.IsReadable() || s.HasEvents())
}

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (s *Select) MatterDeviceType() uint16 { return matterSelectDeviceType }

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// Returns nil for a parameter that cannot back the cluster honestly, so the
// assembler skips the projection and eligibility reports it unmappable —
// see [matterSelectEligible].
//
// The data point itself is the narrow port; the Matter shape of its values
// belongs to the library server. Its persistent DataVersion tracker is
// threaded in so a mode the device changed on its own keeps advancing the
// same counter across the server reconstruction every topology assembly and
// every eligibility query performs.
func (s *Select) MatterClusterServers() []interfaces.MatterClusterServer {
	if !matterSelectEligible(s) {
		return nil
	}
	return []interfaces.MatterClusterServer{
		modeselect.NewServer(modeselect.Config{
			Source:      s,
			DataVersion: &s.matterModeVersion,
		}),
	}
}

// ModeDescription implements [modeselect.ModeSource]. The Description
// attribute is meant to say what this cluster instance selects between,
// which matters on a node carrying several of them. The CCU parameter name
// is the only such text the data point owns — the human-readable device and
// channel naming lives in the endpoint's NodeLabel, composed by the
// north-bound name resolver — so the parameter name is what goes on the
// wire. Inventing a prettier sentence here would put a string in front of
// the user that no CCU source backs.
func (s *Select) ModeDescription() string {
	if s == nil || s.DataPoint == nil {
		return ""
	}
	return string(s.Parameter())
}

// ModeNamespace implements [modeselect.ModeSource] by reporting that there
// is none.
//
// StandardNamespace names a CSA-defined tag namespace whose semantic tags a
// controller may act on without reading a label. A CCU VALUE_LIST is a list
// of device-specific strings and carries no such classification: nothing in
// the paramset description says whether `LIGHT_ONLY` is the CSA "Auto" mode
// or anything else. The attribute is quality X, nullable, default null
// (matter.js mode-select-cluster.element.ts:29-32) for exactly this case, so
// absent is a value the spec provides — picking a namespace would assert a
// meaning the CCU never supplied.
func (s *Select) ModeNamespace() (namespace uint8, present bool) { return 0, false }

// SupportedModes implements [modeselect.ModeSource]: one option per
// VALUE_LIST entry, Label from the list and Mode from the index, which is
// the value this data point's type already is.
//
// SemanticTags is left empty. It is conformance M
// (mode-select-cluster.element.ts:64-69), so the field is always present on
// the wire — an empty list is the encoding for "this mode is anonymous",
// and that is the truthful reading of a CCU enum entry: a label, and no
// classification behind it.
func (s *Select) SupportedModes() []modeselect.ModeOptionStruct {
	if s == nil || s.DataPoint == nil {
		return nil
	}
	values := s.Descriptor.ValueList
	if len(values) > matterSelectMaxModes {
		return nil
	}
	out := make([]modeselect.ModeOptionStruct, 0, len(values))
	for i, label := range values {
		out = append(out, modeselect.ModeOptionStruct{
			Label: label,
			Mode:  uint8(i), //nolint:gosec // the list is length-gated to matterSelectMaxModes (255) above
		})
	}
	return out
}

// CurrentMode implements [modeselect.ModeSource].
//
// The attribute has no "not observed yet" reading — conformance M, no X
// quality (mode-select-cluster.element.ts:41-42) — so a value has to be
// named before the first CCU event arrives. The descriptor's own DEFAULT is
// used when it declares one that indexes the VALUE_LIST, because that is
// the CCU's statement about the parameter rather than this daemon's guess;
// otherwise index 0, the first declared entry, as the only other
// representable answer. Either way the first CCU push replaces it.
func (s *Select) CurrentMode() uint8 {
	if s == nil || s.DataPoint == nil {
		return 0
	}
	if idx, observed := s.Value(); observed && s.modeInRange(idx) {
		return uint8(idx) //nolint:gosec // modeInRange bounds the index by the value list
	}
	if def := s.Default(); def != nil && s.modeInRange(*def) {
		return uint8(*def) //nolint:gosec // modeInRange bounds the index by the value list
	}
	return 0
}

// modeInRange reports whether idx addresses a VALUE_LIST entry that is also
// representable as a Matter mode (uint8, and within the projected list).
func (s *Select) modeInRange(idx int32) bool {
	n := len(s.Descriptor.ValueList)
	if n > matterSelectMaxModes {
		n = matterSelectMaxModes
	}
	return idx >= 0 && int(idx) < n
}

// ChangeToMode implements [modeselect.ModeSource]. The server has already
// checked newMode against [Select.SupportedModes], so the index is known to
// address a VALUE_LIST entry; [Select.SetIndex] re-checks it against the live
// descriptor and sends the index itself, whichever domain the descriptor
// declares — see its doc comment for why an index-typed caller does that, and
// [DataPoint.EnumWireValue] for the label form [Select.SetLabel] would use
// instead. A Matter mode IS an index (mode-select-cluster.element.ts:63-68
// gives ModeOptionStruct a Mode field and a separate Label), so the index is
// the value this path actually has. Dispatched at [matterDispatchPriority] — the cluster contract
// carries no priority, so the urgency of a bridged mode change is decided
// here, next to the device vocabulary.
func (s *Select) ChangeToMode(ctx context.Context, newMode uint8) error {
	return s.SetIndex(ctx, int32(newMode), matterDispatchPriority)
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier]. The
// ModeSelect server forwards this to bump its DataVersion and wake the
// bridge's dirty bucket, so a mode changed at the device — not through a
// controller — reaches a Subscribe instead of waiting for the next read.
// Wraps OnConfirmedUpdate for the same reason [Switch.OnMatterValueChanged]
// does: an optimistic write and its rollback are this daemon's guesses, and
// reporting them would make a controller show a mode the CCU never
// confirmed.
func (s *Select) OnMatterValueChanged(cb func()) func() {
	if s == nil || s.DataPoint == nil || cb == nil {
		return func() {}
	}
	return s.OnConfirmedUpdate(func(_, _ int32) { cb() })
}
