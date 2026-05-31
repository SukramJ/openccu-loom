// Hand-written mirror of the REST DTOs. Will be replaced by
// openapi-typescript once the spec stabilises; keeping it manual
// lets us iterate the SPA without waiting for the generator pipeline.

export type DeviceSummary = {
  address: string;
  central?: string;
  interface: string;
  interface_id: string;
  model: string;
  model_label?: string;
  model_icon?: string;
  sub_model?: string;
  name: string;
  manufacturer?: string;
  product_group?: string;
  available: boolean;
  channels_count: number;
  /** Device *supports* firmware updates (CCU UPDATABLE capability) — NOT
   *  whether one is pending. Use update_available for the "update available"
   *  indicator. */
  updatable: boolean;
  /** An installable firmware update is actually pending: the gated latest
   *  version differs from the installed one (image already delivered for
   *  HmIP-RF / available for BidCos). A newer firmware the CCU merely knows
   *  about but has not delivered does NOT set this. */
  update_available: boolean;
  rooms?: string[];
  functions?: string[];
  /** True when the device's interface delivers reliable CONFIG_PENDING
   *  events on MASTER writes (HmIP-RF, HmIP-Wired). The SPA then waits
   *  for the true→false transition before refreshing MASTER. False for
   *  BidCos-*, VirtualDevices, CUxD — those rely on the save-path
   *  reload because CONFIG_PENDING never fires (or fires unreliably,
   *  per aiohomematic interface_client.py:964-971). */
  master_pushes_config_pending: boolean;
  /** True when the device should be split into multiple logical
   *  sub-devices for northbound presentation. The SPA's CdpTilesPanel
   *  uses this flag to switch from a flat tile grid to per-group
   *  sections. */
  has_sub_devices?: boolean;
};

export type ChannelSummary = {
  address: string;
  number: number;
  type?: string;
  /** Localised channel-type label resolved through the OCCU
   *  `channel_types_<locale>` table (e.g. "Energiemesser" for
   *  `ENERGIE_METER_TRANSMITTER`). Empty when no translation exists. */
  type_label?: string;
  name?: string;
  paramset_key: string;
  data_points_count: number;
  /** Stable name of the Custom-DP this channel is attached to —
   *  empty when the channel is not owned by a CDP. The CDP-first
   *  Übersicht view uses this to hide channels that are already
   *  represented by a CDP tile (ADR 0016). */
  custom_dp_name?: string;
  /** Channel-group number the channel belongs to. Zero / missing
   *  means the channel is not part of any group. */
  group_no?: number;
  /** True when the channel itself is the master of its channel group
   *  (`group_no == number`). */
  is_group_master?: boolean;
  /** True when the channel sits in a channel group that carries more
   *  than one member — i.e. it participates in a sub-device split.
   *  Singleton groups stay `false`. */
  is_in_multi_group?: boolean;
  /** Resolved sub-device label per the channel's group master. Empty
   *  when no sub-device split applies. */
  sub_device_name?: string;
};

export type CustomDPSummary = {
  name: string;
  category: string;
  channel_no: number;
  supported_operations: string[];
  /** Stable widget hint — drives CDP-aware widget selection in the
   *  Übersicht view (ADR 0016). Empty when the kind classifier does
   *  not recognise the Custom-DP type. */
  kind?: string;
  /** CCU channels this Custom-DP composes. Currently always
   *  `[channel_no]`; reserved for future channel-group expansion. */
  channels?: number[];
  /** Optional feature flags (`dimmable`, `color`, `color_temp`,
   *  `tilt`, `boost`, `away`, …). */
  capabilities?: Record<string, boolean>;
  /** Static configuration block from the Custom-DP — temperature
   *  bounds, available HVAC modes, preset / week-program slots,
   *  etc. Mirrors the Go side's `ConfigPayload()`. Empty for
   *  Custom-DPs that don't implement it. */
  config?: Record<string, unknown>;
};

/** Wire-side lifecycle token for a data point. See ADR 0018. */
export type DataPointSource = "unobserved" | "cache" | "live" | "stale";

