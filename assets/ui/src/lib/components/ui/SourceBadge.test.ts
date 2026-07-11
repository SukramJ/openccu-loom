// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";

// prefs is a plain mocked object (not a Svelte $state proxy) — mutate
// prefs.expertMode directly before render() to select basic/expert mode,
// mirroring the pattern in SectionEditor.save.test.ts. i18n.ts also reads
// this same mocked module for prefs.locale, so it is pinned to "en" here —
// these tests care about the actual tooltip/sr-only copy (not just key
// identity), so the real i18n module is exercised rather than an
// identity-mock.
//
// vi.mock factories are hoisted above top-level const declarations, so the
// mocked object must be created via vi.hoisted() to be visible inside it.
const { prefs } = vi.hoisted(() => ({ prefs: { expertMode: false, locale: "en" as const } }));

vi.mock("$lib/stores/preferences.svelte", () => ({ prefs }));

import SourceBadge from "./SourceBadge.svelte";

beforeEach(() => {
  prefs.expertMode = false;
  prefs.locale = "en";
});

afterEach(() => {
  cleanup();
});

describe("SourceBadge", () => {
  it("renders a title tooltip with the full source description for db", () => {
    const { container } = render(SourceBadge, { props: { source: "db" } });
    const wrapper = container.querySelector("span[title]");
    expect(wrapper).not.toBeNull();
    expect(wrapper!.getAttribute("title")).toBe("Saved via the UI");
  });

  it("renders a title tooltip with the full source description for bootstrap", () => {
    const { container } = render(SourceBadge, { props: { source: "bootstrap" } });
    const wrapper = container.querySelector("span[title]");
    expect(wrapper!.getAttribute("title")).toBe("From the bootstrap config file");
  });

  it("renders a title tooltip with the full source description for env", () => {
    const { container } = render(SourceBadge, { props: { source: "env" } });
    const wrapper = container.querySelector("span[title]");
    expect(wrapper!.getAttribute("title")).toBe("Overridden by environment variable");
  });

  it("renders a title tooltip with the full source description for default", () => {
    const { container } = render(SourceBadge, { props: { source: "default" } });
    const wrapper = container.querySelector("span[title]");
    expect(wrapper!.getAttribute("title")).toBe("Default value");
  });

  it("always carries a screen-reader-only text on the dot, even in basic mode", () => {
    prefs.expertMode = false;
    const { container } = render(SourceBadge, { props: { source: "env" } });
    const srText = container.querySelector(".sr-only");
    expect(srText).not.toBeNull();
    expect(srText!.textContent).toBe("Overridden by environment variable");
  });

  it("hides the short label pill in basic mode", () => {
    prefs.expertMode = false;
    const { container, getByText } = render(SourceBadge, { props: { source: "env" } });
    expect(() => getByText("env")).toThrow();
    // The colour dot itself is still present.
    expect(container.querySelector(".rounded-full")).not.toBeNull();
  });

  it("shows the short label pill in expert mode", () => {
    prefs.expertMode = true;
    const { getByText } = render(SourceBadge, { props: { source: "env" } });
    expect(getByText("env")).toBeTruthy();
  });
});
