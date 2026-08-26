// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package builtins blank-imports every built-in custom data-point
// sub-package so each package's `init()` block runs against the
// process-wide [custom.DefaultRegistry], registering its
// [custom.Constructor] entries.
//
// Importing this package is a one-stop way to ensure the full
// constructor catalogue is available — the daemon entry point and
// the [DevicePipeline] both blank-import it so production hydration
// always materialises custom data points.
//
// Tests that exercise the materializer end-to-end against the
// generated profile registry can also blank-import this package; it
// is side-effect-only and exposes no symbols.
package builtins

import (
	// Side-effect imports: every sub-package registers its
	// Constructor functions in init().
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/light"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/valve"
)
