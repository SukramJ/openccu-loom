// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen, waitFor } from "@testing-library/svelte";

// The panel talks to the REST client, the auth store (role gate) and the
// toast surface; all three are mocked so the form itself is what is under
// test. The TTL field is a real <input type="number">, which is the point:
// Svelte coerces a numeric bind:value to a number, so the panel must never
// treat it as a string.

const mockListLogLevels = vi.fn();
const mockSetLogLevel = vi.fn();
const mockResetLogLevel = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    listLogLevels: (...args: unknown[]) => mockListLogLevels(...args),
    setLogLevel: (...args: unknown[]) => mockSetLogLevel(...args),
    resetLogLevel: (...args: unknown[]) => mockResetLogLevel(...args),
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

vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: {
    get identity() {
      return { role: "admin" };
    },
  },
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
  t: (key: string) => key,
}));

import LogLevelsPanel from "./LogLevelsPanel.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  mockListLogLevels.mockResolvedValue({ default: "info", overrides: [] });
  mockSetLogLevel.mockResolvedValue(undefined);
});

afterEach(cleanup);

async function renderForm() {
  render(LogLevelsPanel);
  await screen.findByText("loglevels.add");
  const inputs = document.querySelectorAll<HTMLInputElement>("form input");
  const path = inputs[0];
  const ttl = Array.from(inputs).find((el) => el.type === "number");
  expect(ttl).toBeTruthy();
  return { path, ttl: ttl as HTMLInputElement };
}

describe("LogLevelsPanel — adding an override", () => {
  it("submits a time-limited override with the TTL converted to seconds", async () => {
    const { path, ttl } = await renderForm();

    await fireEvent.input(path, { target: { value: "openccu-loom.client" } });
    await fireEvent.input(ttl, { target: { value: "5" } });
    await fireEvent.submit(document.querySelector("form")!);

    await waitFor(() =>
      expect(mockSetLogLevel).toHaveBeenCalledWith(
        "openccu-loom.client",
        "debug",
        300,
      ),
    );
    expect(mockToastSuccess).toHaveBeenCalled();
  });

  it("submits a permanent override when the TTL is left empty", async () => {
    const { path } = await renderForm();

    await fireEvent.input(path, { target: { value: "openccu-loom.central" } });
    await fireEvent.submit(document.querySelector("form")!);

    await waitFor(() =>
      expect(mockSetLogLevel).toHaveBeenCalledWith(
        "openccu-loom.central",
        "debug",
        0,
      ),
    );
  });

  it("submits a permanent override after the TTL was typed and cleared again", async () => {
    const { path, ttl } = await renderForm();

    await fireEvent.input(path, { target: { value: "openccu-loom.mqtt" } });
    await fireEvent.input(ttl, { target: { value: "5" } });
    await fireEvent.input(ttl, { target: { value: "" } });
    await fireEvent.submit(document.querySelector("form")!);

    await waitFor(() =>
      expect(mockSetLogLevel).toHaveBeenCalledWith(
        "openccu-loom.mqtt",
        "debug",
        0,
      ),
    );
  });

  it("clears the TTL field after a successful add", async () => {
    const { path, ttl } = await renderForm();

    await fireEvent.input(path, { target: { value: "openccu-loom.client" } });
    await fireEvent.input(ttl, { target: { value: "5" } });
    await fireEvent.submit(document.querySelector("form")!);

    await waitFor(() => expect(mockSetLogLevel).toHaveBeenCalled());
    // The reload re-creates the form, so the field has to be looked up
    // again rather than read off the node captured before the submit.
    await waitFor(() => {
      const fresh = document.querySelector<HTMLInputElement>(
        'form input[type="number"]',
      );
      expect(fresh?.value).toBe("");
    });
  });
});
