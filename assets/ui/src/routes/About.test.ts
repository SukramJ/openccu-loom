// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockInfo = vi.fn();
const mockGetSystemCCUs = vi.fn();
const mockGetSystemUpdate = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component / the real
// infoStore. About.svelte drives the daemon-info card off the real
// `infoStore` singleton (not a mock) so the test exercises the actual
// refresh() wiring; only its dependency (`$lib/api/client`) is stubbed,
// mirroring how Fleet.test.ts stubs `deviceStore`'s own dependencies.
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    info: (...args: unknown[]) => mockInfo(...args),
    getSystemCCUs: (...args: unknown[]) => mockGetSystemCCUs(...args),
    getSystemUpdate: (...args: unknown[]) => mockGetSystemUpdate(...args),
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

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// ---------------------------------------------------------------------------
// Component + store under test
// ---------------------------------------------------------------------------

import About from "./About.svelte";

// ---------------------------------------------------------------------------
// Test data
// ---------------------------------------------------------------------------

type DaemonInfo = {
  version: string;
  commit: string;
  build_date: string;
  addon_build: boolean;
  uptime: string;
  started_at: string;
  api_version: string;
  capabilities: string[];
};

function daemonInfo(overrides: Partial<DaemonInfo> = {}): DaemonInfo {
  return {
    version: "1.2.3",
    commit: "abc1234",
    build_date: "2026-06-01T00:00:00Z",
    addon_build: false,
    uptime: "3h5m0s",
    started_at: "2026-06-01T00:00:00Z",
    api_version: "2.15.0",
    capabilities: ["rest.v1", "ws.broadcasts.v1"],
    ...overrides,
  };
}

const CCU = {
  name: "ccu-main",
  host: "172.18.4.29",
  available: true,
  model: "CCU3",
  version: "3.75.7",
  hostname: "ccu-main.local",
  serial: "SERIAL0042",
  url: "https://172.18.4.29",
  is_ha_app: false,
  configured_interfaces: ["HmIP-RF"],
};

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

// ---------------------------------------------------------------------------
// 1. Happy path
// ---------------------------------------------------------------------------

describe("About — happy path", () => {
  it("renders daemon info, capability badges, central details, and license links", async () => {
    mockInfo.mockResolvedValue(daemonInfo({ version: "1.2.3", commit: "abc1234" }));
    mockGetSystemCCUs.mockResolvedValue([CCU]);
    mockGetSystemUpdate.mockResolvedValue([
      { central: "ccu-main", available_firmware: "3.79.4", update_available: true, in_progress: false, observed: true },
    ]);

    render(About);

    await waitFor(() => {
      expect(screen.getByText("v1.2.3")).toBeInTheDocument();
    });

    // Commit is linked to the GitHub commit view.
    const commitLink = screen.getByRole("link", { name: "abc1234" });
    expect(commitLink).toHaveAttribute(
      "href",
      "https://github.com/SukramJ/openccu-loom/commit/abc1234",
    );

    // Capability badges.
    expect(screen.getByText("rest.v1")).toBeInTheDocument();
    expect(screen.getByText("ws.broadcasts.v1")).toBeInTheDocument();

    // Central card: model / firmware / serial.
    expect(screen.getByText("ccu-main")).toBeInTheDocument();
    expect(screen.getByText("CCU3")).toBeInTheDocument();
    expect(screen.getByText("3.75.7")).toBeInTheDocument();
    expect(screen.getByText("SERIAL0042")).toBeInTheDocument();

    // Update badge, since ccu-main has update_available=true.
    expect(screen.getByText("about.centrals.update_available")).toBeInTheDocument();

    // License & links card.
    const githubLink = screen.getByRole("link", { name: "about.links.github" });
    expect(githubLink).toHaveAttribute("href", "https://github.com/SukramJ/openccu-loom");
    const releasesLink = screen.getByRole("link", { name: "about.links.releases" });
    expect(releasesLink).toHaveAttribute(
      "href",
      "https://github.com/SukramJ/openccu-loom/releases",
    );
    const noticesLink = screen.getByRole("link", { name: "about.links.notices" });
    expect(noticesLink).toHaveAttribute(
      "href",
      "https://github.com/SukramJ/openccu-loom/blob/main/THIRD-PARTY-NOTICES.md",
    );
    const docsLink = screen.getByRole("link", { name: "about.links.docs" });
    expect(docsLink).toHaveAttribute(
      "href",
      "https://github.com/SukramJ/openccu-loom/blob/main/docs/user-guide.md",
    );
  });
});

// ---------------------------------------------------------------------------
// 2. Build-variant text
// ---------------------------------------------------------------------------

describe("About — build variant", () => {
  it("shows the CCU/RaspberryMatic add-on variant when addon_build is true", async () => {
    mockInfo.mockResolvedValue(daemonInfo({ version: "9.9.1", addon_build: true }));
    mockGetSystemCCUs.mockResolvedValue([]);
    mockGetSystemUpdate.mockResolvedValue([]);

    render(About);

    await waitFor(() => {
      expect(screen.getByText("v9.9.1")).toBeInTheDocument();
    });
    expect(screen.getByText("about.runtime.addon")).toBeInTheDocument();
    expect(screen.queryByText("about.runtime.standalone")).toBeNull();
  });

  it("shows the standalone variant when addon_build is false", async () => {
    mockInfo.mockResolvedValue(daemonInfo({ version: "9.9.2", addon_build: false }));
    mockGetSystemCCUs.mockResolvedValue([]);
    mockGetSystemUpdate.mockResolvedValue([]);

    render(About);

    await waitFor(() => {
      expect(screen.getByText("v9.9.2")).toBeInTheDocument();
    });
    expect(screen.getByText("about.runtime.standalone")).toBeInTheDocument();
    expect(screen.queryByText("about.runtime.addon")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// 3. Best-effort system-update fetch
// ---------------------------------------------------------------------------

describe("About — system update is best-effort", () => {
  it("still renders the central card when getSystemUpdate rejects, with no update badge", async () => {
    mockInfo.mockResolvedValue(daemonInfo({ version: "4.0.0" }));
    mockGetSystemCCUs.mockResolvedValue([CCU]);
    mockGetSystemUpdate.mockRejectedValue(new Error("forbidden"));

    render(About);

    await waitFor(() => {
      expect(screen.getByText("v4.0.0")).toBeInTheDocument();
    });
    expect(screen.getByText("ccu-main")).toBeInTheDocument();
    expect(screen.getByText("CCU3")).toBeInTheDocument();
    expect(screen.queryByText("about.centrals.update_available")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// 4. Info load failure
// ---------------------------------------------------------------------------

describe("About — info load failure", () => {
  it("shows the shared ErrorState when api.info() rejects", async () => {
    mockInfo.mockRejectedValue(new Error("network down"));
    mockGetSystemCCUs.mockResolvedValue([]);
    mockGetSystemUpdate.mockResolvedValue([]);

    render(About);

    await waitFor(() => {
      expect(screen.getByText(/about\.load_error/)).toBeInTheDocument();
    });
    expect(screen.getByText(/common\.error/)).toBeInTheDocument();
  });
});
