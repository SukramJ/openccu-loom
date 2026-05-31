// SPDX-License-Identifier: MIT
//
// Pure-TS composer for AutoTile. Given a channel + its DPs, produces
// a `ComposedTile` data structure that AutoTile.svelte stamps out.
// No DOM, no Svelte — everything is plain TypeScript so the
// composition logic is unit-testable in Vitest without a browser.
//
// Concept doc: docs/ui/auto-tile-concept.md.
//
// Resolution order:
//   1. Find the primary DP (channel-type → parameter table from
//      primary.ts, then first read+event DP, then first DP).
//   2. Bucket remaining DPs into roles (secondary readout / toggle
//      setting / numeric setting / action / action-with-value)
//      via classify.ts.
//   3. Choose the right control primitive per writable DP based on
//      type + min/max range:
//        bool                       → TogglePill
//        number with min+max        → ControlSlider (range known)
//        number without bounds      → NumericInputFeature (free input)
//        ENUM ≤ 4 entries           → ControlButtonGroup
//        ENUM > 4 entries           → ControlEnumSelect (dropdown)
//   4. Choose the right action primitive per write-only DP:
//        ACTION-typed / typeless    → ActionButton (one-tap fire)
//        value-typed                → NumericActionFeature (inline expand + Send)
//   5. Emit density token (comfortable / compact at 7+ readouts) and
//      grid-span hint (2 cells at ≥ 9 readouts) for the panel.
//   6. Roll up the lifecycle to a tile-tint signal so the AutoTile
//      can apply a worst-case background colour.

import type {
  ChannelSummary,
  DataPointSource,
  DataPointSummary,
} from "$lib/api/types";

import { findPrimaryDP } from "./primary";
import { bucketDPs } from "./classify";

export type DensityToken = "comfortable" | "compact";
export type LifecycleTint = "live" | "cache" | "stale" | "unobserved";

/** What kind of UI primitive AutoTile should render for a writable DP. */
export type ControlKind =
  | "toggle"
  | "slider"
  | "stepper"
  | "free-input"
  | "button-group"
  | "dropdown"
  | "action"
  | "action-with-value";

/** Spec for a single read-only readout (primary or secondary). */
export type ReadoutSpec = {
  dp: DataPointSummary;
  /** Optional override when the parent already determined the icon
   *  (lets the headline carry the channel-type icon while secondaries
   *  use the per-DP ui_hint icon). */
  iconOverride?: string;
};

/** Spec for a writable DP. Carries the resolved control kind + the
 *  range / option list so the renderer doesn't need to inspect the
 *  DP itself. */
export type ControlSpec = {
  dp: DataPointSummary;
  kind: ControlKind;
  /** Numeric bounds for slider / stepper / free-input. Either side
   *  may be undefined when the descriptor didn't ship the bound. */
  min?: number;
  max?: number;
  /** ENUM option labels (passes through dp.value_list). */
  options?: string[];
};

/** Spec for a write-only DP (fire-and-forget action or
 *  action-with-value editor). */
export type ActionSpec = {
  dp: DataPointSummary;
  kind: "action" | "action-with-value";
  /** Numeric range for the action-with-value editor. */
  min?: number;
  max?: number;
};

/** A semantic group of readouts that share a quantity, e.g. three
 *  µg/m³ readings clustered as "particulate". Renderer can display
 *  these as a sub-card. Always at least one entry. */
export type ReadoutBucket = {
  semantic: string;
  readouts: ReadoutSpec[];
};

/** The output of the composer — everything the renderer needs. */
export type ComposedTile = {
  channel: ChannelSummary;
  headline?: ReadoutSpec;
  /** Secondary readouts grouped by ui_hint.semantic. Buckets with a
   *  single member render inline; buckets with ≥ 2 members render
   *  as a labelled sub-card. */
  readoutBuckets: ReadoutBucket[];
  /** Flat secondary list, kept alongside the bucketed view for
   *  renderers that want the simple layout. */
  readouts: ReadoutSpec[];
  controls: ControlSpec[];
  actions: ActionSpec[];
  density: DensityToken;
  /** Grid span: 1 for normal tiles, 2 when the tile carries ≥9
   *  readouts and deserves the wider footprint. */
  gridSpan: 1 | 2;
  /** Worst-case lifecycle across all rendered DPs. Drives the
   *  tile-background tint in AutoTile.svelte. */
  tint: LifecycleTint;
};

