// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// The projection view presents what the daemon decided: a plane's state
// with its reason, a published entry's identity, a withheld entry's rule.
// A token this build has no sentence for is shown as it is.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";

const { mockProjection } = vi.hoisted(() => ({ mockProjection: vi.fn() }));

vi.mock("$lib/api/client", () => ({
  api: { deviceProjection: (...a: unknown[]) => mockProjection(...a) },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

// A catalogue with a few entries: a key it does not hold comes back as the
// key, which is how the real catalogue signals a missing entry.
const catalogue: Record<string, string> = {
  "projection.plane.matter": "Matter",
  "projection.reason.plane_disabled": "This plane is switched off.",
  "projection.reason.not_on_allow_list": "Not switched on in the exposure list.",
  "projection.kind.custom": "Custom data point",
  "projection.identity.endpoint_id": "Endpoint",
};
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    catalogue[key] ?? (vars ? `${key} ${JSON.stringify(vars)}` : key),
}));

import DeviceProjection from "./DeviceProjection.svelte";

const answer = {
  address: "DEV1",
  central: "ccu1",
  planes: [
    {
      plane: "matter",
      state: "active",
      entries: [
        {
          channel: 3,
          kind: "custom",
          key: "STATE",
          verdict: "published",
          identity: { source_key: "ccu1|DEV1|3|custom|STATE", endpoint_id: "7" },
        },
        { channel: 4, kind: "custom", key: "STATE", verdict: "withheld", reason: "not_on_allow_list" },
        { channel: 5, kind: "generic", key: "LEVEL", verdict: "withheld", reason: "a_rule_from_a_later_daemon" },
        { channel: 6, kind: "generic", key: "POWER", verdict: "folded", folded_into: "ELECTRICAL" },
      ],
    },
    { plane: "measurements", state: "inactive", reason: "plane_disabled", entries: [] },
    { plane: "a_later_plane", state: "inactive", reason: "a_later_reason", entries: [] },
  ],
};

beforeEach(() => mockProjection.mockReset());
afterEach(() => cleanup());

describe("DeviceProjection", () => {
  it("shows each plane with what the daemon decided", async () => {
    mockProjection.mockResolvedValue(answer);
    const { getByTestId, container } = render(DeviceProjection, { address: "DEV1" });
    await waitFor(() => expect(getByTestId("device-projection")).toBeTruthy());
    expect(mockProjection).toHaveBeenCalledWith("DEV1");

    const matter = getByTestId("projection-plane-matter").textContent ?? "";
    expect(matter).toContain("Matter");
    // The published entry names where it is: a translated identity key and
    // an untranslated one, both with their values.
    expect(matter).toContain("Endpoint: 7");
    expect(matter).toContain("source_key: ccu1|DEV1|3|custom|STATE");
    // A withheld entry states its rule, as a sentence where one exists…
    expect(matter).toContain("Not switched on in the exposure list.");
    // …and as the daemon's token where this build has none.
    expect(matter).toContain("a_rule_from_a_later_daemon");
    expect(matter).toContain("Custom data point: STATE");
    expect(matter).toContain('projection.folded_into {"target":"ELECTRICAL"}');

    // An inactive plane says why and lists nothing.
    const measurements = getByTestId("projection-plane-measurements");
    expect(measurements.textContent).toContain("This plane is switched off.");
    expect(measurements.querySelector("table")).toBeNull();

    // A plane this build does not know is shown under its own name.
    const later = getByTestId("projection-plane-a_later_plane").textContent ?? "";
    expect(later).toContain("a_later_plane");
    expect(later).toContain("a_later_reason");
    expect(container.textContent).not.toContain("undefined");
  });

  it("offers a retry when the daemon does not answer", async () => {
    mockProjection.mockRejectedValueOnce(new Error("boom"));
    mockProjection.mockResolvedValueOnce({ address: "DEV1", central: "ccu1", planes: [] });
    const { findByText, container } = render(DeviceProjection, { address: "DEV1" });
    await findByText(/boom/);
    const retry = Array.from(container.querySelectorAll("button")).find((b) => b.textContent?.trim());
    expect(retry).toBeTruthy();
    await fireEvent.click(retry!);
    await findByText("projection.empty");
    expect(mockProjection).toHaveBeenCalledTimes(2);
  });
});
