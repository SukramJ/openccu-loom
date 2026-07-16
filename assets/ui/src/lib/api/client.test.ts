import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { api, setUnauthorizedHandler, getHistory, HistoryDisabledError } from "./client";
import type { HistoryBucket } from "./client";

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

describe("api request — 401 session-expiry hook", () => {
  afterEach(() => setUnauthorizedHandler(null));

  it("invokes the unauthorized handler on a 401 from a non-login call", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ detail: "no creds" }, 401));
    const onUnauth = vi.fn();
    setUnauthorizedHandler(onUnauth);
    await expect(api.listInstallModeInterfaces()).rejects.toBeTruthy();
    expect(onUnauth).toHaveBeenCalledOnce();
  });

  it("does not invoke the handler on a 401 from /auth/login", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ detail: "bad creds" }, 401));
    const onUnauth = vi.fn();
    setUnauthorizedHandler(onUnauth);
    await expect(api.login("x", "y")).rejects.toBeTruthy();
    expect(onUnauth).not.toHaveBeenCalled();
  });
});

describe("listPrograms / listSysvars — pagination", () => {
  // Both endpoints page via `page`/`per_page` query params and reply with a
  // bare JSON array (no `{items,total}` wrapper). A client that fires a
  // single unparameterized request only gets whatever the server's default
  // page holds back; page through until a short page signals the end, the
  // same shape devices.svelte.ts already uses for /devices.
  it("listPrograms pages through more than one page's worth of programs", async () => {
    const page1 = Array.from({ length: 200 }, (_, i) => ({ id: `p${i}` }));
    const page2 = Array.from({ length: 30 }, (_, i) => ({ id: `p${200 + i}` }));
    fetchMock
      .mockResolvedValueOnce(jsonResponse(page1))
      .mockResolvedValueOnce(jsonResponse(page2));

    const result = await api.listPrograms();
    expect(result).toHaveLength(230);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("listSysvars pages through more than one page's worth of sysvars", async () => {
    const page1 = Array.from({ length: 200 }, (_, i) => ({ name: `s${i}` }));
    const page2 = Array.from({ length: 5 }, (_, i) => ({ name: `s${200 + i}` }));
    fetchMock
      .mockResolvedValueOnce(jsonResponse(page1))
      .mockResolvedValueOnce(jsonResponse(page2));

    const result = await api.listSysvars();
    expect(result).toHaveLength(205);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("listPrograms stops after a single short page", async () => {
    const page1 = Array.from({ length: 12 }, (_, i) => ({ id: `p${i}` }));
    fetchMock.mockResolvedValueOnce(jsonResponse(page1));

    const result = await api.listPrograms();
    expect(result).toHaveLength(12);
    expect(fetchMock).toHaveBeenCalledTimes(1);
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

describe("api — reliability + values-cache admin wrappers", () => {
  it("getReliability GETs /diagnostics/reliability with no central filter", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    await api.getReliability();
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/diagnostics/reliability");
    expect((init.method ?? "GET").toUpperCase()).toBe("GET");
  });

  it("getReliability scopes to ?central= when given", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    await api.getReliability("alpha");
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/diagnostics/reliability?central=alpha");
  });

  it("getValuesCacheStats GETs /admin/values-cache/stats", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ rows: 0 }));
    await api.getValuesCacheStats();
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/admin/values-cache/stats");
    expect((init.method ?? "GET").toUpperCase()).toBe("GET");
  });

  it("resetValuesCache() with no address POSTs the global reset route", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 204));
    await api.resetValuesCache();
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/admin/values-cache/reset");
    expect((init.method ?? "GET").toUpperCase()).toBe("POST");
  });

  it("resetValuesCache(address) POSTs the per-device reset route", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 204));
    await api.resetValuesCache("00021BE9957782");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/00021BE9957782/values-cache/reset");
    expect((init.method ?? "GET").toUpperCase()).toBe("POST");
  });
});

