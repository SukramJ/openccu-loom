// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import type { Link } from "$lib/api/types";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockListLinks = vi.fn();
const mockUpdateLink = vi.fn();
const mockRemoveLink = vi.fn();
const mockGetDevice = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listLinks: (...args: unknown[]) => mockListLinks(...args),
    updateLink: (...args: unknown[]) => mockUpdateLink(...args),
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
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, _params?: unknown) => key,
}));

const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();
const mockToastPush = vi.fn();

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
    push: (...args: unknown[]) => mockToastPush(...args),
  },
}));

const mockConfirmAsk = vi.fn();
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

// AddLinkForm and LinkConfigPanel have their own component tests (via
// their respective usage sites); here they only need to prove the
// rename panel yields the stage to them and back, so both are stubbed
// to a DOM marker (mirrors the ScheduleTab.test.ts editor-stub pattern).
//
// The mock also captures the `onAdded` callback prop DeviceLinks wires in,
// so the "add-link wakeup hint" tests below can invoke it directly and
// exercise DeviceLinks' own onAdded() handler without re-implementing
// AddLinkForm's submit flow (already covered by AddLinkForm's own tests).
let capturedOnAdded:
  | ((result: { senderAddress: string; receiverAddress: string }) => void)
  | undefined;
vi.mock("./AddLinkForm.svelte", () => ({
  default: (_anchor: unknown, props: Record<string, unknown>) => {
    document.body.setAttribute("data-add-form-rendered", "1");
    capturedOnAdded = props.onAdded as typeof capturedOnAdded;
    return { $set: vi.fn(), $destroy: vi.fn() };
  },
}));
vi.mock("./LinkConfigPanel.svelte", () => ({
  default: () => {
    document.body.setAttribute("data-config-panel-rendered", "1");
    return { $set: vi.fn(), $destroy: vi.fn() };
  },
}));

import DeviceLinks from "./DeviceLinks.svelte";

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

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

function renameButton(container: HTMLElement): HTMLElement {
  const btn = container.querySelector('button[aria-label="links.rename"]');
  if (!btn) throw new Error("rename button not found");
  return btn as HTMLElement;
}

function findButtonByText(container: HTMLElement, text: string): HTMLElement {
  const buttons = Array.from(container.querySelectorAll("button"));
  const btn = buttons.find((b) => b.textContent?.trim() === text);
  if (!btn) throw new Error(`button with text "${text}" not found`);
  return btn as HTMLElement;
}

function nameInput(container: HTMLElement): HTMLInputElement {
  const el = container.querySelector('input[placeholder="links.rename.name_placeholder"]');
  if (!el) throw new Error("rename name input not found");
  return el as HTMLInputElement;
}

function descriptionInput(container: HTMLElement): HTMLInputElement {
  const el = container.querySelector(
    'input[placeholder="links.rename.description_placeholder"]',
  );
  if (!el) throw new Error("rename description input not found");
  return el as HTMLInputElement;
}

async function renderLoaded() {
  const utils = render(DeviceLinks, {
    props: { deviceAddress: "DEV001", interfaceId: "HmIP-RF", locale: "en" },
  });
  await waitFor(() => {
    expect(utils.container.querySelector('button[aria-label="links.rename"]')).toBeTruthy();
  });
  return utils;
}

beforeEach(() => {
  vi.clearAllMocks();
  document.body.removeAttribute("data-add-form-rendered");
  document.body.removeAttribute("data-config-panel-rendered");
  capturedOnAdded = undefined;
  mockListLinks.mockResolvedValue([LINK_A]);
  // Default: destructive prompts are declined so rename tests never
  // reach a delete; the delete-hint tests opt in per case.
  mockConfirmAsk.mockResolvedValue(false);
  // Default: both endpoints are mains devices (no wakeup hint) unless a
  // test overrides per address.
  mockGetDevice.mockResolvedValue({ rx_mode: { always: true } });
});

afterEach(() => {
  cleanup();
});

