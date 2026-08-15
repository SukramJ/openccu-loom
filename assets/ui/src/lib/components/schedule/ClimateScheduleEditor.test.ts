// @vitest-environment happy-dom
//
// A rejected schedule write (device asleep, CCU 502, lock conflict) has to
// reach the operator as a failure. Rendering it the way the confirmation is
// rendered makes a heating profile that was never applied look applied.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";
import type { ClimateSchedule } from "$lib/api/types";

const mockGetDeviceSchedule = vi.fn();
const mockPutDeviceSchedule = vi.fn();
const mockSetDeviceActiveProfile = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    getDeviceSchedule: (...args: unknown[]) => mockGetDeviceSchedule(...args),
    putDeviceSchedule: (...args: unknown[]) => mockPutDeviceSchedule(...args),
    setDeviceActiveProfile: (...args: unknown[]) => mockSetDeviceActiveProfile(...args),
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
const mockToastError = vi.fn();

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    warn: vi.fn(),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
}));

import ClimateScheduleEditor from "./ClimateScheduleEditor.svelte";
import { ApiError } from "$lib/api/client";

const ADDRESS = "0002E4A17B93C1";

function baseSchedule(): ClimateSchedule {
  return {
    channel: { address: `${ADDRESS}:1`, number: 1, device_address: ADDRESS },
    kind: "climate",
    active_profile: "P1",
    profiles: {
      P1: {
        weekdays: {
          MONDAY: { base_temperature: 19, periods: [] },
        },
      },
    },
  } as ClimateSchedule;
}

function findButton(container: HTMLElement, text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === text,
  ) as HTMLButtonElement | undefined;
}

async function buttonWith(container: HTMLElement, text: string): Promise<HTMLButtonElement> {
  let btn: HTMLButtonElement | undefined;
  await waitFor(() => {
    btn = findButton(container, text);
    expect(btn).toBeDefined();
  });
  return btn!;
}

// Dirties the editor through the UI so the Save button enables.
async function addPeriod(container: HTMLElement) {
  await fireEvent.click(await buttonWith(container, "climate.add_period"));
  await waitFor(() => expect(findButton(container, "common.save")?.disabled).toBe(false));
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetDeviceSchedule.mockResolvedValue(baseSchedule());
  mockPutDeviceSchedule.mockResolvedValue(undefined);
  mockSetDeviceActiveProfile.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("ClimateScheduleEditor — save feedback", () => {
  it("reports a rejected write as an error toast", async () => {
    mockPutDeviceSchedule.mockRejectedValue(new ApiError(502, {}, "device not reachable"));

    const { container } = render(ClimateScheduleEditor, { props: { address: ADDRESS } });
    await waitFor(() => expect(mockGetDeviceSchedule).toHaveBeenCalledWith(ADDRESS));
    await addPeriod(container);

    await fireEvent.click(await buttonWith(container, "common.save"));

    await waitFor(() => expect(mockToastError).toHaveBeenCalled());
    expect(mockToastError.mock.calls[0][0]).toBe("schedule.save_failed");
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("confirms a stored write with a success toast", async () => {
    const { container } = render(ClimateScheduleEditor, { props: { address: ADDRESS } });
    await waitFor(() => expect(mockGetDeviceSchedule).toHaveBeenCalledWith(ADDRESS));
    await addPeriod(container);

    await fireEvent.click(await buttonWith(container, "common.save"));

    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith("schedule.saved_toast"));
    expect(mockToastError).not.toHaveBeenCalled();
  });
});

describe("ClimateScheduleEditor — active-profile switch", () => {
  it("reports a rejected profile switch as an error toast", async () => {
    mockSetDeviceActiveProfile.mockRejectedValue(new ApiError(502, {}, "device not reachable"));
    mockGetDeviceSchedule.mockResolvedValue({
      ...baseSchedule(),
      active_profile: "P2",
      profiles: {
        P1: { weekdays: { MONDAY: { base_temperature: 19, periods: [] } } },
        P2: { weekdays: { MONDAY: { base_temperature: 21, periods: [] } } },
      },
    } as ClimateSchedule);

    const { container } = render(ClimateScheduleEditor, { props: { address: ADDRESS } });
    await waitFor(() => expect(mockGetDeviceSchedule).toHaveBeenCalledWith(ADDRESS));

    // Select the profile that is not the running one, then make it active.
    await fireEvent.click(await buttonWith(container, "P1"));
    await fireEvent.click(await buttonWith(container, "climate.set_active"));

    await waitFor(() => expect(mockToastError).toHaveBeenCalled());
    expect(mockToastError.mock.calls[0][0]).toBe("climate.set_active_failed");
  });
});
