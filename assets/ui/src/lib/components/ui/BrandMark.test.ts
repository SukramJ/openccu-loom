// @vitest-environment happy-dom
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, cleanup } from "@testing-library/svelte";

import BrandMark from "./BrandMark.svelte";

// BrandMark derives its <img src> from ingressBase(), which reads
// location.pathname at render time. Stub it per case.
function setPathname(pathname: string): void {
  vi.stubGlobal("location", { pathname });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("BrandMark — ingress-aware asset URL", () => {
  it("uses a root-relative /app path when served directly", () => {
    setPathname("/app/devices");
    const { getByAltText } = render(BrandMark, {
      props: { mode: "wordmark", ariaLabel: "OpenCCU-Loom" },
    });
    const img = getByAltText("OpenCCU-Loom");
    expect(img.getAttribute("src")).toBe("/app/wordmark.svg");
  });

  it("carries the Ingress prefix behind Home Assistant", () => {
    setPathname("/api/hassio_ingress/AbC123-token/app/devices");
    const { getByAltText } = render(BrandMark, {
      props: { mode: "wordmark", ariaLabel: "OpenCCU-Loom" },
    });
    const img = getByAltText("OpenCCU-Loom");
    // Without the prefix the browser would request the HA origin
    // (/app/wordmark.svg) and the logo would 404 — the reported bug.
    expect(img.getAttribute("src")).toBe(
      "/api/hassio_ingress/AbC123-token/app/wordmark.svg",
    );
  });

  it("prefixes the mark variant and points at an asset that exists", () => {
    setPathname("/api/hassio_ingress/AbC123-token/app/");
    const { getByAltText } = render(BrandMark, {
      props: { mode: "mark", ariaLabel: "OpenCCU-Loom" },
    });
    const img = getByAltText("OpenCCU-Loom");
    expect(img.getAttribute("src")).toBe(
      "/api/hassio_ingress/AbC123-token/app/mark-loom.svg",
    );
  });
});
