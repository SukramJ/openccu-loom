// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockListPairingRequests = vi.fn();
const mockApprovePairing = vi.fn();
const mockRejectPairing = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();
const mockConfirmAsk = vi.fn();
const mockSubscribe = vi.fn((..._args: unknown[]) => () => {});

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listPairingRequests: (...args: unknown[]) => mockListPairingRequests(...args),
    approvePairing: (...args: unknown[]) => mockApprovePairing(...args),
    rejectPairing: (...args: unknown[]) => mockRejectPairing(...args),
  },
  ApiError: class ApiError extends Error {
    public readonly status: number;
    public readonly body: unknown;
    constructor(status: number, body: unknown, message: string) {
      super(message);
      this.status = status;
      this.body = body;
    }
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: (...args: unknown[]) => mockSubscribe(...args),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: {
    ask: (...args: unknown[]) => mockConfirmAsk(...args),
  },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en" },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

// ---------------------------------------------------------------------------
// Component under test
// ---------------------------------------------------------------------------

import PairingRequests from "./PairingRequests.svelte";
import { pairingRequestsStore } from "$lib/stores/pairingRequests.svelte";
import { ApiError } from "$lib/api/client";

const REQUEST = {
  id: "req-1",
  app: "Home Assistant",
  app_version: "2026.1.0",
  instance: "hass-core",
  name: "Kitchen bridge",
  address: "192.0.2.10",
  role: "operator",
  purpose: "MQTT discovery",
  code: "123456",
  created: "2026-01-01T08:55:00Z",
  expires: "2026-01-01T09:05:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  mockListPairingRequests.mockResolvedValue([REQUEST]);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => {
  cleanup();
  pairingRequestsStore.close();
});

describe("PairingRequests — rendering", () => {
  it("renders no card when there are no pending requests", async () => {
    mockListPairingRequests.mockResolvedValue([]);
    render(PairingRequests);
    await waitFor(() => expect(mockListPairingRequests).toHaveBeenCalled());
    expect(screen.queryByText("pairing.title")).toBeNull();
  });

  it("renders name, app line, address, role and purpose", async () => {
    render(PairingRequests);
    await waitFor(() => {
      expect(screen.getByText("Kitchen bridge")).toBeInTheDocument();
    });
    expect(screen.getByText(/Home Assistant/)).toBeInTheDocument();
    expect(screen.getByText("192.0.2.10")).toBeInTheDocument();
    expect(screen.getByText("MQTT discovery")).toBeInTheDocument();
    expect(screen.getByText("role.operator")).toBeInTheDocument();
  });

  it("shows the look-alike warning only when the flag is set", async () => {
    mockListPairingRequests.mockResolvedValue([{ ...REQUEST, look_alike: true }]);
    render(PairingRequests);
    await waitFor(() => {
      expect(screen.getByText("pairing.look_alike_warning")).toBeInTheDocument();
    });
  });

  it("does not show the look-alike warning when the flag is absent", async () => {
    render(PairingRequests);
    await waitFor(() => expect(screen.getByText("Kitchen bridge")).toBeInTheDocument());
    expect(screen.queryByText("pairing.look_alike_warning")).toBeNull();
  });
});

describe("PairingRequests — approve", () => {
  it("disables Approve until six digits are typed", async () => {
    render(PairingRequests);
    await waitFor(() => expect(screen.getByText("Kitchen bridge")).toBeInTheDocument());

    const approveBtn = screen.getByRole("button", { name: "pairing.approve" });
    expect(approveBtn).toBeDisabled();

    const codeInput = screen.getByPlaceholderText("pairing.code_placeholder");
    await fireEvent.input(codeInput, { target: { value: "123" } });
    expect(approveBtn).toBeDisabled();

    await fireEvent.input(codeInput, { target: { value: "123456" } });
    expect(approveBtn).not.toBeDisabled();
  });

  it("calls approvePairing with the typed code and refreshes on success", async () => {
    mockApprovePairing.mockResolvedValue(undefined);
    render(PairingRequests);
    await waitFor(() => expect(screen.getByText("Kitchen bridge")).toBeInTheDocument());

    const codeInput = screen.getByPlaceholderText("pairing.code_placeholder");
    await fireEvent.input(codeInput, { target: { value: "654321" } });

    mockListPairingRequests.mockResolvedValueOnce([]);
    await fireEvent.click(screen.getByRole("button", { name: "pairing.approve" }));

    await waitFor(() => expect(mockApprovePairing).toHaveBeenCalledWith("req-1", "654321"));
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith("pairing.approved"));
  });

  it("shows a clear rejected-on-wrong-code toast on 409", async () => {
    mockApprovePairing.mockRejectedValue(new ApiError(409, {}, "wrong code"));

    render(PairingRequests);
    await waitFor(() => expect(screen.getByText("Kitchen bridge")).toBeInTheDocument());

    const codeInput = screen.getByPlaceholderText("pairing.code_placeholder");
    await fireEvent.input(codeInput, { target: { value: "000000" } });
    await fireEvent.click(screen.getByRole("button", { name: "pairing.approve" }));

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith("pairing.wrong_code_rejected"),
    );
  });
});

describe("PairingRequests — reject", () => {
  it("asks for confirmation with destructive: false and rejects on confirm", async () => {
    mockRejectPairing.mockResolvedValue(undefined);
    render(PairingRequests);
    await waitFor(() => expect(screen.getByText("Kitchen bridge")).toBeInTheDocument());

    await fireEvent.click(screen.getByRole("button", { name: "pairing.reject" }));

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalled());
    expect(mockConfirmAsk.mock.calls[0][0]).toMatchObject({ destructive: false });
    await waitFor(() => expect(mockRejectPairing).toHaveBeenCalledWith("req-1"));
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith("pairing.rejected"));
  });

  it("does not reject when the confirm dialog is declined", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    render(PairingRequests);
    await waitFor(() => expect(screen.getByText("Kitchen bridge")).toBeInTheDocument());

    await fireEvent.click(screen.getByRole("button", { name: "pairing.reject" }));

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalled());
    expect(mockRejectPairing).not.toHaveBeenCalled();
  });
});
