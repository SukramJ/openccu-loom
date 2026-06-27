// REST DTOs. Types that have a matching openapi.yaml schema are
// re-exported from types.generated.ts so the SPA tracks the contract
// automatically; the remaining hand-written definitions are the ones
// with no openapi schema at all (generic wrappers like Paginated, the
// SPA-internal normalized WS event shapes, and diagnostics/inbox/log
// surfaces not yet modelled in the spec).
import type { components } from "./types.generated";

// DeviceSummary re-exported from generated schema — openapi.yaml now
// carries every field the Go DTO emits (central, model_label, model_icon,
// update_available, has_sub_devices, master_pushes_config_pending,
// functions) with updatable / update_available / has_sub_devices /
// master_pushes_config_pending marked required to match the always-emitted
// Go json tags.
export type DeviceSummary = components["schemas"]["DeviceSummary"];

// ChannelSummary re-exported from generated schema — matches SPA usage.
// Generated adds: category, paramset_keys, room; those are additive and safe.
export type ChannelSummary = components["schemas"]["ChannelSummary"];

// CustomDPSummary re-exported from generated schema.
// Generated adds: state (live state snapshot). Additive, safe for SPA.
export type CustomDPSummary = components["schemas"]["CustomDPSummary"];

/** Wire-side lifecycle token for a data point. See ADR 0018. */
export type DataPointSource = "unobserved" | "cache" | "live" | "stale";

// DataPointSummary and UIHint re-exported from generated schema.
// Generated adds: category, data_point_type, usage — additive, safe.
export type DataPointSummary = components["schemas"]["DataPointSummary"];

/** Per-DP UI classification the daemon computes via `pkg/hmui.HintFor`. */
export type UIHint = NonNullable<components["schemas"]["DataPointSummary"]["ui_hint"]>;

// DeviceDetail re-exported from generated schema. openapi.yaml now marks
// firmware / availability / channels required (the handler always emits
// them), so the generated members are non-optional and SPA callers can
// index detail.channels without a guard. firmware keeps the capitalized
// keys (Current/Available/Updatable/UpdateState) the Go DTO marshals from
// device.FirmwareInfo; availability uses the capitalized keys the Go DTO
// marshals from device.AvailabilityInfo (IsReachable/LastUpdated/...).
export type DeviceDetail = components["schemas"]["DeviceDetail"];

// Paginated is generic — not generated (openapi-typescript generates per-endpoint
// response types, not a generic wrapper). Keep hand-written.
export type Paginated<T> = {
  items: T[];
  page: number;
  per_page: number;
  total: number;
};

/** Shape of the WS envelope the daemon publishes on /api/v1/events. */
export type EventEnvelope =
  | { type: "data_point"; payload: DataPointChangedEvent }
  | { type: "custom_data_point"; payload: CustomDataPointStateEvent }
  | { type: "device_available"; payload: DeviceAvailableEvent }
  | { type: "sysvar"; payload: SysvarChangedEvent }
  | { type: string; payload: unknown };

// DataPointChangedEvent is the SPA's internal normalized shape produced by
// ws.ts normalizeEvent(). It differs from the wire's DataPointValueChangedPayload
// (channel_address vs. device_address+channel, fewer fields). Keep hand-written.
export type DataPointChangedEvent = {
  central: string;
  interface: string;
  channel_address: string;
  parameter: string;
  value: unknown;
};

/** Aggregated state snapshot for a Custom-DP. Emitted whenever a
 *  wire-DP on the CDP's channel changes — gives SPA tiles one
 *  subscription per CDP instead of one per slot. */
// CustomDataPointStateEvent is the SPA's normalized shape from ws.ts normalizeEvent().
// It differs from the wire's CustomDataPointStateChangedPayload in field set.
export type CustomDataPointStateEvent = {
  central: string;
  device_address: string;
  channel: number;
  name: string;
  kind?: string;
  state: Record<string, unknown>;
};

