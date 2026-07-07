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
  mockToastSuccess,
  mockToastError,
} = vi.hoisted(() => ({
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
  api: {
    getConfigSchema: (...args: unknown[]) => mockGetConfigSchema(...args),
    getEffectiveConfig: (...args: unknown[]) => mockGetEffectiveConfig(...args),
    getConfigChanges: (...args: unknown[]) => mockGetConfigChanges(...args),
    info: (...args: unknown[]) => mockInfo(...args),
    getStartupCapture: (...args: unknown[]) => mockGetStartupCapture(...args),
    reloadMQTT: (...args: unknown[]) => mockReloadMQTT(...args),
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
vi.mock("$lib/components/ui/ExpertGate.svelte", () => ({ default: () => {} }));

import Settings from "./Settings.svelte";

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
