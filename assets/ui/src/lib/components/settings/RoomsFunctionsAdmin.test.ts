// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen, waitFor } from "@testing-library/svelte";
import type { Area, DeviceSummary } from "$lib/api/types";

// This file exercises the real areasStore + deviceStore singletons (not
// stubs) against a mocked $lib/api/client — same approach as
// devices.test.ts / centrals.test.ts — so the Areas admin section's CRUD
// wiring is proven end to end, not just against a hand-rolled store double.
const mockListRooms = vi.fn();
const mockListFunctions = vi.fn();
const mockListCentralsV2 = vi.fn();
const mockListAreas = vi.fn();
const mockCreateArea = vi.fn();
const mockPutArea = vi.fn();
const mockDeleteArea = vi.fn();
const mockPutAreaRooms = vi.fn();
const mockListDevices = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    listRooms: (...a: unknown[]) => mockListRooms(...a),
    listFunctions: (...a: unknown[]) => mockListFunctions(...a),
    listCentralsV2: (...a: unknown[]) => mockListCentralsV2(...a),
    listAreas: (...a: unknown[]) => mockListAreas(...a),
    createArea: (...a: unknown[]) => mockCreateArea(...a),
    putArea: (...a: unknown[]) => mockPutArea(...a),
    deleteArea: (...a: unknown[]) => mockDeleteArea(...a),
    putAreaRooms: (...a: unknown[]) => mockPutAreaRooms(...a),
    listDevices: (...a: unknown[]) => mockListDevices(...a),
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

// Whole-module mocks so the real areasStore/deviceStore singletons never
// pull in the real auth/events chain (mirrors devices.test.ts).
vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: { probe: vi.fn() },
}));
vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: vi.fn(() => () => {}),
}));

const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();
vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...a: unknown[]) => mockToastSuccess(...a),
    error: (...a: unknown[]) => mockToastError(...a),
  },
}));

const mockConfirmAsk = vi.fn();
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...a: unknown[]) => mockConfirmAsk(...a) },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import RoomsFunctionsAdmin from "./RoomsFunctionsAdmin.svelte";

function area(overrides: Partial<Area> = {}): Area {
  return { id: "a1", name: "Ground floor", rooms: [], ...overrides };
}

function device(overrides: Partial<DeviceSummary> & { address: string }): DeviceSummary {
  return {
    interface: "HmIP-RF",
    interface_id: "HmIP-RF",
    model: "HmIP-PSM",
    name: overrides.address,
    available: true,
    channels_count: 1,
    updatable: false,
    update_available: false,
    master_pushes_config_pending: false,
    has_sub_devices: false,
    ...overrides,
  } as DeviceSummary;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockListRooms.mockResolvedValue([]);
  mockListFunctions.mockResolvedValue([]);
  mockListCentralsV2.mockResolvedValue([]);
  mockListAreas.mockResolvedValue([]);
  mockListDevices.mockResolvedValue({ items: [], total: 0 });
  mockCreateArea.mockResolvedValue({ id: "new-1", name: "New area" });
  mockPutArea.mockResolvedValue(undefined);
  mockDeleteArea.mockResolvedValue(undefined);
  mockPutAreaRooms.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => cleanup());

describe("RoomsFunctionsAdmin — Areas: list", () => {
  it("shows the empty state when no areas are configured", async () => {
    render(RoomsFunctionsAdmin);
    expect(await screen.findByText("areas.empty")).toBeTruthy();
  });

  it("renders the configured areas with their room counts", async () => {
    mockListAreas.mockResolvedValue([
      area({ id: "a1", name: "Ground floor", rooms: [{ central: "ccu1", room: "Kitchen" }] }),
    ]);
    render(RoomsFunctionsAdmin);
    expect(await screen.findByText("Ground floor")).toBeTruthy();
  });
});

