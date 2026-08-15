// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  cleanup,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/svelte";

const mockGroupTypes = vi.fn();
const mockSuitable = vi.fn();
const mockCreate = vi.fn();
const mockUpdate = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    groupTypes: (...a: unknown[]) => mockGroupTypes(...a),
    groupSuitableMembers: (...a: unknown[]) => mockSuitable(...a),
    createGroup: (...a: unknown[]) => mockCreate(...a),
    updateGroup: (...a: unknown[]) => mockUpdate(...a),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...a: unknown[]) => mockToastSuccess(...a),
    error: (...a: unknown[]) => mockToastError(...a),
  },
}));

// Whole-module mock (not just $lib/api/client) so the real areas.svelte.ts
// never pulls in auth.svelte.ts's module-level setUnauthorizedHandler side
// effect, which this file's minimal api-client mock does not export.
vi.mock("$lib/stores/areas.svelte", () => ({
  areasStore: {
    areas: [] as { id: string; name: string }[],
    ensureLoaded: vi.fn(),
    areaIdOf: vi.fn(() => undefined),
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import GroupEditor from "./GroupEditor.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  mockGroupTypes.mockResolvedValue([{ id: "hmip.heating.group", label_key: "" }]);
  mockSuitable.mockResolvedValue({
    assignable: [
      { address: "AAA:1", type: "SWITCH_ACTUATOR" },
      { address: "BBB:1", type: "SENSOR_WINDOW" },
    ],
    leftover: [],
  });
  mockCreate.mockResolvedValue({ id: 9, name: "Bad", members: [] });
  mockUpdate.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("GroupEditor — create", () => {
  it("loads types + members and creates the group with the picked members", async () => {
    render(GroupEditor, {
      props: {
        central: "ccu-a",
        onClose: vi.fn(),
        onSaved: vi.fn(),
      },
    });

    // Members loaded for the sole HmIP type — grouped by device. Unenriched
    // candidates fall back to a single-channel device labelled by its address.
    await waitFor(() => expect(screen.getByLabelText("AAA")).toBeInTheDocument());
    expect(mockGroupTypes).toHaveBeenCalledWith("ccu-a");
    expect(mockSuitable).toHaveBeenCalledWith("hmip.heating.group", "ccu-a");

    // Name + one member: AAA is single-channel, so its tri-state device
    // checkbox selects the channel directly.
    const name = screen.getByLabelText("groups.editor.name") as HTMLInputElement;
    await fireEvent.input(name, { target: { value: "Bad" } });
    await fireEvent.click(screen.getByLabelText("AAA"));

    await fireEvent.click(screen.getByText("common.save"));

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith(
        {
          type_id: "hmip.heating.group",
          name: "Bad",
          forbid_single_operation: false,
          members: ["AAA:1"],
        },
        "ccu-a",
      ),
    );
    expect(mockToastSuccess).toHaveBeenCalledWith("groups.editor.created");
  });
});

describe("GroupEditor — edit", () => {
  it("pre-selects the current members and updates the group", async () => {
    render(GroupEditor, {
      props: {
        central: "ccu-a",
        group: {
          id: 4,
          name: "Wohnzimmer",
          type_id: "hmip.heating.group",
          forbid_single_operation: false,
          members: [{ address: "AAA:1", type_id: "SWITCH_ACTUATOR" }],
        },
        onClose: vi.fn(),
        onSaved: vi.fn(),
      },
    });

    await waitFor(() => expect(screen.getByLabelText("AAA")).toBeInTheDocument());
    // The create-form type picker is absent in edit mode.
    expect(mockGroupTypes).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByText("common.save"));

    await waitFor(() =>
      expect(mockUpdate).toHaveBeenCalledWith(
        4,
        {
          name: "Wohnzimmer",
          forbid_single_operation: false,
          members: ["AAA:1"],
        },
        "ccu-a",
      ),
    );
    expect(mockToastSuccess).toHaveBeenCalledWith("groups.editor.updated");
  });
});

describe("GroupEditor — enriched member fallback", () => {
  it("uses a group member's daemon-resolved name when it is not in the suitable list", async () => {
    // The CCU commonly reports already-grouped members as bare device addresses
    // that the type's suitable list no longer surfaces. The member row still
    // carries the daemon-resolved name, which the editor must use.
    mockSuitable.mockResolvedValue({
      assignable: [{ address: "AAA:1", type: "X", device_name: "Radiator" }],
      leftover: [],
    });

    render(GroupEditor, {
      props: {
        central: "ccu-a",
        group: {
          id: 7,
          name: "Duschbad",
          type_id: "hmip.heating.group",
          forbid_single_operation: false,
          members: [
            {
              address: "000C9709AEF269",
              type_id: "THERMOSTAT",
              device_name: "Wandthermostat DB",
              device_model: "HmIP-STHD",
              rooms: ["Duschbad"],
            },
          ],
        },
        onClose: vi.fn(),
        onSaved: vi.fn(),
      },
    });

    // The device checkbox is labelled by the resolved name, never the address.
    await waitFor(() =>
      expect(screen.getByLabelText("Wandthermostat DB")).toBeInTheDocument(),
    );
    expect(screen.queryByLabelText("000C9709AEF269")).not.toBeInTheDocument();
  });
});

describe("GroupEditor — config-pending candidates", () => {
  it("shows a config-pending leftover device but keeps it non-selectable", async () => {
    mockSuitable.mockResolvedValue({
      assignable: [{ address: "AAA:1", type: "X", device_name: "Radiator" }],
      leftover: [
        { address: "PEND:1", type: "X", device_name: "Pending", config_pending: true },
        { address: "WRONG:1", type: "Y", device_name: "WrongType" },
      ],
    });

    render(GroupEditor, {
      props: { central: "ccu-a", onClose: vi.fn(), onSaved: vi.fn() },
    });

    await waitFor(() => expect(screen.getByLabelText("Radiator")).toBeInTheDocument());

    // The config-pending device is surfaced but its checkbox is disabled and
    // carries the hint; a wrong-type leftover stays hidden (noise).
    expect(screen.getByLabelText("Pending")).toBeDisabled();
    expect(screen.getByText("groups.editor.config_pending")).toBeInTheDocument();
    expect(screen.queryByLabelText("WrongType")).not.toBeInTheDocument();

    // Selecting the assignable device and saving never picks up the pending one.
    await fireEvent.click(screen.getByLabelText("Radiator"));
    const name = screen.getByLabelText("groups.editor.name") as HTMLInputElement;
    await fireEvent.input(name, { target: { value: "G" } });
    await fireEvent.click(screen.getByText("common.save"));

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith(
        expect.objectContaining({ members: ["AAA:1"] }),
        "ccu-a",
      ),
    );
  });
});

describe("GroupEditor — type switch", () => {
  it("surfaces a failed candidate fetch and drops the previous type's channels", async () => {
    mockGroupTypes.mockResolvedValue([
      { id: "hmip.heating.group", label_key: "" },
      { id: "hmip.security.group", label_key: "" },
    ]);
    mockSuitable.mockImplementation((type: string) => {
      if (type === "hmip.heating.group") {
        return Promise.resolve({
          assignable: [{ address: "AAA:1", type: "X", device_name: "Radiator" }],
          leftover: [],
        });
      }
      return Promise.reject(new Error("jpages timeout"));
    });

    const { container } = render(GroupEditor, {
      props: { central: "ccu-a", onClose: vi.fn(), onSaved: vi.fn() },
    });

    await waitFor(() => expect(screen.getByLabelText("Radiator")).toBeInTheDocument());

    const select = container.querySelector("select") as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: "hmip.security.group" } });

    await waitFor(() => expect(mockToastError).toHaveBeenCalled());
    // The heating channels are not assignable to the newly selected type, so
    // they must not stay on offer.
    expect(screen.queryByLabelText("Radiator")).not.toBeInTheDocument();
  });
});
