import { describe, it, expect, afterEach } from "vitest";
import { ingressBase, apiBase } from "./base";

// ingressBase()/apiBase() read the global `location` at call time. The
// vitest environment is "node" (no DOM), so each case stubs the pathname.
function setPathname(pathname: string): void {
  (globalThis as { location?: unknown }).location = { pathname } as Location;
}

afterEach(() => {
  delete (globalThis as { location?: unknown }).location;
});

describe("ingressBase", () => {
  it("is empty when served directly under /app/", () => {
    setPathname("/app/");
    expect(ingressBase()).toBe("");
    expect(apiBase()).toBe("/api/v1");
  });

  it("is empty for a deep /app/ path (hash router strips the fragment)", () => {
    setPathname("/app/devices");
    expect(ingressBase()).toBe("");
  });

  it("returns the Ingress proxy prefix behind Home Assistant", () => {
    setPathname("/api/hassio_ingress/AbC123-token/app/");
    expect(ingressBase()).toBe("/api/hassio_ingress/AbC123-token");
    expect(apiBase()).toBe("/api/hassio_ingress/AbC123-token/api/v1");
  });

  it("keeps the full prefix for a deep Ingress path", () => {
    setPathname("/api/hassio_ingress/AbC123-token/app/diagnostics");
    expect(ingressBase()).toBe("/api/hassio_ingress/AbC123-token");
  });

  it("is empty when the path does not contain /app/ (defensive)", () => {
    setPathname("/");
    expect(ingressBase()).toBe("");
    expect(apiBase()).toBe("/api/v1");
  });
});
