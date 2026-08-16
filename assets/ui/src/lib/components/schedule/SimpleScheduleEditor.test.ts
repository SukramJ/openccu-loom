// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/svelte";
import type { ClimateSchedule } from "$lib/api/types";

const mockPutDeviceSchedule = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    putDeviceSchedule: (...args: unknown[]) => mockPutDeviceSchedule(...args),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const mockToastSuccess = vi.fn();
const mockToastWarn = vi.fn();
const mockToastError = vi.fn();

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    warn: (...args: unknown[]) => mockToastWarn(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
}));

import SimpleScheduleEditor from "./SimpleScheduleEditor.svelte";

const ADDRESS = "0002E4A17B93C1";

function baseSchedule(): ClimateSchedule {
  return {
    channel: { address: `${ADDRESS}:4`, number: 4, device_address: ADDRESS },
    kind: "simple",
    domain: "switch",
    simple_entries: [],
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockPutDeviceSchedule.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
});

describe("SimpleScheduleEditor — empty state", () => {
  it("shows the empty-slots message and a disabled Save/Reset when there are no entries", () => {
    const onReload = vi.fn();
    render(SimpleScheduleEditor, {
      props: { address: ADDRESS, schedule: baseSchedule(), onReload },
    });

    expect(screen.getByText("schedule.empty_slots")).toBeInTheDocument();
    expect(screen.getByText(/^common\.save$/)).toBeDisabled();
    expect(screen.getByText("common.reset")).toBeDisabled();
  });
});

describe("SimpleScheduleEditor — add / edit / save", () => {
  it("adds a slot, toggles a weekday, and saves the resulting entry list", async () => {
    const onReload = vi.fn();
    const schedule = baseSchedule();
    render(SimpleScheduleEditor, { props: { address: ADDRESS, schedule, onReload } });

    await fireEvent.click(screen.getByText("schedule.add_slot"));

    // The new slot starts with Mon-Fri selected; toggle Saturday on too.
    // Scoped to the weekday-toggle group — "weekday.short.*" also appears
    // in the read-only visualisation strip above it.
    const weekdayGroup = screen.getByRole("group", { name: "schedule.aria.weekdays" });
    await fireEvent.click(within(weekdayGroup).getByText("weekday.short.SATURDAY"));

    const saveButton = screen.getByText(/^common\.save$/);
    await waitFor(() => expect(saveButton).not.toBeDisabled());
    await fireEvent.click(saveButton);

    await waitFor(() => {
      expect(mockPutDeviceSchedule).toHaveBeenCalledOnce();
    });
    const [addr, payload] = mockPutDeviceSchedule.mock.calls[0] as [string, ClimateSchedule];
    expect(addr).toBe(ADDRESS);
    expect(payload.kind).toBe("simple");
    expect(payload.simple_entries).toHaveLength(1);
    expect(payload.simple_entries?.[0]).toMatchObject({
      slot_no: 1,
      time: "07:00",
      condition: "fixed_time",
      level: 1,
      weekdays: expect.arrayContaining(["MONDAY", "SATURDAY"]),
    });
    expect(mockToastSuccess).toHaveBeenCalledOnce();
    expect(onReload).toHaveBeenCalledOnce();
  });

  it("warns instead of saving when a slot's weekdays are all deselected", async () => {
    const onReload = vi.fn();
    render(SimpleScheduleEditor, { props: { address: ADDRESS, schedule: baseSchedule(), onReload } });

    await fireEvent.click(screen.getByText("schedule.add_slot"));
    const weekdayGroup = screen.getByRole("group", { name: "schedule.aria.weekdays" });
    for (const day of ["MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY"]) {
      await fireEvent.click(within(weekdayGroup).getByText(`weekday.short.${day}`));
    }

    const saveButton = screen.getByText(/^common\.save$/);
    await waitFor(() => expect(saveButton).not.toBeDisabled());
    await fireEvent.click(saveButton);

    await waitFor(() => {
      expect(mockToastWarn).toHaveBeenCalledOnce();
    });
    expect(mockPutDeviceSchedule).not.toHaveBeenCalled();
    expect(onReload).not.toHaveBeenCalled();
  });

  it("removes a slot and disables Save again once back at the server state", async () => {
    render(SimpleScheduleEditor, { props: { address: ADDRESS, schedule: baseSchedule(), onReload: vi.fn() } });

    await fireEvent.click(screen.getByText("schedule.add_slot"));
    expect(screen.getByText(/^schedule\.slots_count/)).toBeInTheDocument();

    await fireEvent.click(screen.getByTitle("common.remove"));

    expect(screen.getByText("schedule.empty_slots")).toBeInTheDocument();
    expect(screen.getByText(/^common\.save$/)).toBeDisabled();
  });
});

