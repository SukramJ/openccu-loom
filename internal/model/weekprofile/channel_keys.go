// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TypedChannel is one channel of a device as the CCU's weekly-program
// editor sees it: its number and its CHANNEL_TYPE.
type TypedChannel struct {
	No   int
	Type string
}

// scheduleRelevantChannelTypeTokens are the CHANNEL_TYPE substrings the
// CCU's weekly-program editor accepts into a device's schedule-relevant
// channel list. Both firmware editors carry the same set:
// ../OpenCCU-Base/src/webui/www_source/ise/js/iseHmIPWeeklyProgram.js:517-555
// (getRelevantChannels) and
// ../OpenCCU-Base/www/config/easymodes/js/HmIPWeeklyProgram.js:200-236
// (getWPVirtualChannels); the first also lists UNIVERSAL_LIGHT_RECEIVER,
// which the second reaches through a per-device override.
var scheduleRelevantChannelTypeTokens = []string{
	"_VIRTUAL_RECEIVER",
	"ACCESS_RECEIVER",
	"ACCESS_TRANSCEIVER",
	"DOOR_LOCK_STATE_TRANSMITTER",
	"OPTICAL_SIGNAL_RECEIVER",
	"UNIVERSAL_LIGHT_RECEIVER",
	"PERMISSION_TRANSCEIVER",
	"SWITCH_TRANSCEIVER",
	"AUTO_RELOCK_TRANSCEIVER",
	"DOOR_LOCK_TRANSCEIVER",
}

// TargetBitOrder returns, for every schedule-relevant channel of a device,
// the bit the CCU addresses it with in `<n>_WP_TARGET_CHANNELS` and in
// WEEK_PROGRAM_CHANNEL_LOCKS (the two share one assignment, see
// iseHmIPWeeklyProgram.js:357 rendering the lock table over the same list).
// The map is keyed by channel number.
//
// The rule is the firmware's own, read from its weekly-program editor
// rather than from any device: the bit is the channel's POSITION in the
// device's schedule-relevant channel list, taken in channel order —
//
//	valCheckBox = Math.pow(2, index)
//	    HmIPWeeklyProgram.js:2899 (expert view) and :2926, over
//	    getWPVirtualChannels (:200-236); iseHmIPWeeklyProgram.js:357 over
//	    getRelevantChannels (:517-555); the read-back at
//	    HmIPWeeklyProgram.js:360-366 tests isBitSet(val, index) over the same
//	    list.
//
// The list is every channel whose CHANNEL_TYPE carries one of
// [scheduleRelevantChannelTypeTokens], ascending by channel number. The
// per-family lists the device-page editor spells out by hand (HmIP-BSL
// `[4,5,6,8,9,10,12,13,14]`, HmIP-WKP `[1,3,5,...,15]`, HmIP-SMO230
// `[10,11,12]`, a window drive `[2]`, HmIP-WGS `[7,9,10,11]`, :394-418) are
// that derivation written out, which is why the WebUI editor, which
// derives the list, and the device-page editor, which lists it, address
// the same bits. A set bit in WEEK_PROGRAM_CHANNEL_LOCKS means LOCKED
// (schedule disabled) — [ParseChannelLocks] carries the inversion.
//
// Two families are not positional, and the editor says so:
//
//   - HmIP-DRG-DALI: the bit is the channel number minus one
//     (HmIPWeeklyProgram.js:2918, iseHmIPWeeklyProgram.js:351) — its list is
//     sparse (the DALI channels 33..48 follow the physical ones, :459-472),
//     so a position would not survive a device with fewer lamps. The
//     device-page editor folds HmIP-LSC into the same branch (:292-295).
//   - HmIP-FWI: positions 0..7 (the eight access-control channels) take bits
//     3..10 and positions 8..10 (the three switch receivers) take bits 0..2
//     — `valHmIP_FWI = [8,16,...,1024,1,2,4]` at HmIPWeeklyProgram.js:2893
//     and the index remap at iseHmIPWeeklyProgram.js:327-345.
//
// "Channel number minus one" was applied to every device here once, and
// was wrong for every ordinary one: an HmIP-BSL slot aimed at channel 4
// set bit 3, which is channel 8 — a signal-LED receiver — and the CCU's
// own editor showed the mistake as a different checkbox. The rule the
// firmware applies is not derivable from a channel number; it needs the
// list, so callers hand the device's channels in and take the map out.
func TargetBitOrder(model string, channels []TypedChannel) map[int]uint {
	relevant := make([]TypedChannel, 0, len(channels))
	for _, ch := range channels {
		if ch.No < 1 || !isScheduleRelevantChannelType(ch.Type) {
			continue
		}
		relevant = append(relevant, ch)
	}
	if len(relevant) == 0 {
		return nil
	}
	sort.Slice(relevant, func(i, j int) bool { return relevant[i].No < relevant[j].No })

	out := make(map[int]uint, len(relevant))
	switch {
	case isDALIUniversalLightModel(model):
		for _, ch := range relevant {
			out[ch.No] = uint(ch.No - 1) //nolint:gosec // ch.No >= 1 by the filter above
		}
	case model == "HmIP-FWI":
		for index, ch := range relevant {
			if index <= 7 {
				out[ch.No] = uint(index + 3) //nolint:gosec // 3..10
			} else {
				out[ch.No] = uint(index - 8) //nolint:gosec // index >= 8 here
			}
		}
	default:
		for index, ch := range relevant {
			out[ch.No] = uint(index) //nolint:gosec // slice index
		}
	}
	return out
}

