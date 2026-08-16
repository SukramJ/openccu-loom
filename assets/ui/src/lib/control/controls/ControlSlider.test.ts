// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.
//
// @vitest-environment happy-dom
//
// Brightness / position sliders run on a 0..1 range with a 0.01 step, so
// an integer-rounded aria-valuenow announces the same number for "off"
// and for anything below half brightness.
import { describe, it, expect, afterEach } from "vitest";
import { render, cleanup, screen } from "@testing-library/svelte";
import ControlSlider from "./ControlSlider.svelte";

afterEach(() => cleanup());

function renderSlider(props: {
  value: number;
  min: number;
  max: number;
  step: number;
}) {
  render(ControlSlider, {
    props: { onChange: () => {}, label: "brightness", ...props },
  });
  return screen.getByRole("slider");
}

describe("ControlSlider — announced value", () => {
  it.each([
    [0.4, "0.4"],
    [0.01, "0.01"],
    [0.6, "0.6"],
    [1, "1"],
  ])("announces %s on a fractional 0..1 range", (value, expected) => {
    const slider = renderSlider({ value, min: 0, max: 1, step: 0.01 });
    expect(slider.getAttribute("aria-valuenow")).toBe(expected);
  });

  it("keeps integer announcements on an integer step", () => {
    const slider = renderSlider({ value: 42.4, min: 0, max: 100, step: 1 });
    expect(slider.getAttribute("aria-valuenow")).toBe("42");
  });
});
