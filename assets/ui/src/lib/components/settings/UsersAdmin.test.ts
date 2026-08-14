// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockListUsersV2 = vi.fn();
const mockCreateUser = vi.fn();
const mockUpdateUser = vi.fn();
const mockDeleteUser = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();
const mockConfirmAsk = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listUsersV2: (...args: unknown[]) => mockListUsersV2(...args),
    createUser: (...args: unknown[]) => mockCreateUser(...args),
    updateUser: (...args: unknown[]) => mockUpdateUser(...args),
    deleteUser: (...args: unknown[]) => mockDeleteUser(...args),
  },
  // Mirrors the real ApiError, including deriving problemCode from the
  // RFC 9457 body — the component branches on it.
  ApiError: class ApiError extends Error {
    public readonly status: number;
    public readonly body: unknown;
    constructor(status: number, body: unknown, message: string) {
      super(message);
      this.status = status;
      this.body = body;
    }
    get problemCode(): string {
      const b = this.body;
      if (typeof b === "object" && b !== null && "code" in b) {
        const code = (b as { code: unknown }).code;
        return typeof code === "string" ? code : "";
      }
      return "";
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

import UsersAdmin from "./UsersAdmin.svelte";

const USERS = [
  { subject: "admin", role: "admin", created_at: "2026-01-01T00:00:00Z" },
  { subject: "viewer1", role: "viewer", created_at: "2026-02-01T00:00:00Z" },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockListUsersV2.mockResolvedValue(USERS);
  mockConfirmAsk.mockResolvedValue(false);
});

afterEach(() => {
  cleanup();
});

// ---------------------------------------------------------------------------
// 1. List rendering
// ---------------------------------------------------------------------------

describe("UsersAdmin — list rendering", () => {
  it("renders user subjects from listUsersV2", async () => {
    render(UsersAdmin);
    // "viewer1" appears only as a subject (not as a role), so it is
    // unambiguous even though the role column renders labels too.
    await waitFor(
      () => {
        expect(screen.getByText("viewer1")).toBeInTheDocument();
      },
      { timeout: 3000 },
    );
  });

  it("renders roles as translated labels, never the raw wire value", async () => {
    render(UsersAdmin);
    await waitFor(() => {
      expect(screen.getByText("role.admin")).toBeInTheDocument();
      expect(screen.getByText("role.viewer")).toBeInTheDocument();
    });
    // The un-translated wire values must not leak into the badge.
    expect(screen.queryByText("viewer", { exact: true })).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// 2. Degraded state — 404 from listUsersV2
// ---------------------------------------------------------------------------

describe("UsersAdmin — degraded state", () => {
  it("shows the degraded note when listUsersV2 returns 404", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockListUsersV2.mockRejectedValue(new ApiError(404, null, "not found"));

    render(UsersAdmin);

    await waitFor(() => {
      expect(screen.getByText("users.degraded_note")).toBeInTheDocument();
    });
  });

  it("hides the add-user action while degraded", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockListUsersV2.mockRejectedValue(new ApiError(404, null, "not found"));

    render(UsersAdmin);

    await waitFor(() => {
      expect(screen.getByText("users.degraded_note")).toBeInTheDocument();
    });

    expect(screen.queryByText("users.add")).toBeNull();
  });

  it("shows an error state for non-404 load failures", async () => {
    mockListUsersV2.mockRejectedValue(new Error("network error"));

    render(UsersAdmin);

    await waitFor(
      () => {
        expect(screen.getByText(/network error/)).toBeInTheDocument();
      },
      { timeout: 3000 },
    );
  });
});

// ---------------------------------------------------------------------------
// 3. Deleting a user
// ---------------------------------------------------------------------------

describe("UsersAdmin — delete", () => {
  it("asks for confirmation and does not call the API when declined", async () => {
    render(UsersAdmin);
    await waitFor(() => expect(screen.getByText("viewer1")).toBeInTheDocument());

    screen.getAllByText("common.delete")[0].click();

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalled());
    expect(mockConfirmAsk.mock.calls[0][0]).toMatchObject({ destructive: true });
    expect(mockDeleteUser).not.toHaveBeenCalled();
  });

  it("reports the last-admin conflict in words, not as a bare status", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockConfirmAsk.mockResolvedValue(true);
    mockDeleteUser.mockRejectedValue(
      new ApiError(409, { code: "conflict" }, "conflict"),
    );

    render(UsersAdmin);
    await waitFor(() => expect(screen.getByText("viewer1")).toBeInTheDocument());

    screen.getAllByText("common.delete")[0].click();

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("users.last_admin_error"));
  });
});

// ---------------------------------------------------------------------------
// 4. Creating a user
// ---------------------------------------------------------------------------

describe("UsersAdmin — create", () => {
  it("says the name is taken when the daemon refuses the create", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockCreateUser.mockRejectedValue(new ApiError(409, { code: "conflict" }, "conflict"));

    const { container } = render(UsersAdmin);
    await waitFor(() => expect(screen.getByText("viewer1")).toBeInTheDocument());

    screen.getByText("users.add").click();
    await waitFor(() => expect(screen.getByText("users.add_title")).toBeInTheDocument());

    const inputs = container.querySelectorAll("input");
    await fireEvent.input(inputs[0], { target: { value: "viewer1" } });
    await fireEvent.input(inputs[1], { target: { value: "s3cret" } });

    screen.getByText("common.add").click();

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("users.exists_error"));
  });
});

// ---------------------------------------------------------------------------
// 5. Editing a user
// ---------------------------------------------------------------------------

describe("UsersAdmin — edit", () => {
  it("names the last-admin lockout when a role change is refused", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockUpdateUser.mockRejectedValue(new ApiError(409, { code: "conflict" }, "conflict"));

    render(UsersAdmin);
    await waitFor(() => expect(screen.getByText("viewer1")).toBeInTheDocument());

    screen.getAllByText("common.edit")[0].click();
    await waitFor(() => expect(screen.getByText("common.save")).toBeInTheDocument());
    screen.getByText("common.save").click();

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith("users.last_admin_demote_error"),
    );
  });
});
