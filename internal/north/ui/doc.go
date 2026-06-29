// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package ui is the server-rendered, no-JS diagnostic surface that
// complements the Svelte 5 SPA at /app/.
//
// Templates and CSS assets are embedded at build time via [go:embed].
// The scope is intentionally narrow — only what stays useful when the
// SPA bundle itself fails to load:
//
//   - server-rendered /health for SPA-down diagnosis
//   - /about (version + license) as a stable diagnostics anchor
//
// Login, logout, OIDC, first-run onboarding, and everything device-,
// program-, sysvar-, paramset-, incident-, settings-, user- or
// token-related live in the Svelte SPA.
package ui
