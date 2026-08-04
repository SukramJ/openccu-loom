// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Module-level mock state — populated per test in beforeEach
// ---------------------------------------------------------------------------

let mockGetConfigSectionResult: Record<string, unknown> = {};
const mockPutConfigSection = vi.fn();
const mockGetConfigSection = vi.fn();
const mockToastError = vi.fn();
const mockToastSuccess = vi.fn();

// ---------------------------------------------------------------------------
// Mocks — registered before any component import
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    getConfigSection: (...args: unknown[]) => mockGetConfigSection(...args),
    putConfigSection: (...args: unknown[]) => mockPutConfigSection(...args),
    getRestartPending: vi.fn().mockResolvedValue({ pending: false, fields: [] }),
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
    error: (...args: unknown[]) => mockToastError(...args),
    success: (...args: unknown[]) => mockToastSuccess(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

// A plain mutable object (not a Svelte $state proxy) so individual tests
// can flip prefs.expertMode before render() to exercise the expert-only
// go_type badge. vi.mock factories are hoisted above top-level const
// declarations, so this must go through vi.hoisted() to be visible inside
// the factory below.
const { mockPrefs } = vi.hoisted(() => ({ mockPrefs: { expertMode: false } }));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: mockPrefs,
  applyTheme: vi.fn(),
  setLocale: vi.fn(),
  setTheme: vi.fn(),
  setNavCollapsed: vi.fn(),
  setExpertMode: vi.fn(),
  setDeviceView: vi.fn(),
  bindSystemTheme: vi.fn(() => () => {}),
}));

vi.mock("$lib/stores/restartPending.svelte", () => ({
  restartPending: { pending: false, fields: [] },
  restartCaps: { supervised: false, loaded: false },
  refreshRestartPending: vi.fn().mockResolvedValue(undefined),
  loadRestartCaps: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, _params?: unknown) => key,
}));

// ---------------------------------------------------------------------------
// Delayed component import so mocks are hoisted first
// ---------------------------------------------------------------------------

import type { ConfigSchemaField } from "$lib/api/client";
import SectionEditor from "./SectionEditor.svelte";

// ---------------------------------------------------------------------------
// Schema fixtures
// ---------------------------------------------------------------------------

const PUBLIC_URL_FIELD: ConfigSchemaField = {
  path: "north.rest.public_url",
  class: "basic",
  go_type: "string",
};

const AUTH_USERS_FIELD: ConfigSchemaField = {
  path: "north.rest.auth.users",
  class: "secret",
  go_type: "map[string]string",
};

const AUTH_TOKENS_FIELD: ConfigSchemaField = {
  path: "north.rest.auth.tokens",
  class: "secret",
  go_type: "map[string]string",
};

// Non-secret complex field for the invalid-JSON test.
const EXTRA_MAP_FIELD: ConfigSchemaField = {
  path: "north.rest.extra_map",
  class: "basic",
  go_type: "map[string]string",
};

// ---------------------------------------------------------------------------
// Helper: render SectionEditor for "north.rest"
// ---------------------------------------------------------------------------

function renderEditor(fields: ConfigSchemaField[]) {
  return render(SectionEditor, {
    props: {
      section: "north.rest",
      schemaFields: fields,
      sources: {} as Record<string, "bootstrap" | "db" | "env" | "default">,
      allSections: ["north.rest"],
    },
  });
}

// ---------------------------------------------------------------------------
// Test lifecycle
// ---------------------------------------------------------------------------

beforeEach(() => {
  vi.clearAllMocks();
  mockPrefs.expertMode = false;
  mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);
  mockPutConfigSection.mockResolvedValue({
    section: "north.rest",
    version: 1,
    updated_at: "",
    restart_required: false,
  });
});

afterEach(() => {
  cleanup();
});

// ---------------------------------------------------------------------------
// Test 1 — Regression: masked secret must NOT abort save()
//
// Before the fix: save() ran JSON.parse("***") on the secret-class
// map[string]string field, threw, and silently returned without calling
// putConfigSection. After the fix secret fields are skipped entirely.
// ---------------------------------------------------------------------------

