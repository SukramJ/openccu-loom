import { describe, it, expect, afterEach } from "vitest";
import {
  roleOf,
  roleLabel,
  isVirtualChannel,
  isWeekProfileChannel,
  channelHeader,
} from "./channel-roles";
import { prefs } from "$lib/stores/preferences.svelte";

describe("roleOf", () => {
  it("reports both sides when both role lists are filled", () => {
    expect(
      roleOf({ link_source_roles: ["SWITCH"], link_target_roles: ["WEATHER"] }),
    ).toBe("both");
  });

  it("reports sender when only the source roles are filled", () => {
    expect(roleOf({ link_source_roles: ["SWITCH"] })).toBe("sender");
  });

  it("reports receiver when only the target roles are filled", () => {
    expect(roleOf({ link_target_roles: ["SWITCH"] })).toBe("receiver");
  });

  // An empty array and an absent key mean the same thing to an operator: the
  // channel cannot be linked on that side. The REST DTO omits the key, but a
  // client generated before API 11.2.0 may send [].
  it("treats an empty array like an absent one", () => {
    expect(roleOf({ link_source_roles: [], link_target_roles: [] })).toBe("none");
    expect(roleOf({})).toBe("none");
  });
});

describe("roleLabel", () => {
  const originalLocale = prefs.locale;
  afterEach(() => {
    prefs.locale = originalLocale;
  });

  it("localises every role", () => {
    prefs.locale = "de";
    expect(roleLabel("sender")).toBe("Sender");
    expect(roleLabel("receiver")).toBe("Empfänger");
    expect(roleLabel("both")).toBe("beides");
    prefs.locale = "en";
    expect(roleLabel("receiver")).toBe("Receiver");
  });
});

describe("isVirtualChannel", () => {
  it("marks channel 50 and up as virtual", () => {
    expect(isVirtualChannel(49)).toBe(false);
    expect(isVirtualChannel(50)).toBe(true);
    expect(isVirtualChannel(51)).toBe(true);
  });
});

describe("isWeekProfileChannel", () => {
  it("matches a type ending in WEEK_PROFILE, case-insensitively", () => {
    expect(isWeekProfileChannel("WEEK_PROFILE")).toBe(true);
    expect(isWeekProfileChannel("HEATING_CLIMATECONTROL_WEEK_PROFILE")).toBe(true);
    expect(isWeekProfileChannel("week_profile")).toBe(true);
    expect(isWeekProfileChannel("SWITCH")).toBe(false);
    expect(isWeekProfileChannel(undefined)).toBe(false);
  });
});

describe("channelHeader", () => {
  const originalLocale = prefs.locale;
  afterEach(() => {
    prefs.locale = originalLocale;
  });

  it("renders label, model and channel number", () => {
    prefs.locale = "de";
    expect(
      channelHeader(
        { number: 12, type: "DOOR_LOCK_TRANSCEIVER", type_label: "Türschlossantrieb" },
        "HmIP-DLP",
      ),
    ).toBe("Türschlossantrieb (HmIP-DLP, Kanal 12)");
  });

  // The channel name belongs to the Name column and the rename field; the
  // heading names the channel *type*, which is what a renamed channel would
  // otherwise stop telling the operator.
  it("names the channel type, not the operator's channel name", () => {
    prefs.locale = "de";
    expect(
      channelHeader(
        { number: 4, type: "SWITCH", type_label: "Schaltaktor" },
        "HmIP-PS",
      ),
    ).toBe("Schaltaktor (HmIP-PS, Kanal 4)");
  });

  // A channel type the translation table does not know still needs a heading.
  it("falls back to the raw type and then to the channel number", () => {
    prefs.locale = "de";
    expect(channelHeader({ number: 2, type: "UNKNOWN_TYPE" }, "HmIP-X")).toBe(
      "UNKNOWN_TYPE (HmIP-X, Kanal 2)",
    );
    expect(channelHeader({ number: 2 }, "HmIP-X")).toBe("Kanal 2 (HmIP-X, Kanal 2)");
  });

  // The device model is unknown on a device the CCU has not described yet;
  // an empty parenthesis pair would read as a rendering fault.
  it("drops the model segment when the model is empty", () => {
    prefs.locale = "de";
    expect(channelHeader({ number: 3, type_label: "Taster" }, "  ")).toBe(
      "Taster (Kanal 3)",
    );
  });
});
