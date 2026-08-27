// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

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
import { authStore, EXPIRY_WARNING_MS } from "./auth.svelte";

const meMock = api.me as ReturnType<typeof vi.fn>;
const loginMock = api.login as ReturnType<typeof vi.fn>;
const logoutMock = api.logout as ReturnType<typeof vi.fn>;
const shutdownMock = shutdownEventPump as ReturnType<typeof vi.fn>;

/** An RFC3339 instant `ms` from now, the shape the daemon sends. */
function inMs(ms: number): string {
  return new Date(Date.now() + ms).toISOString();
}

/** Return the store to a known logged-out state between cases. */
async function reset() {
  logoutMock.mockResolvedValue(undefined);
  await authStore.logout();
  vi.clearAllMocks();
}

beforeEach(async () => {
  vi.useFakeTimers();
  await reset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("credential deadline", () => {
  it("adopts expires_at from the identity probe", async () => {
    meMock.mockResolvedValueOnce({
      subject: "markus",
      role: "admin",
      scheme: "session",
      expires_at: inMs(6 * 60 * 60 * 1000),
    });
    await authStore.probe();

    expect(authStore.expiresAt).toBeInstanceOf(Date);
    expect(authStore.msToExpiry).toBeGreaterThan(0);
    expect(authStore.expiringSoon).toBe(false);
  });

  it("adopts the deadline from the login response too", async () => {
    loginMock.mockResolvedValueOnce({
      subject: "markus",
      role: "admin",
      scheme: "session",
      expires_at: inMs(12 * 60 * 60 * 1000),
    });
    await authStore.login("markus", "secret");

    expect(authStore.expiresAt).toBeInstanceOf(Date);
    expect(authStore.expiringSoon).toBe(false);
  });

  // The negative control for every case above: a deployment whose
  // credential has no server-side expiry — HA Ingress, Basic auth, an
  // unbounded bearer token — must never see a countdown or a banner. If
  // the store invented a deadline here, the warning would fire on a
  // credential that is not going anywhere.
  it("reports no deadline when expires_at is absent", async () => {
    meMock.mockResolvedValueOnce({
      subject: "ha-ingress",
      role: "admin",
      scheme: "ingress",
    });
    await authStore.probe();

    expect(authStore.authenticated).toBe(true);
    expect(authStore.expiresAt).toBeNull();
    expect(authStore.msToExpiry).toBeNull();
    expect(authStore.expiringSoon).toBe(false);
  });

  it("treats an unparseable expires_at as no deadline", async () => {
    meMock.mockResolvedValueOnce({
      subject: "markus",
      role: "admin",
      expires_at: "not-a-timestamp",
    });
    await authStore.probe();

    expect(authStore.authenticated).toBe(true);
    expect(authStore.expiresAt).toBeNull();
    expect(authStore.expiringSoon).toBe(false);
  });
});

describe("expiry warning", () => {
  it("raises expiringSoon once the deadline enters the warning window", async () => {
    meMock.mockResolvedValueOnce({
      subject: "markus",
      role: "admin",
      expires_at: inMs(EXPIRY_WARNING_MS + 5 * 60 * 1000),
    });
    await authStore.probe();
    expect(authStore.expiringSoon).toBe(false);

    // Cross into the window; the 30s ticker recomputes.
    await vi.advanceTimersByTimeAsync(6 * 60 * 1000);
    expect(authStore.expiringSoon).toBe(true);
    expect(authStore.authenticated).toBe(true);
  });

  it("hands over to the logged-out state at the deadline", async () => {
    meMock.mockResolvedValueOnce({
      subject: "markus",
      role: "admin",
      expires_at: inMs(2 * 60 * 1000),
    });
    await authStore.probe();
    expect(authStore.authenticated).toBe(true);

    await vi.advanceTimersByTimeAsync(3 * 60 * 1000);

    expect(authStore.authenticated).toBe(false);
    expect(authStore.identity).toBeNull();
    expect(authStore.expiresAt).toBeNull();
    expect(authStore.expiringSoon).toBe(false);
    expect(authStore.error).toBe("api.error.unauthorized");
    // The daemon closes the socket at the same instant, so a pump left
    // running would reconnect against a socket that can never come back.
    expect(shutdownMock).toHaveBeenCalled();
  });

  it("stops ticking after logout so a stale deadline cannot fire", async () => {
    meMock.mockResolvedValueOnce({
      subject: "markus",
      role: "admin",
      expires_at: inMs(2 * 60 * 1000),
    });
    await authStore.probe();

    logoutMock.mockResolvedValueOnce(undefined);
    await authStore.logout();
    shutdownMock.mockClear();

    await vi.advanceTimersByTimeAsync(5 * 60 * 1000);

    expect(authStore.expiresAt).toBeNull();
    expect(authStore.msToExpiry).toBeNull();
    // The deadline elapsed while logged out: no second teardown, and above
    // all no error banner on the login screen the operator just asked for.
    expect(shutdownMock).not.toHaveBeenCalled();
  });

  it("drops the old deadline when a re-probe returns a fresh credential", async () => {
    meMock.mockResolvedValueOnce({
      subject: "markus",
      role: "admin",
      expires_at: inMs(60 * 1000),
    });
    await authStore.probe();
    expect(authStore.expiringSoon).toBe(true);

    // Under Ingress the re-probe re-authenticates and can hand back a
    // credential with a different lifetime — or none at all.
    meMock.mockResolvedValueOnce({
      subject: "markus",
      role: "admin",
      scheme: "ingress",
    });
    await authStore.expire();

    expect(authStore.authenticated).toBe(true);
    expect(authStore.expiresAt).toBeNull();
    expect(authStore.expiringSoon).toBe(false);

    await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
    expect(authStore.authenticated).toBe(true);
  });
});
