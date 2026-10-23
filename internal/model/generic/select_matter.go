// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"unicode/utf8"

	msdef "github.com/SukramJ/go-fabric/cluster/spec/modeselect"
	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: a read/write ENUM parameter is projected as a
// ModeSelect (0x0027) device whose single mandatory cluster is declared
// with this data point as its ModeSource, the narrow port the kit's
// ModeSelect server reads.
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
	_ interfaces.MatterDeviceSource   = (*Select)(nil)
	_ interfaces.MatterChangeNotifier = (*Select)(nil)
	_ msdef.ModeSource                = (*Select)(nil)
)

// matterSelectDeviceType is the Matter Device Type ID for ModeSelect.
// Its only mandatory server cluster is ModeSelect (0x0050) — matter.js
// packages/model/src/standard/elements/mode-select-device.element.ts:12-19
// lists Descriptor (supplied by the bridge) and that one requirement, so
// unlike the OnOffPlugInUnit projection this endpoint mounts no stubs.
const matterSelectDeviceType = uint16(fabricdevice.ModeSelectID)

// matterSelectMaxModes is the number of VALUE_LIST entries this projection
// can carry: the SupportedModes constraint "max 255"
// (mode-select-cluster.element.ts:37), read from the generated
// definition. Staying within it is the host's part: go-fabric's ModeSelect
// server returns the list as given and the generated codec writes it
// untrimmed (see [msdef.ModeSource]), so a longer list would go on the
// wire for a controller to reject. Trimming it here instead would leave
// modes that CurrentMode can still name but ChangeToMode would reject as
// unsupported, so a parameter with more entries than that opts out of
// Matter entirely rather than being half-projected.
var matterSelectMaxModes = int(msdef.Definition.Attribute(msdef.AttrSupportedModes).Constraint.Max.Int)

// matterModeLabelMaxBytes is the ModeOptionStruct Label constraint
// "max 64" (mode-select-cluster.element.ts:63), read from the generated
// definition of the struct.
var matterModeLabelMaxBytes = modeOptionFieldMax("Label")

// modeOptionFieldMax is the upper bound of the ModeOptionStruct field
// name, as the generated definition states it.
//
// invariant: the generated definition carries the field and its bound; a
// definition without them is a defect in the generator, not a runtime
// condition, and is caught at package initialisation.
func modeOptionFieldMax(name string) int {
	for i := range msdef.ModeOptionStructDef.Fields {
		if f := &msdef.ModeOptionStructDef.Fields[i]; f.Name == name && f.Constraint.Max != nil {
			return int(f.Constraint.Max.Int)
		}
	}
	panic("generic: ModeOptionStruct." + name + " carries no upper bound in the generated definition")
}

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

// MatterDeviceType implements [interfaces.MatterDeviceSource].
func (s *Select) MatterDeviceType() uint16 { return matterSelectDeviceType }

// MatterDevice implements [interfaces.MatterDeviceSource]: a ModeSelect
// device with the cluster declared on this data point as its ModeSource,
// built once per select. A parameter that cannot back the cluster honestly
// projects nothing, so the assembler skips it and eligibility reports it
// unmappable — see [matterSelectEligible].
//
// The data point itself is the narrow port; the Matter shape of its values
// belongs to the kit's server, which reads the port at every read.
func (s *Select) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	if !matterSelectEligible(s) {
		return nil, nil //nolint:nilnil // a parameter that cannot back ModeSelect projects no device
	}
	return s.matterDevice.Device(func() (*fabricdevice.Device, error) {
		return NewMatterDevice(s, matterSelectDeviceType, opts, msdef.Declare{ModeSource: s})
	})
}

// ModeDescription implements [msdef.ModeSource]. The Description
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

// ModeNamespace implements [msdef.ModeSource] by reporting that there
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

// SupportedModes implements [msdef.ModeSource]: one option per
// VALUE_LIST entry, Label from the list and Mode from the index, which is
// the value this data point's type already is.
//
// SemanticTags is left empty. It is conformance M
// (mode-select-cluster.element.ts:64-69), so the field is always present on
// the wire — an empty list is the encoding for "this mode is anonymous",
// and that is the truthful reading of a CCU enum entry: a label, and no
// classification behind it.
//
// The list bound is [matterSelectMaxModes]; each Label is bounded by
// [boundedModeLabel]. Both are the host's part of the
// [msdef.ModeSource] contract.
func (s *Select) SupportedModes() []msdef.ModeOptionStruct {
	if s == nil || s.DataPoint == nil {
		return nil
	}
	values := s.Descriptor.ValueList
	if len(values) > matterSelectMaxModes {
		return nil
	}
	out := make([]msdef.ModeOptionStruct, 0, len(values))
	for i, label := range values {
		out = append(out, msdef.ModeOptionStruct{
			Label: boundedModeLabel(label),
			Mode:  uint8(i), //nolint:gosec // the list is length-gated to matterSelectMaxModes (255) above
		})
	}
	return out
}

// boundedModeLabel trims label to the ModeOptionStruct Label constraint
// "max 64" (mode-select-cluster.element.ts:63, [matterModeLabelMaxBytes]),
// counted in UTF-8 bytes, cutting back to the last whole rune. The
// generated codec writes the label untrimmed, so this bound is the host's
// part; the cut is the one the bridge's former Label encoder applied
// (go-fabric tlv Encoder.PutUTF8Bounded), so a long CCU label keeps the
// prefix it always had on the wire.
func boundedModeLabel(label string) string {
	if len(label) <= matterModeLabelMaxBytes {
		return label
	}
	trimmed := label[:matterModeLabelMaxBytes]
	for !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

// CurrentMode implements [msdef.ModeSource].
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
	n := min(len(s.Descriptor.ValueList), matterSelectMaxModes)
	return idx >= 0 && int(idx) < n
}

// ChangeToMode implements [msdef.ModeSource]. The server has already
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
// bridge advances the cluster's DataVersion and wakes its dirty bucket on
// it, so a mode changed at the device — not through a controller —
// reaches a Subscribe instead of waiting for the next read.
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
