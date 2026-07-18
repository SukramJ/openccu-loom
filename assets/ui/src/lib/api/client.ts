import type {
  AlarmArea,
  AlarmArmAccepted,
  AlarmArmRequest,
  AlarmAreaStatus,
  AlarmCode,
  AlarmCodeRequest,
  AlarmJournalClass,
  AlarmJournalEntry,
  AlarmMessage,
  AlarmModeReadiness,
  AlarmOutput,
  AlarmOutputCandidate,
  AlarmOutputTestRequest,
  AlarmRemoteKeyCandidate,
  AlarmSensor,
  AlarmVerbRequest,
  AlarmWalkTestStatus,
  AuditEntry,
  BackupEntry,
  CentralLinksReport,
  CentralLinksStatus,
  ConfigSnapshot,
  EditSessionResponse,
  EnergyResponse,
  FunctionEntry,
  InboxDevice,
  InstallModeInterfaceEntry,
  RoomEntry,
  UserListEntry,
  ClimateSchedule,
  CustomDPSummary,
  DataPointSummary,
  DeviceDetail,
  DeviceSummary,
  CaptureSummary,
  DiagnosticsEnvelope,
  HealthSnapshot,
  Incident,
  InterfaceInfo,
  LogLevelsResponse,
  Link,
  LinkableChannel,
  Paginated,
  ProgramEntry,
  LogRecord,
  RSSIMatrix,
  RpcRecordingStatus,
  ServiceMessage,
  SysvarEntry,
  SystemCCUEntry,
  UISchema,
} from "./types";
import type {
  MatterBulkUpdateRequest,
  MatterBulkUpdateResponse,
  MatterCommissioningWindow,
  MatterExposableResponse,
  MatterExposureUpdate,
  MatterFabricsResponse,
  MatterSetupPayload,
  MatterStatus,
} from "./matter-types";
import type {
  UnIgnoreCandidateList,
  UnIgnoreListResponse,
  UnIgnoreUpdateRequest,
  UnIgnoreUpdateResponse,
} from "./visibility-types";
import { apiBase } from "./base";

export type Identity = {
  subject: string;
  role: string;
  scheme?: string;
};

/**
 * Thin REST wrapper around `/api/v1/*`. Auth cookies travel via the
 * default `credentials: "same-origin"` — the SPA's login form POSTs to
 * `/auth/login`, the daemon sets the session cookie, and every
 * subsequent request reuses it.
 */
class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly body: unknown,
    message: string,
  ) {
    super(message);
  }

  /**
   * The RFC 9457 problem-type identifier the daemon returned, or
   * empty string when the response body wasn't problem+json. Useful
   * for branching on known transient conditions like
   * `upstream_unavailable` (circuit-breaker open).
   */
  get problemCode(): string {
    const b = this.body;
    if (typeof b === "object" && b !== null && "code" in b) {
      const code = (b as { code: unknown }).code;
      return typeof code === "string" ? code : "";
    }
    return "";
  }

  /**
   * True when the daemon flagged this error as a transient south-bound
   * issue (CCU / interface in an unhealthy window). The SPA shows a
   * "retry in a few seconds" hint instead of a hard error.
   */
  get isUpstreamUnavailable(): boolean {
    return this.problemCode === "upstream_unavailable";
  }

  /**
   * The RFC 9457 `detail` field — the daemon's specific reason for
   * the error (e.g. `climate: IP mode AUTO: setvalue -5 invalid
   * value`). Surface this in the UI banner so the user / developer
   * sees what went wrong instead of a generic "Server-Fehler".
   */
  get problemDetail(): string {
    const b = this.body;
    if (typeof b === "object" && b !== null && "detail" in b) {
      const d = (b as { detail: unknown }).detail;
      return typeof d === "string" ? d : "";
    }
    return "";
  }
}

// CSRFCookieName / CSRFHeaderName mirror internal/auth/csrf.go. The
// daemon mounts a double-submit CSRF guard on the whole REST router, so
// every mutating request must echo the JS-readable csrf cookie back in
// this header — otherwise the daemon answers 403 "token mismatch".
const CSRF_COOKIE = "openccu_loom_csrf";
const CSRF_HEADER = "X-CSRF-Token";

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

// onUnauthorized is invoked whenever a non-auth REST call returns 401 —
// i.e. the session cookie went stale (typically after a daemon restart,
// since auth sessions live in memory). The auth store registers a handler
// that flips the SPA back to the login view; without it the background
// pollers (install-mode every 5s, the event stream) would keep hammering
// 401s instead of recovering. Auth-flow endpoints opt out via the guard
// in request() so a bad-credentials /auth/login does not self-trigger.
let onUnauthorized: (() => void) | null = null;

export function setUnauthorizedHandler(fn: (() => void) | null): void {
  onUnauthorized = fn;
}

function csrfToken(): string {
  const prefix = `${CSRF_COOKIE}=`;
  for (const part of document.cookie.split(";")) {
    const c = part.trim();
    if (c.startsWith(prefix)) {
      return decodeURIComponent(c.slice(prefix.length));
    }
  }
  return "";
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...((init.headers as Record<string, string>) ?? {}),
  };
  // Echo the double-submit CSRF token on mutating requests (the
  // daemon's safe-method allowlist matches SAFE_METHODS above).
  if (!SAFE_METHODS.has(method)) {
    const token = csrfToken();
    if (token) headers[CSRF_HEADER] = token;
  }
  const res = await fetch(`${apiBase()}${path}`, {
    credentials: "same-origin",
    ...init,
    // headers/credentials are spread last so the merged set (Accept +
    // per-call Content-Type + CSRF) is not clobbered by `...init`.
    headers,
  });
  if (!res.ok) {
    // A 401 on any non-login call means the session is no longer valid;
    // notify the auth store so the SPA returns to the login view (which
    // unmounts the app shell and stops its background pollers). /auth/login
    // is excluded — a 401 there is a bad-credentials response shown inline.
    if (res.status === 401 && path !== "/auth/login") {
      onUnauthorized?.();
    }
    // Read the body once as text, then try to parse as JSON. Calling
    // res.json() first and falling back to res.text() fails because
    // the body stream is already consumed by the failed json() call.
    const raw = await res.text();
    let body: unknown = raw;
    if (raw) {
      try {
        body = JSON.parse(raw);
      } catch {
        // keep raw text
      }
    }
    const detail =
      typeof body === "object" && body !== null && "detail" in body
        ? String((body as { detail: unknown }).detail)
        : typeof body === "string" && body
          ? body
          : res.statusText;
    throw new ApiError(
      res.status,
      body,
      `API ${res.status} ${path}: ${detail}`,
    );
  }
  if (res.status === 204) return undefined as T;
  // Many mutation endpoints reply 202 Accepted with an empty body
  // (PutSysvar, ExecuteProgram, SetValue, …). `res.json()` throws on
  // empty input, so we read the body as text first and only parse it
  // when something is there. Callers typed as <void> get undefined,
  // typed-as-T callers fall through to JSON parsing.
  const text = await res.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

