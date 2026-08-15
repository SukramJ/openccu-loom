// @vitest-environment happy-dom
import { describe, it, expect, afterEach } from "vitest";
import { catalogKeys, t } from "$lib/i18n";
import { prefs } from "$lib/stores/preferences.svelte";

// t()'s fallback chain (active locale -> English -> raw key) means a
// component call site can look correct while the catalogue silently lacks
// the key: nothing throws, the raw key string is rendered to the operator
// instead. A test that only asserts a component called t("some.key") -
// against a mocked t() that echoes its argument - proves the call site
// exists, not that either catalogue actually resolves it. These cases
// exercise the real catalogues so a missing entry fails here instead of
// surfacing as a literal dotted key on screen.
//
// The same fallback is why the per-locale cases below ask the catalogue
// directly instead of asserting on t(): with the German entry deleted,
// t() hands back the English string, which still differs from the key —
// so a t()-phrased assertion cannot fail for the locale it names.
describe("i18n catalogue coverage", () => {
  const originalLocale = prefs.locale;

  afterEach(() => {
    prefs.locale = originalLocale;
  });

  const de = new Set(catalogKeys("de"));
  const en = new Set(catalogKeys("en"));

  // RoomsFunctionsAdmin.svelte's saveAssign() success toast.
  it("resolves areas.toast.rooms_saved in both locales", () => {
    prefs.locale = "en";
    expect(t("areas.toast.rooms_saved")).not.toBe("areas.toast.rooms_saved");
    expect(de.has("areas.toast.rooms_saved")).toBe(true);
  });

  // Favorites.svelte's program-card run button label / running state.
  it("resolves programs.run and programs.running in both locales", () => {
    prefs.locale = "en";
    expect(t("programs.run")).not.toBe("programs.run");
    expect(t("programs.running")).not.toBe("programs.running");
    expect(de.has("programs.run")).toBe(true);
    expect(de.has("programs.running")).toBe(true);
  });

  // Every user-visible string ships in both locales (CLAUDE.md, SPA
  // operating concept). A key added to one catalogue only degrades to the
  // other language on screen instead of failing anywhere, so nothing but
  // a key-set comparison catches it.
  it("defines the same keys in both catalogues", () => {
    const missingInDE = [...en].filter((k) => !de.has(k)).sort();
    const missingInEN = [...de].filter((k) => !en.has(k)).sort();
    expect({ missingInDE, missingInEN }).toEqual({
      missingInDE: [],
      missingInEN: [],
    });
  });
});
