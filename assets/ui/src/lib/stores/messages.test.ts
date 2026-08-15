// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";

// Mirrors addonUpdate.test.ts: the WS pump is mocked so the broadcast path
// can be driven by invoking the captured handler directly. The assertions go
// through a rendered probe rather than the getters alone — the store's whole
// purpose is that the sidebar badge repaints when a count changes, and a bare
// getter read stays green even when nothing ever invalidates.

let capturedHandler: ((ev: { type: string; payload?: unknown }) => void) | null =
  null;

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: vi.fn((h: (ev: { type: string; payload?: unknown }) => void) => {
    capturedHandler = h;
    return vi.fn();
  }),
}));

const mockGetHubDataPoints = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    getHubDataPoints: (...args: unknown[]) => mockGetHubDataPoints(...args),
  },
}));

import { messagesStore } from "./messages.svelte";
import MessageCountProbe from "./__testutils__/MessageCountProbe.svelte";

const CENTRALS = ["ccu-1", "ccu-2"];

function text(testId: string): string {
  return document.querySelector(`[data-testid="${testId}"]`)?.textContent ?? "";
}

function broadcast(kind: "service" | "alarm", central: string, count: number) {
  capturedHandler!({ type: `hub.${kind}_message`, payload: { central, count } });
}

// The store is a module singleton, so a test states the full per-central
// picture it expects instead of inheriting whatever the previous one left.
function baseline() {
  for (const central of CENTRALS) {
    broadcast("service", central, 0);
    broadcast("alarm", central, 0);
  }
}

async function mountAndSubscribe() {
  render(MessageCountProbe);
  messagesStore.ensureStream();
  await waitFor(() => expect(capturedHandler).not.toBeNull());
  baseline();
  await waitFor(() => expect(text("total")).toBe("0"));
}

beforeEach(() => {
  vi.clearAllMocks();
  capturedHandler = null;
  mockGetHubDataPoints.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  messagesStore.release();
});

describe("messagesStore — badge reactivity", () => {
  // Declared first so it runs before any broadcast marks a central live,
  // which is what makes the snapshot the authority for it.
  it("repaints the seed the hub snapshot delivers", async () => {
    mockGetHubDataPoints.mockResolvedValue([
      {
        central: CENTRALS[0],
        service_messages: { value: 3 },
        alarm_messages: { value: 1 },
      },
    ]);

    render(MessageCountProbe);
    await messagesStore.refresh();

    await waitFor(() => expect(text("total")).toBe("4"));
  });

  it("repaints the rendered count when a broadcast raises a message", async () => {
    await mountAndSubscribe();

    broadcast("service", CENTRALS[0], 2);

    await waitFor(() => expect(text("service")).toBe("2"));
    expect(text("total")).toBe("2");
  });

  it("sums the counters across centrals and both message kinds", async () => {
    await mountAndSubscribe();

    broadcast("service", CENTRALS[0], 2);
    broadcast("service", CENTRALS[1], 1);
    broadcast("alarm", CENTRALS[0], 4);

    await waitFor(() => expect(text("total")).toBe("7"));
    expect(text("service")).toBe("3");
    expect(text("alarm")).toBe("4");
  });

  it("repaints back to zero when the last message is acknowledged", async () => {
    await mountAndSubscribe();

    broadcast("service", CENTRALS[0], 1);
    await waitFor(() => expect(text("total")).toBe("1"));

    broadcast("service", CENTRALS[0], 0);

    await waitFor(() => expect(text("total")).toBe("0"));
  });
});
