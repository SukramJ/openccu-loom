// Transient device-list filter/sort state, held at module scope so it
// survives the route component unmounting and remounting — i.e. the
// search term, filters and sort stay put when the operator opens a
// device and navigates back. Deliberately NOT persisted to
// localStorage: it should reset on a fresh page load, unlike the
// durable view-mode preference in preferences.svelte.ts.

export type DeviceAvailability = "all" | "available" | "unavailable";
export type DeviceSortColumn = "name" | "address" | "model";

export const deviceListFilters = $state({
  filter: "",
  availability: "all" as DeviceAvailability,
  updateOnly: false,
  roomFilter: "",
  centralFilter: "",
  sortColumn: "name" as DeviceSortColumn,
  sortAsc: true,
  groupByInterface: true,
});
