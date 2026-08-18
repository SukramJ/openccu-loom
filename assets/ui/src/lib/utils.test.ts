import { describe, it, expect } from "vitest";
import { makeTextMatcher } from "./utils";

describe("makeTextMatcher", () => {
  it("matches everything for an empty or whitespace term", () => {
    const m = makeTextMatcher("   ");
    expect(m("anything")).toBe(true);
    expect(m("")).toBe(true);
  });

  it("matches case-insensitively as a substring for plain terms", () => {
    const m = makeTextMatcher("meq");
    expect(m("BidCos-RF.MEQ0123456")).toBe(true);
    expect(m("nothing here")).toBe(false);
  });

  it("honours regular-expression syntax for power users", () => {
    const alternation = makeTextMatcher("MEQ|HEQ");
    expect(alternation("HEQ0815")).toBe(true);
    expect(alternation("OEQ0815")).toBe(false);

    const anchored = makeTextMatcher("^BidCos-RF\\.");
    expect(anchored("BidCos-RF.ABC")).toBe(true);
    expect(anchored("HmIP-RF.BidCos-RF.ABC")).toBe(false);
  });

  it("falls back to a substring match on an invalid pattern", () => {
    // A lone "(" is not a valid regex; it must still match literally
    // rather than throwing while the user is mid-typing.
    const m = makeTextMatcher("foo(");
    expect(m("foo(bar")).toBe(true);
    expect(m("foobar")).toBe(false);
  });

  it("still matches literally when the term happens to also be a valid but different regex", () => {
    // "Wohnzimmer (Decke)" compiles as a regex (the parens become a
    // capture group), which then matches nothing containing the literal
    // text. The literal pass must not be disabled just because the term
    // also parses as a (differently-meaning) pattern.
    expect(makeTextMatcher("Wohnzimmer (Decke)")("Wohnzimmer (Decke)")).toBe(true);
    expect(makeTextMatcher("Bad [oben]")("Bad [oben]")).toBe(true);
    expect(makeTextMatcher("Licht+Steckdose")("Licht+Steckdose")).toBe(true);
    expect(makeTextMatcher("Taster (Kanal 1)")("Taster (Kanal 1)")).toBe(true);
    // A power-user regex that has no literal match still works via the
    // regex pass.
    expect(makeTextMatcher("HmIP-BSM")("HmIP-BSM")).toBe(true);
  });
});