describe("SectionEditor.save — regression: masked secret field", () => {
  it("calls putConfigSection even when a secret field carries the masked '***' sentinel", async () => {
    mockGetConfigSectionResult = {
      public_url: "https://old.example.com",
      auth: {
        users: "***",
        tokens: "***",
      },
    };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container } = renderEditor([
      PUBLIC_URL_FIELD,
      AUTH_USERS_FIELD,
      AUTH_TOKENS_FIELD,
    ]);

    // Wait for onMount to finish (getConfigSection resolves).
    await waitFor(() => {
      expect(mockGetConfigSection).toHaveBeenCalledWith("north.rest");
    });

    // Wait for the text input (public_url) to appear.
    await waitFor(() => {
      const inputs = container.querySelectorAll('input[type="text"]');
      expect(inputs.length).toBeGreaterThan(0);
    });

    // Change the public_url value so the form becomes dirty and Save enables.
    const publicUrlInput = container.querySelector(
      'input[type="text"]',
    ) as HTMLInputElement;
    await fireEvent.input(publicUrlInput, {
      target: { value: "https://new.example.com" },
    });

    // Wait for the Save button to become enabled (isDirty = true).
    await waitFor(() => {
      const buttons = Array.from(
        container.querySelectorAll("button"),
      ) as HTMLButtonElement[];
      const saveBtn = buttons.find((b) =>
        b.textContent?.trim().includes("common.save"),
      );
      expect(saveBtn).toBeDefined();
      expect(saveBtn!.disabled).toBe(false);
    });

    const saveBtn = (
      Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
    ).find((b) => b.textContent?.trim().includes("common.save"))!;

    await fireEvent.click(saveBtn);

    // Core regression assertion: putConfigSection MUST be called.
    // Before the fix JSON.parse("***") aborted save() silently.
    await waitFor(() => {
      expect(mockPutConfigSection).toHaveBeenCalledTimes(1);
    });

    // The PUT must have been called for the right section.
    expect(mockPutConfigSection.mock.calls[0][0]).toBe("north.rest");
  });
});

// ---------------------------------------------------------------------------
// Test 2 — Invalid JSON in a non-secret complex field is surfaced
//
// A basic-class map[string]string field that the user types broken JSON
// into must NOT reach putConfigSection. Either save() calls toastStore.error
// OR the Save button is disabled (both are correct post-fix behaviour).
// ---------------------------------------------------------------------------

describe("SectionEditor.save — invalid JSON in a non-secret complex field", () => {
  it("blocks the PUT and surfaces an error when a basic complex field has bad JSON", async () => {
    mockGetConfigSectionResult = {
      public_url: "https://example.com",
      extra_map: null,
    };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container } = renderEditor([PUBLIC_URL_FIELD, EXTRA_MAP_FIELD]);

    await waitFor(() => {
      expect(mockGetConfigSection).toHaveBeenCalledWith("north.rest");
    });

    // The complex map field renders as a <textarea>.
    await waitFor(() => {
      const textareas = container.querySelectorAll("textarea");
      expect(textareas.length).toBeGreaterThan(0);
    });

    const textarea = container.querySelector(
      "textarea",
    ) as HTMLTextAreaElement;

    // Type invalid JSON.
    await fireEvent.input(textarea, {
      target: { value: "not valid json {{" },
    });

    // Also change the string field so isDirty is true (Save could enable).
    const textInput = container.querySelector(
      'input[type="text"]',
    ) as HTMLInputElement;
    if (textInput) {
      await fireEvent.input(textInput, {
        target: { value: "https://changed.example.com" },
      });
    }

    // Give Svelte reactivity a tick to settle.
    await waitFor(() => {
      // Either the Save button is now disabled (jsonErrors blocks it)
      // or it is still enabled — we will handle both branches below.
      const buttons = container.querySelectorAll("button");
      expect(buttons.length).toBeGreaterThan(0);
    });

    const saveBtn = (
      Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
    ).find((b) => b.textContent?.trim().includes("common.save"));

    if (!saveBtn || saveBtn.disabled) {
      // Correct behaviour path A: Save is disabled because jsonErrors
      // blocks submission. Verify no PUT went out.
      expect(mockPutConfigSection).not.toHaveBeenCalled();

      // Inline error indicator must be visible.
      const errorEl = container.querySelector(
        ".text-red-600, .text-red-400",
      );
      expect(errorEl).not.toBeNull();
    } else {
      // Correct behaviour path B: Save is clickable but save() bails
      // after pre-validation and calls toastStore.error instead.
      await fireEvent.click(saveBtn);

      await waitFor(() => {
        expect(mockToastError).toHaveBeenCalled();
      });
      expect(mockPutConfigSection).not.toHaveBeenCalled();
    }
  });
});

// ---------------------------------------------------------------------------
// Test 3 — Tri-state *bool field round-trips
//
// Go *bool fields are tri-state (nil/true/false). The SectionEditor must:
//   - treat an ABSENT key in the server payload as null ("Default"), NOT false
//   - persist null when the operator has not changed the *bool field
//   - persist true/false unchanged when the field was stored with a value
//
// The tri-state widget is a Select (trigger button), never a checkbox.
// ---------------------------------------------------------------------------