export type DeviceAvailableEvent = {
  central: string;
  address: string;
  available: boolean;
};

// SysvarChangedEvent is the SPA's normalized shape. The wire payload
// (SysvarChangedPayload) carries additional fields not needed in the SPA.
export type SysvarChangedEvent = {
  central: string;
  name: string;
  value: unknown;
};

// --- UI schema -----------------------------------------------------

export type UISchema = {
  channel: {
    address: string;
    number: number;
    type: string;
    label?: string;
    device_address: string;
  };
  groups?: UISchemaGroup[];
  parameter_order?: string[];
  parameters: UISchemaParameter[];
  visibility?: UISchemaVisibility[];
  cross_validations?: UISchemaCrossValidation[];
  profile?: UISchemaProfile;
  subset_groups?: UISchemaSubsetGroup[];
};

export type UISchemaGroup = {
  id: string;
  label: string;
  parameters: string[];
};

export type UISchemaParameter = {
  name: string;
  label?: string;
  help?: string;
  type: "BOOL" | "INTEGER" | "FLOAT" | "STRING" | "ENUM" | "ACTION" | string;
  unit?: string;
  min?: unknown;
  max?: unknown;
  default?: unknown;
  value_list?: UISchemaValueListEntry[];
  operations: { read: boolean; write: boolean; event: boolean };
  flags: { visible: boolean; internal: boolean; service: boolean };
  control?: string;
  value?: unknown;
  observed: boolean;
  modified_at?: string;
  group_id?: string;
  preset?: string;
  // LINK-paramset classification (only present for paramset=LINK).
  // Port of aiohomematic-config's link_param_metadata: category,
  // SHORT/LONG/COMMON keypress group, level-as-percent hint, paired
  // TIME_BASE/TIME_FACTOR linking, and hidden-by-default flags for
  // advanced JT_/CT_/ACTION_TYPE parameters.
  category?:
    | "time"
    | "level"
    | "jump_target"
    | "condition"
    | "action"
    | "other"
    | string;
  keypress_group?: "short" | "long" | "common" | string;
  display_as_percent?: boolean;
  has_last_value?: boolean;
  hidden_by_default?: boolean;
  time_pair_id?: string;
  time_selector_type?: "timeOnOff" | "delay" | "rampOnOff" | string;
  time_presets?: UISchemaTimePreset[];
  presets?: UISchemaPreset[];
};

export type UISchemaPreset = {
  label: string;
  value: unknown;
};

export type UISchemaTimePreset = {
  base: number;
  factor: number;
  label: string;
};

export type UISchemaValueListEntry = {
  value: number;
  key: string;
  label?: string;
};

export type UISchemaVisibility = {
  show: string[];
  trigger: string;
  trigger_value: unknown;
};

export type UISchemaCrossValidation = {
  id: string;
  rule: string;
  param_a: string;
  param_b: string;
  applies_to_params: string[];
  error?: string;
};

export type UISchemaSubsetOption = {
  id: number;
  label: string;
  values: Record<string, unknown>;
};

export type UISchemaSubsetGroup = {
  id: string;
  label: string;
  member_params: string[];
  current_option_id?: number;
  options: UISchemaSubsetOption[];
};

export type UISchemaProfile = {
  receiver_type: string;
  // Sender channel type of the link — set only for LINK paramsets.
  // The profile selector uses it to trim its variant list to the
  // ones the CCU actually ships for this specific sender/receiver
  // pair.
  sender_type?: string;
  // ID of the profile whose constraints match the current values;
  // 0 or omitted means no preset matches (Expert mode). The SPA
  // pre-selects this entry on load.
  active_profile_id?: number;
  raw?: Record<string, unknown>;
};

// --- direct links (Direktverknüpfungen) --------------------------

// Link re-exported from generated schema — shapes match exactly.
export type Link = components["schemas"]["Link"];

// --- Schedules ----------------------------------------------------

