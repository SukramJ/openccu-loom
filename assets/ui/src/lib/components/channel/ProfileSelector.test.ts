// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/svelte";
import type { UISchemaProfile } from "$lib/api/types";
import { profileVariants } from "$lib/links/link-profiles";

// i18n is mocked to echo keys so assertions stay locale-independent.
vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// The real Select wraps bits-ui's floating-portal listbox, which happy-dom
// cannot drive (see SelectStub.svelte).
vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("../../../routes/__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

import ProfileSelector from "./ProfileSelector.svelte";

afterEach(() => {
  cleanup();
});

const PROFILE: UISchemaProfile = {
  receiver_type: "SWITCH_VIRTUAL_RECEIVER",
  sender_type: "KEY_TRANSCEIVER",
  active_profile_id: 1,
  raw: {
    KEY_TRANSCEIVER: {
      profiles: [
        { id: 0, name: { en: "Expert" }, params: {} },
        { id: 1, name: { en: "Switch on" }, description: { en: "Turns it on" }, params: {} },
        { id: 2, name: { en: "Switch off" }, description: { en: "Turns it off" }, params: {} },
      ],
    },
  },
};
const variants = profileVariants(PROFILE, "en");

describe("ProfileSelector", () => {
  it("renders nothing without variants", () => {
    const { container } = render(ProfileSelector, {
      props: { variants: [], selectedId: 0, detectedId: 0, onSelect: vi.fn() },
    });
    expect(container.textContent?.trim()).toBe("");
  });

  it("marks the detected profile and shows its description", () => {
    render(ProfileSelector, {
      props: { variants, selectedId: 1, detectedId: 1, onSelect: vi.fn() },
    });
    expect(screen.getByRole("option", { name: "Switch on" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByText(/profile\.detected/)).toBeTruthy();
    expect(screen.getByText("Turns it on")).toBeTruthy();
  });

  it("drops the detected mark once another profile is chosen", () => {
    render(ProfileSelector, {
      props: { variants, selectedId: 2, detectedId: 1, onSelect: vi.fn() },
    });
    expect(screen.queryByText(/profile\.detected/)).toBeNull();
    expect(screen.getByText("Turns it off")).toBeTruthy();
  });

  it("shows the expert hint instead of a description for Experte", () => {
    render(ProfileSelector, {
      props: { variants, selectedId: 0, detectedId: 1, onSelect: vi.fn() },
    });
    expect(screen.getByText("profile.expert_hint")).toBeTruthy();
  });

  it("reports a new choice and ignores re-picking the current one", async () => {
    const onSelect = vi.fn();
    render(ProfileSelector, {
      props: { variants, selectedId: 1, detectedId: 1, onSelect },
    });
    await fireEvent.click(screen.getByRole("option", { name: "Switch on" }));
    expect(onSelect).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("option", { name: "Switch off" }));
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 2 }));
  });
});
