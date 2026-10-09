// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen } from "@testing-library/svelte";
import type { UISchemaParameter } from "$lib/api/types";

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import ParameterGrid from "./ParameterGrid.svelte";

afterEach(() => cleanup());

function p(name: string, label: string, value: number, extra: Partial<UISchemaParameter> = {}): UISchemaParameter {
  return {
    name,
    label,
    type: "INTEGER",
    min: 0,
    max: 100,
    operations: { read: true, write: true, event: false },
    flags: { visible: true, internal: false, service: false },
    observed: true,
    value,
    ...extra,
  };
}

function mount(parameters: UISchemaParameter[], showRawName: boolean) {
  render(ParameterGrid, {
    props: {
      parameters,
      values: {},
      dirty: new Set<string>(),
      errors: {},
      locale: "en",
      onParamChange: vi.fn(),
      showRawName,
    },
  });
}

// Raw CCU names belong to expert mode. Every row the grid renders has to
// follow the switch — including the ones it hands to a sub-component.
describe("ParameterGrid — raw CCU names", () => {
  it("hides them on the daylight-saving rows outside expert mode", () => {
    mount([p("DST_START_MONTH", "Start month", 3)], false);
    expect(screen.getByText("Start month")).toBeTruthy();
    expect(screen.queryByText("DST_START_MONTH", { exact: true })).toBeNull();
  });

  it("hides them on a time pair's custom fields outside expert mode", () => {
    // 3 / 17 matches no preset, so the pair opens its raw custom fields.
    mount(
      [
        p("ON_TIME_BASE", "On time base", 3, { max: 7 }),
        p("ON_TIME_FACTOR", "On time factor", 17, { max: 31 }),
      ],
      false,
    );
    expect(screen.getByText("On time base")).toBeTruthy();
    expect(screen.queryByText("ON_TIME_BASE", { exact: true })).toBeNull();
    expect(screen.queryByText("ON_TIME_FACTOR", { exact: true })).toBeNull();
  });

  it("shows them in expert mode", () => {
    mount([p("DST_START_MONTH", "Start month", 3)], true);
    expect(screen.getByText("DST_START_MONTH", { exact: true })).toBeTruthy();
  });
});
