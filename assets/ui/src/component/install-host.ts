/**
 * Installs the component host. The entry module imports this first: the
 * stores read their host while their modules evaluate (the preferences
 * load from storage at import), so the host has to be in place before the
 * first editor module runs.
 */
import { streamFromFrames } from "$lib/api/ws";
import { installHost, type HostRequest, type HostResponse } from "$lib/host/host";
import { routeAllowed } from "./routes";

/**
 * What the embedding page hands the element (ADR 0089 decision 1): a request
 * to the daemon's REST API and, optionally, the daemon's event stream. In
 * Home Assistant both go through the integration's relay commands.
 */
export type EditorTransport = {
  /**
   * `path` is relative to `/api/v1` without a query string; `body` is the
   * decoded JSON payload. `headers` carry what the daemon's contract asks
   * of a call beyond its body — `X-Edit-Token`, the edit lock a MASTER
   * write needs, above all — and must reach the daemon. Resolves with the
   * daemon's status and decoded body for every HTTP status; rejects only
   * when the daemon was not reached.
   */
  request(req: {
    method: string;
    path: string;
    query: Record<string, string>;
    headers: Record<string, string>;
    body?: unknown;
  }): Promise<{ status: number; body?: unknown }>;
  /** Delivers each raw frame of the daemon's event stream; returns the closer. */
  subscribe?(onFrame: (raw: unknown) => void): () => void;
};

/** The surfaces the element shows: the configuration editor and the change log. */
const EDITOR_SURFACES: ReadonlySet<string> = new Set([
  "device.configure",
  "device.configure.channels",
  "device.configure.links",
  "device.configure.schedule",
  "device.history",
]);

let transport: EditorTransport | null = null;
let root: ShadowRoot | null = null;
let navigateTo: (hashRoute: string) => void = () => {};

/** Storage that lives as long as the page: the component owns no storage of its own page. */
const memory = new Map<string, string>();

export function configureHost(next: {
  transport?: EditorTransport | null;
  root?: ShadowRoot;
  navigate?: (hashRoute: string) => void;
}): void {
  if (next.transport !== undefined) transport = next.transport;
  if (next.root) root = next.root;
  if (next.navigate) navigateTo = next.navigate;
}

function decodeRequestBody(body: HostRequest["body"]): unknown {
  if (body === undefined) return undefined;
  if (typeof body !== "string") {
    throw new Error("the editor component sends JSON bodies only");
  }
  return body === "" ? undefined : JSON.parse(body);
}

installHost({
  async request(req: HostRequest): Promise<HostResponse> {
    if (!transport) throw new Error("loom-device-editor: no transport set");
    const url = new URL(req.path, "http://relay.invalid");
    if (!routeAllowed(url.pathname)) {
      // Logged as well as thrown: a caller that turns the error into an
      // error state would otherwise hide which route is missing.
      const message = `loom-device-editor: ${req.method} ${url.pathname} is outside the editor's route list`;
      console.error(message);
      throw new Error(message);
    }
    const res = await transport.request({
      method: req.method,
      path: url.pathname,
      query: Object.fromEntries(url.searchParams),
      headers: req.headers,
      body: decodeRequestBody(req.body),
    });
    return { status: res.status, statusText: "", body: res.body };
  },
  events: () => streamFromFrames(transport?.subscribe ? (f) => transport!.subscribe!(f) : null),
  navigate: (hashRoute) => navigateTo(hashRoute),
  storage: {
    getItem: (k) => memory.get(k) ?? null,
    setItem: (k, v) => void memory.set(k, v),
    removeItem: (k) => void memory.delete(k),
  },
  root: () => root ?? document,
  resourceUrl: () => null,
  surfaces: EDITOR_SURFACES,
});
