// SPDX-License-Identifier: MIT
//
// Device → type-icon mapping for the device list. The real eQ-3 device
// images are not embedded (ccudata only carries the icon *filename*,
// not the bytes — the HA reference proxies them from the live CCU), so
// we map a device to a representative Lucide glyph from the local icon
// registry instead. Driven by the model string, which is the most
// deterministic signal; the order is significant (first match wins, so
// the more specific contact/motion checks precede the broad switch
// check). Falls back to a neutral cube so an unknown model still gets a
// consistent leading icon rather than an empty gap.

import type { IconName } from "$lib/icons";

type Rule = { re: RegExp; icon: IconName };

// First match wins — keep specific models above broad families.
const RULES: Rule[] = [
  { re: /SWSD|SMOKE/, icon: "mdi:smoke-detector-variant" },
  { re: /\bSWO\b|WSM|WDS|WEATHER/, icon: "mdi:weather-windy" },
  { re: /SWDO|SWDM|SCI|SRH|SWD\b|SHUTTER|CONTACT|REED/, icon: "mdi:door" },
  { re: /SMI|SMO|SPI|PIR|MOTION|PRESENCE/, icon: "mdi:run-fast" },
  { re: /SLO|WATER|LEAK/, icon: "mdi:water-alert" },
  { re: /ETRV|WTH|STHD|STHO|STH\b|TEMPERATURE|THERMO|HEAT|RADIATOR/, icon: "mdi:thermometer" },
  { re: /BDT|FDT|PDT|BSL|DRDI|DIMM|DIM\b/, icon: "mdi:lightbulb" },
  { re: /PSM|BSM|FSM|DRSI|MOD-OC|PCBS|PMFS|\bPS\b|PS-|SW[0-9]|SWITCH|SCTH/, icon: "mdi:power" },
  { re: /WRC|KRCA|RC[0-9]|RC-|REMOTE|\bKEY\b|BUTTON|TASTER/, icon: "mdi:gesture-tap-button" },
  { re: /DLD|DLS|KFM|LOCK/, icon: "mdi:lock" },
  { re: /ASIR|MP3|SIREN|ALARM/, icon: "mdi:bell-alert" },
];

/**
 * Representative type icon for a device. Matches on the model string
 * (e.g. `HmIP-eTRV-2` → thermometer, `HMIP-PSM` → power, `HmIP-WRC2` →
 * button). Returns a neutral cube when nothing matches.
 */
export function deviceTypeIcon(device: { model?: string; product_group?: string }): IconName {
  const key = `${device.model ?? ""} ${device.product_group ?? ""}`.toUpperCase();
  for (const rule of RULES) {
    if (rule.re.test(key)) return rule.icon;
  }
  return "mdi:cube-outline";
}
