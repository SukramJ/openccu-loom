// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { UISchemaParameter } from "$lib/api/types";

// Echo the key back so assertions can match on the i18n key without
// depending on the real EN/DE catalogue strings.
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string | number>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import ParameterField from "./ParameterField.svelte";

afterEach(() => cleanup());

function determinableParam(
  overrides: Partial<UISchemaParameter> = {},
): UISchemaParameter {
  return {
    name: "TEMPERATURE",
    label: "Temperature",
    type: "FLOAT",
    operations: { read: true, write: true, event: false, determine: true },
    flags: { visible: true, internal: false, service: false },
    observed: true,
    value: 20,
    ...overrides,
  };
}

// A never-settling promise so the spinner state can be observed while the
// determine round-trip is "in flight".
function pending(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

describe("ParameterField — determine button", () => {
  it("renders the button for a determine-capable parameter when onDetermine is wired", () => {
    render(ParameterField, {
      props: {
        parameter: determinableParam(),
        value: 20,
        dirty: false,
        error: null,
        onChange: vi.fn(),
        onDetermine: vi.fn(async () => {}),
      },
    });
    expect(screen.getByText("parameter.determine")).toBeInTheDocument();
  });

  it("does not render the button without an onDetermine handler", () => {
    render(ParameterField, {
      props: {
        parameter: determinableParam(),
        value: 20,
        dirty: false,
        error: null,
        onChange: vi.fn(),
      },
    });
    expect(screen.queryByText("parameter.determine")).not.toBeInTheDocument();
  });

  it("does not render the button when the parameter lacks the determine capability", () => {
    render(ParameterField, {
      props: {
        parameter: determinableParam({
          operations: { read: true, write: true, event: false, determine: false },
        }),
        value: 20,
        dirty: false,
        error: null,
        onChange: vi.fn(),
        onDetermine: vi.fn(async () => {}),
      },
    });
    expect(screen.queryByText("parameter.determine")).not.toBeInTheDocument();
  });

  it("hides the button for a read-only parameter (operations.write=false)", () => {
    render(ParameterField, {
      props: {
        parameter: determinableParam({
          operations: { read: true, write: false, event: false, determine: true },
        }),
        value: 20,
        dirty: false,
        error: null,
        onChange: vi.fn(),
        onDetermine: vi.fn(async () => {}),
      },
    });
    expect(screen.queryByText("parameter.determine")).not.toBeInTheDocument();
  });

  it("hides the button when the field is locked by a profile apply (forceDisabled)", () => {
    render(ParameterField, {
      props: {
        parameter: determinableParam(),
        value: 20,
        dirty: false,
        error: null,
        forceDisabled: true,
        onChange: vi.fn(),
        onDetermine: vi.fn(async () => {}),
      },
    });
    expect(screen.queryByText("parameter.determine")).not.toBeInTheDocument();
  });

  it("invokes onDetermine on click", async () => {
    const onDetermine = vi.fn(async () => {});
    render(ParameterField, {
      props: {
        parameter: determinableParam(),
        value: 20,
        dirty: false,
        error: null,
        onChange: vi.fn(),
        onDetermine,
      },
    });
    await fireEvent.click(screen.getByText("parameter.determine"));
    expect(onDetermine).toHaveBeenCalledOnce();
  });

  it("disables the button while the determine round-trip is in flight", async () => {
    const gate = pending();
    const onDetermine = vi.fn(() => gate.promise);
    render(ParameterField, {
      props: {
        parameter: determinableParam(),
        value: 20,
        dirty: false,
        error: null,
        onChange: vi.fn(),
        onDetermine,
      },
    });
    const button = screen.getByText("parameter.determine").closest("button")!;
    expect(button.disabled).toBe(false);

    await fireEvent.click(button);
    // Still pending → button disabled so it cannot be double-triggered.
    expect(button.disabled).toBe(true);

    gate.resolve();
    // The finally-block clears the spinner; wait for the reactive DOM flush.
    await waitFor(() => expect(button.disabled).toBe(false));
  });
});
