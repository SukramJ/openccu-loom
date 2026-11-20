// The daemon names the longest-running temperature the day's base. With a
// comfort phase longer than the setback hours that puts the setback hours in
// the periods, and the UI highlights them. The display re-bases each day on
// its lowest temperature.
import { describe, it, expect } from "vitest";
import type { ClimateSchedule, ClimateWeekday } from "$lib/api/types";
import { rebaseClimateSchedule, rebaseClimateWeekday } from "./climateRebase";

// 06:00–20:00 at 21 °C over 19.5 °C, as the daemon encodes it: 21 °C runs
// 840 minutes, so it is the base and the setback hours are the periods.
function longComfortDay(): ClimateWeekday {
  return {
    base_temperature: 21,
    periods: [
      { start_time: "00:00", end_time: "06:00", temperature: 19.5 },
      { start_time: "20:00", end_time: "24:00", temperature: 19.5 },
    ],
  };
}

describe("rebaseClimateWeekday", () => {
  it("takes the lowest temperature as base when the comfort phase is longer", () => {
    expect(rebaseClimateWeekday(longComfortDay())).toEqual({
      base_temperature: 19.5,
      periods: [{ start_time: "06:00", end_time: "20:00", temperature: 21 }],
    });
  });

  it("keeps a flat day flat", () => {
    expect(rebaseClimateWeekday({ base_temperature: 17, periods: [] })).toEqual({
      base_temperature: 17,
      periods: [],
    });
  });

  it("leaves a day unchanged whose lowest temperature is the longest", () => {
    const day: ClimateWeekday = {
      base_temperature: 17,
      periods: [
        { start_time: "06:00", end_time: "08:00", temperature: 21 },
        { start_time: "17:00", end_time: "22:00", temperature: 21.5 },
      ],
    };
    expect(rebaseClimateWeekday(day)).toEqual(day);
  });

  it("keeps the warmer periods beside the new base and orders them by start", () => {
    const day: ClimateWeekday = {
      base_temperature: 20,
      periods: [
        { start_time: "22:00", end_time: "24:00", temperature: 16 },
        { start_time: "00:00", end_time: "05:00", temperature: 16 },
        { start_time: "07:00", end_time: "09:00", temperature: 22 },
      ],
    };
    expect(rebaseClimateWeekday(day)).toEqual({
      base_temperature: 16,
      periods: [
        { start_time: "05:00", end_time: "07:00", temperature: 20 },
        { start_time: "07:00", end_time: "09:00", temperature: 22 },
        { start_time: "09:00", end_time: "22:00", temperature: 20 },
      ],
    });
  });
});

describe("rebaseClimateSchedule", () => {
  it("re-bases every day of every profile and keeps the rest", () => {
    const schedule = {
      kind: "climate",
      active_profile: "P2",
      profiles: {
        P1: { weekdays: { MONDAY: longComfortDay() } },
        P2: { weekdays: { SUNDAY: longComfortDay() } },
      },
    } as unknown as ClimateSchedule;
    const out = rebaseClimateSchedule(schedule);
    expect(out.active_profile).toBe("P2");
    expect(out.profiles?.P1.weekdays.MONDAY.base_temperature).toBe(19.5);
    expect(out.profiles?.P2.weekdays.SUNDAY.base_temperature).toBe(19.5);
    // The fetched schedule itself is not mutated.
    expect(schedule.profiles?.P1.weekdays.MONDAY.base_temperature).toBe(21);
  });
});
