// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("$lib/api/client", () => ({
  api: {
    me: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
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
  setUnauthorizedHandler: vi.fn(),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/stores/events.svelte", () => ({
  shutdown: vi.fn(),
}));

import { api } from "$lib/api/client";
import { shutdown as shutdownEventPump } from "$lib/stores/events.svelte";
import { authStore } from "./auth.svelte";

const meMock = api.me as ReturnType<typeof vi.fn>;
const loginMock = api.login as ReturnType<typeof vi.fn>;
const logoutMock = api.logout as ReturnType<typeof vi.fn>;
const shutdownMock = shutdownEventPump as ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();
});

describe("authStore.probe", () => {
  it("sets identity and authenticated=true on a successful probe", async () => {
    meMock.mockResolvedValueOnce({ subject: "admin", role: "admin" });
    await authStore.probe();
    expect(authStore.authenticated).toBe(true);
    expect(authStore.identity?.subject).toBe("admin");
    expect(authStore.checking).toBe(false);
    expect(authStore.error).toBeNull();
  });

  it("clears identity (unauthenticated) when probe returns 401", async () => {
    // first authenticate
    meMock.mockResolvedValueOnce({ subject: "admin", role: "admin" });
    await authStore.probe();
    expect(authStore.authenticated).toBe(true);

    // session expires → 401
    const { ApiError: AE } = await import("$lib/api/client");
    meMock.mockRejectedValueOnce(new AE(401, {}, "unauthorized"));
    await authStore.probe();
    expect(authStore.authenticated).toBe(false);
    expect(authStore.identity).toBeNull();
    expect(authStore.checking).toBe(false);
  });
});

describe("authStore.login", () => {
  it("populates identity on a successful login", async () => {
    loginMock.mockResolvedValueOnce({ subject: "user1", role: "user" });
    await authStore.login("user1", "pass");
    expect(authStore.authenticated).toBe(true);
    expect(authStore.identity?.subject).toBe("user1");
  });

  it("sets error and re-throws on a 401 login failure", async () => {
    // ensure we start unauthenticated so the assertion is meaningful
    logoutMock.mockResolvedValueOnce(undefined);
    await authStore.logout();

    const { ApiError: AE } = await import("$lib/api/client");
    loginMock.mockRejectedValueOnce(new AE(401, {}, "bad credentials"));
    await expect(authStore.login("bad", "creds")).rejects.toBeTruthy();
    expect(authStore.error).toBe("auth.error.invalid_credentials");
    expect(authStore.authenticated).toBe(false);
  });
});

describe("authStore.logout", () => {
  it("clears identity after logout even when the server call succeeds", async () => {
    loginMock.mockResolvedValueOnce({ subject: "admin", role: "admin" });
    await authStore.login("admin", "secret");
    expect(authStore.authenticated).toBe(true);

    logoutMock.mockResolvedValueOnce(undefined);
    await authStore.logout();
    expect(authStore.authenticated).toBe(false);
    expect(authStore.identity).toBeNull();
  });

  it("clears identity even when the logout server call throws", async () => {
    loginMock.mockResolvedValueOnce({ subject: "admin", role: "admin" });
    await authStore.login("admin", "secret");
    expect(authStore.authenticated).toBe(true);

    logoutMock.mockRejectedValueOnce(new Error("network error"));
    await authStore.logout();
    // The store's logout catches errors and still clears identity.
    expect(authStore.authenticated).toBe(false);
  });

  it("shuts down the shared WS pump so an idle tab does not keep reconnecting a rejected socket", async () => {
    loginMock.mockResolvedValueOnce({ subject: "admin", role: "admin" });
    await authStore.login("admin", "secret");

    logoutMock.mockResolvedValueOnce(undefined);
    await authStore.logout();
    expect(shutdownMock).toHaveBeenCalledTimes(1);
  });
});

describe("authStore.expire", () => {
  it("stays authenticated when the re-probe still succeeds (HA Ingress passthrough)", async () => {
    loginMock.mockResolvedValueOnce({ subject: "admin", role: "admin" });
    await authStore.login("admin", "secret");
    expect(authStore.authenticated).toBe(true);

    // A 401 fired expire(), but /auth/me re-authenticates (the Supervisor
    // Ingress passthrough re-injects the admin identity) → identity survives.
    meMock.mockResolvedValueOnce({ subject: "ha-ingress", role: "admin" });
    await authStore.expire();
    expect(meMock).toHaveBeenCalledTimes(1);
    expect(authStore.authenticated).toBe(true);
    expect(authStore.identity?.subject).toBe("ha-ingress");
    // The session survived — the WS pump must stay untouched.
    expect(shutdownMock).not.toHaveBeenCalled();
  });

  it("transitions authenticated→unauthenticated when the re-probe also 401s", async () => {
    loginMock.mockResolvedValueOnce({ subject: "admin", role: "admin" });
    await authStore.login("admin", "secret");
    expect(authStore.authenticated).toBe(true);

    const { ApiError: AE } = await import("$lib/api/client");
    meMock.mockRejectedValueOnce(new AE(401, {}, "unauthorized"));
    await authStore.expire();
    expect(authStore.authenticated).toBe(false);
    expect(authStore.identity).toBeNull();
    // error carries the i18n key (mocked to return the key itself)
    expect(authStore.error).toBe("api.error.unauthorized");
    // The session is genuinely gone — shut down the WS pump so an idle
    // tab does not keep reconnecting a socket the daemon now rejects.
    expect(shutdownMock).toHaveBeenCalledTimes(1);
  });

  it("is a no-op when already unauthenticated (no re-probe)", async () => {
    // ensure we start unauthenticated
    logoutMock.mockResolvedValueOnce(undefined);
    await authStore.logout();
    expect(authStore.authenticated).toBe(false);
    shutdownMock.mockClear();

    // calling expire when already logged-out must not throw, re-probe, or set error
    const prevError = authStore.error;
    await authStore.expire();
    expect(authStore.authenticated).toBe(false);
    expect(meMock).not.toHaveBeenCalled();
    // expire is guarded: no-op when identity is already null
    expect(authStore.error).toBe(prevError);
    expect(shutdownMock).not.toHaveBeenCalled();
  });
});
