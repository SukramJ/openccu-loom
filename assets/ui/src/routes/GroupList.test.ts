// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockGetGroups = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    getGroups: (...args: unknown[]) => mockGetGroups(...args),
  },
  ApiError: class ApiError extends Error {
    constructor(
      public readonly status: number,
      public readonly body: unknown,
      message: string,
    ) {
      super(message);
    }
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string | number>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import GroupList from "./GroupList.svelte";

// ---------------------------------------------------------------------------
// Test data — one central with two heating groups, one flagged
// forbid_single_operation and carrying two members, the other empty.
// ---------------------------------------------------------------------------

const ENTRIES = [
  {
    central: "ccu1",
    groups: [
      {
        id: 1,
        name: "Wohnzimmer",
        group_device_name: "HEATING_CLIMATECONTROL_TRANSCEIVER 1",
        forbid_single_operation: true,
        type_id: "HEATING_CLIMATECONTROL_TRANSCEIVER",
        type_label: "Heating group",
        members: [
          { address: "ABC1234567:1", type_id: "HEATING_CLIMATECONTROL_TRANSCEIVER" },
          { address: "ABC1234568:1", type_id: "HEATING_CLIMATECONTROL_TRANSCEIVER" },
        ],
      },
      {
        id: 2,
        name: "Empty group",
        forbid_single_operation: false,
        type_id: "HEATING_CLIMATECONTROL_TRANSCEIVER",
        members: [],
      },
    ],
  },
];

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => cleanup());

describe("GroupList — rendering", () => {
  it("renders each group with its name, id, type label and member count", async () => {
    mockGetGroups.mockResolvedValue(ENTRIES);

    render(GroupList);

    await waitFor(() => {
      expect(screen.getByText("Wohnzimmer")).toBeInTheDocument();
    });

    expect(mockGetGroups).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Heating group")).toBeInTheDocument();
    // First "groups.field.id" label pairs with the first group's dd sibling.
    const idLabels = screen.getAllByText("groups.field.id");
    expect(idLabels[0].nextElementSibling?.textContent?.trim()).toBe("1");
    expect(screen.getByText(/ABC1234567:1/)).toBeInTheDocument();
    expect(screen.getByText(/ABC1234568:1/)).toBeInTheDocument();
  });

  it("renders resolved member device names and falls back to the address otherwise", async () => {
    mockGetGroups.mockResolvedValue([
      {
        central: "ccu1",
        groups: [
          {
            id: 3,
            name: "Bad",
            forbid_single_operation: false,
            type_id: "HEATING_CLIMATECONTROL_TRANSCEIVER",
            type_label: "Heating group",
            members: [
              {
                address: "000C9709AEF269:1",
                type_id: "THERMOSTAT",
                device_name: "Wandthermostat DB",
                channel_name: "Heizen",
                rooms: ["Duschbad"],
              },
              // No device_name -> the client falls back to the raw address.
              { address: "00109709B13456:1", type_id: "SENSOR_WINDOW" },
            ],
          },
        ],
      },
    ]);

    render(GroupList);

    await waitFor(() => {
      expect(screen.getByText("Wandthermostat DB")).toBeInTheDocument();
    });
    expect(screen.getByText(/Heizen/)).toBeInTheDocument();
    expect(screen.getByText(/Duschbad/)).toBeInTheDocument();
    // The resolved member shows its name, never the raw address.
    expect(screen.queryByText(/000C9709AEF269/)).not.toBeInTheDocument();
    // The unresolved member still falls back to its address.
    expect(screen.getByText(/00109709B13456:1/)).toBeInTheDocument();
  });

  it("falls back to type_id when type_label is empty", async () => {
    mockGetGroups.mockResolvedValue(ENTRIES);

    render(GroupList);

    await waitFor(() => {
      expect(screen.getByText("Empty group")).toBeInTheDocument();
    });
    // The second group has no type_label, so its raw type_id is shown.
    expect(screen.getAllByText("HEATING_CLIMATECONTROL_TRANSCEIVER").length).toBeGreaterThan(0);
  });

  it("shows the group-only-operation badge only for the flagged group", async () => {
    mockGetGroups.mockResolvedValue(ENTRIES);

    render(GroupList);

    await waitFor(() => {
      expect(screen.getByText("Wohnzimmer")).toBeInTheDocument();
    });

    const badges = screen.getAllByText("groups.operate_only_via_group");
    expect(badges).toHaveLength(1);
  });

  it("shows the no-members hint for a group with an empty roster", async () => {
    mockGetGroups.mockResolvedValue(ENTRIES);

    render(GroupList);

    await waitFor(() => {
      expect(screen.getByText("Empty group")).toBeInTheDocument();
    });
    expect(screen.getByText("groups.members.empty")).toBeInTheDocument();
  });
});

describe("GroupList — empty state", () => {
  it("shows the empty-state message when no groups exist on any central", async () => {
    mockGetGroups.mockResolvedValue([{ central: "ccu1", groups: [] }]);

    render(GroupList);

    await waitFor(() => {
      expect(screen.getByText("groups.empty")).toBeInTheDocument();
    });
  });
});

describe("GroupList — error state", () => {
  it("shows the error state with a retry action when the load fails", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockGetGroups.mockRejectedValue(new ApiError(502, null, "ccu unreachable"));

    render(GroupList);

    // ErrorState renders "common.error" and the message as one text node
    // ("{t("common.error")} {message}"), so match the substring rather
    // than the exact node text.
    await waitFor(() => {
      expect(screen.getByText(/ccu unreachable/)).toBeInTheDocument();
    });
  });
});
