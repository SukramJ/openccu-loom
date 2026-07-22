// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent } from "@testing-library/svelte";

const mockStatus = vi.fn();
const mockCreate = vi.fn();
const mockRemove = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastWarn = vi.fn();
const mockToastError = vi.fn();
const mockConfirmAsk = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    centralLinksStatus: (...args: unknown[]) => mockStatus(...args),
    createCentralLinks: (...args: unknown[]) => mockCreate(...args),
    removeCentralLinks: (...args: unknown[]) => mockRemove(...args),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    warn: (...args: unknown[]) => mockToastWarn(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import CentralLinksPanel from "./CentralLinksPanel.svelte";
import { ApiError } from "$lib/api/client";

const SUPPORTED_STATUS = {
  supported: true,
  eligible_channels: 2,
  channels: [{ address: "ABC0000001:4", number: 4, eligible: true }],
};

const UNSUPPORTED_STATUS = {
  supported: false,
  reason: "no button channels",
};

const NO_ELIGIBLE_STATUS = {
  supported: true,
  eligible_channels: 0,
  channels: [],
};

beforeEach(() => {
  vi.clearAllMocks();
  mockStatus.mockResolvedValue(SUPPORTED_STATUS);
});

afterEach(() => {
  cleanup();
});

// The device-wide switch renders its "enable" / "disable" buttons first,
// so the leading role query targets it (the per-channel row repeats the
// same labels below it).
async function deviceWideButton(label: string): Promise<HTMLElement> {
  let btn: HTMLElement | undefined;
  await waitFor(() => {
    const buttons = screen.getAllByRole("button", { name: label });
    btn = buttons[0];
    expect(btn).toBeTruthy();
  });
  return btn!;
}

// The per-channel row repeats the device-wide button labels below the
// device-wide row, so index 1 is the single channel's button in tests
// that render exactly one channel.
async function channelButton(label: string): Promise<HTMLElement> {
  let btn: HTMLElement | undefined;
  await waitFor(() => {
    const buttons = screen.getAllByRole("button", { name: label });
    expect(buttons.length).toBeGreaterThan(1);
    btn = buttons[1];
  });
  return btn!;
}

describe("CentralLinksPanel — toggle confirmation", () => {
  it("does not call the API when the confirm dialog is cancelled", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const enable = await deviceWideButton("central.enable");
    await fireEvent.click(enable);

    await waitFor(() => {
      expect(mockConfirmAsk).toHaveBeenCalledTimes(1);
    });
    expect(mockCreate).not.toHaveBeenCalled();
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("enables and shows a success toast on confirm", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockCreate.mockResolvedValue({ touched: 1, skipped: 0, failed: 0 });
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const enable = await deviceWideButton("central.enable");
    await fireEvent.click(enable);

    await waitFor(() => {
      expect(mockCreate).toHaveBeenCalledWith("ABC0000001");
      expect(mockToastSuccess).toHaveBeenCalled();
    });
    // Enabling is a non-destructive confirmation.
    expect(mockConfirmAsk).toHaveBeenCalledWith(
      expect.objectContaining({
        title: "central.confirm.enable_title",
        destructive: false,
      }),
    );
    expect(mockToastError).not.toHaveBeenCalled();
  });

  it("disables through the destructive confirm and shows a success toast", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockRemove.mockResolvedValue({ touched: 1, skipped: 0, failed: 0 });
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const disable = await deviceWideButton("central.disable");
    await fireEvent.click(disable);

    await waitFor(() => {
      expect(mockRemove).toHaveBeenCalledWith("ABC0000001");
      expect(mockToastSuccess).toHaveBeenCalled();
    });
    expect(mockConfirmAsk).toHaveBeenCalledWith(
      expect.objectContaining({
        title: "central.confirm.disable_title",
        destructive: true,
      }),
    );
  });

  it("warns instead of success when a channel failed", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockCreate.mockResolvedValue({ touched: 1, skipped: 0, failed: 1 });
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const enable = await deviceWideButton("central.enable");
    await fireEvent.click(enable);

    await waitFor(() => {
      expect(mockToastWarn).toHaveBeenCalled();
    });
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("shows an error toast when the API rejects", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockCreate.mockRejectedValue(new Error("network down"));
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const enable = await deviceWideButton("central.enable");
    await fireEvent.click(enable);

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalled();
    });
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("does not call the API when the destructive disable confirm is cancelled", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const disable = await deviceWideButton("central.disable");
    await fireEvent.click(disable);

    await waitFor(() => {
      expect(mockConfirmAsk).toHaveBeenCalledTimes(1);
    });
    expect(mockRemove).not.toHaveBeenCalled();
    expect(mockToastSuccess).not.toHaveBeenCalled();
    expect(mockToastError).not.toHaveBeenCalled();
  });

  it("formats the ApiError status and message into the error toast detail", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockCreate.mockRejectedValue(new ApiError(404, {}, "not found"));
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const enable = await deviceWideButton("central.enable");
    await fireEvent.click(enable);

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("central.action_failed", "404: not found");
    });
  });

  it("falls back to String(err) in the error toast detail for a non-Error rejection", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    // Not every rejection is an Error/ApiError instance (e.g. a thrown
    // string from an intermediate layer) — errorText()'s final branch
    // must still produce a readable toast instead of "[object Object]".
    mockCreate.mockRejectedValue("boom");
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const enable = await deviceWideButton("central.enable");
    await fireEvent.click(enable);

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("central.action_failed", "boom");
    });
  });
});

