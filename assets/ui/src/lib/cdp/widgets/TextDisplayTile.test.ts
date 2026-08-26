// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// The CDP dispatcher builds its display row from a fixed key set and drops
// every other param without complaining, so a colour sent under a key it
// does not read is lost behind a successful write.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { CustomDPSummary } from "$lib/api/types";

const { mockInvoke } = vi.hoisted(() => ({ mockInvoke: vi.fn() }));

vi.mock("$lib/api/client", () => ({
  api: { invokeCustomDataPoint: (...args: unknown[]) => mockInvoke(...args) },
  friendlyError: (err: unknown) =>
    err instanceof Error ? err.message : String(err),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import TextDisplayTile from "./TextDisplayTile.svelte";

const ADDRESS = "0001DISPLAY";

const cdp = {
  name: "text_display",
  kind: "text_display",
  channel_no: 1,
} as unknown as CustomDPSummary;

beforeEach(() => {
  vi.clearAllMocks();
  mockInvoke.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("TextDisplayTile — write", () => {
  it("sends the colour under the text_color key the dispatcher reads", async () => {
    render(TextDisplayTile, { props: { address: ADDRESS, cdp } });

    await fireEvent.input(screen.getByPlaceholderText("cdp.textdisplay.text_placeholder"), {
      target: { value: "Hello" },
    });
    await fireEvent.click(screen.getByText("cdp.textdisplay.advanced"));
    await fireEvent.input(screen.getByPlaceholderText("cdp.textdisplay.color_placeholder"), {
      target: { value: "red" },
    });
    await fireEvent.click(screen.getByText("cdp.textdisplay.write"));

    await waitFor(() => expect(mockInvoke).toHaveBeenCalledTimes(1));
    expect(mockInvoke).toHaveBeenCalledWith(ADDRESS, "text_display", "write", {
      id: 1,
      text: "Hello",
      text_color: "RED",
    });
  });

  it("omits the colour entirely when the operator left it blank", async () => {
    render(TextDisplayTile, { props: { address: ADDRESS, cdp } });

    await fireEvent.input(screen.getByPlaceholderText("cdp.textdisplay.text_placeholder"), {
      target: { value: "Hello" },
    });
    await fireEvent.click(screen.getByText("cdp.textdisplay.write"));

    await waitFor(() => expect(mockInvoke).toHaveBeenCalledTimes(1));
    expect(mockInvoke).toHaveBeenCalledWith(ADDRESS, "text_display", "write", {
      id: 1,
      text: "Hello",
    });
  });
});
