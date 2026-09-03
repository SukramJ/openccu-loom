// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
//
// The firmware does establish the mapping, and this grid is not it. The CCU's
// own weekly-program editor derives the bit POSITIONALLY from the channel's
// place in the device's schedule-relevant channel list — every
// `*_VIRTUAL_RECEIVER` plus the access-control channel types, in channel order
// (`getRelevantChannels`,
// ../OpenCCU-Base/src/webui/www_source/ise/js/iseHmIPWeeklyProgram.js:517-555)
// — and takes the bit as `Math.pow(2, index)` over that list (:357).
//
// Two firmware facts do back the shape below. The semantics are inverted (a
// set bit selects Manu, i.e. schedule off — :239-241), and the stride is 3:
// the non-expert view seeds `tmpVal = 1` and shifts `tmpVal << 3` per actor
// (:361-364) and reads back every third bit (:614). So "actor N, sub S → bit
// 3(N-1)+(S-1)" is the firmware's own scheme for an ordinary multi-actor
// virtual-receiver device, and it is right for HmIP-BSM / PS / FSM and for
// HmIP-BSL, whose own firmware override `[4, 5, 6, 8, 9, 10, 12, 13, 14]`
// (:48-54) lands on positions 0..8 exactly as keys 1_1..3_3 do here.
//
// Where it is wrong, because the firmware overrides the list per family and
// this table cannot express an override:
//
//   - HmIP-DLP: the editor puts DOOR_LOCK_TRANSCEIVER :12 at bit 8 (value
//     256) and AUTO_RELOCK_TRANSCEIVER :13 at bit 9 (512), with bits 0..7
//     taken by the eight PERMISSION_TRANSCEIVER channels
//     (iseHmIPWeeklyProgram_AccessReceiver.js:300-313). A registry that
//     schedules only the door-lock group mints key 1_1 and writes bit 0 —
//     a permission channel.
//   - HmIP-FWI (Wiegand): ACCESS_TRANSCEIVER channels take bits 3..10 and the
//     virtual switch receivers keep bits 0..2 (:330-345, :465-479).
//   - HmIP-DRG-DALI: the bit is the channel number minus one over 48 channels
//     (:351), which is neither this stride nor 24 bits wide.
//   - HmIP-RGBW: the universal-light group carries a fourth member, which
//     mints key `1_4` — no entry here, so [ChannelKeyToBitmask] rejects it and
//     the write fails rather than addressing the firmware's bit 3.
//
// Deriving the bit from the device's own channel list is the fix, and it
// cannot be made here: the keys are minted from the custom-DP channel groups
// upstream of this package, and for the families above that list is not the
// firmware's relevant-channel list. The 24-bit mask measured on live hardware
// is a property of the devices it was measured on, not of the scheme —
// HmIP-DRG-DALI carries a 48-bit value.
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
