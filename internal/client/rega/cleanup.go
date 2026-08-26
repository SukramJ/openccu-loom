// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rega

import "strings"

// CleanupScriptForSessionRecorder strips a ReGa script body down to the
// minimal form used by the session recorder: the first line (the script
// identifier / name header) plus any lines that carry parameter
// declarations (prefix "!# param:").
//
// This lets the recorder store a compact, reproducible representation of
// each script invocation without embedding the full multi-line body.
//
// Empty scripts are returned as the empty string.
func CleanupScriptForSessionRecorder(script string) string {
	lines := strings.Split(script, "\n")
	if len(lines) == 0 {
		return ""
	}
	// Normalise: splitlines in Python does not yield an empty trailing
	// element for a trailing newline, but strings.Split does. Trim it.
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	result := make([]string, 0, len(lines))
	// Keep the first line (script name / identifier header).
	result = append(result, lines[0])
	// Keep any subsequent lines that declare a parameter.
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "!# param:") {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
