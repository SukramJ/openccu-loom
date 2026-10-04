// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { InstallModeInterfaceEntry } from "$lib/api/types";

const { mockToggle, mockSearchWired, lacking } = vi.hoisted(() => ({
  mockToggle: vi.fn(),
  mockSearchWired: vi.fn(),
  // Central names that lack install_mode.local for a lasting reason.
  lacking: { local: [] as string[] },
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listInstallModeInterfaces: vi.fn().mockResolvedValue([]),
    setInstallModeInterface: vi.fn(),
    pairDeviceInstallMode: vi.fn(),
    searchWiredDevices: (...args: unknown[]) => mockSearchWired(...args),
  },
  ApiError: class ApiError extends Error {
    status = 0;
  },
}));

vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: {
    items: [],
    featureAvailable: () => true,
    centralsLacking: (key: string) =>
      key === "install_mode.local" ? lacking.local.map((name) => ({ name })) : [],
  },
}));

// The store is a plain object here so each case can choose the interfaces
// the component sees; the real one polls the daemon. Hoisted because the
// module mock below is evaluated before this file's own statements.
const store = vi.hoisted(() => ({
  active: false,
  remainingSeconds: null as number | null,
  interfaces: [] as InstallModeInterfaceEntry[],
  busy: false,
  banner: null as string | null,
  refresh: () => {},
  toggle: (...args: unknown[]) => mockToggle(...args),
  ensurePoll: () => {},
  release: () => {},
}));
vi.mock("$lib/stores/installMode.svelte", () => ({ installModeStore: store }));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// The real Select wraps bits-ui's floating-portal listbox, which happy-dom
// cannot drive (see SelectStub.svelte).
vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("../../../routes/__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

import PairingControls from "./PairingControls.svelte";

function iface(name: string, central = ""): InstallModeInterfaceEntry {
  return { interface: name, active: false, seconds: 0, observed: true, central };
}

beforeEach(() => {
  vi.clearAllMocks();
  store.interfaces = [];
  store.active = false;
  lacking.local = [];
  mockSearchWired.mockResolvedValue({ central: "", interface: "BidCos-Wired", found: 0 });
});

afterEach(() => cleanup());

describe("PairingControls — interface choice", () => {
  it("starts the install mode on the selected interface", async () => {
    store.interfaces = [iface("BidCos-RF"), iface("HmIP-RF")];
    render(PairingControls);

    // Pick the second radio so the call cannot pass by defaulting to the
    // first entry.
    await fireEvent.click(await screen.findByRole("option", { name: "HmIP-RF" }));
    await fireEvent.click(screen.getByText("add_device.start_pairing"));

    expect(mockToggle).toHaveBeenCalledWith({ interface: "HmIP-RF", central: "" });
  });

  it("hides the interface choice when there is only one", async () => {
    store.interfaces = [iface("HmIP-RF")];
    render(PairingControls);
    await screen.findByText("add_device.start_pairing");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("names the central when several centrals offer interfaces", async () => {
    store.interfaces = [iface("HmIP-RF", "ccu-a"), iface("HmIP-RF", "ccu-b")];
    render(PairingControls);
    expect(await screen.findByRole("option", { name: "HmIP-RF · ccu-a" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "HmIP-RF · ccu-b" })).toBeInTheDocument();
  });

  it("starts the install mode on the chosen central of two same-named interfaces", async () => {
    store.interfaces = [iface("HmIP-RF", "a"), iface("HmIP-RF", "b")];
    render(PairingControls);
    await fireEvent.click(await screen.findByRole("option", { name: "HmIP-RF · b" }));
    await fireEvent.click(screen.getByText("add_device.start_pairing"));
    expect(mockToggle).toHaveBeenCalledWith({ interface: "HmIP-RF", central: "b" });
  });
});

describe("PairingControls — HmIP local teach-in", () => {
  it("offers the SGTIN + key form on an HmIP interface", async () => {
    store.interfaces = [iface("HmIP-RF")];
    render(PairingControls);
    await waitFor(() =>
      expect(screen.getByText("inbox.install_mode_local_submit")).toBeInTheDocument(),
    );
  });

  // Negative control: the local teach-in is an HmIP mechanism and must not
  // be offered on a BidCos radio.
  it("does not offer the SGTIN + key form on a BidCos interface", async () => {
    store.interfaces = [iface("BidCos-RF")];
    render(PairingControls);
    await screen.findByText("add_device.start_pairing");
    expect(screen.queryByText("inbox.install_mode_local_submit")).toBeNull();
  });

  it("does not offer the SGTIN + key form when the central lacks it", async () => {
    lacking.local = ["lite"];
    store.interfaces = [iface("HmIP-RF", "lite")];
    render(PairingControls);
    await screen.findByText("add_device.start_pairing");
    expect(screen.queryByText("inbox.install_mode_local_submit")).toBeNull();
  });
});

describe("PairingControls — pairing by serial", () => {
  it("offers the serial form on a BidCos radio", async () => {
    store.interfaces = [iface("BidCos-RF")];
    render(PairingControls);
    await screen.findByText("add_device.start_pairing");
    expect(screen.getByText("inbox.pair_serial_submit")).toBeInTheDocument();
  });

  // The CCU has no address-targeted pairing on the HmIP radio; SGTIN + key
  // is the HmIP way and stays.
  it("withholds the serial form on the HmIP radio", async () => {
    store.interfaces = [iface("HmIP-RF")];
    render(PairingControls);
    await screen.findByText("add_device.start_pairing");
    expect(screen.queryByText("inbox.pair_serial_submit")).toBeNull();
    expect(screen.getByText("inbox.install_mode_local_submit")).toBeInTheDocument();
  });
});

describe("PairingControls — wired bus", () => {
  it("offers the bus search instead of a pairing window", async () => {
    store.interfaces = [iface("BidCos-Wired", "ccu")];
    render(PairingControls);
    await fireEvent.click(await screen.findByText("inbox.search_wired"));

    expect(screen.queryByText("add_device.start_pairing")).toBeNull();
    await waitFor(() => expect(mockSearchWired).toHaveBeenCalledWith("BidCos-Wired", "ccu"));
  });
});

describe("PairingControls — pairing tick", () => {
  it("asks the host to reload silently while the install mode runs", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      store.active = true;
      const onChange = vi.fn();
      render(PairingControls, { onChange });
      await vi.advanceTimersByTimeAsync(3100);
      expect(onChange).toHaveBeenCalledWith({ silent: true });
    } finally {
      vi.useRealTimers();
    }
  });
});
