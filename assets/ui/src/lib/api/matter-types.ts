// Matter bridge REST DTOs. Hand-written mirror of the openapi.yaml shapes.

export type MatterStatus = {
  enabled: boolean;
  listening: boolean;
  endpoint_count: number;
  fabric_count: number;
  enabled_count: number;
  advertising: boolean;
  commissioning_window_open: boolean;
  commissioning_window_duration_seconds: number;
};

export type MatterFabric = {
  fabric_index: number;
  /**
   * 64-bit fabric / operational node identifiers. Unlike the session DTO,
   * which hex-encodes its ids into strings, the fabric list serves them as
   * JSON numbers — render them with an explicit `toString(16)` rather than
   * gluing a `0x` in front of the decimal value.
   */
  fabric_id: number;
  node_id: number;
  vendor_id: number;
  label: string;
  compressed_id: string;
  root_public_key: string;
};

export type MatterFabricsResponse = {
  fabrics: MatterFabric[];
};

/** One open secure session, from GET /matter/sessions. */
export type MatterSession = {
  session_id: number;
  fabric_index: number;
  peer_node_id: string;
  local_node_id: string;
  is_pase: boolean;
  subscriptions: number;
  last_activity: string;
  last_peer_activity: string;
  idle_seconds: number;
  /** Seconds since the controller last sent anything — the liveness signal. */
  peer_idle_seconds: number;
};

/**
 * Usage of the bridge's 16-bit session-id space.
 *
 * `reserved` is the count the session list cannot show: a CASE handshake
 * announces its session id one round trip before the session exists, so an
 * id staked by a handshake that never completes holds its slot without ever
 * appearing as a session.
 */
export type MatterSessionOccupancy = {
  live: number;
  reserved: number;
  capacity: number;
  free: number;
};

export type MatterSessionsResponse = {
  sessions: MatterSession[];
  occupancy: MatterSessionOccupancy;
};

export type MatterMdnsService = {
  service_type: string;
  instance_name: string;
  host_name: string;
  port: number;
  addresses: string[];
  subtypes: string[];
  txt: Record<string, string>;
};

export type MatterMdnsFinding = {
  severity: "error" | "warning";
  code: string;
  message: string;
  service?: string;
};

export type MatterMdnsDiagnostics = {
  advertising: boolean;
  services: MatterMdnsService[];
  findings: MatterMdnsFinding[];
};

export type MatterEndpointCluster = {
  id: number;
  name: string;
  revision: number;
};

export type MatterEndpointInfo = {
  endpoint_id: number;
  parent_endpoint_id: number;
  device_type: number;
  device_type_name: string;
  device_type_revision?: number;
  reachable: boolean;
  friendly_name: string;
  device_address?: string;
  channel_address?: string;
  clusters: MatterEndpointCluster[];
};

export type MatterEndpointsResponse = {
  endpoints: MatterEndpointInfo[];
};

export type MatterEcosystem = {
  ecosystem: string;
  vendor_id: number;
  fabric_index: number;
  label?: string;
};

export type MatterCompatFinding = {
  ecosystem: string;
  code: string;
  message: string;
  device_type?: number;
};

export type MatterCompatibility = {
  ecosystems: MatterEcosystem[];
  endpoint_count: number;
  findings: MatterCompatFinding[];
};

/** Mapping eligibility state from the cluster mapper. */
export type MatterMappability = "mappable" | "partially_mappable" | "unmappable";

export type MatterExposure = {
  central_name: string;
  device_address: string;
  channel_no: number;
  dp_kind: "custom" | "generic" | "calculated" | "combined" | string;
  dp_key: string;
  /** Localised parameter label (channel-typed or bare-parameter lookup
      via the OCCU translation catalogue). Empty when the catalogue has
      no entry — UI falls back to `dp_key`. */
  parameter_label: string;
  display_name: string;
  enabled: boolean;
  friendly_name: string;
  mappable: MatterMappability;
  /** Numeric Matter Device Type ID (e.g. 0x0301 for Thermostat). 0 when the
      DP rides on a host endpoint (Power/Energy/Battery measurements). */
  device_type: number;
  /** Operator-facing label for `device_type`. Backend-provided so the SPA
      does not maintain a parallel ID → label map. Empty when device_type=0. */
  device_type_label: string;
  /** Numeric Matter Cluster IDs the DP contributes. */
  clusters: number[];
  reason: string;
};

export type MatterExposableResponse = {
  items: MatterExposure[];
};

export type MatterExposureUpdate = {
  central_name: string;
  device_address: string;
  channel_no: number;
  dp_kind: string;
  dp_key: string;
  enabled: boolean;
  friendly_name?: string;
};

export type MatterBulkUpdateRequest = {
  items: MatterExposureUpdate[];
};

export type MatterBulkUpdateResponse = {
  applied: number;
};

export type MatterSetupPayload = {
  discriminator: number;
  passcode: number;
  vendor_id: number;
  product_id: number;
  qr_code: string;
  manual_code: string;
};

export type MatterCommissioningWindow = {
  discriminator: number;
  passcode: number;
  duration_seconds: number;
  qr_code: string;
  manual_code: string;
};
