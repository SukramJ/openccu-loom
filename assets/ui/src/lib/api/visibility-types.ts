// Visibility / un_ignore REST DTOs. Hand-written mirror of the
// openapi.yaml shapes for the /visibility/unignore* endpoints.
// See notes/concepts/ui/unignore-concept.md.

export type UnIgnoreEntry = {
  pattern: string;
  updated_at?: string;
  updated_by?: string;
};

export type UnIgnoreCentralPatterns = {
  central_name: string;
  patterns: UnIgnoreEntry[];
};

export type UnIgnoreListResponse = {
  centrals: UnIgnoreCentralPatterns[];
};

export type UnIgnoreUpdateRequest = {
  central_name: string;
  patterns: string[];
};

export type UnIgnoreUpdateResponse = {
  applied_count: number;
  parse_errors?: string[];
  affected_devices: number;
  patterns: UnIgnoreEntry[];
};

/** The rule that suppressed a parameter. Mirrors
    visibility.HiddenReason (internal/store/visibility/reason.go); the
    server also ships the full vocabulary in `reasons` so an unknown
    value here is rendered rather than dropped. */
export type UnIgnoreReason =
  | "operation_mode"
  | "week_profile"
  | "master_gate"
  | "device_specific"
  | "ignore_list"
  | "wildcard_prefix"
  | "wildcard_suffix"
  | "hidden"
  | "channel_restricted"
  | "event_suppressed"
  | "internal_flag"
  | "read_only"
  | "unknown";

export type UnIgnoreCandidateChannel = {
  channel: number;
  pattern: string;
};

export type UnIgnoreCandidateModel = {
  model: string;
  wildcard_pattern?: string;
  channels: UnIgnoreCandidateChannel[];
  device_count: number;
};

export type UnIgnoreCandidateGroup = {
  parameter: string;
  label?: string;
  paramset: string;
  reason: UnIgnoreReason;
  reasons: UnIgnoreReason[];
  simple_pattern?: string;
  models: UnIgnoreCandidateModel[];
  device_count: number;
  channel_count: number;
};

export type UnIgnoreCandidateList = {
  candidates: string[];
  include_master: boolean;
  /** One entry per (parameter, paramset) — the picker's primary shape.
      Absent on responses from a daemon older than API 2.18.0. */
  groups?: UnIgnoreCandidateGroup[];
  /** Reason vocabulary the server can emit, in display order. */
  reasons?: UnIgnoreReason[];
};