func isScheduleRelevantChannelType(channelType string) bool {
	for _, token := range scheduleRelevantChannelTypeTokens {
		if strings.Contains(channelType, token) {
			return true
		}
	}
	return false
}

func isDALIUniversalLightModel(model string) bool {
	return model == "HmIP-DRG-DALI" || model == "HmIP-LSC"
}

// ParseChannelLocks decodes the raw WEEK_PROGRAM_CHANNEL_LOCKS integer
// value into a per-key enabled/disabled map. Only keys carried by
// `known` — each with the bit [TargetBitOrder] assigned its channel —
// are populated.
//
// Inverted bit logic: a SET bit means LOCKED (returned as `false`);
// a CLEAR bit means ENABLED (returned as `true`).
func ParseChannelLocks(locksValue uint32, known TargetChannelBits) map[string]bool {
	out := make(map[string]bool, len(known))
	for key, bit := range known {
		out[key] = locksValue&(1<<bit) == 0
	}
	return out
}

// scheduleKeyPrefix is the constant middle segment of the MASTER paramset
// schedule-group keys (e.g. "01_WP_CONDITION" → prefix "WP_").
const scheduleKeyPrefix = "_WP_"

// ExtractSupportedScheduleFields scans a MASTER paramset description map
// (keys are CCU parameter names) and returns the set of schedule fields the
// device advertises.
//
// CCU devices expose schedule group parameters in the MASTER paramset under
// keys matching the pattern `\d+_WP_<FIELDNAME>` (e.g. `01_WP_CONDITION`,
// `01_WP_LEVEL`). This function extracts every unique FIELDNAME that matches
// a known [hmenum.ScheduleField] value and returns them as a slice.
// Unknown field names and keys that do not match the pattern are silently
// skipped, matching the Python `_extract_supported_schedule_fields` logic.
//
// The returned slice is sorted for determinism.
func ExtractSupportedScheduleFields(masterParamset map[string]struct{}) []hmenum.ScheduleField {
	seen := make(map[hmenum.ScheduleField]struct{})
	for key := range masterParamset {
		// Fast path: key must contain "_WP_".
		before, after, ok := strings.Cut(key, scheduleKeyPrefix)
		if !ok {
			continue
		}
		// Everything before "_WP_" must be all digits.
		prefix := before
		if !allDigits(prefix) || prefix == "" {
			continue
		}
		fieldName := after
		if fieldName == "" {
			continue
		}
		sf := hmenum.ScheduleField(fieldName)
		switch sf {
		case hmenum.ScheduleFieldAstroOffset,
			hmenum.ScheduleFieldAstroType,
			hmenum.ScheduleFieldCondition,
			hmenum.ScheduleFieldDurationBase,
			hmenum.ScheduleFieldDurationFactor,
			hmenum.ScheduleFieldFixedHour,
			hmenum.ScheduleFieldFixedMinute,
			hmenum.ScheduleFieldLevel,
			hmenum.ScheduleFieldLevel2,
			hmenum.ScheduleFieldRampTimeBase,
			hmenum.ScheduleFieldRampTimeFactor,
			hmenum.ScheduleFieldTargetChannels,
			hmenum.ScheduleFieldWeekday,
			hmenum.ScheduleFieldColorType,
			hmenum.ScheduleFieldColorValue,
			hmenum.ScheduleFieldOutputBehaviour:
			seen[sf] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]hmenum.ScheduleField, 0, len(seen))
	for sf := range seen {
		out = append(out, sf)
	}
	// Sort for determinism.
	sortScheduleFields(out)
	return out
}

