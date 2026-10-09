// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent, screen } from "@testing-library/svelte";

const { mockGetDevice, mockLinkable, mockListLinks, mockAddLink, mockToastSuccess, mockToastError, editable, devices } =
  vi.hoisted(() => ({
    mockGetDevice: vi.fn(),
    mockLinkable: vi.fn(),
    mockListLinks: vi.fn(),
    mockAddLink: vi.fn(),
    mockToastSuccess: vi.fn(),
    mockToastError: vi.fn(),
    editable: { value: true },
    devices: {
      items: [
        { address: "KEY", name: "Taster", model: "HmIP-WRC2", interface_id: "HmIP-RF", channels_count: 3, rooms: ["Flur"], functions: ["Licht"] },
        { address: "LAMP", name: "Lampe", model: "HmIP-BSM", interface_id: "HmIP-RF", channels_count: 5, rooms: ["Küche"], functions: ["Licht"] },
      ],
      loading: false,
      refresh: vi.fn(),
    },
  }));

vi.mock("$lib/api/client", () => ({
  api: {
    getDevice: (...a: unknown[]) => mockGetDevice(...a),
    linkableChannels: (...a: unknown[]) => mockLinkable(...a),
    listLinks: (...a: unknown[]) => mockListLinks(...a),
    addLink: (...a: unknown[]) => mockAddLink(...a),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) => (vars ? `${key} ${JSON.stringify(vars)}` : key),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...a: unknown[]) => mockToastSuccess(...a),
    error: (...a: unknown[]) => mockToastError(...a),
    push: vi.fn(),
  },
}));

vi.mock("$lib/stores/surfaces.svelte", () => ({
  surfacesStore: { opensVisible: () => editable.value, visible: () => true },
}));

vi.mock("$lib/stores/devices.svelte", () => ({ deviceStore: devices }));

vi.mock("$lib/links/wakeup-hint", () => ({
  notifyWakeupPending: vi.fn().mockResolvedValue(false),
}));

import LinkWizard from "./LinkWizard.svelte";

const KEY_DETAIL = {
  address: "KEY",
  name: "Taster",
  model: "HmIP-WRC2",
  interface_id: "HmIP-RF",
  channels: [
    { address: "KEY:0", number: 0, type: "MAINTENANCE", link_source_roles: [], link_target_roles: [] },
    { address: "KEY:1", number: 1, name: "Taste oben", type: "KEY_TRANSCEIVER", type_label: "Taster", link_source_roles: ["KEYMATIC"] },
    { address: "KEY:2", number: 2, type: "KEY_TRANSCEIVER", type_label: "Taster", link_source_roles: ["KEYMATIC"] },
  ],
};
const LAMP_DETAIL = {
  address: "LAMP",
  name: "Lampe",
  model: "HmIP-BSM",
  interface_id: "HmIP-RF",
  channels: [
    { address: "LAMP:4", number: 4, name: "Licht Küche", type: "SWITCH_VIRTUAL_RECEIVER", type_label: "Schaltaktor", link_target_roles: ["SWITCH"] },
  ],
};

function mount(query: string) {
  return render(LinkWizard, { props: { query, locale: "en" } });
}

