// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent, within } from "@testing-library/svelte";
import type { SecuritySourceView } from "$lib/api/types";

const {
  mockListSecuritySources,
  mockGetSecuritySnapshot,
  mockPutSecuritySourceOverride,
  mockToastSuccess,
  mockToastError,
} = vi.hoisted(() => ({
  mockListSecuritySources: vi.fn(),
  mockGetSecuritySnapshot: vi.fn(),
  mockPutSecuritySourceOverride: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listSecuritySources: (...args: unknown[]) => mockListSecuritySources(...args),
    getSecuritySnapshot: (...args: unknown[]) => mockGetSecuritySnapshot(...args),
    putSecuritySourceOverride: (...args: unknown[]) =>
      mockPutSecuritySourceOverride(...args),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: mockToastSuccess, error: mockToastError },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import SecuritySources from "./SecuritySources.svelte";

function source(overrides: Partial<SecuritySourceView> = {}): SecuritySourceView {
  return {
    ref: "ccu-1|IF1|ADDR1:1|STATE",
    central: "ccu-1",
    interface_id: "IF1",
    channel_address: "ADDR1:1",
    device_address: "ADDR1",
    parameter: "STATE",
    name: "Kitchen smoke",
    class: "smoke",
    active: true,
    relevant: true,
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetSecuritySnapshot.mockResolvedValue({
    severity: "ok",
    classes: [],
    engine_healthy: true,
    zones: [],
  });
  mockPutSecuritySourceOverride.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("SecuritySources — inventory table", () => {
  it("renders one row per classified source", async () => {
    mockListSecuritySources.mockResolvedValue([
      source(),
      source({
        ref: "ccu-2|IF2|ADDR2:1|STATE",
        central: "ccu-2",
        channel_address: "ADDR2:1",
        device_address: "ADDR2",
        name: "Garage water",
        class: "water",
        active: false,
        relevant: false,
      }),
    ]);
    const { findByRole, getByText } = render(SecuritySources);

    await findByRole("table");
    expect(getByText("Kitchen smoke")).toBeTruthy();
    expect(getByText("Garage water")).toBeTruthy();
  });

  it("narrows the visible rows when the relevant-only filter is switched on", async () => {
    mockListSecuritySources.mockResolvedValue([
      source(),
      source({
        ref: "ccu-2|IF2|ADDR2:1|STATE",
        channel_address: "ADDR2:1",
        device_address: "ADDR2",
        name: "Garage water",
        class: "water",
        relevant: false,
      }),
    ]);
    const { findByRole, getByText, queryByText } = render(SecuritySources);
    await findByRole("table");
    expect(getByText("Garage water")).toBeTruthy();

    const relevantLabel = getByText("security.sources.filter.relevant").closest("label")!;
    await fireEvent.click(within(relevantLabel).getByRole("switch"));

    expect(queryByText("Garage water")).toBeNull();
    expect(getByText("Kitchen smoke")).toBeTruthy();
  });
});

describe("SecuritySources — override flow", () => {
  it("saves an override with the entered note and reloads the inventory", async () => {
    mockListSecuritySources
      .mockResolvedValueOnce([source()])
      .mockResolvedValueOnce([source({ overridden: true })]);
    const { findByRole, getByText } = render(SecuritySources);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    const note = within(row).getByPlaceholderText(
      "security.sources.override.note_placeholder",
    );
    await fireEvent.input(note, { target: { value: "misclassified" } });
    await fireEvent.click(
      within(row).getByRole("button", { name: "security.sources.override.save" }),
    );

    await waitFor(() => expect(mockPutSecuritySourceOverride).toHaveBeenCalledTimes(1));
    expect(mockPutSecuritySourceOverride).toHaveBeenCalledWith(source().ref, {
      included: true,
      note: "misclassified",
    });
    await waitFor(() =>
      expect(mockToastSuccess).toHaveBeenCalledWith("security.sources.toast.saved"),
    );
    await waitFor(() => expect(mockListSecuritySources).toHaveBeenCalledTimes(2));
  });

  it("offers a one-click reset only when overridden, sending the bare removal payload", async () => {
    mockListSecuritySources.mockResolvedValue([source({ overridden: true })]);
    const { findByRole, getByText } = render(SecuritySources);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    const resetBtn = within(row).getByRole("button", {
      name: "security.sources.override.reset",
    });

    await fireEvent.click(resetBtn);

    await waitFor(() => expect(mockPutSecuritySourceOverride).toHaveBeenCalledTimes(1));
    // The undo is the bare "keep classifier verdict, included" payload,
    // independent of whatever the draft fields currently hold.
    expect(mockPutSecuritySourceOverride).toHaveBeenCalledWith(source().ref, {
      included: true,
    });
    await waitFor(() =>
      expect(mockToastSuccess).toHaveBeenCalledWith("security.sources.toast.reset"),
    );
  });

  it("does not offer a reset button for a source with no override", async () => {
    mockListSecuritySources.mockResolvedValue([source({ overridden: false })]);
    const { findByRole, getByText, queryByRole } = render(SecuritySources);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    expect(
      within(row).queryByRole("button", { name: "security.sources.override.reset" }),
    ).toBeNull();
    expect(queryByRole("table")).toBeTruthy();
  });

  it("seeds the included switch OFF for a source the operator already excluded", async () => {
    mockListSecuritySources.mockResolvedValue([
      source({ overridden: true, relevant: false }),
    ]);
    const { findByRole, getByText } = render(SecuritySources);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    const toggle = within(row).getByRole("switch");
    expect(toggle).toHaveAttribute("aria-checked", "false");
  });

  it("seeds the included switch ON for a note-only override whose stored bit is included, even though relevant is false", async () => {
    // A note-only override (no class picked) on a source with no alarm
    // role classifies as relevant=false regardless of its stored
    // `included` bit — `relevant` is not a proxy for "was this
    // excluded". The exact stored bit is override_included, and it
    // must win over the lossy relevant fallback.
    mockListSecuritySources.mockResolvedValue([
      source({ overridden: true, relevant: false, override_included: true }),
    ]);
    const { findByRole, getByText } = render(SecuritySources);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    const toggle = within(row).getByRole("switch");
    expect(toggle).toHaveAttribute("aria-checked", "true");
  });

  it("seeds the included switch ON for a source with no override", async () => {
    mockListSecuritySources.mockResolvedValue([
      source({ overridden: false, relevant: true }),
    ]);
    const { findByRole, getByText } = render(SecuritySources);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    const toggle = within(row).getByRole("switch");
    expect(toggle).toHaveAttribute("aria-checked", "true");
  });

  it("re-saving an excluded source (e.g. adding a note) keeps it excluded", async () => {
    // Before the fix, freshDraft() hard-coded included:true, so this
    // save silently re-included a source the operator had excluded.
    mockListSecuritySources.mockResolvedValue([
      source({ overridden: true, relevant: false }),
    ]);
    const { findByRole, getByText } = render(SecuritySources);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    const note = within(row).getByPlaceholderText(
      "security.sources.override.note_placeholder",
    );
    await fireEvent.input(note, { target: { value: "still a false positive" } });
    await fireEvent.click(
      within(row).getByRole("button", { name: "security.sources.override.save" }),
    );

    await waitFor(() => expect(mockPutSecuritySourceOverride).toHaveBeenCalledTimes(1));
    expect(mockPutSecuritySourceOverride).toHaveBeenCalledWith(source().ref, {
      included: false,
      note: "still a false positive",
    });
  });

  it("surfaces a save failure via the error toast without a silent abort", async () => {
    mockListSecuritySources.mockResolvedValue([source()]);
    mockPutSecuritySourceOverride.mockRejectedValueOnce(new Error("rega down"));
    const { findByRole, getByText } = render(SecuritySources);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    await fireEvent.click(
      within(row).getByRole("button", { name: "security.sources.override.save" }),
    );

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith(
        "security.sources.toast.save_failed",
        "rega down",
      ),
    );
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});
