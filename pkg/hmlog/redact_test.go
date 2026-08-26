// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// newTextHandler returns a text-format slog.Handler writing into buf,
// mirroring the helper pattern used in request_filter_test.go.
func newTextHandler(buf *bytes.Buffer) slog.Handler {
	return slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
}

// --------------------------------------------------------------------------
// Default-Redaction
// --------------------------------------------------------------------------

func TestRedactingHandler_DefaultKeys(t *testing.T) {
	sensitiveKeys := []struct {
		key   string
		value string
	}{
		{"password", "geheim"},
		{"token", "tok123"},
		{"api_key", "key456"},
		{"client_secret", "sec789"},
		{"authorization", "Bearer xyz"},
		{"cookie", "session=abc"},
	}

	for _, tc := range sensitiveKeys {
		t.Run(tc.key, func(t *testing.T) {
			var buf bytes.Buffer
			h := hmlog.NewRedactingHandler(newTextHandler(&buf))
			logger := slog.New(h)
			logger.Info("msg", slog.String(tc.key, tc.value))

			out := buf.String()
			if !strings.Contains(out, hmlog.RedactMask) {
				t.Errorf("key %q: expected %q in output; got: %s", tc.key, hmlog.RedactMask, out)
			}
			if strings.Contains(out, tc.value) {
				t.Errorf("key %q: sensitive value %q must not appear in output; got: %s", tc.key, tc.value, out)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Case-Insensitive matching
// --------------------------------------------------------------------------

func TestRedactingHandler_CaseInsensitive(t *testing.T) {
	cases := []string{"Password", "PASSWORD", "X-Api-Key", "OIDC_Client_Secret"}

	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			var buf bytes.Buffer
			h := hmlog.NewRedactingHandler(newTextHandler(&buf))
			logger := slog.New(h)
			logger.Info("msg", slog.String(key, "secret-value"))

			out := buf.String()
			if !strings.Contains(out, hmlog.RedactMask) {
				t.Errorf("key %q: expected redaction; got: %s", key, out)
			}
			if strings.Contains(out, "secret-value") {
				t.Errorf("key %q: value must not appear; got: %s", key, out)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Substring matching
// --------------------------------------------------------------------------

func TestRedactingHandler_SubstringMatch(t *testing.T) {
	cases := []string{"db_password", "refresh_token", "set_cookie"}

	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			var buf bytes.Buffer
			h := hmlog.NewRedactingHandler(newTextHandler(&buf))
			logger := slog.New(h)
			logger.Info("msg", slog.String(key, "should-be-hidden"))

			out := buf.String()
			if !strings.Contains(out, hmlog.RedactMask) {
				t.Errorf("key %q: expected redaction via substring; got: %s", key, out)
			}
			if strings.Contains(out, "should-be-hidden") {
				t.Errorf("key %q: value must not appear; got: %s", key, out)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Non-sensitive keys pass through unchanged
// --------------------------------------------------------------------------

func TestRedactingHandler_NonSensitivePassThrough(t *testing.T) {
	var buf bytes.Buffer
	h := hmlog.NewRedactingHandler(newTextHandler(&buf))
	logger := slog.New(h)
	logger.Info("msg", slog.String("central_name", "ccu1"))

	out := buf.String()
	if !strings.Contains(out, "central_name=ccu1") {
		t.Errorf("non-sensitive key should pass through unchanged; got: %s", out)
	}
	if strings.Contains(out, hmlog.RedactMask) {
		t.Errorf("RedactMask must not appear for non-sensitive key; got: %s", out)
	}
}

// --------------------------------------------------------------------------
// Group recursion
// --------------------------------------------------------------------------

func TestRedactingHandler_GroupRecursion(t *testing.T) {
	var buf bytes.Buffer
	h := hmlog.NewRedactingHandler(newTextHandler(&buf))
	logger := slog.New(h)
	logger.Info("msg", slog.Group(
		"oidc",
		slog.String("client_secret", "x"),
		slog.String("issuer", "https://example.com"),
	))

	out := buf.String()
	if !strings.Contains(out, hmlog.RedactMask) {
		t.Errorf("client_secret inside group must be redacted; got: %s", out)
	}
	if strings.Contains(out, "\"x\"") || strings.Contains(out, "=x ") || strings.Contains(out, "=x\n") {
		t.Errorf("raw secret value 'x' must not appear; got: %s", out)
	}
	if !strings.Contains(out, "https://example.com") {
		t.Errorf("issuer (non-sensitive) must pass through; got: %s", out)
	}
}

// --------------------------------------------------------------------------
// WithAttrs inherits redaction
// --------------------------------------------------------------------------

func TestRedactingHandler_WithAttrsInheritsRedaction(t *testing.T) {
	var buf bytes.Buffer
	h := hmlog.NewRedactingHandler(newTextHandler(&buf))
	logger := slog.New(h).With("token", "abc")
	logger.Info("msg")

	out := buf.String()
	if !strings.Contains(out, hmlog.RedactMask) {
		t.Errorf("pre-bound token attr must be redacted; got: %s", out)
	}
	if strings.Contains(out, "abc") {
		t.Errorf("raw token value must not appear; got: %s", out)
	}
}

// --------------------------------------------------------------------------
// Empty / nil pattern list → pass-through
// --------------------------------------------------------------------------

func TestRedactingHandler_EmptyPatternList_PassThrough(t *testing.T) {
	var buf bytes.Buffer
	h := hmlog.NewRedactingHandlerWithKeys(newTextHandler(&buf), nil)
	logger := slog.New(h)
	logger.Info("msg", slog.String("password", "open"))

	out := buf.String()
	if strings.Contains(out, hmlog.RedactMask) {
		t.Errorf("nil pattern list: RedactMask must not appear; got: %s", out)
	}
	if !strings.Contains(out, "open") {
		t.Errorf("nil pattern list: value should pass through; got: %s", out)
	}
}

func TestRedactingHandler_EmptySlicePatternList_PassThrough(t *testing.T) {
	var buf bytes.Buffer
	h := hmlog.NewRedactingHandlerWithKeys(newTextHandler(&buf), []string{})
	logger := slog.New(h)
	logger.Info("msg", slog.String("password", "open"))

	out := buf.String()
	if strings.Contains(out, hmlog.RedactMask) {
		t.Errorf("empty pattern list: RedactMask must not appear; got: %s", out)
	}
}

// Only the custom patterns are active; default sensitive keys must not be masked.
func TestRedactingHandler_CustomPattern_OnlyMatchesConfigured(t *testing.T) {
	var buf bytes.Buffer
	h := hmlog.NewRedactingHandlerWithKeys(newTextHandler(&buf), []string{"pin"})
	logger := slog.New(h)
	logger.Info(
		"msg",
		slog.String("pin", "1234"),
		slog.String("device_pin", "5678"),
		slog.String("password", "clear"),
	)

	out := buf.String()
	if !strings.Contains(out, hmlog.RedactMask) {
		t.Errorf("custom pattern 'pin' must trigger redaction; got: %s", out)
	}
	if strings.Contains(out, "1234") {
		t.Errorf("pin value must not appear; got: %s", out)
	}
	if strings.Contains(out, "5678") {
		t.Errorf("device_pin value must not appear; got: %s", out)
	}
	// password is NOT in the custom list → must pass through.
	if !strings.Contains(out, "clear") {
		t.Errorf("password value should pass through with custom pattern list; got: %s", out)
	}
}

// --------------------------------------------------------------------------
// Nil inner handler panics
// --------------------------------------------------------------------------

func TestNewRedactingHandlerWithKeys_NilInnerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewRedactingHandlerWithKeys(nil, ...) must panic")
		}
	}()
	hmlog.NewRedactingHandlerWithKeys(nil, []string{"password"})
}

func TestNewRedactingHandler_NilInnerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewRedactingHandler(nil) must panic")
		}
	}()
	hmlog.NewRedactingHandler(nil)
}

// --------------------------------------------------------------------------
// IsSensitive
// --------------------------------------------------------------------------

func TestIsSensitive_EmptyKeyReturnsFalse(t *testing.T) {
	var buf bytes.Buffer
	h := hmlog.NewRedactingHandler(newTextHandler(&buf))
	if h.IsSensitive("") {
		t.Error("IsSensitive(\"\") must return false")
	}
}

func TestIsSensitive_TableTests(t *testing.T) {
	var buf bytes.Buffer
	h := hmlog.NewRedactingHandler(newTextHandler(&buf))

	cases := []struct {
		key  string
		want bool
	}{
		{"password", true},
		{"PASSWORD", true},
		{"db_password", true},
		{"token", true},
		{"refresh_token", true},
		{"api_key", true},
		{"X-Api-Key", true},
		{"cookie", true},
		{"set_cookie", true},
		{"client_secret", true},
		{"central_name", false},
		{"issuer", false},
		{"level", false},
		{"msg", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got := h.IsSensitive(tc.key)
			if got != tc.want {
				t.Errorf("IsSensitive(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// WithGroup propagates pattern list
// --------------------------------------------------------------------------

func TestRedactingHandler_WithGroup_PropagatesPatterns(t *testing.T) {
	var buf bytes.Buffer
	h := hmlog.NewRedactingHandler(newTextHandler(&buf))
	logger := slog.New(h.WithGroup("net"))
	logger.Info("msg", slog.String("password", "hidden"), slog.String("host", "localhost"))

	out := buf.String()
	if !strings.Contains(out, hmlog.RedactMask) {
		t.Errorf("WithGroup child must still redact sensitive keys; got: %s", out)
	}
	if strings.Contains(out, "hidden") {
		t.Errorf("raw password value must not appear; got: %s", out)
	}
	if !strings.Contains(out, "localhost") {
		t.Errorf("non-sensitive host must pass through; got: %s", out)
	}
}

// --------------------------------------------------------------------------
// Compile-time interface assertion
// --------------------------------------------------------------------------

func TestRedactingHandler_ImplementsSlogHandler(t *testing.T) {
	var buf bytes.Buffer
	var _ slog.Handler = hmlog.NewRedactingHandler(newTextHandler(&buf))
}

// --------------------------------------------------------------------------
// Enabled delegates to inner
// --------------------------------------------------------------------------

func TestRedactingHandler_EnabledDelegatesToInner(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := hmlog.NewRedactingHandler(inner)

	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Debug should not be enabled when inner handler requires Warn+")
	}
	if !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("Warn should be enabled")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Error should be enabled")
	}
}