// Schedule types re-exported from generated schema — shapes match.
export type ClimatePeriod = components["schemas"]["ClimatePeriod"];
export type ClimateWeekday = components["schemas"]["ClimateWeekday"];
export type ClimateProfile = components["schemas"]["ClimateProfile"];
export type SimpleScheduleEntry = components["schemas"]["SimpleScheduleEntry"];

// ClimateSchedule uses the generated Schedule shape — additive fields
// (active_profile_index) are safe to ignore in existing SPA callers.
export type ClimateSchedule = components["schemas"]["Schedule"];

// BackupEntry: not in generated schema (no backup schema defined in openapi.yaml components).
// Keep hand-written.
export type BackupEntry = {
  id: string;
  central: string;
  bytes: number;
  created_at: string;
};

// SysvarEntry re-exported from the generated SysvarSummary. central is
// optional (Go json:"central,omitempty"); SysvarList.svelte builds its
// composite key as (sv.central ?? "") + "/" + sv.name.
export type SysvarEntry = components["schemas"]["SysvarSummary"];

// ProgramEntry re-exported from the generated ProgramSummary. central is
// optional (Go json:"central,omitempty"); ProgramList.svelte builds its
// composite key as (p.central ?? "") + "/" + p.id. Generated adds extra
// fields (unique_id, last_executed, ...) that SPA callers ignore.
export type ProgramEntry = components["schemas"]["ProgramSummary"];

export type AuditChange = {
  parameter: string;
  before?: unknown;
  after?: unknown;
};

// AuditEntry re-exported from generated schema — openapi.yaml now carries
// the central field (optional, derived best-effort from the device address).
// action is a plain string; AuditLog.svelte's actionLabel() resolves known
// tags via a lookup set, so no string union is needed.
export type AuditEntry = components["schemas"]["AuditEntry"];

// AlarmMessage re-exported from generated schema — openapi.yaml now carries
// the central field (optional, Go json:"central,omitempty"). MessageList.svelte
// builds its composite key as (a.central ?? "") + "/" + a.id.
export type AlarmMessage = components["schemas"]["AlarmMessage"];

// ServiceMessage re-exported from generated schema — openapi.yaml now carries
// the central field (optional, Go json:"central,omitempty") and marks quittable
// required (the Go DTO always emits it). MessageList.svelte builds its composite
// key as (s.central ?? "") + "/" + s.id.
export type ServiceMessage = components["schemas"]["ServiceMessage"];

// InterfaceInfo re-exported from generated InterfaceState — same shape.
export type InterfaceInfo = components["schemas"]["InterfaceState"];

// HealthComponent derived from generated Health schema (inline component type).
// Generated makes recorded_at required; SPA only reads optional fields.
export type HealthComponent = {
  name: string;
  status: "healthy" | "degraded" | "unhealthy" | string;
  note?: string;
  recorded_at?: string;
};

// HealthSnapshot re-exported from generated Health schema.
// Generated uses a stricter status enum but is a superset — safe.
export type HealthSnapshot = components["schemas"]["Health"];

// Incident: not in generated schema (no /incidents endpoint schema defined in openapi.yaml).
// Keep hand-written.
export type Incident = {
  id: string;
  when: string;
  component: string;
  severity: "info" | "warn" | "error" | string;
  summary: string;
  detail?: string;
};

// --- Diagnostics --------------------------------------------------

export type LogLevelEntry = {
  path: string;
  level: "debug" | "info" | "warn" | "error" | string;
  permanent: boolean;
  expires_at?: string;
  remaining_ms?: number;
};

export type LogLevelsResponse = {
  default: "debug" | "info" | "warn" | "error" | string;
  overrides: LogLevelEntry[];
};

export type CaptureSummary = {
  id: string;
  status: "running" | "stopped" | "expired" | "aborted" | string;
  started_at: string;
  ends_at: string;
  stopped_at?: string;
  anonymised: boolean;
  events: number;
  buffer_bytes: number;
  archive_size?: number;
  triggered_by?: string;
};