describe("api paramset writes — edit-lock token header", () => {
  it("putParamset sends X-Edit-Token when a token is held", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.putParamset("00021BE9957782:4", "MASTER", { TEMPERATURE: 21 }, "tok-abc");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/00021BE9957782%3A4/paramsets/MASTER");
    expect((init.method ?? "GET").toUpperCase()).toBe("PUT");
    const headers = headersOf(fetchMock.mock.calls[0]);
    expect(headers["X-Edit-Token"]).toBe("tok-abc");
    expect(headers["Content-Type"]).toBe("application/json");
  });

  it("putParamset omits X-Edit-Token when no token is held", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.putParamset("00021BE9957782:4", "MASTER", { TEMPERATURE: 21 });
    const headers = headersOf(fetchMock.mock.calls[0]);
    expect(headers["X-Edit-Token"]).toBeUndefined();
    expect(headers["Content-Type"]).toBe("application/json");
  });

  it("putLinkParamset sends X-Edit-Token when a token is held", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.putLinkParamset(
      "00021BE9957782:4",
      "00021BE9957783:1",
      { LEVEL: 1 },
      "tok-xyz",
    );
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    // Pins the contract path (assets/openapi.yaml /devices/{addr}/link-ps/{peer});
    // the daemon decodes the percent-encoded segments centrally.
    expect(url).toBe(
      "/api/v1/devices/00021BE9957782%3A4/link-ps/00021BE9957783%3A1",
    );
    expect((init.method ?? "GET").toUpperCase()).toBe("PUT");
    const headers = headersOf(fetchMock.mock.calls[0]);
    expect(headers["X-Edit-Token"]).toBe("tok-xyz");
  });
});

describe("getHistory", () => {
  const BASE_PARAMS = {
    central: "ccu1",
    interfaceId: "ccu1-HmIP-RF",
    channel: "ABC001:4",
    parameter: "ACTUAL_TEMPERATURE",
    from: "2026-06-01T00:00:00Z",
    to: "2026-06-01T01:00:00Z",
  };

  const BUCKET: HistoryBucket = {
    ts: "2026-06-01T00:00:00Z",
    avg: 21.5,
    min: 20.0,
    max: 23.0,
    count: 4,
  };

  it("builds the correct querystring with interface_id (not interfaceId)", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([BUCKET]));
    await getHistory(BASE_PARAMS);
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    const qs = new URL(url, "http://x").searchParams;
    expect(qs.get("central")).toBe("ccu1");
    expect(qs.get("interface_id")).toBe("ccu1-HmIP-RF");
    expect(qs.get("channel")).toBe("ABC001:4");
    expect(qs.get("parameter")).toBe("ACTUAL_TEMPERATURE");
    expect(qs.get("from")).toBe("2026-06-01T00:00:00Z");
    expect(qs.get("to")).toBe("2026-06-01T01:00:00Z");
    // interfaceId must NOT appear in the URL as-is (camelCase)
    expect(qs.has("interfaceId")).toBe(false);
  });

  it("includes optional buckets parameter when provided", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([BUCKET]));
    await getHistory({ ...BASE_PARAMS, buckets: 50 });
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    const qs = new URL(url, "http://x").searchParams;
    expect(qs.get("buckets")).toBe("50");
  });

  it("omits buckets parameter when not provided", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([BUCKET]));
    await getHistory(BASE_PARAMS);
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    const qs = new URL(url, "http://x").searchParams;
    expect(qs.has("buckets")).toBe(false);
  });

  it("parses the bucket array and returns typed HistoryBucket[]", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([BUCKET]));
    const result = await getHistory(BASE_PARAMS);
    expect(result).toHaveLength(1);
    expect(result[0].avg).toBe(21.5);
    expect(result[0].min).toBe(20.0);
    expect(result[0].max).toBe(23.0);
    expect(result[0].count).toBe(4);
    expect(result[0].ts).toBe("2026-06-01T00:00:00Z");
  });

  it("returns an empty array when the server returns an empty array", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    const result = await getHistory(BASE_PARAMS);
    expect(result).toEqual([]);
  });

  it("throws HistoryDisabledError on 404 (feature off)", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ detail: "history feature disabled" }, 404),
    );
    await expect(getHistory(BASE_PARAMS)).rejects.toBeInstanceOf(HistoryDisabledError);
  });

  it("re-throws ApiError for non-404 errors (e.g. 400 bad request)", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ detail: "missing param" }, 400),
    );
    const err = await getHistory(BASE_PARAMS).catch((e: unknown) => e);
    expect(err).not.toBeInstanceOf(HistoryDisabledError);
    expect((err as Error).message).toMatch(/400/);
  });

  it("sends the request as GET without a CSRF header", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([BUCKET]));
    await getHistory(BASE_PARAMS);
    const headers = headersOf(fetchMock.mock.calls[0]);
    expect(headers["X-CSRF-Token"]).toBeUndefined();
  });
});
