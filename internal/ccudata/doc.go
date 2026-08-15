// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package ccudata loads the CCU-WebUI metadata archives produced by
// The
//
// Three artefact groups are read from the upstream data module (see
// embed.go); they are no longer carried in a local directory:
//
//   - translation_extract.json.gz — per-locale channel-type labels,
//     device-model descriptions, parameter labels + help, value
//     labels, plus a locale-independent device-icon map.
//
//   - easymode_extract.json.gz — channel metadata (sender-type
//     groupings, parameter order), option presets, conditional
//     visibility, and cross-validation rules.
//
//   - profiles/<RECEIVER>.json.gz — per-receiver link-profile
//     catalogue plus `_receiver_type_aliases.json` for name
//     resolution.
//
// Operators can override the embedded archives by pointing the
// daemon at file paths (see `cfg.CCUData.*_path`); the loader falls
// through to the embedded copy when nothing is configured.
//
// Licensing is split (see the NOTICE file under embedded/):
//
//   - openccu-loom's source (this package) is MIT.
//   - The raw extract archives are derivative works of OCCU /
//     RaspberryMatic and inherit the eQ-3 HomeMatic Software License
//     (free for private, non-commercial use).
//   - The curated `translation_custom/*.json` and
//     `profiles/_receiver_type_aliases.json` are authored by
//
// See docs/adr/0003-embed-occu-extracts.md for the full rationale.
package ccudata
