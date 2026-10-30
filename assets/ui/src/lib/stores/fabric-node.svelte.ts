// The @sukramj/fabric-ui context for the Matter area, one per SPA session.
//
// The Matter pages (status, pairing, fabrics, selection, endpoints,
// surface, diagnostics) come from the package and read this context; the Matter
// route provides it above them. It is a module singleton rather than a
// per-mount object because the navigation gates outside the route (the
// sidebar, the navigation-views editor, the landing-page picker) ask the
// same node store whether the bridge is enabled — one status, one source.
//
// Everything host-specific is wired here: the session-aware fetch with the
// CSRF header, the Ingress-aware API root, loom's catalogues layered over
// the package's, the shared toast and confirm stores, and the WebSocket hub
// in place of the package's own event stream.
import { createClient, createFabricUI, createTranslator, type StreamEvent, type Translate } from "@sukramj/fabric-ui";
import { apiBase } from "$lib/api/base";
import { loomFetch } from "$lib/api/client";
import type { EventEnvelope } from "$lib/api/types";
import { catalogs } from "$lib/i18n";
import { confirmStore } from "./confirm.svelte";
import { subscribe as subscribeHub } from "./events.svelte";
import { prefs } from "./preferences.svelte";
import { toastStore } from "./toast.svelte";

/** The hrefs of the seven Matter pages under loom's hash router. */
export const matterRoutes = {
  status: "#/matter",
  pair: "#/matter/pair",
  fabrics: "#/matter/fabrics",
  selection: "#/matter/selection",
  endpoints: "#/matter/endpoints",
  surface: "#/matter/surface",
  diagnostics: "#/matter/diagnostics",
} as const;

// The daemon publishes the node events on the hub as `node.<event>` with
// the stream event's data as payload; the package's store takes them as
// `{ type: <event>, data }`. Every other envelope is not ours.
const NODE_PREFIX = "node.";

export function nodeEventFromEnvelope(ev: EventEnvelope): StreamEvent | null {
  if (!ev.type.startsWith(NODE_PREFIX)) return null;
  return { type: ev.type.slice(NODE_PREFIX.length), data: ev.payload } as StreamEvent;
}

// loomFetch and the catalogues are looked up on first use rather than at
// import, so importing this module (the sidebar does, for the nav gate)
// touches neither the client's nor the i18n module's exports before a page
// renders — a partial mock of either in a component test stays enough.
let translate: Translate | null = null;

export const fabricUI = createFabricUI({
  client: createClient(apiBase(), (input, init) => loomFetch(input, init)),
  // The locale getter reads reactive state, so the pages re-render on a
  // language switch; loom's keys win over the package's on a collision.
  t: (key, vars) => (translate ??= createTranslator(() => prefs.locale, catalogs))(key, vars),
  toast: {
    info: (m) => void toastStore.info(m),
    success: (m) => void toastStore.success(m),
    error: (m) => void toastStore.error(m),
  },
  confirm: (o) => confirmStore.ask(o),
  routes: matterRoutes,
  subscribe: (onEvent) =>
    subscribeHub((ev) => {
      const node = nodeEventFromEnvelope(ev);
      if (node) onEvent(node);
    }),
});
