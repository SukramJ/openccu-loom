// @vitest-environment happy-dom
//
// Pins the reconnect resync in Logs.svelte: after a daemon restart the
// process' own log-seq counter restarts at 1, so appendRecords' `seq >
// lastSeq` filter (lastSeq is a monotonic high-water mark from the old
// process) would otherwise drop every post-restart record forever while
// the connection badge keeps reading "live". The fix probes the
// server's own last_seq on every reconnect and, when it is lower than
// what the SPA remembers, drops the stale `since=` pin and reopens.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen } from "@testing-library/svelte";
import type { LogRecord } from "$lib/api/types";

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
  // Pulled in transitively via LogLevelsPanel → authStore.
  setUnauthorizedHandler: vi.fn(),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn(), warn: vi.fn() },
}));

// A minimal EventSource stand-in. The production code only uses
// addEventListener + close, so that is all this implements. Every
// `new EventSource(...)` call is recorded so the test can drive
// (and inspect) each connection the component opens in turn.
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

  emit(type: string, detail?: unknown) {
    for (const cb of this.listeners.get(type) ?? []) cb(detail);
  }
}

function logEvent(r: LogRecord) {
  return { data: JSON.stringify(r) };
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

describe("Logs — resync after a daemon restart", () => {
  it("resumes the tail once the daemon's own seq counter has reset", async () => {
    mockGetLogs.mockResolvedValueOnce({ last_seq: 100, records: [] });

    const Logs = (await import("./Logs.svelte")).default;
    render(Logs);

    await waitFor(() => expect(MockEventSource.instances).toHaveLength(1));
    const first = MockEventSource.instances[0];
    expect(first.url).toContain("since=100");

    // Initial connect — just the mount, not a reconnect.
    first.emit("open");

    // A record arrives from the pre-restart process. warn+ so it shows
    // up in the default aggregated view.
    first.emit(
      "log",
      logEvent({ seq: 101, time: "2026-08-16T10:00:00Z", level: "warn", msg: "before restart" }),
    );
    await waitFor(() => expect(screen.getByText("before restart")).toBeInTheDocument());

    // The daemon restarts: the connection drops...
    first.emit("error");

    // ...and the probe this reconnect triggers sees a fresh process
    // whose ring only holds a handful of records so far.
    mockGetLogs.mockResolvedValueOnce({ last_seq: 3, records: [] });

    // The browser's own retry succeeds on the same EventSource object.
    first.emit("open");

    await waitFor(() => expect(mockGetLogs).toHaveBeenCalledWith({ limit: 1, minLevel: "debug" }));
    await waitFor(() => expect(MockEventSource.instances).toHaveLength(2));
    expect(first.closed).toBe(true);

    const second = MockEventSource.instances[1];
    // The stale since= pin (100) is gone — the new connection starts
    // from scratch so the new process' own backfill can be delivered.
    expect(second.url).toContain("since=0");

    // A post-restart record, numbered from the new process' own
    // counter, must now actually reach the view...
    second.emit(
      "log",
      logEvent({ seq: 1, time: "2026-08-16T10:05:00Z", level: "warn", msg: "after restart" }),
    );
    await waitFor(() => expect(screen.getByText("after restart")).toBeInTheDocument());

    // ...and the pre-restart line must still be there, exactly once.
    expect(screen.getAllByText("before restart")).toHaveLength(1);
  // The first test in this file pays the cold-transform cost of the
  // shared icon barrel that Card/Badge/ErrorState pull in; subsequent
  // runs are fast, but a cold run needs more than the 5s default.
  }, 150000);

  it("does not reopen the stream on an ordinary same-process reconnect", async () => {
    mockGetLogs.mockResolvedValueOnce({ last_seq: 50, records: [] });

    const Logs = (await import("./Logs.svelte")).default;
    render(Logs);

    await waitFor(() => expect(MockEventSource.instances).toHaveLength(1));
    const first = MockEventSource.instances[0];
    first.emit("open");
    first.emit("error");

    // Same process: its last_seq only ever grows.
    mockGetLogs.mockResolvedValueOnce({ last_seq: 55, records: [] });
    first.emit("open");

    await waitFor(() => expect(mockGetLogs).toHaveBeenCalledWith({ limit: 1, minLevel: "debug" }));
    // No reset detected — the original connection is left alone.
    expect(MockEventSource.instances).toHaveLength(1);
    expect(first.closed).toBe(false);
  }, 150000);
});
