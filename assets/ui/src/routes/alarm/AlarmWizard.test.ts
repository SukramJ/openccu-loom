// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen } from "@testing-library/svelte";

const mockRefresh = vi.fn();
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: { refresh: (...args: unknown[]) => mockRefresh(...args) },
}));

const mockCreateAlarmArea = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: { createAlarmArea: (...args: unknown[]) => mockCreateAlarmArea(...args) },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import AlarmWizard from "./AlarmWizard.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  mockRefresh.mockResolvedValue(undefined);
  mockCreateAlarmArea.mockResolvedValue({ id: "area-1", name: "Ground floor" });
  location.hash = "";
});

afterEach(() => {
  cleanup();
});

// `screen` binds queries to document.body, so every step's Next button can
// be found the same way regardless of which step is currently mounted —
// mirrors the pattern in routes/Setup.test.ts.
async function next() {
  await fireEvent.click(screen.getByRole("button", { name: /alarm.wizard.next/ }));
}

describe("AlarmWizard — step advance", () => {
  it("advances from step 1 through the step dots on Next", async () => {
    render(AlarmWizard);

    expect(screen.getByText("alarm.wizard.step.areas")).toBeTruthy();
    await next();
    expect(screen.getByText("alarm.wizard.step.sensors")).toBeTruthy();
    await next();
    expect(screen.getByText("alarm.wizard.step.outputs")).toBeTruthy();
    await next();
    expect(screen.getByText("alarm.wizard.step.delays")).toBeTruthy();
  });

  it("Back returns to the previous step", async () => {
    render(AlarmWizard);
    await next();
    expect(screen.getByText("alarm.wizard.step.sensors")).toBeTruthy();

    await fireEvent.click(screen.getByRole("button", { name: /alarm.wizard.back/ }));
    expect(screen.getByText("alarm.wizard.step.areas")).toBeTruthy();
  });
});

describe("AlarmWizard — finish", () => {
  it("calls createAlarmArea with the entered name + default delays on the final step", async () => {
    render(AlarmWizard);

    await fireEvent.input(screen.getByLabelText("alarm.area.name"), {
      target: { value: "Ground floor" },
    });
    await next(); // -> sensors
    await next(); // -> outputs
    await next(); // -> delays
    await next(); // -> codes
    await next(); // -> done
    expect(screen.getByText("alarm.wizard.step.done")).toBeTruthy();

    await fireEvent.click(screen.getByRole("button", { name: /alarm.wizard.finish/ }));

    await waitFor(() => {
      expect(mockCreateAlarmArea).toHaveBeenCalledOnce();
    });
    const payload = mockCreateAlarmArea.mock.calls[0][0];
    expect(payload.name).toBe("Ground floor");
    expect(payload.config.modes.perimeter).toEqual({
      exit_delay_s: 30,
      entry_delay_s: 15,
      trigger_time_s: 180,
    });
    expect(mockRefresh).toHaveBeenCalledOnce();
    expect(location.hash).toBe("#/alarm");
  });
});
