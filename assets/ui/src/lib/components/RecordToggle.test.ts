// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen } from "@testing-library/svelte";

const mockGet = vi.fn();
const mockSet = vi.fn();

vi.mock("$lib/api/client", () => {
  class HistoryDisabledError extends Error {}
  class ApiError extends Error {
    constructor(
      public readonly status: number,
      public readonly body: unknown,
      message: string,
    ) {
      super(message);
    }
  }
  return {
    getRecordingOverride: (...args: unknown[]) => mockGet(...args),
    setRecordingOverride: (...args: unknown[]) => mockSet(...args),
    HistoryDisabledError,
    ApiError,
  };
});

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import RecordToggle from "./RecordToggle.svelte";

const PROPS = { central: "ccu1", interfaceId: "if", channel: "DEV:1", parameter: "TEMPERATURE" };

beforeEach(() => vi.clearAllMocks());
afterEach(() => cleanup());

describe("RecordToggle", () => {
  it("renders the record label + reset when an override is active", async () => {
    mockGet.mockResolvedValue({ record: true, source: "override" });
    render(RecordToggle, { props: PROPS });

    await waitFor(() => {
      expect(screen.getByText("history.record_label")).toBeInTheDocument();
    });
    // The reset affordance appears only for an explicit override.
    expect(screen.getByText("history.record_reset")).toBeInTheDocument();
  });

  it("hides the reset button when the source is the glob policy", async () => {
    mockGet.mockResolvedValue({ record: false, source: "policy" });
    render(RecordToggle, { props: PROPS });

    await waitFor(() => {
      expect(screen.getByText("history.record_label")).toBeInTheDocument();
    });
    expect(screen.queryByText("history.record_reset")).not.toBeInTheDocument();
  });

  it("renders nothing when the history feature is disabled", async () => {
    const { HistoryDisabledError } = await import("$lib/api/client");
    mockGet.mockRejectedValue(new HistoryDisabledError());
    const { container } = render(RecordToggle, { props: PROPS });

    await waitFor(() => {
      expect(mockGet).toHaveBeenCalled();
    });
    expect(container.textContent?.trim()).toBe("");
  });
});
