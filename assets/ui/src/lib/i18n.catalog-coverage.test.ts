// @vitest-environment happy-dom
import { describe, it, expect, afterEach } from "vitest";
import { t } from "$lib/i18n";
import { prefs } from "$lib/stores/preferences.svelte";

// t()'s fallback chain (active locale -> English -> raw key) means a
// component call site can look correct while the catalogue silently lacks
// the key: nothing throws, the raw key string is rendered to the operator
// instead. A test that only asserts a component called t("some.key") -
// against a mocked t() that echoes its argument - proves the call site
// exists, not that either catalogue actually resolves it. These cases
// exercise the real catalogues so a missing entry fails here instead of
// surfacing as a literal dotted key on screen.
describe("i18n catalogue coverage", () => {
  const originalLocale = prefs.locale;

  afterEach(() => {
    prefs.locale = originalLocale;
  });

  // RoomsFunctionsAdmin.svelte's saveAssign() success toast.
  it("resolves areas.toast.rooms_saved in both locales", () => {
    prefs.locale = "en";
    expect(t("areas.toast.rooms_saved")).not.toBe("areas.toast.rooms_saved");
    prefs.locale = "de";
    expect(t("areas.toast.rooms_saved")).not.toBe("areas.toast.rooms_saved");
  });

  // Favorites.svelte's program-card run button label / running state.
  it("resolves programs.run and programs.running in both locales", () => {
    for (const locale of ["en", "de"] as const) {
      prefs.locale = locale;
      expect(t("programs.run")).not.toBe("programs.run");
      expect(t("programs.running")).not.toBe("programs.running");
    }
  });
});
