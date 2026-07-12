// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";

// t() is mocked to return the raw key (with interpolation values appended)
// so assertions can match on stable strings instead of localized prose.
vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
}));

import CentralStatusBadge from "./CentralStatusBadge.svelte";

afterEach(() => cleanup());

type Readiness = {
  phase: "unknown" | "waiting_for_ccu" | "loading_hub" | "loading_devices" | "ready";
  ready: boolean;
  interfaces_loaded: number;
  interfaces_total: number;
};

function readiness(overrides: Partial<Readiness>): Readiness {
  return {
    phase: "unknown",
    ready: false,
    interfaces_loaded: 0,
    interfaces_total: 0,
    ...overrides,
  };
}

describe("CentralStatusBadge", () => {
  it("renders Ready when readiness.ready is true, regardless of phase", () => {
    const { getByText } = render(CentralStatusBadge, {
      available: true,
      readiness: readiness({ phase: "ready", ready: true }),
    });
    expect(getByText("central.readiness.ready")).toBeTruthy();
  });

  it("renders Waiting for CCU during waiting_for_ccu", () => {
    const { getByText } = render(CentralStatusBadge, {
      available: true,
      readiness: readiness({ phase: "waiting_for_ccu" }),
    });
    expect(getByText("central.readiness.waiting")).toBeTruthy();
  });

  it("renders the hub-loading label during loading_hub", () => {
    const { getByText } = render(CentralStatusBadge, {
      available: true,
      readiness: readiness({ phase: "loading_hub" }),
    });
    expect(getByText("central.readiness.loading_hub")).toBeTruthy();
  });

  it("renders the device-loading label with the x/y interface count", () => {
    const { getByText } = render(CentralStatusBadge, {
      available: true,
      readiness: readiness({
        phase: "loading_devices",
        interfaces_loaded: 2,
        interfaces_total: 5,
      }),
    });
    expect(
      getByText(
        'central.readiness.loading_devices:{"loaded":2,"total":5}',
      ),
    ).toBeTruthy();
  });

  it("renders Offline when available is false and not ready, even mid-phase", () => {
    const { getByText } = render(CentralStatusBadge, {
      available: false,
      readiness: readiness({ phase: "loading_devices" }),
    });
    expect(getByText("central.readiness.offline")).toBeTruthy();
  });

  it("renders Unknown for an unrecognized phase while available", () => {
    const { getByText } = render(CentralStatusBadge, {
      available: true,
      readiness: readiness({ phase: "unknown" }),
    });
    expect(getByText("central.readiness.unknown")).toBeTruthy();
  });

  it("prefers Ready over Offline when ready is true even if available is false", () => {
    const { getByText } = render(CentralStatusBadge, {
      available: false,
      readiness: readiness({ phase: "ready", ready: true }),
    });
    expect(getByText("central.readiness.ready")).toBeTruthy();
  });
});
