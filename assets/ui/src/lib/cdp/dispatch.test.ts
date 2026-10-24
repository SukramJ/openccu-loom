// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.
import { describe, expect, it } from "vitest";

import type { CustomDPSummary } from "$lib/api/types";

import { hasCdpWidget, isCdpRenderable } from "./dispatch";

function cdp(over: Partial<CustomDPSummary>): CustomDPSummary {
  return {
    name: "lock",
    category: "lock",
    channel_no: 0,
    kind: "lock",
    supported_operations: ["lock", "unlock"],
    ...over,
  } as unknown as CustomDPSummary;
}

describe("isCdpRenderable", () => {
  it("renders a door lock as a tile", () => {
    expect(
      isCdpRenderable(
        cdp({ capabilities: { open: true, child_safe: false, button: false } }),
      ),
    ).toBe(true);
    expect(isCdpRenderable(cdp({ capabilities: {} }))).toBe(true);
  });

  it("renders no card for a device's operating lock", () => {
    // The lock widget stays registered — only this data point is dropped.
    expect(hasCdpWidget("lock")).toBe(true);
    expect(
      isCdpRenderable(
        cdp({ capabilities: { open: false, child_safe: false, button: true } }),
      ),
    ).toBe(false);
  });

  it("leaves the other kinds to the widget registry", () => {
    expect(
      isCdpRenderable(
        cdp({
          kind: "switch",
          category: "switch",
          capabilities: { button: true },
        }),
      ),
    ).toBe(true);
    expect(isCdpRenderable(cdp({ kind: "no_such_kind" }))).toBe(false);
  });
});
