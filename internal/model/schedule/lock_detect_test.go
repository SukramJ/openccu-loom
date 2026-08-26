// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package schedule

import "testing"

// TestDetectLockActionKnown exercises all four canonical wire encodings.
func TestDetectLockActionKnown(t *testing.T) {
	cases := []struct {
		level     float64
		durBase   int
		durFactor int
		want      LockAction
	}{
		{level: 0.0, durBase: 0, durFactor: 0, want: LockActionAutoRelockStart},
		{level: 0.0, durBase: 7, durFactor: 31, want: LockActionAutoRelockEnd},
		{level: 1.0, durBase: 7, durFactor: 31, want: LockActionUnlock},
		{level: 1.01, durBase: 7, durFactor: 31, want: LockActionOpen},
	}
	for _, tc := range cases {
		got := DetectLockAction(tc.level, tc.durBase, tc.durFactor)
		if got != tc.want {
			t.Errorf("DetectLockAction(%.4f,%d,%d) = %q, want %q",
				tc.level, tc.durBase, tc.durFactor, got, tc.want)
		}
	}
}

// TestDetectLockActionFallback verifies unrecognised wire data maps to AutoRelockStart.
func TestDetectLockActionFallback(t *testing.T) {
	if got := DetectLockAction(0.5, 99, 99); got != LockActionAutoRelockStart {
		t.Errorf("fallback = %q, want %q", got, LockActionAutoRelockStart)
	}
}

// TestEncodeLockActionRoundTrip verifies EncodeLockAction is the inverse of DetectLockAction.
func TestEncodeLockActionRoundTrip(t *testing.T) {
	cases := []struct {
		action    LockAction
		level     float64
		durBase   int
		durFactor int
	}{
		{LockActionAutoRelockStart, 0.0, 0, 0},
		{LockActionAutoRelockEnd, 0.0, 7, 31},
		{LockActionUnlock, 1.0, 7, 31},
		{LockActionOpen, 1.01, 7, 31},
	}
	for _, tc := range cases {
		level, durBase, durFactor, ok := EncodeLockAction(tc.action)
		if !ok {
			t.Errorf("EncodeLockAction(%q): not found", tc.action)
			continue
		}
		if level != tc.level || durBase != tc.durBase || durFactor != tc.durFactor {
			t.Errorf("EncodeLockAction(%q) = (%.4f,%d,%d), want (%.4f,%d,%d)",
				tc.action, level, durBase, durFactor, tc.level, tc.durBase, tc.durFactor)
		}
		// Round-trip back to action.
		if got := DetectLockAction(level, durBase, durFactor); got != tc.action {
			t.Errorf("round-trip: DetectLockAction(%.4f,%d,%d) = %q, want %q",
				level, durBase, durFactor, got, tc.action)
		}
	}
}

// TestLockActionLockAlias ensures LockActionLock and LockActionAutoRelockStart
// produce the same (0.0, 0, 0) wire encoding — the zero-value triplet.
func TestLockActionLockAlias(t *testing.T) {
	if LockActionLock != LockActionAutoRelockStart {
		t.Errorf("LockActionLock (%q) != LockActionAutoRelockStart (%q)", LockActionLock, LockActionAutoRelockStart)
	}
	level, durBase, durFactor, ok := EncodeLockAction(LockActionLock)
	if !ok || level != 0.0 || durBase != 0 || durFactor != 0 {
		t.Errorf("EncodeLockAction(LockActionLock) = (%.4f,%d,%d,%v), want (0,0,0,true)",
			level, durBase, durFactor, ok)
	}
}

// TestDetectLockModeDetection verifies channel-prefix-based mode detection.
func TestDetectLockModeDetection(t *testing.T) {
	if got := DetectLockMode([]string{"1_0001", "2_0002"}); got != LockModeDoorLock {
		t.Errorf("mode = %q, want %q", got, LockModeDoorLock)
	}
	if got := DetectLockMode([]string{"2_0001", "3_0002"}); got != LockModeUserPermission {
		t.Errorf("mode = %q, want %q", got, LockModeUserPermission)
	}
	if got := DetectLockMode(nil); got != LockModeUserPermission {
		t.Errorf("empty channels: mode = %q, want %q", got, LockModeUserPermission)
	}
}

// TestDetectLockPermissionBoundary covers the 0.5 threshold edge.
func TestDetectLockPermissionBoundary(t *testing.T) {
	cases := []struct {
		level float64
		want  LockPermission
	}{
		{0.5, LockPermissionAllowed},
		{1.0, LockPermissionAllowed},
		{0.0, LockPermissionDenied},
		{0.49, LockPermissionDenied},
	}
	for _, tc := range cases {
		got := DetectLockPermission(tc.level)
		if got != tc.want {
			t.Errorf("DetectLockPermission(%.2f) = %q, want %q", tc.level, got, tc.want)
		}
	}
}
