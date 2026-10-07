// @vitest-environment happy-dom
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

import { isEmbedded, resolveSkin, startHaBridge } from "./ha-bridge";
import { liteShell } from "./lite-shell.svelte";

const INGRESS_PATH = "/api/hassio_ingress/tok3n/app/";

// Serve the document under HA's Ingress path — the positive HA signal.
function underIngress(): void {
  history.replaceState(null, "", INGRESS_PATH);
}

// Restore window.top/window.parent to their happy-dom defaults (== window)
// after every test so isEmbedded() reports false again.
function resetFraming(): void {
  Object.defineProperty(window, "top", { value: window, configurable: true });
  Object.defineProperty(window, "parent", { value: window, configurable: true });
}

beforeEach(() => {
  resetFraming();
  document.documentElement.className = "";
  document.documentElement.removeAttribute("style");
});

afterEach(() => {
  resetFraming();
  history.replaceState(null, "", "/");
  liteShell.active = false;
  liteShell.theme = null;
  liteShell.lang = null;
  document.documentElement.className = "";
  document.documentElement.removeAttribute("style");
});

describe("isEmbedded", () => {
  it("is false when window.self === window.top (standalone)", () => {
    expect(isEmbedded()).toBe(false);
  });

  it("is true framed under HA's Ingress path", () => {
    underIngress();
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    expect(isEmbedded()).toBe(true);
  });

  it("is true framed by a same-origin parent that is HA's frontend", () => {
    const haDoc = document.implementation.createHTMLDocument("ha");
    haDoc.body.appendChild(haDoc.createElement("home-assistant"));
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    Object.defineProperty(window, "parent", {
      value: { document: haDoc },
      configurable: true,
    });
    expect(isEmbedded()).toBe(true);
  });

  it("is false in an unknown iframe (not every parent is HA)", () => {
    const otherDoc = document.implementation.createHTMLDocument("other");
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    Object.defineProperty(window, "parent", {
      value: { document: otherDoc },
      configurable: true,
    });
    expect(isEmbedded()).toBe(false);
  });

  it("is false in a cross-origin iframe off the Ingress path", () => {
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    Object.defineProperty(window, "parent", {
      get() {
        throw new DOMException("cross-origin");
      },
      configurable: true,
    });
    expect(isEmbedded()).toBe(false);
  });

  it("is false once the openccu-lite shell has signalled, even under an Ingress-like path", () => {
    underIngress();
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    liteShell.active = true;
    expect(isEmbedded()).toBe(false);
  });
});

describe("resolveSkin", () => {
  it("returns the stored skin when standalone", () => {
    expect(resolveSkin("loom")).toBe("loom");
    expect(resolveSkin("ha")).toBe("ha");
  });

  it("forces 'ha' when embedded in HA, regardless of the stored value", () => {
    underIngress();
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    expect(resolveSkin("loom")).toBe("ha");
    expect(resolveSkin("ha")).toBe("ha");
  });
});

describe("startHaBridge", () => {
  it("is a no-op when standalone (returns a cleanup, touches nothing)", () => {
    const cleanup = startHaBridge();
    expect(typeof cleanup).toBe("function");
    expect(document.documentElement.style.getPropertyValue("--primary-color")).toBe("");
    expect(() => cleanup()).not.toThrow();
  });

  it("copies HA theme vars from a same-origin parent onto our root", () => {
    const fakeParentRoot = document.createElement("html");
    const computedValues: Record<string, string> = {
      "--primary-color": "#03a9f4",
      "--card-background-color": "#ffffff",
      "--primary-background-color": "#ffffff",
    };
    const fakeGetComputedStyle = vi.fn(() => ({
      getPropertyValue: (name: string) => computedValues[name] ?? "",
      backgroundColor: "",
    }));

    underIngress();
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    Object.defineProperty(window, "parent", {
      value: {
        document: { documentElement: fakeParentRoot },
        getComputedStyle: fakeGetComputedStyle,
      },
      configurable: true,
    });

    const cleanup = startHaBridge();

    expect(document.documentElement.style.getPropertyValue("--primary-color")).toBe(
      "#03a9f4",
    );
    expect(
      document.documentElement.style.getPropertyValue("--card-background-color"),
    ).toBe("#ffffff");
    // A light background must NOT flip us into dark mode.
    expect(document.documentElement.classList.contains("dark")).toBe(false);

    cleanup();
  });

  it("adds the dark class when the parent's background luminance is low", () => {
    const fakeParentRoot = document.createElement("html");
    const computedValues: Record<string, string> = {
      "--primary-color": "#03a9f4",
      "--primary-background-color": "#111111",
    };
    const fakeGetComputedStyle = vi.fn(() => ({
      getPropertyValue: (name: string) => computedValues[name] ?? "",
      backgroundColor: "",
    }));

    underIngress();
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    Object.defineProperty(window, "parent", {
      value: {
        document: { documentElement: fakeParentRoot },
        getComputedStyle: fakeGetComputedStyle,
      },
      configurable: true,
    });

    const cleanup = startHaBridge();

    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(document.documentElement.style.colorScheme).toBe("dark");

    cleanup();
  });

  it("swallows a throwing (cross-origin) parent access and returns a no-op cleanup", () => {
    underIngress();
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    Object.defineProperty(window, "parent", {
      get() {
        throw new DOMException("Blocked a frame with origin from accessing a cross-origin frame.");
      },
      configurable: true,
    });

    let cleanup: () => void = () => {};
    expect(() => {
      cleanup = startHaBridge();
    }).not.toThrow();
    expect(typeof cleanup).toBe("function");
    expect(() => cleanup()).not.toThrow();
  });
});
