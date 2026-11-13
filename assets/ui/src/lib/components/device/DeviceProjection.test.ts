// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// The projection view arranges what the daemon decided as a matrix: a row
// per subject, a column per active plane. It opens on what appears
// somewhere; the rest is behind two switches and the list of rules.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import type { ChannelSummary } from "$lib/api/types";

const { mockProjection, mockToast } = vi.hoisted(() => ({
  mockProjection: vi.fn(),
  mockToast: { success: vi.fn(), error: vi.fn(), warn: vi.fn() },
}));

vi.mock("$lib/api/client", () => ({
  api: { deviceProjection: (...a: unknown[]) => mockProjection(...a) },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));
vi.mock("$lib/stores/toast.svelte", () => ({ toastStore: mockToast }));

// A catalogue with a few entries: a key it does not hold comes back as the
// key, which is how the real catalogue signals a missing entry.
const catalogue: Record<string, string> = {
  "projection.plane.matter": "Matter",
  "projection.plane.ha_discovery": "Home Assistant (MQTT discovery)",
  "projection.plane_short.ha_discovery": "Home Assistant",
  "projection.reason.plane_disabled": "This plane is switched off.",
  "projection.reason.not_on_allow_list": "Not switched on in the exposure list.",
  "projection.reason.hidden_parameter": "Hidden by the visibility rules.",
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
        { channel: 3, kind: "custom", key: "STATE", verdict: "published", identity: { source_key: "k3", endpoint_id: "7" } },
        { channel: 3, kind: "generic", key: "HUMIDITY", verdict: "withheld", reason: "not_on_allow_list" },
        { channel: 5, kind: "generic", key: "LEVEL", verdict: "withheld", reason: "a_rule_from_a_later_daemon" },
      ],
    },
    { plane: "measurements", state: "inactive", reason: "plane_disabled", entries: [] },
    {
      plane: "ha_discovery",
      state: "active",
      entries: [
        { channel: 3, kind: "channel_entity", verdict: "published", identity: { discovery_topic: "ha/switch/n/3_switch/config" } },
        { channel: 3, kind: "data_point", key: "HUMIDITY", verdict: "published", identity: { discovery_topic: "ha/sensor/n/3_humidity/config" } },
        { channel: 3, kind: "data_point", key: "MASTER:ON_TIME", verdict: "withheld", reason: "hidden_parameter" },
      ],
    },
  ],
};

function rowTexts(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll("tbody tr")).map((r) => r.textContent ?? "");
}
const hasRow = (container: HTMLElement, text: string) => rowTexts(container).some((r) => r.includes(text));

beforeEach(() => {
  mockProjection.mockReset();
  mockToast.success.mockReset();
  mockToast.error.mockReset();
});
afterEach(() => cleanup());

describe("DeviceProjection", () => {
  it("opens on what appears somewhere, one column per active plane", async () => {
    mockProjection.mockResolvedValue(answer);
    const { getByTestId, container } = render(DeviceProjection, {
      address: "DEV1",
      // The view reads a channel's number, name and type label; the rest of
      // the summary does not reach it.
      channels: [{ number: 3, name: "Lamp", type_label: "Switch" }] as unknown as ChannelSummary[],
    });
    await waitFor(() => expect(getByTestId("device-projection")).toBeTruthy());
    expect(mockProjection).toHaveBeenCalledWith("DEV1");

    const headers = Array.from(container.querySelectorAll("thead th")).map((h) => h.textContent?.trim());
    expect(headers).toContain("Matter");
    expect(headers).toContain("Home Assistant");
    // The inactive plane is a line above the table, not a column.
    expect(headers.join("|")).not.toContain("measurements");
    expect(getByTestId("projection-plane-measurements").textContent).toContain("This plane is switched off.");

    // Rows that appear somewhere are shown…
    expect(hasRow(container, "projection.row.entity")).toBe(true);
    expect(hasRow(container, "HUMIDITY")).toBe(true);
    // …a row no plane carries and a configuration parameter are not.
    expect(hasRow(container, "LEVEL")).toBe(false);
    expect(hasRow(container, "ON_TIME")).toBe(false);
    expect(getByTestId("projection-hidden-count").textContent).toContain('"n":2');

    // The channel's name heads its group.
    expect(container.textContent).toContain('projection.group.channel_named {"n":3,"name":"Lamp"}');
    // One subject, two planes, two verdicts in one row.
    const humidity = Array.from(container.querySelectorAll("tbody tr")).find((r) => r.textContent?.includes("HUMIDITY"))!;
    const verdicts = Array.from(humidity.querySelectorAll("[data-verdict]")).map((d) => d.getAttribute("data-verdict"));
    expect(verdicts).toEqual(["withheld", "published"]);
  });

  it("lists what is left out by rule and shows the rows behind one", async () => {
    mockProjection.mockResolvedValue(answer);
    const { getByTestId, container } = render(DeviceProjection, { address: "DEV1" });
    await waitFor(() => expect(getByTestId("projection-withheld")).toBeTruthy());

    const withheld = getByTestId("projection-withheld");
    expect(withheld.textContent).toContain("Not switched on in the exposure list.");
    expect(withheld.textContent).toContain("Hidden by the visibility rules.");
    // A rule this build has no sentence for is shown as the daemon's token.
    expect(withheld.textContent).toContain("a_rule_from_a_later_daemon");

    const item = Array.from(withheld.querySelectorAll("li")).find((li) => li.textContent?.includes("Hidden by the visibility rules."))!;
    const show = Array.from(item.querySelectorAll("button")).find((b) => b.textContent?.includes("projection.withheld.show"))!;
    await fireEvent.click(show);
    await waitFor(() => expect(getByTestId("projection-reason-filter")).toBeTruthy());
    // Exactly the rows behind the rule, the configuration parameter included.
    expect(hasRow(container, "ON_TIME")).toBe(true);
    expect(hasRow(container, "HUMIDITY")).toBe(false);
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
