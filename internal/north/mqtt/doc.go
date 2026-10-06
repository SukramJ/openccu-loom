// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package mqtt is the MQTT north-bound bridge.
//
// It publishes two topic planes in parallel (see ADR 0011, and ADR 0083 for
// the grammar):
//
//   - The raw plane: mqtt-smarthome 2.0's `<base>/<function>/<item…>` —
//     `<base>/status/<central>/<interface>/<address>/<channel>/<bucket>/
//     <parameter>` status items, the same item path under `set` for
//     commands and under `meta` for descriptors, plus the instance topics
//     `<base>/connected`, `<base>/info` and `<base>/maintenance/…`. This is
//     the always-on API for non-HA consumers.
//   - The HA Discovery plane: `homeassistant/<component>/
//     openccu-loom/<object_id>/config` descriptors that point at the
//     raw plane.
//
// The broker client itself is abstracted behind [Publisher] /
// [Subscriber]; adapters live out of band. The bridge is happy with
// any RFC-compliant MQTT 3.1.1 / 5 client.
package mqtt
