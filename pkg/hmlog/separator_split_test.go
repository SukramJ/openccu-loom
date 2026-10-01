// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog

import (
	"log/slog"
	"testing"
)

// TestSubsystemPathForFuncSlashEdges pins the split at the last "/" of the
// module-relative function name: absent, leading, trailing and nested.
func TestSubsystemPathForFuncSlashEdges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc.(*C).Do": "openccu-loom.client.transport.xmlrpc",
		"github.com/SukramJ/openccu-loom/pkg/hmlog.F":                              "openccu-loom.hmlog",
		"github.com/SukramJ/openccu-loom/cmd/openccu-loom.main":                    "openccu-loom.cmd.openccu-loom",
		"github.com/SukramJ/openccu-loom/top.F":                                    "openccu-loom.top",
		"github.com/SukramJ/openccu-loom//x.F":                                     "openccu-loom.x",
		"github.com/SukramJ/openccu-loom/x/":                                       "",
		"github.com/SukramJ/openccu-loom/":                                         "",
		"example.com/other/x.F":                                                    "",
	} {
		if got := subsystemPathForFunc(in); got != want {
			t.Errorf("subsystemPathForFunc(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResolveWalksDotSeparatedAncestors pins the ancestor walk that strips
// one segment after the last "." per step.
func TestResolveWalksDotSeparatedAncestors(t *testing.T) {
	t.Parallel()
	r := NewLevelRegistry(slog.LevelInfo)
	r.Set("a", slog.LevelDebug, 0)
	r.Set("x.", slog.LevelError, 0)
	for in, want := range map[string]slog.Level{
		"a.b.c": slog.LevelDebug,
		"a":     slog.LevelDebug,
		"ab":    slog.LevelInfo,
		".a":    slog.LevelInfo,
		"x..y":  slog.LevelError,
		"x.y":   slog.LevelInfo,
	} {
		if got := r.Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %v, want %v", in, got, want)
		}
	}
}
