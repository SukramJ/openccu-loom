// Shared tab descriptor for the Tabs design-system component. Kept in a plain
// .ts module because a Svelte component cannot export a type for use in markup
// — the same reason `data-table.ts` sits beside `DataTable.svelte`.

import type { IconName } from "$lib/icons";

export type TabItem = {
  // Stable key — compared against the strip's `active` to mark the tab.
  key: string;
  // Localized label.
  label: string;
  // Deep link for route-driven tabs. Without it the tab renders as a button
  // and reports through the strip's `onSelect`.
  href?: string;
  icon?: IconName;
  // Trailing count, as the message and keypress strips carry: the number of
  // rows behind the tab, so an empty section is visible before opening it.
  badge?: string | number;
};
