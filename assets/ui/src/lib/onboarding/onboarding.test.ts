import { describe, it, expect } from "vitest";
import { emptyOnboarding, liteCredential, liteReady } from "./onboarding";

const lite = { ...emptyOnboarding(), systemType: "openccu-lite" as const };

describe("liteReady", () => {
  it("needs an approved pairing or a pasted token", () => {
    expect(liteReady(lite)).toBe(false);
    expect(liteReady({ ...lite, pairingId: "p1" })).toBe(true);
    expect(liteReady({ ...lite, mode: "token", apiToken: "  " })).toBe(false);
    expect(liteReady({ ...lite, mode: "token", apiToken: "olt_x" })).toBe(true);
  });

  it("accepts the stored token when editing", () => {
    expect(liteReady(lite, true)).toBe(true);
    expect(liteReady({ ...lite, mode: "token" }, true)).toBe(true);
  });

  it("refuses an HTTPS fingerprint nobody compared", () => {
    const pinned = { ...lite, pairingId: "p1", tls: true, tlsFingerprint: "ab" };
    expect(liteReady(pinned)).toBe(false);
    expect(liteReady({ ...pinned, fingerprintConfirmed: true })).toBe(true);
    // A certificate a trusted authority signed leaves nothing to pin.
    expect(liteReady({ ...pinned, tlsFingerprint: "" })).toBe(true);
  });
});

describe("liteCredential", () => {
  it("sends the pairing id or the token, never both", () => {
    expect(liteCredential({ ...lite, pairingId: "p1", apiToken: "olt_x" })).toEqual({ pairing_id: "p1" });
    expect(liteCredential({ ...lite, mode: "token", pairingId: "p1", apiToken: " olt_x " })).toEqual({
      api_token: "olt_x",
    });
    expect(liteCredential(lite)).toEqual({});
  });
});
