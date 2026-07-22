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

  // include_internal defaults to false (system programs hidden, matching the
  // CCU WebUI default) and is only forced true on an explicit caller request.
  it("listPrograms defaults to include_internal=false", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    await api.listPrograms();
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/programs?page=1&per_page=200&include_internal=false");
  });

  it("listPrograms(true) requests include_internal=true", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    await api.listPrograms(true);
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/programs?page=1&per_page=200&include_internal=true");
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

  // restoreDeviceConfig re-transmits the stored configuration after a
  // factory reset (admin-only; HmIP-RF / BidCos-RF only).
  it("restoreDeviceConfig posts to /devices/{addr}/config/restore", async () => {
    await api.restoreDeviceConfig("HmIP-X");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/HmIP-X/config/restore");
    expect((init.method ?? "GET").toUpperCase()).toBe("POST");
  });

  it("restoreDeviceConfig percent-encodes the device address", async () => {
    await api.restoreDeviceConfig("ABC 123");
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/ABC%20123/config/restore");
  });
});

describe("api — deleteDevice reset/force query flags", () => {
  it("DELETEs without a query string when no options are given", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.deleteDevice("0001ABCD");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD");
    expect((init.method ?? "GET").toUpperCase()).toBe("DELETE");
  });

  it("omits the query string when reset and force are both explicitly false", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.deleteDevice("0001ABCD", { reset: false, force: false });
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD");
  });

  it("appends only reset=true when force is omitted", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.deleteDevice("0001ABCD", { reset: true });
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD?reset=true");
  });

  it("appends only force=true when reset is omitted", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.deleteDevice("0001ABCD", { force: true });
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD?force=true");
  });

  it("appends both flags when reset and force are true", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.deleteDevice("0001ABCD", { reset: true, force: true });
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD?reset=true&force=true");
  });

  it("percent-encodes the address ahead of the query flags", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.deleteDevice("0001:2", { reset: true });
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001%3A2?reset=true");
  });
});

// central-links (createCentralLinks/removeCentralLinks) accept an optional
// `channel` scope: an empty/omitted channel touches the whole device
// (unchanged historical behaviour), a channel address scopes the call to
// that single channel exactly like the CCU channel-config dialog. Both
// verbs build the query string identically, so cover POST and DELETE.
describe("api — central links channel scoping", () => {
  it("centralLinksStatus GETs the device's status with no query string", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ supported: true }));
    await api.centralLinksStatus("0001ABCD");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD/central-links");
    expect((init.method ?? "GET").toUpperCase()).toBe("GET");
  });

  it("createCentralLinks POSTs without a query string when no channel is given", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ touched: 2, skipped: 0, failed: 0 }, 202));
    await api.createCentralLinks("0001ABCD");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD/central-links");
    expect((init.method ?? "GET").toUpperCase()).toBe("POST");
  });

  it("createCentralLinks appends ?channel= when a channel address is given", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ touched: 1, skipped: 0, failed: 0 }, 202));
    await api.createCentralLinks("0001ABCD", "0001ABCD:2");
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD/central-links?channel=0001ABCD%3A2");
  });

  it("removeCentralLinks DELETEs without a query string when no channel is given", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ touched: 2, skipped: 0, failed: 0 }, 202));
    await api.removeCentralLinks("0001ABCD");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD/central-links");
    expect((init.method ?? "GET").toUpperCase()).toBe("DELETE");
  });

  it("removeCentralLinks appends ?channel= when a channel address is given", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ touched: 1, skipped: 0, failed: 0 }, 202));
    await api.removeCentralLinks("0001ABCD", "0001ABCD:2");
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD/central-links?channel=0001ABCD%3A2");
  });

  it("treats an empty-string channel the same as an omitted channel", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ touched: 2, skipped: 0, failed: 0 }, 202));
    await api.createCentralLinks("0001ABCD", "");
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001ABCD/central-links");
  });
});

