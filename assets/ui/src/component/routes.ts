import routes from "./editor-routes.json";

/**
 * The REST routes the editor component may call, as path prefixes below
 * `/api/v1`. It is a contract with the integration that relays the calls
 * (ADR 0089): the daemon publishes the list next to the bundle, the relay
 * forwards nothing outside it, and the component refuses to send a call
 * outside it, so a new route the editor starts to use fails here first
 * instead of in the field.
 */
export const ROUTE_PREFIXES: readonly string[] = routes.prefixes;

/** A prefix covers itself and every path below it: `/devices` covers `/devices/A:1/paramsets/MASTER`. */
export function routeAllowed(path: string, prefixes: readonly string[] = ROUTE_PREFIXES): boolean {
  return prefixes.some((p) => path === p || path.startsWith(`${p}/`));
}
