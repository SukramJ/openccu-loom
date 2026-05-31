// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package i18n provides translation catalogues for the UI.
//
// The MVP ships `de` and `en` JSON files embedded via go:embed.
// The lookup API is intentionally minimal: one function that takes a
// locale + message id and returns the translated string, falling
// back to the key itself.
package i18n
