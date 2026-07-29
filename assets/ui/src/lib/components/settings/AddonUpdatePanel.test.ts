// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen } from "@testing-library/svelte";

// The card sits on top of the real addonUpdate store (addonUpdate.svelte.ts)
// so its rune-based reactivity actually drives the DOM in this test — only
// the store's own collaborators (the REST client and the WS pump) are
// mocked, mirroring addonUpdate.test.ts. infoStore / confirmStore /
// toastStore / preferences are mocked directly since the card talks to
// them without an intermediate store.

let capturedHandler: ((ev: { type: string; payload?: unknown }) => void) | null =
  null;

vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: vi.fn((h: (ev: { type: string; payload?: unknown }) => void) => {
    capturedHandler = h;
    return vi.fn();
  }),
  status: vi.fn(() => "open" as const),
}));

const mockGetAddonUpdateStatus = vi.fn();
const mockCheckAddonUpdate = vi.fn();
const mockInstallAddonUpdate = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    getAddonUpdateStatus: (...args: unknown[]) => mockGetAddonUpdateStatus(...args),
    checkAddonUpdate: (...args: unknown[]) => mockCheckAddonUpdate(...args),
    installAddonUpdate: (...args: unknown[]) => mockInstallAddonUpdate(...args),
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
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

// Mutable per-test capability list — the card renders nothing at all
// when `addon_self_update` is absent from it.
const mockCapabilities: { list: string[] } = { list: ["addon_self_update"] };
const mockInfoEnsure = vi.fn().mockResolvedValue(undefined);
vi.mock("$lib/stores/info.svelte", () => ({
  infoStore: {
    get info() {
      return { capabilities: mockCapabilities.list };
    },
    ensure: (...args: unknown[]) => mockInfoEnsure(...args),
  },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en" },
}));

const mockConfirmAsk = vi.fn();
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();
vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string>) =>
    vars ? `${key} ${JSON.stringify(vars)}` : key,
}));

import { addonUpdateStore } from "$lib/stores/addonUpdate.svelte";
import type { AddonUpdateStatus } from "$lib/api/types";
import AddonUpdatePanel from "./AddonUpdatePanel.svelte";

function status(overrides: Partial<AddonUpdateStatus> = {}): AddonUpdateStatus {
  return {
    supported: true,
    current_version: "0.47.0",
    state: "idle",
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  capturedHandler = null;
  mockCapabilities.list = ["addon_self_update"];
  mockGetAddonUpdateStatus.mockResolvedValue(status());
  mockCheckAddonUpdate.mockResolvedValue(undefined);
  mockInstallAddonUpdate.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => {
  cleanup();
  addonUpdateStore.close();
});

describe("AddonUpdatePanel — capability gate", () => {
  it("renders nothing and never probes the API when the capability is absent", async () => {
    mockCapabilities.list = [];
    render(AddonUpdatePanel);

    // Give any stray onMount microtask a chance to run before asserting
    // the negative — otherwise a bug that fires unconditionally would
    // not be caught by a synchronous check alone.
    await Promise.resolve();
    await Promise.resolve();

    expect(screen.queryByText("addon_update.title")).toBeNull();
    expect(mockGetAddonUpdateStatus).not.toHaveBeenCalled();
  });

  it("renders the card when the capability is present", async () => {
    render(AddonUpdatePanel);
    await screen.findByText("addon_update.title");
    expect(mockGetAddonUpdateStatus).toHaveBeenCalledTimes(1);
  });
});

describe("AddonUpdatePanel — check for updates", () => {
  it("calls the check API when the button is clicked", async () => {
    render(AddonUpdatePanel);
    await screen.findByText("addon_update.title");

    await fireEvent.click(screen.getByText("addon_update.check"));

    await waitFor(() => expect(mockCheckAddonUpdate).toHaveBeenCalledTimes(1));
  });

  it("toasts an error when the check fails", async () => {
    mockCheckAddonUpdate.mockRejectedValueOnce(new Error("network down"));
    render(AddonUpdatePanel);
    await screen.findByText("addon_update.title");

    await fireEvent.click(screen.getByText("addon_update.check"));

    await waitFor(() => expect(mockToastError).toHaveBeenCalled());
  });
});

describe("AddonUpdatePanel — install flow", () => {
  it("asks the destructive confirm dialog and installs only once confirmed", async () => {
    mockGetAddonUpdateStatus.mockResolvedValue(
      status({ latest_version: "0.48.0", update_available: true }),
    );
    render(AddonUpdatePanel);
    await screen.findByText("addon_update.available");

    await fireEvent.click(screen.getByText("addon_update.install"));

    await waitFor(() =>
      expect(mockConfirmAsk).toHaveBeenCalledWith(
        expect.objectContaining({ destructive: true }),
      ),
    );
    await waitFor(() => expect(mockInstallAddonUpdate).toHaveBeenCalledTimes(1));
  });

  it("does not install when the confirm dialog is cancelled", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    mockGetAddonUpdateStatus.mockResolvedValue(
      status({ latest_version: "0.48.0", update_available: true }),
    );
    render(AddonUpdatePanel);
    await screen.findByText("addon_update.available");

    await fireEvent.click(screen.getByText("addon_update.install"));

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalled());
    expect(mockInstallAddonUpdate).not.toHaveBeenCalled();
  });

  it("shows the persistent installing notice while state is installing", async () => {
    mockGetAddonUpdateStatus.mockResolvedValue(status({ state: "installing" }));
    render(AddonUpdatePanel);

    await screen.findByText("addon_update.installing_notice");
    expect(screen.queryByText("addon_update.field.current_version")).toBeNull();
  });
});

describe("AddonUpdatePanel — failed state", () => {
  it("surfaces the daemon-reported error via the persistent ErrorState", async () => {
    mockGetAddonUpdateStatus.mockResolvedValue(
      status({ state: "failed", error: "checksum mismatch" }),
    );
    render(AddonUpdatePanel);

    await waitFor(() =>
      expect(screen.getByText(/checksum mismatch/)).toBeTruthy(),
    );
  });
});

describe("AddonUpdatePanel — reconnect after install", () => {
  it("toasts success once the socket is back and a new version is observed", async () => {
    render(AddonUpdatePanel);
    await screen.findByText("addon_update.title");
    expect(capturedHandler).not.toBeNull();

    // Let the mount's own refresh() resolve and paint the idle state first
    // — otherwise that still-pending fetch can resolve *after* the
    // broadcast below and clobber it back to idle before we can observe
    // the installing notice.
    await screen.findByText("addon_update.field.current_version");

    // Queue the post-restart GET response *before* triggering the
    // installing state — wsStatus() is mocked as always "open" in this
    // suite, so the reconnect-check effect fires the instant
    // `pendingInstallVersion` is armed, with no separate reconnect event
    // to wait for in between.
    mockGetAddonUpdateStatus.mockResolvedValueOnce(status({ current_version: "0.48.0" }));

    // The daemon pushes `installing` right before it restarts itself.
    capturedHandler!({
      type: "addon_update.state_changed",
      payload: status({ state: "installing" }),
    });

    await waitFor(() =>
      expect(mockToastSuccess).toHaveBeenCalledWith(
        expect.stringContaining("addon_update.toast.installed"),
      ),
    );
  });
});
