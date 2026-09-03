// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// TestLockPermissionEncodingReadsTheModelVocabulary drives the serialiser
// with the permission strings taken from the model constants rather than
// restated here, so the encode side is pinned to the same vocabulary the
// decode side (schedule.DetectLockPermission) already speaks. A test that
// spelled "granted" itself would stay green while the two sides drifted.
func TestLockPermissionEncodingReadsTheModelVocabulary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		permission schedule.LockPermission
		wantLevel  float64
	}{
		{schedule.LockPermissionAllowed, 1.0},
		{schedule.LockPermissionDenied, 0.0},
	}

	for _, tc := range cases {
		t.Run(string(tc.permission), func(t *testing.T) {
			t.Parallel()

			entries := []hmapi.SimpleScheduleEntry{
				{
					SlotNo:     1,
					Weekdays:   []string{"MONDAY"},
					Time:       "07:30",
					LockMode:   string(schedule.LockModeUserPermission),
					Permission: string(tc.permission),
				},
			}
			m, err := serializeSimpleScheduleWithDomain(
				entries, "lock", schedule.SimpleMaxSlot, nil, weekprofile.AstroOffsetLimits{},
			)
			if err != nil {
				t.Fatalf("serializeSimpleScheduleWithDomain lock: %v", err)
			}
			lvl, ok := m["01_WP_LEVEL"]
			if !ok {
				t.Fatalf("permission %q: LEVEL not written", tc.permission)
			}
			if lvl != tc.wantLevel {
				t.Errorf("permission %q: LEVEL = %v, want %v", tc.permission, lvl, tc.wantLevel)
			}
		})
	}
}
