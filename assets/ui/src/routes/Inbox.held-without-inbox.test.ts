// @vitest-environment happy-dom
//
// A system without a CCU inbox (openccu-lite) never offers the hub.inbox
// feature, yet the daemon's own hold — a device parked by
// delay_new_device_creation, or one waiting for its release — is listed by
// the inbox endpoint there too, and this view is the only place it can be
// accepted. The feature gate must not hide those entries; it keeps its
// explanation only when there is nothing held to show.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent } from "@testing-library/svelte";

const { mockListInbox, mockAcceptInboxDevice } = vi.hoisted(() => ({
  mockListInbox: vi.fn(),
  mockAcceptInboxDevice: vi.fn(),
}));

// One openccu-lite central that lacks the inbox for a lasting reason.
// Hoisted because the module mock below is evaluated before this file's
// own statements.
const LITE = vi.hoisted(() => ({
  name: "lite",
  features: { "hub.inbox": { available: false, reason: "unsupported" } },
}));

vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: {
    items: [LITE],
    offers: () => false,
    featureAvailable: (key: string) => key !== "hub.inbox",
    centralsLacking: (key: string) => (key === "hub.inbox" ? [LITE] : []),
    featureOf: () => undefined,
    byName: () => undefined,
  },
}));

vi.mock("$lib/features", () => ({
  centralFeatureReason: () => "reason",
  featureName: (key: string) => key,
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listInbox: (...args: unknown[]) => mockListInbox(...args),
    listRooms: vi.fn().mockResolvedValue([]),
    listFunctions: vi.fn().mockResolvedValue([]),
    acceptInboxDevice: (...args: unknown[]) => mockAcceptInboxDevice(...args),
    releaseDevice: vi.fn(),
    listReplaceCandidates: vi.fn().mockResolvedValue([]),
    replaceDevice: vi.fn(),
    searchWiredDevices: vi.fn(),
    getGroups: vi.fn().mockResolvedValue([]),
    groupSuitableMembers: vi.fn(),
    updateGroup: vi.fn(),
    createRoom: vi.fn(),
    createFunction: vi.fn(),
    listInstallModeInterfaces: vi.fn().mockResolvedValue([]),
    setInstallModeInterface: vi.fn(),
    pairDeviceInstallMode: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status = 0;
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(true) },
}));

vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("./__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en", expertMode: false },
}));

// The inbox opens the add-device dialog, which reads the device list; the
// real store would pull in the auth store.
vi.mock("$lib/stores/devices.svelte", () => ({
  deviceStore: {
    items: [],
    loading: false,
    error: null,
    lastLoaded: null,
    refresh: vi.fn().mockResolvedValue(undefined),
    ensureStream: vi.fn(),
    close: vi.fn(),
  },
}));

vi.mock("$lib/stores/installMode.svelte", () => ({
  installModeStore: {
    active: false,
    remainingSeconds: null,
    interfaces: [],
    busy: false,
    banner: null,
    refresh: vi.fn(),
    toggle: vi.fn(),
    ensurePoll: vi.fn(),
    release: vi.fn(),
  },
}));

import Inbox from "./Inbox.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  mockAcceptInboxDevice.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("Inbox — fleet without a CCU inbox", () => {
  it("lists a device the daemon holds back and accepts it", async () => {
    mockListInbox.mockResolvedValue([
      { address: "0009ABCD", model: "HmIP-STH", central: "lite", pending_creation: true },
    ]);
    render(Inbox);

    await waitFor(() =>
      expect(screen.getByText("inbox.pending_creation_badge")).toBeInTheDocument(),
    );
    await fireEvent.click(screen.getByText("inbox.accept"));
    await fireEvent.click(await screen.findByText("inbox.accept_dialog.submit"));

    await waitFor(() =>
      expect(mockAcceptInboxDevice).toHaveBeenCalledWith("0009ABCD", "lite", undefined),
    );
  });

  // Negative control: with nothing held, the gate still explains why the
  // view is empty instead of rendering an empty table.
  it("keeps the feature gate's explanation when nothing is held", async () => {
    mockListInbox.mockResolvedValue([]);
    render(Inbox);

    await waitFor(() => expect(mockListInbox).toHaveBeenCalled());
    expect(screen.getByText("feature.gate.none")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });
});
