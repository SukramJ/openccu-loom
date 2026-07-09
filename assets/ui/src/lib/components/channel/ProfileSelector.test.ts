// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen, waitFor } from "@testing-library/svelte";
import type { UISchemaProfile } from "$lib/api/types";

// i18n is mocked to echo keys so assertions stay locale-independent.
vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import ProfileSelector from "./ProfileSelector.svelte";

afterEach(() => {
  cleanup();
});

// The bits-ui Select popover does not open under happy-dom (no
// floating-ui/pointer-capture support), so these tests exercise the
// component's own state — pre-seeded from `active_profile_id`, mirroring
// what a real click-then-select interaction would leave behind — rather
// than driving the dropdown UI directly.

const PROFILE: UISchemaProfile = {
  receiver_type: "SWITCH_VIRTUAL_RECEIVER",
  sender_type: "SWITCH_TRANSMITTER",
  active_profile_id: 1,
  raw: {
    SWITCH_TRANSMITTER: {
      profiles: [
        {
          id: 1,
          name: { en: "Profile One" },
          description: { en: "The first profile" },
          params: {
            LEVEL: { constraint_type: "fixed", value: 1 },
            RAMP_TIME: {
              constraint_type: "range",
              default: 5,
              min_value: 0,
              max_value: 10,
            },
          },
        },
      ],
    },
  },
};

describe("ProfileSelector — rendering", () => {
  it("renders nothing when the profile carries no variants", () => {
    const { container } = render(ProfileSelector, {
      props: { profile: { receiver_type: "X" }, locale: "en", onApply: vi.fn() },
    });
    expect(container.textContent).toBe("");
  });

  it("preselects the active profile and shows the sender -> receiver header plus description", async () => {
    render(ProfileSelector, {
      props: { profile: PROFILE, locale: "en", onApply: vi.fn() },
    });

    await waitFor(() => {
      expect(screen.getByText(/SWITCH_TRANSMITTER → SWITCH_VIRTUAL_RECEIVER/)).toBeInTheDocument();
    });
    expect(screen.getByText(/profile\.detected/)).toBeInTheDocument();
    expect(screen.getByText("The first profile")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "profile.apply" })).not.toBeDisabled();
  });
});

describe("ProfileSelector — apply", () => {
  it("calls onApply with the fixed value patched and range params marked editable", async () => {
    const onApply = vi.fn();
    render(ProfileSelector, {
      props: { profile: PROFILE, locale: "en", onApply },
    });

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "profile.apply" })).not.toBeDisabled();
    });
    await fireEvent.click(screen.getByRole("button", { name: "profile.apply" }));

    expect(onApply).toHaveBeenCalledWith(
      { LEVEL: 1, RAMP_TIME: 5 },
      { fixed: ["LEVEL"], editable: ["RAMP_TIME"] },
    );
  });
});

describe("ProfileSelector — dry-run preview", () => {
  it("classifies current values into matching/would-change buckets and expands the detail table", async () => {
    render(ProfileSelector, {
      props: {
        profile: PROFILE,
        locale: "en",
        currentValues: { LEVEL: 0, RAMP_TIME: 5 },
        onApply: vi.fn(),
      },
    });

    await waitFor(() => {
      expect(screen.getByText(/profile\.preview\.matching/)).toBeInTheDocument();
    });
    // RAMP_TIME (5) already equals the range default -> counted as matching;
    // LEVEL (0) differs from the fixed value (1) -> counted as would-change.
    expect(screen.getByText(/✓\s*1\s*profile\.preview\.matching/)).toBeInTheDocument();
    expect(screen.getByText(/↻\s*1\s*profile\.preview\.will_change/)).toBeInTheDocument();
    expect(screen.queryByText(/profile\.preview\.conflict/)).toBeNull();

    await fireEvent.click(screen.getByText("profile.preview.show"));
    expect(screen.getByText("LEVEL")).toBeInTheDocument();
    expect(screen.getByText("RAMP_TIME")).toBeInTheDocument();
  });
});
