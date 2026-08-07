// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockListAudit = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listAudit: (...args: unknown[]) => mockListAudit(...args),
    auditDownloadUrl: () => "/api/v1/audit?format=csv",
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, _params?: unknown) => key,
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en" },
}));

// ---------------------------------------------------------------------------
// Component under test
// ---------------------------------------------------------------------------

import AuditLog from "./AuditLog.svelte";

// One operator action can emit several audit rows within the same second.
// Creating an alarm area writes area_create + sensors_replace +
// outputs_replace: same user, same action tag, no device address — every
// field except the note is identical. Rows are only distinguishable by
// their persisted id.
const SAME_SECOND_BURST = [
  {
    id: 3,
    timestamp: "2026-07-28T10:40:24Z",
    user: "markus",
    action: "alarm_config_change",
    note: "outputs_replace=835e80c3",
  },
  {
    id: 2,
    timestamp: "2026-07-28T10:40:24Z",
    user: "markus",
    action: "alarm_config_change",
    note: "sensors_replace=835e80c3",
  },
  {
    id: 1,
    timestamp: "2026-07-28T10:40:24Z",
    user: "markus",
    action: "alarm_config_change",
    note: "area_create=835e80c3",
  },
];

// Two paramset writes on the same channel in the same second: identical in
// every column the table renders, differing only in their changes.
const IDENTICAL_WITH_CHANGES = [
  {
    id: 11,
    timestamp: "2026-05-31T17:24:19Z",
    user: "markus",
    action: "paramset_write",
    device_address: "000A1709AF5344",
    channel_no: 1,
    paramset: "MASTER",
    changes: [{ parameter: "FIRST_ONE", before: 1, after: 2 }],
  },
  {
    id: 12,
    timestamp: "2026-05-31T17:24:19Z",
    user: "markus",
    action: "paramset_write",
    device_address: "000A1709AF5344",
    channel_no: 1,
    paramset: "MASTER",
    changes: [{ parameter: "SECOND_ONE", before: 3, after: 4 }],
  },
];

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockListAudit.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
});

// ---------------------------------------------------------------------------
// Row identity
// ---------------------------------------------------------------------------

describe("AuditLog row identity", () => {
  it("renders every entry of a same-second burst", async () => {
    mockListAudit.mockResolvedValue(SAME_SECOND_BURST);
    render(AuditLog);

    // A duplicate {#each} key aborts the render mid-way: the table never
    // appears and the view stays stuck on its loading state.
    await waitFor(() => {
      expect(screen.getByText("outputs_replace=835e80c3")).toBeInTheDocument();
    });
    expect(screen.getByText("sensors_replace=835e80c3")).toBeInTheDocument();
    expect(screen.getByText("area_create=835e80c3")).toBeInTheDocument();
    expect(document.querySelectorAll("tbody tr")).toHaveLength(3);
  });

  it("expands only the entry that was clicked when two rows are otherwise identical", async () => {
    mockListAudit.mockResolvedValue(IDENTICAL_WITH_CHANGES);
    render(AuditLog);

    await waitFor(() => {
      expect(document.querySelectorAll("tbody tr")).toHaveLength(2);
    });
    expect(screen.queryByText("FIRST_ONE")).not.toBeInTheDocument();
    expect(screen.queryByText("SECOND_ONE")).not.toBeInTheDocument();

    const firstToggle = document.querySelectorAll("tbody tr")[0].querySelector("button");
    expect(firstToggle).not.toBeNull();
    await fireEvent.click(firstToggle as HTMLElement);

    // Expansion state is keyed by row identity — sharing a key would open
    // both sub-tables at once.
    await waitFor(() => {
      expect(screen.getByText("FIRST_ONE")).toBeInTheDocument();
    });
    expect(screen.queryByText("SECOND_ONE")).not.toBeInTheDocument();
  });
});
