// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

import { describe, it, expect } from "vitest";
import type { DeviceProjection } from "$lib/api/types";
import { appears, buildMatrix, hasReason, primaryIdentity, rowOf } from "./matrix";

const projection: DeviceProjection = {
  address: "DEV1",
  central: "ccu1",
  planes: [
    {
      plane: "matter",
      state: "active",
      entries: [
        { channel: 1, kind: "custom", key: "CLIMATE", verdict: "published", identity: { source_key: "k", endpoint_id: "4" } },
        { channel: 1, kind: "generic", key: "HUMIDITY", verdict: "withheld", reason: "not_on_allow_list" },
        { channel: 0, kind: "generic", key: "LOW_BAT", verdict: "folded", folded_into: "POWER_SOURCE" },
        { channel: 2, verdict: "withheld", reason: "channel_hidden" },
        { channel: 3, kind: "generic", key: "ELECTRICAL", verdict: "published", identity: { source_key: "e" } },
      ],
    },
    { plane: "measurements", state: "inactive", reason: "plane_disabled", entries: [] },
    {
      plane: "ha_discovery",
      state: "active",
      entries: [
        { channel: 1, kind: "channel_entity", verdict: "published", identity: { discovery_topic: "homeassistant/climate/n/1_climate/config" } },
        { channel: 1, kind: "data_point", key: "HUMIDITY", verdict: "published", identity: { discovery_topic: "t/h" } },
        { channel: 1, kind: "data_point", key: "MASTER:TEMPERATURE_OFFSET", verdict: "withheld", reason: "hidden_parameter" },
        { channel: 1, kind: "data_point", key: "MASTER:BOOST_TIME", verdict: "withheld", reason: "hidden_parameter" },
        { channel: -1, kind: "update", verdict: "published", identity: { discovery_topic: "t/u" } },
      ],
    },
    {
      plane: "mqtt_raw",
      state: "active",
      entries: [
        { channel: 1, kind: "channel_state", key: "climate", verdict: "published", identity: { topic: "s/1/custom/climate" } },
        { channel: 1, kind: "week_profile", verdict: "withheld", reason: "empty_value" },
        { channel: 1, kind: "schedule", key: "active_entries", verdict: "published", identity: { topic: "s/1/schedule/active_entries" } },
        { channel: -1, kind: "device", key: "update", verdict: "published", identity: { topic: "s/update" } },
        { channel: -1, kind: "device", key: "online", verdict: "published", identity: { topic: "s/online" } },
      ],
    },
  ],
};

describe("rowOf", () => {
  it("puts the planes' names for one subject on one row", () => {
    // The channel's own entity under three names.
    const entity = { kind: "entity", key: "" };
    expect(rowOf("matter", { channel: 1, kind: "custom", key: "CLIMATE", verdict: "published" })).toEqual(entity);
    expect(rowOf("ha_discovery", { channel: 1, kind: "channel_entity", verdict: "published" })).toEqual(entity);
    expect(rowOf("mqtt_raw", { channel: 1, kind: "channel_state", key: "climate", verdict: "published" })).toEqual(entity);
    // A parameter, with and without a kind.
    const humidity = { kind: "parameter", key: "HUMIDITY" };
    expect(rowOf("matter", { channel: 1, kind: "generic", key: "HUMIDITY", verdict: "withheld" })).toEqual(humidity);
    expect(rowOf("rest", { channel: 1, key: "HUMIDITY", verdict: "published" })).toEqual(humidity);
    expect(rowOf("measurements", { channel: 1, key: "HUMIDITY", verdict: "published" })).toEqual(humidity);
    // The firmware update under the discovery kind and the raw device key.
    expect(rowOf("mqtt_raw", { channel: -1, kind: "device", key: "update", verdict: "published" })).toEqual(
      rowOf("ha_discovery", { channel: -1, kind: "update", verdict: "published" }),
    );
  });

  it("keeps a configuration parameter apart from the value of the same name", () => {
    expect(rowOf("ha_discovery", { channel: 1, kind: "data_point", key: "MASTER:LEVEL", verdict: "withheld" })).toEqual({
      kind: "master",
      key: "LEVEL",
    });
  });

  it("gives an entry of an unknown plane or kind a row instead of dropping it", () => {
    expect(rowOf("a_later_plane", { channel: 1, kind: "x", key: "y", verdict: "published" }).kind).toBe("other");
    expect(rowOf("mqtt_raw", { channel: 1, kind: "a_later_kind", key: "y", verdict: "published" }).kind).toBe("other");
  });
});

