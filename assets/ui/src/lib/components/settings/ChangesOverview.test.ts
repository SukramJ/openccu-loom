// @vitest-environment happy-dom
//
// "Revert" destroys an operator-supplied value server-side (DELETE
// /config/fields/{path}) and the UI offers no undo, so it belongs behind the
// shared confirm dialog like every other destructive action.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";
import type { ConfigSchemaField } from "$lib/api/client";

const mockResetConfigField = vi.fn();
const mockConfirmAsk = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    resetConfigField: (...args: unknown[]) => mockResetConfigField(...args),
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

vi.mock("$lib/stores/restartPending.svelte", () => ({
  refreshRestartPending: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, _params?: unknown) => key,
}));

import ChangesOverview from "./ChangesOverview.svelte";

const BROKER_URL: ConfigSchemaField = {
  path: "north.mqtt.broker_url",
  class: "basic",
  go_type: "string",
};

function renderOverview() {
  return render(ChangesOverview, {
    props: {
      changedPaths: [BROKER_URL.path],
      schemaFields: [BROKER_URL],
      effectiveConfig: { north: { mqtt: { broker_url: "tcp://broker:1883" } } },
      allSections: ["north.mqtt"],
    },
  });
}

async function clickRevert(container: HTMLElement) {
  const btn = Array.from(container.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === "changes.revert",
  );
  expect(btn).toBeDefined();
  await fireEvent.click(btn!);
}

beforeEach(() => {
  vi.clearAllMocks();
  mockResetConfigField.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("ChangesOverview — revert", () => {
  it("asks for confirmation before discarding the value", async () => {
    mockConfirmAsk.mockResolvedValue(true);

    const { container } = renderOverview();
    await clickRevert(container);

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    expect(mockConfirmAsk.mock.calls[0][0]).toMatchObject({ destructive: true });
    await waitFor(() =>
      expect(mockResetConfigField).toHaveBeenCalledWith(BROKER_URL.path),
    );
  });

  it("keeps the value when the operator cancels", async () => {
    mockConfirmAsk.mockResolvedValue(false);

    const { container } = renderOverview();
    await clickRevert(container);

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    expect(mockResetConfigField).not.toHaveBeenCalled();
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});
