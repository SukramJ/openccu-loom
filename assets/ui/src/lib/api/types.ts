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
export type UIHint = NonNullable<
  components["schemas"]["DataPointSummary"]["ui_hint"]
>;

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

// BackupEntry re-exported from generated schema.
export type BackupEntry = components["schemas"]["BackupEntry"];

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

// Health snapshot + component derived from the generated Health schema.
// `note_key` is the i18n catalogue key for a static note (see
// HealthComponent.NoteKey in internal/north/rest/handlers/health.go);
// absent for interpolated notes, where `note` is rendered verbatim.
export type HealthSnapshot = components["schemas"]["Health"];
export type HealthComponent = HealthSnapshot["components"][number];

// Incident re-exported from generated schema.
export type Incident = components["schemas"]["Incident"];

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

// RoomEntry/FunctionEntry re-exported from generated schema.
export type RoomEntry = components["schemas"]["RoomEntry"];
export type FunctionEntry = components["schemas"]["FunctionEntry"];

// UserListEntry re-exported from generated schema — shapes match.
export type UserListEntry = components["schemas"]["UserListEntry"];

// TokenListEntry re-exported from generated schema — shapes match.
// Generated adds an `id` field; SPA callers use only fingerprint/subject/role.
// Additive extra field is safe to ignore.
export type TokenListEntry = components["schemas"]["TokenListEntry"];

// EditSessionResponse re-exported from generated schema.
export type EditSessionResponse = components["schemas"]["EditSessionResponse"];

// InboxDevice re-exported from generated schema. central is optional (Go json:"central,omitempty").
export type InboxDevice = components["schemas"]["InboxDevice"];

// ConfigSnapshot re-exported from generated schema.
// Generated adds: extras, policies fields — additive, safe.
export type ConfigSnapshot = components["schemas"]["ConfigSnapshot"];

// LinkableChannel re-exported from generated schema.
export type LinkableChannel = components["schemas"]["LinkableChannel"];

// SystemCCUEntry re-exported from generated schema — per-central fleet
// metadata (name/host/availability/model/version/config-URL/configured
// interfaces) served by GET /api/v1/system/ccu. Backs the read-only
// cross-CCU overview (Fleet.svelte).
export type SystemCCUEntry = components["schemas"]["SystemCCUEntry"];

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

// RpcRecordingStatus re-exported from generated RPCRecordingStatus schema.
export type RpcRecordingStatus = components["schemas"]["RPCRecordingStatus"];

// InstallModeInterfaceEntry re-exported from generated schema.
export type InstallModeInterfaceEntry =
  components["schemas"]["InstallModeInterfaceEntry"];

// LogRecord re-exported from generated schema.
export type LogRecord = components["schemas"]["LogRecord"];

// Energy aggregation types (GET /api/v1/energy), re-exported from the
// generated schema — see docs/plans/A2-timeseries-energy.md. Values are
// Wh on the wire; the SPA divides by 1000 to render kWh.
export type EnergyBucket = components["schemas"]["EnergyBucket"];
export type EnergyDevice = components["schemas"]["EnergyDevice"];
export type EnergyResponse = components["schemas"]["EnergyResponse"];

// --- Alarm panel --------------------------------------------------
// The native intrusion-alarm engine (docs/alarm-concept.md). Distinct
// from the CCU alarm-messages surface above (AlarmMessage) — these are
// the alarm-panel schemas served under /api/v1/alarm/*. All re-exported
// from the generated contract so the SPA tracks the spec automatically.

// One armable area (partition) — config-level identity + ordering.
export type AlarmArea = components["schemas"]["AlarmArea"];
// Free-form engine-owned per-area configuration document.
export type AlarmAreaConfig = components["schemas"]["AlarmAreaConfig"];
// One area's live status (state machine + incident + countdown +
// per-mode readiness), returned by GET /alarm/state.
export type AlarmAreaStatus = components["schemas"]["AlarmAreaStatus"];
// One enrolled sensor input (door/window/motion/tamper/hazard/panic).
export type AlarmSensor = components["schemas"]["AlarmSensor"];
// One enrolled output consequence (siren/light/chirp/notification/…).
export type AlarmOutput = components["schemas"]["AlarmOutput"];
// One channel that can back a device-backed output class, with the
// device's ENUM extras (tones/lights/soundfiles) for real-value pickers.
export type AlarmOutputCandidate =
  components["schemas"]["AlarmOutputCandidate"];
