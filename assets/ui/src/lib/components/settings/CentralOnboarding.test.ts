// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, fireEvent, waitFor, cleanup } from "@testing-library/svelte";

const probe = vi.fn();
const start = vi.fn();
const status = vi.fn();
const cancel = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    probeCentral: (...a: unknown[]) => probe(...a),
    startCentralPairing: (...a: unknown[]) => start(...a),
    getCentralPairing: (...a: unknown[]) => status(...a),
    cancelCentralPairing: (...a: unknown[]) => cancel(...a),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("$lib/stores/centrals.svelte", () => ({ centralStore: { byName: () => undefined } }));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string>) => (vars ? `${key} ${Object.values(vars).join(" ")}` : key),
}));

import CentralOnboarding from "./CentralOnboarding.svelte";
import { emptyOnboarding } from "$lib/onboarding/onboarding";

const FP = "cd".repeat(32);

beforeEach(() => {
  vi.clearAllMocks();
  probe.mockImplementation(async (req: { tls?: boolean }) =>
    req.tls
      ? { system_type: "openccu-lite", ready: true, tls_fingerprint: FP, lite: { pairing_available: true } }
      : { system_type: "openccu-lite", ready: true, lite: { pairing_available: true } },
  );
  start.mockResolvedValue({ pairing_id: "p1", code: "042317", fingerprint: FP, expires_in: 300 });
  status.mockResolvedValue({ state: "approved", scopes: ["rpc:read", "meta:read"] });
  cancel.mockResolvedValue(undefined);
});
afterEach(() => cleanup());

function btn(container: HTMLElement, label: string) {
  return Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
    (b) => b.textContent?.trim() === label,
  );
}

describe("CentralOnboarding", () => {
  it("asks a box found over HTTP again over HTTPS and shows the fingerprint to compare", async () => {
    const { container, getByText } = render(CentralOnboarding, {
      props: { host: "box.local", value: emptyOnboarding() },
    });
    await fireEvent.click(btn(container, "onboarding.probe")!);
    await waitFor(() => expect(getByText(FP)).toBeTruthy());
    expect(probe).toHaveBeenCalledTimes(2);
    expect(probe.mock.calls[1][0]).toMatchObject({ host: "box.local", tls: true });
    // Pairing waits until the operator compared the fingerprint.
    expect(btn(container, "onboarding.pair.start")!.disabled).toBe(true);
  });

  it("pairs once the fingerprint is confirmed and never shows a token", async () => {
    const { container, getByText, getByTestId } = render(CentralOnboarding, {
      props: { host: "box.local", value: emptyOnboarding() },
    });
    await fireEvent.click(btn(container, "onboarding.probe")!);
    await waitFor(() => expect(getByText(FP)).toBeTruthy());
    await fireEvent.click(container.querySelector<HTMLInputElement>('input[type="checkbox"]')!);
    await fireEvent.click(btn(container, "onboarding.pair.start")!);

    expect(start).toHaveBeenCalledWith(
      { host: "box.local", port: undefined, tls: true, tls_fingerprint: FP, access: "full" },
      false,
    );
    await waitFor(() => expect(getByText("onboarding.pair.approved")).toBeTruthy());
    expect(getByText(/rpc:read, meta:read/)).toBeTruthy();
    expect(() => getByTestId("pairing-code")).toThrow();
    expect(container.textContent).not.toContain("olt_");
  });

  it("uses the session-less setup routes in the wizard", async () => {
    const { container } = render(CentralOnboarding, {
      props: { host: "box.local", setup: true, value: emptyOnboarding() },
    });
    await fireEvent.click(btn(container, "onboarding.probe")!);
    await waitFor(() => expect(probe).toHaveBeenCalled());
    expect(probe.mock.calls[0][1]).toBe(true);
  });

  it("reports a CCU without offering pairing", async () => {
    probe.mockResolvedValue({ system_type: "ccu", ready: true });
    const { container, getByText, queryByText } = render(CentralOnboarding, {
      props: { host: "ccu.local", value: emptyOnboarding() },
    });
    await fireEvent.click(btn(container, "onboarding.probe")!);
    await waitFor(() => expect(getByText("onboarding.type.ccu")).toBeTruthy());
    expect(queryByText("onboarding.tab.pair")).toBeNull();
    expect(probe).toHaveBeenCalledTimes(1);
  });
});