// fetchAllPages pages through an endpoint whose body is a bare JSON array
// (no `{items,total}` wrapper) and whose pagination is expressed purely via
// `page`/`per_page` query params — e.g. /programs, /sysvars. A page shorter
// than `perPage` signals the end; a safety cap prevents unbounded looping on
// unexpected server behaviour. Mirrors the `Paginated<T>` loop in
// devices.svelte.ts, adapted for endpoints that don't echo a `total`.
async function fetchAllPages<T>(
  fetchPage: (page: number, perPage: number) => Promise<T[]>,
  perPage = 200,
  maxPages = 100,
): Promise<T[]> {
  const all: T[] = [];
  for (let page = 1; page <= maxPages; page++) {
    const items = await fetchPage(page, perPage);
    all.push(...items);
    if (items.length < perPage) return all;
  }
  console.warn(
    `[api] paginated fetch capped at ${maxPages} pages (${maxPages * perPage} items).`,
  );
  return all;
}

// editLockHeaders builds the header set for a configuration paramset
// write: always JSON, plus the X-Edit-Token when the caller holds an
// edit-lock token. The daemon rejects a MASTER/LINK write without a
// valid token with 423 Locked.
function editLockHeaders(editToken?: string): Record<string, string> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (editToken) headers["X-Edit-Token"] = editToken;
  return headers;
}

// alarmVerbInit builds the POST init for a code-carrying alarm verb
// (disarm / silence). A supplied code rides in an AlarmVerbRequest body;
// a code-free call sends no body at all, which the daemon tolerates
// (absent body == code-free, S3/S6).
function alarmVerbInit(code?: string): RequestInit {
  if (!code) return { method: "POST" };
  const body: AlarmVerbRequest = { code };
  return {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  };
}

