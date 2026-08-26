// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rega

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// scriptFS holds every .fn body the daemon knows about. Keep in sync
// with hmenum.AllRegaScripts — the test TestEveryKnownScriptHasBody
// asserts both sides agree.
//
//go:embed scripts/*.fn
var scriptFS embed.FS

// loadScript returns the embedded body for s, or an error if none is
// registered. The body is read once on demand; embed.FS already caches
// the bytes, so there's no benefit to an in-process memoisation layer.
func loadScript(s hmenum.RegaScript) (string, error) {
	path := "scripts/" + string(s) + ".fn"
	data, err := fs.ReadFile(scriptFS, path)
	if err != nil {
		return "", fmt.Errorf("rega: script %q not embedded: %w", s, err)
	}
	return string(data), nil
}
