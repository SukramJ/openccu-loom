// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";
import type { AlarmArea } from "$lib/api/types";

let mockAreasConfig: AlarmArea[] = [];
const mockRefresh = vi.fn().mockResolvedValue(undefined);
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get areasConfig() {
      return mockAreasConfig;
    },
    refresh: (...args: unknown[]) => mockRefresh(...args),
  },
}));

const mockGetAlarmArea = vi.fn();
const mockPutAlarmArea = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    getAlarmArea: (...args: unknown[]) => mockGetAlarmArea(...args),
    putAlarmArea: (...args: unknown[]) => mockPutAlarmArea(...args),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import AlarmPolicies from "./AlarmPolicies.svelte";

function area(config: Record<string, unknown> = {}) {
  return { id: "area-1", name: "Ground floor", position: 1, config };
}

// Locates a Switch by the text of the <label> that wraps it — every toggle
// in this view sits inside `<label class={rowClass}><span>text</span>
// <Switch/></label>`, so this mirrors the DOM shape instead of relying on
// switch ordering (which shifts as sections gain/lose rows).
function switchByLabel(container: HTMLElement, text: string): HTMLElement {
  const label = Array.from(container.querySelectorAll("label")).find(
    (l) => l.querySelector("span")?.textContent === text,
  );
  if (!label) throw new Error(`label "${text}" not found`);
  const el = label.querySelector('[role="switch"]');
  if (!el) throw new Error(`no switch under label "${text}"`);
  return el as HTMLElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockAreasConfig = [{ id: "area-1", name: "Ground floor" }];
  mockGetAlarmArea.mockResolvedValue(area());
  mockPutAlarmArea.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
});

describe("AlarmPolicies — schedules", () => {
  it("adding a schedule row appends one entry, removing it drops that entry only", async () => {
    const { getByRole, findByText, queryByText, getAllByRole } = render(AlarmPolicies);

    expect(await findByText("alarm.policies.schedules.empty")).toBeTruthy();

    await fireEvent.click(getByRole("button", { name: /alarm.policies.schedules.add/ }));
    expect(queryByText("alarm.policies.schedules.empty")).toBeNull();
    expect(document.querySelectorAll('input[type="time"]')).toHaveLength(1);

    await fireEvent.click(getByRole("button", { name: /alarm.policies.schedules.add/ }));
    expect(document.querySelectorAll('input[type="time"]')).toHaveLength(2);

    // Remove the first row; exactly one should remain.
    const removeButtons = getAllByRole("button", { name: "common.remove" });
    expect(removeButtons).toHaveLength(2);
    await fireEvent.click(removeButtons[0]);

    expect(document.querySelectorAll('input[type="time"]')).toHaveLength(1);
  });

  it("toggling a weekday button on a schedule row marks it pressed", async () => {
    const { getByRole, findByText, getAllByRole } = render(AlarmPolicies);
    await findByText("alarm.policies.schedules.empty");

    await fireEvent.click(getByRole("button", { name: /alarm.policies.schedules.add/ }));

    const monday = getAllByRole("button", { name: "weekday.short.MONDAY" })[0];
    expect(monday).toHaveAttribute("aria-pressed", "false");

    await fireEvent.click(monday);
    expect(monday).toHaveAttribute("aria-pressed", "true");
  });
});

describe("AlarmPolicies — code policy toggles persist via the mocked client", () => {
  it("flips require_arm on and saves it through putAlarmArea", async () => {
    const { container, getByRole, findByText } = render(AlarmPolicies);
    await findByText("alarm.policies.section.codes");

    const requireArm = switchByLabel(container, "alarm.policies.code.require_arm");
    expect(requireArm).toHaveAttribute("aria-checked", "false");

    await fireEvent.click(requireArm);
    expect(requireArm).toHaveAttribute("aria-checked", "true");

    // The dirty banner appears once the local draft differs from the
    // loaded config, offering the Save that PUTs the whole area back.
    expect(getByRole("button", { name: "common.save" })).toBeTruthy();
    await fireEvent.click(getByRole("button", { name: "common.save" }));

    await waitFor(() => expect(mockPutAlarmArea).toHaveBeenCalledOnce());
    const [id, sent] = mockPutAlarmArea.mock.calls[0];
    expect(id).toBe("area-1");
    expect(sent.config.code_policy.require_arm).toBe(true);
  });

  it("hazard-output silent toggle persists under hazard_outputs.silent", async () => {
    const { container, getByRole, findByText } = render(AlarmPolicies);
    await findByText("alarm.policies.section.hazard");

    const hazardSection = Array.from(container.querySelectorAll("h3")).find(
      (h) => h.textContent === "alarm.policies.section.hazard",
    )?.closest("div");
    if (!hazardSection) throw new Error("hazard card not found");
    const silentSwitch = switchByLabel(hazardSection as HTMLElement, "alarm.policies.output.silent");

    await fireEvent.click(silentSwitch);
    await fireEvent.click(getByRole("button", { name: "common.save" }));

    await waitFor(() => expect(mockPutAlarmArea).toHaveBeenCalledOnce());
    const sent = mockPutAlarmArea.mock.calls[0][1];
    expect(sent.config.hazard_outputs.silent).toBe(true);
  });

  it("loads an existing require_disarm=false policy into the tri-state select as 'never'", async () => {
    mockGetAlarmArea.mockResolvedValueOnce(
      area({ code_policy: { require_disarm: false } }),
    );
    const { findByText } = render(AlarmPolicies);

    // The Select trigger renders the option label as its own text.
    expect(await findByText("alarm.policies.code.require_disarm.never")).toBeTruthy();
  });
});
