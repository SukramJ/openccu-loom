import type {
  AlarmMessage,
  AuditEntry,
  BackupEntry,
  CentralLinksReport,
  CentralLinksStatus,
  ConfigSnapshot,
  EditSessionResponse,
  FunctionEntry,
  InboxDevice,
  RoomEntry,
  TokenListEntry,
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
  RpcRecordingStatus,
  ServiceMessage,
  SysvarEntry,
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
 * default `credentials: "same-origin"` — the daemon sets the session
 * cookie on the HTMX login page, and the SPA reuses it.
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
  info() {
    return request<DaemonInfo>(`/info`);
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
  ) {
    return request<void>(
      `/devices/${encodeURIComponent(channelAddress)}/link-paramsets/${encodeURIComponent(peer)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(values),
      },
    );
  },
  putParamset(
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
  triggerBackup() {
    return request<{ id: string }>(`/backups`, { method: "POST" });
  },
  listBackups() {
    return request<BackupEntry[]>(`/backups`);
  },
  backupDownloadUrl(id: string): string {
    return `${apiBase()}/backups/${encodeURIComponent(id)}/download`;
  },
  // --- Sysvars / programs / messages ----------------------------
  listSysvars() {
    return request<SysvarEntry[]>(`/sysvars`);
  },
  getSysvar(name: string) {
    return request<SysvarEntry>(`/sysvars/${encodeURIComponent(name)}`);
  },
  setSysvar(name: string, value: unknown, central: string) {
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
    central: string,
  ) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/sysvars/${encodeURIComponent(name)}${qs}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  },
  deleteSysvar(name: string, central: string) {
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
    return request<ProgramEntry[]>(`/programs`);
  },
  executeProgram(id: string, central: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(
      `/programs/${encodeURIComponent(id)}/execute${qs}`,
      { method: "POST" },
    );
  },
  setProgramEnabled(id: string, active: boolean, central: string) {
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
  listAudit(limit = 200) {
    return request<AuditEntry[]>(`/audit?limit=${limit}`);
  },
  // --- Refresh devices (CCU re-pull) ---------------------------
  refreshDevices() {
    return request<void>(`/devices/refresh`, { method: "POST" });
  },
  // --- Users (admin) -------------------------------------------
  listUsers() {
    return request<UserListEntry[]>(`/auth/users`);
  },
  listTokens() {
    return request<TokenListEntry[]>(`/auth/tokens`);
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
  // --- CCU system (firmware) update ----------------------------
  getSystemUpdate() {
    return request<SystemUpdateEntry[]>(`/system/update`);
  },
  installSystemUpdate(central?: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(`/system/update/install${qs}`, { method: "POST" });
  },
  // --- Messages: ack / clear -----------------------------------
  ackAlarm(id: string, central: string) {
    const qs = central ? `?central=${encodeURIComponent(central)}` : "";
    return request<void>(
      `/alarm-messages/${encodeURIComponent(id)}/ack${qs}`,
      { method: "POST" },
    );
  },
  ackService(id: string, central: string) {
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
  async getInstallMode() {
    const r = await request<{ active: boolean; seconds?: number }>(
      `/install-mode`,
    );
    return {
      active: r.active,
      remaining_seconds: r.seconds ?? null,
    };
  },
  async setInstallMode(active: boolean, seconds?: number) {
    // The handler acks with 202 and no body; the caller re-reads
    // state via getInstallMode(). We expose a unified return shape.
    await request<void>(`/install-mode`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        active,
        ...(seconds ? { seconds } : {}),
      }),
    });
    return api.getInstallMode();
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

export type DaemonInfo = {
  version: string;
  commit: string;
  build_date: string;
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