export const api = {
  login(username: string, password: string) {
    return request<Identity>(`/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
  },
  logout() {
    return request<void>(`/auth/logout`, { method: "POST" });
  },
  me() {
    return request<Identity>(`/auth/me`);
  },
  // Self-service password change for the logged-in local user. Requires
  // the current password; the role is preserved server-side.
  changeOwnPassword(currentPassword: string, newPassword: string) {
    return request<void>(`/auth/me/password`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        current_password: currentPassword,
        new_password: newPassword,
      }),
    });
  },
  info() {
    return request<DaemonInfo>(`/info`);
  },
  // First-run onboarding. setupStatus is probed on boot to decide between
  // the wizard and the login screen; submitSetup atomically persists the
  // wizard's accumulated state. Both are unauthenticated — no admin exists
  // yet when the wizard runs — and submitSetup hard-gates server-side.
  setupStatus() {
    return request<{ required: boolean }>(`/setup/status`);
  },
  submitSetup(payload: SetupPayload) {
    return request<void>(`/setup`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  },
  listDevices(page = 1, perPage = 50) {
    return request<Paginated<DeviceSummary>>(
      `/devices?page=${page}&per_page=${perPage}`,
    );
  },
  getDevice(address: string) {
    return request<DeviceDetail>(`/devices/${encodeURIComponent(address)}`);
  },
  listChannels(address: string) {
    return request<{ items: DeviceDetail["channels"] }>(
      `/devices/${encodeURIComponent(address)}/channels`,
    );
  },
  listDataPoints(address: string, channel: number) {
    return request<DataPointSummary[]>(
      `/devices/${encodeURIComponent(address)}/channels/${channel}/data-points`,
    );
  },
  listCustomDataPoints(address: string) {
    return request<CustomDPSummary[]>(
      `/devices/${encodeURIComponent(address)}/cdps`,
    );
  },
  invokeCustomDataPoint(
    address: string,
    name: string,
    operation: string,
    params: Record<string, unknown> = {},
    priority?: "critical" | "high" | "default" | "low",
  ) {
    const body: Record<string, unknown> = { params };
    if (priority) body.priority = priority;
    return request<void>(
      `/devices/${encodeURIComponent(address)}/cdps/${encodeURIComponent(name)}/${encodeURIComponent(operation)}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      },
    );
  },
  uiSchema(
    address: string,
    channel: number,
    paramset: "VALUES" | "MASTER" | "LINK",
    locale: string,
    peer?: string,
    expert?: boolean,
  ) {
    const qs = new URLSearchParams({ locale, paramset });
    if (peer) qs.set("peer", peer);
    if (expert) qs.set("expert", "true");
    return request<UISchema>(
      `/devices/${encodeURIComponent(address)}/channels/${channel}/ui-schema?${qs.toString()}`,
    );
  },
  // Direct links (Direktverknüpfungen)
  listLinks(address: string, locale: string) {
    const qs = new URLSearchParams({ locale });
    return request<Link[]>(
      `/devices/${encodeURIComponent(address)}/links?${qs.toString()}`,
    );
  },
  addLink(
    address: string,
    body: {
      sender_address: string;
      receiver_address: string;
      name?: string;
      description?: string;
    },
  ) {
    return request<void>(`/devices/${encodeURIComponent(address)}/links`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  removeLink(address: string, sender: string, receiver: string) {
    const qs = new URLSearchParams({ sender, receiver });
    return request<void>(
      `/devices/${encodeURIComponent(address)}/links?${qs.toString()}`,
      { method: "DELETE" },
    );
  },
  linkableChannels(
    deviceAddress: string,
    channelNo: number,
    role: "sender" | "receiver",
    interfaceId: string,
    locale: string,
  ) {
    const qs = new URLSearchParams({ role, interface: interfaceId, locale });
    return request<LinkableChannel[]>(
      `/devices/${encodeURIComponent(deviceAddress)}/channels/${channelNo}/linkable-channels?${qs.toString()}`,
    );
  },
  putLinkParamset(
    channelAddress: string,
    peer: string,
    values: Record<string, unknown>,
    // Edit-lock token. LINK writes are configuration changes the daemon
    // gates behind the per-resource edit lock; pass the held token so
    // the write carries the X-Edit-Token header. Omitting it yields 423.
    editToken?: string,
  ) {
    return request<void>(
      `/devices/${encodeURIComponent(channelAddress)}/link-ps/${encodeURIComponent(peer)}`,
      {
        method: "PUT",
        headers: editLockHeaders(editToken),
        body: JSON.stringify(values),
      },
    );
  },
  putParamset(
    channelAddress: string,
    paramset: "VALUES" | "MASTER",
    values: Record<string, unknown>,
    // Edit-lock token. MASTER writes are gated behind the edit lock;
    // pass the held token so the write carries X-Edit-Token. VALUES
    // writes ignore it. Omitting it on MASTER yields 423 Locked.
    editToken?: string,
  ) {
    return request<void>(
      `/devices/${encodeURIComponent(channelAddress)}/paramsets/${paramset}`,
      {
        method: "PUT",
        headers: editLockHeaders(editToken),
        body: JSON.stringify(values),
      },
    );
  },
  setValue(address: string, channel: number, parameter: string, value: unknown) {
    return request<void>(
      `/devices/${encodeURIComponent(address)}/channels/${channel}/data-points/${parameter}/value`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ value }),
      },
    );
  },
  // --- Device lifecycle -----------------------------------------
  renameDevice(address: string, name: string) {
    return request<void>(`/devices/${encodeURIComponent(address)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
  },
  deleteDevice(address: string) {
    return request<void>(`/devices/${encodeURIComponent(address)}`, {
      method: "DELETE",
    });
  },
  acceptInboxDevice(address: string, central: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(
      `/devices/${encodeURIComponent(address)}/accept${qs}`,
      { method: "POST" },
    );
  },
  // --- Backups --------------------------------------------------
  // centralName selects the target explicitly (multi-CCU-correct); omit
  // to back up the first registered central for backward compatibility.
  triggerBackup(centralName?: string) {
    return request<{ id: string }>(`/backups`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(centralName ? { central_name: centralName } : {}),
    });
  },
  listBackups() {
    return request<BackupEntry[]>(`/backups`);
  },
  backupDownloadUrl(id: string): string {
    return `${apiBase()}/backups/${encodeURIComponent(id)}/download`;
  },
  // --- Sysvars / programs / messages ----------------------------
  // Force a re-pull of the CCU sysvar catalogue (SysVar.getAll) into the
  // hub model before reading it — without this a reload only serves the
  // daemon's periodic-poll state (up to one poll interval stale).
  fetchSysvars(central?: string) {
    const q = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/sysvars/fetch${q}`, { method: "POST" });
  },
  listSysvars() {
    return fetchAllPages<SysvarEntry>((page, perPage) =>
      request<SysvarEntry[]>(`/sysvars?page=${page}&per_page=${perPage}`),
    );
  },
  getSysvar(name: string) {
    return request<SysvarEntry>(`/sysvars/${encodeURIComponent(name)}`);
  },
  setSysvar(name: string, value: unknown, central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/sysvars/${encodeURIComponent(name)}${qs}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ value }),
    });
  },
  createSysvar(
    body: {
      name: string;
      value_type: string;
      unit?: string;
      min?: string;
      max?: string;
      value_list?: string[];
    },
    central: string,
  ) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/sysvars${qs}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  patchSysvar(
    name: string,
    body: {
      unit?: string;
      min?: string;
      max?: string;
      value_list?: string[];
      description?: string;
    },
    central?: string,
  ) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/sysvars/${encodeURIComponent(name)}${qs}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  deleteSysvar(name: string, central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/sysvars/${encodeURIComponent(name)}${qs}`, {
      method: "DELETE",
    });
  },
  setDeviceRooms(address: string, rooms: string[]) {
    return request<void>(`/devices/${encodeURIComponent(address)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ rooms }),
    });
  },
  listPrograms() {
    return fetchAllPages<ProgramEntry>((page, perPage) =>
      request<ProgramEntry[]>(`/programs?page=${page}&per_page=${perPage}`),
    );
  },
  executeProgram(id: string, central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(
      `/programs/${encodeURIComponent(id)}/execute${qs}`,
      { method: "POST" },
    );
  },
  setProgramEnabled(id: string, active: boolean, central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(
      `/programs/${encodeURIComponent(id)}${qs}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ active }),
      },
    );
  },
  listAlarmMessages() {
    return request<AlarmMessage[]>(`/alarm-messages`);
  },
  listServiceMessages() {
    return request<ServiceMessage[]>(`/service-messages`);
  },
  // --- Interfaces -----------------------------------------------
  listInterfaces() {
    return request<InterfaceInfo[]>(`/interfaces`);
  },
  reconnectInterface(id: string) {
    return request<void>(
      `/interfaces/${encodeURIComponent(id)}/reconnect`,
      { method: "POST" },
    );
  },
  // --- Audit log -----------------------------------------------
  listAudit(
    p: {
      limit?: number;
      offset?: number;
      device?: string;
      since?: string;
      until?: string;
    } = {},
  ) {
    const qs = new URLSearchParams({ limit: String(p.limit ?? 200) });
    if (p.offset) qs.set("offset", String(p.offset));
    if (p.device) qs.set("device", p.device);
    if (p.since) qs.set("since", p.since);
    if (p.until) qs.set("until", p.until);
    return request<AuditEntry[]>(`/audit?${qs.toString()}`);
  },
  auditDownloadUrl(p: {
    limit?: number;
    device?: string;
    since?: string;
    until?: string;
  }): string {
    const qs = new URLSearchParams({
      format: "csv",
      limit: String(p.limit ?? 10000),
    });
    if (p.device) qs.set("device", p.device);
    if (p.since) qs.set("since", p.since);
    if (p.until) qs.set("until", p.until);
    return `${apiBase()}/audit?${qs.toString()}`;
  },
  // --- Per-user preferences (favorites / dashboard) -----------
  // Values are opaque JSON owned by the SPA. getPreference resolves to
  // null when the key is unset (404).
  async getPreference<T = unknown>(key: string): Promise<T | null> {
    try {
      const r = await request<{ key: string; value: T }>(
        `/me/preferences/${encodeURIComponent(key)}`,
      );
      return r.value;
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) return null;
      throw err;
    }
  },
  async putPreference(key: string, value: unknown): Promise<void> {
    await request<void>(`/me/preferences/${encodeURIComponent(key)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(value),
    });
  },
  async deletePreference(key: string): Promise<void> {
    await request<void>(`/me/preferences/${encodeURIComponent(key)}`, {
      method: "DELETE",
    });
  },
  // Replace the daemon's TLS certificate at runtime (admin). The PEM
  // cert + key are sent as multipart/form-data; the certificate
  // hot-reloads so the API and SPA are re-secured without a restart.
  async uploadTLSCertificate(certPEM: File | Blob, keyPEM: File | Blob) {
    const form = new FormData();
    form.append("cert", certPEM);
    form.append("key", keyPEM);
    await request<void>(`/admin/tls/certificate`, {
      method: "POST",
      body: form,
    });
  },
  // --- Refresh devices (CCU re-pull) ---------------------------
  refreshDevices() {
    return request<void>(`/devices/refresh`, { method: "POST" });
  },
  // --- Users (admin) -------------------------------------------
  listUsers() {
    return request<UserListEntry[]>(`/auth/users`);
  },
  // --- Edit-session locking ------------------------------------
  openEditSession(key: string, subject?: string) {
    return request<EditSessionResponse>(`/sessions/edit`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key, subject }),
    });
  },
  heartbeatEditSession(s: EditSessionResponse) {
    return request<EditSessionResponse>(`/sessions/edit/heartbeat`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(s),
    });
  },
  closeEditSession(s: EditSessionResponse) {
    return request<void>(`/sessions/edit`, {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(s),
    });
  },
  takeOverEditSession(key: string) {
    return request<void>(`/sessions/edit/take-over`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key }),
    });
  },
  // --- Rooms / Functions (read-only) ---------------------------
  listRooms() {
    return request<RoomEntry[]>(`/rooms`);
  },
  listFunctions() {
    return request<FunctionEntry[]>(`/functions`);
  },
  // Room / function entity CRUD (CCU-side). `central` is required when
  // more than one CCU is configured.
  createRoom(name: string, central?: string) {
    return request<{ id: number; name: string }>(`/rooms`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, ...(central ? { central } : {}) }),
    });
  },
  renameRoom(name: string, newName: string, central?: string) {
    return request<void>(`/rooms/${encodeURIComponent(name)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ new_name: newName, ...(central ? { central } : {}) }),
    });
  },
  deleteRoom(name: string, central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/rooms/${encodeURIComponent(name)}${qs}`, {
      method: "DELETE",
    });
  },
  createFunction(name: string, central?: string) {
    return request<{ id: number; name: string }>(`/functions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, ...(central ? { central } : {}) }),
    });
  },
  renameFunction(name: string, newName: string, central?: string) {
    return request<void>(`/functions/${encodeURIComponent(name)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ new_name: newName, ...(central ? { central } : {}) }),
    });
  },
  deleteFunction(name: string, central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/functions/${encodeURIComponent(name)}${qs}`, {
      method: "DELETE",
    });
  },
  setDeviceFunctions(address: string, functions: string[]) {
    return request<void>(`/devices/${encodeURIComponent(address)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ functions }),
    });
  },
  // --- Inbox (pending pairings) --------------------------------
  listInbox() {
    return request<InboxDevice[]>(`/inbox`);
  },
  // --- Daemon configuration ------------------------------------
  config() {
    return request<ConfigSnapshot>(`/config`);
  },
  // --- Diagnostics ----------------------------------------------
  health() {
    return request<HealthSnapshot>(`/health`);
  },
  incidents() {
    return request<Incident[] | null>(`/incidents`).then((v) => v ?? []);
  },
  // --- Diagnostics --------------------------------------------
  diagnostics(anonymize: boolean = true) {
    const q = anonymize ? "?anonymize=1" : "?anonymize=0";
    return request<DiagnosticsEnvelope>(`/diagnostics${q}`);
  },
  rssiInfo() {
    return request<RSSIMatrix>(`/diagnostics/rssi`);
  },
  listLogLevels() {
    return request<LogLevelsResponse>(`/diagnostics/log-levels`);
  },
  setLogLevel(path: string, level: string, ttl_seconds: number = 0) {
    return request<void>(
      `/diagnostics/log-levels/${encodeURIComponent(path)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ level, ttl_seconds }),
      },
    );
  },
  resetLogLevel(path: string) {
    return request<void>(
      `/diagnostics/log-levels/${encodeURIComponent(path)}`,
      { method: "DELETE" },
    );
  },
  listCaptures() {
    return request<CaptureSummary[] | null>(`/diagnostics/capture`).then(
      (v) => v ?? [],
    );
  },
  startCapture(opts: {
    duration_seconds?: number;
    log_levels?: Record<string, string>;
    anonymise?: boolean;
  }) {
    return request<CaptureSummary>(`/diagnostics/capture`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(opts),
    });
  },
  stopCapture(id: string) {
    return request<CaptureSummary>(
      `/diagnostics/capture/${encodeURIComponent(id)}/stop`,
      { method: "POST" },
    );
  },
  captureDownloadURL(id: string): string {
    return `${apiBase()}/diagnostics/capture/${encodeURIComponent(id)}/download`;
  },
  // --- RPC traffic recording -----------------------------------
  listRpcRecordings() {
    return request<RpcRecordingStatus[]>(`/diagnostics/rpc-recording`).then(
      (v) => v ?? [],
    );
  },
  startRpcRecording(
    centrals?: string[],
    durationSeconds?: number,
    randomize?: boolean,
  ) {
    const body: Record<string, unknown> = {};
    if (centrals !== undefined) body.centrals = centrals;
    if (durationSeconds !== undefined) body.duration_seconds = durationSeconds;
    if (randomize !== undefined) body.randomize = randomize;
    return request<RpcRecordingStatus[]>(`/diagnostics/rpc-recording`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).then((v) => v ?? []);
  },
  stopRpcRecording(centrals?: string[]) {
    return request<RpcRecordingStatus[]>(`/diagnostics/rpc-recording/stop`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ centrals }),
    }).then((v) => v ?? []);
  },
  rpcRecordingDownloadUrl(central: string, format?: "map" | "golden"): string {
    const base = `${apiBase()}/diagnostics/rpc-recording/${encodeURIComponent(central)}/download`;
    return format ? `${base}?format=${encodeURIComponent(format)}` : base;
  },
  // --- Diagnostic log viewer ----------------------------------
  async getLogs(p: { limit?: number; minLevel?: string } = {}): Promise<{ last_seq: number; records: LogRecord[] }> {
    const qs = new URLSearchParams();
    if (p.limit !== undefined) qs.set("limit", String(p.limit));
    if (p.minLevel) qs.set("min_level", p.minLevel);
    const q = qs.toString() ? `?${qs.toString()}` : "";
    return request<{ last_seq: number; records: LogRecord[] }>(`/diagnostics/logs${q}`);
  },
  async getDefaultLogLevel(): Promise<string> {
    const r = await request<{ default: string }>(`/diagnostics/log-level`);
    return r.default;
  },
  async setDefaultLogLevel(level: string): Promise<string> {
    const r = await request<{ default: string }>(`/diagnostics/log-level`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ level }),
    });
    return r.default;
  },
  logsDownloadUrl(p: { limit: number; minLevel?: string }): string {
    const qs = new URLSearchParams({ download: "1", format: "ndjson", limit: String(p.limit) });
    if (p.minLevel) qs.set("min_level", p.minLevel);
    return `${apiBase()}/diagnostics/logs?${qs.toString()}`;
  },
  logsStreamUrl(p: { since?: number; minLevel?: string }): string {
    const qs = new URLSearchParams();
    if (p.since !== undefined) qs.set("since", String(p.since));
    if (p.minLevel) qs.set("min_level", p.minLevel);
    const q = qs.toString() ? `?${qs.toString()}` : "";
    return `${apiBase()}/diagnostics/logs/stream${q}`;
  },
  // --- System admin -------------------------------------------
  getStartupCapture() {
    return request<{
      enabled: boolean;
      duration_seconds: number;
      anonymise: boolean;
    }>(`/system/startup-capture`);
  },
  putStartupCapture(cfg: {
    enabled: boolean;
    duration_seconds: number;
    anonymise: boolean;
  }) {
    return request<typeof cfg>(`/system/startup-capture`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(cfg),
    });
  },
  restartDaemon() {
    return request<{ status: string; at: string }>(`/system/restart`, {
      method: "POST",
    });
  },
  getRestartPending() {
    return request<{ pending: boolean; fields: string[] }>(`/system/restart-pending`);
  },
  // Config field paths changed since the daemon started (boot diff).
  getConfigChanges() {
    return request<{ fields: string[] }>(`/system/config-changes`);
  },
  // Per-central fleet metadata (name/host/availability/model/version/
  // config-URL/configured interfaces) for the read-only cross-CCU
  // overview (Fleet.svelte). Reflects runtime-added CCUs live.
  async getSystemCCUs(): Promise<SystemCCUEntry[]> {
    const r = await request<{ entries: SystemCCUEntry[] }>(`/system/ccu`);
    return r.entries;
  },
  // --- CCU system (firmware) update ----------------------------
  getSystemUpdate() {
    return request<SystemUpdateEntry[]>(`/system/update`);
  },
  installSystemUpdate(central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/system/update/install${qs}`, { method: "POST" });
  },
  // --- Messages: ack / clear -----------------------------------
  ackAlarm(id: string, central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(
      `/alarm-messages/${encodeURIComponent(id)}/ack${qs}`,
      { method: "POST" },
    );
  },
  ackService(id: string, central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(
      `/service-messages/${encodeURIComponent(id)}/ack${qs}`,
      { method: "POST" },
    );
  },
  // --- Backup restore ------------------------------------------
  restoreBackup(id: string) {
    return request<{ id: string }>(
      `/backups/${encodeURIComponent(id)}/restore`,
      { method: "POST" },
    );
  },
  // --- Central links (Device.create_central_links) -------------
  centralLinksStatus(address: string) {
    return request<CentralLinksStatus>(
      `/devices/${encodeURIComponent(address)}/central-links`,
    );
  },
  createCentralLinks(address: string) {
    return request<CentralLinksReport>(
      `/devices/${encodeURIComponent(address)}/central-links`,
      { method: "POST" },
    );
  },
  removeCentralLinks(address: string) {
    return request<CentralLinksReport>(
      `/devices/${encodeURIComponent(address)}/central-links`,
      { method: "DELETE" },
    );
  },
  // --- Firmware update -----------------------------------------
  updateFirmware(address: string) {
    return request<{ status: string }>(
      `/devices/${encodeURIComponent(address)}/firmware/update`,
      { method: "POST" },
    );
  },
  // Force a re-read of per-device firmware data from every CCU so the
  // firmware overview reflects updates the CCU performed, without
  // waiting for the next scheduled poll. Synchronous 204.
  refreshFirmwareData() {
    return request<void>(`/devices/firmware/refresh`, { method: "POST" });
  },
  // --- Paramset export / import --------------------------------
  exportParamset(
    channelAddress: string,
    paramset: "VALUES" | "MASTER",
  ) {
    return request<Record<string, unknown>>(
      `/devices/${encodeURIComponent(channelAddress)}/paramsets/${paramset}`,
    );
  },
  importParamset(
    channelAddress: string,
    paramset: "VALUES" | "MASTER",
    values: Record<string, unknown>,
  ) {
    return request<void>(
      `/devices/${encodeURIComponent(channelAddress)}/paramsets/${paramset}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(values),
      },
    );
  },
  // --- Schedules -----------------------------------------------
  // Device-level: resolves the schedule channel automatically. Use
  // these from the device-detail tab unless a specific channel is
  // already known (legacy callers / scripts).
  getDeviceSchedule(address: string) {
    return request<ClimateSchedule>(
      `/devices/${encodeURIComponent(address)}/schedule`,
    );
  },
  putDeviceSchedule(address: string, schedule: ClimateSchedule) {
    return request<void>(`/devices/${encodeURIComponent(address)}/schedule`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(schedule),
    });
  },
  setDeviceActiveProfile(address: string, profile: string) {
    return request<void>(
      `/devices/${encodeURIComponent(address)}/schedule/active-profile`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ profile }),
      },
    );
  },
  // Channel-level (kept for back-compat).
  getSchedule(address: string, channelNo: number) {
    return request<ClimateSchedule>(
      `/devices/${encodeURIComponent(address)}/channels/${channelNo}/schedule`,
    );
  },
  putSchedule(address: string, channelNo: number, schedule: ClimateSchedule) {
    return request<void>(
      `/devices/${encodeURIComponent(address)}/channels/${channelNo}/schedule`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(schedule),
      },
    );
  },
  setActiveProfile(address: string, channelNo: number, profile: string) {
    return request<void>(
      `/devices/${encodeURIComponent(address)}/channels/${channelNo}/schedule/active-profile`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ profile }),
      },
    );
  },
  // --- Install mode --------------------------------------------
  // Install mode on the CCU is per-interface only (there is no CCU-wide
  // toggle). The operator opens teach-in on a single radio (BidCos-RF /
  // HmIP-RF, etc.), mirroring the CCU WebUI's interface-selective pairing.
  // `deviceAddress` requests targeted pairing (e.g. by serial).
  async listInstallModeInterfaces() {
    return request<InstallModeInterfaceEntry[]>(`/install-mode/interfaces`);
  },
  async setInstallModeInterface(
    iface: string,
    active: boolean,
    seconds?: number,
    deviceAddress?: string,
  ) {
    await request<void>(`/install-mode/interfaces`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        interface: iface,
        active,
        ...(seconds ? { seconds } : {}),
        ...(deviceAddress ? { device_address: deviceAddress } : {}),
      }),
    });
    return api.listInstallModeInterfaces();
  },
  // Open a targeted pairing window for a single device address (serial),
  // mirroring the CCU WebUI's serial-targeted teach-in.
  async pairDeviceInstallMode(address: string, seconds?: number) {
    await request<void>(`/devices/${encodeURIComponent(address)}/install-mode`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(seconds ? { seconds } : {}),
    });
  },
  // --- Matter bridge -------------------------------------------
  matterStatus() {
    return request<MatterStatus>(`/matter/status`);
  },
  matterFabrics() {
    return request<MatterFabricsResponse>(`/matter/fabrics`);
  },
  deleteMatterFabric(id: number) {
    return request<void>(`/matter/fabrics/${id}`, { method: "DELETE" });
  },
  matterExposable() {
    return request<MatterExposableResponse>(`/matter/exposable`);
  },
  putMatterExposure(update: MatterExposureUpdate) {
    return request<void>(`/matter/exposable`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(update),
    });
  },
  bulkMatterExposure(req: MatterBulkUpdateRequest) {
    return request<MatterBulkUpdateResponse>(`/matter/exposable/bulk`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    });
  },
  matterSetupPayload() {
    return request<MatterSetupPayload>(`/matter/setup-payload`);
  },
  openCommissioningWindow(duration_seconds: number) {
    return request<MatterCommissioningWindow>(`/matter/commissioning/window`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ duration_seconds }),
    });
  },
  closeCommissioningWindow() {
    return request<void>(`/matter/commissioning/window/close`, {
      method: "POST",
    });
  },
  matterShareBridge(duration_seconds: number) {
    return request<MatterCommissioningWindow>(`/matter/share`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ duration_seconds }),
    });
  },
  // --- Visibility / un_ignore ---------------------------------
  listVisibilityUnIgnore() {
    return request<UnIgnoreListResponse>(`/visibility/unignore`);
  },
  listVisibilityUnIgnoreCandidates(includeMaster = false) {
    return request<UnIgnoreCandidateList>(
      `/visibility/unignore/candidates?include_master=${includeMaster}`,
    );
  },
  putVisibilityUnIgnore(req: UnIgnoreUpdateRequest) {
    return request<UnIgnoreUpdateResponse>(`/visibility/unignore`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    });
  },
  // --- Live config edit ---------------------------------------
  // GET /config/schema returns the typed field schema + the
  // section list the SPA renders as tabs.
  getConfigSchema() {
    return request<ConfigSchemaResponse>(`/config/schema`);
  },
  getEffectiveConfig() {
    return request<EffectiveConfigResponse>(`/config/effective`);
  },
  // Per-section CRUD. Section payload is free-form JSON shaped
  // like the matching Go struct (see assets/openapi.yaml).
  getConfigSection<T = unknown>(section: string) {
    return request<T>(`/config/sections/${encodeURIComponent(section)}`);
  },
  putConfigSection<T = unknown>(section: string, value: T) {
    return request<{
      section: string;
      version: number;
      updated_at: string;
      restart_required: boolean;
    }>(`/config/sections/${encodeURIComponent(section)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(value),
    });
  },
  deleteConfigSection(section: string) {
    return request<void>(`/config/sections/${encodeURIComponent(section)}`, {
      method: "DELETE",
    });
  },
  resetConfigField(path: string) {
    return request<void>(`/config/fields/${encodeURIComponent(path)}`, {
      method: "DELETE",
    });
  },
  // Trigger an atomic MQTT-stack rebuild from the live config. The
  // new broker connection is established before the previous one is
  // torn down; on failure the previous stack continues to serve.
  reloadMQTT() {
    return request<{ reloaded: boolean; took_ms: number }>(`/admin/mqtt/reload`, {
      method: "POST",
    });
  },
  // Clear CCU-derived in-memory and on-disk caches. Does NOT touch
  // config, visibility, auth, or Matter state. 200 = full success,
  // 502 = partial (report still in body), 503 = feature unavailable.
  clearCache(body: {
    kind: "global" | "central" | "interface" | "device";
    central?: string;
    interface?: string;
    device?: string;
  }) {
    return request<CacheClearReport>(`/admin/cache/clear`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  // Per-(central,interface) circuit-breaker + connection-state snapshot.
  // Optionally scoped to one central.
  getReliability(central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<ReliabilityRow[]>(`/diagnostics/reliability${qs}`);
  },
  // Persistent VALUES-cache statistics (row count, byte size, cumulative
  // restore/cast/gc/flush counters since process start). See ADR 0018.
  getValuesCacheStats() {
    return request<ValuesCacheStats>(`/admin/values-cache/stats`);
  },
  // Drop every row from the persistent VALUES cache, or — when `address`
  // is given — only the rows for that one device.
  resetValuesCache(address?: string) {
    if (address) {
      return request<void>(
        `/devices/${encodeURIComponent(address)}/values-cache/reset`,
        { method: "POST" },
      );
    }
    return request<void>(`/admin/values-cache/reset`, { method: "POST" });
  },
  // User CRUD ----------------------------------------------------
  listUsersV2() {
    return request<UserSummaryV2[]>(`/users`);
  },
  createUser(body: { username: string; password: string; role: string }) {
    return request<{ subject: string; role: string }>(`/users`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  updateUser(subject: string, body: { password?: string; role?: string }) {
    return request<void>(`/users/${encodeURIComponent(subject)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  deleteUser(subject: string) {
    return request<void>(`/users/${encodeURIComponent(subject)}`, {
      method: "DELETE",
    });
  },
  // Token CRUD ---------------------------------------------------
  listTokensV2() {
    return request<TokenSummaryV2[]>(`/auth/tokens/v2`);
  },
  createTokenV2(body: { subject: string; role: string }) {
    return request<{ token: string; fingerprint: string }>(`/auth/tokens/v2`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  deleteTokenV2(fingerprint: string) {
    return request<void>(
      `/auth/tokens/v2/${encodeURIComponent(fingerprint)}`,
      { method: "DELETE" },
    );
  },
  // Centrals CRUD ------------------------------------------------
  listCentralsV2() {
    return request<CentralRow[]>(`/centrals`);
  },
  getCentralV2(name: string) {
    return request<CentralRow>(`/centrals/${encodeURIComponent(name)}`);
  },
  createCentralV2(row: CentralRow) {
    return request<{ name: string }>(`/centrals`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(row),
    });
  },
  updateCentralV2(name: string, row: CentralRow) {
    return request<void>(`/centrals/${encodeURIComponent(name)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(row),
    });
  },
  deleteCentralV2(name: string) {
    return request<void>(`/centrals/${encodeURIComponent(name)}`, {
      method: "DELETE",
    });
  },
  // CCU discovery (SSDP/UPnP) ------------------------------------
  listDiscoveredCentrals() {
    return request<DiscoveredCCU[]>(`/centrals/discovered`);
  },
  listIgnoredCentrals() {
    return request<IgnoredCCU[]>(`/centrals/discovered/ignored`);
  },
  ignoreDiscoveredCentral(serial: string) {
    return request<void>(
      `/centrals/discovered/${encodeURIComponent(serial)}/ignore`,
      { method: "POST" },
    );
  },
  unignoreDiscoveredCentral(serial: string) {
    return request<void>(
      `/centrals/discovered/${encodeURIComponent(serial)}/ignore`,
      { method: "DELETE" },
    );
  },
  // --- Alarm panel (native intrusion-alarm engine) --------------
  // docs/alarm-concept.md §13. Areas are daemon-level (no central
  // scoping in the path); sensors/outputs reference (central,
  // channel_address) inside their bodies. Control verbs
  // (arm/disarm/silence/…) are the safety surface — the alarm store
  // wraps them so a failure toasts but never blocks the UI (S3/S6).
  getAlarmState() {
    return request<{ areas: AlarmAreaStatus[] }>(`/alarm/state`);
  },
  listAlarmAreas() {
    return request<AlarmArea[]>(`/alarm/areas`);
  },
  createAlarmArea(area: AlarmArea) {
    return request<AlarmArea>(`/alarm/areas`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(area),
    });
  },
  getAlarmArea(id: string) {
    return request<AlarmArea>(`/alarm/areas/${encodeURIComponent(id)}`);
  },
  putAlarmArea(id: string, area: AlarmArea) {
    return request<void>(`/alarm/areas/${encodeURIComponent(id)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(area),
    });
  },
  deleteAlarmArea(id: string) {
    return request<void>(`/alarm/areas/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },
  listAlarmAreaSensors(id: string) {
    return request<AlarmSensor[]>(
      `/alarm/areas/${encodeURIComponent(id)}/sensors`,
    );
  },
  putAlarmAreaSensors(id: string, sensors: AlarmSensor[]) {
    return request<void>(`/alarm/areas/${encodeURIComponent(id)}/sensors`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(sensors),
    });
  },
  listAlarmAreaOutputs(id: string) {
    return request<AlarmOutput[]>(
      `/alarm/areas/${encodeURIComponent(id)}/outputs`,
    );
  },
  putAlarmAreaOutputs(id: string, outputs: AlarmOutput[]) {
    return request<void>(`/alarm/areas/${encodeURIComponent(id)}/outputs`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(outputs),
    });
  },
  listAlarmOutputCandidates(cls?: string) {
    const q = cls ? `?class=${encodeURIComponent(cls)}` : "";
    return request<AlarmOutputCandidate[]>(`/alarm/output-candidates${q}`);
  },
  listAlarmRemoteKeyCandidates() {
    return request<AlarmRemoteKeyCandidate[]>(`/alarm/remote-key-candidates`);
  },
  armAlarmArea(id: string, req: AlarmArmRequest) {
    return request<AlarmArmAccepted>(
      `/alarm/areas/${encodeURIComponent(id)}/arm`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(req),
      },
    );
  },
  disarmAlarmArea(id: string, code?: string) {
    return request<void>(
      `/alarm/areas/${encodeURIComponent(id)}/disarm`,
      alarmVerbInit(code),
    );
  },
  silenceAlarmArea(id: string, code?: string) {
    return request<void>(
      `/alarm/areas/${encodeURIComponent(id)}/silence`,
      alarmVerbInit(code),
    );
  },
  acknowledgeAlarmArea(id: string) {
    return request<void>(
      `/alarm/areas/${encodeURIComponent(id)}/acknowledge`,
      { method: "POST" },
    );
  },
  silenceAllAlarmAreas() {
    return request<void>(`/alarm/silence-all`, { method: "POST" });
  },
  getAlarmAreaReadiness(id: string) {
    return request<Record<string, AlarmModeReadiness>>(
      `/alarm/areas/${encodeURIComponent(id)}/readiness`,
    );
  },
  // Alarm codes (operator-gated; hash + cleartext PIN never returned —
  // docs/alarm-concept.md §11/§16). The write body's `pin` is
  // write-only: omit it on update to keep the stored hash.
  listAlarmCodes() {
    return request<AlarmCode[]>(`/alarm/codes`);
  },
  createAlarmCode(body: AlarmCodeRequest) {
    return request<AlarmCode>(`/alarm/codes`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  getAlarmCode(id: string) {
    return request<AlarmCode>(`/alarm/codes/${encodeURIComponent(id)}`);
  },
  putAlarmCode(id: string, body: AlarmCodeRequest) {
    return request<void>(`/alarm/codes/${encodeURIComponent(id)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  deleteAlarmCode(id: string) {
    return request<void>(`/alarm/codes/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },
  listAlarmJournal(
    p: {
      area?: string;
      class?: AlarmJournalClass;
      from?: string;
      to?: string;
      limit?: number;
    } = {},
  ) {
    const qs = new URLSearchParams();
    if (p.area) qs.set("area", p.area);
    if (p.class) qs.set("class", p.class);
    if (p.from) qs.set("from", p.from);
    if (p.to) qs.set("to", p.to);
    if (p.limit !== undefined) qs.set("limit", String(p.limit));
    const q = qs.toString() ? `?${qs.toString()}` : "";
    return request<AlarmJournalEntry[]>(`/alarm/journal${q}`);
  },
  startAlarmWalkTest(id: string) {
    return request<void>(
      `/alarm/areas/${encodeURIComponent(id)}/walktest/start`,
      { method: "POST" },
    );
  },
  stopAlarmWalkTest(id: string) {
    return request<void>(
      `/alarm/areas/${encodeURIComponent(id)}/walktest/stop`,
      { method: "POST" },
    );
  },
  getAlarmWalkTestStatus(id: string) {
    return request<AlarmWalkTestStatus>(
      `/alarm/areas/${encodeURIComponent(id)}/walktest`,
    );
  },
  testAlarmOutput(id: string, req: AlarmOutputTestRequest = {}) {
    return request<void>(`/alarm/outputs/${encodeURIComponent(id)}/test`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    });
  },
};

// DiscoveredCCU mirrors one entry of GET /api/v1/centrals/discovered: a central
// found on the LAN via SSDP/UPnP, flagged whether it is already configured.
export type DiscoveredCCU = {
  serial: string;
  name: string;
  host: string;
  // Address to pre-fill on adoption — "localhost" for a CCU on the daemon's own
  // host, a reverse-resolved docker hostname for a co-located HA add-on, else
  // equal to host.
  suggested_host: string;
  manufacturer?: string;
  model?: string;
  last_seen: string;
  already_configured: boolean;
};

// IgnoredCCU mirrors one entry of GET /api/v1/centrals/discovered/ignored.
export type IgnoredCCU = {
  serial: string;
  name?: string;
  host?: string;
  ignored_at: string;
  ignored_by?: string;
};

// SystemUpdateEntry mirrors the Go SystemUpdateEntry (GET /system/update):
// the CCU's firmware-update state, one entry per central.
export type SystemUpdateEntry = {
  central?: string;
  current_firmware?: string;
  available_firmware?: string;
  update_available: boolean;
  in_progress: boolean;
  observed: boolean;
};

// SetupPayload mirrors the POST /api/v1/setup request body. `ccu` and
// `mqtt` are optional — omit them to skip that wizard step.
export type SetupPayload = {
  admin: { username: string; password: string };
  locale: { locale: "de" | "en"; theme: "light" | "dark" | "system" };
  ccu?: {
    name: string;
    host: string;
    username?: string;
    password?: string;
    interfaces: string[];
  };
  mqtt?: {
    broker_url: string;
    username?: string;
    password?: string;
  };
};

export type DaemonInfo = {
  version: string;
  commit: string;
  build_date: string;
  // True when the daemon binary was built as the CCU/RaspberryMatic
  // add-on (i.e. it runs on the CCU itself).
  addon_build: boolean;
  uptime: string;
  started_at: string;
  api_version: string;
  capabilities: string[];
};

// --- Type definitions for the live-edit config surface ---------

export type ConfigSchemaField = {
  path: string;
  class: "basic" | "expert" | "secret" | "";
  go_type: string;
  restart_required?: boolean;
  /**
   * Effective default value the daemon falls back to when neither
   * YAML nor SQLite nor an env-override supplies a value. Carries
   * the curated "consumer-resolved" defaults (e.g. ValuesCache
   * enabled = true, FlushInterval = 60s) in addition to the
   * Go zero value where it actually matters. Undefined when the
   * default is just the Go zero (0 / false / "").
   */
  default?: unknown;
};

export type ConfigSchemaResponse = {
  sections: string[];
  fields: ConfigSchemaField[];
};

export type ConfigFieldSource = "bootstrap" | "db" | "env" | "default";

export type EffectiveConfigResponse = {
  config: Record<string, unknown>;
  sources: Record<string, ConfigFieldSource>;
};

export type UserSummaryV2 = {
  subject: string;
  role: string;
  created_at?: string;
  last_seen_at?: string | null;
};

export type TokenSummaryV2 = {
  fingerprint: string;
  subject: string;
  role: string;
  created_at?: string;
  last_seen_at?: string | null;
};

export type InterfaceSpec = {
  name: string;
  port?: number;
  remote_path?: string;
  rpc_type?: string;
};

export type VisibilityConfig = {
  un_ignore?: string[];
};

export type CentralRow = {
  name: string;
  host: string;
  // Hardware serial captured at adoption; lets discovery match this central by
  // serial regardless of host. Empty/omitted for manual entries.
  serial?: string;
  port?: number;
  json_rpc_port?: number;
  username?: string;
  password_env?: string;
  password_plain?: string;
  tls?: boolean;
  tls_insecure_skip_verify?: boolean;
  primary_interface?: string;
  interfaces: InterfaceSpec[];
  ports?: Record<string, number>;
  visibility?: VisibilityConfig;
  behavior?: CentralBehavior;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
};

/**
 * Per-central behaviour toggles (CentralConfig.Behavior). All fields
 * optional; an omitted field means "daemon default". `sysvar_scan_interval`
 * is a Go time.Duration on the wire — i.e. **nanoseconds** as a number.
 */
export type DescriptionMarker = "HAHM" | "HX" | "INTERNAL" | "MQTT";

export type CentralBehavior = {
  light_last_brightness?: boolean;
  use_group_channel_for_cover_state?: boolean;
  enable_sysvar_scan?: boolean;
  enable_program_scan?: boolean;
  include_internal_sysvars?: boolean;
  include_internal_programs?: boolean;
  sysvar_markers?: DescriptionMarker[];
  program_markers?: DescriptionMarker[];
  sysvar_scan_interval?: number; // nanoseconds (Go time.Duration)
  enable_device_firmware_check?: boolean;
  delay_new_device_creation?: boolean;
};

/**
 * One aggregated measurement bucket returned by GET /api/v1/history.
 * The ts field is the UTC start of the bucket's time span (RFC3339).
 */
export type HistoryBucket = {
  ts: string;
  avg: number;
  min: number;
  max: number;
  count: number;
};

/**
 * Thrown by getHistory when the history feature is disabled on the
 * daemon (the /history route returns 404). Callers distinguish this
 * from a generic 404 so the UI can display a "history not enabled"
 * message instead of a generic error banner.
 */
export class HistoryDisabledError extends Error {
  constructor() {
    super("history feature not enabled");
    this.name = "HistoryDisabledError";
  }
}

/**
 * Fetch bucketed measurement history for one numeric data point.
 * Returns an empty array when the daemon returns no buckets (valid
 * range with no recorded samples). Throws HistoryDisabledError when
 * the daemon returns 404 (feature off). Re-throws ApiError for 400/5xx.
 */
export async function getHistory(params: {
  central: string;
  interfaceId: string;
  channel: string;
  parameter: string;
  from: string;
  to: string;
  buckets?: number;
}): Promise<HistoryBucket[]> {
  const qs = new URLSearchParams({
    central: params.central,
    interface_id: params.interfaceId,
    channel: params.channel,
    parameter: params.parameter,
    from: params.from,
    to: params.to,
  });
  if (params.buckets !== undefined) {
    qs.set("buckets", String(params.buckets));
  }
  try {
    const result = await request<HistoryBucket[]>(`/history?${qs.toString()}`);
    return result ?? [];
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      throw new HistoryDisabledError();
    }
    throw err;
  }
}

/**
 * Fetch the per-device power/energy breakdown for a central over a
 * time range (GET /api/v1/energy). Wh on the wire; callers divide by
 * 1000 to render kWh. Throws HistoryDisabledError when the daemon
 * returns 404 (history feature off — the energy rollups share the
 * same gate). Re-throws ApiError for 400/5xx.
 */
export async function getEnergy(params: {
  central: string;
  from: string;
  to: string;
  group?: "hour" | "day" | "month";
  device?: string;
}): Promise<EnergyResponse> {
  const qs = new URLSearchParams({
    central: params.central,
    from: params.from,
    to: params.to,
  });
  if (params.group !== undefined) {
    qs.set("group", params.group);
  }
  if (params.device !== undefined) {
    qs.set("device", params.device);
  }
  try {
    return await request<EnergyResponse>(`/energy?${qs.toString()}`);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      throw new HistoryDisabledError();
    }
    throw err;
  }
}