describe("RoomsFunctionsAdmin — Areas: create", () => {
  it("creates an area from the name input (Enter) and refreshes the list", async () => {
    mockListAreas
      .mockResolvedValueOnce([]) // initial load on mount
      .mockResolvedValueOnce([area({ id: "new-1", name: "Attic" })]); // post-create refresh
    render(RoomsFunctionsAdmin);

    const input = await screen.findByPlaceholderText("areas.placeholder");
    await fireEvent.input(input, { target: { value: "Attic" } });
    await fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => expect(mockCreateArea).toHaveBeenCalledWith({ id: "", name: "Attic" }));
    expect(mockToastSuccess).toHaveBeenCalledWith("groups.created");
    await waitFor(() => expect(screen.getByText("Attic")).toBeTruthy());
    // The input clears after a successful create. The list started empty,
    // so the Areas card briefly shows its own loading state during the
    // post-create refresh (same `loading && length === 0` idiom used
    // elsewhere in the SPA) — re-query rather than reuse the pre-refresh
    // element reference, which the remount detaches.
    const inputAfter = screen.getByPlaceholderText("areas.placeholder") as HTMLInputElement;
    expect(inputAfter.value).toBe("");
  });

  it("does not create when the trimmed name is empty", async () => {
    render(RoomsFunctionsAdmin);

    const input = await screen.findByPlaceholderText("areas.placeholder");
    await fireEvent.input(input, { target: { value: "   " } });
    await fireEvent.keyDown(input, { key: "Enter" });

    expect(mockCreateArea).not.toHaveBeenCalled();
  });
});

describe("RoomsFunctionsAdmin — Areas: rename", () => {
  it("renames inline via Enter and refreshes", async () => {
    mockListAreas
      .mockResolvedValueOnce([area({ id: "a1", name: "Attic", position: 3 })])
      .mockResolvedValueOnce([area({ id: "a1", name: "Loft", position: 3 })]);
    render(RoomsFunctionsAdmin);
    await screen.findByText("Attic");

    await fireEvent.click(screen.getByText("groups.rename"));
    const input = screen.getByDisplayValue("Attic");
    await fireEvent.input(input, { target: { value: "Loft" } });
    await fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() =>
      expect(mockPutArea).toHaveBeenCalledWith("a1", { id: "a1", name: "Loft", position: 3 }),
    );
    expect(mockToastSuccess).toHaveBeenCalledWith("groups.renamed");
    await waitFor(() => expect(screen.getByText("Loft")).toBeTruthy());
  });

  it("Escape cancels the rename without calling the API", async () => {
    mockListAreas.mockResolvedValue([area({ id: "a1", name: "Attic" })]);
    render(RoomsFunctionsAdmin);
    await screen.findByText("Attic");

    await fireEvent.click(screen.getByText("groups.rename"));
    const input = screen.getByDisplayValue("Attic");
    await fireEvent.input(input, { target: { value: "Loft" } });
    await fireEvent.keyDown(input, { key: "Escape" });

    expect(mockPutArea).not.toHaveBeenCalled();
    expect(screen.getByText("Attic")).toBeTruthy();
  });
});

describe("RoomsFunctionsAdmin — Areas: delete", () => {
  it("asks for confirmation, then deletes and refreshes", async () => {
    mockListAreas
      .mockResolvedValueOnce([area({ id: "a1", name: "Attic" })])
      .mockResolvedValueOnce([]);
    render(RoomsFunctionsAdmin);
    await screen.findByText("Attic");

    await fireEvent.click(screen.getByText("common.delete"));

    expect(mockConfirmAsk).toHaveBeenCalledWith(
      expect.objectContaining({ title: "areas.delete_confirm", destructive: true }),
    );
    await waitFor(() => expect(mockDeleteArea).toHaveBeenCalledWith("a1"));
    expect(mockToastSuccess).toHaveBeenCalledWith("groups.deleted");
    await waitFor(() => expect(screen.queryByText("Attic")).toBeNull());
  });

  it("does not delete when the confirm dialog is declined", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    mockListAreas.mockResolvedValue([area({ id: "a1", name: "Attic" })]);
    render(RoomsFunctionsAdmin);
    await screen.findByText("Attic");

    await fireEvent.click(screen.getByText("common.delete"));

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalled());
    expect(mockDeleteArea).not.toHaveBeenCalled();
    expect(screen.getByText("Attic")).toBeTruthy();
  });
});

