// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent } from "@testing-library/svelte";

// Bulk-acknowledge ("acknowledge all") coverage: the confirm-gated button
// only renders when there is something to acknowledge, delegates to the
// bulk-ack API scoped by the active central filter, and surfaces both the
// CCU-reported count (success) and a failure (error path) via the shared
// toast store rather than failing silently.

const {
  mockListAlarmMessages,
  mockListServiceMessages,
  mockListSuppressedServices,
  mockAckAllAlarms,
  mockAckAllServices,
  mockDisableService,
  mockUnsuppressService,
  mockToastSuccess,
  mockToastError,
  mockConfirmAsk,
} = vi.hoisted(() => ({
  mockListAlarmMessages: vi.fn(),
  mockListServiceMessages: vi.fn(),
  mockListSuppressedServices: vi.fn(),
  mockAckAllAlarms: vi.fn(),
  mockAckAllServices: vi.fn(),
  mockDisableService: vi.fn(),
  mockUnsuppressService: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
  mockConfirmAsk: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listAlarmMessages: (...args: unknown[]) => mockListAlarmMessages(...args),
    listServiceMessages: (...args: unknown[]) => mockListServiceMessages(...args),
    listSuppressedServices: (...args: unknown[]) =>
      mockListSuppressedServices(...args),
    ackAlarm: vi.fn().mockResolvedValue(undefined),
    ackService: vi.fn().mockResolvedValue(undefined),
    disableService: (...args: unknown[]) => mockDisableService(...args),
    unsuppressService: (...args: unknown[]) => mockUnsuppressService(...args),
    ackAllAlarms: (...args: unknown[]) => mockAckAllAlarms(...args),
    ackAllServices: (...args: unknown[]) => mockAckAllServices(...args),
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
  toastStore: { success: mockToastSuccess, error: mockToastError },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: mockConfirmAsk },
}));

import MessageList from "./MessageList.svelte";

const ALARM = {
  id: "A1",
  name: "Smoke Alarm",
  timestamp: "2026-01-01T00:00:00Z",
  counter: 1,
};

const QUITTABLE_SERVICE = {
  id: "S1",
  name: "Low Battery",
  timestamp: "2026-01-01T00:00:00Z",
  counter: 1,
  quittable: true,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockListAlarmMessages.mockResolvedValue([]);
  mockListServiceMessages.mockResolvedValue([]);
  mockListSuppressedServices.mockResolvedValue([]);
  mockDisableService.mockResolvedValue(undefined);
  mockUnsuppressService.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => cleanup());

function findAckAllButton(container: HTMLElement): HTMLButtonElement | undefined {
  return [...container.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === "messages.ack_all.button",
  ) as HTMLButtonElement | undefined;
}

function findButtonByText(container: HTMLElement, text: string): HTMLButtonElement | undefined {
  return [...container.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === text,
  ) as HTMLButtonElement | undefined;
}

const SUPPRESSED_ENTRY = {
  central: "ccu-alpha",
  interface: "HmIP-RF",
  channel: "ABC123:1",
  parameter: "LOWBAT",
  device_name: "Flur Sensor",
};

describe("MessageList — acknowledge-all button visibility", () => {
  it("is hidden on the alarm tab when there are no alarm messages", async () => {
    mockListAlarmMessages.mockResolvedValue([]);
    const { container } = render(MessageList);
    await waitFor(() => expect(mockListAlarmMessages).toHaveBeenCalled());

    expect(findAckAllButton(container)).toBeUndefined();
  });

  it("is shown on the alarm tab once an alarm message is present", async () => {
    mockListAlarmMessages.mockResolvedValue([ALARM]);
    const { container } = render(MessageList);
    await waitFor(() => expect(findAckAllButton(container)).toBeDefined());
  });

  it("is hidden on the service tab when no message is quittable", async () => {
    mockListServiceMessages.mockResolvedValue([{ ...QUITTABLE_SERVICE, quittable: false }]);
    const { container } = render(MessageList);
    await waitFor(() => expect(mockListServiceMessages).toHaveBeenCalled());

    const serviceTab = screen.getByText("messages.service");
    await fireEvent.click(serviceTab);

    expect(findAckAllButton(container)).toBeUndefined();
  });
});