describe("CentralLinksPanel — per-channel toggle", () => {
  it("enables a single channel independently of the device-wide switch", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockCreate.mockResolvedValue({ touched: 1, skipped: 0, failed: 0 });
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const enable = await channelButton("central.enable");
    await fireEvent.click(enable);

    await waitFor(() => {
      expect(mockCreate).toHaveBeenCalledWith("ABC0000001", "ABC0000001:4");
      expect(mockToastSuccess).toHaveBeenCalled();
    });
    expect(mockRemove).not.toHaveBeenCalled();
  });

  it("disables a single channel through the destructive confirm", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockRemove.mockResolvedValue({ touched: 1, skipped: 0, failed: 0 });
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const disable = await channelButton("central.disable");
    await fireEvent.click(disable);

    await waitFor(() => {
      expect(mockRemove).toHaveBeenCalledWith("ABC0000001", "ABC0000001:4");
      expect(mockToastSuccess).toHaveBeenCalled();
    });
    expect(mockConfirmAsk).toHaveBeenCalledWith(
      expect.objectContaining({ title: "central.confirm.disable_title", destructive: true }),
    );
  });

  it("does not call the API when a channel toggle is cancelled", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const enable = await channelButton("central.enable");
    await fireEvent.click(enable);

    await waitFor(() => {
      expect(mockConfirmAsk).toHaveBeenCalledTimes(1);
    });
    expect(mockCreate).not.toHaveBeenCalled();
    expect(mockRemove).not.toHaveBeenCalled();
  });

  it("shows an error toast when a channel toggle rejects", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockRemove.mockRejectedValue(new Error("channel busy"));
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const disable = await channelButton("central.disable");
    await fireEvent.click(disable);

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("central.action_failed", "channel busy");
    });
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});

describe("CentralLinksPanel — duty-cycle / battery help hint", () => {
  it("renders the collapsible help with both key statements", async () => {
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    // The <details> content is present in the DOM even while collapsed.
    await waitFor(() => {
      expect(screen.getByText("central.help.summary")).toBeTruthy();
    });
    expect(screen.getByText("central.help.no_link")).toBeTruthy();
    expect(screen.getByText("central.help.duty_cycle")).toBeTruthy();
  });

  it("keeps the help hint visible even for unsupported devices", async () => {
    mockStatus.mockResolvedValue(UNSUPPORTED_STATUS);
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    await waitFor(() => {
      expect(screen.getByText("central.help.no_link")).toBeTruthy();
    });
    expect(screen.getByText("central.help.duty_cycle")).toBeTruthy();
  });

  it("stays visible while the initial status is still loading", async () => {
    let resolveStatus!: (value: typeof SUPPORTED_STATUS) => void;
    mockStatus.mockReturnValue(
      new Promise((resolve) => {
        resolveStatus = resolve;
      }),
    );
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    // The help hint is static markup, not gated on the status fetch —
    // it must render on first paint, before the request resolves.
    await waitFor(() => {
      expect(screen.getByText("common.loading")).toBeTruthy();
    });
    expect(screen.getByText("central.help.no_link")).toBeTruthy();
    expect(screen.getByText("central.help.duty_cycle")).toBeTruthy();

    resolveStatus(SUPPORTED_STATUS);
    await waitFor(() => {
      expect(screen.queryByText("common.loading")).toBeNull();
    });
  });

  it("stays visible when the status fetch fails", async () => {
    mockStatus.mockRejectedValue(new ApiError(500, {}, "ccu unreachable"));
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    await waitFor(() => {
      expect(screen.getByText(/ccu unreachable/)).toBeTruthy();
    });
    expect(screen.getByText("central.help.no_link")).toBeTruthy();
    expect(screen.getByText("central.help.duty_cycle")).toBeTruthy();
  });

  it("renders the <details> collapsed by default", async () => {
    const { container } = render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    await waitFor(() => {
      expect(screen.getByText("central.help.summary")).toBeTruthy();
    });
    const details = container.querySelector("details");
    expect(details).toBeTruthy();
    expect(details?.open).toBe(false);
  });
});

describe("CentralLinksPanel — status rendering edge cases", () => {
  it("disables the device-wide buttons when there are no eligible channels", async () => {
    mockStatus.mockResolvedValue(NO_ELIGIBLE_STATUS);
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    const enable = await deviceWideButton("central.enable");
    const disable = await deviceWideButton("central.disable");

    expect(enable).toBeDisabled();
    expect(disable).toBeDisabled();
  });

  it("renders no toggle buttons when the device does not support central links", async () => {
    mockStatus.mockResolvedValue(UNSUPPORTED_STATUS);
    const { container } = render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    // The unsupported message and its "(reason)" span share one <p>, so
    // matching on the container's flattened text avoids ambiguity
    // between the wrapping element and its nested reason span.
    await waitFor(() => {
      expect(container.textContent).toMatch(/central\.unsupported/);
    });
    expect(screen.queryAllByRole("button")).toHaveLength(0);
  });

  it("surfaces the load error message when the status fetch fails", async () => {
    mockStatus.mockRejectedValue(new ApiError(500, {}, "ccu unreachable"));
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    await waitFor(() => {
      expect(screen.getByText(/ccu unreachable/)).toBeTruthy();
    });
    expect(screen.queryAllByRole("button")).toHaveLength(0);
  });

  it("falls back to String(err) when the status fetch rejects with a non-ApiError value", async () => {
    // load()'s catch branch has the same ApiError/else split as
    // errorText() — a rejection that is not an ApiError instance must
    // still render a message instead of leaving the panel blank.
    mockStatus.mockRejectedValue("offline");
    render(CentralLinksPanel, { props: { address: "ABC0000001" } });

    await waitFor(() => {
      expect(screen.getByText(/offline/)).toBeTruthy();
    });
    expect(screen.queryAllByRole("button")).toHaveLength(0);
  });
});
