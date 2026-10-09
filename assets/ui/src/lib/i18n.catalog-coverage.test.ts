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

  // ChannelTable.svelte's column headers, role labels and status chips, plus
  // the channel-editor heading channel-roles.ts composes. The table renders
  // every one of these on a device page, so a key missing from one catalogue
  // shows as a dotted literal in a column header.
  it("resolves the channel-table keys in both locales", () => {
    prefs.locale = "en";
    const keys = [
      "device.channels.col.number",
      "device.channels.col.name",
      "device.channels.col.description",
      "device.channels.col.type",
      "device.channels.col.role",
      "device.channels.col.datapoints",
      "device.channels.col.links",
      "device.channels.col.status",
      "device.channels.search",
      "device.channel.role.sender",
      "device.channel.role.receiver",
      "device.channel.role.both",
      "device.channel.role.none",
      "device.channel.header",
      "device.channel.chip.hidden",
      "device.channel.chip.locked",
      "device.channel.chip.virtual",
      "device.channel.chip.week_profile",
      "device.channel.chip.group",
    ];
    for (const key of keys) {
      expect(t(key)).not.toBe(key);
      expect(de.has(key), `${key} missing in de`).toBe(true);
      expect(en.has(key), `${key} missing in en`).toBe(true);
    }
  });

  // The write preview, the read-back report and the two new preference
  // controls. The preview dialog is the surface an operator reads right
  // before a configuration write lands, so a dotted literal in it is worse
  // than in most places.
  it("resolves the write-preview and preference keys in both locales", () => {
    prefs.locale = "en";
    const keys = [
      "channel.preview.title",
      "channel.preview.request",
      "channel.preview.body",
      "channel.preview.col.parameter",
      "channel.preview.col.from",
      "channel.preview.col.to",
      "channel.preview.write",
      "channel.preview.nothing_to_write",
      "channel.readback.title",
      "channel.readback.body",
      "channel.readback.chip",
      "parameter.default",
      "settings.prefs.write_preview",
      "settings.prefs.write_preview_help",
      "settings.prefs.param_density",
      "settings.prefs.density.compact",
      "settings.prefs.density.comfortable",
    ];
    for (const key of keys) {
      expect(t(key)).not.toBe(key);
      expect(de.has(key), `${key} missing in de`).toBe(true);
      expect(en.has(key), `${key} missing in en`).toBe(true);
    }
  });

  // The device-list table's filter row and expandable channel rows.
  it("resolves the device-table keys in both locales", () => {
    prefs.locale = "en";
    const keys = [
      "datatable.filter_by",
      "datatable.filter_all",
      "datatable.expand",
      "datatable.collapse",
      "datatable.details",
      "devicelist.channels_failed",
      "devicelist.status_reachable",
      "devicelist.status_unreachable",
    ];
    for (const key of keys) {
      expect(t(key)).not.toBe(key);
      expect(de.has(key), `${key} missing in de`).toBe(true);
      expect(en.has(key), `${key} missing in en`).toBe(true);
    }
  });

  // Sidebar.svelte / ChangePasswordCard.svelte's box-shell (scheme
  // occulite) identity line and its explanation.
  it("resolves the box-shell sign-in keys in both locales", () => {
    for (const key of [
      "auth.box_shell.signed_in",
      "auth.box_shell.signed_in_help",
    ]) {
      expect(de.has(key), `${key} missing in de`).toBe(true);
      expect(en.has(key), `${key} missing in en`).toBe(true);
    }
  });

  // LinkEditor.svelte, the link-profile picker and the keypress table.
  // The page is the one place a direct link is edited, so a dotted literal
  // in its save bar or panel headings stands in front of every link write.
  it("resolves the link page keys in both locales", () => {
    for (const key of [
      "links.edit",
      "links.add.create_and_edit",
      "links.editor.title",
      "links.editor.link",
      "links.editor.sender_profile",
      "links.editor.receiver_profile",
      "links.editor.sender_empty",
      "links.editor.channel_params",
      "links.editor.delete",
      "links.editor.not_found",
      "links.editor.pending",
      "links.editor.pending_hint",
      "links.editor.apply",
      "links.editor.saved",
      "links.editor.sender",
      "links.editor.receiver",
      "links.editor.meta",
      "links.editor.partial_title",
      "links.editor.partial_body",
      "profile.expert_hint",
      "profile.no_settings",
      "profile.hidden_count",
      "page.title.link_editor",
    ]) {
      expect(de.has(key), `${key} missing in de`).toBe(true);
      expect(en.has(key), `${key} missing in en`).toBe(true);
    }
  });

  // LinkTable.svelte (both link lists) and LinkWizard.svelte.
  it("resolves the link list and wizard keys in both locales", () => {
    for (const key of [
      "links.col.serial",
      "links.col.action",
      "links.col.model",
      "links.col.room",
      "links.col.function",
      "links.col.category",
      "links.group_by",
      "links.group.none",
      "links.group.sender",
      "links.group.receiver",
      "links.add_receiver",
      "links.add_sender",
      "links.no_description",
      "links.new",
      "links.wizard.crumb",
      "links.wizard.step1",
      "links.wizard.step2",
      "links.wizard.step3",
      "links.wizard.step1_hint",
      "links.wizard.step2_hint",
      "links.wizard.pick",
      "links.wizard.pick_as_sender",
      "links.wizard.pick_as_receiver",
      "links.wizard.no_linkable_channels",
      "links.wizard.no_devices",
      "links.wizard.not_chosen",
      "links.wizard.default_name",
      "links.wizard.default_description",
      "links.wizard.exists",
      "links.wizard.create_failed",
      "links.wizard.all_devices",
      "links.wizard.show_virtual",
      "links.wizard.hide_virtual",
      "page.title.link_wizard",
    ]) {
      expect(de.has(key), `${key} missing in de`).toBe(true);
      expect(en.has(key), `${key} missing in en`).toBe(true);
    }
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
