import { describe, expect, it } from "vitest";
import { ROUTE_PREFIXES, routeAllowed } from "./routes";

describe("routeAllowed", () => {
  it("covers a prefix and the paths below it, not a longer sibling name", () => {
    const prefixes = ["/devices", "/audit"];
    expect(routeAllowed("/devices", prefixes)).toBe(true);
    expect(routeAllowed("/devices/A%3A1/paramsets/MASTER", prefixes)).toBe(true);
    expect(routeAllowed("/audit", prefixes)).toBe(true);
    // A route whose name merely starts with an allowed one is another route.
    expect(routeAllowed("/devicesx", prefixes)).toBe(false);
    expect(routeAllowed("/auditing", prefixes)).toBe(false);
    expect(routeAllowed("/backups", prefixes)).toBe(false);
  });

  it("ships a list of absolute prefixes without a trailing slash", () => {
    expect(ROUTE_PREFIXES.length).toBeGreaterThan(0);
    for (const p of ROUTE_PREFIXES) {
      expect(p).toMatch(/^\/[a-z]/);
      expect(p.endsWith("/")).toBe(false);
    }
  });
});
