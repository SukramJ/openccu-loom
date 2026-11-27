import routes from "./editor-routes.json";

/** One entry of the editor's route list: an HTTP method and a path prefix below `/api/v1`. */
export type EditorRoute = { readonly method: string; readonly prefix: string };

/**
 * The REST routes the editor component may call. It is a contract with the
 * integration that relays the calls (ADR 0089): the daemon publishes the
 * list next to the bundle, the relay forwards nothing outside it, and the
 * component refuses to send a call outside it, so a new route the editor
 * starts to use fails here first instead of in the field.
 *
 * A route names its method because a path alone cannot tell the editor's
 * calls from a device's lifecycle: `POST /devices/{addr}/paramsets/…` edits a
 * configuration, `POST /devices/{addr}/release` unpairs the device.
 */
export const EDITOR_ROUTES: readonly EditorRoute[] = routes.routes;

/**
 * A prefix covers itself and every path below it, segment by segment; a `*`
 * segment matches exactly one segment of any value. `/devices/*` covers
 * `/devices/A%3A1/channels/1/flags`, not `/devices` itself and not
 * `/devicesx/1`.
 */
export function routeAllowed(
  method: string,
  path: string,
  allowed: readonly EditorRoute[] = EDITOR_ROUTES,
): boolean {
  const verb = method.toUpperCase();
  const segments = path.split("/");
  return allowed.some((r) => {
    if (r.method !== verb) return false;
    const want = r.prefix.split("/");
    if (segments.length < want.length) return false;
    return want.every((seg, i) => (seg === "*" ? segments[i] !== "" : seg === segments[i]));
  });
}
