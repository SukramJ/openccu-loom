// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

import type {
  ClimatePeriod,
  ClimateSchedule,
  ClimateWeekday,
} from "$lib/api/types";

// The daemon's simple week profile takes the temperature that runs for the
// most minutes as the day's base (schedule.IdentifyBaseTemperatureFromSegments,
// kept identical to the reference wire). Once a comfort phase is longer than
// the setback hours, that rule turns the day inside out: the comfort
// temperature becomes the base and the setback hours become the periods.
//
// The display re-bases every day on its lowest temperature, as the Home
// Assistant frontend does (homematicip-local-frontend
// packages/schedule-core/src/utils/converters.ts, parseSimpleWeekdaySchedule).
// The simple format always describes the full day, so the re-based form is
// the same schedule on the device; the daemon's write path accepts any base.

const DAY_MINUTES = 1440;

function timeToMinutes(t: string): number {
  const [h, m] = t.split(":").map(Number);
  if (!Number.isFinite(h) || !Number.isFinite(m)) return 0;
  return h * 60 + m;
}

function minutesToTime(m: number): string {
  const h = Math.floor(m / 60);
  const mm = m % 60;
  return `${String(h).padStart(2, "0")}:${String(mm).padStart(2, "0")}`;
}

type Segment = {
  start: number;
  end: number;
  startTime: string;
  endTime: string;
  temperature: number;
};

// The full day as contiguous segments: the periods in start order, every gap
// filled with the daemon's base, neighbours of equal temperature merged.
function fullDay(day: ClimateWeekday): Segment[] {
  const base = day.base_temperature;
  const periods: Segment[] = [...(day.periods ?? [])]
    .map((p) => ({
      start: timeToMinutes(p.start_time),
      end: timeToMinutes(p.end_time),
      startTime: p.start_time,
      endTime: p.end_time,
      temperature: p.temperature,
    }))
    .sort((a, b) => a.start - b.start);

  const filled: Segment[] = [];
  let cursor = 0;
  for (const p of periods) {
    if (p.start > cursor) {
      filled.push({
        start: cursor,
        end: p.start,
        startTime: minutesToTime(cursor),
        endTime: p.startTime,
        temperature: base,
      });
    }
    filled.push(p);
    cursor = p.end;
  }
  if (cursor < DAY_MINUTES) {
    filled.push({
      start: cursor,
      end: DAY_MINUTES,
      startTime: minutesToTime(cursor),
      endTime: "24:00",
      temperature: base,
    });
  }

  const merged: Segment[] = [];
  for (const s of filled) {
    const last = merged[merged.length - 1];
    if (last && last.end === s.start && last.temperature === s.temperature) {
      last.end = s.end;
      last.endTime = s.endTime;
    } else {
      merged.push({ ...s });
    }
  }
  return merged;
}

// rebaseClimateWeekday returns the day with its lowest temperature as the base
// and every warmer stretch as a period, in start order.
export function rebaseClimateWeekday(day: ClimateWeekday): ClimateWeekday {
  const segments = fullDay(day);
  const base = Math.min(...segments.map((s) => s.temperature));
  const periods: ClimatePeriod[] = segments
    .filter((s) => s.temperature !== base)
    .map((s) => ({
      start_time: s.startTime,
      end_time: s.endTime,
      temperature: s.temperature,
    }));
  return { base_temperature: base, periods };
}

// rebaseClimateSchedule applies rebaseClimateWeekday to every day of every
// profile and leaves the rest of the schedule as it is.
export function rebaseClimateSchedule(schedule: ClimateSchedule): ClimateSchedule {
  if (!schedule.profiles) return schedule;
  const profiles: NonNullable<ClimateSchedule["profiles"]> = {};
  for (const [pid, profile] of Object.entries(schedule.profiles)) {
    const weekdays: typeof profile.weekdays = {};
    for (const [day, wd] of Object.entries(profile.weekdays ?? {})) {
      weekdays[day] = rebaseClimateWeekday(wd);
    }
    profiles[pid] = { ...profile, weekdays };
  }
  return { ...schedule, profiles };
}
