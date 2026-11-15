import { describe, it, expect } from "vitest";
import type { CentralProbeResult, CentralRow } from "$lib/api/client";
import {
  boxLink,
  isLiteMoveWarning,
  liteMoveBody,
  liteMoveReason,
  lostInterfaces,
  offeredInterfaces,
  probeAllowsMove,
} from "./lite-move";

const stored: CentralRow = {
  name: "ccu1",
  host: "192.0.2.10",
  enabled: true,
  system_type: "ccu",
  username: "Admin",
  password_plain: "***",
  json_rpc_port: 443,
  tls: true,
  tls_insecure_skip_verify: true,
  primary_interface: "HmIP-RF",
  interfaces: [{ name: "HmIP-RF", port: 2010 }, { name: "BidCos-RF" }, { name: "CUxD", rpc_type: "binrpc" }],
  visibility: { un_ignore: ["X"] },
  behavior: { enable_sysvar_scan: true },
};

const probe: CentralProbeResult = {
  system_type: "openccu-lite",
  ready: true,
  lite: { pairing_available: true, interfaces: ["BidCos-RF", "HmIP-RF"] },
};

describe("liteMoveReason", () => {
  it("reads the reason from the warning's message key", () => {
    expect(liteMoveReason("warnings.lite_move_pending.no_token")).toBe("no_token");
    expect(liteMoveReason("warnings.lite_move_pending.ambiguous")).toBe("ambiguous");
    expect(liteMoveReason("warnings.lite_move_pending.failed")).toBe("failed");
  });

  it("offers nothing for an unknown reason or another warning", () => {
    expect(liteMoveReason("warnings.lite_move_pending.later")).toBeNull();
    expect(liteMoveReason("warnings.health.unhealthy")).toBeNull();
  });

  it("recognises the warning by its id", () => {
    expect(isLiteMoveWarning("lite-move:ccu1")).toBe(true);
    expect(isLiteMoveWarning("health:mqtt")).toBe(false);
  });
});

describe("boxLink", () => {
  it("names the page's host on its default port", () => {
    expect(boxLink({ protocol: "https:", hostname: "192.0.2.10" })).toBe("https://192.0.2.10/");
  });
});

describe("probeAllowsMove", () => {
  it("needs a ready box that offers pairing", () => {
    expect(probeAllowsMove(probe)).toBe(true);
    expect(probeAllowsMove({ ...probe, ready: false })).toBe(false);
    expect(probeAllowsMove({ ...probe, system_type: "ccu" })).toBe(false);
    expect(probeAllowsMove({ ...probe, lite: { pairing_available: false } })).toBe(false);
    expect(probeAllowsMove({ system_type: "openccu-lite", ready: true })).toBe(false);
  });
});

describe("lostInterfaces", () => {
  it("lists the stored interfaces the box does not offer", () => {
    expect(lostInterfaces(stored, offeredInterfaces(probe))).toEqual(["CUxD"]);
    expect(lostInterfaces(stored, ["HmIP-RF", "BidCos-RF", "CUxD"])).toEqual([]);
  });
});

describe("liteMoveBody", () => {
  it("names every field the move changes and nothing it keeps", () => {
    const body = liteMoveBody(stored, probe, "p1");
    expect(body).toEqual({
      name: "ccu1",
      host: "192.0.2.10",
      enabled: true,
      system_type: "openccu-lite",
      pairing_id: "p1",
      json_rpc_port: 0,
      tls: false,
      tls_insecure_skip_verify: false,
      primary_interface: "HmIP-RF",
      interfaces: [{ name: "BidCos-RF" }, { name: "HmIP-RF" }],
    });
    // Omitted, so the update keeps them.
    expect(body).not.toHaveProperty("visibility");
    expect(body).not.toHaveProperty("behavior");
    // Never a credential of the system the central leaves.
    expect(body).not.toHaveProperty("username");
    expect(body).not.toHaveProperty("password_plain");
  });

  it("clears a primary interface the box does not offer", () => {
    const body = liteMoveBody({ ...stored, primary_interface: "CUxD" }, probe, "p1");
    expect(body.primary_interface).toBe("");
  });

  it("keeps a disabled central disabled", () => {
    expect(liteMoveBody({ ...stored, enabled: false }, probe, "p1").enabled).toBe(false);
  });
});