export { ApiError };

/**
 * Translate any thrown error into a short, locale-friendly user
 * message — recognises the daemon's problem-types so transient
 * upstream issues (circuit-breaker open, auth retry) read as a
 * normal "try again in a moment" instead of a raw stack-trace
 * paste. Falls back to the raw error text.
 */
export function friendlyError(
  err: unknown,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  if (err instanceof ApiError) {
    if (err.isUpstreamUnavailable) {
      return t("api.error.upstream_unavailable");
    }
    if (err.status === 401) {
      return t("api.error.unauthorized");
    }
    if (err.status === 403) {
      return t("api.error.forbidden");
    }
    if (err.status === 404) {
      return t("api.error.not_found");
    }
    if (err.status === 429) {
      return t("api.error.rate_limited");
    }
    if (err.status >= 500) {
      // Surface the daemon's problem.detail when available — it
      // carries the actual failure reason (e.g.
      // "climate: IP mode AUTO: setvalue -5 invalid value")
      // far more useful than the generic "Server-Fehler" label.
      const detail = err.problemDetail;
      if (detail) {
        return `${t("api.error.server", { status: String(err.status) })} — ${detail}`;
      }
      return t("api.error.server", { status: String(err.status) });
    }
    return err.message;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}

export type CacheClearReport = {
  scope: string;
  devices: number;
  paramsets: number;
  values: number;
  master: number;
  centrals_reinit: number;
  errors: number;
};

// Live InterfaceClient state embedded in a ReliabilityRow, when the client
// exposes one. Mirrors GET /diagnostics/reliability's `state` object.
export type ReliabilityClientState = {
  state?: string;
  closed?: boolean;
  total_requests?: number;
  executed_requests?: number;
  pending_requests?: number;
  last_failure_at?: string;
  last_callback_at?: string;
};

// One row of GET /diagnostics/reliability: circuit-breaker + connection
// state for a single (central, interface) pair.
export type ReliabilityRow = {
  central: string;
  interface: string;
  circuit_state: number;
  state?: ReliabilityClientState;
};

export type ValuesCacheStats = {
  rows: number;
  value_json_bytes: number;
  restored_rows: number;
  cast_failures: number;
  gc_rows_deleted: number;
  flush_batches: number;
  flushed_entries: number;
};
