// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package payload partitions a struct's fields into three
// categorical maps — info, config, state — driven by struct tags.
// It is the Go adaptation.
// `@info_property` / `@config_property` / `@state_property`
// decorators.
//
// Tag syntax:
//
//	type Foo struct {
//	    Model   string `payload:"info"`
//	    Icon    string `payload:"config"`
//	    Level   int    `payload:"state"`
//	    Unit    string `payload:"config,alt=unit_of_measurement"`
//	    Secret  string `payload:"-"`             // explicitly skipped
//	    Rooms   []string
//	}
//
// Default behaviour:
//   - untagged fields are ignored (opt-in)
//   - `alt=NAME` overrides the map key (JSON-tag-style fallback: we
//     use it when the call site asks for `UseAltNames`)
//   - values that are zero, nil-pointer, nil-slice, or nil-map are
//     omitted by default
//
// Consumers:
//   - the MQTT HA-Discovery builder pulls [KindInfo] for the HA
//     device descriptor and [KindConfig] for the entity config
//   - the REST adapter can expose [KindInfo] + [KindState] via
//     `GET /api/v1/devices/{addr}` without hand-maintaining a DTO
//
// Reflection cost is paid once per type via an internal cache.
package payload
