// @vitest-environment happy-dom
import { describe, it, expect, afterEach, vi } from "vitest";

import {
  detectInitialLook,
  listenForShellLook,
  parseShellLang,
  parseShellMessage,
  readCookieLook,
  readUrlLook,
} from "./lite-shell.svelte";

function resetFraming(): void {
  Object.defineProperty(window, "top", { value: window, configurable: true });
  Object.defineProperty(window, "parent", { value: window, configurable: true });
}

// A same-origin parent, as the openccu-lite shell is.
function frameSameOrigin(): Record<string, unknown> {
  const parent = { location: { origin: window.location.origin } };
  Object.defineProperty(window, "top", { value: parent, configurable: true });
  Object.defineProperty(window, "parent", { value: parent, configurable: true });
  return parent;
}

function clearCookies(): void {
  for (const name of ["ol-theme", "ol-lang"]) {
    document.cookie = `${name}=; path=/; max-age=0`;
  }
}

afterEach(() => {
  resetFraming();
  clearCookies();
  history.replaceState(null, "", "/");
});

describe("parsers", () => {
  it("reads theme and lang off the frame URL", () => {
    expect(readUrlLook("?sid=@x@&theme=dark&lang=de")).toEqual({ theme: "dark", lang: "de" });
    expect(readUrlLook("?theme=purple&lang=fr")).toEqual({ theme: null, lang: null });
    expect(readUrlLook("")).toEqual({ theme: null, lang: null });
  });

  it("normalises the shell's language tag", () => {
    expect(parseShellLang("de")).toBe("de");
    expect(parseShellLang("en-US")).toBe("en");
    expect(parseShellLang("fr")).toBeNull();
    expect(parseShellLang(42)).toBeNull();
  });

  it("reads the ol-theme / ol-lang cookies, ol-theme only resolved", () => {
    expect(readCookieLook("a=1; ol-theme=light; ol-lang=en")).toEqual({
      theme: "light",
      lang: "en",
    });
    // The cookie carries `system` already resolved; anything else is ignored.
    expect(readCookieLook("ol-theme=system")).toEqual({ theme: null, lang: null });
  });

  it("accepts only the contract's message shape", () => {
    expect(parseShellMessage({ type: "openccu-lite:theme", theme: "light", lang: "de" })).toEqual({
      theme: "light",
      lang: "de",
    });
    expect(parseShellMessage({ type: "openccu-lite:theme", theme: "light" })).toEqual({
      theme: "light",
      lang: null,
    });
    expect(parseShellMessage({ type: "set-theme", payload: { theme: "auto" } })).toBeNull();
    expect(parseShellMessage({ type: "openccu-lite:theme", theme: "auto" })).toBeNull();
    expect(parseShellMessage("openccu-lite:theme")).toBeNull();
    expect(parseShellMessage(null)).toBeNull();
  });
});

describe("detectInitialLook", () => {
  it("is null standalone, even with theme= on the URL", () => {
    history.replaceState(null, "", "/app/?theme=dark&lang=de");
    expect(detectInitialLook()).toBeNull();
  });

  it("is null under a cross-origin parent", () => {
    history.replaceState(null, "", "/app/?theme=dark&lang=de");
    const parent = {
      get location(): never {
        throw new DOMException("cross-origin");
      },
    };
    Object.defineProperty(window, "parent", { value: parent, configurable: true });
    expect(detectInitialLook()).toBeNull();
  });

  it("takes the URL under a same-origin parent, cookies filling the gaps", () => {
    frameSameOrigin();
    document.cookie = "ol-theme=dark; path=/";
    document.cookie = "ol-lang=en; path=/";
    history.replaceState(null, "", "/app/?theme=light");
    expect(detectInitialLook()).toEqual({ theme: "light", lang: "en" });
  });

  it("is null in a same-origin frame without any shell signal", () => {
    frameSameOrigin();
    expect(detectInitialLook()).toBeNull();
  });
});

describe("listenForShellLook", () => {
  it("passes on a valid message from the same-origin parent only", () => {
    const parent = frameSameOrigin();
    const onLook = vi.fn();
    const stop = listenForShellLook(onLook);
    const msg = { type: "openccu-lite:theme", theme: "dark", lang: "de" };

    window.dispatchEvent(
      new MessageEvent("message", { data: msg, origin: "https://evil.example", source: parent as never }),
    );
    window.dispatchEvent(
      new MessageEvent("message", { data: msg, origin: window.location.origin, source: null }),
    );
    window.dispatchEvent(
      new MessageEvent("message", {
        data: { type: "other", theme: "dark" },
        origin: window.location.origin,
        source: parent as never,
      }),
    );
    expect(onLook).not.toHaveBeenCalled();

    window.dispatchEvent(
      new MessageEvent("message", { data: msg, origin: window.location.origin, source: parent as never }),
    );
    expect(onLook).toHaveBeenCalledWith({ theme: "dark", lang: "de" });

    stop();
  });
});
