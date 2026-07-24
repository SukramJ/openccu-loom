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

    // Members loaded for the sole HmIP type.
    await waitFor(() => expect(screen.getByText("AAA:1")).toBeInTheDocument());
    expect(mockGroupTypes).toHaveBeenCalledWith("ccu-a");
    expect(mockSuitable).toHaveBeenCalledWith("hmip.heating.group", "ccu-a");

    // Name + one member.
    const name = screen.getByLabelText("groups.editor.name") as HTMLInputElement;
    await fireEvent.input(name, { target: { value: "Bad" } });
    await fireEvent.click(
      screen.getByText("AAA:1").closest("label")!.querySelector("input")!,
    );

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

    await waitFor(() => expect(screen.getByText("AAA:1")).toBeInTheDocument());
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
