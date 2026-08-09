// Surface-profile REST DTOs. Hand-written mirror of the openapi.yaml
// shapes for /ui/surfaces. See notes/concepts/ui-surface-profiles.md.

/** Stored visibility of one surface. */
export type SurfaceState = "visible" | "hidden";

/** The two surface profiles. */
export type ProfileName = "standalone" | "embedded";

/** Editor bucket, mirroring the navigation clusters. */
export type SurfaceGroup =
  | "overview"
  | "automation"
  | "diagnose"
  | "bridges"
  | "system"
  | "settings"
  | "device";

/** Where a surface can never be hidden. */
export type SurfaceFloor = "always" | "standalone";

/** A runtime capability a surface additionally depends on. */
export type SurfaceGate = "matter" | "history";

/** A condition under which hiding asks for confirmation first. */
export type SurfaceWarn = "alarm_armed" | "security_faults" | "last_ccu_editor";

/** One addressable Config-UI surface. */
export type SurfaceInfo = {
  id: string;
  group: SurfaceGroup;
  /** Shipped visibility per profile name. */
  defaults: Record<string, boolean>;
  floor?: SurfaceFloor;
  gate?: SurfaceGate;
  warn?: SurfaceWarn;
  warn_profile?: ProfileName;
  parent?: string;
  /**
   * The editor this read-only overview hands off to. While that editor
   * is hidden the overview stays — it answers a fleet-wide question the
   * device detail cannot — but its rows stop linking into a tab that is
   * not there.
   */
  opens?: string;
  role_admin?: boolean;
  /**
   * The embedded default flips back to visible when the daemon serves
   * more than one CCU: a Home Assistant config entry addresses one CCU,
   * so HA cannot own the config surface of the ones it has no entry for.
   * `defaults` already reflects the current fleet; this only explains it.
   */
  multi_central_visible?: boolean;
  ha_owns?: boolean;
};

/** Where the embedded declaration applies. */
export type EmbeddedScope = "inside_ha" | "always";

/** `GET|PUT /api/v1/ui/surfaces`. */
export type SurfacesResponse = {
  embedded: boolean;
  /** Where the master toggle applies. `inside_ha` is the default. */
  embedded_scope: EmbeddedScope;
  /**
   * Whether THIS request reached the daemon through Home Assistant.
   * With the default scope it is what selects `profile`, so the same
   * configuration answers differently on the other door.
   */
  inside_ha: boolean;
  /** The profile served to this request. */
  profile: ProfileName;
  /** Stored, sparse overrides per profile name. */
  profiles: Partial<Record<ProfileName, Record<string, SurfaceState>>>;
  /**
   * Resolved visibility of the live profile. Capability and role gates
   * are NOT folded in — the client applies those.
   */
  effective: Record<string, boolean>;
  /** How many CCUs this daemon serves. Above one it moves two defaults. */
  centrals: number;
  surfaces: SurfaceInfo[];
};

/** `PUT /api/v1/ui/surfaces`. Every field is optional. */
export type SurfacesRequest = {
  embedded?: boolean;
  embedded_scope?: EmbeddedScope;
  profiles?: Partial<Record<ProfileName, Record<string, SurfaceState>>>;
};
