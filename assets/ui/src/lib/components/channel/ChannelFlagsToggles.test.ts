// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const mockSetChannelFlags = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    setChannelFlags: (...args: unknown[]) => mockSetChannelFlags(...args),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

// i18n echoes keys so assertions stay locale-independent.
vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

import ChannelFlagsToggles from "./ChannelFlagsToggles.svelte";
import { toastStore } from "$lib/stores/toast.svelte";

function switches(container: HTMLElement): HTMLElement[] {
  return Array.from(
    container.querySelectorAll('[role="switch"]'),
  ) as HTMLElement[];
}

beforeEach(() => {
  vi.clearAllMocks();
  mockSetChannelFlags.mockResolvedValue({ hidden: false, locked: false });
});
afterEach(() => cleanup());

describe("ChannelFlagsToggles", () => {
  it("renders two switches reflecting the initial props", () => {
    const { container } = render(ChannelFlagsToggles, {
      props: { address: "ABC0000001", channelNo: 1, hidden: true, locked: false },
    });
    const [hidden, locked] = switches(container);
    expect(hidden.getAttribute("aria-checked")).toBe("true");
    expect(locked.getAttribute("aria-checked")).toBe("false");
  });

  it("toggling hidden calls setChannelFlags with {hidden:true} and toasts", async () => {
    mockSetChannelFlags.mockResolvedValue({ hidden: true, locked: false });
    const { container } = render(ChannelFlagsToggles, {
      props: { address: "ABC0000001", channelNo: 2, hidden: false, locked: false },
    });

    await fireEvent.click(switches(container)[0]);

    await waitFor(() => expect(mockSetChannelFlags).toHaveBeenCalledTimes(1));
    expect(mockSetChannelFlags).toHaveBeenCalledWith("ABC0000001", 2, {
      hidden: true,
    });
    expect(toastStore.success).toHaveBeenCalledWith("channel.flags.saved_toast");
  });

  it("toggling locked calls setChannelFlags with {locked:true}", async () => {
    mockSetChannelFlags.mockResolvedValue({ hidden: false, locked: true });
    const { container } = render(ChannelFlagsToggles, {
      props: { address: "ABC0000001", channelNo: 3, hidden: false, locked: false },
    });

    await fireEvent.click(switches(container)[1]);

    await waitFor(() => expect(mockSetChannelFlags).toHaveBeenCalledTimes(1));
    expect(mockSetChannelFlags).toHaveBeenCalledWith("ABC0000001", 3, {
      locked: true,
    });
  });

  it("surfaces a failure as an error toast", async () => {
    mockSetChannelFlags.mockRejectedValue(new Error("upstream down"));
    const { container } = render(ChannelFlagsToggles, {
      props: { address: "ABC0000001", channelNo: 4, hidden: false, locked: false },
    });

    await fireEvent.click(switches(container)[0]);

    await waitFor(() => expect(toastStore.error).toHaveBeenCalledTimes(1));
    expect(toastStore.error).toHaveBeenCalledWith(
      "channel.flags.failed",
      "upstream down",
    );
    expect(toastStore.success).not.toHaveBeenCalled();
  });
});
