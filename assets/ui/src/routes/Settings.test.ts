// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const {
  mockGetConfigSchema,
  mockGetEffectiveConfig,
  mockGetConfigChanges,
  mockInfo,
  mockGetStartupCapture,
  mockReloadMQTT,
  mockGetSystemCCUs,
  mockToastSuccess,
  mockToastError,
} = vi.hoisted(() => ({
  mockGetSystemCCUs: vi.fn(),
  mockGetConfigSchema: vi.fn(),
  mockGetEffectiveConfig: vi.fn(),
  mockGetConfigChanges: vi.fn(),
  mockInfo: vi.fn(),
  mockGetStartupCapture: vi.fn(),
  mockReloadMQTT: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  setUnauthorizedHandler: vi.fn(),
  api: {
    getConfigSchema: (...args: unknown[]) => mockGetConfigSchema(...args),
    getEffectiveConfig: (...args: unknown[]) => mockGetEffectiveConfig(...args),
    getConfigChanges: (...args: unknown[]) => mockGetConfigChanges(...args),
    info: (...args: unknown[]) => mockInfo(...args),
    getStartupCapture: (...args: unknown[]) => mockGetStartupCapture(...args),
    reloadMQTT: (...args: unknown[]) => mockReloadMQTT(...args),
    getSystemCCUs: (...args: unknown[]) => mockGetSystemCCUs(...args),
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
  t: (key: string, params?: Record<string, string>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: mockToastSuccess, error: mockToastError },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

// Settings.svelte pulls in a long tail of settings-panel components that
// are irrelevant to the two behaviours under test (toast feedback + the
// shared ErrorState). Stub them out to keep the render surface small.
// vi.mock() factories are hoisted above imports, so each target needs its
// own literal call rather than a loop over a path list.
vi.mock("$lib/components/settings/SectionEditor.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/UsersAdmin.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/TokensAdmin.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/ChangePasswordCard.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/CentralsAdmin.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/RoomsFunctionsAdmin.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/TlsCertCard.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/SystemUpdatePanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/ChangesOverview.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/ConnectivityLights.svelte", () => ({ default: () => {} }));
// ExpertGate stays real: it renders nothing while expert mode is off, which
// is the default, and the callback-tab cases below switch expert mode on.

import Settings from "./Settings.svelte";
import { centralStore } from "$lib/stores/centrals.svelte";
import { setExpertMode } from "$lib/stores/preferences.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  mockGetConfigSchema.mockResolvedValue({ sections: [], fields: [] });
  mockGetEffectiveConfig.mockResolvedValue({ config: {}, sources: {} });
  mockGetConfigChanges.mockResolvedValue({ fields: [] });
  mockInfo.mockResolvedValue({ capabilities: [] });
  mockGetStartupCapture.mockResolvedValue({
    enabled: false,
    duration_seconds: 600,
    anonymise: true,
  });
});

afterEach(() => {
  cleanup();
});

// The bare `#/settings` names the General tab, so the `tab` prop going
// from a value back to undefined is a navigation the view has to follow.
// Ignoring that transition left the previous panel on screen while the
// address bar already read `#/settings` — a link shared from that state
// opens a different view than the sender was looking at.
describe("Settings — a hash without ?tab lands on General", () => {
  it("switches back to General when the tab prop drops to undefined", async () => {
    location.hash = "#/settings?tab=mqtt";
    const { rerender } = render(Settings, { props: { tab: "mqtt" } });

    await waitFor(() =>
      expect(screen.getByText("settings.mqtt.reload_title")).toBeInTheDocument(),
    );

    await rerender({ tab: undefined });

    await waitFor(() =>
      expect(screen.getByText("settings.interface")).toBeInTheDocument(),
    );
    expect(screen.queryByText("settings.mqtt.reload_title")).toBeNull();
    expect(location.hash).toBe("#/settings");
  });

  it("still follows a deep link into a tab", async () => {
    location.hash = "#/settings";
    const { rerender } = render(Settings, { props: { tab: undefined } });

    await waitFor(() =>
      expect(screen.getByText("settings.interface")).toBeInTheDocument(),
    );

    await rerender({ tab: "mqtt" });

    await waitFor(() =>
      expect(screen.getByText("settings.mqtt.reload_title")).toBeInTheDocument(),
    );
    expect(location.hash).toBe("#/settings?tab=mqtt");
  });
});

describe("Settings — schema load failure uses the shared ErrorState", () => {
  it("renders ErrorState with the error message and no ad-hoc Card banner", async () => {
    mockGetConfigSchema.mockRejectedValue(new Error("boom"));
    const { container } = render(Settings);

    await waitFor(() => {
      expect(screen.getByText(/boom/)).toBeInTheDocument();
    });

    // The shared ErrorState renders an alert icon + "common.error" prefix +
    // a retry button labelled "common.reload" — not a bare red-text <p>.
    expect(screen.getByText(/common\.error/)).toBeInTheDocument();
    expect(container.querySelector(".lucide-circle-alert")).toBeTruthy();
  });

  it("retrying calls loadSchema again via the ErrorState onRetry action", async () => {
    mockGetConfigSchema.mockRejectedValueOnce(new Error("boom"));
    render(Settings);

    await waitFor(() => expect(screen.getByText(/boom/)).toBeInTheDocument());
    expect(mockGetConfigSchema).toHaveBeenCalledTimes(1);

    mockGetConfigSchema.mockResolvedValueOnce({ sections: [], fields: [] });
    const retryButtons = screen.getAllByText("common.reload");
    // Two reload buttons exist (header + ErrorState); click the last one,
    // which belongs to the ErrorState renders after the header.
    retryButtons[retryButtons.length - 1].dispatchEvent(
      new MouseEvent("click", { bubbles: true }),
    );

    await waitFor(() => expect(mockGetConfigSchema).toHaveBeenCalledTimes(2));
  });
});

// GET /config/effective is admin-gated (the assembled config names every CCU
// host, the broker URL and the OIDC issuer) while GET /config/schema is not.
// The Settings entry itself is open to every identity, so a viewer must get
// the schema-only read-only view — an ErrorState over an empty page would
// make the whole view look broken for everyone but the admin.
describe("Settings — a forbidden effective-config read degrades, not fails", () => {
  it("renders the page with a note instead of the ErrorState on 403", async () => {
    // The mocked ApiError class is what `instanceof` checks against, so build
    // the rejection from it rather than from a plain Error.
    const { ApiError } = await import("$lib/api/client");
    mockGetEffectiveConfig.mockRejectedValue(new ApiError(403, null, "forbidden"));
    mockGetConfigSchema.mockResolvedValue({
      sections: ["north.mqtt"],
      fields: [{ path: "north.mqtt.enabled", class: "basic", go_type: "bool" }],
    });

    render(Settings);

    await waitFor(() => expect(mockGetEffectiveConfig).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByText("settings.values_admin_only")).toBeInTheDocument(),
    );

    // No error banner, and the page itself still renders its tabs.
    expect(screen.queryByText(/common\.error/)).toBeNull();
    expect(screen.getByText("settings.interface")).toBeInTheDocument();
  });

  it("still surfaces a non-403 failure as the ErrorState", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockGetEffectiveConfig.mockRejectedValue(new ApiError(500, null, "config store down"));

    render(Settings);

    await waitFor(() =>
      expect(screen.getByText(/config store down/)).toBeInTheDocument(),
    );
    expect(screen.queryByText("settings.values_admin_only")).toBeNull();
  });
});

