// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rega

import (
	"testing"
)

// TestCleanupScriptForSessionRecorder_NameOnly verifies that a script
// with only a header line (no param lines) returns just that header.
func TestCleanupScriptForSessionRecorder_NameOnly(t *testing.T) {
	t.Parallel()
	script := "!# name: fetch_all_device_data.fn\nvar x = 42;\nWriteLine(x);"
	got := CleanupScriptForSessionRecorder(script)
	want := "!# name: fetch_all_device_data.fn"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestCleanupScriptForSessionRecorder_WithParams verifies that param lines
// are preserved while non-param body lines are stripped.
func TestCleanupScriptForSessionRecorder_WithParams(t *testing.T) {
	t.Parallel()
	script := "!# name: set_system_variable.fn\n!# param: name\n!# param: value\nvar x = '##name##';\nWriteLine(x);"
	got := CleanupScriptForSessionRecorder(script)
	want := "!# name: set_system_variable.fn\n!# param: name\n!# param: value"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestCleanupScriptForSessionRecorder_EmptyString verifies that an empty
// input returns an empty string without panicking.
func TestCleanupScriptForSessionRecorder_EmptyString(t *testing.T) {
	t.Parallel()
	got := CleanupScriptForSessionRecorder("")
	if got != "" {
		t.Fatalf("got %q, want %q", got, "")
	}
}

// TestCleanupScriptForSessionRecorder_TrailingNewline verifies that a
// trailing newline does not produce a spurious empty line in the output,
// mirroring Python's splitlines() behaviour.
func TestCleanupScriptForSessionRecorder_TrailingNewline(t *testing.T) {
	t.Parallel()
	script := "!# name: get_serial.fn\nWriteLine('serial');\n"
	got := CleanupScriptForSessionRecorder(script)
	want := "!# name: get_serial.fn"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestCleanupScriptForSessionRecorder_SingleLine verifies that a script
// consisting of only a single line (no newline at all) returns that line.
func TestCleanupScriptForSessionRecorder_SingleLine(t *testing.T) {
	t.Parallel()
	script := "!# name: single_line.fn"
	got := CleanupScriptForSessionRecorder(script)
	if got != script {
		t.Fatalf("got %q, want %q", got, script)
	}
}

// TestCleanupScriptForSessionRecorder_NoParamPrefixNotKept verifies that
// lines that look similar to param lines but lack the exact prefix are
// not retained.
func TestCleanupScriptForSessionRecorder_NoParamPrefixNotKept(t *testing.T) {
	t.Parallel()
	script := "!# name: script.fn\n# param: should-be-dropped\n!#param: also-dropped\n!# param: kept"
	got := CleanupScriptForSessionRecorder(script)
	want := "!# name: script.fn\n!# param: kept"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
