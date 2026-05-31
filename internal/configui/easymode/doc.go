// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package easymode hosts the Easymode use-case strategies that
// transform a form schema with channel-specific UX rules from the
// CCU's WebUI metadata. Three use cases are mirrored 1:1 from
//
//   - UC2 — conditional visibility: parameters appear / disappear
//     depending on the value of a trigger parameter.
//   - UC5 — option presets: numeric parameters offer a curated list
//     of suggested values (with optional custom-value escape hatch).
//   - UC6 — subset membership: several parameters are manipulated
//     together via a single virtual selector ("Auf — Hoch", "Toggle",
//     ...).
//
// Each UC implements the [UseCase] interface (Resolve / Validate /
// Apply) so the form-schema generator can pick its way through the
// pipeline without coupling to UC internals.
package easymode
