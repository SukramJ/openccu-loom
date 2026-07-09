// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/svelte";
import type { UISchemaSubsetGroup } from "$lib/api/types";

// i18n is mocked to echo keys so assertions stay locale-independent.
vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import SubsetGroupSelector from "./SubsetGroupSelector.svelte";

afterEach(() => {
  cleanup();
});

// The bits-ui Select popover does not open under happy-dom (no
// floating-ui/pointer-capture support), so these tests exercise the
// component's own state — pre-seeded from `current_option_id`, mirroring
// what a real click-then-select interaction would leave behind — rather
// than driving the dropdown UI directly.

const GROUP: UISchemaSubsetGroup = {
  id: "eco-mode",
  label: "Eco mode",
  member_params: ["LEVEL", "RAMP_TIME"],
  options: [
    { id: 1, label: "Off", values: { LEVEL: 0 } },
    { id: 2, label: "Eco", values: { LEVEL: 0.3, RAMP_TIME: 5 } },
  ],
};

describe("SubsetGroupSelector — rendering", () => {
  it("renders the group label and member params, with no active badge and a disabled Apply button", () => {
    render(SubsetGroupSelector, { props: { group: GROUP, onApply: vi.fn() } });

    expect(screen.getByText("Eco mode")).toBeInTheDocument();
    expect(screen.getByText("LEVEL, RAMP_TIME")).toBeInTheDocument();
    expect(screen.queryByText("subset.active")).toBeNull();
    expect(screen.getByRole("button", { name: "profile.apply" })).toBeDisabled();
  });

  it("shows the active badge and pre-selects the current option when current_option_id is set", () => {
    const group: UISchemaSubsetGroup = { ...GROUP, current_option_id: 2 };
    render(SubsetGroupSelector, { props: { group, onApply: vi.fn() } });

    expect(screen.getByText("subset.active")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "profile.apply" })).not.toBeDisabled();
  });
});

describe("SubsetGroupSelector — apply", () => {
  it("calls onApply with the option's values as fixed params", async () => {
    const onApply = vi.fn();
    const group: UISchemaSubsetGroup = { ...GROUP, current_option_id: 2 };
    render(SubsetGroupSelector, { props: { group, onApply } });

    await fireEvent.click(screen.getByRole("button", { name: "profile.apply" }));

    expect(onApply).toHaveBeenCalledWith(
      { LEVEL: 0.3, RAMP_TIME: 5 },
      { fixed: ["LEVEL", "RAMP_TIME"], editable: [] },
    );
  });

  it("does not call onApply when no option is selected", async () => {
    const onApply = vi.fn();
    render(SubsetGroupSelector, { props: { group: GROUP, onApply } });

    // The button is disabled, but assert the handler contract directly
    // too: clicking a disabled button must not fire onApply.
    await fireEvent.click(screen.getByRole("button", { name: "profile.apply" }));

    expect(onApply).not.toHaveBeenCalled();
  });
});
