// @vitest-environment happy-dom
import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from "vitest";

function resetFraming(): void {
  Object.defineProperty(window, "top", { value: window, configurable: true });
  Object.defineProperty(window, "parent", { value: window, configurable: true });
}

// Warm the module transform once, so the first test's dynamic import is not
// timed against a cold compile on a loaded machine.
beforeAll(async () => {
  await import("./preferences.svelte");
}, 60_000);

// Stub the OS / browser colour-scheme preference.
function osDark(dark: boolean): void {
  Object.defineProperty(window, "matchMedia", {
    value: (q: string) => ({
      matches: dark && q.includes("dark"),
      media: q,
      addEventListener: () => {},
      removeEventListener: () => {},
    }),
    configurable: true,
  });
}

// A same-origin parent, as the openccu-lite shell is.
function frameSameOrigin(): Record<string, unknown> {
  const parent = {
    location: { origin: window.location.origin },
    document: document.implementation.createHTMLDocument("parent"),
  };
  Object.defineProperty(window, "top", { value: parent, configurable: true });
  Object.defineProperty(window, "parent", { value: parent, configurable: true });
  return parent;
}

function storedPrefs(): Record<string, unknown> | null {
  const raw = localStorage.getItem("openccu-loom.prefs.v1");
  return raw ? (JSON.parse(raw) as Record<string, unknown>) : null;
}

function seedOwn(theme: string, locale: string): void {
  localStorage.setItem("openccu-loom.prefs.v1", JSON.stringify({ theme, locale }));
}

function shellMessage(source: unknown, data: unknown, origin = window.location.origin): void {
  window.dispatchEvent(new MessageEvent("message", { data, origin, source: source as never }));
}

const isDark = () => document.documentElement.classList.contains("dark");

beforeEach(() => {
  resetFraming();
  localStorage.clear();
  document.documentElement.className = "";
  document.documentElement.removeAttribute("data-skin");
  document.documentElement.removeAttribute("style");
});

afterEach(() => {
  resetFraming();
  history.replaceState(null, "", "/");
  for (const name of ["ol-theme", "ol-lang"]) {
    document.cookie = `${name}=; path=/; max-age=0`;
  }
  osDark(false);
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

  it("applyTheme forces skin to 'ha' when embedded in HA, regardless of the stored value", async () => {
    vi.resetModules();
    const { prefs, applyTheme, setSkin } = await import("./preferences.svelte");
    setSkin("loom");

    history.replaceState(null, "", "/api/hassio_ingress/tok3n/app/");
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    applyTheme();
    expect(document.documentElement.dataset.skin).toBe("ha");
    expect(prefs.skin).toBe("loom");
  });
});

describe("preferences: standalone (no parent)", () => {
  it("ignores theme= / lang= on the URL and the shell cookies", async () => {
    osDark(true);
    seedOwn("light", "en");
    history.replaceState(null, "", "/app/?theme=dark&lang=de");
    document.cookie = "ol-theme=dark; path=/";
    vi.resetModules();
    const { prefs, applyTheme } = await import("./preferences.svelte");
    expect(prefs.theme).toBe("light");
    expect(prefs.locale).toBe("en");
    applyTheme();
    expect(isDark()).toBe(false);
  });

  it("resolves 'system' via prefers-color-scheme and persists setTheme as before", async () => {
    osDark(true);
    vi.resetModules();
    const { prefs, applyTheme, setTheme, setLocale } = await import("./preferences.svelte");
    expect(prefs.theme).toBe("system");
    applyTheme();
    expect(isDark()).toBe(true);
    setTheme("light");
    setLocale("de");
    expect(isDark()).toBe(false);
    expect(storedPrefs()).toMatchObject({ theme: "light", locale: "de" });
  });
});

