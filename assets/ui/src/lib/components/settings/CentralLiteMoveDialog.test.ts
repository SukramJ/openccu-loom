// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, fireEvent, waitFor, cleanup } from "@testing-library/svelte";

const list = vi.fn();
const probe = vi.fn();
const start = vi.fn();
const status = vi.fn();
const cancel = vi.fn();
const update = vi.fn();
const toastSuccess = vi.fn();
const toastError = vi.fn();
const refresh = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    listCentralsV2: (...a: unknown[]) => list(...a),
    probeCentral: (...a: unknown[]) => probe(...a),
    startCentralPairing: (...a: unknown[]) => start(...a),
    getCentralPairing: (...a: unknown[]) => status(...a),
    cancelCentralPairing: (...a: unknown[]) => cancel(...a),
    updateCentralV2: (...a: unknown[]) => update(...a),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: {
    byName: () => ({
      name: "ccu1",
      available: true,
      readiness: { phase: "loading_hub", ready: false, interfaces_loaded: 0, interfaces_total: 1 },
    }),
    refresh: (...a: unknown[]) => refresh(...a),
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: (...a: unknown[]) => toastSuccess(...a), error: (...a: unknown[]) => toastError(...a) },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string | number>) =>
    vars ? `${key} ${Object.values(vars).join(" ")}` : key,
}));

import CentralLiteMoveDialog from "./CentralLiteMoveDialog.svelte";

const stored = {
  name: "ccu1",
  host: "192.0.2.10",
  enabled: true,
  system_type: "ccu",
  username: "Admin",
  json_rpc_port: 443,
  tls: true,
  primary_interface: "HmIP-RF",
  interfaces: [{ name: "HmIP-RF" }, { name: "CUxD" }],
};

const liteBox = {
  system_type: "openccu-lite",
  ready: true,
  lite: { pairing_available: true, interfaces: ["BidCos-RF", "HmIP-RF"] },
};

beforeEach(() => {
  vi.clearAllMocks();
  list.mockResolvedValue([stored]);
  probe.mockResolvedValue(liteBox);
  start.mockResolvedValue({ pairing_id: "p1", code: "042317", expires_in: 300 });
  status.mockResolvedValue({ state: "approved", scopes: ["rpc:read"] });
  cancel.mockResolvedValue(undefined);
  update.mockResolvedValue(undefined);
});
afterEach(() => cleanup());

function btn(container: HTMLElement, label: string) {
  return Array.from(container.ownerDocument.querySelectorAll<HTMLButtonElement>("button")).find(
    (b) => b.textContent?.trim() === label,
  );
}

function mount(reason: "no_token" | "ambiguous" | "failed" | null = "ambiguous") {
  const onClose = vi.fn();
  const r = render(CentralLiteMoveDialog, { props: { open: true, central: "ccu1", reason, onClose } });
  return { ...r, onClose };
}

describe("CentralLiteMoveDialog", () => {
  it("probes the stored host and names what the move leaves behind", async () => {
    const { container, findByTestId, getByText } = mount();
    expect((await findByTestId("lite-move-lost")).textContent).toContain("CUxD");
    expect(probe).toHaveBeenCalledWith({ host: "192.0.2.10" });
    expect(getByText("lite_move.lost.hub")).toBeTruthy();
    expect(btn(container, "lite_move.start")).toBeTruthy();
  });

  it("adds the reinstall alternative only when the add-on got no token", async () => {
    const a = mount("no_token");
    await a.findByText("lite_move.start");
    expect(a.queryByText("lite_move.reason.no_token")).toBeTruthy();
    cleanup();
    const b = mount("failed");
    await b.findByText("lite_move.start");
    expect(b.queryByText("lite_move.reason.no_token")).toBeNull();
  });

  it("pairs with full access, shows the code and the box link, and switches the central on approval", async () => {
    let approve: (v: unknown) => void = () => {};
    status.mockReturnValueOnce(new Promise((r) => (approve = r)));
    const { container, findByTestId, findByText } = mount();
    await findByText("lite_move.start");
    await fireEvent.click(btn(container, "lite_move.start")!);

    expect((await findByTestId("pairing-code")).textContent?.trim()).toBe("042317");
    expect(start).toHaveBeenCalledWith({ host: "192.0.2.10", access: "full" });
    const link = container.ownerDocument.querySelector<HTMLAnchorElement>('a[target="_blank"]');
    expect(link?.getAttribute("href")).toBe(`${location.protocol}//${location.hostname}/`);
    expect(update).not.toHaveBeenCalled();

    approve({ state: "approved", scopes: ["rpc:read"] });
    await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
    expect(update).toHaveBeenCalledWith("ccu1", {
      name: "ccu1",
      host: "192.0.2.10",
      enabled: true,
      system_type: "openccu-lite",
      pairing_id: "p1",
      json_rpc_port: 0,
      tls: false,
      tls_insecure_skip_verify: false,
      primary_interface: "HmIP-RF",
      interfaces: [{ name: "BidCos-RF" }, { name: "HmIP-RF" }],
    });
    expect(await findByText("lite_move.done ccu1")).toBeTruthy();
    expect(toastSuccess).toHaveBeenCalledTimes(1);
    expect(refresh).toHaveBeenCalled();
  });

  it("does not switch the central when the box rejects the pairing", async () => {
    status.mockResolvedValue({ state: "rejected" });
    const { container, findByText } = mount();
    await findByText("lite_move.start");
    await fireEvent.click(btn(container, "lite_move.start")!);
    expect(await findByText("onboarding.pair.rejected")).toBeTruthy();
    expect(update).not.toHaveBeenCalled();
  });

  it("surfaces a refused switch with a toast and a retry", async () => {
    update.mockRejectedValueOnce(new Error("validation failed"));
    const { container, findByText } = mount();
    await findByText("lite_move.start");
    await fireEvent.click(btn(container, "lite_move.start")!);
    await waitFor(() => expect(toastError).toHaveBeenCalledTimes(1));
    expect(await findByText(/validation failed/)).toBeTruthy();
    expect(toastSuccess).not.toHaveBeenCalled();
  });

  it.each([
    [{ system_type: "ccu", ready: true }, "lite_move.blocked.not_lite"],
    [{ ...liteBox, ready: false }, "lite_move.blocked.not_ready"],
    [{ ...liteBox, lite: { pairing_available: false } }, "lite_move.blocked.no_pairing"],
  ])("offers no pairing when the box cannot take it (%#)", async (answer, key) => {
    probe.mockResolvedValue(answer);
    const { container, findByText } = mount();
    expect(await findByText(new RegExp(key))).toBeTruthy();
    expect(btn(container, "lite_move.start")).toBeUndefined();
    expect(start).not.toHaveBeenCalled();
  });

  it("says so when the central is not configured", async () => {
    list.mockResolvedValue([]);
    const { findByText } = mount();
    expect(await findByText(/lite_move.blocked.not_found/)).toBeTruthy();
    expect(probe).not.toHaveBeenCalled();
  });

  it("withdraws a pending pairing when closed", async () => {
    status.mockReturnValue(new Promise(() => {}));
    const { container, findByTestId, onClose } = mount();
    await waitFor(() => expect(btn(container, "lite_move.start")).toBeTruthy());
    await fireEvent.click(btn(container, "lite_move.start")!);
    await findByTestId("pairing-code");
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
    await waitFor(() => expect(cancel).toHaveBeenCalledWith("p1"));
  });
});