describe("DeviceLinks — load", () => {
  it("renders the loaded link with its name and party labels", async () => {
    const { container } = await renderLoaded();
    expect(container.textContent).toContain("Original Name");
    expect(container.textContent).toContain("Switch A");
    expect(container.textContent).toContain("Switch B");
    expect(mockListLinks).toHaveBeenCalledWith("DEV001", "en");
  });
});

describe("DeviceLinks — rename happy path", () => {
  it("opens the rename form prefilled from the link", async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(renameButton(container));

    expect(nameInput(container).value).toBe("Original Name");
    expect(descriptionInput(container).value).toBe("Original Description");
  });

  it("saves the edited name/description, toasts success, and reloads", async () => {
    mockUpdateLink.mockResolvedValue(undefined);
    const { container } = await renderLoaded();
    await fireEvent.click(renameButton(container));

    await fireEvent.input(nameInput(container), { target: { value: "Stairs" } });
    await fireEvent.input(descriptionInput(container), { target: { value: "auto light" } });
    await fireEvent.click(findButtonByText(container, "common.save"));

    await waitFor(() => {
      expect(mockUpdateLink).toHaveBeenCalledWith("DEV001", {
        sender_address: "DEV001:1",
        receiver_address: "DEV002:2",
        name: "Stairs",
        description: "auto light",
      });
    });
    await waitFor(() => {
      expect(mockToastSuccess).toHaveBeenCalledWith("links.renamed");
    });
    // The rename panel closes and the list is reloaded (initial load + reload).
    await waitFor(() => {
      expect(container.querySelector('input[placeholder="links.rename.name_placeholder"]')).toBeNull();
    });
    expect(mockListLinks).toHaveBeenCalledTimes(2);
  });

  it("clearing the description sends an empty string to clear it on the CCU", async () => {
    mockUpdateLink.mockResolvedValue(undefined);
    const { container } = await renderLoaded();
    await fireEvent.click(renameButton(container));

    await fireEvent.input(descriptionInput(container), { target: { value: "" } });
    await fireEvent.click(findButtonByText(container, "common.save"));

    await waitFor(() => {
      expect(mockUpdateLink).toHaveBeenCalledWith("DEV001", {
        sender_address: "DEV001:1",
        receiver_address: "DEV002:2",
        name: "Original Name",
        description: "",
      });
    });
  });
});

describe("DeviceLinks — rename error path", () => {
  it("shows an error toast, keeps the form open, and re-enables Save on failure", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockUpdateLink.mockRejectedValue(new ApiError(500, {}, "boom"));
    const { container } = await renderLoaded();
    await fireEvent.click(renameButton(container));
    await fireEvent.click(findButtonByText(container, "common.save"));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("links.rename_failed", "500: boom");
    });
    // The form is still open — a failed save must not silently discard
    // the operator's edit, and the button must not stay stuck "saving".
    expect(nameInput(container)).toBeTruthy();
    expect(findButtonByText(container, "common.save")).toBeTruthy();
  });
});

describe("DeviceLinks — rename edge cases", () => {
  it("cancel closes the form without calling the API", async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(renameButton(container));
    await fireEvent.input(nameInput(container), { target: { value: "Changed but discarded" } });

    await fireEvent.click(findButtonByText(container, "common.cancel"));

    expect(
      container.querySelector('input[placeholder="links.rename.name_placeholder"]'),
    ).toBeNull();
    expect(mockUpdateLink).not.toHaveBeenCalled();
  });

  it("opening the add-link form closes an in-progress rename", async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(renameButton(container));
    expect(nameInput(container)).toBeTruthy();

    await fireEvent.click(findButtonByText(container, "common.add"));

    await waitFor(() => {
      expect(document.body.getAttribute("data-add-form-rendered")).toBe("1");
    });
    expect(
      container.querySelector('input[placeholder="links.rename.name_placeholder"]'),
    ).toBeNull();
  });

  it("starting a rename closes an in-progress add", async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(findButtonByText(container, "common.add"));
    await waitFor(() => {
      expect(document.body.getAttribute("data-add-form-rendered")).toBe("1");
    });

    await fireEvent.click(renameButton(container));

    expect(nameInput(container).value).toBe("Original Name");
  });
});

