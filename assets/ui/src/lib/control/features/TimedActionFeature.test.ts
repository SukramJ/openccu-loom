// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";

import TimedActionFeature from "./TimedActionFeature.svelte";

afterEach(() => {
  cleanup();
});

describe("TimedActionFeature — label + presets", () => {
  it("renders the provided label", () => {
    const { getByText } = render(TimedActionFeature, {
      props: { label: "On for…", color: "var(--ha-primary-color)", onSubmit: vi.fn() },
    });
    expect(getByText("On for…")).toBeTruthy();
  });

  it("renders one chip per default preset with humanised labels", () => {
    const { getByRole } = render(TimedActionFeature, {
      props: { label: "On for…", color: "var(--ha-primary-color)", onSubmit: vi.fn() },
    });
    expect(getByRole("button", { name: "30 s" })).toBeTruthy();
    expect(getByRole("button", { name: "1 min" })).toBeTruthy();
    expect(getByRole("button", { name: "5 min" })).toBeTruthy();
  });
});

describe("TimedActionFeature — preset chip click", () => {
  it("calls onSubmit exactly once with 60 when the 1 min chip is clicked", async () => {
    const onSubmit = vi.fn();
    const { getByRole } = render(TimedActionFeature, {
      props: { label: "On for…", color: "var(--ha-primary-color)", onSubmit },
    });
    await fireEvent.click(getByRole("button", { name: "1 min" }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit).toHaveBeenCalledWith(60);
  });
});

describe("TimedActionFeature — free number input", () => {
  it("submits the entered value via the submit button", async () => {
    const onSubmit = vi.fn();
    const { getByRole } = render(TimedActionFeature, {
      props: { label: "On for…", color: "var(--ha-primary-color)", onSubmit },
    });
    // The number input and the submit button both carry aria-label={label};
    // role="spinbutton" vs role="button" disambiguates them.
    const input = getByRole("spinbutton", { name: "On for…" });
    const submit = getByRole("button", { name: "On for…" });

    await fireEvent.input(input, { target: { value: "45" } });
    await fireEvent.click(submit);

    expect(onSubmit).toHaveBeenCalledWith(45);
  });

  it("clamps a sub-1 value up to 1", async () => {
    const onSubmit = vi.fn();
    const { getByRole } = render(TimedActionFeature, {
      props: { label: "On for…", color: "var(--ha-primary-color)", onSubmit },
    });
    const input = getByRole("spinbutton", { name: "On for…" });
    const submit = getByRole("button", { name: "On for…" });

    await fireEvent.input(input, { target: { value: "0" } });
    await fireEvent.click(submit);

    expect(onSubmit).toHaveBeenCalledWith(1);
  });

  it("rounds a fractional value", async () => {
    const onSubmit = vi.fn();
    const { getByRole } = render(TimedActionFeature, {
      props: { label: "On for…", color: "var(--ha-primary-color)", onSubmit },
    });
    const input = getByRole("spinbutton", { name: "On for…" });
    const submit = getByRole("button", { name: "On for…" });

    await fireEvent.input(input, { target: { value: "2.7" } });
    await fireEvent.click(submit);

    expect(onSubmit).toHaveBeenCalledWith(3);
  });
});

describe("TimedActionFeature — disabled state", () => {
  it("disables the submit button and preset chips", () => {
    const { getByRole } = render(TimedActionFeature, {
      props: {
        label: "On for…",
        color: "var(--ha-primary-color)",
        onSubmit: vi.fn(),
        disabled: true,
      },
    });
    expect(getByRole("button", { name: "On for…" })).toBeDisabled();
    expect(getByRole("button", { name: "30 s" })).toBeDisabled();
    expect(getByRole("spinbutton", { name: "On for…" })).toBeDisabled();
  });
});
