// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestScheduleUnsupportedFieldsStripMatchesValidator pins the read path's
// strip to the write path's rejection, per category and per field: a field
// the adapter clears must be exactly a field the domain validator refuses,
// and vice versa.
//
// The asymmetry it guards is the round-trip defect — the CCU's
// COMBINED_PARAMETER carries fields a channel type never uses, so a read that
// keeps one emits a schedule the following write rejects, while a read that
// drops one the validator accepts silently loses operator data.
func TestScheduleUnsupportedFieldsStripMatchesValidator(t *testing.T) {
	t.Parallel()

	categories := []hmenum.DataPointCategory{
		hmenum.DataPointCategorySwitch,
		hmenum.DataPointCategoryLight,
		hmenum.DataPointCategoryCover,
		hmenum.DataPointCategoryValve,
		hmenum.DataPointCategoryLock,
	}

	half := 0.5
	fields := []struct {
		name string
		set  func(*schedule.SimpleEntry, *hmapi.SimpleScheduleEntry)
		kept func(hmapi.SimpleScheduleEntry) bool
	}{
		{
			name: "level_2",
			set: func(d *schedule.SimpleEntry, a *hmapi.SimpleScheduleEntry) {
				v := half
				d.Level2 = &v
				w := half
				a.Level2 = &w
			},
			kept: func(a hmapi.SimpleScheduleEntry) bool { return a.Level2 != nil },
		},
		{
			name: "ramp_time",
			set: func(d *schedule.SimpleEntry, a *hmapi.SimpleScheduleEntry) {
				d.RampTime = "2s"
				a.RampTime = "2s"
			},
			kept: func(a hmapi.SimpleScheduleEntry) bool { return a.RampTime != "" },
		},
		{
			name: "duration",
			set: func(d *schedule.SimpleEntry, a *hmapi.SimpleScheduleEntry) {
				d.Duration = "10s"
				a.Duration = "10s"
			},
			kept: func(a hmapi.SimpleScheduleEntry) bool { return a.Duration != "" },
		},
	}

	for _, cat := range categories {
		for _, f := range fields {
			t.Run(string(cat)+"/"+f.name, func(t *testing.T) {
				t.Parallel()

				domainEntry := schedule.EmptySimpleEntry(cat)
				wire := hmapi.SimpleScheduleEntry{}
				f.set(&domainEntry, &wire)

				// Baseline: the entry without the field must pass, otherwise
				// the row measures an unrelated validation failure.
				base := schedule.EmptySimpleEntry(cat)
				if err := base.ValidateFor(cat); err != nil {
					t.Fatalf("baseline entry for %s is already invalid: %v", cat, err)
				}

				rejected := domainEntry.ValidateFor(cat) != nil

				entries := []hmapi.SimpleScheduleEntry{wire}
				stripUnsupportedFields(entries, string(cat))
				stripped := !f.kept(entries[0])

				if stripped != rejected {
					t.Fatalf("%s/%s: read path strips = %v, write path rejects = %v — the two catalogues disagree",
						cat, f.name, stripped, rejected)
				}
			})
		}
	}
}