export type DataPointSummary = {
  parameter: string;
  parameter_label?: string;
  value: unknown;
  observed: boolean;
  modified_at?: string;
  /** Wire-side lifecycle token. Tells UI consumers whether the value
   *  is fresh (`live`), restored from disk awaiting the first live
   *  push (`cache`), known but the connection just dropped (`stale`),
   *  or never seen (`unobserved`). */
  source?: DataPointSource;
  /** When the data point was last observed via any push event. Cyclic
   *  info telegrams that repeat the previous value still bump this. */
  last_seen_at?: string;
  /** When the value actually changed last. Cyclic info telegrams do
   *  NOT bump this; equivalent to the legacy `modified_at`. */
  last_changed_at?: string;
  /** Pre-computed seconds between `last_seen_at` and the response
   *  time, so the browser does not need to parse the timestamp on
   *  every render. */
  value_age_seconds?: number;
  /** OPERATIONS bitmask split into the three logical axes the CCU
   *  exposes per parameter. The QuickControl tab uses `write` to drop
   *  sensor-only channels (e.g. SWITCH_TRANSMITTER) from the actor
   *  list — heuristics on channel type alone misclassify those. */
  operations: { read: boolean; write: boolean; event: boolean };
  /** CCU paramset descriptor's CONTROL attribute of the form
   *  `WIDGET_FAMILY.SLOT`. Drives the CONTROL-aware widget resolver
   *  in lib/control/. Empty when the descriptor carries no CONTROL. */
  control?: string;
  /** CCU descriptor TYPE (BOOL, INTEGER, FLOAT, ENUM, ...). Lets the
   *  SPA pick the right widget primitive without re-reading the
   *  paramset descriptor. */
  type?: string;
  /** Ordered enum labels for ENUM-typed parameters. Empty for
   *  non-ENUM. Drives picker widgets (colour palette, effect select). */
  value_list?: string[];
  /** Parameter descriptor's UNIT ("°C", "%", "mA", "Hz", "Wh", ...).
   *  Empty when the CCU carries no unit. */
  unit?: string;
  /** Descriptor numeric bounds + preset, passed through from the
   *  wire. The AutoTile composer reads them to pick slider /
   *  stepper / free-input primitives for writable numeric DPs. */
  min?: unknown;
  max?: unknown;
  default?: unknown;
  /** Daemon-computed UI classification envelope. See
   *  docs/ui/auto-tile-concept.md. The AutoTile composer reads the
   *  fields verbatim; no client-side re-classification runs. */
  ui_hint?: UIHint;
};

/** Per-DP UI classification the daemon computes via
 *  `pkg/hmui.HintFor`. Additive, backwards-compatible. */
export type UIHint = {
  icon: string;
  semantic: string;
  state_color_rule?: string;
};

export type DeviceDetail = DeviceSummary & {
  firmware: {
    /** Current firmware version installed on the device. */
    Current?: string;
    /** Latest available firmware version reported by the CCU. */
    Available?: string;
    /** True when an update is available and the device can be updated. */
    Updatable: boolean;
    /**
     * Update lifecycle state as reported by the CCU.
     * Known values: UNKNOWN, UP_TO_DATE, LIVE_UP_TO_DATE,
     * NEW_FIRMWARE_AVAILABLE, LIVE_NEW_FIRMWARE_AVAILABLE,
     * DELIVER_FIRMWARE_IMAGE, LIVE_DELIVER_FIRMWARE_IMAGE,
     * READY_FOR_UPDATE, DO_UPDATE_PENDING, PERFORMING_UPDATE,
     * BACKGROUND_UPDATE_NOT_SUPPORTED.
     */
    UpdateState?: string;
  };
  availability: {
    reachable: boolean;
    reason?: string;
  };
  channels: ChannelSummary[];
};

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

export type Link = {
  sender_address: string;
  receiver_address: string;
  name?: string;
  description?: string;
  flags?: number;
  sender_device_name?: string;
  sender_device_model?: string;
  sender_channel_type?: string;
  sender_channel_type_label?: string;
  sender_channel_name?: string;
  receiver_device_name?: string;
  receiver_device_model?: string;
  receiver_channel_type?: string;
  receiver_channel_type_label?: string;
  receiver_channel_name?: string;
  peer_address: string;
  peer_device_name?: string;
  peer_device_model?: string;
  direction: "outgoing" | "incoming";
};

// --- Schedules ----------------------------------------------------

export type ClimatePeriod = {
  start_time: string;
  end_time: string;
  temperature: number;
};

export type ClimateWeekday = {
  base_temperature: number;
  periods: ClimatePeriod[];
};

export type ClimateProfile = {
  weekdays: Record<string, ClimateWeekday>;
};

export type SimpleScheduleEntry = {
  slot_no: number;
  weekdays: string[];
  time: string;
  condition?:
    | "fixed_time"
    | "astro"
    | "fixed_if_before_astro"
    | "astro_if_before_fixed"
    | "fixed_if_after_astro"
    | "astro_if_after_fixed"
    | "earliest_of_fixed_and_astro"
    | "latest_of_fixed_and_astro"
    | string;
  astro_type?: "sunrise" | "sunset" | string;
  astro_offset_minutes?: number;
  target_channels?: string[];
  level: number;
  level_2?: number;
  duration?: string;
  ramp_time?: string;
  // Lock-only fields; ignored when domain != "lock".
  lock_mode?: "door_lock" | "user_permission" | string;
  lock_action?:
    | "lock_autorelock_end"
    | "lock_autorelock_start"
    | "unlock_autorelock_end"
    | "autorelock_end"
    | string;
  permission?: "granted" | "not_granted" | string;
};

