import type {
  UISchemaCrossValidation,
  UISchemaParameter,
  UISchemaVisibility,
} from "$lib/api/types";

/**
 * Current working values keyed by parameter name. Used by both the
 * cross-validation pass and the visibility evaluator below.
 */
export type ParamValues = Record<string, unknown>;

/**
 * Evaluate every cross-validation rule against `values` and return a
 * map of `param_name → error_message` for UI display. A rule
 * contributes its error to every parameter in `applies_to_params` so
 * the conflict is visible on each input.
 */
export function validateCrossRules(
  rules: UISchemaCrossValidation[],
  values: ParamValues,
): Record<string, string> {
  const errors: Record<string, string> = {};
  for (const rule of rules) {
    const a = values[rule.param_a];
    const b = values[rule.param_b];
    if (!evaluateRule(rule.rule, a, b)) {
      for (const p of rule.applies_to_params) {
        if (!errors[p]) errors[p] = rule.error || rule.id;
      }
    }
  }
  return errors;
}

/**
 * Return the subset of parameters that should currently be rendered.
 * All parameters NOT mentioned in any `show` clause are always
 * visible; parameters listed in one or more `show` clauses are
 * visible when at least one trigger matches.
 */
export function visibleParameters(
  parameters: UISchemaParameter[],
  visibility: UISchemaVisibility[] | undefined,
  values: ParamValues,
): UISchemaParameter[] {
  if (!visibility || visibility.length === 0) return parameters;

  const gated = new Set<string>();
  for (const rule of visibility) for (const p of rule.show) gated.add(p);

  const matched = new Set<string>();
  for (const rule of visibility) {
    const v = values[rule.trigger];
    if (matchesTrigger(v, rule.trigger_value)) {
      for (const p of rule.show) matched.add(p);
    }
  }

  return parameters.filter(
    (p) => !gated.has(p.name) || matched.has(p.name),
  );
}

function evaluateRule(rule: string, a: unknown, b: unknown): boolean {
  const na = toNumber(a);
  const nb = toNumber(b);
  // Numeric-only rules skip silently when either side is non-numeric.
  if (na === null || nb === null) return true;
  switch (rule) {
    case "eq":
      return na === nb;
    case "ne":
      return na !== nb;
    case "gt":
      return na > nb;
    case "gte":
      return na >= nb;
    case "lt":
      return na < nb;
    case "lte":
      return na <= nb;
    default:
      // Unknown comparator → treat as satisfied; the server gets to
      // reject on write.
      return true;
  }
}

function matchesTrigger(current: unknown, trigger: unknown): boolean {
  if (Array.isArray(trigger)) {
    return trigger.some((t) => equalLoose(current, t));
  }
  return equalLoose(current, trigger);
}

function equalLoose(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  const na = toNumber(a);
  const nb = toNumber(b);
  if (na !== null && nb !== null) return na === nb;
  return String(a) === String(b);
}

function toNumber(v: unknown): number | null {
  if (typeof v === "number" && Number.isFinite(v)) return v;
  if (typeof v === "boolean") return v ? 1 : 0;
  if (typeof v === "string" && v.trim() !== "") {
    const n = Number(v);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}
