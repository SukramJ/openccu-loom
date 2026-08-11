import { describe, expect, it } from "vitest";
import type {
  UnIgnoreCandidateGroup,
  UnIgnoreReason,
} from "$lib/api/visibility-types";
import {
  activeScopeCount,
  defaultReasonFilter,
  emptyFilter,
  filterGroups,
  groupKey,
  groupPatterns,
  groupState,
  isNoiseReason,
  NOISE_REASONS,
  orphanPatterns,
  presentReasons,
  reasonBadgeText,
  reasonCounts,
  suppressedCount,
  toggleGroup,
  togglePattern,
} from "./candidates";

/** Build a VALUES group with the three pattern forms the daemon emits. */
function valuesGroup(
  parameter: string,
  reason: UnIgnoreReason,
  models: Array<{ model: string; channels: number[] }>,
): UnIgnoreCandidateGroup {
  return {
    parameter,
    paramset: "VALUES",
    reason,
    reasons: [reason],
    simple_pattern: parameter,
    device_count: models.length,
    channel_count: models.reduce((n, m) => n + m.channels.length, 0),
    models: models.map((m) => ({
      model: m.model,
      wildcard_pattern: `${parameter}:VALUES@${m.model}:all`,
      device_count: 1,
      channels: m.channels.map((c) => ({
        channel: c,
        pattern: `${parameter}:VALUES@${m.model}:${c}`,
      })),
    })),
  };
}

/** MASTER groups carry neither the simple nor the wildcard form. */
function masterGroup(
  parameter: string,
  model: string,
  channels: number[],
): UnIgnoreCandidateGroup {
  return {
    parameter,
    paramset: "MASTER",
    reason: "master_gate",
    reasons: ["master_gate"],
    device_count: 1,
    channel_count: channels.length,
    models: [
      {
        model,
        device_count: 1,
        channels: channels.map((c) => ({
          channel: c,
          pattern: `${parameter}:MASTER@${model}:${c}`,
        })),
      },
    ],
  };
}

const lowBat = valuesGroup("LOW_BAT", "hidden", [
  { model: "HmIP-eTRV-2", channels: [0, 1] },
  { model: "HmIP-SWDO", channels: [0] },
]);
const errBit = valuesGroup("ERR_TTM_INTERNAL", "internal_flag", [
  { model: "HM-Sec-Win", channels: [1] },
]);
const tempOffset = masterGroup("TEMPERATURE_OFFSET", "HmIP-BWTH", [1]);

describe("groupPatterns", () => {
  it("collects every pattern form of a VALUES group", () => {
    expect(groupPatterns(lowBat)).toEqual([
      "LOW_BAT",
      "LOW_BAT:VALUES@HmIP-eTRV-2:all",
      "LOW_BAT:VALUES@HmIP-eTRV-2:0",
      "LOW_BAT:VALUES@HmIP-eTRV-2:1",
      "LOW_BAT:VALUES@HmIP-SWDO:all",
      "LOW_BAT:VALUES@HmIP-SWDO:0",
    ]);
  });

  it("omits the VALUES-only forms for a MASTER group", () => {
    expect(groupPatterns(tempOffset)).toEqual([
      "TEMPERATURE_OFFSET:MASTER@HmIP-BWTH:1",
    ]);
  });
});

describe("groupState", () => {
  it("is off when no pattern of the group is active", () => {
    expect(groupState(lowBat, new Set())).toBe("off");
    expect(groupState(lowBat, new Set(["OTHER_PARAM"]))).toBe("off");
  });

  it("is all when the fleet-wide form is active", () => {
    expect(groupState(lowBat, new Set(["LOW_BAT"]))).toBe("all");
  });

  it("is partial when only a narrower scope is active", () => {
    expect(
      groupState(lowBat, new Set(["LOW_BAT:VALUES@HmIP-SWDO:0"])),
    ).toBe("partial");
    expect(
      groupState(lowBat, new Set(["LOW_BAT:VALUES@HmIP-eTRV-2:all"])),
    ).toBe("partial");
  });

  it("counts the active scopes", () => {
    const active = new Set([
      "LOW_BAT:VALUES@HmIP-SWDO:0",
      "LOW_BAT:VALUES@HmIP-eTRV-2:1",
    ]);
    expect(activeScopeCount(lowBat, active)).toBe(2);
  });
});

describe("groupKey", () => {
  it("separates the same parameter in VALUES and MASTER", () => {
    const valuesTwin = valuesGroup("TEMPERATURE_OFFSET", "hidden", [
      { model: "HmIP-BWTH", channels: [1] },
    ]);
    expect(groupKey(tempOffset)).not.toBe(groupKey(valuesTwin));
  });
});