describe("Settings — MQTT reload uses toastStore, not an inline banner", () => {
  it("shows a success toast and no inline banner span on success", async () => {
    mockReloadMQTT.mockResolvedValue({ reloaded: true, took_ms: 42 });
    render(Settings);

    await waitFor(() => expect(mockGetConfigSchema).toHaveBeenCalled());

    // Navigate to the "mqtt" tab.
    const mqttTabs = screen.getAllByText("settings.tab.mqtt");
    mqttTabs[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));

    await waitFor(() => {
      expect(screen.getByText("settings.mqtt.reload")).toBeInTheDocument();
    });
    screen.getByText("settings.mqtt.reload").dispatchEvent(
      new MouseEvent("click", { bubbles: true }),
    );

    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledTimes(1));
    expect(mockToastSuccess.mock.calls[0][0]).toContain("settings.mqtt.reload_success");
    expect(mockToastError).not.toHaveBeenCalled();
  });

  it("shows an error toast on failure", async () => {
    mockReloadMQTT.mockRejectedValue(new Error("mqtt down"));
    render(Settings);

    await waitFor(() => expect(mockGetConfigSchema).toHaveBeenCalled());

    const mqttTabs = screen.getAllByText("settings.tab.mqtt");
    mqttTabs[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));

    await waitFor(() => {
      expect(screen.getByText("settings.mqtt.reload")).toBeInTheDocument();
    });
    screen.getByText("settings.mqtt.reload").dispatchEvent(
      new MouseEvent("click", { bubbles: true }),
    );

    await waitFor(() => expect(mockToastError).toHaveBeenCalledTimes(1));
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});

// The callback ports are global, but an openccu-lite central never uses
// them. The tab stays visible for a mixed fleet and says whom it reaches.
describe("Settings — callback tab scope note for openccu-lite centrals", () => {
  async function openCallbackTab(fleet: { name: string; system_type: string }[]) {
    mockGetSystemCCUs.mockResolvedValue(fleet);
    await centralStore.refresh();
    setExpertMode(true);
    render(Settings, { props: { tab: "callback" } });
    await waitFor(() => expect(mockGetConfigSchema).toHaveBeenCalled());
  }

  // The central store and the preferences are module singletons; reset both
  // so the fleet and expert mode set here do not leak into other cases.
  afterEach(async () => {
    setExpertMode(false);
    mockGetSystemCCUs.mockResolvedValue([]);
    await centralStore.refresh();
  });

  it("says no configured system uses the ports when every central is openccu-lite", async () => {
    await openCallbackTab([{ name: "box", system_type: "openccu-lite" }]);
    await waitFor(() =>
      expect(screen.getByText("settings.callback.lite_only_hint")).toBeInTheDocument(),
    );
    expect(screen.queryByText(/settings\.callback\.lite_mixed_hint/)).toBeNull();
  });

  it("names the openccu-lite centrals in a mixed fleet", async () => {
    await openCallbackTab([
      { name: "ccu1", system_type: "ccu" },
      { name: "box", system_type: "openccu-lite" },
    ]);
    await waitFor(() =>
      expect(
        screen.getByText('settings.callback.lite_mixed_hint:{"centrals":"box"}'),
      ).toBeInTheDocument(),
    );
    expect(screen.queryByText("settings.callback.lite_only_hint")).toBeNull();
  });

  it("shows no note for a CCU-only fleet", async () => {
    await openCallbackTab([{ name: "ccu1", system_type: "ccu" }]);
    await waitFor(() => expect(mockGetConfigSchema).toHaveBeenCalled());
    // Give the tab a render pass after the schema load settles.
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByText(/settings\.callback\.lite_/)).toBeNull();
  });
});