const CSRF_ENABLED_FIELD: ConfigSchemaField = {
  path: "north.rest.csrf_enabled",
  class: "basic",
  go_type: "*bool",
};

describe("SectionEditor.save — tri-state *bool field", () => {
  it("persists an unset *bool as null, not false", async () => {
    // Payload has public_url but NO csrf_enabled key — *bool is unset/nil.
    mockGetConfigSectionResult = {
      public_url: "https://example.com",
    };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container } = renderEditor([PUBLIC_URL_FIELD, CSRF_ENABLED_FIELD]);

    // Wait for onMount / getConfigSection to complete.
    await waitFor(() => {
      expect(mockGetConfigSection).toHaveBeenCalledWith("north.rest");
    });

    // Wait for the text input (public_url) to be rendered.
    await waitFor(() => {
      const inputs = container.querySelectorAll('input[type="text"]');
      expect(inputs.length).toBeGreaterThan(0);
    });

    // Dirty the form by changing public_url.
    const publicUrlInput = container.querySelector(
      'input[type="text"]',
    ) as HTMLInputElement;
    await fireEvent.input(publicUrlInput, {
      target: { value: "https://changed.example.com" },
    });

    // Wait for Save button to become enabled.
    await waitFor(() => {
      const saveBtn = (
        Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
      ).find((b) => b.textContent?.trim().includes("common.save"));
      expect(saveBtn).toBeDefined();
      expect(saveBtn!.disabled).toBe(false);
    });

    const saveBtn = (
      Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
    ).find((b) => b.textContent?.trim().includes("common.save"))!;

    await fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(mockPutConfigSection).toHaveBeenCalledTimes(1);
    });

    // The PUT section must be "north.rest".
    expect(mockPutConfigSection.mock.calls[0][0]).toBe("north.rest");

    // Core assertion: the unset *bool must arrive as null, not false.
    // parseValue() maps an absent key to null for *bool fields.
    const payload = mockPutConfigSection.mock.calls[0][1] as Record<string, unknown>;
    expect(payload.csrf_enabled).toBeNull();
  });

  it("round-trips a stored true unchanged", async () => {
    // Payload has csrf_enabled explicitly set to true.
    mockGetConfigSectionResult = {
      public_url: "https://example.com",
      csrf_enabled: true,
    };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container } = renderEditor([PUBLIC_URL_FIELD, CSRF_ENABLED_FIELD]);

    await waitFor(() => {
      expect(mockGetConfigSection).toHaveBeenCalledWith("north.rest");
    });

    await waitFor(() => {
      const inputs = container.querySelectorAll('input[type="text"]');
      expect(inputs.length).toBeGreaterThan(0);
    });

    // Dirty the form by changing public_url.
    const publicUrlInput = container.querySelector(
      'input[type="text"]',
    ) as HTMLInputElement;
    await fireEvent.input(publicUrlInput, {
      target: { value: "https://changed.example.com" },
    });

    await waitFor(() => {
      const saveBtn = (
        Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
      ).find((b) => b.textContent?.trim().includes("common.save"));
      expect(saveBtn).toBeDefined();
      expect(saveBtn!.disabled).toBe(false);
    });

    const saveBtn = (
      Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
    ).find((b) => b.textContent?.trim().includes("common.save"))!;

    await fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(mockPutConfigSection).toHaveBeenCalledTimes(1);
    });

    expect(mockPutConfigSection.mock.calls[0][0]).toBe("north.rest");

    // Core assertion: stored true must round-trip as true, not as null or false.
    const payload = mockPutConfigSection.mock.calls[0][1] as Record<string, unknown>;
    expect(payload.csrf_enabled).toBe(true);
  });

  it("renders a *bool as a select trigger, not a checkbox", async () => {
    mockGetConfigSectionResult = {
      public_url: "https://example.com",
    };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container } = renderEditor([PUBLIC_URL_FIELD, CSRF_ENABLED_FIELD]);

    await waitFor(() => {
      expect(mockGetConfigSection).toHaveBeenCalledWith("north.rest");
    });

    // Wait for loading to finish (inputs should appear).
    await waitFor(() => {
      const inputs = container.querySelectorAll('input[type="text"]');
      expect(inputs.length).toBeGreaterThan(0);
    });

    // The *bool field must NOT render as a plain checkbox.
    const checkboxes = container.querySelectorAll('input[type="checkbox"]');
    expect(checkboxes).toHaveLength(0);

    // The tri-state Select renders a trigger button containing the
    // current option label. With the t() mock returning the key
    // verbatim the default option label is "settings.tristate.default".
    const buttons = Array.from(container.querySelectorAll("button")) as HTMLButtonElement[];
    const triggerBtn = buttons.find((b) =>
      b.textContent?.includes("settings.tristate.default"),
    );
    expect(triggerBtn).toBeDefined();
  });
});

