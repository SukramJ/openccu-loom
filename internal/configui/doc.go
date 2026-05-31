// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package configui implements the configuration-UI logic so the
// openccu-loom REST/WebSocket layer can expose form schemas, edit
// sessions, and parameter groupings to a frontend.
//
// Sub-systems:
//
//   - [Schema] family — FormParameter / FormSection / FormSchema
//     value types that the FormSchema generator emits.
//   - [DetermineWidget] — chooses an appropriate UI widget for a
//     parameter (TOGGLE, SLIDER_WITH_INPUT, NUMBER_INPUT, RADIO_GROUP,
//     DROPDOWN, TEXT_INPUT, BUTTON, READ_ONLY).
//   - [Session] — server-side edit session with undo/redo and dirty
//     tracking, the gateway through which the WebSocket handlers
//     manage configuration changes safely.
//
// Each component has a stable wire shape (tagged structs); the JSON
// emitted from this package is consumed unchanged by the SPA
// frontend.
package configui
