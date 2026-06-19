// @vitest-environment happy-dom
import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/svelte";

// i18n is mocked to echo keys so assertions stay locale-independent.
vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import LoadingState from "./LoadingState.svelte";
import EmptyState from "./EmptyState.svelte";
import ErrorState from "./ErrorState.svelte";

describe("LoadingState", () => {
  it("renders the default loading label and a status spinner", () => {
    const { getByText, getByRole } = render(LoadingState);
    expect(getByText("common.loading")).toBeTruthy();
    // The component exposes role=status (aria-live) — the shared spinner.
    expect(getByRole("status")).toBeTruthy();
  });

  it("renders a custom label when provided", () => {
    const { getByText } = render(LoadingState, { props: { label: "devices.loading" } });
    expect(getByText("devices.loading")).toBeTruthy();
  });
});

describe("EmptyState", () => {
  it("renders the message", () => {
    const { getByText } = render(EmptyState, { props: { message: "devices.empty" } });
    expect(getByText("devices.empty")).toBeTruthy();
  });
});

describe("ErrorState", () => {
  it("renders the localized error prefix plus the message", () => {
    const { getByText } = render(ErrorState, { props: { message: "boom" } });
    // The shared error surface always prefixes with the common.error key.
    expect(getByText(/common\.error/)).toBeTruthy();
    expect(getByText(/boom/)).toBeTruthy();
  });

  it("shows no retry button when onRetry is omitted", () => {
    const { queryByRole } = render(ErrorState, { props: { message: "boom" } });
    expect(queryByRole("button")).toBeNull();
  });

  it("renders a retry button that invokes onRetry", async () => {
    const onRetry = vi.fn();
    const { getByRole } = render(ErrorState, { props: { message: "boom", onRetry } });
    await fireEvent.click(getByRole("button"));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
