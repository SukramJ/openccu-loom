// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent } from "@testing-library/svelte";
import type { UISchemaParameter } from "$lib/api/types";

// Echoes the key with its interpolation vars appended, so assertions can
// check the actual reading reached the rendered label/tooltip without
// depending on the real EN/DE catalogue strings.
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string | number>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import ParameterField from "./ParameterField.svelte";

afterEach(() => cleanup());

function condParam(overrides: Partial<UISchemaParameter> = {}): UISchemaParameter {
  return {
    name: "SHORT_COND_VALUE_LO",
    label: "Threshold",
    type: "FLOAT",
    operations: { read: true, write: true, event: true },
    flags: { visible: true, internal: false, service: false },
    observed: true,
    value: 50,
    ...overrides,
  };
}

describe("ParameterField — brightness helper button", () => {
  it("renders the button and patches the field via onChange on click (happy path)", async () => {
    const onChange = vi.fn();
    render(ParameterField, {
      props: {
        parameter: condParam(),
        value: 50,
        dirty: false,
        error: null,
        onChange,
        brightnessHelper: { value: 128, display: "128" },
      },
    });

    const button = screen.getByText(/channel\.brightness\.apply::.*128/);
    expect(button).toBeInTheDocument();
    expect(screen.getByTitle(/128/)).toBeInTheDocument();

    await fireEvent.click(button);
    expect(onChange).toHaveBeenCalledOnce();
    expect(onChange).toHaveBeenCalledWith(128);
  });

  it("renders no button when brightnessHelper is absent (default)", () => {
    const onChange = vi.fn();
    render(ParameterField, {
      props: {
        parameter: condParam(),
        value: 50,
        dirty: false,
        error: null,
        onChange,
      },
    });

    expect(screen.queryByText(/channel\.brightness\.apply/)).not.toBeInTheDocument();
  });

  it("hides the button when the parameter is read-only (operations.write=false)", () => {
    const onChange = vi.fn();
    render(ParameterField, {
      props: {
        parameter: condParam({ operations: { read: true, write: false, event: true } }),
        value: 50,
        dirty: false,
        error: null,
        onChange,
        brightnessHelper: { value: 128, display: "128" },
      },
    });

    expect(screen.queryByText(/channel\.brightness\.apply/)).not.toBeInTheDocument();
  });

  it("hides the button when the field is locked by a profile apply (forceDisabled)", () => {
    const onChange = vi.fn();
    render(ParameterField, {
      props: {
        parameter: condParam(),
        value: 50,
        dirty: false,
        error: null,
        forceDisabled: true,
        onChange,
        brightnessHelper: { value: 128, display: "128" },
      },
    });

    expect(screen.queryByText(/channel\.brightness\.apply/)).not.toBeInTheDocument();
  });

  it("passes the reading's unit-formatted display through to the tooltip", () => {
    const onChange = vi.fn();
    render(ParameterField, {
      props: {
        parameter: condParam({ name: "LONG_COND_VALUE_HI" }),
        value: 50,
        dirty: false,
        error: null,
        onChange,
        brightnessHelper: { value: 42.5, display: "42.5 lx" },
      },
    });

    expect(screen.getByTitle(/42\.5 lx/)).toBeInTheDocument();
    expect(screen.getByText(/42\.5 lx/)).toBeInTheDocument();
  });
});
