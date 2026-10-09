import type { UISchemaProfile } from "$lib/api/types";

/**
 * Easymode link profiles as the openccu-data archive ships them:
 *
 *   {
 *     <sender_type>: {
 *       "profiles": [
 *         { id, name: { en, de, … }, description: { en, de }, params: { <p>: {...} } },
 *         …
 *       ]
 *     }
 *   }
 *
 * Profile 0 is "Expert" / "Experte" in every sender group of the
 * archive and carries no parameters: it stands for "no easymode, edit
 * the raw paramset" — the same slot the CCU WebUI gives its "Experte"
 * option (easymodes/<RECEIVER>/<SENDER>.tcl, PROFILES_MAP(0)). The
 * daemon reports active_profile_id 0 when no other profile matches.
 */
export const EXPERT_PROFILE_ID = 0;

type RawParam = {
  constraint_type: string;
  value?: unknown;
  default?: unknown;
  values?: unknown[];
  min_value?: unknown;
  max_value?: unknown;
};

type RawProfile = {
  id: number;
  name?: Record<string, string>;
  description?: Record<string, string>;
  params?: Record<string, RawParam>;
};

export type ProfileVariant = {
  /** `<sender_type>::<id>` — unique even across sender groups. */
  key: string;
  id: number;
  label: string;
  description: string;
  params: Record<string, RawParam>;
};

/**
 * The profile variants a link offers. For LINK the daemon pre-filters
 * to the link's sender type (profile.sender_type); when it could not
 * determine one, every sender group is listed and each label names its
 * sender so the entries stay distinguishable.
 */
export function profileVariants(
  profile: UISchemaProfile,
  locale: string,
): ProfileVariant[] {
  const raw = profile.raw as Record<string, { profiles?: RawProfile[] }> | undefined;
  if (!raw) return [];
  const senderKeys =
    profile.sender_type && profile.sender_type in raw
      ? [profile.sender_type]
      : Object.keys(raw);
  const singleSender = senderKeys.length === 1;
  const out: ProfileVariant[] = [];
  for (const sender of senderKeys) {
    for (const p of raw[sender]?.profiles ?? []) {
      const name =
        p.name?.[locale] ?? p.name?.en ?? Object.values(p.name ?? {})[0] ?? `#${p.id}`;
      out.push({
        key: `${sender}::${p.id}`,
        id: p.id,
        label: singleSender ? name : `${name} (${sender})`,
        description: p.description?.[locale] ?? p.description?.en ?? "",
        params: p.params ?? {},
      });
    }
  }
  return out;
}

/**
 * The variant to show as selected for the link's current values: the
 * one the daemon matched, else Expert. Null when the archive offers
 * nothing for this link at all.
 */
export function activeVariant(
  variants: ProfileVariant[],
  activeId: number | undefined,
): ProfileVariant | null {
  const wanted = activeId ?? EXPERT_PROFILE_ID;
  return (
    variants.find((v) => v.id === wanted) ??
    variants.find((v) => v.id === EXPERT_PROFILE_ID) ??
    null
  );
}

export type ProfilePatch = {
  /** Values to stage: the fixed constraints plus the range defaults. */
  patch: Record<string, unknown>;
  /** Parameters the profile hard-codes. */
  fixed: string[];
  /** Parameters the profile leaves to the operator (range and list constraints). */
  editable: string[];
};

/**
 * What choosing a profile writes. Port of aiohomematic-config's
 * ResolvedProfile.fixed_params / editable_params split: a fixed
 * constraint pins its value, a range constraint stages its default and
 * stays editable, a list constraint keeps the current value and stays
 * editable within the listed options.
 */
export function profilePatch(variant: ProfileVariant): ProfilePatch {
  const patch: Record<string, unknown> = {};
  const fixed: string[] = [];
  const editable: string[] = [];
  for (const [name, param] of Object.entries(variant.params)) {
    if (param.constraint_type === "fixed" && param.value !== undefined) {
      patch[name] = param.value;
      fixed.push(name);
    } else if (param.constraint_type === "range") {
      if (param.default !== undefined) patch[name] = param.default;
      editable.push(name);
    } else if (param.constraint_type === "list") {
      editable.push(name);
    }
  }
  return { patch, fixed, editable };
}