// ---------------------------------------------------------------------------
// Test 4 — `bool` field renders as the design-system Switch
//
// Review finding: bool fields used to render as a native <input
// type="checkbox">, inconsistent with the Switch used by the channel
// configurator. They must now render as the shared Switch (a
// role="switch" element, bits-ui), stay label-associated via id/for,
// and route toggles through the same setIn() update path a checkbox
// onchange used to.
// ---------------------------------------------------------------------------

const ENABLED_FIELD: ConfigSchemaField = {
  path: "north.rest.enabled",
  class: "basic",
  go_type: "bool",
};

const OIDC_CLIENT_SECRET_FIELD: ConfigSchemaField = {
  path: "north.rest.auth.oidc.client_secret",
  class: "secret",
  go_type: "string",
};

describe("SectionEditor — bool field renders as a Switch", () => {
  it("renders a role=switch element instead of a checkbox", async () => {
    mockGetConfigSectionResult = { enabled: true };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container, getByRole } = renderEditor([ENABLED_FIELD]);

    await waitFor(() => {
      expect(container.querySelector('[role="switch"]')).not.toBeNull();
    });

    expect(container.querySelectorAll('input[type="checkbox"]')).toHaveLength(0);
    expect(getByRole("switch")).toBeTruthy();
  });

  it("is reachable via its associated label (id/for kept)", async () => {
    mockGetConfigSectionResult = { enabled: true };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { getByLabelText } = renderEditor([ENABLED_FIELD]);

    // humanize("enabled") -> "Enabled" (the i18n mock returns the raw
    // key, so fieldLabel() falls through to the humanised tail key).
    await waitFor(() => {
      expect(getByLabelText("Enabled")).toBeTruthy();
    });
  });

  it("toggling the switch dirties the form and persists the new value on save", async () => {
    mockGetConfigSectionResult = { enabled: false };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container, getByRole } = renderEditor([ENABLED_FIELD]);

    await waitFor(() => {
      expect(container.querySelector('[role="switch"]')).not.toBeNull();
    });

    const toggle = getByRole("switch");
    expect(toggle.getAttribute("aria-checked")).toBe("false");

    await fireEvent.click(toggle);

    await waitFor(() => {
      const saveBtn = (
        Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
      ).find((b) => b.textContent?.trim().includes("common.save"));
      expect(saveBtn).toBeDefined();
      expect(saveBtn!.disabled).toBe(false);
    });

    const saveBtn = (
      Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
    ).find((b) => b.textContent?.trim().includes("common.save"))!;
    await fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(mockPutConfigSection).toHaveBeenCalledTimes(1);
    });

    const payload = mockPutConfigSection.mock.calls[0][1] as Record<string, unknown>;
    expect(payload.enabled).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// Test 5 — go_type badge is expert-mode-only
//
// Review finding: the font-mono go_type span (e.g. "bool", "string") is
// developer-facing jargon and must only render when prefs.expertMode is
// on; basic-mode operators must not see it.
// ---------------------------------------------------------------------------

