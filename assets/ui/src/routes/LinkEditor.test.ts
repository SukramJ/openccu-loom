// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent, screen, act } from "@testing-library/svelte";
import type { Link } from "$lib/api/types";

const {
  mockListLinks,
  mockUpdateLink,
  mockRemoveLink,
  mockToastSuccess,
  mockToastError,
  mockAsk,
  editable,
} = vi.hoisted(() => ({
  mockListLinks: vi.fn(),
  mockUpdateLink: vi.fn(),
  mockRemoveLink: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
  mockAsk: vi.fn(),
  editable: { value: true },
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listLinks: (...a: unknown[]) => mockListLinks(...a),
    updateLink: (...a: unknown[]) => mockUpdateLink(...a),
    removeLink: (...a: unknown[]) => mockRemoveLink(...a),
  },
  ApiError: class ApiError extends Error {
    status = 500;
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key} ${JSON.stringify(vars)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...a: unknown[]) => mockToastSuccess(...a),
    error: (...a: unknown[]) => mockToastError(...a),
    warn: vi.fn(),
    push: vi.fn(),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...a: unknown[]) => mockAsk(...a) },
}));

vi.mock("$lib/stores/surfaces.svelte", () => ({
  surfacesStore: { opensVisible: () => editable.value, visible: () => true },
}));

vi.mock("$lib/links/wakeup-hint", () => ({
  notifyWakeupPending: vi.fn().mockResolvedValue(false),
}));

vi.mock("$lib/components/channel/ChannelPanel.svelte", async () => {
  const mod = await import("./__testutils__/ChannelPanelStub.svelte");
  return { default: mod.default };
});

import LinkEditor from "./LinkEditor.svelte";
import { stubSides, resetStubSides } from "./__testutils__/channel-panel-stub";

const LINK: Link = {
  sender_address: "SEND:1",
  receiver_address: "RECV:4",
  name: "Taster Flur mit Licht Flur",
  description: "Schalter ein / aus",
  sender_channel_name: "Taster Flur",
  sender_device_name: "Taster",
  sender_device_model: "HmIP-WRC2",
  receiver_channel_name: "Licht Flur",
  receiver_device_name: "Licht",
  receiver_device_model: "HmIP-BSM",
  peer_address: "RECV:4",
  direction: "outgoing",
};

async function mount() {
  const r = render(LinkEditor, {
    props: { sender: "SEND:1", receiver: "RECV:4", locale: "en" },
  });
  await waitFor(() => expect(screen.getByTestId("link-peer-header")).toBeTruthy());
  return r;
}

async function makeDirty(side: "sender" | "receiver", n = 1) {
  await act(() => stubSides[side].markDirty!(n));
}

function applyButton(): HTMLElement {
  return screen.getByText("links.editor.apply");
}

beforeEach(() => {
  resetStubSides();
  editable.value = true;
  mockListLinks.mockReset();
  mockListLinks.mockResolvedValue([LINK]);
  mockUpdateLink.mockReset();
  mockUpdateLink.mockResolvedValue(undefined);
  mockRemoveLink.mockReset();
  mockRemoveLink.mockResolvedValue(undefined);
  mockToastSuccess.mockReset();
  mockToastError.mockReset();
  mockAsk.mockReset();
  location.hash = "";
});

afterEach(() => cleanup());

describe("LinkEditor — layout", () => {
  it("names both parties and mounts a panel per side, each keyed by the other end", async () => {
    await mount();
    expect(mockListLinks).toHaveBeenCalledWith("SEND", "en");
    const header = screen.getByTestId("link-peer-header");
    expect(header.textContent).toContain("Taster Flur");
    expect(header.textContent).toContain("Licht Flur");
    expect(screen.getByTestId("panel-sender").textContent).toContain("SEND:1 ← RECV:4");
    expect(screen.getByTestId("panel-receiver").textContent).toContain("RECV:4 ← SEND:1");
  });

  it("says so when the sender has no link parameters", async () => {
    resetStubSides();
    stubSides.sender = { saveResult: true, saves: 0, discards: 0, paramCount: 0 };
    await mount();
    await waitFor(() => expect(screen.getByText("links.editor.sender_empty")).toBeTruthy());
  });

  it("offers no editing where the surface profile hides the link editor", async () => {
    editable.value = false;
    render(LinkEditor, { props: { sender: "SEND:1", receiver: "RECV:4", locale: "en" } });
    await waitFor(() => expect(screen.getByText("links.editor_hidden")).toBeTruthy());
    expect(screen.queryByTestId("panel-receiver")).toBeNull();
  });

  it("reports a link that does not exist", async () => {
    mockListLinks.mockResolvedValue([]);
    render(LinkEditor, { props: { sender: "SEND:1", receiver: "RECV:4", locale: "en" } });
    await waitFor(() => expect(screen.getByText("links.editor.not_found")).toBeTruthy());
  });
});

