// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent, within } from "@testing-library/svelte";
import type { SecurityFault } from "$lib/api/types";

const {
  mockListSecurityFaults,
  mockAcknowledgeSecurityFault,
  mockToastSuccess,
  mockToastError,
  mockConfirmAsk,
} = vi.hoisted(() => ({
  mockListSecurityFaults: vi.fn(),
  mockAcknowledgeSecurityFault: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
  mockConfirmAsk: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listSecurityFaults: (...args: unknown[]) => mockListSecurityFaults(...args),
    acknowledgeSecurityFault: (...args: unknown[]) =>
      mockAcknowledgeSecurityFault(...args),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

// The ledger refreshes off the daemon's security.fault_changed broadcast,
// so the shared pump is replaced by a hand-driven one.
let emit: ((ev: { type: string }) => void) | null = null;
vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: (handler: (ev: { type: string }) => void) => {
    emit = handler;
    return () => {
      emit = null;
    };
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: mockToastSuccess, error: mockToastError },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: mockConfirmAsk },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en" },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import SecurityFaults from "./SecurityFaults.svelte";

function fault(overrides: Partial<SecurityFault> = {}): SecurityFault {
  return {
    id: "f1",
    class: "smoke",
    reason: "unreachable",
    severity: "warning",
    source: { ref: "ccu-1|IF1|ADDR1:1|STATE", name: "Kitchen smoke", at: "2026-01-01T00:00:00Z" },
    since: "2026-06-30T00:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => cleanup());

describe("SecurityFaults — standing faults", () => {
  it("always shows the acknowledge-does-not-clear hint, not just a tooltip", async () => {
    mockListSecurityFaults.mockResolvedValue([]);
    const { findByText } = render(SecurityFaults);
    await findByText("security.faults.hint");
  });

  it("renders one row per fault with its reason, source and open status", async () => {
    mockListSecurityFaults.mockResolvedValue([fault()]);
    const { findByRole, getByText } = render(SecurityFaults);

    await findByRole("table");
    expect(getByText("Kitchen smoke")).toBeTruthy();
    expect(getByText("security.fault_reason.unreachable")).toBeTruthy();
    expect(getByText("security.faults.status.open")).toBeTruthy();
  });

  it("shows the acknowledged badge and no action for an already-acknowledged fault", async () => {
    mockListSecurityFaults.mockResolvedValue([
      fault({ acknowledged_at: "2026-07-01T00:00:00Z", acknowledged_by: "admin" }),
    ]);
    const { findByRole, getByText } = render(SecurityFaults);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    expect(within(row).queryByRole("button", { name: "common.acknowledge" })).toBeNull();
    expect(getByText(/security.faults.status.acknowledged_by/)).toBeTruthy();
  });
});

describe("SecurityFaults — acknowledge flow", () => {
  it("confirms destructively, then acknowledges and reloads", async () => {
    mockListSecurityFaults
      .mockResolvedValueOnce([fault()])
      .mockResolvedValueOnce([fault({ acknowledged_at: "2026-07-01T00:00:00Z" })]);
    mockAcknowledgeSecurityFault.mockResolvedValue(undefined);
    const { findByRole, getByText } = render(SecurityFaults);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    await fireEvent.click(within(row).getByRole("button", { name: "common.acknowledge" }));

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    expect(mockConfirmAsk.mock.calls[0][0]).toMatchObject({ destructive: true });
    await waitFor(() => expect(mockAcknowledgeSecurityFault).toHaveBeenCalledWith("f1"));
    await waitFor(() =>
      expect(mockToastSuccess).toHaveBeenCalledWith("security.faults.toast.acknowledged"),
    );
    await waitFor(() => expect(mockListSecurityFaults).toHaveBeenCalledTimes(2));
  });

  it("does not acknowledge when the operator declines the confirm dialog", async () => {
    mockListSecurityFaults.mockResolvedValue([fault()]);
    mockConfirmAsk.mockResolvedValue(false);
    const { findByRole, getByText } = render(SecurityFaults);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    await fireEvent.click(within(row).getByRole("button", { name: "common.acknowledge" }));

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    expect(mockAcknowledgeSecurityFault).not.toHaveBeenCalled();
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("surfaces an acknowledge failure via the error toast", async () => {
    mockListSecurityFaults.mockResolvedValue([fault()]);
    mockAcknowledgeSecurityFault.mockRejectedValueOnce(new Error("rega down"));
    const { findByRole, getByText } = render(SecurityFaults);
    await findByRole("table");

    const row = getByText("Kitchen smoke").closest("tr")!;
    await fireEvent.click(within(row).getByRole("button", { name: "common.acknowledge" }));

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith(
        "security.faults.toast.acknowledge_failed",
        "rega down",
      ),
    );
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});

describe("SecurityFaults — live refresh off the daemon's push", () => {
  it("re-reads the ledger when a fault broadcast arrives", async () => {
    mockListSecurityFaults.mockResolvedValue([]);
    const { findByText } = render(SecurityFaults);
    await findByText("security.faults.empty");

    // A detector going unreachable opens a fault. Without the push binding
    // the ledger stays empty until the operator reloads the page.
    mockListSecurityFaults.mockResolvedValue([fault()]);
    emit?.({ type: "security.fault_changed" });

    await findByText("Kitchen smoke");
  });

  it("ignores broadcasts from other domains", async () => {
    mockListSecurityFaults.mockResolvedValue([fault()]);
    render(SecurityFaults);
    await waitFor(() => expect(mockListSecurityFaults).toHaveBeenCalledTimes(1));

    emit?.({ type: "security.class_changed" });
    await new Promise((resolve) => setTimeout(resolve, 400));
    expect(mockListSecurityFaults).toHaveBeenCalledTimes(1);
  });
});
