// Pure helpers behind the hidden-parameters picker (VisibilityAdmin).
//
// The daemon returns one group per (parameter, paramset); this module
// turns a group plus the operator's active pattern set into the row
// state the view renders, and applies the category / search / paramset
// filters. It holds no Svelte state so the whole filtering contract is
// unit-testable without mounting a component.
//
// Why filtering matters here: a 399-device fleet yields ~2800 flat
// candidate patterns out of ~45 parameters, and roughly two thirds of
// those parameters are diagnostic bits (ERR_*, STICKY_*) that no
// operator wants to scroll past to reach the handful that are useful.

import type {
  UnIgnoreCandidateGroup,
  UnIgnoreReason,
} from "$lib/api/visibility-types";

/** Selection state of one candidate group. */
export type GroupState = "off" | "all" | "partial";

/** Reasons that describe internal plumbing rather than something an
    operator set out to look for. Hidden on first open so the list
    starts at the parameters worth a decision; the view always shows how
    many rows this suppressed and offers to reveal them. */
export const NOISE_REASONS: readonly UnIgnoreReason[] = [
  "week_profile",
  "read_only",
  "wildcard_suffix",
  "wildcard_prefix",
  "ignore_list",
  "internal_flag",
  "event_suppressed",
  "channel_restricted",
];

/** Display order for the category chips. Reasons the server sends that
    are absent here are appended in server order, so a new backend
    category shows up without a frontend release. */
export const REASON_ORDER: readonly UnIgnoreReason[] = [
  "operation_mode",
  "master_gate",
  "week_profile",
  "device_specific",
  "hidden",
  "ignore_list",
  "wildcard_prefix",
  "wildcard_suffix",
  "channel_restricted",
  "event_suppressed",
  "internal_flag",
  "read_only",
  "unknown",
];

/** i18n key for a reason's chip / badge label. */
export function reasonLabelKey(reason: UnIgnoreReason | string): string {
  return `unignore.reason.${reason}`;
}

/** i18n key for a reason's explanatory text. */
export function reasonHelpKey(reason: UnIgnoreReason | string): string {
  return `unignore.reason_help.${reason}`;
}

/** i18n key for a reason's badge when the server supplied the concrete
    rule text. Takes a `{pattern}` placeholder. */
export function reasonDetailLabelKey(reason: UnIgnoreReason | string): string {
  return `unignore.reason_detail.${reason}`;
}

/** Badge text for a candidate group.
 *
 * With a `reason_detail` the badge names the rule that fired
 * ("Prefix STATUS_FLAG_"); without one it falls back to the rule's
 * category ("Name prefix"). The category alone leaves an operator to
 * guess which of seven prefixes applied, so the detail is preferred
 * whenever the server sends it.
 */
export function reasonBadgeText(
  group: { reason: UnIgnoreReason | string; reason_detail?: string },
  t: (key: string, vars?: Record<string, string>) => string,
): string {
  const detail = group.reason_detail;
  if (!detail) return t(reasonLabelKey(group.reason));
  const key = reasonDetailLabelKey(group.reason);
  const text = t(key, { pattern: detail });
  // A reason that gained a detail server-side but has no detail
  // catalogue entry yet must not render its raw key at the operator.
  return text === key ? t(reasonLabelKey(group.reason)) : text;
}

export function isNoiseReason(reason: UnIgnoreReason | string): boolean {
  return (NOISE_REASONS as readonly string[]).includes(reason);
}

/** Every pattern that belongs to `group`: the fleet-wide short form,
    each model wildcard, and each channel-specific form. */
export function groupPatterns(group: UnIgnoreCandidateGroup): string[] {
  const out: string[] = [];
  if (group.simple_pattern) out.push(group.simple_pattern);
  for (const model of group.models ?? []) {
    if (model.wildcard_pattern) out.push(model.wildcard_pattern);
    for (const ch of model.channels ?? []) out.push(ch.pattern);
  }
  return out;
}

/** Selection state of `group` against the active pattern set. */
export function groupState(
  group: UnIgnoreCandidateGroup,
  active: ReadonlySet<string>,
): GroupState {
  if (group.simple_pattern && active.has(group.simple_pattern)) return "all";
  for (const pattern of groupPatterns(group)) {
    if (active.has(pattern)) return "partial";
  }
  return "off";
}

/** Number of scope patterns of `group` currently enabled. */
export function activeScopeCount(
  group: UnIgnoreCandidateGroup,
  active: ReadonlySet<string>,
): number {
  return groupPatterns(group).filter((p) => active.has(p)).length;
}

/** Stable identity of a group — parameter and paramset together, since
    the same name can be hidden in both VALUES and MASTER. */
export function groupKey(group: UnIgnoreCandidateGroup): string {
  return `${group.paramset}:${group.parameter}`;
}

export type CandidateFilter = {
  /** Free-text query matched against parameter, label and model names. */
  query: string;
  /** Reasons to show. Empty means "every reason". */
  reasons: ReadonlySet<string>;
  /** Paramsets to show. Empty means "every paramset". */
  paramsets: ReadonlySet<string>;
  /** Restrict to groups with at least one enabled pattern. */
  onlyEnabled: boolean;
};

export function emptyFilter(): CandidateFilter {
  return {
    query: "",
    reasons: new Set(),
    paramsets: new Set(),
    onlyEnabled: false,
  };
}

/** Case- and separator-insensitive substring match: "low bat" and
    "lowbat" both find LOW_BAT. */
