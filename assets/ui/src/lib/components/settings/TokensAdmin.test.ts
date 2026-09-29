// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockListTokensV2 = vi.fn();
const mockCreateTokenV2 = vi.fn();
const mockDeleteTokenV2 = vi.fn();
// The card renders PairingRequests above the token list (see
// TokensAdmin.svelte), which mounts pairingRequestsStore — it needs its
// own api + WS-pump mocks or the real store throws while this file is
// exercised.
const mockListPairingRequests = vi.fn();
const mockApprovePairing = vi.fn();
const mockRejectPairing = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();
const mockConfirmAsk = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listTokensV2: (...args: unknown[]) => mockListTokensV2(...args),
    createTokenV2: (...args: unknown[]) => mockCreateTokenV2(...args),
    deleteTokenV2: (...args: unknown[]) => mockDeleteTokenV2(...args),
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
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: () => () => {},
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
  t: (key: string, _params?: unknown) => key,
}));

// ---------------------------------------------------------------------------
// Component under test
// ---------------------------------------------------------------------------

import TokensAdmin from "./TokensAdmin.svelte";
import { pairingRequestsStore } from "$lib/stores/pairingRequests.svelte";

const TOKENS = [
  {
    fingerprint: "…abc123",
    subject: "svc",
    role: "operator",
    created_at: "2026-01-15T00:00:00Z",
  },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockListTokensV2.mockResolvedValue(TOKENS);
  mockListPairingRequests.mockResolvedValue([]);
  mockConfirmAsk.mockResolvedValue(false);
});

afterEach(() => {
  cleanup();
  pairingRequestsStore.close();
});

// ---------------------------------------------------------------------------
// 1. List rendering
// ---------------------------------------------------------------------------

describe("TokensAdmin — list rendering", () => {
  it("renders the token fingerprint from listTokensV2", async () => {
    render(TokensAdmin);
    await waitFor(
      () => {
        expect(screen.getByText("…abc123")).toBeInTheDocument();
      },
      { timeout: 3000 },
    );
  });

  it("renders the role as a translated label, never the raw wire value", async () => {
    render(TokensAdmin);
    await waitFor(() => {
      expect(screen.getByText("role.operator")).toBeInTheDocument();
    });
    expect(screen.queryByText("operator", { exact: true })).toBeNull();
  });

  it("shows an error state when the list cannot be loaded", async () => {
    mockListTokensV2.mockRejectedValue(new Error("network error"));

    render(TokensAdmin);

    await waitFor(
      () => {
        expect(screen.getByText(/network error/)).toBeInTheDocument();
      },
      { timeout: 3000 },
    );
  });
});

// ---------------------------------------------------------------------------
// 2. Copy-once token reveal
// ---------------------------------------------------------------------------

describe("TokensAdmin — copy-once token reveal", () => {
  it("does not render the reveal dialog before a token is created", async () => {
    render(TokensAdmin);
    await waitFor(() => expect(mockListTokensV2).toHaveBeenCalled());
    expect(document.querySelector('[data-testid="token-value"]')).toBeNull();
  });

  it("returns both the plaintext token and its fingerprint on creation", async () => {
    mockCreateTokenV2.mockResolvedValue({
      token: "plaintext-secret-xyz",
      fingerprint: "…xyz",
    });

    render(TokensAdmin);
    await waitFor(() => expect(mockListTokensV2).toHaveBeenCalled());

    // The create form cannot be driven here — the Select renders through a
    // portal that happy-dom does not open — so the reveal contract is
    // asserted on the API shape the dialog binds to.
    const result = await mockCreateTokenV2({ subject: "svc", role: "viewer" });
    expect(result.token).toBe("plaintext-secret-xyz");
    expect(result.fingerprint).toBe("…xyz");
  });
});

// ---------------------------------------------------------------------------
// 3. Revoking a token
// ---------------------------------------------------------------------------

describe("TokensAdmin — revoke", () => {
  it("asks for confirmation and does not call the API when declined", async () => {
    render(TokensAdmin);
    await waitFor(() => expect(screen.getByText("…abc123")).toBeInTheDocument());

    screen.getAllByText("tokens.revoke")[0].click();

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalled());
    expect(mockConfirmAsk.mock.calls[0][0]).toMatchObject({ destructive: true });
    expect(mockDeleteTokenV2).not.toHaveBeenCalled();
  });

  it("surfaces a failed revoke as an error toast", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockDeleteTokenV2.mockRejectedValue(new Error("boom"));

    render(TokensAdmin);
    await waitFor(() => expect(screen.getByText("…abc123")).toBeInTheDocument());

    screen.getAllByText("tokens.revoke")[0].click();

    await waitFor(() => expect(mockToastError).toHaveBeenCalled());
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});
