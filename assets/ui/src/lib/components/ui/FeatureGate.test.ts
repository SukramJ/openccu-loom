// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";

afterEach(() => cleanup());

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string>) => (vars ? `${key} ${Object.values(vars).join(" / ")}` : key),
}));

type Entry = {
  name: string;
  system_type?: string;
  features?: Record<string, { available: boolean; reason?: string; scope?: string }>;
};
let fleet: Entry[] = [];

vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: {
    get items() {
      return fleet;
    },
    centralsLacking: (key: string) =>
      fleet.filter((c) => {
        const f = c.features?.[key];
        return f !== undefined && !f.available && f.reason !== "not_ready";
      }),
    byName: (n: string) => fleet.find((c) => c.name === n),
  },
}));

import FeatureGate from "./FeatureGate.svelte";

const body = createRawSnippet(() => ({ render: () => "<p>the view</p>" }));

const lite: Entry = {
  name: "box",
  system_type: "openccu-lite",
  features: { "hub.programs": { available: false, reason: "not_supported_by_system" } },
};
const ccu: Entry = { name: "ccu1", system_type: "ccu", features: { "hub.programs": { available: true } } };

describe("FeatureGate", () => {
  it("renders the view when every central offers the feature", () => {
    fleet = [ccu];
    const { queryByText, queryByRole } = render(FeatureGate, {
      props: { feature: "hub.programs", children: body },
    });
    expect(queryByText("the view")).toBeTruthy();
    expect(queryByRole("note")).toBeNull();
  });

  it("replaces the view with the reason when no central offers it", () => {
    fleet = [lite];
    const { queryByText, getByText } = render(FeatureGate, {
      props: { feature: "hub.programs", children: body },
    });
    expect(queryByText("the view")).toBeNull();
    expect(getByText(/feature\.gate\.none/)).toBeTruthy();
    expect(getByText(/box/)).toBeTruthy();
  });

  it("names the centrals that lack it above the view on a mixed fleet", () => {
    fleet = [lite, ccu];
    const { queryByText, getByRole } = render(FeatureGate, {
      props: { feature: "hub.programs", children: body },
    });
    expect(queryByText("the view")).toBeTruthy();
    const note = getByRole("note");
    expect(note.textContent).toContain("feature.gate.some");
    expect(note.textContent).toContain("box");
    expect(note.textContent).not.toContain("ccu1");
  });

  it("renders the view before the fleet has loaded", () => {
    fleet = [];
    const { queryByText } = render(FeatureGate, {
      props: { feature: "hub.programs", children: body },
    });
    expect(queryByText("the view")).toBeTruthy();
  });

  it("asks only about the named central", () => {
    fleet = [lite, ccu];
    const { queryByText } = render(FeatureGate, {
      props: { feature: "hub.programs", central: "box", children: body },
    });
    expect(queryByText("the view")).toBeNull();
  });
});