describe("toggleGroup", () => {
  it("turns an off group on fleet-wide", () => {
    expect(toggleGroup(lowBat, [])).toEqual(["LOW_BAT"]);
  });

  it("clears every scope of an enabled group", () => {
    const before = [
      "LOW_BAT:VALUES@HmIP-SWDO:0",
      "LOW_BAT:VALUES@HmIP-eTRV-2:all",
      "OTHER_PARAM",
    ];
    expect(toggleGroup(lowBat, before)).toEqual(["OTHER_PARAM"]);
  });

  it("leaves patterns of other groups untouched", () => {
    const next = toggleGroup(lowBat, ["ERR_TTM_INTERNAL"]);
    expect(next).toContain("ERR_TTM_INTERNAL");
    expect(next).toContain("LOW_BAT");
  });

  it("enables every model wildcard for a group without a simple form", () => {
    // MASTER has no fleet-wide and no wildcard form, so the row toggle
    // falls through to the channel patterns.
    expect(toggleGroup(tempOffset, [])).toEqual([
      "TEMPERATURE_OFFSET:MASTER@HmIP-BWTH:1",
    ]);
  });
});

describe("togglePattern", () => {
  it("adds a scope pattern", () => {
    expect(togglePattern(lowBat, "LOW_BAT:VALUES@HmIP-SWDO:0", [])).toEqual([
      "LOW_BAT:VALUES@HmIP-SWDO:0",
    ]);
  });

  it("removes an active scope pattern", () => {
    expect(
      togglePattern(lowBat, "LOW_BAT:VALUES@HmIP-SWDO:0", [
        "LOW_BAT:VALUES@HmIP-SWDO:0",
        "OTHER",
      ]),
    ).toEqual(["OTHER"]);
  });

  it("drops the fleet-wide form when a narrower scope is picked", () => {
    // Otherwise the list would claim "all devices" while the operator
    // just chose one model, and the narrower tick would be a no-op.
    const next = togglePattern(lowBat, "LOW_BAT:VALUES@HmIP-SWDO:0", [
      "LOW_BAT",
    ]);
    expect(next).toEqual(["LOW_BAT:VALUES@HmIP-SWDO:0"]);
  });

  it("keeps the fleet-wide form when it is the pattern being toggled", () => {
    expect(togglePattern(lowBat, "LOW_BAT", [])).toEqual(["LOW_BAT"]);
  });
});

describe("filterGroups", () => {
  const groups = [lowBat, errBit, tempOffset];

  it("matches the parameter name case- and separator-insensitively", () => {
    const f = { ...emptyFilter(), query: "low bat" };
    expect(filterGroups(groups, f, new Set()).map((g) => g.parameter)).toEqual([
      "LOW_BAT",
    ]);
  });

  it("matches a device model", () => {
    const f = { ...emptyFilter(), query: "swdo" };
    expect(filterGroups(groups, f, new Set()).map((g) => g.parameter)).toEqual([
      "LOW_BAT",
    ]);
  });

  it("matches the translated label", () => {
    const labelled = { ...lowBat, label: "Batteriezustand" };
    const f = { ...emptyFilter(), query: "batterie" };
    expect(filterGroups([labelled], f, new Set())).toHaveLength(1);
  });

  it("restricts to the selected reasons", () => {
    const f = { ...emptyFilter(), reasons: new Set(["hidden"]) };
    expect(filterGroups(groups, f, new Set()).map((g) => g.parameter)).toEqual([
      "LOW_BAT",
    ]);
  });

  it("treats an empty reason set as no filter", () => {
    expect(filterGroups(groups, emptyFilter(), new Set())).toHaveLength(3);
  });

  it("restricts to the selected paramsets", () => {
    const f = { ...emptyFilter(), paramsets: new Set(["MASTER"]) };
    expect(filterGroups(groups, f, new Set()).map((g) => g.parameter)).toEqual([
      "TEMPERATURE_OFFSET",
    ]);
  });

  it("restricts to enabled groups", () => {
    const f = { ...emptyFilter(), onlyEnabled: true };
    const active = new Set(["LOW_BAT"]);
    expect(filterGroups(groups, f, active).map((g) => g.parameter)).toEqual([
      "LOW_BAT",
    ]);
  });
});

describe("reasonCounts", () => {
  it("keeps every chip's count while one chip is selected", () => {
    // A chip that reads 0 because it is unselected is unusable: the
    // operator cannot tell an empty category from a filtered-out one.
    const groups = [lowBat, errBit, tempOffset];
    const f = { ...emptyFilter(), reasons: new Set(["hidden"]) };
    const counts = reasonCounts(groups, f, new Set());
    expect(counts.get("hidden")).toBe(1);
    expect(counts.get("internal_flag")).toBe(1);
    expect(counts.get("master_gate")).toBe(1);
  });

  it("still honours the other filters", () => {
    const groups = [lowBat, errBit, tempOffset];
    const f = { ...emptyFilter(), paramsets: new Set(["VALUES"]) };
    const counts = reasonCounts(groups, f, new Set());
    expect(counts.get("master_gate")).toBeUndefined();
  });
});