// One remote/wall-button key channel usable for a remote-key code
// binding (PRESS_SHORT / PRESS_LONG dispatch, e.g. HmIP-KRCA).
export type AlarmRemoteKeyCandidate =
  components["schemas"]["AlarmRemoteKeyCandidate"];
// Whether an area is ready to arm into one specific mode + blocker list.
export type AlarmModeReadiness = components["schemas"]["AlarmModeReadiness"];
// Arm request body (POST /alarm/areas/{id}/arm) and its accepted reply.
// AlarmArmRequest carries an optional `code` (docs/alarm-concept.md §11).
export type AlarmArmRequest = components["schemas"]["AlarmArmRequest"];
export type AlarmArmAccepted = components["schemas"]["AlarmArmAccepted"];
// Optional { code? } body of the code-carrying verbs (disarm / silence /
// acknowledge). An absent body acts without a code (S3/S6).
export type AlarmVerbRequest = components["schemas"]["AlarmVerbRequest"];
// One alarm code (GET /alarm/codes). Hash-free and PIN-free by contract
// — the cleartext is never serialized onto this surface (§11/§16).
export type AlarmCode = components["schemas"]["AlarmCode"];
// Create/update body for a code (POST/PUT /alarm/codes). `pin` is
// write-only; an empty pin on update keeps the stored hash.
export type AlarmCodeRequest = components["schemas"]["AlarmCodeRequest"];
// Per-code verb permissions (arm / disarm / silence).
export type AlarmCodePerms = components["schemas"]["AlarmCodePerms"];
// Code class union: pin | keypad_slot | remote_key.
export type AlarmCodeKind = AlarmCode["kind"];
// One append-only journal entry (GET /alarm/journal).
export type AlarmJournalEntry = components["schemas"]["AlarmJournalEntry"];
// Live walk-test session status (GET /alarm/areas/{id}/walktest).
export type AlarmWalkTestStatus = components["schemas"]["AlarmWalkTestStatus"];
// Output test-fire request body (POST /alarm/outputs/{id}/test).
export type AlarmOutputTestRequest =
  components["schemas"]["AlarmOutputTestRequest"];

// Convenience string unions extracted from the status schema — views
// switch on these to pick badges / colours / mode buttons.
export type AlarmState = AlarmAreaStatus["state"];
export type AlarmMode = NonNullable<AlarmAreaStatus["mode"]>;
export type AlarmSensorType = AlarmSensor["type"];
export type AlarmOutputClass = AlarmOutput["class"];
export type AlarmJournalClass = AlarmJournalEntry["class"];

// The seven `alarm.*` WS broadcast payloads (topic `alarm.panel`). The
// events pump passes these through untouched as { type, payload }; the
// alarm store narrows `payload` to the matching alias in applyEvent.
export type AlarmStateChangedPayload =
  components["schemas"]["AlarmStateChangedPayload"];
export type AlarmCountdownPayload =
  components["schemas"]["AlarmCountdownPayload"];
export type AlarmReadinessChangedPayload =
  components["schemas"]["AlarmReadinessChangedPayload"];
export type AlarmTriggeredPayload =
  components["schemas"]["AlarmTriggeredPayload"];
export type AlarmJournalAppendedPayload =
  components["schemas"]["AlarmJournalAppendedPayload"];
export type AlarmWalkTestProgressPayload =
  components["schemas"]["AlarmWalkTestProgressPayload"];
export type AlarmHealthChangedPayload =
  components["schemas"]["AlarmHealthChangedPayload"];
