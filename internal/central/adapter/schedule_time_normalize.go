// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"sort"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// normalizeClimateScheduleTimes rewrites every period time of a submitted
// climate schedule into the form the grammar accepts, and reports each
// rewrite.
//
// It runs on the write path only, before serialisation, and it is the single
// place a correction becomes visible: [schedule.ParseClimateTimeCorrecting]
// knows that "24:30" is accepted as 23:55, but only a caller holding the whole
// payload can say which weekday and which period it happened in.
//
// Times the grammar rejects are left untouched — validation downstream reports
// them as errors, and rewriting them here would turn a refusal into a silent
// guess.
func normalizeClimateScheduleTimes(sched *hmapi.ClimateSchedule) []hmapi.ClimateTimeCorrection {
	if sched == nil || len(sched.Profiles) == 0 {
		return nil
	}
	var out []hmapi.ClimateTimeCorrection
	profiles := make([]string, 0, len(sched.Profiles))
	for name := range sched.Profiles {
		profiles = append(profiles, name)
	}
	// Map iteration is unordered; a stable report is what makes the result
	// comparable between two writes of the same payload.
	sort.Strings(profiles)
	for _, name := range profiles {
		prof := sched.Profiles[name]
		days := make([]string, 0, len(prof.Weekdays))
		for day := range prof.Weekdays {
			days = append(days, day)
		}
		sort.Strings(days)
		for _, day := range days {
			wd := prof.Weekdays[day]
			for i := range wd.Periods {
				p := &wd.Periods[i]
				for _, f := range []struct {
					name  string
					value *string
				}{{"start_time", &p.StartTime}, {"end_time", &p.EndTime}} {
					_, applied, err := schedule.ParseClimateTimeCorrecting(*f.value)
					if err != nil || applied == "" {
						continue
					}
					out = append(out, hmapi.ClimateTimeCorrection{
						Profile:   name,
						Weekday:   day,
						Period:    i,
						Field:     f.name,
						Requested: *f.value,
						Applied:   applied,
					})
					*f.value = applied
				}
			}
			prof.Weekdays[day] = wd
		}
	}
	return out
}
