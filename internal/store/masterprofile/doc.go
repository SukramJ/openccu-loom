// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package masterprofile hosts the embedded master-profile catalogue
// Extracted. Each profile bundles a set of pre-
// canned MASTER paramset values for a given device-type / channel-
// type combination — the configuration UI surfaces them as templates
// the operator can apply with a single click ("Auf — Hoch", "Toggle",
// "Door-lock", …).
//
// The data lives as gzipped JSON under the data/ subdirectory and is
// embedded into the binary via go:embed. A lazy [Store] decodes a file
// on first access and caches the parsed profiles for the lifetime of
// the process.
//
// The extractor produces the same JSON files; the Python reference reads
// them from the installed package at runtime. We skip the disk
// detour and embed the files directly.
package masterprofile
