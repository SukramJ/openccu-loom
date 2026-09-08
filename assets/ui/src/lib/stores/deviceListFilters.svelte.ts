// Device-list filter state. Held at module scope so it survives the route
// component unmounting/remounting, and persisted to localStorage so the
// operator's search term, filters and grouping survive a full page reload.
// Sort is not here: the DataTable owns the column order and persists it
// under its own key, so a second copy would only be able to disagree.

export type DeviceAvailability = "all" | "available" | "unavailable";

type DeviceListFilters = {
  filter: string;
  availability: DeviceAvailability;
  updateOnly: boolean;
  roomFilter: string;
  centralFilter: string;
  /** Selected Area id (settings/RoomsFunctionsAdmin.svelte — an
   *  operator-defined grouping ABOVE CCU rooms). Empty = no filter. */
  areaFilter: string;
  groupByInterface: boolean;
};

const STORAGE_KEY = "openccu-loom.device-list-filters.v1";

const defaults: DeviceListFilters = {
  filter: "",
  availability: "all",
  updateOnly: false,
  roomFilter: "",
  centralFilter: "",
  areaFilter: "",
  groupByInterface: true,
};

function load(): DeviceListFilters {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) return { ...defaults, ...(JSON.parse(raw) as Partial<DeviceListFilters>) };
  } catch {
    // storage unavailable / malformed — fall back to defaults.
  }
  return { ...defaults };
}

export const deviceListFilters = $state<DeviceListFilters>(load());

// Persist the current filter/sort state. Call after mutating the store (the
// DeviceList view does so from its sync effect). Best-effort; ignores a
// disabled or full localStorage.
export function persistDeviceListFilters(): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(deviceListFilters));
  } catch {
    // storage unavailable — settings simply do not persist this session.
  }
}
