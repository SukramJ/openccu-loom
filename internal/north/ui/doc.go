// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package ui is the bootstrap-and-diagnose HTMX surface that
// complements the Svelte 5 SPA at /app/.
//
// Templates and CSS assets are embedded at build time via [go:embed].
// The scope is intentionally narrow — only what the SPA structurally
// cannot cover:
//
//   - first-run admin setup (/setup)
//   - login + logout (form-based, no JS) and OIDC PKCE callback
//   - server-rendered /health for the case where the SPA bundle
//     fails to load
//   - /about (version + license) as a stable diagnostics anchor
//
// Anything device-, program-, sysvar-, paramset-, incident-,
// settings-, user- or token-related lives in the Svelte SPA.
package ui
