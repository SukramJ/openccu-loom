// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent } from "@testing-library/svelte";
import type { UISchemaParameter } from "$lib/api/types";

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string | number>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import ParameterField from "./ParameterField.svelte";

afterEach(() => cleanup());

// DIRT_LEVEL: raw wire value in [0, 1], reported unit "%", multiplier 100 —
// the same shape as LEVEL/SMOKE_LEVEL/etc. on the VALUES/MASTER paramsets
// this component renders for. LINK-paramset LEVEL fields carry no
// `multiplier` at all (they route through display_as_percent /
// ParameterLevelField instead) so this fixture never exercises that path.
function levelParam(overrides: Partial<UISchemaParameter> = {}): UISchemaParameter {
  return {
    name: "DIRT_LEVEL",
    label: "Dirt level",
    type: "FLOAT",
    unit: "%",
    min: 0,
    max: 1,
    multiplier: 100,
    operations: { read: true, write: true, event: true },
    flags: { visible: true, internal: false, service: false },
    observed: true,
    value: 0.42,
    ...overrides,
  };
}

describe("ParameterField — display_value / multiplier projection (read-only)", () => {
  it("renders the multiplier-scaled number, not the raw wire value", () => {
    render(ParameterField, {
      props: {
        parameter: levelParam({ operations: { read: true, write: false, event: true } }),
        value: 0.42,
        dirty: false,
        error: null,
        onChange: vi.fn(),
      },
    });

    // 0.42 * 100 = 42 — the raw "0.42 %" bug this feature fixes.
    expect(screen.getByText("42")).toBeInTheDocument();
    expect(screen.queryByText("0.42")).not.toBeInTheDocument();
  });

  it("renders the value unchanged when the parameter carries no multiplier", () => {
    render(ParameterField, {
      props: {
        parameter: levelParam({
          operations: { read: true, write: false, event: true },
          multiplier: undefined,
          unit: "s",
        }),
        value: 0.42,
        dirty: false,
        error: null,
        onChange: vi.fn(),
      },
    });

    // The read-only numeric branch renders a float with one decimal
    // (`numericDisplay`'s toFixed(1)), so an unprojected 0.42 shows as
    // "0.4". What this pins is that the value is NOT scaled — a projection
    // would put it at 42.
    expect(screen.getByText("0.4")).toBeInTheDocument();
    expect(screen.queryByText("42")).not.toBeInTheDocument();
  });

  it("keeps a live-updated raw value at the same scale as the initial render", async () => {
    // ChannelPanel's WS handler patches `values[parameter]` with the raw
    // wire value straight from the event (see ChannelPanel.svelte's
    // `data_point` subscription) and that flows back in through the same
    // `value` prop — mirror that update path directly.
    const { rerender } = render(ParameterField, {
      props: {
        parameter: levelParam({ operations: { read: true, write: false, event: true } }),
        value: 0.42,
        dirty: false,
        error: null,
        onChange: vi.fn(),
      },
    });
    expect(screen.getByText("42")).toBeInTheDocument();

    await rerender({ value: 0.55 });

    expect(screen.getByText("55")).toBeInTheDocument();
    expect(screen.queryByText("42")).not.toBeInTheDocument();
  });
});

describe("ParameterField — display_value / multiplier projection (write path)", () => {
  it("shows the scaled value in the slider's number input and divides back on write", async () => {
    const onChange = vi.fn();
    render(ParameterField, {
      props: {
        parameter: levelParam(),
        value: 0.42,
        dirty: false,
        error: null,
        onChange,
      },
    });

    const numberInput = screen.getByRole("spinbutton") as HTMLInputElement;
    expect(numberInput.value).toBe("42");

    await fireEvent.input(numberInput, { target: { value: "50" } });

    expect(onChange).toHaveBeenCalledOnce();
    expect(onChange).toHaveBeenCalledWith(0.5);
  });

  it("divides back on write from the range slider too", async () => {
    const onChange = vi.fn();
    render(ParameterField, {
      props: {
        parameter: levelParam(),
        value: 0.42,
        dirty: false,
        error: null,
        onChange,
      },
    });

    const slider = screen.getByRole("slider") as HTMLInputElement;
    expect(slider.value).toBe("42");

    await fireEvent.input(slider, { target: { value: "80" } });

    expect(onChange).toHaveBeenCalledWith(0.8);
  });
});
