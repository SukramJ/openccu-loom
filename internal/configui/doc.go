// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package configui implements the configuration-UI session logic so
// the openccu-loom REST/WebSocket layer can expose safe MASTER/LINK
// paramset editing to a frontend.
//
// Sub-systems:
//
//   - [Session] / [SessionStore] — server-side edit sessions with
//     undo/redo, dirty tracking and cross-parameter validation
//     ([CrossValidationConstraint]), the gateway through which the
//     WebSocket handlers manage configuration changes safely.
//   - [ExportConfiguration] / [ImportConfiguration] /
//     [ApplyConfiguration] — channel-configuration export/import.
//
// Rendering schemas (labels, groupings, widgets) are NOT built here:
// the UI schema served to the SPA is assembled by the central
// adapter's UISchema service from the device registry and the
// embedded metadata archives.
//
// Each component has a stable wire shape (tagged structs); the JSON
// emitted from this package is consumed unchanged by the SPA
// frontend.
package configui
