// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package i18n provides translation catalogues for the UI.
//
// The MVP ships `de` and `en` JSON files embedded via go:embed.
// The lookup API is intentionally minimal: one function that takes a
// locale + message id and returns the translated string, falling
// back to the key itself.
package i18n
