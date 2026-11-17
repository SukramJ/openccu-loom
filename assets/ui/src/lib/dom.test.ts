// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from "vitest";
import { deepActiveElement } from "./dom";

afterEach(() => {
  document.body.replaceChildren();
});

describe("deepActiveElement", () => {
  it("returns the focused element in the light DOM", () => {
    const input = document.createElement("input");
    document.body.append(input);
    input.focus();
    expect(deepActiveElement()).toBe(input);
  });

  it("follows focus through nested shadow roots to the focused field", () => {
    const outer = document.createElement("div");
    document.body.append(outer);
    const outerRoot = outer.attachShadow({ mode: "open" });
    const inner = document.createElement("div");
    outerRoot.append(inner);
    const innerRoot = inner.attachShadow({ mode: "open" });
    const input = document.createElement("input");
    innerRoot.append(input);
    input.focus();
    // The plain document read stops at the outermost host; the helper must not.
    expect(document.activeElement).toBe(outer);
    expect(deepActiveElement()).toBe(input);
  });
});
