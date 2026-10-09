import { describe, it, expect } from "vitest";
import type { UISchemaProfile } from "$lib/api/types";
import { activeVariant, profilePatch, profileVariants } from "./link-profiles";

// Shaped like the archive's SWITCH_VIRTUAL_RECEIVER / KEY_TRANSCEIVER
// group: profile 0 is "Experte" without parameters.
const PROFILE: UISchemaProfile = {
  receiver_type: "SWITCH_VIRTUAL_RECEIVER",
  sender_type: "KEY_TRANSCEIVER",
  active_profile_id: 3,
  raw: {
    KEY_TRANSCEIVER: {
      profiles: [
        { id: 0, name: { en: "Expert", de: "Experte" }, params: {} },
        {
          id: 3,
          name: { en: "Switch on / off", de: "Schalter ein / aus" },
          description: { de: "Toggle" },
          params: {
            SHORT_ON_TIME_MODE: { constraint_type: "fixed", value: 0 },
            SHORT_ON_TIME_BASE: { constraint_type: "range", default: 7, min_value: 0, max_value: 7 },
            SHORT_JT_ON: { constraint_type: "list", values: [1, 3] },
          },
        },
      ],
    },
    OTHER_SENDER: { profiles: [{ id: 1, name: { en: "Other" }, params: {} }] },
  },
};

describe("profileVariants", () => {
  it("lists only the link's sender group, localized", () => {
    const v = profileVariants(PROFILE, "de");
    expect(v.map((x) => [x.id, x.label])).toEqual([
      [0, "Experte"],
      [3, "Schalter ein / aus"],
    ]);
  });

  it("names the sender when the sender type is unknown", () => {
    const v = profileVariants({ ...PROFILE, sender_type: undefined }, "en");
    expect(v.map((x) => x.label)).toContain("Other (OTHER_SENDER)");
  });
});

describe("activeVariant", () => {
  const v = profileVariants(PROFILE, "de");
  it("selects the matched profile", () => {
    expect(activeVariant(v, 3)?.id).toBe(3);
  });
  it("falls back to Experte when nothing matched or the id is unknown", () => {
    expect(activeVariant(v, 0)?.id).toBe(0);
    expect(activeVariant(v, 42)?.id).toBe(0);
    expect(activeVariant(v, undefined)?.id).toBe(0);
  });
});

describe("profilePatch", () => {
  it("pins fixed values, stages range defaults, leaves list values alone", () => {
    const variant = profileVariants(PROFILE, "de")[1];
    expect(profilePatch(variant)).toEqual({
      patch: { SHORT_ON_TIME_MODE: 0, SHORT_ON_TIME_BASE: 7 },
      fixed: ["SHORT_ON_TIME_MODE"],
      editable: ["SHORT_ON_TIME_BASE", "SHORT_JT_ON"],
    });
  });
});
