// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import "embed"

//go:embed templates/*.html
var templateFS embed.FS

//go:embed assets/*.css assets/logo/*.svg assets/logo/*.png assets/logo/*.webmanifest
var assetFS embed.FS
