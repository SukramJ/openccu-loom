// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent, screen } from "@testing-library/svelte";
import type { Link } from "$lib/api/types";

const { mockListLinks, mockRemoveLink, mockGetDevice, mockToastSuccess, mockToastError, mockToastPush, mockConfirmAsk } =
  vi.hoisted(() => ({
    mockListLinks: vi.fn(),
    mockRemoveLink: vi.fn(),
    mockGetDevice: vi.fn(),
    mockToastSuccess: vi.fn(),
    mockToastError: vi.fn(),
    mockToastPush: vi.fn(),
    mockConfirmAsk: vi.fn(),
  }));

vi.mock("$lib/api/client", () => ({
  api: {
    listLinks: (...args: unknown[]) => mockListLinks(...args),
    removeLink: (...args: unknown[]) => mockRemoveLink(...args),
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
    push: (...args: unknown[]) => mockToastPush(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

import DeviceLinks from "./DeviceLinks.svelte";

const LINK_A: Link = {
  sender_address: "DEV001:1",
  receiver_address: "DEV002:2",
  peer_address: "DEV002:2",
  direction: "outgoing",
  name: "Original Name",
  description: "Original Description",
  sender_device_name: "Switch A",
  sender_channel_name: "Channel 1",
  sender_channel_type_label: "Switch",
  receiver_device_name: "Switch B",
  receiver_channel_name: "Channel 2",
  receiver_channel_type_label: "Switch",
};

async function renderLoaded() {
  const utils = render(DeviceLinks, { props: { deviceAddress: "DEV001", locale: "en" } });
  await waitFor(() => expect(screen.getByText("Original Name")).toBeTruthy());
  return utils;
}

function deleteButton(): HTMLElement {
  const btn = Array.from(document.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === "common.delete",
  );
  if (!btn) throw new Error("delete button not found");
  return btn;
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  location.hash = "";
  mockListLinks.mockResolvedValue([LINK_A]);
  mockConfirmAsk.mockResolvedValue(false);
  mockGetDevice.mockResolvedValue({ rx_mode: { always: true } });
});

afterEach(() => cleanup());

describe("DeviceLinks — list", () => {
  it("lists the device's links under the Sender | Link | Receiver header", async () => {
    await renderLoaded();
    expect(mockListLinks).toHaveBeenCalledWith("DEV001", "en");
    const groups = screen.getByTestId("column-groups");
    expect(groups.textContent).toContain("links.sender");
    expect(groups.textContent).toContain("links.editor.link");
    expect(groups.textContent).toContain("links.receiver");
    expect(screen.getByText("Channel 1")).toBeTruthy();
    expect(screen.getByText("Channel 2")).toBeTruthy();
    expect(screen.getByText("Original Description")).toBeTruthy();
  });

  it("links each row to the link's own page", async () => {
    await renderLoaded();
    const edit = screen.getByText("links.edit").closest("a");
    expect(edit?.getAttribute("href")).toBe("#/links/DEV001%3A1/DEV002%3A2");
  });

  it("starts the wizard on this device", async () => {
    await renderLoaded();
    await fireEvent.click(screen.getByText("links.new"));
    expect(location.hash).toBe("#/links/new?device=DEV001");
  });

  it("shows the empty state when the device has no links", async () => {
    mockListLinks.mockResolvedValue([]);
    render(DeviceLinks, { props: { deviceAddress: "DEV001", locale: "en" } });
    await waitFor(() => expect(screen.getByText("links.no_for_device")).toBeTruthy());
  });
});

describe("DeviceLinks — delete", () => {
  it("does nothing when the confirmation is declined", async () => {
    await renderLoaded();
    await fireEvent.click(deleteButton());
    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalled());
    expect(mockRemoveLink).not.toHaveBeenCalled();
  });

  it("shows the pending-wakeup hint instead of the plain toast when an end is a battery device", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockRemoveLink.mockResolvedValue(undefined);
    mockGetDevice.mockImplementation((addr: string) =>
      Promise.resolve(addr === "DEV002" ? { rx_mode: { wakeup: true } } : { rx_mode: { always: true } }),
    );
    await renderLoaded();
    await fireEvent.click(deleteButton());
    await waitFor(() => expect(mockRemoveLink).toHaveBeenCalledWith("DEV001", "DEV001:1", "DEV002:2"));
    await waitFor(() => expect(mockToastPush).toHaveBeenCalledTimes(1));
    expect(mockToastPush.mock.calls[0][1]).toBe("links.wakeup_pending.title");
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("shows the plain 'removed' toast for mains devices and reloads", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockRemoveLink.mockResolvedValue(undefined);
    await renderLoaded();
    await fireEvent.click(deleteButton());
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith("links.removed"));
    expect(mockListLinks).toHaveBeenCalledTimes(2);
  });

  it("reports a failed removal", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockRemoveLink.mockRejectedValue(new Error("boom"));
    await renderLoaded();
    await fireEvent.click(deleteButton());
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("links.removal_failed", "boom"));
  });
});
