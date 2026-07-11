// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/svelte";

// The events store drives the badge's WS state. We mock the whole
// module so no real WebSocket is opened during the unit test.
let wsStatusValue: "connecting" | "open" | "closed" = "closed";

vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: vi.fn(() => () => {}),
  // status() is a reactive getter — return whatever the outer variable says.
  status: () => wsStatusValue,
  diagnostics: () => ({ received: 0, lastType: "" }),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import ConnectionBadge from "./ConnectionBadge.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  wsStatusValue = "closed";
});

describe("ConnectionBadge", () => {
  it("renders the disconnected label when WS is closed", () => {
    wsStatusValue = "closed";
    const { getByText } = render(ConnectionBadge);
    // The badge labels the live-update (WS) stream; t() is mocked to the key.
    expect(getByText("connection.live_off")).toBeTruthy();
  });

  it("renders the connected label when WS is open", () => {
    wsStatusValue = "open";
    const { getByText } = render(ConnectionBadge);
    expect(getByText("connection.live_on")).toBeTruthy();
  });

  it("renders the reconnecting label when WS is connecting", () => {
    wsStatusValue = "connecting";
    const { getByText } = render(ConnectionBadge);
    expect(getByText("connection.reconnecting")).toBeTruthy();
  });

  it("does not render an event counter when received=0", () => {
    wsStatusValue = "open";
    const { container } = render(ConnectionBadge);
    // The event-count span carries tabular-nums; it must not be present when received=0.
    const countSpan = container.querySelector(".tabular-nums");
    expect(countSpan).toBeNull();
  });

  it("paints the disconnected dot neutral-warning (amber), never red", () => {
    wsStatusValue = "closed";
    const { container } = render(ConnectionBadge);
    const dot = container.querySelector("span.rounded-full");
    expect(dot).not.toBeNull();
    // Disconnected is an expected, self-healing state — amber, not red.
    expect(dot!.className).toContain("bg-amber-500");
    expect(dot!.className).not.toContain("bg-red");
  });

  it("uses emerald for the connected dot", () => {
    wsStatusValue = "open";
    const { container } = render(ConnectionBadge);
    const dot = container.querySelector("span.rounded-full");
    expect(dot!.className).toContain("bg-emerald-500");
  });

  it("exposes an explanatory title and aria-label even when the text label is hidden", () => {
    wsStatusValue = "closed";
    const { container } = render(ConnectionBadge);
    const badge = container.querySelector("span[title]");
    expect(badge).not.toBeNull();
    // Both the tooltip and the aria-label carry the plain-language explanation
    // so the meaning survives on phones, where only the dot is visible.
    expect(badge!.getAttribute("title")).toContain("connection.tooltip.off");
    expect(badge!.getAttribute("aria-label")).toContain("connection.tooltip.off");
  });
});