describe("preferences: openccu-lite shell", () => {
  it("applies theme= / lang= from the frame URL at load, before any render", async () => {
    osDark(true);
    seedOwn("dark", "en");
    frameSameOrigin();
    history.replaceState(null, "", "/addons/loom/app/?sid=@x@&theme=light&lang=de");
    vi.resetModules();
    const { prefs, applyTheme } = await import("./preferences.svelte");
    // Already effective at import time — the first render sees the shell's look.
    expect(prefs.theme).toBe("light");
    expect(prefs.locale).toBe("de");
    applyTheme();
    expect(isDark()).toBe(false);
    // Not Home Assistant: the operator's skin, not the forced HA one.
    expect(document.documentElement.dataset.skin).toBe("loom");
    // Nothing of the shell's look was written into the own preference.
    expect(storedPrefs()).toMatchObject({ theme: "dark", locale: "en" });
  });

  it("resolves the shell's 'system' via prefers-color-scheme", async () => {
    osDark(true);
    seedOwn("light", "en");
    frameSameOrigin();
    history.replaceState(null, "", "/app/?theme=system&lang=en");
    vi.resetModules();
    const { applyTheme } = await import("./preferences.svelte");
    applyTheme();
    expect(isDark()).toBe(true);
  });

  it("falls back to the ol-theme / ol-lang cookies without URL or message", async () => {
    frameSameOrigin();
    document.cookie = "ol-theme=dark; path=/";
    document.cookie = "ol-lang=en; path=/";
    seedOwn("light", "de");
    vi.resetModules();
    const { prefs, applyTheme } = await import("./preferences.svelte");
    expect(prefs.theme).toBe("dark");
    expect(prefs.locale).toBe("en");
    applyTheme();
    expect(isDark()).toBe(true);
  });

  it("switches theme and language on a valid same-origin message, never persisting it", async () => {
    seedOwn("system", "en");
    const parent = frameSameOrigin();
    history.replaceState(null, "", "/app/?theme=light&lang=en");
    vi.resetModules();
    const { prefs, bindShellLook, setExpertMode } = await import("./preferences.svelte");
    const stop = bindShellLook();

    shellMessage(parent, { type: "openccu-lite:theme", theme: "dark", lang: "de" });
    expect(prefs.theme).toBe("dark");
    expect(prefs.locale).toBe("de");
    expect(isDark()).toBe(true);

    // Persisting another preference must not leak the shell's look.
    setExpertMode(true);
    expect(storedPrefs()).toMatchObject({ theme: "system", locale: "en", expertMode: true });
    stop();
  });

  it("ignores a message from another origin, another sender or in another shape", async () => {
    const parent = frameSameOrigin();
    history.replaceState(null, "", "/app/?theme=light&lang=en");
    vi.resetModules();
    const { prefs, bindShellLook } = await import("./preferences.svelte");
    const stop = bindShellLook();

    const msg = { type: "openccu-lite:theme", theme: "dark", lang: "de" };
    shellMessage(parent, msg, "https://evil.example");
    shellMessage({}, msg);
    shellMessage(parent, { type: "set-theme", payload: { theme: "dark" } });
    shellMessage(parent, { type: "openccu-lite:theme", theme: "auto" });
    expect(prefs.theme).toBe("light");
    expect(prefs.locale).toBe("en");
    stop();
  });

  it("keeps the own choice while following, so it shows again standalone", async () => {
    seedOwn("system", "en");
    frameSameOrigin();
    history.replaceState(null, "", "/app/?theme=dark&lang=de");
    vi.resetModules();
    const { prefs, setTheme } = await import("./preferences.svelte");
    setTheme("light");
    expect(prefs.theme).toBe("dark");
    expect(storedPrefs()).toMatchObject({ theme: "light" });
  });

  it("regression: a manual theme change in the lite frame takes effect where the shell names no theme", async () => {
    osDark(true);
    seedOwn("system", "en");
    frameSameOrigin();
    // The shell named only the language.
    history.replaceState(null, "", "/app/?lang=de");
    vi.resetModules();
    const { prefs, applyTheme, setTheme } = await import("./preferences.svelte");
    applyTheme();
    expect(isDark()).toBe(true);
    setTheme("light");
    expect(prefs.theme).toBe("light");
    expect(isDark()).toBe(false);
    expect(prefs.locale).toBe("de");
  });
});

describe("preferences: unknown iframe", () => {
  it("behaves as standalone — own theme applies, no forced skin", async () => {
    osDark(true);
    seedOwn("system", "en");
    frameSameOrigin();
    vi.resetModules();
    const { applyTheme, setTheme } = await import("./preferences.svelte");
    applyTheme();
    expect(isDark()).toBe(true);
    expect(document.documentElement.dataset.skin).toBe("loom");
    setTheme("light");
    expect(isDark()).toBe(false);
  });

  it("switches to following when a shell message arrives later", async () => {
    const parent = frameSameOrigin();
    seedOwn("light", "en");
    vi.resetModules();
    const { prefs, bindShellLook } = await import("./preferences.svelte");
    const { liteShell } = await import("$lib/theme/lite-shell.svelte");
    expect(liteShell.active).toBe(false);
    const stop = bindShellLook();
    shellMessage(parent, { type: "openccu-lite:theme", theme: "dark", lang: "de" });
    expect(liteShell.active).toBe(true);
    expect(prefs.theme).toBe("dark");
    expect(isDark()).toBe(true);
    stop();
  });
});

describe("preferences: Home Assistant ingress", () => {
  it("is unchanged — HA skin forced, .dark left to the HA bridge", async () => {
    osDark(false);
    seedOwn("light", "en");
    history.replaceState(null, "", "/api/hassio_ingress/tok3n/app/");
    Object.defineProperty(window, "top", { value: {}, configurable: true });
    document.documentElement.classList.add("dark");
    vi.resetModules();
    const { applyTheme, setTheme } = await import("./preferences.svelte");
    applyTheme();
    expect(document.documentElement.dataset.skin).toBe("ha");
    setTheme("light");
    // applyTheme does not fight the bridge's .dark under HA.
    expect(isDark()).toBe(true);
  });
});