describe("DeviceLinks — delete wakeup hint", () => {
  it("shows the pending-wakeup info toast instead of the plain 'removed' toast when a link endpoint is a battery device", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockRemoveLink.mockResolvedValue(undefined);
    // Receiver DEV002 is a battery device; sender DEV001 is mains.
    mockGetDevice.mockImplementation((addr: string) =>
      Promise.resolve(
        addr === "DEV002"
          ? { rx_mode: { wakeup: true } }
          : { rx_mode: { always: true } },
      ),
    );
    const { container } = await renderLoaded();
    await fireEvent.click(findButtonByText(container, "common.delete"));

    await waitFor(() => {
      expect(mockRemoveLink).toHaveBeenCalledWith(
        "DEV001",
        "DEV001:1",
        "DEV002:2",
      );
    });
    await waitFor(() => {
      expect(mockToastPush).toHaveBeenCalledTimes(1);
    });
    const [severity, title] = mockToastPush.mock.calls[0];
    expect(severity).toBe("info");
    expect(title).toBe("links.wakeup_pending.title");
    // The plain success toast is suppressed — the hint conveys success.
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("shows the plain 'removed' toast when both endpoints are mains devices", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockRemoveLink.mockResolvedValue(undefined);
    mockGetDevice.mockResolvedValue({ rx_mode: { always: true } });
    const { container } = await renderLoaded();
    await fireEvent.click(findButtonByText(container, "common.delete"));

    await waitFor(() => {
      expect(mockToastSuccess).toHaveBeenCalledWith("links.removed");
    });
    expect(mockToastPush).not.toHaveBeenCalled();
  });
});

describe("DeviceLinks — add wakeup hint", () => {
  async function openAddForm(container: HTMLElement) {
    await fireEvent.click(findButtonByText(container, "common.add"));
    await waitFor(() => {
      expect(document.body.getAttribute("data-add-form-rendered")).toBe("1");
    });
    expect(capturedOnAdded).toBeTypeOf("function");
  }

  it("shows the pending-wakeup info toast instead of the plain 'created' toast when a link endpoint is a battery device", async () => {
    mockGetDevice.mockImplementation((addr: string) =>
      Promise.resolve(
        addr === "DEV009"
          ? { rx_mode: { wakeup: true } }
          : { rx_mode: { always: true } },
      ),
    );
    const { container } = await renderLoaded();
    await openAddForm(container);

    await capturedOnAdded!({
      senderAddress: "DEV001:1",
      receiverAddress: "DEV009:2",
    });

    await waitFor(() => {
      expect(mockToastPush).toHaveBeenCalledTimes(1);
    });
    const [severity, title] = mockToastPush.mock.calls[0];
    expect(severity).toBe("info");
    expect(title).toBe("links.wakeup_pending.title");
    // The plain success toast is suppressed — the hint conveys success.
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("shows the plain 'created' toast when both endpoints are mains devices", async () => {
    mockGetDevice.mockResolvedValue({ rx_mode: { always: true } });
    const { container } = await renderLoaded();
    await openAddForm(container);

    await capturedOnAdded!({
      senderAddress: "DEV001:1",
      receiverAddress: "DEV002:2",
    });

    await waitFor(() => {
      expect(mockToastSuccess).toHaveBeenCalledWith("links.created");
    });
    expect(mockToastPush).not.toHaveBeenCalled();
  });

  it("treats a device fetch failure on either endpoint as 'no wakeup' and still shows the plain 'created' toast", async () => {
    mockGetDevice.mockRejectedValue(new Error("network down"));
    const { container } = await renderLoaded();
    await openAddForm(container);

    await capturedOnAdded!({
      senderAddress: "DEV001:1",
      receiverAddress: "DEV002:2",
    });

    await waitFor(() => {
      expect(mockToastSuccess).toHaveBeenCalledWith("links.created");
    });
    expect(mockToastPush).not.toHaveBeenCalled();
  });
});
