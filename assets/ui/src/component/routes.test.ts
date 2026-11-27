import { describe, expect, it } from "vitest";
import { EDITOR_ROUTES, routeAllowed, type EditorRoute } from "./routes";

describe("routeAllowed", () => {
  const routes: EditorRoute[] = [
    { method: "GET", prefix: "/devices" },
    { method: "POST", prefix: "/devices/*/paramsets" },
  ];

  it("covers a prefix and the paths below it, not a longer sibling name", () => {
    expect(routeAllowed("GET", "/devices", routes)).toBe(true);
    expect(routeAllowed("GET", "/devices/A%3A1/paramsets/MASTER", routes)).toBe(true);
    // A route whose name merely starts with an allowed one is another route.
    expect(routeAllowed("GET", "/devicesx", routes)).toBe(false);
    expect(routeAllowed("GET", "/backups", routes)).toBe(false);
  });

  it("matches the method, case-insensitively, and nothing else", () => {
    expect(routeAllowed("get", "/devices/A", routes)).toBe(true);
    expect(routeAllowed("DELETE", "/devices/A", routes)).toBe(false);
  });

  it("lets a * segment stand for exactly one non-empty segment", () => {
    expect(routeAllowed("POST", "/devices/A%3A1/paramsets/MASTER/apply-to", routes)).toBe(true);
    expect(routeAllowed("POST", "/devices/A%3A1/release", routes)).toBe(false);
    expect(routeAllowed("POST", "/devices/paramsets", routes)).toBe(false);
    expect(routeAllowed("POST", "/devices//paramsets", routes)).toBe(false);
  });

  it("ships absolute prefixes without a trailing slash and with known methods", () => {
    expect(EDITOR_ROUTES.length).toBeGreaterThan(0);
    for (const r of EDITOR_ROUTES) {
      expect(["GET", "PUT", "POST", "PATCH", "DELETE"]).toContain(r.method);
      expect(r.prefix).toMatch(/^\/[a-z]/);
      expect(r.prefix.endsWith("/")).toBe(false);
    }
  });

  it("keeps the device lifecycle and the admin change log out of the editor's reach", () => {
    for (const [method, path] of [
      ["DELETE", "/devices/A"],
      ["POST", "/devices/A/release"],
      ["POST", "/devices/A/accept"],
      ["POST", "/devices/A/firmware/update"],
      ["POST", "/devices/A/replace"],
      ["POST", "/devices/A/config/restore"],
      ["POST", "/devices/A/reload"],
      ["POST", "/devices/A/links"],
      ["GET", "/audit"],
      ["GET", "/backups"],
    ]) {
      expect(routeAllowed(method, path), `${method} ${path}`).toBe(false);
    }
  });
});
