// @vitest-environment happy-dom
//
// Pins the `loadGeneration` guard in AuditLog.svelte's `load()`: the
// `$effect` re-triggers a fetch on every since/until/page change, and a
// slower older request must never overwrite what a newer filter's
// (possibly faster-resolving) response produced.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent } from "@testing-library/svelte";

const mockListAudit = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    listAudit: (...args: unknown[]) => mockListAudit(...args),
    auditDownloadUrl: () => "/api/v1/audit?format=csv",
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, _params?: unknown) => key,
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en" },
}));

import AuditLog from "./AuditLog.svelte";

// Deferred promise helper — lets the test control exactly when a given
// api.listAudit(...) call resolves, independent of call order.
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

afterEach(() => cleanup());

describe("AuditLog — server-side filter load race", () => {
  it("keeps the since+until result even if the earlier since-only response resolves later", async () => {
    const onMountCall = Promise.resolve([]);
    const sinceOnly = deferred<{ id: number; timestamp: string; user: string; action: string; note: string }[]>();
    const sinceAndUntil = deferred<{ id: number; timestamp: string; user: string; action: string; note: string }[]>();

    mockListAudit
      .mockReturnValueOnce(onMountCall)
      .mockImplementationOnce(() => sinceOnly.promise)
      .mockImplementationOnce(() => sinceAndUntil.promise);

    render(AuditLog);
    await waitFor(() => expect(mockListAudit).toHaveBeenCalledTimes(1));

    const sinceInput = screen.getByLabelText("audit.from") as HTMLInputElement;
    await fireEvent.input(sinceInput, { target: { value: "2026-07-01T00:00" } });
    await waitFor(() => expect(mockListAudit).toHaveBeenCalledTimes(2));

    const untilInput = screen.getByLabelText("audit.to") as HTMLInputElement;
    await fireEvent.input(untilInput, { target: { value: "2026-07-02T00:00" } });
    await waitFor(() => expect(mockListAudit).toHaveBeenCalledTimes(3));

    // The narrower (since+until) request resolves first...
    sinceAndUntil.resolve([
      { id: 1, timestamp: "2026-07-01T12:00:00Z", user: "markus", action: "x", note: "since_and_until" },
    ]);
    await waitFor(() => {
      expect(screen.getByText("since_and_until")).toBeInTheDocument();
    });

    // ...then the stale since-only request resolves late. It must be
    // discarded — it must never overwrite the newer, narrower result.
    sinceOnly.resolve([
      { id: 2, timestamp: "2026-07-01T13:00:00Z", user: "markus", action: "x", note: "since_only" },
    ]);
    await new Promise((r) => setTimeout(r, 0));

    expect(screen.getByText("since_and_until")).toBeInTheDocument();
    expect(screen.queryByText("since_only")).not.toBeInTheDocument();
  });
});
