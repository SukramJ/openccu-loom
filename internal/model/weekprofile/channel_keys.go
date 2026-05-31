// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package weekprofile

import (
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// channelKeyBitmask maps the `<actor>_<sub>` channel key to its
// WEEK_PROGRAM_CHANNEL_LOCKS bit (8 actors × 3 sub-channels =
// 24 bits, each a distinct power of two).
//
// Inverted bit semantics: a SET bit means the channel is LOCKED
// (schedule disabled); a CLEAR bit means the channel is ENABLED.
// [ParseChannelLocks] handles the inversion.
var channelKeyBitmask = map[string]uint32{
	"1_1": 1 << 0,  // 1
	"1_2": 1 << 1,  // 2
	"1_3": 1 << 2,  // 4
	"2_1": 1 << 3,  // 8
	"2_2": 1 << 4,  // 16
	"2_3": 1 << 5,  // 32
	"3_1": 1 << 6,  // 64
	"3_2": 1 << 7,  // 128
	"3_3": 1 << 8,  // 256
	"4_1": 1 << 9,  // 512
	"4_2": 1 << 10, // 1024
	"4_3": 1 << 11, // 2048
	"5_1": 1 << 12, // 4096
	"5_2": 1 << 13, // 8192
	"5_3": 1 << 14, // 16384
	"6_1": 1 << 15, // 32768
	"6_2": 1 << 16, // 65536
	"6_3": 1 << 17, // 131072
	"7_1": 1 << 18, // 262144
	"7_2": 1 << 19, // 524288
	"7_3": 1 << 20, // 1048576
	"8_1": 1 << 21, // 2097152
	"8_2": 1 << 22, // 4194304
	"8_3": 1 << 23, // 8388608
}

// AllChannelKeys returns the canonical ordering of every supported
// schedule channel key (actor 1..8 × sub 1..3). The slice is freshly
// allocated; callers may mutate freely.
func AllChannelKeys() []string {
	out := make([]string, 0, 24)
	for actor := 1; actor <= 8; actor++ {
		for sub := 1; sub <= 3; sub++ {
			out = append(out, fmt.Sprintf("%d_%d", actor, sub))
		}
	}
	return out
}

// ChannelKeyToBitmask returns the WEEK_PROGRAM_CHANNEL_LOCKS bit for
// key. Returns (0, false) when key is not a recognised <actor>_<sub>
// pair.
func ChannelKeyToBitmask(key string) (uint32, bool) {
	v, ok := channelKeyBitmask[key]
	return v, ok
}

// BitmaskToChannelKey reverses [ChannelKeyToBitmask]: returns the key
// for an exact-match bitmask (a single bit). Returns ("", false) when
// bitmask is zero, has multiple bits set, or matches no known channel.
func BitmaskToChannelKey(bitmask uint32) (string, bool) {
	if bitmask == 0 || bitmask&(bitmask-1) != 0 {
		// Zero or multi-bit — not a single channel.
		return "", false
	}
	for key, bit := range channelKeyBitmask {
		if bit == bitmask {
			return key, true
		}
	}
	return "", false
}

// ParseChannelLocks decodes the raw WEEK_PROGRAM_CHANNEL_LOCKS integer
// value into a per-key enabled/disabled map. Only keys in the
// `availableKeys` slice are populated.
//
// Inverted bit logic: a SET bit means LOCKED (returned as `false`);
// a CLEAR bit means ENABLED (returned as `true`).
func ParseChannelLocks(locksValue uint32, availableKeys []string) map[string]bool {
	out := make(map[string]bool, len(availableKeys))
	for _, key := range availableKeys {
		bit, ok := channelKeyBitmask[key]
		if !ok {
			continue
		}
		out[key] = locksValue&bit == 0
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
		idx := strings.Index(key, scheduleKeyPrefix)
		if idx < 0 {
			continue
		}
		// Everything before "_WP_" must be all digits.
		prefix := key[:idx]
		if !allDigits(prefix) || prefix == "" {
			continue
		}
		fieldName := key[idx+len(scheduleKeyPrefix):]
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
			hmenum.ScheduleFieldWeekday:
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
		idx := strings.Index(k, scheduleKeyPrefix)
		if idx < 0 {
			// Non-WP key — keep as-is.
			out[k] = v
			continue
		}
		// Always keep the WEEKDAY and TARGET_CHANNELS deactivation
		// sentinel values so deleted slots are cleared on the CCU.
		fieldName := k[idx+len(scheduleKeyPrefix):]
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
func BuildCombinedParameterValue(bitmask uint32, enabled bool) string {
	mode := 0
	if enabled {
		mode = 2
	}
	return fmt.Sprintf("WPTCLS=%d,WPTCL=%d", bitmask, mode)
}
