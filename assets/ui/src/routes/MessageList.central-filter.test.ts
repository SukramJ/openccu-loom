// @vitest-environment happy-dom
//
// The CCU filter is persisted in localStorage and applied to all three
// tabs, but each tab derives its own list of centrals from its own rows.
// A filter naming a central that has no rows in the active tab therefore
// hides every message there, so the control that clears it has to stay on
// screen for as long as the filter is set — otherwise the operator's
// primary "what is wrong in my house" surface reads as empty with no way
// back.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent, within } from "@testing-library/svelte";

const {
  mockListAlarmMessages,
  mockListServiceMessages,
  mockListSuppressedServices,
} = vi.hoisted(() => ({
  mockListAlarmMessages: vi.fn(),
  mockListServiceMessages: vi.fn(),
  mockListSuppressedServices: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listAlarmMessages: (...args: unknown[]) => mockListAlarmMessages(...args),
    listServiceMessages: (...args: unknown[]) => mockListServiceMessages(...args),
    listSuppressedServices: (...args: unknown[]) => mockListSuppressedServices(...args),
    ackAlarm: vi.fn().mockResolvedValue(undefined),
    ackService: vi.fn().mockResolvedValue(undefined),
    disableService: vi.fn().mockResolvedValue(undefined),
    unsuppressService: vi.fn().mockResolvedValue(undefined),
    ackAllAlarms: vi.fn().mockResolvedValue({ acknowledged: 0 }),
    ackAllServices: vi.fn().mockResolvedValue({ acknowledged: 0 }),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(true) },
}));

// The real Select wraps bits-ui's floating-portal listbox, which happy-dom
// cannot drive (see SelectStub.svelte).
vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("./__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

import MessageList from "./MessageList.svelte";

const ALARM_A = {
  id: "A1",
  central: "ccu-a",
  name: "Rauchmelder Flur",
  timestamp: "2026-01-01T00:00:00Z",
  counter: 1,
};

const SERVICE_A = {
  id: "S1",
  central: "ccu-a",
  name: "Low Battery",
  timestamp: "2026-01-01T00:00:00Z",
  counter: 1,
  quittable: true,
};

const SERVICE_B = { ...SERVICE_A, id: "S2", central: "ccu-b", name: "Sabotage" };

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockListAlarmMessages.mockResolvedValue([ALARM_A]);
  mockListServiceMessages.mockResolvedValue([SERVICE_A, SERVICE_B]);
  mockListSuppressedServices.mockResolvedValue([]);
});

afterEach(() => cleanup());

/** The CCU filter is the only Select rendered in the page header. */
function centralFilterListbox(): HTMLElement | undefined {
  return screen.queryAllByRole("listbox")[0];
}

describe("MessageList — persisted CCU filter", () => {
  it("keeps its Select on a tab whose rows all belong to another central", async () => {
    // Persisted from the service tab, where both CCUs have messages.
    localStorage.setItem("messages:central", "ccu-b");

    render(MessageList);
    await waitFor(() => expect(mockListAlarmMessages).toHaveBeenCalled());

    // The alarm tab only carries ccu-a rows, so the filter empties it...
    await waitFor(() =>
      expect(screen.queryByText("Rauchmelder Flur")).not.toBeInTheDocument(),
    );

    // ...and the control that clears the filter must still be reachable.
    const listbox = centralFilterListbox();
    expect(listbox).toBeDefined();
    const clearAll = within(listbox!).getByText("common.all_ccus");
    await fireEvent.click(clearAll);

    await waitFor(() =>
      expect(screen.getByText("Rauchmelder Flur")).toBeInTheDocument(),
    );
    expect(localStorage.getItem("messages:central")).toBe("");
  });

  it("offers the stored central as an option even when no row carries it", async () => {
    // A renamed or removed CCU leaves a filter that matches nothing anywhere.
    localStorage.setItem("messages:central", "ccu-gone");

    render(MessageList);
    await waitFor(() => expect(mockListAlarmMessages).toHaveBeenCalled());

    const listbox = centralFilterListbox();
    expect(listbox).toBeDefined();
    const selected = within(listbox!).getByText("ccu-gone");
    expect(selected).toHaveAttribute("aria-selected", "true");
  });

  it("hides the filter when nothing is filtered and only one central reports", async () => {
    render(MessageList);
    await waitFor(() =>
      expect(screen.getByText("Rauchmelder Flur")).toBeInTheDocument(),
    );

    expect(centralFilterListbox()).toBeUndefined();
  });
});
