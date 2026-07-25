import { describe, it, expect } from "vitest";
import type { DataPointSummary } from "$lib/api/types";
import { findPrimaryDP } from "./primary";

function dp(parameter: string, overrides: Partial<DataPointSummary> = {}): DataPointSummary {
  return {
    parameter,
    value: 0,
    observed: true,
    operations: { read: false, write: false, event: false },
    unique_id: `dp-${parameter}`,
    ...overrides,
  };
}

describe("findPrimaryDP — empty input", () => {
  it("returns undefined for an empty DP list", () => {
    expect(findPrimaryDP(undefined, [])).toBeUndefined();
  });
});

describe("findPrimaryDP — write-only action channels (e.g. virtual-remote KEY)", () => {
  it("falls back to the single DP when it is the only one, even if write-only", () => {
    // Documented edge case: a channel with exactly one DP always returns
    // it, whether or not it is readable — there is nothing else to show.
    const pressShort = dp("PRESS_SHORT", {
      operations: { read: false, write: true, event: false },
    });
    expect(findPrimaryDP("KEY", [pressShort])).toBe(pressShort);
  });

  it("never promotes a write-only DP to the headline when a readable DP exists", () => {
    const pressShort = dp("PRESS_SHORT", {
      operations: { read: false, write: true, event: false },
    });
    const state = dp("STATE", {
      operations: { read: true, write: false, event: false },
    });
    expect(findPrimaryDP("KEY", [pressShort, state])).toBe(state);
    // Order in the source list must not matter.
    expect(findPrimaryDP("KEY", [state, pressShort])).toBe(state);
  });

  it("picks a readable DP over a write-only PRESS_SHORT even without the read+event pairing", () => {
    // `state` here is read-only (no event flag) — it only qualifies via
    // the "any readable" fallback, not the read+event preference.
    const pressShort = dp("PRESS_SHORT", {
      operations: { read: false, write: true, event: false },
    });
    const readOnly = dp("READ_ONLY_STATE", {
      operations: { read: true, write: false, event: false },
    });
    expect(findPrimaryDP(undefined, [pressShort, readOnly])).toBe(readOnly);
  });
});

describe("findPrimaryDP — preference order", () => {
  it("prefers a read+event DP over a merely-readable one", () => {
    const readOnly = dp("LEVEL_REAL", {
      operations: { read: true, write: false, event: false },
    });
    const readAndEvent = dp("LEVEL", {
      operations: { read: true, write: false, event: true },
    });
    expect(findPrimaryDP(undefined, [readOnly, readAndEvent])).toBe(readAndEvent);
  });

  it("still honours the channel-type mapping table ahead of the read+event preference", () => {
    const other = dp("OTHER", {
      operations: { read: true, write: false, event: true },
    });
    const state = dp("STATE", {
      operations: { read: true, write: false, event: true },
    });
    // SHUTTER_CONTACT maps to STATE in PRIMARY_DP_BY_CHANNEL_TYPE.
    expect(findPrimaryDP("SHUTTER_CONTACT", [other, state])).toBe(state);
  });
});
