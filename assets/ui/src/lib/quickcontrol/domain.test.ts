import { describe, it, expect } from "vitest";
import { isVirtualRemoteModel } from "./domain";

describe("isVirtualRemoteModel", () => {
  it.each(["HM-RCV-50", "HMW-RCV-50", "HmIP-RCV-50"])(
    "returns true for the virtual-remote model %s",
    (model) => {
      expect(isVirtualRemoteModel(model)).toBe(true);
    },
  );

  it("trims leading/trailing whitespace before matching", () => {
    expect(isVirtualRemoteModel("  HM-RCV-50  ")).toBe(true);
    expect(isVirtualRemoteModel("\tHmIP-RCV-50\n")).toBe(true);
  });

  it("returns false for undefined", () => {
    expect(isVirtualRemoteModel(undefined)).toBe(false);
  });

  it("returns false for an empty string", () => {
    expect(isVirtualRemoteModel("")).toBe(false);
  });

  it("returns false for a normal (non-remote) device model", () => {
    expect(isVirtualRemoteModel("HmIP-PSM")).toBe(false);
  });

  it("is case-sensitive — a lowercase variant does not match", () => {
    expect(isVirtualRemoteModel("hm-rcv-50")).toBe(false);
  });
});
