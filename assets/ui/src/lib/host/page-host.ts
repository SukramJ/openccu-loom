import { apiBase } from "$lib/api/base";
import { connectEvents } from "$lib/api/ws";
import type { Host, HostRequest, HostResponse } from "./host";

// CSRFCookieName / CSRFHeaderName mirror internal/auth/csrf.go. The
// daemon mounts a double-submit CSRF guard on the whole REST router, so
// every mutating request must echo the JS-readable csrf cookie back in
// this header — otherwise the daemon answers 403 "token mismatch".
const CSRF_COOKIE = "openccu_loom_csrf";
export const CSRF_HEADER = "X-CSRF-Token";

export const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

export function csrfToken(): string {
  const prefix = `${CSRF_COOKIE}=`;
  for (const part of document.cookie.split(";")) {
    const c = part.trim();
    if (c.startsWith(prefix)) {
      return decodeURIComponent(c.slice(prefix.length));
    }
  }
  return "";
}

/**
 * Reads a body once as text and decodes it as JSON when it parses. A body
 * stream can be consumed only once, so `json()` with a `text()` fallback
 * would fail on every non-JSON answer.
 */
export async function decodeBody(res: Response): Promise<unknown> {
  const raw = await res.text();
  if (!raw) return undefined;
  try {
    return JSON.parse(raw);
  } catch {
    return raw;
  }
}

/**
 * The SPA's own page: same-origin `fetch` with the session cookie and the
 * CSRF echo, the WebSocket event stream, the hash router, `localStorage`
 * and the document.
 */
export const pageHost: Host = {
  async request(req: HostRequest): Promise<HostResponse> {
    const method = req.method.toUpperCase();
    const headers = { ...req.headers };
    if (!SAFE_METHODS.has(method)) {
      const token = csrfToken();
      if (token) headers[CSRF_HEADER] = token;
    }
    const res = await fetch(`${apiBase()}${req.path}`, {
      method,
      headers,
      body: req.body,
      credentials: "same-origin",
    });
    return { status: res.status, statusText: res.statusText, body: await decodeBody(res) };
  },
  events: connectEvents,
  navigate(hashRoute: string): void {
    location.hash = hashRoute;
  },
  storage: {
    getItem: (k) => localStorage.getItem(k),
    setItem: (k, v) => localStorage.setItem(k, v),
    removeItem: (k) => localStorage.removeItem(k),
  },
  root: () => document,
  resourceUrl: (apiPath: string) => `${apiBase()}${apiPath}`,
  surfaces: null,
};
