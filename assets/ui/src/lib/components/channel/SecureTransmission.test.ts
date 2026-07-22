// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const mockGetParamset = vi.fn();
const mockPutParamset = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    getParamset: (...args: unknown[]) => mockGetParamset(...args),
    putParamset: (...args: unknown[]) => mockPutParamset(...args),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

const mockConfirmAsk = vi.fn();
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

// i18n is mocked to echo keys so assertions stay locale-independent.
vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import SecureTransmission from "./SecureTransmission.svelte";
import { toastStore } from "$lib/stores/toast.svelte";

function switchEl(container: HTMLElement): HTMLElement {
  const sw = container.querySelector('[role="switch"]');
  if (!sw) throw new Error("switch not found");
  return sw as HTMLElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockPutParamset.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
});

describe("SecureTransmission — presence", () => {
  it("renders nothing when the MASTER paramset has no AES_ACTIVE", async () => {
    mockGetParamset.mockResolvedValue({ SOME_OTHER: 1 });
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1" },
    });
    await waitFor(() =>
      expect(mockGetParamset).toHaveBeenCalledWith("ABC0000001:1", "MASTER"),
    );
    expect(container.querySelector('[role="switch"]')).toBeNull();
    expect(container.textContent).not.toContain(
      "channel.secure_transmission.title",
    );
  });

  it("stays hidden when the raw paramset read fails", async () => {
    mockGetParamset.mockRejectedValue(new Error("boom"));
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1" },
    });
    await waitFor(() => expect(mockGetParamset).toHaveBeenCalled());
    expect(container.querySelector('[role="switch"]')).toBeNull();
  });

  it("renders the toggle reflecting the current OFF value", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: false });
    const { container, getByText } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1" },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );
    expect(getByText("channel.secure_transmission.title")).toBeInTheDocument();
    expect(getByText("channel.secure_transmission.help")).toBeInTheDocument();
    expect(switchEl(container).getAttribute("aria-checked")).toBe("false");
  });

  it("reflects an active value delivered as the integer 1", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: 1 });
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1" },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );
    expect(switchEl(container).getAttribute("aria-checked")).toBe("true");
  });

  it("reflects an active value delivered as the string '1'", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: "1" });
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1" },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );
    expect(switchEl(container).getAttribute("aria-checked")).toBe("true");
  });

  it("reflects an inactive value delivered as the integer 0", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: 0 });
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1" },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );
    expect(switchEl(container).getAttribute("aria-checked")).toBe("false");
  });
});

describe("SecureTransmission — parent-held disabled guard", () => {
  it("renders the switch disabled and ignores a click while a lock is held elsewhere", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: false });
    const { container } = render(SecureTransmission, {
      props: {
        channelAddress: "ABC0000001:1",
        editToken: "tok-1",
        disabled: true,
      },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );

    const sw = switchEl(container);
    expect(sw.hasAttribute("disabled")).toBe(true);

    await fireEvent.click(sw);

    expect(mockConfirmAsk).not.toHaveBeenCalled();
    expect(mockPutParamset).not.toHaveBeenCalled();
  });
});

describe("SecureTransmission — enabling", () => {
  it("confirms, then writes AES_ACTIVE=true through the edit-locked MASTER path", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: false });
    mockConfirmAsk.mockResolvedValue(true);
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1", editToken: "tok-1" },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );

    await fireEvent.click(switchEl(container));

    await waitFor(() => expect(mockPutParamset).toHaveBeenCalledTimes(1));
    expect(mockConfirmAsk).toHaveBeenCalledTimes(1);
    expect(mockPutParamset).toHaveBeenCalledWith(
      "ABC0000001:1",
      "MASTER",
      { AES_ACTIVE: true },
      "tok-1",
    );
    expect(toastStore.success).toHaveBeenCalledWith(
      "channel.secure_transmission.enabled_toast",
    );
  });

  it("does not write when the enable confirmation is cancelled", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: false });
    mockConfirmAsk.mockResolvedValue(false);
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1", editToken: "tok-1" },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );

    await fireEvent.click(switchEl(container));

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));
    expect(mockPutParamset).not.toHaveBeenCalled();
    expect(toastStore.success).not.toHaveBeenCalled();
  });

  it("surfaces a write failure as an error toast", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: false });
    mockConfirmAsk.mockResolvedValue(true);
    mockPutParamset.mockRejectedValue(new Error("upstream down"));
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1", editToken: "tok-1" },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );

    await fireEvent.click(switchEl(container));

    await waitFor(() => expect(toastStore.error).toHaveBeenCalledTimes(1));
    expect(toastStore.error).toHaveBeenCalledWith(
      "channel.secure_transmission.failed",
      "upstream down",
    );
    expect(toastStore.success).not.toHaveBeenCalled();
  });
});

describe("SecureTransmission — disabling", () => {
  it("writes AES_ACTIVE=false without a confirmation prompt", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: true });
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1", editToken: "tok-1" },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );

    await fireEvent.click(switchEl(container));

    await waitFor(() => expect(mockPutParamset).toHaveBeenCalledTimes(1));
    expect(mockConfirmAsk).not.toHaveBeenCalled();
    expect(mockPutParamset).toHaveBeenCalledWith(
      "ABC0000001:1",
      "MASTER",
      { AES_ACTIVE: false },
      "tok-1",
    );
    expect(toastStore.success).toHaveBeenCalledWith(
      "channel.secure_transmission.disabled_toast",
    );
  });

  it("surfaces a write failure as an error toast without a confirmation prompt", async () => {
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: true });
    mockPutParamset.mockRejectedValue(new Error("upstream down"));
    const { container } = render(SecureTransmission, {
      props: { channelAddress: "ABC0000001:1", editToken: "tok-1" },
    });
    await waitFor(() =>
      expect(container.querySelector('[role="switch"]')).not.toBeNull(),
    );

    await fireEvent.click(switchEl(container));

    await waitFor(() => expect(toastStore.error).toHaveBeenCalledTimes(1));
    expect(mockConfirmAsk).not.toHaveBeenCalled();
    expect(toastStore.error).toHaveBeenCalledWith(
      "channel.secure_transmission.failed",
      "upstream down",
    );
    expect(toastStore.success).not.toHaveBeenCalled();
    // Failed write reverts the switch back to the pre-toggle (still on)
    // state rather than sticking on the optimistic "off" the user clicked.
    expect(switchEl(container).getAttribute("aria-checked")).toBe("true");
  });
});
