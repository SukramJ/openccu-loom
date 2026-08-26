// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package mqtt is the MQTT north-bound bridge.
//
// It publishes two topic planes in parallel (see ADR 0011):
//
//   - The raw plane: deterministic `<base>/<central>/<interface>/
//     <address>/<channel>/<parameter>` state + `.../set` command
//     topics. This is the always-on API for non-HA consumers.
//   - The HA Discovery plane: `homeassistant/<component>/
//     openccu-loom/<object_id>/config` descriptors that point at the
//     raw plane.
//
// The broker client itself is abstracted behind [Publisher] /
// [Subscriber]; adapters live out of band. The bridge is happy with
// any RFC-compliant MQTT 3.1.1 / 5 client.
package mqtt
