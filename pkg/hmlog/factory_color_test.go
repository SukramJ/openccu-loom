// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseFormat_TextColorAliases(t *testing.T) {
	t.Parallel()
	cases := []string{"text-color", "color", "tint"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if got := ParseFormat(raw); got != FormatTextColor {
				t.Fatalf("ParseFormat(%q) = %v, want FormatTextColor", raw, got)
			}
		})
	}
}

func TestParseFormat_KeepsExistingMappings(t *testing.T) {
	t.Parallel()
	if got := ParseFormat("text"); got != FormatText {
		t.Fatalf("text → %v, want FormatText", got)
	}
	if got := ParseFormat("json"); got != FormatJSON {
		t.Fatalf("json → %v, want FormatJSON", got)
	}
	if got := ParseFormat("nonsense"); got != FormatJSON {
		t.Fatalf("unknown → %v, want FormatJSON (fallback)", got)
	}
}

func TestFormatTextColor_NonTTYFallsBackToPlainText(t *testing.T) {
	t.Parallel()
	// A bytes.Buffer is the canonical non-TTY io.Writer — writerIsTTY
	// returns false, the factory must pick plain TextHandler so no
	// escape sequences leak into the captured output.
	var buf bytes.Buffer
	stack := BuildFullStack(StackOptions{
		Writer: &buf,
		Format: FormatTextColor,
	}, slog.LevelInfo)
	stack.Logger.Info("smoke", slog.String("kind", "non-tty"))

	got := buf.String()
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("ANSI escape leaked into non-TTY writer: %q", got)
	}
	if !strings.Contains(got, "smoke") {
		t.Fatalf("missing message: %q", got)
	}
}