// Guided device replace: listReplaceCandidates is a read-only GET scoped
// by an optional `central` query param; replaceDevice is the admin-only
// POST that swaps a paired device for the new one, carrying old_address
// (and central, when given) in the JSON body.
describe("api — device replace workflow", () => {
  it("listReplaceCandidates GETs the replace-candidates endpoint and unwraps the envelope", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ candidates: [{ address: "OLD001", model: "HM-Sec-SC", model_matches: true }] }),
    );
    const candidates = await api.listReplaceCandidates("NEW001", "ccu");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/NEW001/replace-candidates?central=ccu");
    expect((init.method ?? "GET").toUpperCase()).toBe("GET");
    expect(candidates).toEqual([{ address: "OLD001", model: "HM-Sec-SC", model_matches: true }]);
  });

  it("listReplaceCandidates omits the query string when no central is given", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ candidates: [] }));
    await api.listReplaceCandidates("NEW001");
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/NEW001/replace-candidates");
  });

  it("replaceDevice POSTs old_address and central in the JSON body", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ status: "replacing", old_address: "OLD001", new_address: "NEW001", central: "ccu" }, 202),
    );
    await api.replaceDevice("NEW001", "OLD001", "ccu");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/NEW001/replace");
    expect((init.method ?? "GET").toUpperCase()).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({ old_address: "OLD001", central: "ccu" });
  });

  it("replaceDevice omits central from the body when not given", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ status: "replacing", old_address: "OLD001", new_address: "NEW001" }, 202),
    );
    await api.replaceDevice("NEW001", "OLD001");
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ old_address: "OLD001" });
  });
});

// acceptInboxDevice's config body is optional and must stay backward
// compatible: an empty/omitted config keeps the historical "accept only"
// POST with no body, so the daemon's `io.EOF` fast-path is exercised. Only
// a config with at least one defined field switches to a JSON body.
describe("api — acceptInboxDevice optional config body", () => {
  it("POSTs with no body when config is omitted", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.acceptInboxDevice("0009ABCD", "");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0009ABCD/accept");
    expect((init.method ?? "GET").toUpperCase()).toBe("POST");
    expect(init.body).toBeUndefined();
  });

  it("POSTs with no body when config is an empty object", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.acceptInboxDevice("0009ABCD", "", {});
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.body).toBeUndefined();
  });

  it("scopes to ?central= when a central is given", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.acceptInboxDevice("0009ABCD", "alpha");
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0009ABCD/accept?central=alpha");
  });

  it("sends a JSON body with only the name when just a name is given", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.acceptInboxDevice("0009ABCD", "", { name: "Kitchen Switch" });
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ name: "Kitchen Switch" });
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });

  it("sends every supplied field, including an explicit empty rooms array", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.acceptInboxDevice("0009ABCD", "", {
      name: "Kitchen Switch",
      include_channels: true,
      rooms: [],
      functions: ["Lights"],
    });
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({
      name: "Kitchen Switch",
      include_channels: true,
      rooms: [],
      functions: ["Lights"],
    });
  });
});