function matchesQuery(group: UnIgnoreCandidateGroup, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  const loose = needle.replace(/[\s_-]+/g, "");
  const haystack = [
    group.parameter,
    group.label ?? "",
    ...(group.models ?? []).map((m) => m.model),
  ]
    .join(" ")
    .toLowerCase();
  return (
    haystack.includes(needle) || haystack.replace(/[\s_-]+/g, "").includes(loose)
  );
}

/** Apply `filter` to `groups`. `active` is only consulted for the
    onlyEnabled switch. */
export function filterGroups(
  groups: readonly UnIgnoreCandidateGroup[],
  filter: CandidateFilter,
  active: ReadonlySet<string>,
): UnIgnoreCandidateGroup[] {
  return groups.filter((g) => {
    if (filter.paramsets.size > 0 && !filter.paramsets.has(g.paramset)) {
      return false;
    }
    if (filter.reasons.size > 0 && !filter.reasons.has(g.reason)) return false;
    if (filter.onlyEnabled && groupState(g, active) === "off") return false;
    return matchesQuery(g, filter.query);
  });
}

/** Count of groups per reason, for the chip badges. Counts ignore the
    reason filter itself so a chip keeps showing its size while another
    chip is selected — otherwise every unselected chip reads "0". */
export function reasonCounts(
  groups: readonly UnIgnoreCandidateGroup[],
  filter: CandidateFilter,
  active: ReadonlySet<string>,
): Map<string, number> {
  const withoutReasons: CandidateFilter = { ...filter, reasons: new Set() };
  const counts = new Map<string, number>();
  for (const g of filterGroups(groups, withoutReasons, active)) {
    counts.set(g.reason, (counts.get(g.reason) ?? 0) + 1);
  }
  return counts;
}

/** Reasons present in `groups`, in [REASON_ORDER] order followed by any
    server-side reason this build does not know about. */
export function presentReasons(
  groups: readonly UnIgnoreCandidateGroup[],
  vocabulary: readonly string[] = [],
): string[] {
  const present = new Set<string>();
  for (const g of groups) present.add(g.reason);
  const ordered: string[] = [];
  for (const r of REASON_ORDER) {
    if (present.has(r)) ordered.push(r);
  }
  for (const r of vocabulary) {
    if (present.has(r) && !ordered.includes(r)) ordered.push(r);
  }
  for (const r of present) {
    if (!ordered.includes(r)) ordered.push(r);
  }
  return ordered;
}

/** The reason filter a fresh view starts with: every present reason
    except the noise ones. Returns an empty set when that would hide
    everything, so a fleet made entirely of diagnostic bits still shows
    a list rather than an empty state. */
export function defaultReasonFilter(
  groups: readonly UnIgnoreCandidateGroup[],
  vocabulary: readonly string[] = [],
): Set<string> {
  const present = presentReasons(groups, vocabulary);
  const kept = present.filter((r) => !isNoiseReason(r));
  if (kept.length === 0) return new Set();
  return new Set(kept);
}

/** How many groups the current reason filter hides. Drives the
    "N hidden — show all" affordance. */
export function suppressedCount(
  groups: readonly UnIgnoreCandidateGroup[],
  filter: CandidateFilter,
  active: ReadonlySet<string>,
): number {
  if (filter.reasons.size === 0) return 0;
  const all = filterGroups(
    groups,
    { ...filter, reasons: new Set() },
    active,
  ).length;
  return all - filterGroups(groups, filter, active).length;
}

/** Toggle the whole group: off → fleet-wide on; anything else → clear
    every scope. Returns the next pattern list.

    A group without a simple pattern (MASTER has none) enables every
    model wildcard or, failing that, every channel pattern, so the row
    toggle stays meaningful there too. */
export function toggleGroup(
  group: UnIgnoreCandidateGroup,
  patterns: readonly string[],
): string[] {
  const active = new Set(patterns);
  const owned = groupPatterns(group);
  const state = groupState(group, active);
  const next = patterns.filter((p) => !owned.includes(p));
  if (state !== "off") return next.sort();
  if (group.simple_pattern) return [...next, group.simple_pattern].sort();
  const wildcards = (group.models ?? [])
    .map((m) => m.wildcard_pattern)
    .filter((p): p is string => Boolean(p));
  if (wildcards.length > 0) return [...next, ...wildcards].sort();
  return [...next, ...owned].sort();
}

/** Toggle a single scope pattern. Enabling a narrower scope while the
    fleet-wide form is active first drops that form, so the resulting
    list says what the operator sees: exactly these scopes. */
export function togglePattern(
  group: UnIgnoreCandidateGroup,
  pattern: string,
  patterns: readonly string[],
): string[] {
  const active = new Set(patterns);
  if (active.has(pattern)) {
    return patterns.filter((p) => p !== pattern).sort();
  }
  let next = [...patterns];
  if (group.simple_pattern && pattern !== group.simple_pattern) {
    next = next.filter((p) => p !== group.simple_pattern);
  }
  return [...next, pattern].sort();
}

/** Patterns the operator saved that no candidate group explains —
    entries typed by hand, or ones whose devices have since left the
    fleet. They are kept visible so a save never silently drops them. */
export function orphanPatterns(
  groups: readonly UnIgnoreCandidateGroup[],
  patterns: readonly string[],
): string[] {
  const known = new Set<string>();
  for (const g of groups) {
    for (const p of groupPatterns(g)) known.add(p);
  }
  return patterns.filter((p) => !known.has(p)).sort();
}
