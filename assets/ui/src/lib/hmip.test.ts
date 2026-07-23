import { describe, it, expect } from "vitest";
import { normalizeSgtin, isValidHmIPKeyInput, stripLabelSeparators } from "./hmip";

describe("stripLabelSeparators", () => {
  it("removes dashes and spaces and uppercases the rest", () => {
    expect(stripLabelSeparators("3014-f711 a061-a7d5")).toBe("3014F711A061A7D5");
  });

  it("returns an empty string for input made only of separators", () => {
    expect(stripLabelSeparators("- - -")).toBe("");
  });

  it("leaves an already-clean uppercase string unchanged", () => {
    expect(stripLabelSeparators("ABCDEF")).toBe("ABCDEF");
  });
});

describe("normalizeSgtin", () => {
  it("normalises a dashed SGTIN to 24 uppercase hex characters", () => {
    expect(normalizeSgtin("3014-F711-A061-A7D5-6989-2A67")).toBe("3014F711A061A7D569892A67");
  });

  it("accepts lowercase input and uppercases it", () => {
    expect(normalizeSgtin("3014f711a061a7d569892a67")).toBe("3014F711A061A7D569892A67");
  });

  it("returns null for input shorter than 24 hex characters", () => {
    expect(normalizeSgtin("3014-F711-A061")).toBeNull();
  });

  it("returns null for input longer than 24 hex characters", () => {
    expect(normalizeSgtin("3014-F711-A061-A7D5-6989-2A67-FF")).toBeNull();
  });

  it("returns null for non-hex characters", () => {
    expect(normalizeSgtin("3014-G711-A061-A7D5-6989-2AZ7")).toBeNull();
  });
});

describe("isValidHmIPKeyInput", () => {
  it("accepts a full 32-hex key", () => {
    expect(isValidHmIPKeyInput("0110C8531D0952D8D73E1194E95B5F19")).toBe(true);
  });

  it("accepts a 32-hex key with separators and lowercase", () => {
    expect(isValidHmIPKeyInput("0110c853-1d09-52d8-d73e-1194-e95b-5f19")).toBe(true);
  });

  it("accepts the shorter Base32 label form", () => {
    expect(isValidHmIPKeyInput("0123456789ABCEFGHJKLMNPQRS")).toBe(true);
  });

  it("rejects D, I, O, and V — excluded from the label alphabet", () => {
    for (const ch of ["D", "I", "O", "V"]) {
      expect(isValidHmIPKeyInput(`ABC${ch}EFG`)).toBe(false);
    }
  });

  it("rejects input of 33 or more characters", () => {
    expect(isValidHmIPKeyInput("0110C8531D0952D8D73E1194E95B5F190")).toBe(false);
  });

  it("rejects an empty string", () => {
    expect(isValidHmIPKeyInput("")).toBe(false);
  });

  it("rejects input that strips down to empty (separators only)", () => {
    expect(isValidHmIPKeyInput("- - -")).toBe(false);
  });

  it("rejects a 32-character string that is not valid hex", () => {
    // Drawn from the label alphabet but at length 32 only hex is accepted.
    expect(isValidHmIPKeyInput("0123456789ABCEFGHJKLMNPQRSTUWXYZ")).toBe(false);
  });
});