describe("SectionEditor — go_type badge visibility", () => {
  it("hides the go_type badge in basic mode", async () => {
    mockPrefs.expertMode = false;
    mockGetConfigSectionResult = { public_url: "https://example.com" };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container, queryByText } = renderEditor([PUBLIC_URL_FIELD]);

    await waitFor(() => {
      expect(container.querySelectorAll('input[type="text"]').length).toBeGreaterThan(0);
    });

    expect(queryByText("string")).toBeNull();
  });

  it("shows the go_type badge in expert mode", async () => {
    mockPrefs.expertMode = true;
    mockGetConfigSectionResult = { public_url: "https://example.com" };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container, queryByText } = renderEditor([PUBLIC_URL_FIELD]);

    await waitFor(() => {
      expect(container.querySelectorAll('input[type="text"]').length).toBeGreaterThan(0);
    });

    expect(queryByText("string")).not.toBeNull();
  });

  it("hides the go_type badge for a secret field in basic mode", async () => {
    mockPrefs.expertMode = false;
    // AUTH_USERS_FIELD/AUTH_TOKENS_FIELD are excluded from SectionEditor's
    // rendering (MANAGED_ELSEWHERE — they have dedicated admin surfaces),
    // so use a secret field that actually renders here: the OIDC client
    // secret, which shares north.rest.auth.oidc.client_secret with
    // secretEnvName()'s known env-override mapping.
    mockGetConfigSectionResult = { auth: { oidc: { client_secret: "***" } } };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const { container, queryByText } = renderEditor([OIDC_CLIENT_SECRET_FIELD]);

    await waitFor(() => {
      expect(container.querySelector('input[type="password"]')).not.toBeNull();
    });

    expect(queryByText("string")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Secret payload contract
//
// The backend reads an ABSENT secret key as "unchanged, keep the stored
// value". Sending a placeholder instead is what silently wiped credentials:
// editing an unrelated field of the same section persisted an empty secret,
// after which the daemon connected to the broker without a password.
// An edited secret is sent as typed — including "" to clear it.
// ---------------------------------------------------------------------------

const MQTT_PASSWORD_FIELD: ConfigSchemaField = {
  path: "north.rest.password",
  class: "secret",
  go_type: "string",
};

async function renderAndSave(
  fields: ConfigSchemaField[],
  edit: (container: HTMLElement) => Promise<void>,
): Promise<Record<string, unknown>> {
  const { container } = renderEditor(fields);
  await waitFor(() => {
    expect(mockGetConfigSection).toHaveBeenCalledWith("north.rest");
  });
  await waitFor(() => {
    expect(container.querySelectorAll("input").length).toBeGreaterThan(0);
  });

  await edit(container);

  await waitFor(() => {
    const saveBtn = (
      Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
    ).find((b) => b.textContent?.trim().includes("common.save"));
    expect(saveBtn).toBeDefined();
    expect(saveBtn!.disabled).toBe(false);
  });
  const saveBtn = (
    Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
  ).find((b) => b.textContent?.trim().includes("common.save"))!;
  await fireEvent.click(saveBtn);

  await waitFor(() => {
    expect(mockPutConfigSection).toHaveBeenCalledTimes(1);
  });
  return mockPutConfigSection.mock.calls[0][1] as Record<string, unknown>;
}

describe("SectionEditor.save — secret payload contract", () => {
  it("omits an untouched secret so the stored credential survives the save", async () => {
    mockGetConfigSectionResult = {
      public_url: "https://old.example.com",
      password: "***",
    };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const payload = await renderAndSave(
      [PUBLIC_URL_FIELD, MQTT_PASSWORD_FIELD],
      async (container) => {
        const urlInput = container.querySelector(
          'input[type="text"]',
        ) as HTMLInputElement;
        await fireEvent.input(urlInput, {
          target: { value: "https://new.example.com" },
        });
      },
    );

    expect(payload.public_url).toBe("https://new.example.com");
    // Core assertion: the key must be absent, not null and not "***".
    expect("password" in payload).toBe(false);
  });

  it("sends a newly typed secret verbatim", async () => {
    mockGetConfigSectionResult = { public_url: "https://old.example.com", password: "***" };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const payload = await renderAndSave(
      [PUBLIC_URL_FIELD, MQTT_PASSWORD_FIELD],
      async (container) => {
        const pw = container.querySelector(
          'input[type="password"]',
        ) as HTMLInputElement;
        await fireEvent.input(pw, { target: { value: "brand-new-secret" } });
      },
    );

    expect(payload.password).toBe("brand-new-secret");
  });

  it("sends an emptied secret as \"\" so a credential can be cleared", async () => {
    mockGetConfigSectionResult = { public_url: "https://old.example.com", password: "***" };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const payload = await renderAndSave(
      [PUBLIC_URL_FIELD, MQTT_PASSWORD_FIELD],
      async (container) => {
        const pw = container.querySelector(
          'input[type="password"]',
        ) as HTMLInputElement;
        await fireEvent.input(pw, { target: { value: "" } });
      },
    );

    expect(payload.password).toBe("");
  });

  it("omits untouched complex secrets the editor never renders", async () => {
    mockGetConfigSectionResult = {
      public_url: "https://old.example.com",
      auth: { users: "", tokens: "" },
    };
    mockGetConfigSection.mockResolvedValue(mockGetConfigSectionResult);

    const payload = await renderAndSave(
      [PUBLIC_URL_FIELD, AUTH_USERS_FIELD, AUTH_TOKENS_FIELD],
      async (container) => {
        const urlInput = container.querySelector(
          'input[type="text"]',
        ) as HTMLInputElement;
        await fireEvent.input(urlInput, {
          target: { value: "https://new.example.com" },
        });
      },
    );

    const auth = (payload.auth ?? {}) as Record<string, unknown>;
    expect("users" in auth).toBe(false);
    expect("tokens" in auth).toBe(false);
  });
});