async function pickFirst(name = "links.wizard.pick") {
  const btn = await waitFor(() => screen.getAllByText(name)[0]);
  await fireEvent.click(btn);
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  location.hash = "";
  editable.value = true;
  mockGetDevice.mockImplementation((addr: string) =>
    Promise.resolve(addr === "KEY" ? KEY_DETAIL : LAMP_DETAIL),
  );
  mockListLinks.mockResolvedValue([]);
  mockAddLink.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("LinkWizard — first partner from a device", () => {
  it("lists only the device's linkable channels, without the maintenance channel", async () => {
    mount("device=KEY");
    await waitFor(() => expect(screen.getByText("Taste oben")).toBeTruthy());
    expect(screen.getByText("Taster · Taster")).toBeTruthy();
    expect(screen.queryByText("Ch. 0")).toBeNull();
  });

  it("asks for the partners that fit a sender and fills room and function from the device list", async () => {
    mockLinkable.mockResolvedValue([
      { address: "LAMP:4", channel_type: "SWITCH_VIRTUAL_RECEIVER", channel_type_label: "Schaltaktor", channel_name: "Licht Küche", device_address: "LAMP", device_name: "Lampe", device_model: "HmIP-BSM" },
    ]);
    mount("device=KEY");
    await pickFirst();
    await waitFor(() => expect(mockLinkable).toHaveBeenCalledWith("KEY", 1, "sender", "HmIP-RF", "en"));
    await waitFor(() => expect(screen.getByText("Licht Küche")).toBeTruthy());
    expect(screen.getByText("Küche")).toBeTruthy();
  });

  it("names the link like the CCU and creates it from the sender's device", async () => {
    mockLinkable.mockResolvedValue([
      { address: "LAMP:4", channel_type_label: "Schaltaktor", channel_name: "Licht Küche", device_address: "LAMP", device_name: "Lampe", device_model: "HmIP-BSM" },
    ]);
    mount("device=KEY");
    await pickFirst();
    await pickFirst();
    const nameInput = (await waitFor(() => screen.getByLabelText("links.rename.name"))) as HTMLInputElement;
    expect(nameInput.value).toBe('links.wizard.default_name {"sender":"Taste oben","receiver":"Licht Küche"}');
    await fireEvent.click(screen.getByText("links.add.create"));
    await waitFor(() =>
      expect(mockAddLink).toHaveBeenCalledWith("KEY", {
        sender_address: "KEY:1",
        receiver_address: "LAMP:4",
        name: nameInput.value,
        description: 'links.wizard.default_description {"sender":"Taster","receiver":"Schaltaktor"}',
      }),
    );
    await waitFor(() => expect(location.hash).toBe("#/devices/KEY?tab=links"));
  });

  it("opens the new link's page on Create and edit", async () => {
    mockLinkable.mockResolvedValue([
      { address: "LAMP:4", channel_type_label: "Schaltaktor", channel_name: "Licht Küche", device_address: "LAMP", device_name: "Lampe", device_model: "HmIP-BSM" },
    ]);
    mount("device=KEY");
    await pickFirst();
    await pickFirst();
    await fireEvent.click(await waitFor(() => screen.getByText("links.add.create_and_edit")));
    await waitFor(() => expect(location.hash).toBe("#/links/KEY%3A1/LAMP%3A4"));
  });

  it("warns when the link already exists", async () => {
    mockLinkable.mockResolvedValue([
      { address: "LAMP:4", channel_type_label: "Schaltaktor", channel_name: "Licht Küche", device_address: "LAMP", device_name: "Lampe", device_model: "HmIP-BSM" },
    ]);
    mockListLinks.mockResolvedValue([{ sender_address: "KEY:1", receiver_address: "LAMP:4" }]);
    mount("device=KEY");
    await pickFirst();
    await pickFirst();
    await waitFor(() => expect(screen.getByText("links.wizard.exists")).toBeTruthy());
  });
});

describe("LinkWizard — a receiver first", () => {
  it("asks for senders and puts the receiver on the receiving end", async () => {
    mockLinkable.mockResolvedValue([
      { address: "KEY:1", channel_type_label: "Taster", channel_name: "Taste oben", device_address: "KEY", device_name: "Taster", device_model: "HmIP-WRC2" },
    ]);
    mount("device=LAMP");
    await pickFirst();
    await waitFor(() => expect(mockLinkable).toHaveBeenCalledWith("LAMP", 4, "receiver", "HmIP-RF", "en"));
    await pickFirst();
    await fireEvent.click(await waitFor(() => screen.getByText("links.add.create")));
    await waitFor(() =>
      expect(mockAddLink).toHaveBeenCalledWith("KEY", expect.objectContaining({ sender_address: "KEY:1", receiver_address: "LAMP:4" })),
    );
  });
});

describe("LinkWizard — anchored", () => {
  it("starts at step 2 for 'add receiver' on a sender group", async () => {
    mockLinkable.mockResolvedValue([]);
    mount("sender=KEY%3A1");
    await waitFor(() => expect(mockLinkable).toHaveBeenCalledWith("KEY", 1, "sender", "HmIP-RF", "en"));
  });

  it("offers nothing where the link editor is hidden", async () => {
    editable.value = false;
    mount("");
    expect(screen.getByText("links.editor_hidden")).toBeTruthy();
  });

  it("reports a failed create", async () => {
    mockLinkable.mockResolvedValue([
      { address: "LAMP:4", channel_type_label: "Schaltaktor", channel_name: "Licht Küche", device_address: "LAMP", device_name: "Lampe", device_model: "HmIP-BSM" },
    ]);
    mockAddLink.mockRejectedValue(new Error("nope"));
    mount("device=KEY");
    await pickFirst();
    await pickFirst();
    await fireEvent.click(await waitFor(() => screen.getByText("links.add.create")));
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("links.wizard.create_failed", "nope"));
    expect(location.hash).toBe("");
  });
});
