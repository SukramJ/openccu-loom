/**
 * The host seam: everything the editor would otherwise take from the page.
 *
 * The device editor runs in two hosts. In the SPA the page is the daemon's
 * own document, and the editor may use `fetch`, the WebSocket, the hash
 * router and `localStorage` directly. As a web component inside Home
 * Assistant (ADR 0089) the page belongs to Home Assistant: a URL resolves
 * against Home Assistant's origin, `location.hash` is Home Assistant's
 * router, and its storage is shared with every other panel. So the editor
 * reaches none of them itself; it asks the installed host.
 *
 * One host per bundle instance: the stores the editor reads are module
 * singletons, so a page carries one editor element at a time.
 */

import type { EventStream } from "$lib/api/ws";
import { pageHost } from "./page-host";

/** A REST call relative to `/api/v1`, query string included in `path`. */
export type HostRequest = {
  method: string;
  path: string;
  headers: Record<string, string>;
  body?: string;
};

/**
 * The answer to a {@link HostRequest}. `body` is the decoded JSON payload,
 * the raw text when it was not JSON, or `undefined` when empty.
 */
export type HostResponse = {
  status: number;
  statusText: string;
  body: unknown;
};

export interface Host {
  /** Performs a REST call. Never throws for an HTTP status; throws on transport failure. */
  request(req: HostRequest): Promise<HostResponse>;
  /**
   * Opens the daemon's event stream. A host without an event source returns
   * a stream that stays "closed" and never delivers an event.
   */
  events(): EventStream;
  /** Moves to an SPA hash route (`#/devices/…`, `#/links/…`, `#/schedules/…`). */
  navigate(hashRoute: string): void;
  /** Key-value storage for preferences, table state and remembered choices. */
  storage: Pick<Storage, "getItem" | "setItem" | "removeItem">;
  /** The document or shadow root the editor renders into, for id lookups. */
  root(): Document | ShadowRoot;
  /**
   * The URL of a daemon resource the browser loads itself (an image, a
   * download), or `null` when the host cannot give the browser one.
   */
  resourceUrl(apiPath: string): string | null;
  /**
   * The surfaces this host shows, on top of the operator's profile, or
   * `null` for all of them. The web component shows the configuration
   * editor and the change log and nothing else of the device page.
   */
  surfaces: ReadonlySet<string> | null;
}

let current: Host | null = null;

/** Installs the host. The web component installs its own before it mounts the editor. */
export function installHost(next: Host): void {
  current = next;
}

/**
 * The installed host, or the page host when none is installed: the SPA is
 * the page's own document, so it needs no installation step.
 */
export function host(): Host {
  return current ?? pageHost;
}
