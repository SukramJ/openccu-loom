// Shared Security & Safety domain constants (docs/security-safety-concept.md
// §4). The hazard/fault class taxonomy, kept in the exact escalation order
// `hmenum.SecurityClasses()` returns (pkg/hmenum/security.go) — GET /security
// emits `classes` in that order, so a dropdown built from any other order
// (e.g. alphabetical, or the openapi.yaml enum listing) would visibly
// disagree with the Overview tiles. Also carries the glyph each class
// renders with. Centralised so the Overview, Sources and Faults views under
// routes/security/ never drift on the order or the icon.
import type { IconName } from "$lib/icons";
import type { SecurityClass } from "$lib/api/types";

export const SECURITY_CLASSES: SecurityClass[] = [
  "smoke",
  "gas",
  "co",
  "intrusion",
  "panic",
  "water",
  "tamper",
  "technical",
  "battery",
];

const CLASS_ICON: Record<string, IconName> = {
  smoke: "mdi:smoke-detector-variant",
  water: "mdi:water-alert",
  gas: "mdi:gas-cylinder",
  co: "mdi:molecule-co",
  tamper: "mdi:lock-open",
  battery: "mdi:battery-alert",
  technical: "mdi:cog",
  intrusion: "mdi:shield",
  panic: "mdi:bell-alert",
};

/** The glyph for a hazard/fault class token; falls back to a plain alert. */
export function securityClassIcon(cls: string): IconName {
  return CLASS_ICON[cls] ?? "mdi:alert-circle";
}