describe("RoomsFunctionsAdmin — Areas: room assignment drawer", () => {
  it("lists known (central, room) pairs, pre-checks the area's current rooms, and saves a full-set replace", async () => {
    mockListAreas.mockResolvedValue([
      area({ id: "a1", name: "Upstairs", rooms: [{ central: "ccu1", room: "Bedroom" }] }),
    ]);
    mockListDevices.mockResolvedValue({
      items: [
        device({ address: "D1", central: "ccu1", rooms: ["Bedroom"] }),
        device({ address: "D2", central: "ccu1", rooms: ["Kitchen"] }),
      ],
      total: 2,
    });
    render(RoomsFunctionsAdmin);
    await screen.findByText("Upstairs");

    await fireEvent.click(screen.getByText("areas.assign_rooms"));
    await screen.findByText("Bedroom");
    expect(screen.getByText("Kitchen")).toBeTruthy();

    const bedroom = screen.getByRole("checkbox", { name: "Bedroom" }) as HTMLInputElement;
    const kitchen = screen.getByRole("checkbox", { name: "Kitchen" }) as HTMLInputElement;
    // Already assigned to this area — pre-checked; Kitchen is not.
    expect(bedroom.checked).toBe(true);
    expect(kitchen.checked).toBe(false);

    await fireEvent.click(kitchen);
    await fireEvent.click(screen.getByText("common.save"));

    await waitFor(() => expect(mockPutAreaRooms).toHaveBeenCalledTimes(1));
    const [id, refs] = mockPutAreaRooms.mock.calls[0];
    expect(id).toBe("a1");
    expect(refs).toEqual(
      expect.arrayContaining([
        { central: "ccu1", room: "Bedroom" },
        { central: "ccu1", room: "Kitchen" },
      ]),
    );
    expect(refs).toHaveLength(2);
    expect(mockToastSuccess).toHaveBeenCalledWith("areas.toast.rooms_saved");
  });

  it("unchecking a pre-assigned room drops it from the full-set replace", async () => {
    mockListAreas.mockResolvedValue([
      area({ id: "a1", name: "Upstairs", rooms: [{ central: "ccu1", room: "Bedroom" }] }),
    ]);
    mockListDevices.mockResolvedValue({
      items: [device({ address: "D1", central: "ccu1", rooms: ["Bedroom"] })],
      total: 1,
    });
    render(RoomsFunctionsAdmin);
    await screen.findByText("Upstairs");

    await fireEvent.click(screen.getByText("areas.assign_rooms"));
    const bedroom = (await screen.findByRole("checkbox", { name: "Bedroom" })) as HTMLInputElement;
    await fireEvent.click(bedroom);
    await fireEvent.click(screen.getByText("common.save"));

    await waitFor(() => expect(mockPutAreaRooms).toHaveBeenCalledWith("a1", []));
  });

  it("surfaces which other area a room currently belongs to", async () => {
    // "Attic" sorts before "Garden" alphabetically, so its assign button is
    // the first one — opening it must not show Shed as already its own.
    mockListAreas.mockResolvedValue([
      area({ id: "a1", name: "Attic", rooms: [] }),
      area({ id: "a2", name: "Garden", rooms: [{ central: "ccu1", room: "Shed" }] }),
    ]);
    mockListDevices.mockResolvedValue({
      items: [device({ address: "D1", central: "ccu1", rooms: ["Shed"] })],
      total: 1,
    });
    render(RoomsFunctionsAdmin);
    await screen.findByText("Attic");

    const assignButtons = screen.getAllByText("areas.assign_rooms");
    await fireEvent.click(assignButtons[0]);

    await screen.findByText("Shed");
    expect(
      screen.getByText('areas.rooms_dialog.current_area:{"name":"Garden"}'),
    ).toBeTruthy();
  });

  it("cancel closes the drawer without saving", async () => {
    mockListAreas.mockResolvedValue([area({ id: "a1", name: "Upstairs", rooms: [] })]);
    mockListDevices.mockResolvedValue({
      items: [device({ address: "D1", central: "ccu1", rooms: ["Bedroom"] })],
      total: 1,
    });
    render(RoomsFunctionsAdmin);
    await screen.findByText("Upstairs");

    await fireEvent.click(screen.getByText("areas.assign_rooms"));
    const bedroom = (await screen.findByRole("checkbox", { name: "Bedroom" })) as HTMLInputElement;
    await fireEvent.click(bedroom);
    await fireEvent.click(screen.getByText("common.cancel"));

    expect(mockPutAreaRooms).not.toHaveBeenCalled();
    expect(screen.queryByText("Bedroom")).toBeNull();
  });
});
