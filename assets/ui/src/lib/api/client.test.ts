import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { api } from "./client";

// The daemon mounts a double-submit CSRF guard on the whole REST router
// (internal/auth/csrf.go). The SPA must echo the JS-readable csrf cookie
// back in the X-CSRF-Token header on every mutating request, or the
// daemon answers 403 "token mismatch". These tests lock that contract.

const TOKEN = "csrf-abc-123";

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: "OK",
    text: async () => (body === undefined ? "" : JSON.stringify(body)),
  } as unknown as Response;
}

function headersOf(call: unknown): Record<string, string> {
  const init = (call as [string, RequestInit])[1];
  return (init.headers as Record<string, string>) ?? {};
}

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchMock = vi.fn(async () => jsonResponse({ subject: "admin", role: "admin" }));
  vi.stubGlobal("fetch", fetchMock);
  // node-env vitest has no document; provide a minimal cookie jar.
  vi.stubGlobal("document", { cookie: `openccu_loom_csrf=${TOKEN}` });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("api request — CSRF double-submit", () => {
  it("sends X-CSRF-Token on POST /auth/login from the cookie", async () => {
    await api.login("admin", "secret");
    const headers = headersOf(fetchMock.mock.calls[0]);
    expect(headers["X-CSRF-Token"]).toBe(TOKEN);
    // per-call Content-Type and the default Accept both survive the merge
    expect(headers["Content-Type"]).toBe("application/json");
    expect(headers["Accept"]).toBe("application/json");
  });

  it("does not send X-CSRF-Token on safe GET requests", async () => {
    await api.me();
    const headers = headersOf(fetchMock.mock.calls[0]);
    expect(headers["X-CSRF-Token"]).toBeUndefined();
  });

  it("omits the header when no csrf cookie is present", async () => {
    vi.stubGlobal("document", { cookie: "other=1" });
    await api.logout();
    const headers = headersOf(fetchMock.mock.calls[0]);
    expect(headers["X-CSRF-Token"]).toBeUndefined();
  });

  it("url-decodes a percent-encoded cookie value", async () => {
    vi.stubGlobal("document", { cookie: "openccu_loom_csrf=a%2Bb%3Dc" });
    await api.logout();
    const headers = headersOf(fetchMock.mock.calls[0]);
    expect(headers["X-CSRF-Token"]).toBe("a+b=c");
  });
});

describe("api endpoint paths", () => {
  // triggerBackup must POST to /backups (plural) — the singular /backup is
  // not described in the OpenAPI spec and the validator rejects it with 404.
  it("triggerBackup posts to /api/v1/backups", async () => {
    await api.triggerBackup();
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/backups");
    expect((init.method ?? "GET").toUpperCase()).toBe("POST");
  });
});
