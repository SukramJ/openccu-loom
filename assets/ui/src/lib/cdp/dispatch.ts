// SPDX-License-Identifier: MIT
// CDP-aware widget dispatch. Each kind string returned by the backend
// (`internal/model/custom/cdpkind`) maps to a Svelte widget that knows
// how to drive that kind of device through the
// `POST /devices/.../cdps/{name}/{operation}` semantic surface.
//
// Unknown kinds fall through to `null` and the caller renders nothing
// — the CDP then waits its turn in the per-category rollout (ADR 0016
// follow-ups) until a widget lands for it.

import type { Component } from "svelte";

import ClimateTile from "./widgets/ClimateTile.svelte";
import CoverTile from "./widgets/CoverTile.svelte";
import LightTile from "./widgets/LightTile.svelte";
import LockTile from "./widgets/LockTile.svelte";
import SirenTile from "./widgets/SirenTile.svelte";
import SwitchTile from "./widgets/SwitchTile.svelte";
import TextDisplayTile from "./widgets/TextDisplayTile.svelte";
import ValveTile from "./widgets/ValveTile.svelte";

export type CdpWidget = Component<{
  address: string;
  cdp: import("$lib/api/types").CustomDPSummary;
  /** Optional display title — typically the channel's user-facing
   *  name (e.g. "Bücherregal") rather than the wire-DP parameter
   *  identifier (e.g. "SET_POINT_TEMPERATURE"). Tiles fall back to
   *  `cdp.name` when omitted. */
  title?: string;
}>;

const REGISTRY: Record<string, CdpWidget> = {
  // light family — one widget handles every variant; capability flags
  // decide which controls render. Covers light / light_color /
  // light_color_temp / light_fixed_color / light_rgbw / light_dali.
  light: LightTile as unknown as CdpWidget,
  light_color: LightTile as unknown as CdpWidget,
  light_color_temp: LightTile as unknown as CdpWidget,
  light_fixed_color: LightTile as unknown as CdpWidget,
  light_rgbw: LightTile as unknown as CdpWidget,
  light_dali: LightTile as unknown as CdpWidget,
  light_effect: LightTile as unknown as CdpWidget,
  light_sound_led: LightTile as unknown as CdpWidget,

  // cover family — basic / blind (tilt) / garage all share one tile,
  // the kind flag picks the layout (open/close/stop/set_position
  // vs. open/close/stop/ventilate).
  cover: CoverTile as unknown as CdpWidget,
  cover_blind: CoverTile as unknown as CdpWidget,
  cover_garage: CoverTile as unknown as CdpWidget,

  // climate family — simple (HM-CC-TC heat-on + setpoint) / RF
  // (action mode picker) / HmIP (CONTROL_MODE write + presets).
  climate_simple: ClimateTile as unknown as CdpWidget,
  climate_rf: ClimateTile as unknown as CdpWidget,
  climate_hmip: ClimateTile as unknown as CdpWidget,

  // remaining single-flavour categories — each maps one custom-DP
  // surface to one tile.
  lock: LockTile as unknown as CdpWidget,
  siren: SirenTile as unknown as CdpWidget,
  switch: SwitchTile as unknown as CdpWidget,

  // niche categories rounding out the kind inventory.
  text_display: TextDisplayTile as unknown as CdpWidget,
  valve_irrigation: ValveTile as unknown as CdpWidget,
  valve_modulating: ValveTile as unknown as CdpWidget,
};

/** Returns the widget Component for a CDP kind, or undefined when
 *  no widget is registered (the per-category rollout hasn't reached
 *  this kind yet). */
export function cdpWidgetFor(kind: string | undefined): CdpWidget | undefined {
  if (!kind) return undefined;
  return REGISTRY[kind];
}

/** Returns true when the kind has a CDP-side widget. Used by the
 *  Übersicht panel to decide whether to render a CDP tile or fall
 *  through to the channel-tile (orphan) section. */
export function hasCdpWidget(kind: string | undefined): boolean {
  return cdpWidgetFor(kind) !== undefined;
}
