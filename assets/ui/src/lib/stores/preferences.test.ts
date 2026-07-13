// @vitest-environment happy-dom
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

function resetFraming(): void {
  Object.defineProperty(window, "top", { value: window, configurable: true });
  Object.defineProperty(window, "parent", { value: window, configurable: true });
}

beforeEach(() => {
  resetFraming();
  localStorage.clear();
  document.documentElement.className = "";
  document.documentElement.removeAttribute("data-skin");
  document.documentElement.removeAttribute("style");
});

afterEach(() => {
  resetFraming();
  localStorage.clear();
  document.documentElement.className = "";
  document.documentElement.removeAttribute("data-skin");
  document.documentElement.removeAttribute("style");
});

describe("preferences: skin", () => {
  it("defaults to 'loom' standalone", async () => {
    const { prefs } = await import("./preferences.svelte");
    expect(prefs.skin).toBe("loom");
  });

  it("applyTheme sets document.documentElement.dataset.skin to the stored skin", async () => {
    const { prefs, applyTheme, setSkin } = await import("./preferences.svelte");
    setSkin("ha");
    expect(prefs.skin).toBe("ha");
    applyTheme();
    expect(document.documentElement.dataset.skin).toBe("ha");

    setSkin("loom");
    applyTheme();
    expect(document.documentElement.dataset.skin).toBe("loom");
  });

  it("setSkin persists the choice across a reload (module re-import reads storage)", async () => {
    vi.resetModules();
    const mod1 = await import("./preferences.svelte");
    mod1.setSkin("ha");

    vi.resetModules();
    const mod2 = await import("./preferences.svelte");
    expect(mod2.prefs.skin).toBe("ha");
  });

  it("applyTheme forces skin to 'ha' when embedded, regardless of the stored value", async () => {
    vi.resetModules();
    const { prefs, applyTheme, setSkin } = await import("./preferences.svelte");
    setSkin("loom");

    Object.defineProperty(window, "top", { value: {}, configurable: true });
    applyTheme();
    expect(document.documentElement.dataset.skin).toBe("ha");
    expect(prefs.skin).toBe("loom");
  });
});
