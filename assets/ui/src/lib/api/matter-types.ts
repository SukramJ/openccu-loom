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
  fabric_id: string;
  node_id: string;
  vendor_id: number;
  label: string;
  compressed_id: string;
  root_public_key: string;
};

export type MatterFabricsResponse = {
  fabrics: MatterFabric[];
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
