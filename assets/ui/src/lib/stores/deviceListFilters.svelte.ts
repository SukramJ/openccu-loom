// Device-list filter/sort state. Held at module scope so it survives the
// route component unmounting/remounting, and persisted to localStorage so
// the operator's search term, filters, grouping, and (card-mode) sort also
// survive a full page reload. (The view mode — cards vs table — is the
// durable preference in preferences.svelte; the table-mode column sort is
// persisted by the DataTable itself under its own key.)

export type DeviceAvailability = "all" | "available" | "unavailable";
export type DeviceSortColumn = "name" | "address" | "model";

type DeviceListFilters = {
  filter: string;
  availability: DeviceAvailability;
  updateOnly: boolean;
  roomFilter: string;
  centralFilter: string;
  sortColumn: DeviceSortColumn;
  sortAsc: boolean;
  groupByInterface: boolean;
};

const STORAGE_KEY = "openccu-loom.device-list-filters.v1";

const defaults: DeviceListFilters = {
  filter: "",
  availability: "all",
  updateOnly: false,
  roomFilter: "",
  centralFilter: "",
  sortColumn: "name",
  sortAsc: true,
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
