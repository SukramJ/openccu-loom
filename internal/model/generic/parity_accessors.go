// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── Translation ─────────────────────────────────────────────────────

// Translation returns the locale-aware human-readable label for this
// parameter. Set by the device-ingest pipeline from the CCU translations
// catalogue; empty when no translation was found.
//
// self._translation = ccu_translations.get_parameter_translation(...)
func (d *DataPoint[T]) Translation() string {
	return d.Spec.Translation
}

// ─── Description ─────────────────────────────────────────────────────

// Description returns the help/tooltip text for this parameter from the CCU
// translations catalogue. Empty when the catalogue has no entry.
//
// description: Final = DelegatedProperty[str | None](path="_description",
// kind=Kind.INFO)
func (d *DataPoint[T]) Description() string {
	return d.Spec.Description
}

// ─── RawUnit ─────────────────────────────────────────────────────────

// RawUnit returns the unit string as reported by the CCU before any
// normalisation is applied. This may differ from [Unit] when the CCU uses a
// non-canonical spelling ("Lux" vs "lx", "100%" vs "%", etc.).
//
// raw_unit: Final = DelegatedProperty[str | None](path="_raw_unit")
func (d *DataPoint[T]) RawUnit() string {
	return d.Descriptor.Unit
}

// ─── ValueTranslations ───────────────────────────────────────────────

// ValueTranslations returns the per-value locale strings for ENUM parameters.
// Keys are the raw VALUE_LIST entries; values are the human-readable labels.
// Returns nil when the parameter has no VALUE_LIST or no translations were
// found.
//
// value_translations: Final = DelegatedProperty[dict[str, str | None] |
// None](...)
func (d *DataPoint[T]) ValueTranslations() map[string]string {
	return d.Spec.ValueTranslations
}

// ─── IsHmtype ────────────────────────────────────────────────────────

// IsHmtype reports whether this is a native HomeMatic-protocol data point one
// that originated from a real CCU XML-RPC/BIN-RPC paramset rather than being
// synthesised by the daemon (calculated, combined, custom). Set to true by
// the device-ingest pipeline for all real protocol DPs; false for hub /
// custom / calculated.
func (d *DataPoint[T]) IsHmtype() bool {
	return d.Spec.IsHmtype
}

// ─── Service ─────────────────────────────────────────────────────────

// Service reports whether the FLAGS bitmask of this parameter has the SERVICE
// bit set. Service parameters are only shown in the CCU service menus and
// should typically be hidden from regular UI surfaces.
//
// service: Final = DelegatedProperty[bool](path="_service") self._service =
// flags & Flag.SERVICE == Flag.SERVICE
func (d *DataPoint[T]) Service() bool {
	return d.Descriptor.IsService()
}

// ─── StatusDPK ───────────────────────────────────────────────────────

// StatusDPK returns the [hmtypes.DataPointKey] for the paired STATUS
// parameter when one has been detected (i.e. [StatusParameter] is non-empty),
// or the zero value and false when this data point has no STATUS partner.
//
// self._status_dpk = DataPointKey( interface_id=..., channel_address=...,
// paramset_key=..., parameter=self._status_parameter, )
func (d *DataPoint[T]) StatusDPK() (hmtypes.DataPointKey, bool) {
	d.mu.RLock()
	sp := d.statusParameter
	d.mu.RUnlock()
	if sp == "" {
		return hmtypes.DataPointKey{}, false
	}
	return hmtypes.DataPointKey{
		InterfaceID:    d.Key.InterfaceID,
		ChannelAddress: d.Key.ChannelAddress,
		ParamsetKey:    d.Key.ParamsetKey,
		Parameter:      sp,
	}, true
}

// ─── Signature ───────────────────────────────────────────────────────

// Signature returns a stable string for discovery deduplication. The format
// is `<category>/<deviceModel>/<parameter>` — the same three fields Python
// uses to deduplicate HA entity registrations across firmware updates.
//
// def _get_signature(self) -> str: return
// f"{self._category}/{self._channel.device.model}/{self._parameter}"
func (d *DataPoint[T]) Signature() string {
	cat := string(d.Category())
	model := d.DeviceModel
	param := d.Key.Parameter
	var sb strings.Builder
	sb.Grow(len(cat) + 1 + len(model) + 1 + len(param))
	sb.WriteString(cat)
	sb.WriteByte('/')
	sb.WriteString(model)
	sb.WriteByte('/')
	sb.WriteString(param)
	return sb.String()
}

// ─── EnabledByChannelOperationMode ───────────────────────────────────

// EnabledByChannelOperationMode returns the tri-state gate for
// CHANNEL_OPERATION_MODE visibility: - (true, true) — current operation mode
// explicitly includes this parameter. - (false, true) — current operation
// mode explicitly excludes this parameter. - (_, false) — no
// CHANNEL_OPERATION_MODE constraint applies (channel type is not
// configurable, parameter is not in the visibility map, or operation mode has
// not been read yet).
//
// def _enabled_by_channel_operation_mode(self) -> bool | None: if
// self._channel.type_name not in _CONFIGURABLE_CHANNEL: return None ...
// return cop in KEY_CHANNEL_OPERATION_MODE_VISIBILITY[self._parameter]
//
// The device pipeline writes this gate via [SetOperationModeAllowed].
func (d *DataPoint[T]) EnabledByChannelOperationMode() (enabled, ok bool) {
	d.mu.RLock()
	gate := d.enabledByChannelOperationMode
	d.mu.RUnlock()
	if gate == nil {
		return false, false
	}
	return *gate, true
}

// SetOperationModeAllowed records the tri-state gate value set by the device
// pipeline's `applyChannelOperationModeGating` pass. Passing true marks the
// DP as included by the current CHANNEL_OPERATION_MODE; false marks it as
// excluded. Call with a non-nil pointer to set, or never call to leave the
// gate at nil ("no constraint"). Mirrors the equivalent setter on
// [event.Source.SetOperationModeAllowed].
func (d *DataPoint[T]) SetOperationModeAllowed(allowed bool) {
	d.mu.Lock()
	d.enabledByChannelOperationMode = &allowed
	d.mu.Unlock()
}
