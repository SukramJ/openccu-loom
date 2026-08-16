// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package visibility

import (
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// configurableChannelTypes is the set of CCU channel-type strings whose
// VALUES paramset entries are gated on the channel's CHANNEL_OPERATION_MODE
// master parameter.
//
// On these channel types the operator can flip between several behaviours
// (e.g. `KEY_BEHAVIOR` vs `SWITCH_BEHAVIOR` on a HmIP-FCI-Sx multi-mode
// input): the wire-level paramset stays the same, but only a subset of VALUES
// parameters is meaningful in each mode. The gate hides everything that the
// current mode does not enable.
var configurableChannelTypes = map[string]struct{}{
	"KEY_TRANSCEIVER":              {},
	"MULTI_MODE_INPUT_TRANSMITTER": {},
}

// channelOperationModeVisibility maps a wire parameter to the set of
// CHANNEL_OPERATION_MODE values that keep the parameter visible.
//
// A parameter that is not a key here is *never* gated — the gate only applies
// to the listed input-event parameters and STATE on multi-mode inputs.
var channelOperationModeVisibility = map[hmenum.Parameter]map[string]struct{}{
	hmenum.ParameterState: {
		"BINARY_BEHAVIOR": {},
	},
	hmenum.ParameterPressLong: {
		"KEY_BEHAVIOR":    {},
		"SWITCH_BEHAVIOR": {},
	},
	hmenum.ParameterPressLongRelease: {
		"KEY_BEHAVIOR":    {},
		"SWITCH_BEHAVIOR": {},
	},
	hmenum.ParameterPressLongStart: {
		"KEY_BEHAVIOR":    {},
		"SWITCH_BEHAVIOR": {},
	},
	hmenum.ParameterPressShort: {
		"KEY_BEHAVIOR":    {},
		"SWITCH_BEHAVIOR": {},
	},
}

// usageForcer is the narrow contract a [device.ParameterDataPoint]
// must satisfy to participate in operation-mode gating: every
// `*generic.DataPoint[T]` implements it through the embedded
// [datapoint.BaseDataPointFields].
type usageForcer interface {
	SetForcedUsage(usage hmenum.DataPointUsage)
}

// operationModeGater is the narrow contract an event source must satisfy
// to participate in operation-mode gating. Any wrapper around
// [event.Source] that implements [device.AttachableEvent] and this
// interface will have its operation-mode allowed state set by the
// pipeline pass.
type operationModeGater interface {
	SetOperationModeAllowed(allowed bool)
}

// ApplyNoEventNoWriteMarks walks every channel of dev and force-usages
// every VALUES paramset entry whose OPERATIONS bitmask has neither the
// EVENT bit nor the WRITE bit set to [hmenum.DataPointUsageIgnored].
//
// This mirrors the first skip-branch of `_should_skip_data_point`
// (model/__init__.py:183-184):
//
//	not parameter_data["OPERATIONS"] & Operations.EVENT
//	and not parameter_data["OPERATIONS"] & Operations.WRITE
//
// A parameter that is purely READ (or has no operations at all) carries
// no user-actionable state: it cannot be written and the CCU never pushes
// events for it. Surfacing it as a standalone DP would only add noise.
//
// Skips DPs that carry an existing forced-usage=DataPoint mark (custom-DP
// pipeline) or an un-ignored mark (operator override), following the same
// precedence rules as the INTERNAL and HIDDEN passes.
//
// Idempotent.
func ApplyNoEventNoWriteMarks(dev *device.Device) {
	if dev == nil {
		return
	}
	for _, ch := range dev.Channels() {
		for _, dp := range ch.DataPoints() {
			markIfNoEventNoWrite(dp)
		}
	}
}

// markIfNoEventNoWrite is the per-DP body of [ApplyNoEventNoWriteMarks].
func markIfNoEventNoWrite(dp device.ParameterDataPoint) {
	pd := dp.ParameterData()
	if pd.Operations.IsEvent() || pd.Operations.IsWritable() {
		// Has at least one of EVENT or WRITE — keep as normal DP.
		return
	}
	// Honour existing un-ignored operator override.
	if r, ok := dp.(unIgnoredReader); ok && r.IsUnIgnored() {
		return
	}
	// Honour custom-DP pipeline explicit DataPoint promotion.
	if r, ok := dp.(forcedUsageReader); ok {
		if u, set := r.ForcedUsage(); set && u == hmenum.DataPointUsageDataPoint {
			return
		}
	}
	if f, ok := dp.(usageForcer); ok {
		f.SetForcedUsage(hmenum.DataPointUsageIgnored)
	}
}

// ApplyChannelOperationModeGating walks ch's VALUES paramset and
// force-marks every entry whose visibility depends on the channel's
// CHANNEL_OPERATION_MODE master parameter. The result.
// branch in `GenericDataPoint.usage`:
//
// - the channel's `Type` is in the configurable-channel set and
// - the parameter is in the per-parameter visibility map and
// - the channel's CHANNEL_OPERATION_MODE master value is read,
//
// then the parameter's data point is marked
// [hmenum.DataPointUsageDataPoint] when the mode is in the
// allowed-set or [hmenum.DataPointUsageIgnored] otherwise. When
// any of the three pre-conditions fails the data point's usage is
// left untouched — the standard visibility decider continues to
// govern it.
//
// Idempotent: the call only writes through [usageForcer.SetForcedUsage],
// so re-running on the same channel yields the same forced state.
//
// Call site: the device pipeline after VALUES seeding (so the
// CHANNEL_OPERATION_MODE master DP carries a value) and after the
// custom-DP materialise pass (so a gate-driven NoCreate cannot
// override an explicit `visible(...)` mark — `visible(...)` set
// CDPVisible, which the gate respects by skipping).
func ApplyChannelOperationModeGating(ch *device.Channel) {
	if ch == nil {
		return
	}
	if _, ok := configurableChannelTypes[ch.Type]; !ok {
		return
	}
	mode := ch.OperationMode()
	if mode == "" {
		// CHANNEL_OPERATION_MODE not yet observed — leave usage untouched. The
		// click-event pass withholds the unknown-mode press *buttons* (see
		// applyClickEventMarks); STATE and the event sources keep their base
		// usage so a not-yet-read mode does not blank out real state/events.
		return
	}
	for _, dp := range ch.DataPoints() {
		modes, gated := channelOperationModeVisibility[dp.Parameter()]
		if !gated {
			continue
		}
		f, ok := dp.(usageForcer)
		if !ok {
			continue
		}
		if _, allowed := modes[mode]; allowed {
			f.SetForcedUsage(hmenum.DataPointUsageDataPoint)
		} else {
			f.SetForcedUsage(hmenum.DataPointUsageIgnored)
		}
	}
	// Apply the same gating logic to attached event sources. An event source
	// whose parameter is in [channelOperationModeVisibility] but excluded by
	// the current CHANNEL_OPERATION_MODE must be marked Ignored so north-bound
	// adapters don't surface it (and the un-ignore feature can offer it).
	for _, ev := range ch.GenericEvents() {
		g, ok := ev.(operationModeGater)
		if !ok {
			continue
		}
		param := hmenum.Parameter(ev.DataPointKey().Parameter)
		modes, gated := channelOperationModeVisibility[param]
		if !gated {
			continue
		}
		_, allowed := modes[mode]
		g.SetOperationModeAllowed(allowed)
	}
}

// ApplyChannelOperationModeGatingDevice runs
// [ApplyChannelOperationModeGating] for every channel of dev. A
// best-effort helper for the pipeline so it can issue a single call
// per device after seedValues. Nil-safe.
func ApplyChannelOperationModeGatingDevice(dev *device.Device) {
	if dev == nil {
		return
	}
	for _, ch := range dev.Channels() {
		ApplyChannelOperationModeGating(ch)
	}
}

// forceSensorMarker is the narrow contract a [device.ParameterDataPoint]
// must satisfy for [ApplyForceSensorMarks] to flip it into read-only
// sensor mode. Every `*generic.DataPoint[T]` implements it through the
// embedded [datapoint.BaseDataPointFields].
type forceSensorMarker interface {
	MarkForcedSensor()
}

// unIgnoredMarker is the narrow contract a [device.ParameterDataPoint]
// must satisfy to participate in the un-ignored mark-back from the
// visibility decider.
//
// Both directions belong to the contract. The un-ignore configuration is
// editable while the daemon runs, so the pass has to be able to take the
// mark away again; a marker that can only set the flag turns every
// promotion into a permanent one.
type unIgnoredMarker interface {
	MarkUnIgnored()
	ClearUnIgnored()
}

// ApplyUnIgnoredMarks walks every channel of dev and brings every VALUES
// paramset entry in line with the [ParameterDecider]'s current un-ignore
// verdict: matches are marked, non-matches are cleared.
//
// Computing the full set — rather than only adding marks — is what makes
// the pass usable after a configuration change. The un-ignore rules are
// edited at runtime (REST PUT, SPA), and the operator's removal of a
// pattern is only observable if the re-run can put the data point back
// under the static decider's verdict.
//
// The mark survives the custom-DP suppression pass
// ([SuppressUndefinedGenericDataPointsWith] explicitly skips un-ignored DPs)
// and is consulted by `force_to_sensor` checks so the override prevails even
// on parameters that would otherwise be rewritten to read-only sensors.
//
// `decider` is the per-central decider populated from the CCU's un-ignore
// configuration. Pass nil to skip the pass.
func ApplyUnIgnoredMarks(dev *device.Device, decider *ParameterDecider) {
	if dev == nil || decider == nil {
		return
	}
	for _, ch := range dev.Channels() {
		for _, dp := range ch.DataPoints() {
			m, ok := dp.(unIgnoredMarker)
			if !ok {
				continue
			}
			// Only user-provided `un_ignore` entries set the DP-level
			// flag. Built-in un-ignore rules are honoured by the
			// suppression / hidden / internal passes via the decider
			// directly so the DP-level mark stays snapshot-symmetric.
			if decider.IsUnIgnored(dev.Model, ch.Type, hmenum.ParamsetKeyValues, dp.Parameter()) {
				m.MarkUnIgnored()
				continue
			}
			m.ClearUnIgnored()
			reapplyValuesSuppression(dev, ch, dp, decider)
		}
	}
}

// reapplyValuesSuppression restores the static suppression verdict for a
// VALUES data point whose operator un-ignore mark was just withdrawn.
//
// Dropping the mark alone is not enough: the three suppression passes skip
// un-ignored data points, so a parameter promoted at boot never received
// its `no_create` mark. Without re-running them here the parameter would
// keep surfacing on every north-bound plane even though the rule that
// promoted it is gone.
func reapplyValuesSuppression(dev *device.Device, ch *device.Channel, dp device.ParameterDataPoint, decider *ParameterDecider) {
	markIfIgnored(dev, ch, dp, hmenum.ParamsetKeyValues, decider)
	markIfInternal(dp, dev.Model, ch.Type, hmenum.ParamsetKeyValues, nil)
	markIfHidden(dp, dev.Model, ch.Type, ch.Number, hmenum.ParamsetKeyValues, nil)
}

// ApplyIgnoredParameterMarks walks every channel of dev and force-usages
// every DP whose (model, channelType, channelNo, paramset, parameter) tuple
// the [ParameterDecider] reports as ignored. Mirrors the "DP exists but is
// invisible" model: openccu-loom always creates the DP for every wire
// parameter (so diagnostics, custom-DP composition, and operator overrides
// via un_ignore.txt see them); the no_create mark only governs the UI / MQTT
// surface.
//
// Skips DPs that already carry an un-ignored mark — the operator override
// re-promotes them.
//
// Idempotent.
func ApplyIgnoredParameterMarks(dev *device.Device, decider *ParameterDecider) {
	if dev == nil || decider == nil {
		return
	}
	for _, ch := range dev.Channels() {
		for _, dp := range ch.DataPoints() {
			markIfIgnored(dev, ch, dp, hmenum.ParamsetKeyValues, decider)
		}
		for _, dp := range ch.MasterDataPoints() {
			markIfIgnored(dev, ch, dp, hmenum.ParamsetKeyMaster, decider)
		}
	}
}

// markIfIgnored is the per-DP body of [ApplyIgnoredParameterMarks].
func markIfIgnored(dev *device.Device, ch *device.Channel, dp device.ParameterDataPoint, paramset hmenum.ParamsetKey, decider *ParameterDecider) {
	if !decider.IsParameterIgnored(dev.Model, ch.Type, ch.Number, paramset, dp.Parameter()) {
		return
	}
	if r, ok := dp.(unIgnoredReader); ok && r.IsUnIgnored() {
		return
	}
	// A custom-DP promotion (`ForcedUsage=DataPoint` from
	// markAdditionalDataPoints / applyFieldVisibility) does NOT shield a
	// device-ignored parameter: in the reference stack an ignored parameter
	// never gets a generic DP, so the custom-DP `_mark_data_point` promotion
	// finds nothing to promote. The cases that must survive this pass
	// (HM-Sec-Key/Win DIRECTION/ERROR/WORKING, HmIP-PCBS(-BAT)
	// LOW_BAT/OPERATING_VOLTAGE) are covered by the decider's leading
	// un-ignore guard (`unIgnoreParametersByDevice`), which already returned
	// false from IsParameterIgnored above.
	if f, ok := dp.(usageForcer); ok {
		f.SetForcedUsage(hmenum.DataPointUsageIgnored)
	}
}

// ApplyInternalParameterMarks walks every channel of dev and force-
// usages every parameter whose `FLAGS` field includes `Flag.INTERNAL`
// to [hmenum.DataPointUsageIgnored], unless the parameter is in
// [generic.AllowedInternalParameters] or has been explicitly
// Un-ignored. Mirrors the second branch.
// `_should_skip_data_point` (`model/__init__.py:180-189`):
//
//	(parameter_data["FLAGS"] & Flag.INTERNAL
//	 and parameter not in _ALLOWED_INTERNAL_PARAMETERS.values()
//	 and not parameter_is_un_ignored)
//
// openccu-loom always materialises every wire parameter as a DP, so
// the filter takes the form of a `forced_usage = no_create` mark on
// the affected DPs (matching the rest of the visibility-pipeline
// idiom). Idempotent. Skips DPs that have already been promoted to
// [hmenum.DataPointUsageDataPoint] by a custom-DP wiring step.
//
// Runs late in the pipeline so un-ignored marks already in place can
// suppress the filter for operator overrides. The same applies to MASTER
// Paramset entries
// for both VALUES and MASTER, so we mirror that here.
func ApplyInternalParameterMarks(dev *device.Device) {
	ApplyInternalParameterMarksWithDecider(dev, nil)
}

// ApplyInternalParameterMarksWithDecider is the decider-aware variant
// kept as a hook for future strategies. The current implementation
// Keeps parity with
// consulting the decider — the built-in `unIgnoreParametersByDevice`
// exemption is applied at the DP-creation layer
// (`resolveDataPointWithUnIgnore`) so the suppression mark stays
// snapshot-symmetric (Python's `is_un_ignored` is custom_only=True).
func ApplyInternalParameterMarksWithDecider(dev *device.Device, _ *ParameterDecider) {
	if dev == nil {
		return
	}
	for _, ch := range dev.Channels() {
		for _, dp := range ch.DataPoints() {
			markIfInternal(dp, dev.Model, ch.Type, hmenum.ParamsetKeyValues, nil)
		}
		for _, dp := range ch.MasterDataPoints() {
			markIfInternal(dp, dev.Model, ch.Type, hmenum.ParamsetKeyMaster, nil)
		}
	}
}

// markIfInternal is the per-DP body of [ApplyInternalParameterMarksWithDecider].
// Extracted so VALUES + MASTER iterations share one implementation.
func markIfInternal(dp device.ParameterDataPoint, model, channelType string, paramset hmenum.ParamsetKey, decider *ParameterDecider) {
	pd := dp.ParameterData()
	if !pd.Flags.IsInternal() {
		return
	}
	if _, allowed := generic.AllowedInternalParameters[string(dp.Parameter())]; allowed {
		return
	}
	// Built-in `unIgnoreParametersByDevice` exemption ( Lock-ERROR
	// fix): when the decider reports the parameter as un-ignored
	// (custom_only=false → consults user rules + built-in entries),
	// the FLAG.INTERNAL filter is bypassed so HM-Sec-Key/HM-Sec-Win
	// ERROR and HmIP-DLD/HmIP-DLP ERROR_JAMMED survive as DPs.
	if decider != nil && decider.IsUnIgnoredCustomOnly(model, channelType, paramset, dp.Parameter(), false) {
		return
	}
	if r, ok := dp.(unIgnoredReader); ok && r.IsUnIgnored() {
		return
	}
	// Built-in unIgnoreParametersByDevice exemption (reverse-prefix): a base
	// model whose key the device matches keeps its INTERNAL DP even when it was
	// additional_data_points-promoted (HM-Sec-Win base WORKING). A longer
	// variant that does not match (HM-Sec-Win-Generic) does NOT — mirroring the
	// reference, where an INTERNAL parameter that is neither allowed-internal nor
	// un-ignored is skipped at DP creation, so its additional_data_points
	// promotion no-ops. Consulting the built-in list here (rather than blindly
	// preserving any DataPoint promotion) is what distinguishes the two.
	if deviceUnIgnoresByPrefix(model, dp.Parameter()) {
		return
	}
	if f, ok := dp.(usageForcer); ok {
		f.SetForcedUsage(hmenum.DataPointUsageIgnored)
	}
}

// ApplyHiddenParameterMarks walks every channel of dev and force-
// usages every VALUES paramset entry whose parameter appears in
// [hiddenParameters] to [hmenum.DataPointUsageIgnored].
// `parameter_is_hidden` (HIDDEN_PARAMETERS in store/visibility/rules.py:108):
// these parameters are technically writable on the wire but are not
// surfaced as standalone DPs because operators consume them through
// other channels (the maintenance-channel aggregator, custom-DP
// state, MQTT diagnostic topics).
//
// Skips DPs that already carry an un-ignored mark — the operator
// override re-promotes them to [hmenum.DataPointUsageDataPoint], same
// As.
//
// Idempotent.
func ApplyHiddenParameterMarks(dev *device.Device) {
	ApplyHiddenParameterMarksWithDecider(dev, nil)
}

// ApplyHiddenParameterMarksWithDecider is the decider-aware variant
// kept as a hook for future bypass-strategies; the current
// Implementation does not consult the decider because
// snapshot does not flag built-in un-ignored params with
// `is_un_ignored=True`. The pass therefore only respects the per-DP
// `IsUnIgnored()` mark (custom_only=True semantics).
func ApplyHiddenParameterMarksWithDecider(dev *device.Device, _ *ParameterDecider) {
	if dev == nil {
		return
	}
	model := dev.Model
	for _, ch := range dev.Channels() {
		chNo := ch.Number
		for _, dp := range ch.DataPoints() {
			markIfHidden(dp, model, ch.Type, chNo, hmenum.ParamsetKeyValues, nil)
		}
		for _, dp := range ch.MasterDataPoints() {
			markIfHidden(dp, model, ch.Type, chNo, hmenum.ParamsetKeyMaster, nil)
		}
	}
}

// forcedUsageReader is the read-side companion to [usageForcer].
// Every `*generic.DataPoint[T]` satisfies it through the embedded
// [datapoint.BaseDataPointFields].
type forcedUsageReader interface {
	ForcedUsage() (hmenum.DataPointUsage, bool)
}

// markIfHidden is the per-DP body of [ApplyHiddenParameterMarks]; extracted
// so the same logic runs on the VALUES and MASTER iterations without
// copy-paste.
//
// Note: the MASTER-whitelist (RELEVANT_MASTER_PARAMSETS_BY_DEVICE
// RELEVANT_MASTER_PARAMSETS_BY_CHANNEL) is intentionally NOT consulted here.
// `Channel.OperationMode()` and similar accessors read the DP value
// regardless of the no_create mark.
func markIfHidden(dp device.ParameterDataPoint, model, channelType string, _ int, paramset hmenum.ParamsetKey, decider *ParameterDecider) {
	if _, hidden := hiddenParameters[dp.Parameter()]; !hidden {
		return
	}
	// Built-in `unIgnoreParametersByDevice` exemption ( Lock-DIRECTION fix):
	// when the decider reports the parameter as un-ignored (custom_only=false →
	// consults built-in entries), the HIDDEN_PARAMETERS suppression is bypassed
	// so HM-Sec-Key/HM-Sec-Win DIRECTION + ERROR survive as data_point.
	if decider != nil && decider.IsUnIgnoredCustomOnly(model, channelType, paramset, dp.Parameter(), false) {
		return
	}
	if r, ok := dp.(unIgnoredReader); ok && r.IsUnIgnored() {
		return
	}
	// skip hidden-mark when the custom-DP pipeline has already explicitly
	// promoted this DP to DATA_POINT via markAdditionalDataPoints (e.g.
	// DIRECTION / ERROR on HM-Sec-Key/HM-Sec-Win).
	if r, ok := dp.(forcedUsageReader); ok {
		if u, set := r.ForcedUsage(); set && u == hmenum.DataPointUsageDataPoint {
			return
		}
	}
	if f, ok := dp.(usageForcer); ok {
		f.SetForcedUsage(hmenum.DataPointUsageIgnored)
	}
}

// unIgnoredReader is the read-side counterpart to
// [unIgnoredMarker]. Every `*generic.DataPoint[T]` satisfies it
// through the embedded BaseDataPointFields.
type unIgnoredReader interface {
	IsUnIgnored() bool
}

// ApplyForceSensorMarks walks every channel of dev and flags every VALUES
// paramset entry whose (model, parameter) tuple appears in the
// `_SWITCH_DP_TO_SENSOR` map.
//
// The mark causes [generic.DataPoint.IsWritable] to return false regardless
// of the descriptor's operations bitmask — REST / WS adapters reject writes
// at the adapter layer instead of forwarding them to the CCU just to receive
// a -5 / -1 fault. The MQTT layer already classifies these parameters as
// Sensor in PR-8; this pass extends the override into the operator API.
func ApplyForceSensorMarks(dev *device.Device) {
	if dev == nil || dev.Model == "" {
		return
	}
	for _, ch := range dev.Channels() {
		for _, dp := range ch.DataPoints() {
			if !generic.IsForceSensorParameter(dev.Model, dp.Parameter()) {
				continue
			}
			if m, ok := dp.(forceSensorMarker); ok {
				m.MarkForcedSensor()
			}
		}
	}
}