export type DiagnosticsClient = {
  name: string;
  score: number;
  status: string;
  last_successful_request?: string;
  last_failed_request?: string;
  last_event_received?: string;
  consecutive_failures: number;
  reconnect_attempts: number;
  in_recovery: boolean;
};

export type DiagnosticsHealth = {
  status: string;
  score: number;
  available: boolean;
  degraded: boolean;
  failed: boolean;
  components: HealthComponent[];
  clients?: DiagnosticsClient[];
  central_scores?: Record<string, number>;
  gauges?: Record<string, number>;
  primary_client_healthy: boolean;
};

export type DiagnosticsEnvelope = {
  schema_version: string;
  generated_at: string;
  anonymized: boolean;
  build: {
    version?: string;
    commit?: string;
    go_version: string;
    build_time?: string;
  };
  health: DiagnosticsHealth;
  interfaces?: InterfaceInfo[];
  incidents?: Incident[];
  system_status?: unknown[];
  log_levels?: LogLevelsResponse;
};

export type CentralLinksStatus = {
  supported: boolean;
  reason?: string;
  eligible_channels?: number;
};

export type CentralLinksReport = {
  touched: number;
  skipped: number;
  failed: number;
};

// RoomEntry/FunctionEntry: not in generated schema components. Keep hand-written.
export type RoomEntry = {
  name: string;
  device_count: number;
};

export type FunctionEntry = {
  name: string;
  device_count: number;
};

// UserListEntry re-exported from generated schema — shapes match.
export type UserListEntry = components["schemas"]["UserListEntry"];

// TokenListEntry re-exported from generated schema — shapes match.
// Generated adds an `id` field; SPA callers use only fingerprint/subject/role.
// Additive extra field is safe to ignore.
export type TokenListEntry = components["schemas"]["TokenListEntry"];

// EditSessionResponse: not in generated schema components. Keep hand-written.
export type EditSessionResponse = {
  token: string;
  key: string;
  subject?: string;
  expires: string;
};

// InboxDevice: not in generated schema components. Keep hand-written.
export type InboxDevice = {
  central: string;
  address: string;
  model: string;
  serial?: string;
  manufacturer?: string;
  first_seen?: number;
};

// ConfigSnapshot re-exported from generated schema.
// Generated adds: extras, policies fields — additive, safe.
export type ConfigSnapshot = components["schemas"]["ConfigSnapshot"];

// LinkableChannel: not in generated schema components. Keep hand-written.
export type LinkableChannel = {
  address: string;
  channel_type?: string;
  channel_type_label?: string;
  channel_name?: string;
  device_address: string;
  device_name?: string;
  device_model?: string;
};

// Per-device RF reception strength from `GET /diagnostics/rssi`, read from
// the maintenance-channel RSSI_DEVICE / RSSI_PEER data points (works for HmIP
// and BidCos). rssi_device / rssi_peer are null when the device does not
// report that reading. Not in the generated schema.
export type RSSIDevice = {
  address: string;
  name: string;
  interface_id: string;
  central: string;
  rssi_device: number | null;
  rssi_peer: number | null;
  battery_level: number | null;
  low_battery: boolean | null;
  reachable: boolean;
};

export type RSSIMatrix = {
  devices: RSSIDevice[];
};

// RpcRecordingStatus: not in generated schema components. Keep hand-written.
export type RpcRecordingStatus = {
  central: string;
  active: boolean;
  entries: number;
  ends_at?: string;
  randomize?: boolean;
};

// InstallModeInterfaceEntry: one radio's install-mode state from
// `GET /install-mode/interfaces`. Not in the generated schema yet.
export type InstallModeInterfaceEntry = {
  central?: string;
  interface: string;
  active: boolean;
  seconds: number;
  observed: boolean;
};

// LogRecord: not in generated schema components. Keep hand-written.
export type LogRecord = {
  seq: number;
  time: string;
  level: "debug" | "info" | "warn" | "error" | string;
  logger?: string;
  msg: string;
  attrs?: Record<string, unknown>;
};
