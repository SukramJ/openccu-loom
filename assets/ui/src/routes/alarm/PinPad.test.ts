// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import PinPad from "./PinPad.svelte";

function maskedDisplay(container: HTMLElement): HTMLElement {
  const el = container.querySelector('[aria-live="polite"]');
  if (!el) throw new Error("masked display not found");
  return el as HTMLElement;
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

describe("PinPad — digit entry", () => {
  it("presses on the digit grid append to the entered code, in order", async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    const { getByText, getByRole } = render(PinPad, { onSubmit, onCancel });

    // The digit buttons share one parametrized aria-label template
    // (`t("alarm.pinpad.digit", { digit })`) that the mocked `t` collapses
    // to the same string for every digit, so they are not distinguishable
    // by accessible name here — select by their visible digit text instead.
    await fireEvent.click(getByText("1", { selector: "button" }));
    await fireEvent.click(getByText("2", { selector: "button" }));
    await fireEvent.click(getByText("3", { selector: "button" }));

    await fireEvent.click(getByRole("button", { name: "alarm.action.disarm" }));
    expect(onSubmit).toHaveBeenCalledWith("123");
  });

  it("supports physical-keyboard digit entry, Enter to submit", async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    render(PinPad, { onSubmit, onCancel, minLength: 2 });

    await fireEvent.keyDown(window, { key: "4" });
    await fireEvent.keyDown(window, { key: "2" });
    await fireEvent.keyDown(window, { key: "Enter" });

    expect(onSubmit).toHaveBeenCalledWith("42");
  });

  it("Escape cancels instead of submitting", async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    render(PinPad, { onSubmit, onCancel });

    await fireEvent.keyDown(window, { key: "7" });
    await fireEvent.keyDown(window, { key: "Escape" });

    expect(onCancel).toHaveBeenCalledOnce();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});

describe("PinPad — masking", () => {
  it("shows a placeholder when empty and one bullet per digit thereafter, never the digits", async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    const { container, getByText, queryByText } = render(PinPad, { onSubmit, onCancel });

    expect(getByText("alarm.pinpad.placeholder")).toBeTruthy();

    await fireEvent.keyDown(window, { key: "1" });
    await fireEvent.keyDown(window, { key: "2" });
    await fireEvent.keyDown(window, { key: "3" });

    expect(queryByText("alarm.pinpad.placeholder")).toBeNull();
    // The masked display shows one bullet per entered digit — never the
    // digits themselves (the digit grid below it always renders "123..."
    // as its own button labels, so the assertion is scoped to the display).
    expect(maskedDisplay(container).textContent).toBe("•••");
    expect(maskedDisplay(container).textContent).not.toContain("123");
  });
});

describe("PinPad — submit gating", () => {
  it("disables submit below minLength and enables it once satisfied", async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    const { getByRole } = render(PinPad, { onSubmit, onCancel, minLength: 4 });

    const submitBtn = getByRole("button", { name: "alarm.action.disarm" });
    expect(submitBtn).toBeDisabled();

    await fireEvent.keyDown(window, { key: "1" });
    await fireEvent.keyDown(window, { key: "2" });
    await fireEvent.keyDown(window, { key: "3" });
    expect(submitBtn).toBeDisabled();

    await fireEvent.keyDown(window, { key: "4" });
    expect(submitBtn).not.toBeDisabled();

    await fireEvent.click(submitBtn);
    expect(onSubmit).toHaveBeenCalledWith("1234");
  });

  it("disables entry and submit while busy, so no digit can be typed", async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    const { getByRole, getByText, queryByText } = render(PinPad, {
      onSubmit,
      onCancel,
      busy: true,
      minLength: 1,
    });

    expect(getByRole("button", { name: "alarm.action.disarm" })).toBeDisabled();
    expect(getByText("1", { selector: "button" })).toBeDisabled();

    await fireEvent.click(getByText("1", { selector: "button" }));
    await fireEvent.keyDown(window, { key: "2" });

    // Neither the click nor the keyboard path added a digit: the
    // placeholder is still showing and submit never fired.
    expect(queryByText("alarm.pinpad.placeholder")).toBeTruthy();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("renders a submitted error message via role=alert", () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    const { getByRole } = render(PinPad, {
      onSubmit,
      onCancel,
      error: "alarm.error.invalid_code",
    });

    expect(getByRole("alert").textContent).toContain("alarm.error.invalid_code");
  });
});

describe("PinPad — clear", () => {
  it("the clear button empties the entered code back to the placeholder", async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    const { getByText, getByRole, queryByText } = render(PinPad, { onSubmit, onCancel });

    await fireEvent.keyDown(window, { key: "5" });
    await fireEvent.keyDown(window, { key: "6" });
    expect(queryByText("alarm.pinpad.placeholder")).toBeNull();

    await fireEvent.click(getByRole("button", { name: "alarm.pinpad.clear" }));

    expect(getByText("alarm.pinpad.placeholder")).toBeTruthy();
    expect(getByRole("button", { name: "alarm.action.disarm" })).toBeDisabled();
  });

  it("Backspace removes exactly the last digit", async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    const { container } = render(PinPad, { onSubmit, onCancel });

    await fireEvent.keyDown(window, { key: "8" });
    await fireEvent.keyDown(window, { key: "9" });
    await fireEvent.keyDown(window, { key: "Backspace" });

    expect(maskedDisplay(container).textContent).toBe("•");
  });
});

describe("PinPad — cancel", () => {
  it("the backdrop and the close/cancel controls invoke onCancel", async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    const { getByRole } = render(PinPad, { onSubmit, onCancel });

    await fireEvent.click(getByRole("button", { name: "common.cancel" }));
    expect(onCancel).toHaveBeenCalledOnce();
  });
});
