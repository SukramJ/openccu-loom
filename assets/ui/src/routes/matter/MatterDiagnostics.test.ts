// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen } from "@testing-library/svelte";
import type {
  MatterCompatibility,
  MatterEndpointsResponse,
  MatterMdnsDiagnostics,
  MatterSessionsResponse,
} from "$lib/api/matter-types";

let sessions: MatterSessionsResponse = { sessions: [] };
let mdns: MatterMdnsDiagnostics = { advertising: true, services: [], findings: [] };
let endpoints: MatterEndpointsResponse = { endpoints: [] };
let compat: MatterCompatibility = { ecosystems: [], endpoint_count: 0, findings: [] };

vi.mock("$lib/api/client", () => ({
  api: {
    matterSessions: () => Promise.resolve(sessions),
    matterMdns: () => Promise.resolve(mdns),
    matterEndpoints: () => Promise.resolve(endpoints),
    matterCompatibility: () => Promise.resolve(compat),
  },
}));

import MatterDiagnostics from "./MatterDiagnostics.svelte";

describe("MatterDiagnostics", () => {
  beforeEach(() => {
    sessions = { sessions: [] };
    mdns = { advertising: true, services: [], findings: [] };
    endpoints = { endpoints: [] };
    compat = { ecosystems: [], endpoint_count: 0, findings: [] };
  });
  afterEach(cleanup);

  // The blocking discovery findings are the reason an operator opens this
  // view at all: the bridge advertises, the log looks fine, and the
  // controller never offers it. The message has to be on screen, not
  // folded behind a severity filter.
  it("shows a blocking discovery finding with its explanation", async () => {
    mdns = {
      advertising: true,
      services: [],
      findings: [
        {
          severity: "error",
          code: "no_subtypes",
          message: "The commissionable record announces no service subtypes.",
        },
      ],
    };

    render(MatterDiagnostics);
    expect(
      await screen.findByText(/announces no service subtypes/i),
    ).toBeTruthy();
  });

  // A controller that stopped talking keeps its session, so the quiet age
  // is the only thing that distinguishes it from a healthy one.
  it("renders how long each controller has been quiet", async () => {
    sessions = {
      sessions: [
        {
          session_id: 7,
          fabric_index: 1,
          peer_node_id: "00000000DEADBEEF",
          local_node_id: "0000000000000001",
          is_pase: false,
          subscriptions: 2,
          last_activity: new Date().toISOString(),
          last_peer_activity: new Date().toISOString(),
          idle_seconds: 3,
          peer_idle_seconds: 2400,
        },
      ],
    };

    render(MatterDiagnostics);
    // 2400 s renders as an hour-scale age rather than a raw number.
    expect(await screen.findByText("40min")).toBeTruthy();
  });

  // A commissioned controller holding no subscriptions receives nothing,
  // which looks identical to a healthy session without this hint.
  it("marks a session that carries no subscriptions", async () => {
    sessions = {
      sessions: [
        {
          session_id: 3,
          fabric_index: 1,
          peer_node_id: "1",
          local_node_id: "2",
          is_pase: false,
          subscriptions: 0,
          last_activity: new Date().toISOString(),
          last_peer_activity: new Date().toISOString(),
          idle_seconds: 1,
          peer_idle_seconds: 1,
        },
      ],
    };

    render(MatterDiagnostics);
    expect(await screen.findByText(/receiving nothing|empfängt aber nichts/i)).toBeTruthy();
  });

  it("reports an ecosystem incompatibility for the paired controllers", async () => {
    compat = {
      ecosystems: [{ ecosystem: "google", vendor_id: 0x6006, fabric_index: 1 }],
      endpoint_count: 4,
      findings: [
        {
          ecosystem: "google",
          code: "device_type_unsupported",
          message: "Google Home does not support the water-valve device type.",
          device_type: 0x0042,
        },
      ],
    };

    render(MatterDiagnostics);
    expect(await screen.findByText(/does not support the water-valve/i)).toBeTruthy();
  });

  // The root and aggregator endpoints are bridge scaffolding; listing
  // them would put two entries above every real device.
  it("lists bridged endpoints and hides the bridge's own scaffolding", async () => {
    endpoints = {
      endpoints: [
        {
          endpoint_id: 0,
          parent_endpoint_id: 0,
          device_type: 0x0016,
          device_type_name: "RootNode",
          reachable: true,
          friendly_name: "",
          clusters: [],
        },
        {
          endpoint_id: 2,
          parent_endpoint_id: 1,
          device_type: 0x010a,
          device_type_name: "OnOffPlugInUnit",
          reachable: true,
          friendly_name: "Bücherregal",
          clusters: [{ id: 6, name: "OnOff", revision: 6 }],
        },
      ],
    };

    render(MatterDiagnostics);
    expect(await screen.findByText("Bücherregal")).toBeTruthy();
    expect(screen.queryByText("RootNode")).toBeNull();
  });
});