// Unified schedule envelope — `kind` selects the populated branch.
// "climate" → profiles (P1..P6), used by thermostats.
// "simple"  → simple_entries, used by switches/covers/lights.
export type ClimateSchedule = {
  channel: {
    address: string;
    number: number;
    device_address: string;
  };
  kind: "climate" | "simple" | string;
  // For "simple" schedules: which kind of actor is being scheduled.
  // Drives which widgets the SPA shows ("switch" → on/off toggle,
  // "light" → slider+ramp, "cover" → slider+slat, "lock" → action,
  // "valve" → slider). Empty / "" → generic fallback editor.
  domain?: "switch" | "light" | "cover" | "lock" | "valve" | "climate" | string;
  active_profile?: string;
  profiles?: Record<string, ClimateProfile>;
  simple_entries?: SimpleScheduleEntry[];
};

export type BackupEntry = {
  id: string;
  central: string;
  bytes: number;
  created_at: string;
};

export type SysvarEntry = {
  central: string;
  name: string;
  description?: string;
  unit?: string;
  value_type: "BOOL" | "INTEGER" | "FLOAT" | "STRING" | "ENUM" | string;
  value?: unknown;
  observed: boolean;
  value_list?: string[];
};

export type ProgramEntry = {
  central: string;
  id: string;
  name: string;
  description?: string;
  active?: boolean;
};

export type AuditChange = {
  parameter: string;
  before?: unknown;
  after?: unknown;
};

export type AuditEntry = {
  central?: string;
  timestamp: string;
  user?: string;
  action:
    | "paramset_write"
    | "link_paramset_write"
    | "link_add"
    | "link_remove"
    | "schedule_write"
    | "active_profile"
    | "data_point_write"
    | string;
  device_address?: string;
  channel_no?: number;
  paramset?: string;
  peer?: string;
  parameter?: string;
  changes?: AuditChange[];
  note?: string;
};

export type AlarmMessage = {
  central: string;
  id: string;
  name: string;
  description?: string;
  device_name?: string;
  timestamp: string;
  counter: number;
  last_trigger?: string;
  rooms?: string[];
};

export type ServiceMessage = {
  central: string;
  id: string;
  name: string;
  address?: string;
  device_name?: string;
  type?: string;
  timestamp: string;
  counter: number;
  quittable: boolean;
};

export type InterfaceInfo = {
  id: string;
  name: string;
  connected: boolean;
  interface: string;
  central_id?: string;
  host?: string;
  note?: string;
};

export type HealthComponent = {
  name: string;
  status: "healthy" | "degraded" | "unhealthy" | string;
  note?: string;
  recorded_at?: string;
};

export type HealthSnapshot = {
  status: "healthy" | "degraded" | "unhealthy" | string;
  components: HealthComponent[];
};

export type Incident = {
  id: string;
  when: string;
  component: string;
  severity: "info" | "warn" | "error" | string;
  summary: string;
  detail?: string;
};

// --- Diagnostics (Wave 3) -----------------------------------------

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

export type RoomEntry = {
  name: string;
  device_count: number;
};

export type FunctionEntry = {
  name: string;
  device_count: number;
};

export type UserListEntry = {
  username: string;
  role: "admin" | "operator" | "viewer" | string;
};

export type TokenListEntry = {
  fingerprint: string;
  subject: string;
  role: "admin" | "operator" | "viewer" | string;
};

export type EditSessionResponse = {
  token: string;
  key: string;
  subject?: string;
  expires: string;
};

export type InboxDevice = {
  central: string;
  address: string;
  model: string;
  serial?: string;
  manufacturer?: string;
  first_seen?: number;
};

export type ConfigSnapshot = {
  locale?: string;
  centrals?: { name: string; host: string; interfaces: string[] }[];
  callback_ports?: { xmlrpc?: number; binrpc?: number };
  features?: Record<string, boolean>;
  [k: string]: unknown;
};

export type LinkableChannel = {
  address: string;
  channel_type?: string;
  channel_type_label?: string;
  channel_name?: string;
  device_address: string;
  device_name?: string;
  device_model?: string;
};

export type RpcRecordingStatus = {
  central: string;
  active: boolean;
  entries: number;
  ends_at?: string;
  randomize?: boolean;
};

export type LogRecord = {
  seq: number;
  time: string;
  level: "debug" | "info" | "warn" | "error" | string;
  logger?: string;
  msg: string;
  attrs?: Record<string, unknown>;
};