describe("MessageList — acknowledge-all flow", () => {
  it("asks for confirmation, then calls the bulk API and reports the count", async () => {
    mockListAlarmMessages.mockResolvedValue([ALARM]);
    mockAckAllAlarms.mockResolvedValue({ acknowledged: 3 });
    const { container } = render(MessageList);
    await waitFor(() => expect(findAckAllButton(container)).toBeDefined());

    await fireEvent.click(findAckAllButton(container)!);

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(mockAckAllAlarms).toHaveBeenCalledTimes(1));
    // Undefined central (no filter set) — the bulk pass covers every central.
    expect(mockAckAllAlarms).toHaveBeenCalledWith(undefined);
    await waitFor(() =>
      expect(mockToastSuccess).toHaveBeenCalledWith(
        expect.stringContaining("messages.ack_all.done"),
      ),
    );
    // A successful bulk ack must reload the list so the acknowledged
    // messages disappear from the view.
    await waitFor(() => expect(mockListAlarmMessages).toHaveBeenCalledTimes(2));
  });

  it("does not call the bulk API when the operator declines the confirm dialog", async () => {
    mockListAlarmMessages.mockResolvedValue([ALARM]);
    mockConfirmAsk.mockResolvedValue(false);
    const { container } = render(MessageList);
    await waitFor(() => expect(findAckAllButton(container)).toBeDefined());

    await fireEvent.click(findAckAllButton(container)!);

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    expect(mockAckAllAlarms).not.toHaveBeenCalled();
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("surfaces a CCU-side failure via the error toast without reloading", async () => {
    mockListAlarmMessages.mockResolvedValue([ALARM]);
    mockAckAllAlarms.mockRejectedValue(new Error("rega down"));
    const { container } = render(MessageList);
    await waitFor(() => expect(findAckAllButton(container)).toBeDefined());

    await fireEvent.click(findAckAllButton(container)!);

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("rega down"));
    expect(mockToastSuccess).not.toHaveBeenCalled();
    // The list is not force-reloaded on failure — the stale ack_all count
    // and messages remain exactly as they were.
    expect(mockListAlarmMessages).toHaveBeenCalledTimes(1);
  });

  it("scopes the bulk call to the service-messages tab and central filter", async () => {
    mockListAlarmMessages.mockResolvedValue([]);
    mockListServiceMessages.mockResolvedValue([
      { ...QUITTABLE_SERVICE, central: "ccu-alpha" },
    ]);
    mockAckAllServices.mockResolvedValue({ acknowledged: 1 });
    const { container } = render(MessageList);
    await waitFor(() => expect(mockListServiceMessages).toHaveBeenCalled());

    await fireEvent.click(screen.getByText("messages.service"));
    await waitFor(() => expect(findAckAllButton(container)).toBeDefined());

    await fireEvent.click(findAckAllButton(container)!);

    await waitFor(() => expect(mockAckAllServices).toHaveBeenCalledTimes(1));
    // No central filter was set in this test, so the call carries undefined
    // (bulk-ack across every registered central) rather than a stale value.
    expect(mockAckAllServices).toHaveBeenCalledWith(undefined);
    expect(mockAckAllAlarms).not.toHaveBeenCalled();
  });
});

// Permanent-suppression coverage: "Hide permanently" on a service message
// confirms, calls the disable-service API, and reloads (including the
// suppressed list); the "Restore" action on the Suppressed tab clears a
// suppression the same way. Both surface failures via the shared error
// toast instead of failing silently, matching the ack-all conventions
// above.
describe("MessageList — permanent suppression flow", () => {
  it("confirms, calls disableService, and reloads on success", async () => {
    mockListServiceMessages.mockResolvedValue([
      { ...QUITTABLE_SERVICE, central: "ccu-alpha" },
    ]);
    const { container } = render(MessageList);
    await fireEvent.click(screen.getByText("messages.service"));
    await waitFor(() => expect(findButtonByText(container, "messages.suppress")).toBeDefined());

    await fireEvent.click(findButtonByText(container, "messages.suppress")!);

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(mockDisableService).toHaveBeenCalledWith(QUITTABLE_SERVICE.id, "ccu-alpha"),
    );
    await waitFor(() =>
      expect(mockToastSuccess).toHaveBeenCalledWith("messages.suppressed"),
    );
    // A successful suppression reloads every list, including the
    // suppressed-messages tab.
    await waitFor(() => expect(mockListSuppressedServices).toHaveBeenCalledTimes(2));
  });

  it("does not call disableService when the operator declines the confirm dialog", async () => {
    mockListServiceMessages.mockResolvedValue([QUITTABLE_SERVICE]);
    mockConfirmAsk.mockResolvedValue(false);
    const { container } = render(MessageList);
    await fireEvent.click(screen.getByText("messages.service"));
    await waitFor(() => expect(findButtonByText(container, "messages.suppress")).toBeDefined());

    await fireEvent.click(findButtonByText(container, "messages.suppress")!);

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    expect(mockDisableService).not.toHaveBeenCalled();
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("surfaces a CCU-side suppression failure via the error toast", async () => {
    mockListServiceMessages.mockResolvedValue([QUITTABLE_SERVICE]);
    mockDisableService.mockRejectedValue(new Error("rega down"));
    const { container } = render(MessageList);
    await fireEvent.click(screen.getByText("messages.service"));
    await waitFor(() => expect(findButtonByText(container, "messages.suppress")).toBeDefined());

    await fireEvent.click(findButtonByText(container, "messages.suppress")!);

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("rega down"));
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("restores a suppressed message via unsuppressService and reloads", async () => {
    mockListSuppressedServices.mockResolvedValue([SUPPRESSED_ENTRY]);
    const { container } = render(MessageList);
    await fireEvent.click(screen.getByText("messages.suppressed.tab"));
    await waitFor(() =>
      expect(findButtonByText(container, "messages.unsuppress.button")).toBeDefined(),
    );

    await fireEvent.click(findButtonByText(container, "messages.unsuppress.button")!);

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(mockUnsuppressService).toHaveBeenCalledWith(
        {
          interface: SUPPRESSED_ENTRY.interface,
          channel: SUPPRESSED_ENTRY.channel,
          parameter: SUPPRESSED_ENTRY.parameter,
        },
        SUPPRESSED_ENTRY.central,
      ),
    );
    await waitFor(() =>
      expect(mockToastSuccess).toHaveBeenCalledWith("messages.unsuppressed"),
    );
    await waitFor(() => expect(mockListSuppressedServices).toHaveBeenCalledTimes(2));
  });

  it("surfaces a CCU-side restore failure via the error toast", async () => {
    mockListSuppressedServices.mockResolvedValue([SUPPRESSED_ENTRY]);
    mockUnsuppressService.mockRejectedValue(new Error("rega down"));
    const { container } = render(MessageList);
    await fireEvent.click(screen.getByText("messages.suppressed.tab"));
    await waitFor(() =>
      expect(findButtonByText(container, "messages.unsuppress.button")).toBeDefined(),
    );

    await fireEvent.click(findButtonByText(container, "messages.unsuppress.button")!);

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("rega down"));
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});