// setChannelRooms / setChannelFunctions PATCH the same channel resource as
// renameChannel, but with a `rooms`/`functions` body instead of `name` —
// the channel-level twin of the device-level room/function assignment.
describe("api — setChannelRooms / setChannelFunctions", () => {
  it("setChannelRooms PATCHes the channel with a rooms body", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.setChannelRooms("HmIP-X", 2, ["Wohnzimmer"]);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/HmIP-X/channels/2");
    expect((init.method ?? "GET").toUpperCase()).toBe("PATCH");
    expect(JSON.parse(init.body as string)).toEqual({ rooms: ["Wohnzimmer"] });
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });

  it("setChannelRooms sends an explicit empty array to clear the assignment", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.setChannelRooms("HmIP-X", 2, []);
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ rooms: [] });
  });

  it("setChannelFunctions PATCHes the channel with a functions body", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.setChannelFunctions("HmIP-X", 2, ["Licht"]);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/HmIP-X/channels/2");
    expect((init.method ?? "GET").toUpperCase()).toBe("PATCH");
    expect(JSON.parse(init.body as string)).toEqual({ functions: ["Licht"] });
  });

  it("setChannelFunctions sends an explicit empty array to clear the assignment", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.setChannelFunctions("HmIP-X", 2, []);
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ functions: [] });
  });

  it("percent-encodes the device address in the channel path", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    await api.setChannelRooms("0001:2", 3, ["Küche"]);
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001%3A2/channels/3");
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

  it("getParamset GETs the raw paramset with no edit-lock header", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ AES_ACTIVE: false }));
    const result = await api.getParamset("00021BE9957782:4", "MASTER");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/00021BE9957782%3A4/paramsets/MASTER");
    expect((init.method ?? "GET").toUpperCase()).toBe("GET");
    const headers = headersOf(fetchMock.mock.calls[0]);
    expect(headers["X-Edit-Token"]).toBeUndefined();
    expect(result).toEqual({ AES_ACTIVE: false });
  });

  it("getParamset percent-encodes the channel address for the VALUES paramset", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ STATE: true }));
    await api.getParamset("0001:2", "VALUES");
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001%3A2/paramsets/VALUES");
  });

  it("getParamset propagates the daemon error on a failed read", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ detail: "not found" }, 404));
    await expect(api.getParamset("0001ABCD:1", "MASTER")).rejects.toBeTruthy();
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

describe("api — determineParameter (MASTER editor 'Determine' button)", () => {
  it("POSTs the parameter name to the channel-scoped determine route (happy path)", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ value: 21.5 }));
    const result = await api.determineParameter("00021BE9957782", 4, "MASTER", "TEMPERATURE");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(
      "/api/v1/devices/00021BE9957782/channels/4/paramsets/MASTER/determine",
    );
    expect((init.method ?? "GET").toUpperCase()).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({ parameter: "TEMPERATURE" });
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe(
      "application/json",
    );
    expect(result).toEqual({ value: 21.5 });
  });

  it("percent-encodes the device address (edge case: address carrying reserved characters)", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ value: 1 }));
    await api.determineParameter("0001:2", 1, "LINK", "ON_TIME");
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/devices/0001%3A2/channels/1/paramsets/LINK/determine");
  });

  it("propagates the daemon error on a failed determine (error path)", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ detail: "ccu unreachable" }, 502));
    await expect(
      api.determineParameter("00021BE9957782", 4, "MASTER", "TEMPERATURE"),
    ).rejects.toBeTruthy();
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

describe("api.setInstallModeInterface — LOCAL teach-in body shape", () => {
  // setInstallModeInterface POSTs, then re-fetches the interface list; every
  // test here mocks both responses so the follow-up GET doesn't throw.
  beforeEach(() => {
    fetchMock.mockResolvedValueOnce(jsonResponse(undefined, 202));
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
  });

  it("includes sgtin and key in the body when a local arg is given", async () => {
    await api.setInstallModeInterface("HmIP-RF", true, 300, undefined, {
      sgtin: "3014-F711-A061-A7D5-6989-2A67",
      key: "0110C8531D0952D8D73E1194E95B5F19",
    });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/install-mode/interfaces");
    const body = JSON.parse(init.body as string);
    expect(body.sgtin).toBe("3014-F711-A061-A7D5-6989-2A67");
    expect(body.key).toBe("0110C8531D0952D8D73E1194E95B5F19");
    expect(body.interface).toBe("HmIP-RF");
    expect(body.active).toBe(true);
    expect(body.seconds).toBe(300);
  });

  it("omits sgtin and key from the body when no local arg is given", async () => {
    await api.setInstallModeInterface("HmIP-RF", true, 60);
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body).not.toHaveProperty("sgtin");
    expect(body).not.toHaveProperty("key");
  });

  it("omits sgtin and key from the body for a plain device_address teach-in", async () => {
    await api.setInstallModeInterface("HmIP-RF", true, 60, "AABBCCDD:1");
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.device_address).toBe("AABBCCDD:1");
    expect(body).not.toHaveProperty("sgtin");
    expect(body).not.toHaveProperty("key");
  });
});
