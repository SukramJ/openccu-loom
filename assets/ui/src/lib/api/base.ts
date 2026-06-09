/**
 * Ingress-aware API base path.
 *
 * The SPA is served at `<prefix>/app/`. Served directly the prefix is
 * empty (document at `/app/`); behind Home Assistant Ingress it is the
 * per-session proxy path (document at `/api/hassio_ingress/<token>/app/`).
 * Every absolute REST/WebSocket URL must carry that same prefix, or it
 * bypasses the Ingress proxy and hits the Home Assistant origin instead.
 *
 * The prefix is derived from `location.pathname` — no server-injected
 * header is needed. The SPA uses a hash router, so client-side routes live
 * in the fragment and never perturb the path before `/app/`.
 */
export function ingressBase(): string {
  // No DOM (SSR / unit tests) → no prefix.
  if (typeof location === "undefined") return "";
  const i = location.pathname.indexOf("/app/");
  return i > 0 ? location.pathname.slice(0, i) : "";
}

/** Absolute REST API root, Ingress-prefixed when applicable. */
export function apiBase(): string {
  return `${ingressBase()}/api/v1`;
}