describe("defaultReasonFilter", () => {
  it("hides the noise categories on first open", () => {
    const filter = defaultReasonFilter([lowBat, errBit, tempOffset]);
    expect(filter.has("hidden")).toBe(true);
    expect(filter.has("master_gate")).toBe(true);
    expect(filter.has("internal_flag")).toBe(false);
  });

  it("shows everything when every category is noise", () => {
    // Suppressing the whole list would leave an empty screen with no
    // hint that anything exists.
    const filter = defaultReasonFilter([errBit]);
    expect(filter.size).toBe(0);
    expect(filterGroups([errBit], { ...emptyFilter(), reasons: filter }, new Set())).toHaveLength(1);
  });

  it("classifies every noise reason as noise", () => {
    for (const r of NOISE_REASONS) expect(isNoiseReason(r)).toBe(true);
    expect(isNoiseReason("hidden")).toBe(false);
  });

  it("hides week-profile cells but keeps ordinary MASTER settings", () => {
    // A single climate device contributes hundreds of profile cells and
    // they already have a schedule editor, so they must not compete for
    // attention with the MASTER knobs an operator might actually tune.
    const cell = masterGroup("P1_ENDTIME_MONDAY_1", "HmIP-eTRV-2", [1]);
    cell.reason = "week_profile";
    cell.reasons = ["week_profile", "master_gate"];
    const filter = defaultReasonFilter([cell, tempOffset]);
    expect(filter.has("week_profile")).toBe(false);
    expect(filter.has("master_gate")).toBe(true);

    const visible = filterGroups(
      [cell, tempOffset],
      { ...emptyFilter(), reasons: filter },
      new Set(),
    );
    expect(visible.map((g) => g.parameter)).toEqual(["TEMPERATURE_OFFSET"]);
  });
});

describe("suppressedCount", () => {
  it("reports how many groups the category filter hides", () => {
    const groups = [lowBat, errBit, tempOffset];
    const f = { ...emptyFilter(), reasons: defaultReasonFilter(groups) };
    expect(suppressedCount(groups, f, new Set())).toBe(1);
  });

  it("is zero when no category filter is active", () => {
    const groups = [lowBat, errBit];
    expect(suppressedCount(groups, emptyFilter(), new Set())).toBe(0);
  });
});

describe("presentReasons", () => {
  it("orders known reasons and appends unknown server reasons", () => {
    const exotic = { ...errBit, reason: "brand_new_rule" as UnIgnoreReason };
    const order = presentReasons([tempOffset, lowBat, exotic], [
      "master_gate",
      "hidden",
      "brand_new_rule",
    ]);
    expect(order.slice(0, 2)).toEqual(["master_gate", "hidden"]);
    expect(order).toContain("brand_new_rule");
  });

  it("lists only reasons that occur", () => {
    expect(presentReasons([lowBat])).toEqual(["hidden"]);
  });
});

describe("orphanPatterns", () => {
  it("surfaces saved patterns that no candidate explains", () => {
    const saved = ["LOW_BAT", "TYPO:VALUES@HmIP-XXX:9"];
    expect(orphanPatterns([lowBat], saved)).toEqual(["TYPO:VALUES@HmIP-XXX:9"]);
  });

  it("returns nothing when every pattern is covered", () => {
    expect(orphanPatterns([lowBat], ["LOW_BAT:VALUES@HmIP-SWDO:0"])).toEqual([]);
  });
});

describe("reasonBadgeText", () => {
  // The catalogue is stubbed so the test pins the fallback logic rather
  // than the wording of any one locale.
  const t = (key: string, vars?: Record<string, string>) => {
    const catalogue: Record<string, string> = {
      "unignore.reason.wildcard_prefix": "Name prefix",
      "unignore.reason.wildcard_suffix": "Name suffix",
      "unignore.reason.ignore_list": "Excluded",
      "unignore.reason_detail.wildcard_prefix": "Prefix {pattern}",
      "unignore.reason_detail.wildcard_suffix": "Suffix {pattern}",
    };
    const raw = catalogue[key];
    if (raw === undefined) return key;
    return vars
      ? raw.replace(/\{(\w+)\}/g, (_, k: string) => vars[k] ?? `{${k}}`)
      : raw;
  };

  it("names the matched pattern when the server supplies one", () => {
    expect(
      reasonBadgeText(
        { reason: "wildcard_prefix", reason_detail: "STATUS_FLAG_" },
        t,
      ),
    ).toBe("Prefix STATUS_FLAG_");
    expect(
      reasonBadgeText({ reason: "wildcard_suffix", reason_detail: "_STATUS" }, t),
    ).toBe("Suffix _STATUS");
  });

  it("falls back to the category when no detail is supplied", () => {
    expect(reasonBadgeText({ reason: "wildcard_prefix" }, t)).toBe("Name prefix");
    expect(reasonBadgeText({ reason: "ignore_list" }, t)).toBe("Excluded");
  });

  it("falls back rather than rendering a raw key for an uncatalogued detail", () => {
    // A daemon that grows a detail for a reason the catalogue does not
    // cover yet must not leak "unignore.reason_detail.x" into the badge.
    expect(
      reasonBadgeText({ reason: "ignore_list", reason_detail: "BOOTED" }, t),
    ).toBe("Excluded");
  });
});
