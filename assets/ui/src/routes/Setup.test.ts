// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns — cleared in beforeEach
// ---------------------------------------------------------------------------

const mockSubmitSetup = vi.fn();
const mockComplete = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — must be declared before the component import so that
// vi.mock hoisting places them above any module initialisation.
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    submitSetup: (...args: unknown[]) => mockSubmitSetup(...args),
    listDiscoveredCentrals: vi.fn().mockResolvedValue([]),
  },
  ApiError: class ApiError extends Error {
    public readonly status: number;
    public readonly body: unknown;
    constructor(status: number, body: unknown, message: string) {
      super(message);
      this.status = status;
      this.body = body;
    }
  },
  friendlyError: (err: unknown, _t: unknown) =>
    err instanceof Error ? err.message : "error",
}));

vi.mock("$lib/stores/setup.svelte", () => ({
  setupStore: {
    get required() {
      return false;
    },
    get checking() {
      return false;
    },
    probe: vi.fn(),
    complete: (...args: unknown[]) => mockComplete(...args),
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  setLocale: vi.fn(),
  setTheme: vi.fn(),
}));

vi.mock("$lib/i18n", () => ({
  // Return the raw key so assertions can use stable, markup-independent
  // strings instead of locale-dependent translated text.
  t: (key: string, _params?: unknown) => key,
}));

// BrandMark renders an SVG logo; stub it out to avoid asset-import issues.
vi.mock("$lib/components/ui/BrandMark.svelte", () => ({
  default: vi.fn(),
}));

// ---------------------------------------------------------------------------
// Component under test — imported after mocks are registered
// ---------------------------------------------------------------------------

import Setup from "./Setup.svelte";

// ---------------------------------------------------------------------------
// Test lifecycle
// ---------------------------------------------------------------------------

beforeEach(() => {
  vi.clearAllMocks();
  // Default: submitSetup succeeds.
  mockSubmitSetup.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
});

// ---------------------------------------------------------------------------
// Helper — fills the admin step and advances to step 2.
// The button text is the i18n key because t() returns the key itself.
// ---------------------------------------------------------------------------

async function fillAndAdvanceAdminStep(): Promise<void> {
  await fireEvent.input(screen.getByLabelText("setup.username"), {
    target: { value: "admin" },
  });
  await fireEvent.input(screen.getByLabelText("setup.password"), {
    target: { value: "securepassword" },
  });
  await fireEvent.input(screen.getByLabelText("setup.confirm"), {
    target: { value: "securepassword" },
  });
  await fireEvent.click(screen.getByRole("button", { name: "setup.next" }));
}

// ---------------------------------------------------------------------------
// 1.  Step-1 admin validation
// ---------------------------------------------------------------------------

describe("Setup wizard — step 1 admin validation", () => {
  it("Next button is disabled when all fields are empty", () => {
    const { getByRole } = render(Setup);
    expect(getByRole("button", { name: "setup.next" })).toBeDisabled();
  });

  it("Next button is disabled when username is empty but passwords are set", async () => {
    const { getByRole, getByLabelText } = render(Setup);
    await fireEvent.input(getByLabelText("setup.password"), {
      target: { value: "securepassword" },
    });
    await fireEvent.input(getByLabelText("setup.confirm"), {
      target: { value: "securepassword" },
    });
    expect(getByRole("button", { name: "setup.next" })).toBeDisabled();
  });

  it("Next button is disabled when password is shorter than 8 characters", async () => {
    const { getByRole, getByLabelText } = render(Setup);
    await fireEvent.input(getByLabelText("setup.username"), {
      target: { value: "admin" },
    });
    await fireEvent.input(getByLabelText("setup.password"), {
      target: { value: "short" },
    });
    await fireEvent.input(getByLabelText("setup.confirm"), {
      target: { value: "short" },
    });
    expect(getByRole("button", { name: "setup.next" })).toBeDisabled();
  });

  it("Next button is disabled when password and confirm do not match", async () => {
    const { getByRole, getByLabelText } = render(Setup);
    await fireEvent.input(getByLabelText("setup.username"), {
      target: { value: "admin" },
    });
    await fireEvent.input(getByLabelText("setup.password"), {
      target: { value: "password123" },
    });
    await fireEvent.input(getByLabelText("setup.confirm"), {
      target: { value: "different!!" },
    });
    expect(getByRole("button", { name: "setup.next" })).toBeDisabled();
  });

  it("Next button is enabled once username is set, password ≥ 8 chars, and passwords match", async () => {
    const { getByRole, getByLabelText } = render(Setup);
    await fireEvent.input(getByLabelText("setup.username"), {
      target: { value: "admin" },
    });
    await fireEvent.input(getByLabelText("setup.password"), {
      target: { value: "securepassword" },
    });
    await fireEvent.input(getByLabelText("setup.confirm"), {
      target: { value: "securepassword" },
    });
    expect(getByRole("button", { name: "setup.next" })).not.toBeDisabled();
  });
});

// ---------------------------------------------------------------------------
// 2.  Full walkthrough: admin + locale only (CCU and MQTT disabled)
// ---------------------------------------------------------------------------

describe("Setup wizard — full walkthrough (CCU + MQTT disabled)", () => {
  it("calls api.submitSetup with admin+locale payload (no ccu/mqtt) and shows a success toast", async () => {
    const { getByRole } = render(Setup);

    // Step 1: admin
    await fillAndAdvanceAdminStep();

    // Step 2: locale — canAdvance is always true for step 2, just proceed
    await fireEvent.click(getByRole("button", { name: "setup.next" }));

    // Step 3: CCU — the switch starts checked (ccuEnabled=true).
    // Toggle it off so ccuEnabled becomes false → ccuValid=true → Next enabled.
    const ccuSwitch = getByRole("switch");
    await fireEvent.click(ccuSwitch);
    await fireEvent.click(getByRole("button", { name: "setup.next" }));

    // Step 4: MQTT — mqttEnabled starts false → mqttValid=true → Finish enabled
    await fireEvent.click(getByRole("button", { name: "setup.finish" }));

    await waitFor(() => {
      expect(mockSubmitSetup).toHaveBeenCalledOnce();
      expect(mockSubmitSetup).toHaveBeenCalledWith({
        admin: { username: "admin", password: "securepassword" },
        locale: { locale: "en", theme: "system" },
        // ccu and mqtt must be absent
      });
      expect(mockToastSuccess).toHaveBeenCalledOnce();
      expect(mockComplete).toHaveBeenCalledOnce();
    });
  });
});

// ---------------------------------------------------------------------------
// 3.  Error handling: api.submitSetup rejects
// ---------------------------------------------------------------------------

describe("Setup wizard — error handling", () => {
  it("shows an error toast and does not call complete() when api.submitSetup rejects", async () => {
    mockSubmitSetup.mockRejectedValue(new Error("network error"));

    const { getByRole } = render(Setup);

    // Navigate through all four steps (same path as the success test)
    await fillAndAdvanceAdminStep();
    await fireEvent.click(getByRole("button", { name: "setup.next" }));
    await fireEvent.click(getByRole("switch"));
    await fireEvent.click(getByRole("button", { name: "setup.next" }));
    await fireEvent.click(getByRole("button", { name: "setup.finish" }));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledOnce();
      expect(mockComplete).not.toHaveBeenCalled();
      // submitSetup was still called — the rejection is what triggers the error
      expect(mockSubmitSetup).toHaveBeenCalledOnce();
    });
  });
});
