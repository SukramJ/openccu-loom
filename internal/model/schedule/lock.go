// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package schedule

import "math"

// LockActionRaw bundles the (level, durBase, durFactor) wire encoding for one
// door-lock action. Exported so tests and adapters can iterate LockActionTable
// without duplicating the field names.
type LockActionRaw struct {
	level     float64
	durBase   int
	durFactor int
}

// Level returns the level wire value.
func (r LockActionRaw) Level() float64 { return r.level }

// DurBase returns the duration-base wire value.
func (r LockActionRaw) DurBase() int { return r.durBase }

// DurFactor returns the duration-factor wire value.
func (r LockActionRaw) DurFactor() int { return r.durFactor }

// LockActionTable maps door-lock action names to their wire encoding.
// The adapter layer reads this table directly so both sides share the same
// canonical label ↔ wire mapping without duplication.
var LockActionTable = map[LockAction]LockActionRaw{
	LockActionAutoRelockStart: {level: 0.0, durBase: 0, durFactor: 0},
	LockActionAutoRelockEnd:   {level: 0.0, durBase: 7, durFactor: 31},
	LockActionUnlock:          {level: 1.0, durBase: 7, durFactor: 31},
	LockActionOpen:            {level: 1.01, durBase: 7, durFactor: 31},
}

// lockActionTable is the unexported alias kept so that DetectLockAction (which
// iterates the table) continues to compile unchanged.
var lockActionTable = LockActionTable

// LockPermissionGranted and LockPermissionDenied are the canonical level
// thresholds used by [DetectLockPermission].
const lockPermissionThreshold = 0.5

// DetectLockAction maps a (level, durBase, durFactor) wire triplet back to the
// canonical [LockAction] label. Falls back to [LockActionAutoRelockStart] when
// no exact match is found.
//
// Exposed here so the domain model and tests can round-trip wire-level lock
// entries without depending on the adapter package.
func DetectLockAction(level float64, durBase, durFactor int) LockAction {
	for action, raw := range lockActionTable {
		if math.Abs(level-raw.Level()) < 1e-6 && durBase == raw.DurBase() && durFactor == raw.DurFactor() {
			return action
		}
	}
	return LockActionAutoRelockStart
}

// EncodeLockAction converts a [LockAction] label to its (level, durBase,
// durFactor) wire triplet. The second return value is false when the action is
// not in [LockActionTable].
func EncodeLockAction(action LockAction) (level float64, durBase, durFactor int, ok bool) {
	raw, ok := LockActionTable[action]
	if !ok {
		return 0, 0, 0, false
	}
	return raw.Level(), raw.DurBase(), raw.DurFactor(), true
}

// DetectLockMode returns [LockModeDoorLock] when any of the target channels
// belongs to actor 1 (channel IDs starting with "1_"); otherwise it returns
// [LockModeUserPermission].
//
// Mirrors detectLockMode in the adapter layer.
func DetectLockMode(targetChannels []string) LockMode {
	for _, ch := range targetChannels {
		if len(ch) >= 2 && ch[0] == '1' && ch[1] == '_' {
			return LockModeDoorLock
		}
	}
	return LockModeUserPermission
}

// DetectLockPermission maps a LEVEL value to [LockPermissionAllowed] (>= 0.5)
// or [LockPermissionDenied] (< 0.5).
//
// Mirrors detectLockPermission in the adapter layer.
func DetectLockPermission(level float64) LockPermission {
	if level >= lockPermissionThreshold {
		return LockPermissionAllowed
	}
	return LockPermissionDenied
}