// allDigits reports whether s consists entirely of ASCII digit characters.
func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// sortScheduleFields sorts a slice of ScheduleField values in-place.
func sortScheduleFields(fields []hmenum.ScheduleField) {
	// Simple insertion sort — the slice is almost always tiny (≤13 elements).
	for i := 1; i < len(fields); i++ {
		key := fields[i]
		j := i - 1
		for j >= 0 && string(fields[j]) > string(key) {
			fields[j+1] = fields[j]
			j--
		}
		fields[j+1] = key
	}
}

// FilterRawScheduleByFields removes from raw all keys whose schedule
// field suffix is not present in supported. Keys that do not match the
// "NN_WP_FIELD" pattern are kept unchanged.
//
// When supported is nil or empty the raw map is returned unmodified
// because an absent description is not a reason to strip valid data.
//
// Mirrors `_filter_raw_schedule_by_supported_fields` from
// week_profile.py. Call this before writing a simple-schedule paramset
// to devices like HmIP-DLD that advertise only a subset of WP_* fields;
// sending unsupported fields causes the CCU to silently reject the
// write and leave CONFIG_PENDING set.
func FilterRawScheduleByFields(raw map[string]any, supported []hmenum.ScheduleField) map[string]any {
	if len(supported) == 0 || len(raw) == 0 {
		return raw
	}
	// Build a fast-lookup set.
	ok := make(map[hmenum.ScheduleField]struct{}, len(supported))
	for _, f := range supported {
		ok[f] = struct{}{}
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		_, after, ok0 := strings.Cut(k, scheduleKeyPrefix)
		if !ok0 {
			// Non-WP key — keep as-is.
			out[k] = v
			continue
		}
		// Always keep the WEEKDAY and TARGET_CHANNELS deactivation
		// sentinel values so deleted slots are cleared on the CCU.
		fieldName := after
		sf := hmenum.ScheduleField(fieldName)
		if _, supported := ok[sf]; supported ||
			sf == hmenum.ScheduleFieldWeekday ||
			sf == hmenum.ScheduleFieldTargetChannels {
			out[k] = v
		}
	}
	return out
}

// BuildCombinedParameterValue renders the COMBINED_PARAMETER write
// payload for a single-channel toggle in the
// `WPTCLS=<bitmask>,WPTCL=<mode>` format where mode=0 (MANU, disable)
// or mode=2 (AUTO, enable).
//
// The CCU processes WPTCLS first (which channels to affect), then
// WPTCL (what mode to set them to) — both in a single atomic setValue.
//
// Why the mode is binary although the parameter is a three-value ENUM.
// WEEK_PROGRAM_TARGET_CHANNEL_LOCK declares
// ["MANU_MODE", "AUTO_MODE_WITH_RESET", "AUTO_MODE_WITHOUT_RESET"] on every
// device that carries it, so index 1 is inside the declared range. It is
// skipped deliberately: the CCU's own weekly-program editor comments that
// option out of its select, and the firmware's own label for it reads
// "Wochenprogramm: Auto mit Reset (Reset ohne Funktion)" / "week program:
// Auto with reset (reset without function)". The vendor states the reset
// does nothing, which makes index 1 a no-op variant of index 2 rather than a
// third behaviour an operator could choose between.
//
// Bound on the firmware evidence for the format itself: this
// `WPTCLS=…,WPTCL=…` string appears in the CCU tree exactly once, in a
// getConfigString() helper that nothing calls, and not at all in the
// deployed www/ tree; the firmware's own live write is an
// Interface.putParamset on VALUES carrying the mode NAME as a string
// alongside WEEK_PROGRAM_TARGET_CHANNEL_LOCKS as an int. The format is kept
// because it was verified on real hardware — WPTCL=0 selected MANU and
// WPTCL=2 selected AUTO on two devices — which outranks a reading of the
// CCU's web UI about what a device accepts.
func BuildCombinedParameterValue(bitmask uint32, enabled bool) string {
	mode := 0
	if enabled {
		mode = 2
	}
	return fmt.Sprintf("WPTCLS=%d,WPTCL=%d", bitmask, mode)
}
