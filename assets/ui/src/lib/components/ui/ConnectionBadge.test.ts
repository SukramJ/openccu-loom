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
    // t("diagnostics.disconnected") is mocked to return the key
    expect(getByText("diagnostics.disconnected")).toBeTruthy();
  });

  it("renders the connected label when WS is open", () => {
    wsStatusValue = "open";
    const { getByText } = render(ConnectionBadge);
    expect(getByText("diagnostics.connected")).toBeTruthy();
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
});