/**
 * The composer entry point. Takes a channel and its DP list and
 * returns the layout description. Pure — same inputs always
 * produce the same output.
 */
export function composeTile(
  channel: ChannelSummary,
  dps: readonly DataPointSummary[],
): ComposedTile {
  const primaryDP = findPrimaryDP(channel.type, dps);
  const buckets = bucketDPs(dps, primaryDP?.parameter);

  const headline: ReadoutSpec | undefined = buckets.primary
    ? { dp: buckets.primary }
    : undefined;

  const readouts: ReadoutSpec[] = buckets.secondary.map((dp) => ({ dp }));
  const readoutBuckets = groupBySemantic(readouts);

  const controls: ControlSpec[] = [
    ...buckets.toggles.map(toToggleControl),
    ...buckets.numerics.map(toNumericControl),
  ];

  const actions: ActionSpec[] = [
    ...buckets.actions.map(toActionSpec),
    ...buckets.actionsWithValue.map(toActionWithValueSpec),
  ];

  const density: DensityToken = readouts.length >= 7 ? "compact" : "comfortable";
  const gridSpan: 1 | 2 = readouts.length >= 9 ? 2 : 1;
  const tint = worstLifecycle([headline?.dp, ...readouts.map((r) => r.dp)]);

  return {
    channel,
    headline,
    readoutBuckets,
    readouts,
    controls,
    actions,
    density,
    gridSpan,
    tint,
  };
}

// --- Control primitive picking -----------------------------------

function toToggleControl(dp: DataPointSummary): ControlSpec {
  return { dp, kind: "toggle" };
}

function toNumericControl(dp: DataPointSummary): ControlSpec {
  const t = (dp.type ?? "").toUpperCase();
  if (t === "ENUM" && dp.value_list && dp.value_list.length > 0) {
    return {
      dp,
      kind: dp.value_list.length <= 4 ? "button-group" : "dropdown",
      options: dp.value_list,
    };
  }
  const min = toNumber(dp.min);
  const max = toNumber(dp.max);
  if (min !== undefined && max !== undefined) {
    // Slider when range is known and bounded.
    return { dp, kind: "slider", min, max };
  }
  return { dp, kind: "free-input", min, max };
}

function toActionSpec(dp: DataPointSummary): ActionSpec {
  return { dp, kind: "action" };
}

function toActionWithValueSpec(dp: DataPointSummary): ActionSpec {
  const min = toNumber(dp.min);
  const max = toNumber(dp.max);
  return { dp, kind: "action-with-value", min, max };
}

// --- Grouping by semantic ----------------------------------------

function groupBySemantic(readouts: ReadoutSpec[]): ReadoutBucket[] {
  const map = new Map<string, ReadoutSpec[]>();
  for (const r of readouts) {
    const sem = r.dp.ui_hint?.semantic ?? "other";
    const list = map.get(sem) ?? [];
    list.push(r);
    map.set(sem, list);
  }
  // Stable order: largest bucket first (so dense same-quantity
  // groups dominate), ties broken by semantic alphabetical.
  return [...map.entries()]
    .sort((a, b) => {
      if (b[1].length !== a[1].length) return b[1].length - a[1].length;
      return a[0].localeCompare(b[0]);
    })
    .map(([semantic, list]) => ({ semantic, readouts: list }));
}

// --- Lifecycle roll-up -------------------------------------------

function worstLifecycle(dps: (DataPointSummary | undefined)[]): LifecycleTint {
  let worst: LifecycleTint = "live";
  for (const dp of dps) {
    if (!dp) continue;
    const s = (dp.source ?? "live") as DataPointSource;
    if (s === "stale") return "stale"; // worst possible, return immediately
    if (s === "unobserved") {
      worst = "unobserved";
    } else if (s === "cache" && worst === "live") {
      worst = "cache";
    }
  }
  return worst;
}

// --- Number coercion --------------------------------------------

function toNumber(v: unknown): number | undefined {
  if (typeof v === "number" && Number.isFinite(v)) return v;
  if (typeof v === "string") {
    const n = parseFloat(v);
    return Number.isFinite(n) ? n : undefined;
  }
  return undefined;
}