describe("SimpleScheduleEditor — universal-light colour (W02)", () => {
  function lightSchedule(capable: boolean, colorType: number | null): ClimateSchedule {
    return {
      channel: { address: `${ADDRESS}:1`, number: 1, device_address: ADDRESS },
      kind: "simple",
      domain: "light",
      color_capable: capable,
      simple_entries: [
        {
          slot_no: 1,
          weekdays: ["MONDAY"],
          time: "07:00",
          level: 1,
          ...(colorType != null ? { color_type: colorType, color_value: 524288 } : {}),
        },
      ],
    };
  }

  it("shows a colour summary when the device is colour-capable", async () => {
    render(SimpleScheduleEditor, {
      props: { address: ADDRESS, schedule: lightSchedule(true, 2), onReload: vi.fn() },
    });
    // The colour summary lives in the advanced row (next to ramp time).
    await fireEvent.click(screen.getByText(/schedule\.advanced/));
    expect(screen.getByText("schedule.color.effect")).toBeInTheDocument();
  });

  it("hides the colour summary on a non-colour device", async () => {
    render(SimpleScheduleEditor, {
      props: { address: ADDRESS, schedule: lightSchedule(false, null), onReload: vi.fn() },
    });
    await fireEvent.click(screen.getByText(/schedule\.advanced/));
    expect(screen.queryByText("schedule.color.effect")).not.toBeInTheDocument();
    expect(screen.queryByText("schedule.color.hue_saturation")).not.toBeInTheDocument();
  });

  it("preserves the opaque colour through a save round-trip", async () => {
    const onReload = vi.fn();
    render(SimpleScheduleEditor, {
      props: { address: ADDRESS, schedule: lightSchedule(true, 2), onReload },
    });
    // Dirty the entry (add a weekday) so Save enables, then save.
    const weekdayGroup = screen.getByRole("group", { name: "schedule.aria.weekdays" });
    await fireEvent.click(within(weekdayGroup).getByText("weekday.short.TUESDAY"));
    const saveButton = screen.getByText(/^common\.save$/);
    await waitFor(() => expect(saveButton).not.toBeDisabled());
    await fireEvent.click(saveButton);

    await waitFor(() => expect(mockPutDeviceSchedule).toHaveBeenCalledOnce());
    const [, payload] = mockPutDeviceSchedule.mock.calls[0] as [string, ClimateSchedule];
    expect(payload.simple_entries?.[0]).toMatchObject({
      color_type: 2,
      color_value: 524288,
    });
  });
});

describe("SimpleScheduleEditor — expanded rows survive a delete", () => {
  function threeSlots(): ClimateSchedule {
    return {
      ...baseSchedule(),
      simple_entries: [1, 2, 3].map((slot) => ({
        slot_no: slot,
        weekdays: ["MONDAY"],
        time: `0${slot}:00`,
        level: 1,
      })),
    } as ClimateSchedule;
  }

  function advancedToggle(slotNo: number): HTMLElement {
    const row = document.getElementById(`schedule-slot-${slotNo}`);
    expect(row).not.toBeNull();
    return within(row as HTMLElement).getByText(/schedule\.advanced/);
  }

  it("keeps the advanced panel on the slot the operator opened", async () => {
    render(SimpleScheduleEditor, {
      props: { address: ADDRESS, schedule: threeSlots(), onReload: vi.fn() },
    });

    await fireEvent.click(advancedToggle(3));
    expect(advancedToggle(3).getAttribute("aria-expanded")).toBe("true");

    // Deleting slot 1 renumbers the array indices of slots 2 and 3.
    const firstRow = document.getElementById("schedule-slot-1") as HTMLElement;
    await fireEvent.click(within(firstRow).getByTitle("common.remove"));

    await waitFor(() => expect(document.getElementById("schedule-slot-1")).toBeNull());
    expect(advancedToggle(3).getAttribute("aria-expanded")).toBe("true");
    expect(advancedToggle(2).getAttribute("aria-expanded")).toBe("false");
  });
});
