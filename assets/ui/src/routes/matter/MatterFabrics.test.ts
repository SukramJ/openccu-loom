// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
//
// The fabric list serves fabric_id / node_id as JSON numbers (the session
// DTO is the one that hex-encodes into strings). Printing "0x" in front of
// the decimal value produces an identifier that matches nothing in the
// controller's own UI or in chip-tool output.
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen } from "@testing-library/svelte";
import type { MatterFabricsResponse } from "$lib/api/matter-types";

let fabrics: MatterFabricsResponse = { fabrics: [] };

vi.mock("$lib/api/client", () => ({
  api: {
    matterFabrics: () => Promise.resolve(fabrics),
    matterStatus: () => Promise.resolve({}),
  },
  ApiError: class ApiError extends Error {},
  // auth.svelte.ts registers this at module scope when the store graph loads.
  setUnauthorizedHandler: vi.fn(),
}));

import MatterFabrics from "./MatterFabrics.svelte";

afterEach(cleanup);

describe("MatterFabrics — node id column", () => {
  it("renders the node id as padded hex, not a decimal behind a 0x", async () => {
    fabrics = {
      fabrics: [
        {
          fabric_index: 1,
          fabric_id: 0x1122,
          node_id: 0x11f71b4b3f4,
          vendor_id: 0x1349,
          label: "Apple Home",
          compressed_id: "aabb",
          root_public_key: "04aa",
        },
      ],
    };

    render(MatterFabrics);

    expect(await screen.findByText("0x0000011F71B4B3F4")).toBeTruthy();
    expect(screen.queryByText("0x1234605616436")).toBeNull();
  });
});
