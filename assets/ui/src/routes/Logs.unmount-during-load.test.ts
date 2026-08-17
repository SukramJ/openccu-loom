// @vitest-environment happy-dom
//
// onMount is async and only calls openStream() after `await
// Promise.all([api.getLogs(...), api.getDefaultLogLevel()])` resolves.
// Svelte runs onDestroy synchronously at unmount, but the promise
// continuation of an async onMount keeps running afterwards — so
// leaving the Logs view before that seed fetch settles used to open a
// live, self-reconnecting EventSource that nothing owned, streaming
// into a component that no longer exists.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";

const mockGetLogs = vi.fn();
const mockGetDefaultLogLevel = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    getLogs: (...args: unknown[]) => mockGetLogs(...args),
    getDefaultLogLevel: (...args: unknown[]) => mockGetDefaultLogLevel(...args),
    setDefaultLogLevel: vi.fn(),
    logsStreamUrl: (p: { since?: number; minLevel?: string }) => {
      const qs = new URLSearchParams();
      if (p.since !== undefined) qs.set("since", String(p.since));
      if (p.minLevel) qs.set("min_level", p.minLevel);
      return `/api/v1/diagnostics/logs/stream?${qs.toString()}`;
    },
    logsDownloadUrl: () => "/api/v1/diagnostics/logs?download=1",
    me: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
  setUnauthorizedHandler: vi.fn(),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn(), warn: vi.fn() },
}));

import Logs from "./Logs.svelte";

// Same minimal stand-in as Logs.resync.test.ts.
class MockEventSource {
  static instances: MockEventSource[] = [];
  url: string;
  closed = false;
  private listeners = new Map<string, ((ev: unknown) => void)[]>();

  constructor(url: string, _init?: unknown) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, cb: (ev: unknown) => void) {
    const list = this.listeners.get(type) ?? [];
    list.push(cb);
    this.listeners.set(type, list);
  }

  removeEventListener() {
    // unused by the component
  }

  close() {
    this.closed = true;
  }
}

beforeEach(() => {
  vi.clearAllMocks();
  MockEventSource.instances = [];
  vi.stubGlobal("EventSource", MockEventSource);
  mockGetDefaultLogLevel.mockResolvedValue("info");
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Logs — unmount while the seed fetch is in flight", () => {
  it("never opens an EventSource once the component is gone", async () => {
    let resolveSeed: (v: { last_seq: number; records: unknown[] }) => void = () => {};
    const seed = new Promise((resolve) => {
      resolveSeed = resolve;
    });
    mockGetLogs.mockReturnValueOnce(seed);

    const { unmount } = render(Logs);

    // The operator navigates away before the seed request ever answers.
    unmount();

    // The in-flight fetch now settles, resuming onMount's continuation.
    resolveSeed({ last_seq: 10, records: [] });
    for (let i = 0; i < 8; i++) await Promise.resolve();

    expect(MockEventSource.instances).toHaveLength(0);
  });
});
