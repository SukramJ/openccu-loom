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
          vendor_name: "Apple Home",
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

describe("MatterFabrics — vendor column", () => {
  // The daemon owns the vendor table so the two surfaces cannot drift.
  // They had: this component carried its own list in which Amazon and
  // SmartThings were mapped to ids the ledger assigns to nobody by those
  // names, so a fabric could read as one vendor here and classify as
  // another ecosystem on the compatibility tab.
  it("renders the name the daemon supplies", async () => {
    fabrics = {
      fabrics: [
        {
          fabric_index: 1,
          fabric_id: 0x1122,
          node_id: 0x11f71b4b3f4,
          vendor_id: 0x115f,
          vendor_name: "Aqara",
          label: "",
          compressed_id: "aabb",
          root_public_key: "04aa",
        },
      ],
    };
    render(MatterFabrics);
    expect(await screen.findByText("Aqara")).toBeTruthy();
  });

  // A fabric served by a daemon older than the field must still identify
  // its controller rather than rendering an empty cell.
  it("falls back to the raw id when the daemon sends no name", async () => {
    fabrics = {
      fabrics: [
        {
          fabric_index: 1,
          fabric_id: 0x1122,
          node_id: 0x11f71b4b3f4,
          vendor_id: 0xabcd,
          vendor_name: "",
          label: "",
          compressed_id: "aabb",
          root_public_key: "04aa",
        },
      ],
    };
    render(MatterFabrics);
    expect(await screen.findByText("0xABCD")).toBeTruthy();
  });
});
