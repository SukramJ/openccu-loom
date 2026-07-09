// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockLogin = vi.fn();
const mockInfo = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    info: (...args: unknown[]) => mockInfo(...args),
  },
}));

let authError: string | null = null;

vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: {
    login: (...args: unknown[]) => mockLogin(...args),
    get error() {
      return authError;
    },
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, _params?: unknown) => key,
}));

// BrandMark renders an SVG logo; stub it out to avoid asset-import issues.
vi.mock("$lib/components/ui/BrandMark.svelte", () => ({
  default: vi.fn(),
}));

// ---------------------------------------------------------------------------
// Component under test — imported after mocks are registered
// ---------------------------------------------------------------------------

import Login from "./Login.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  authError = null;
  mockInfo.mockResolvedValue({ capabilities: [] });
  mockLogin.mockResolvedValue(undefined);
  location.hash = "";
});

afterEach(() => {
  cleanup();
});

// ---------------------------------------------------------------------------
// Basic rendering
// ---------------------------------------------------------------------------

describe("Login — rendering", () => {
  it("renders username/password fields, the submit button and the SSO link", () => {
    render(Login);

    expect(screen.getByLabelText("login.username")).toBeInTheDocument();
    expect(screen.getByLabelText("login.password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "login.submit" })).toBeInTheDocument();

    const sso = screen.getByRole("link", { name: "login.sso" });
    expect(sso).toHaveAttribute("href", "/api/v1/auth/oidc/start");
  });

  it("does not show the CCU hint when auth.ccu.v1 is absent from capabilities", async () => {
    mockInfo.mockResolvedValue({ capabilities: [] });
    render(Login);

    await waitFor(() => expect(mockInfo).toHaveBeenCalled());
    expect(screen.queryByText("login.ccu_hint")).toBeNull();
  });

  it("shows the CCU hint once api.info() reports the auth.ccu.v1 capability", async () => {
    mockInfo.mockResolvedValue({ capabilities: ["auth.ccu.v1"] });
    render(Login);

    await waitFor(() => {
      expect(screen.getByText("login.ccu_hint")).toBeInTheDocument();
    });
  });
});

// ---------------------------------------------------------------------------
// Submit flow
// ---------------------------------------------------------------------------

describe("Login — submit flow", () => {
  it("submits the entered credentials to authStore.login and navigates to the device list", async () => {
    render(Login);

    await fireEvent.input(screen.getByLabelText("login.username"), {
      target: { value: "admin" },
    });
    await fireEvent.input(screen.getByLabelText("login.password"), {
      target: { value: "secret123" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "login.submit" }));

    await waitFor(() => {
      expect(mockLogin).toHaveBeenCalledWith("admin", "secret123");
      expect(location.hash).toBe("#/devices");
    });
  });

  it("does not navigate when authStore.login rejects", async () => {
    mockLogin.mockRejectedValue(new Error("invalid"));

    render(Login);

    await fireEvent.input(screen.getByLabelText("login.username"), {
      target: { value: "admin" },
    });
    await fireEvent.input(screen.getByLabelText("login.password"), {
      target: { value: "wrong" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "login.submit" }));

    await waitFor(() => {
      expect(mockLogin).toHaveBeenCalledWith("admin", "wrong");
    });
    expect(location.hash).toBe("");
  });
});

// ---------------------------------------------------------------------------
// Error banner — rendered from the store's current error state
// ---------------------------------------------------------------------------

describe("Login — error banner", () => {
  it("renders the store's error message inline when authStore.error is already set", () => {
    authError = "auth.error.invalid_credentials";
    render(Login);

    expect(screen.getByText("auth.error.invalid_credentials")).toBeInTheDocument();
  });

  it("renders no error banner when authStore.error is null", () => {
    authError = null;
    render(Login);

    expect(screen.queryByText("auth.error.invalid_credentials")).toBeNull();
  });
});
