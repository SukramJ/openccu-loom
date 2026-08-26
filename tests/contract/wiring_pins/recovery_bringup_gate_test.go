// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_EveryRecoveryPipelineWiringArmsItsInterface pins the other side of
// the recovery coordinator's bring-up gate.
//
// The gate is fail-closed: until a wiring path calls ArmInterface, every
// trigger for that interface is dropped. That is what keeps a bring-up from
// being read as an outage — but it also means a wiring path that registers a
// recovery pipeline and forgets to arm the interface leaves that interface
// unable to repair itself, and nothing says so. The failure is invisible
// until a CCU actually goes away, which is the one moment it matters.
//
// The file list is derived from the source rather than written down here, so
// a new south-bound wiring path is covered the day it registers a pipeline.
func TestPin_EveryRecoveryPipelineWiringArmsItsInterface(t *testing.T) {
	files := filesCalling(t, "internal/central/adapter", "WithPipelineFor")
	if len(files) == 0 {
		t.Fatal("no wiring file registers a recovery pipeline — the search is broken, not the wiring")
	}
	for _, rel := range files {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			contract.MustFindMethodCall(t, rel, "Recovery", "ArmInterface")
		})
	}
}

// filesCalling returns the repo-relative paths of the non-test Go files under
// dir whose source mentions ident.
func filesCalling(t *testing.T, dir, ident string) []string {
	t.Helper()
	root := pinRepoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, dir, name))
		if err != nil {
			t.Fatalf("read %s/%s: %v", dir, name, err)
		}
		if strings.Contains(string(src), ident+"(") {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// pinRepoRoot resolves the repository root from this file's location
// (tests/contract/wiring_pins/ → three levels up).
func pinRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	if err != nil {
		t.Fatalf("repo root resolution: %v", err)
	}
	return abs
}
