// @vitest-environment happy-dom
import { describe, it, expect, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";

import Button from "./Button.svelte";
import Card from "./Card.svelte";

afterEach(() => cleanup());

describe("Button: HA token-backed classes", () => {
  it("renders the default variant with --ha-* CSS vars, not hard-coded brand colours", () => {
    const { getByRole } = render(Button, { props: { children: undefined } });
    const btn = getByRole("button");
    expect(btn.className).toContain("bg-[var(--ha-primary-color)]");
    expect(btn.className).toContain("hover:bg-[var(--ha-primary-color-hover)]");
    expect(btn.className).not.toMatch(/bg-brand-\d/);
    // The enabled fill is token-backed; only the disabled-state fallback
    // (a deliberately neutral grey, unrelated to the skin palette) still
    // uses a literal slate shade.
    expect(btn.className).not.toMatch(/(?<!disabled:)bg-slate-\d/);
  });

  it("renders the outline variant with token-backed border/background/text", () => {
    const { getByRole } = render(Button, { props: { variant: "outline" } });
    const btn = getByRole("button");
    expect(btn.className).toContain("border-[var(--ha-divider-color)]");
    expect(btn.className).toContain("bg-[var(--ha-card-background-color)]");
    expect(btn.className).toContain("text-[var(--ha-primary-text-color)]");
    expect(btn.className).not.toMatch(/border-slate-\d/);
  });

  it("uses the --ha-primary-color token for the focus-visible ring", () => {
    const { getByRole } = render(Button, {});
    const btn = getByRole("button");
    expect(btn.className).toContain("focus-visible:ring-[var(--ha-primary-color)]");
    expect(btn.className).not.toMatch(/ring-brand-\d/);
  });
});

describe("Card: HA token-backed classes", () => {
  it("renders with token-backed border and background instead of hard-coded slate", () => {
    const { container } = render(Card, {});
    const card = container.querySelector("div");
    expect(card).not.toBeNull();
    expect(card!.className).toContain("border-[var(--ha-divider-color)]");
    expect(card!.className).toContain("bg-[var(--ha-card-background-color)]");
    expect(card!.className).not.toMatch(/bg-white\b/);
    expect(card!.className).not.toMatch(/dark:bg-slate-\d/);
  });
});
