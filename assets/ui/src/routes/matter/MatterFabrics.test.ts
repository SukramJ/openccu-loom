// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// The fabric list carries its 64-bit ids twice: as JSON numbers, which lose
// every bit above 2^53 in transport, and as pre-formatted hex strings, which
// do not. The column must render the hex field — printing "0x" in front of a
// decimal produces an identifier that matches nothing in the controller's own
// UI or in chip-tool output, and rendering a rounded number produces one that
// looks comparable and is not.
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
          fabric_id_hex: "0000000000001122",
          node_id: 0x11f71b4b3f4,
          node_id_hex: "0000011F71B4B3F4",
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

  // A daemon older than node_id_hex serves the id only as a JSON number,
  // and above 2^53 the digits printed from it are not the ones the
  // controller prints. Saying so is the difference between a comparison
  // that fails loudly and one that quietly compares the wrong value.
  it("marks a node id that arrived rounded because only the numeric field was served", async () => {
    fabrics = {
      fabrics: [
        {
          fabric_index: 1,
          fabric_id: 0x1122,
          node_id: 0x1b2c3d4e5f607182,
          vendor_id: 0x1349,
          vendor_name: "Apple Home",
          label: "Apple Home",
          compressed_id: "aabb",
          root_public_key: "04aa",
        },
      ],
    };

    render(MatterFabrics);
    expect(await screen.findByText("rounded")).toBeTruthy();
  });

  it("does not mark an id that fits the transported precision", async () => {
    fabrics = {
      fabrics: [
        {
          fabric_index: 1,
          fabric_id: 0x1122,
          fabric_id_hex: "0000000000001122",
          node_id: 0x11f71b4b3f4,
          node_id_hex: "0000011F71B4B3F4",
          vendor_id: 0x1349,
          vendor_name: "Apple Home",
          label: "Apple Home",
          compressed_id: "aabb",
          root_public_key: "04aa",
        },
      ],
    };

    render(MatterFabrics);
    await screen.findByText("0x0000011F71B4B3F4");
    expect(screen.queryByText("rounded")).toBeNull();
  });

  // The point of the hex field: an id far above 2^53 is rendered exactly
  // and carries no rounding warning, even though the numeric field beside
  // it arrived mangled.
  it("renders a large id from the hex field and does not flag it", async () => {
    fabrics = {
      fabrics: [
        {
          fabric_index: 1,
          fabric_id: 0x1b2c3d4e5f607182,
          fabric_id_hex: "1B2C3D4E5F607182",
          node_id: 0xfedcba9876543210,
          node_id_hex: "FEDCBA9876543210",
          vendor_id: 0x1349,
          vendor_name: "Apple Home",
          label: "Apple Home",
          compressed_id: "aabb",
          root_public_key: "04aa",
        },
      ],
    };

    render(MatterFabrics);
    expect(await screen.findByText("0xFEDCBA9876543210")).toBeTruthy();
    expect(screen.queryByText("rounded")).toBeNull();
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
          fabric_id_hex: "0000000000001122",
          node_id: 0x11f71b4b3f4,
          node_id_hex: "0000011F71B4B3F4",
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
          fabric_id_hex: "0000000000001122",
          node_id: 0x11f71b4b3f4,
          node_id_hex: "0000011F71B4B3F4",
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

// Adding a second controller used to exist twice: the fabrics tab
// opened a commissioning window of its own and printed the codes as
// plain text — no QR, no countdown, and no way to close the window it
// had just opened, because the close control lives on the pairing tab.
// An operator who navigated away lost the codes while the window stayed
// open on the daemon. The tab now points at the one surface that runs
// the whole flow.
describe("MatterFabrics — adding a controller", () => {
  it("sends the operator to the pairing tab instead of opening a second window here", async () => {
    fabrics = { fabrics: [] };

    render(MatterFabrics);

    const link = await screen.findByTestId("share-bridge-link");
    expect(link.getAttribute("href")).toBe("#/matter/pair");
  });
});
