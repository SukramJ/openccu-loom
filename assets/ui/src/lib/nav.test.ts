// @vitest-environment happy-dom
import { describe, it, expect, vi } from "vitest";

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import {
  foldedRouteTarget,
  isKnownLandingRoute,
  isValidLandingRoute,
  landingTargets,
  navClusters,
  navSurfaceID,
} from "./nav";

const ALL_GATES = { matterEnabled: true, historyEnabled: true, isAdmin: true };

function allHrefs(): string[] {
  return navClusters(ALL_GATES).flatMap((cluster) => cluster.items.map((item) => item.href));
}

describe("nav — folded views", () => {
  // Both views moved into Settings tabs. Leaving them in the navigation
  // would offer the operator two doors into one surface, which is the
  // duplication the move removed.
  it("no longer lists the access-control or hidden-parameters views", () => {
    expect(allHrefs()).not.toContain("#/access");
    expect(allHrefs()).not.toContain("#/visibility");
  });

  it("still lists Settings, which absorbed them", () => {
    expect(allHrefs()).toContain("#/settings");
  });
});

describe("nav — folded-route resolution", () => {
  it("resolves a folded route to the settings tab that absorbed it", () => {
    expect(foldedRouteTarget("/access")).toBe("/settings?tab=users");
    expect(foldedRouteTarget("/visibility")).toBe("/settings?tab=visibility");
  });

  it("answers in the hash form it was asked in", () => {
    expect(foldedRouteTarget("#/access")).toBe("#/settings?tab=users");
    expect(foldedRouteTarget("#/visibility")).toBe("#/settings?tab=visibility");
  });

  it("ignores a query string on the folded route", () => {
    expect(foldedRouteTarget("#/access?foo=bar")).toBe("#/settings?tab=users");
  });

  it("leaves routes that were never folded alone", () => {
    expect(foldedRouteTarget("#/settings")).toBeNull();
    expect(foldedRouteTarget("#/devices")).toBeNull();
    expect(foldedRouteTarget("")).toBeNull();
  });
});

describe("nav — landing page candidates", () => {
  it("does not offer a folded view as a landing page", () => {
    expect(isValidLandingRoute("#/access", ALL_GATES)).toBe(false);
    expect(isValidLandingRoute("#/visibility", ALL_GATES)).toBe(false);
    expect(isKnownLandingRoute("#/access")).toBe(false);
    expect(isKnownLandingRoute("#/visibility")).toBe(false);
  });

  it("offers every navigation entry, and only those", () => {
    const targets = landingTargets(ALL_GATES).map((target) => target.href);
    expect(targets).toEqual(allHrefs());
    expect(targets).toContain("#/settings");
  });
});

describe("nav — surface profile", () => {
  it("drops the views the operator's profile hides", () => {
    const hrefs = navClusters({
      ...ALL_GATES,
      surfaceVisible: (id) => id !== "nav.alarm" && id !== "nav.favorites",
    }).flatMap((c) => c.items.map((i) => i.href));

    expect(hrefs).not.toContain("#/alarm");
    expect(hrefs).not.toContain("#/favorites");
    expect(hrefs).toContain("#/devices");
  });

  it("drops a cluster once its last item is hidden", () => {
    // Bridges holds Matter alone. An empty labelled group would read as
    // a broken feature rather than a configured-away one.
    const labels = navClusters({
      ...ALL_GATES,
      surfaceVisible: (id) => id !== "nav.matter",
    }).map((c) => c.label);
    const withMatter = navClusters(ALL_GATES).map((c) => c.label);

    expect(withMatter.length - labels.length).toBe(1);
  });

  it("is ANDed with the capability gates, never a replacement", () => {
    // Showing a surface cannot conjure a view whose feature is off.
    const hrefs = navClusters({
      matterEnabled: false,
      historyEnabled: true,
      isAdmin: true,
      surfaceVisible: () => true,
    }).flatMap((c) => c.items.map((i) => i.href));

    expect(hrefs).not.toContain("#/matter");
  });

  it("offers only profile-visible views as landing pages", () => {
    const gates = { ...ALL_GATES, surfaceVisible: (id: string) => id !== "nav.alarm" };
    expect(isValidLandingRoute("#/alarm", gates)).toBe(false);
    expect(isValidLandingRoute("#/devices", gates)).toBe(true);
  });
});

describe("navSurfaceID", () => {
  it("maps a navigation href onto its surface id", () => {
    expect(navSurfaceID("#/devices")).toBe("nav.devices");
    expect(navSurfaceID("#/settings")).toBe("nav.settings");
  });

  it("resolves a sub-path or query to its top-level view", () => {
    expect(navSurfaceID("#/alarm/zones/3")).toBe("nav.alarm");
    expect(navSurfaceID("#/settings?tab=users")).toBe("nav.settings");
  });
});
