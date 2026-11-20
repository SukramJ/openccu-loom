// @vitest-environment happy-dom
//
// The weekday grid paints the base grey and the periods coloured. A day whose
// comfort phase is longer than its setback hours arrives with the comfort
// temperature as the daemon's base; the grid must still highlight the comfort
// phase, not the setback hours.
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";
import type { ClimateProfile } from "$lib/api/types";

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import ClimateScheduleVisualization from "./ClimateScheduleVisualization.svelte";

afterEach(() => cleanup());

function mondayTitles(container: HTMLElement): string[] {
  const column = container.querySelector('button[aria-label="weekday.short.MONDAY"]');
  expect(column).not.toBeNull();
  return Array.from(column!.querySelectorAll<HTMLElement>("div[title]")).map(
    (d) => d.getAttribute("title") ?? "",
  );
}

describe("ClimateScheduleVisualization", () => {
  it("shows the setback temperature as base and the comfort phase as period", () => {
    const profile: ClimateProfile = {
      weekdays: {
        MONDAY: {
          base_temperature: 21,
          periods: [
            { start_time: "00:00", end_time: "06:00", temperature: 19.5 },
            { start_time: "20:00", end_time: "24:00", temperature: 19.5 },
          ],
        },
      },
    };
    const { container } = render(ClimateScheduleVisualization, { props: { profile } });
    expect(mondayTitles(container)).toEqual([
      "00:00 – 06:00 · 19.5 °C · schedule.base_temperature",
      "06:00 – 20:00 · 21.0 °C",
      "20:00 – 24:00 · 19.5 °C · schedule.base_temperature",
    ]);
  });
});