describe("LinkEditor — one save bar for the link", () => {
  it("stays hidden until something is pending", async () => {
    await mount();
    expect(screen.queryByText("links.editor.apply")).toBeNull();
    await makeDirty("receiver", 2);
    expect(screen.getByText(/links\.editor\.pending \{"count":2\}/)).toBeTruthy();
  });

  it("writes both sides with one Übernehmen and confirms once", async () => {
    await mount();
    await makeDirty("receiver", 2);
    await makeDirty("sender", 1);
    await fireEvent.click(applyButton());
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith("links.editor.saved"));
    expect(stubSides.receiver.saves).toBe(1);
    expect(stubSides.sender.saves).toBe(1);
    expect(mockToastError).not.toHaveBeenCalled();
  });

  it("skips a side without edits", async () => {
    await mount();
    await makeDirty("receiver", 1);
    await fireEvent.click(applyButton());
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalled());
    expect(stubSides.sender.saves).toBe(0);
  });

  it("still writes the other side when one fails, and names the failed one", async () => {
    await mount();
    stubSides.receiver.saveResult = false;
    await makeDirty("receiver", 1);
    await makeDirty("sender", 1);
    await fireEvent.click(applyButton());
    await waitFor(() => expect(mockToastError).toHaveBeenCalled());
    expect(stubSides.sender.saves).toBe(1);
    expect(mockToastError.mock.calls[0][1]).toContain("links.editor.receiver");
    expect(mockToastError.mock.calls[0][1]).not.toContain("links.editor.sender");
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("saves name and description through the link's PATCH", async () => {
    await mount();
    const nameInput = screen.getByLabelText("links.rename.name") as HTMLInputElement;
    await fireEvent.input(nameInput, { target: { value: "Flurlicht" } });
    expect(screen.getByText(/links\.editor\.pending \{"count":1\}/)).toBeTruthy();
    await fireEvent.click(applyButton());
    await waitFor(() =>
      expect(mockUpdateLink).toHaveBeenCalledWith("SEND", {
        sender_address: "SEND:1",
        receiver_address: "RECV:4",
        name: "Flurlicht",
        description: "Schalter ein / aus",
      }),
    );
    await waitFor(() => expect(screen.queryByText("links.editor.apply")).toBeNull());
  });

  it("Abbrechen discards both sides and the name edit", async () => {
    await mount();
    await makeDirty("receiver", 1);
    const nameInput = screen.getByLabelText("links.rename.name") as HTMLInputElement;
    await fireEvent.input(nameInput, { target: { value: "x" } });
    await fireEvent.click(screen.getByText("common.cancel"));
    expect(stubSides.receiver.discards).toBe(1);
    expect(stubSides.sender.discards).toBe(1);
    expect(nameInput.value).toBe(LINK.name);
    await waitFor(() => expect(screen.queryByText("links.editor.apply")).toBeNull());
  });
});

describe("LinkEditor — delete", () => {
  it("deletes after confirmation and returns to the list", async () => {
    mockAsk.mockResolvedValue(true);
    await mount();
    await fireEvent.click(screen.getByText("links.editor.delete"));
    await waitFor(() => expect(mockRemoveLink).toHaveBeenCalledWith("SEND", "SEND:1", "RECV:4"));
    await waitFor(() => expect(location.hash).toBe("#/links"));
  });

  it("does nothing when the confirmation is declined", async () => {
    mockAsk.mockResolvedValue(false);
    await mount();
    await fireEvent.click(screen.getByText("links.editor.delete"));
    await waitFor(() => expect(mockAsk).toHaveBeenCalled());
    expect(mockRemoveLink).not.toHaveBeenCalled();
  });
});
