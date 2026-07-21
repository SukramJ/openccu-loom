// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent } from "@testing-library/svelte";
import type { UISchemaParameter } from "$lib/api/types";

// Echoes the key with its interpolation vars appended, mirroring the
// approach in ParameterField.brightness.test.ts.
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string | number>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import ParameterGrid from "./ParameterGrid.svelte";

afterEach(() => cleanup());

function param(name: string, overrides: Partial<UISchemaParameter> = {}): UISchemaParameter {
  return {
    name,
    label: name,
    type: "FLOAT",
    operations: { read: true, write: true, event: true },
    flags: { visible: true, internal: false, service: false },
    observed: true,
    value: 0,
    ...overrides,
  };
}

function renderGrid(
  parameters: UISchemaParameter[],
  brightnessSource: { value: number; unit: string | null } | null,
  onParamChange = vi.fn(),
) {
  render(ParameterGrid, {
    props: {
      parameters,
      values: {},
      dirty: new Set<string>(),
      errors: {},
      locale: "en",
      brightnessSource,
      onParamChange,
    },
  });
  return onParamChange;
}

describe("ParameterGrid — brightness helper wiring", () => {
  it("attaches the helper only to the SHORT_/LONG_ COND_VALUE_LO/_HI fields (happy path)", () => {
    renderGrid(
      [
        param("SHORT_COND_VALUE_LO"),
        param("LEVEL", { display_as_percent: false }),
        param("LONG_COND_VALUE_HI"),
      ],
      { value: 64, unit: null },
    );

    const buttons = screen.getAllByText(/channel\.brightness\.apply::/);
    expect(buttons).toHaveLength(2);
  });

  it("renders no helper buttons when brightnessSource is null (VALUES/MASTER paramsets)", () => {
    renderGrid(
      [param("SHORT_COND_VALUE_LO"), param("LONG_COND_VALUE_HI")],
      null,
    );

    expect(screen.queryByText(/channel\.brightness\.apply::/)).not.toBeInTheDocument();
  });

  it("renders no helper buttons when the parameter list has no condition-value fields", () => {
    renderGrid(
      [param("LEVEL"), param("ON_TIME"), param("SHORT_CT_ON")],
      { value: 64, unit: null },
    );

    expect(screen.queryByText(/channel\.brightness\.apply::/)).not.toBeInTheDocument();
  });

  it("patches the correct parameter through onParamChange when the button is clicked", async () => {
    const onParamChange = renderGrid(
      [param("SHORT_COND_VALUE_LO"), param("LONG_COND_VALUE_HI")],
      { value: 90, unit: null },
    );

    const buttons = screen.getAllByText(/channel\.brightness\.apply::/);
    await fireEvent.click(buttons[1]);

    expect(onParamChange).toHaveBeenCalledOnce();
    expect(onParamChange).toHaveBeenCalledWith("LONG_COND_VALUE_HI", 90);
  });

  it("formats a unit-carrying reading (e.g. lux) into the button label", () => {
    renderGrid([param("SHORT_COND_VALUE_LO")], { value: 42.5, unit: "lx" });

    expect(screen.getByText(/42\.5 lx/)).toBeInTheDocument();
  });
});
