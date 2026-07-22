// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock state
// ---------------------------------------------------------------------------

const mockGetSystemUpdate = vi.fn();
const mockInstallSystemUpdate = vi.fn();
const mockDownloadSystemFirmware = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();
const mockConfirmAsk = vi.fn();

// Identity is mutated per test to flip the admin gate.
const mockIdentity: { role: string | null } = { role: "admin" };

// ---------------------------------------------------------------------------
// Module mocks — hoisted before the component import
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    getSystemUpdate: (...args: unknown[]) => mockGetSystemUpdate(...args),
    installSystemUpdate: (...args: unknown[]) => mockInstallSystemUpdate(...args),
    downloadSystemFirmware: (...args: unknown[]) => mockDownloadSystemFirmware(...args),
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

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: {
    get identity() {
      return mockIdentity.role ? { role: mockIdentity.role } : null;
    },
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// ---------------------------------------------------------------------------
// Component under test
// ---------------------------------------------------------------------------

import SystemUpdatePanel from "./SystemUpdatePanel.svelte";

const SINGLE_CENTRAL = [
  {
    central: "ccu-home",
    current_firmware: "3.75.9",
    available_firmware: "",
    update_available: false,
    in_progress: false,
    observed: true,
  },
];

const TWO_CENTRALS = [
  {
    central: "ccu-a",
    current_firmware: "3.75.9",
    available_firmware: "",
    update_available: false,
    in_progress: false,
    observed: true,
  },
  {
    central: "ccu-b",
    current_firmware: "3.75.9",
    available_firmware: "",
    update_available: false,
    in_progress: false,
    observed: true,
  },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockIdentity.role = "admin";
  mockGetSystemUpdate.mockResolvedValue(SINGLE_CENTRAL);
  mockInstallSystemUpdate.mockResolvedValue(undefined);
  mockDownloadSystemFirmware.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(cleanup);

describe("SystemUpdatePanel firmware download", () => {
  it("downloads the firmware, toasts success, and clears the url field", async () => {
    render(SystemUpdatePanel);
    await screen.findByText("firmware_download.title");

    const input = screen.getByLabelText("firmware_download.url_label") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "https://example.invalid/fw.tgz" } });
    await fireEvent.click(screen.getByText("firmware_download.download"));

    await waitFor(() =>
      expect(mockDownloadSystemFirmware).toHaveBeenCalledWith(
        "https://example.invalid/fw.tgz",
        undefined,
      ),
    );
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledTimes(1));
    expect(input.value).toBe("");
  });

  it("surfaces a download failure as a toast error and keeps the url for a retry", async () => {
    mockDownloadSystemFirmware.mockRejectedValue(new Error("ccu unreachable"));
    render(SystemUpdatePanel);
    await screen.findByText("firmware_download.title");

    const input = screen.getByLabelText("firmware_download.url_label") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "https://example.invalid/fw.tgz" } });
    await fireEvent.click(screen.getByText("firmware_download.download"));

    await waitFor(() => expect(mockToastError).toHaveBeenCalledTimes(1));
    expect(mockToastSuccess).not.toHaveBeenCalled();
    // The url is preserved so the operator can retry without retyping it.
    expect(input.value).toBe("https://example.invalid/fw.tgz");
  });

  it("disables the download button while the url field is empty", async () => {
    render(SystemUpdatePanel);
    await screen.findByText("firmware_download.title");

    const button = screen.getByText("firmware_download.download").closest("button");
    expect(button).toBeDisabled();

    const input = screen.getByLabelText("firmware_download.url_label") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "   " } });
    // Whitespace-only input must not enable the trigger.
    expect(button).toBeDisabled();

    await fireEvent.input(input, { target: { value: "https://example.invalid/fw.tgz" } });
    expect(button).not.toBeDisabled();
  });

  it("omits the central for a single-CCU deployment and does not render the central selector", async () => {
    const { container } = render(SystemUpdatePanel);
    await screen.findByText("firmware_download.title");

    // The bits-ui Select trigger is a <button aria-haspopup="listbox">, not
    // a native <select> — query by that marker rather than role="combobox".
    expect(container.querySelector('button[aria-haspopup="listbox"]')).toBeNull();

    const input = screen.getByLabelText("firmware_download.url_label") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "https://example.invalid/fw.tgz" } });
    await fireEvent.click(screen.getByText("firmware_download.download"));

    await waitFor(() =>
      expect(mockDownloadSystemFirmware).toHaveBeenCalledWith(
        "https://example.invalid/fw.tgz",
        undefined,
      ),
    );
  });

  it("defaults the central selector to the first CCU and includes it in a multi-CCU deployment", async () => {
    mockGetSystemUpdate.mockResolvedValue(TWO_CENTRALS);
    const { container } = render(SystemUpdatePanel);
    await screen.findByText("firmware_download.title");

    // Two distinct centrals render the selector (a bits-ui Select trigger,
    // not a native <select> — no role="combobox").
    await waitFor(() =>
      expect(container.querySelector('button[aria-haspopup="listbox"]')).toBeTruthy(),
    );

    const input = screen.getByLabelText("firmware_download.url_label") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "https://example.invalid/fw.tgz" } });
    await fireEvent.click(screen.getByText("firmware_download.download"));

    await waitFor(() =>
      expect(mockDownloadSystemFirmware).toHaveBeenCalledWith(
        "https://example.invalid/fw.tgz",
        "ccu-a",
      ),
    );
  });

  it("hides the firmware-download form for non-admins", async () => {
    mockIdentity.role = "viewer";
    render(SystemUpdatePanel);
    await screen.findByText("ccu_update.admin_only");
    expect(screen.queryByText("firmware_download.title")).toBeNull();
  });
});
