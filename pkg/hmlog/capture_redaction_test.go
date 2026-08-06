// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// TestCaptureAndLiveLogCarryRedaction pins that every branch of the
// logging stack masks secrets: stdout, the operator-triggered capture
// archive, and the live-log ring the diagnostics endpoint streams.
//
// The tee used to sit ABOVE the redactor, so it mirrored the record
// before masking ran. A secret shown as ***REDACTED*** on stdout was
// then served in cleartext by GET /api/v1/diagnostics/logs and inside
// the capture download — the two surfaces an operator is most likely to
// forward to a bug report. Both stayed silent about it; the stdout
// assertion in every existing test passed.
func TestCaptureAndLiveLogCarryRedaction(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	stack := hmlog.BuildFullStack(hmlog.StackOptions{
		Writer: &stdout,
		Format: hmlog.FormatJSON,
	}, slog.LevelDebug)

	sink := hmlog.NewCaptureSink(0, false)
	stack.Tee.Attach(sink)

	const secret = "s3cr3t-value-do-not-leak"
	// Both shapes matter: an attribute on the record itself, and one
	// pre-bound through With(...) — WithAttrs is a separate code path.
	stack.Logger.With(slog.String("bound_password", secret)).
		Warn("oidc handshake failed", slog.String("client_secret", secret))

	if got := stdout.String(); strings.Contains(got, secret) {
		t.Errorf("stdout leaked the secret: %s", got)
	}

	captured := string(sink.Snapshot())
	if captured == "" {
		t.Fatal("capture sink recorded nothing; the tee is not in the chain")
	}
	if strings.Contains(captured, secret) {
		t.Errorf("capture archive leaked the secret: %s", captured)
	}

	var live strings.Builder
	for _, rec := range stack.Live.Snapshot(0, slog.LevelDebug) {
		live.WriteString(rec.Msg)
		for k, v := range rec.Attrs {
			live.WriteString(k)
			live.WriteString(fmt.Sprint(v))
		}
	}
	if live.Len() == 0 {
		t.Fatal("live log recorded nothing; the ring is not in the chain")
	}
	if strings.Contains(live.String(), secret) {
		t.Errorf("live log leaked the secret: %s", live.String())
	}
}
