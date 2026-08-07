import { t } from "$lib/i18n";

/**
 * Role vocabulary shared by the users and the API-token settings tabs.
 *
 * Both tabs render the same three roles, in the same order, with the
 * same labels and badge colours. Holding that in one place is not
 * cosmetic: while the two surfaces each carried their own copy, a fix
 * for one of them (rendering the raw wire value `viewer` instead of the
 * translated label) had to be made twice, and was found twice.
 */
export const ROLES = ["viewer", "operator", "admin"] as const;

/** Badge colours, ordered by how much the role can do. */
type BadgeVariant = "danger" | "warning" | "muted";

/**
 * The translated label for a role.
 *
 * `role` arrives as a plain string on the wire (`UserSummaryV2` /
 * `TokenSummaryV2`), not as a union: a value this build does not know —
 * a role added by a newer daemon, or a stale row during a rename — falls
 * back to the raw string rather than rendering a bare translation key.
 */
export function roleLabel(role: string): string {
  switch (role) {
    case "viewer":
      return t("role.viewer");
    case "operator":
      return t("role.operator");
    case "admin":
      return t("role.admin");
    default:
      return role;
  }
}

/** The three roles as `Select` options, labelled in the active locale. */
export function roleOptions(): { value: string; label: string }[] {
  return ROLES.map((role) => ({ value: role, label: roleLabel(role) }));
}

/** Badge variant for a role; unknown roles stay neutral. */
export function roleBadgeVariant(role: string): BadgeVariant {
  if (role === "admin") return "danger";
  if (role === "operator") return "warning";
  return "muted";
}
