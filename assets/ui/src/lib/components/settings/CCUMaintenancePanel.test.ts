// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock state
// ---------------------------------------------------------------------------

const mockGetSystemCCUs = vi.fn();
const mockRebootCCU = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();
const mockConfirmAsk = vi.fn();

// Identity is mutated per test to flip the admin gate.
const mockIdentity: { role: string | null } = { role: "admin" };

// ---------------------------------------------------------------------------
// Module mocks — hoisted before the component import
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    getSystemCCUs: (...args: unknown[]) => mockGetSystemCCUs(...args),
    rebootCCU: (...args: unknown[]) => mockRebootCCU(...args),
  },
  ApiError: class ApiError extends Error {
    constructor(
      public readonly status: number,
      public readonly body: unknown,
      message: string,
    ) {
      super(message);
    }
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: {
    get identity() {
      return mockIdentity.role ? { role: mockIdentity.role } : null;
    },
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// ---------------------------------------------------------------------------
// Component under test
// ---------------------------------------------------------------------------

import CCUMaintenancePanel from "./CCUMaintenancePanel.svelte";

const CCUS = [
  {
    name: "ccu-home",
    host: "192.0.2.29",
    available: true,
    is_ha_app: false,
    configured_interfaces: ["HmIP-RF"],
    readiness: { phase: "ready", ready: true, interfaces_loaded: 1, interfaces_total: 1 },
  },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockIdentity.role = "admin";
  mockGetSystemCCUs.mockResolvedValue(CCUS);
  mockRebootCCU.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(cleanup);

describe("CCUMaintenancePanel", () => {
  it("reboots a central after a confirmed prompt and toasts success", async () => {
    render(CCUMaintenancePanel);
    const btn = await screen.findByText("ccu_maintenance.reboot");
    await fireEvent.click(btn);
    await waitFor(() => expect(mockRebootCCU).toHaveBeenCalledWith("ccu-home"));
    expect(mockConfirmAsk).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledTimes(1));
  });

  it("does not reboot when the confirm is dismissed", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    render(CCUMaintenancePanel);
    const btn = await screen.findByText("ccu_maintenance.reboot");
    await fireEvent.click(btn);
    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    expect(mockRebootCCU).not.toHaveBeenCalled();
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("surfaces a reboot failure as a toast error", async () => {
    mockRebootCCU.mockRejectedValue(new Error("ccu down"));
    render(CCUMaintenancePanel);
    const btn = await screen.findByText("ccu_maintenance.reboot");
    await fireEvent.click(btn);
    await waitFor(() => expect(mockToastError).toHaveBeenCalledTimes(1));
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("hides the reboot button for non-admins", async () => {
    mockIdentity.role = "viewer";
    render(CCUMaintenancePanel);
    // The admin-only note renders once the list has loaded.
    await screen.findByText("ccu_maintenance.admin_only");
    expect(screen.queryByText("ccu_maintenance.reboot")).toBeNull();
  });

  it("shows an empty state when no CCU is configured", async () => {
    mockGetSystemCCUs.mockResolvedValue([]);
    render(CCUMaintenancePanel);
    await screen.findByText("ccu_maintenance.empty");
    expect(screen.queryByText("ccu_maintenance.reboot")).toBeNull();
  });
});