describe("buildMatrix", () => {
  const m = buildMatrix(projection);
  const row = (id: string) => m.rows.find((r) => r.id === id);

  it("has one row per subject with a cell per plane that names it", () => {
    const entity = row("1|entity|")!;
    expect(Object.keys(entity.cells).sort()).toEqual(["ha_discovery", "matter", "mqtt_raw"]);
    const humidity = row("1|parameter|HUMIDITY")!;
    expect(humidity.cells.matter.verdict).toBe("withheld");
    expect(humidity.cells.ha_discovery.verdict).toBe("published");
    // An inactive plane contributes no cell.
    expect(humidity.cells.measurements).toBeUndefined();
  });

  it("shows a cell as present when any of its entries is", () => {
    const schedule = row("1|schedule|")!;
    expect(schedule.cells.mqtt_raw.entries).toHaveLength(2);
    expect(schedule.cells.mqtt_raw.verdict).toBe("published");
  });

  it("orders by channel, then by the kind of subject", () => {
    const ids = m.rows.map((r) => r.id);
    expect(ids.indexOf("-1|update|")).toBeLessThan(ids.indexOf("0|parameter|LOW_BAT"));
    expect(ids.indexOf("1|entity|")).toBeLessThan(ids.indexOf("1|parameter|HUMIDITY"));
    expect(ids.indexOf("1|parameter|HUMIDITY")).toBeLessThan(ids.indexOf("1|master|BOOST_TIME"));
  });

  it("counts what is withheld per plane and reason, most frequent first", () => {
    expect(m.withheld[0]).toEqual({ plane: "ha_discovery", reason: "hidden_parameter", count: 2 });
    expect(m.withheld).toContainEqual({ plane: "matter", reason: "channel_hidden", count: 1 });
  });

  it("tells a row that appears somewhere from one that appears nowhere", () => {
    expect(appears(row("1|parameter|HUMIDITY")!)).toBe(true);
    expect(appears(row("0|parameter|LOW_BAT")!)).toBe(true); // folded counts
    expect(appears(row("1|master|BOOST_TIME")!)).toBe(false);
    expect(appears(row("2|channel|")!)).toBe(false);
  });

  it("finds the rows behind a reason", () => {
    expect(hasReason(row("1|master|BOOST_TIME")!, "ha_discovery", "hidden_parameter")).toBe(true);
    expect(hasReason(row("1|parameter|HUMIDITY")!, "ha_discovery", "hidden_parameter")).toBe(false);
  });
});

describe("primaryIdentity", () => {
  it("names the identifier an operator looks for on each plane", () => {
    expect(primaryIdentity("matter", { channel: 1, verdict: "published", identity: { source_key: "k", endpoint_id: "4" } })).toEqual({
      key: "endpoint_id",
      value: "4",
    });
    // Before the bridge assigned an endpoint, the source key is all there is.
    expect(primaryIdentity("matter", { channel: 1, verdict: "published", identity: { source_key: "k" } })?.key).toBe("source_key");
    expect(primaryIdentity("mqtt_raw", { channel: 1, verdict: "published", identity: { topic: "t" } })?.value).toBe("t");
    expect(primaryIdentity("rest", { channel: 1, verdict: "published", identity: { path: "/p" } })?.value).toBe("/p");
    expect(primaryIdentity("a_later_plane", { channel: 1, verdict: "published", identity: { whatever: "v" } })?.value).toBe("v");
    expect(primaryIdentity("matter", { channel: 1, verdict: "withheld" })).toBeNull();
  });
});
