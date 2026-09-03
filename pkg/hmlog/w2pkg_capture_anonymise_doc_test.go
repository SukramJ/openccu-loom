// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// w2PkgAnonymiseShapedPhrase is this repository's term of art for a value
// class an anonymiser actually replaces with a hash. It is used that way for
// the RPC recorder, which regex-matches Homematic addresses, and in
// SPECIFICATION.md for the diagnostics dump. A capture-sink doc using it for
// device addresses therefore promises address hashing, not merely
// "identifying data".
const w2PkgAnonymiseShapedPhrase = "device-address-shaped"

// w2PkgCaptureAnonymiseKeys returns, for each attribute key, whether the
// capture sink's encoder actually replaced its value with a hash. The answer
// is measured by running one record through the production encoder — nothing
// here reads the switch statement or a comment.
func w2PkgCaptureAnonymiseKeys(t *testing.T, keys ...string) map[string]bool {
	t.Helper()

	var inner bytes.Buffer
	tee := hmlog.NewTeeHandler(slog.NewJSONHandler(&inner, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sink := hmlog.NewCaptureSink(0, true)
	tee.Attach(sink)

	attrs := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		attrs = append(attrs, k, "w2pkg-probe-"+k)
	}
	slog.New(tee).Info("w2pkg anonymise probe", attrs...)

	snap := sink.Snapshot()
	if len(snap) == 0 {
		t.Fatal("capture sink produced no record")
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimRight(snap, "\n"), &rec); err != nil {
		t.Fatalf("unmarshal capture record: %v — raw %q", err, snap)
	}

	got := make(map[string]bool, len(keys))
	for _, k := range keys {
		v, ok := rec[k].(string)
		if !ok {
			t.Fatalf("key %q missing from capture record %v", k, rec)
		}
		switch {
		case strings.HasPrefix(v, "anon:"):
			got[k] = true
		case v == "w2pkg-probe-"+k:
			got[k] = false
		default:
			t.Fatalf("key %q = %q: neither the probe value nor an anon: hash", k, v)
		}
	}
	return got
}

// TestW2PkgCaptureAnonymiseDocMatchesTheEncoder ties the doc a caller reads —
// the anonymise field, the Anonymise accessor and AnonymiseToken — to what
// the encoder measurably does with a device address. The direction of the
// assertion is taken from the measurement, so the guard follows the code: if
// address hashing is ever implemented, the doc is then required to say so.
func TestW2PkgCaptureAnonymiseDocMatchesTheEncoder(t *testing.T) {
	t.Parallel()

	measured := w2PkgCaptureAnonymiseKeys(t, "subject", "username", "remote", "device_address", "host", "interface_id")

	// Positive control: the sink does anonymise something, so an absent
	// hash below is a statement about the key, not about a dead flag.
	if !measured["subject"] {
		t.Fatalf("subject was not hashed — the anonymise flag reached the encoder as off, so this guard measures nothing")
	}

	src, err := os.ReadFile("capture.go")
	if err != nil {
		t.Fatalf("read capture.go: %v", err)
	}
	docs := w2PkgDocComments(t, string(src))

	hashesAddresses := measured["device_address"]
	promises := strings.Contains(docs, w2PkgAnonymiseShapedPhrase)

	switch {
	case hashesAddresses && !promises:
		t.Errorf("the encoder hashes device_address but no capture doc comment says so; state it with %q", w2PkgAnonymiseShapedPhrase)
	case !hashesAddresses && promises:
		t.Errorf("capture.go doc comments promise %q anonymisation, but the encoder left device_address in clear text (measured: device_address=%v, host=%v, interface_id=%v); a reader of the field, the accessor or the OpenAPI description is told the CCU fleet is hidden in an archive that shows it",
			w2PkgAnonymiseShapedPhrase, measured["device_address"], measured["host"], measured["interface_id"])
	}
}

// w2PkgDocComments returns every "//" comment line of the file joined by
// newlines, lower-cased, so a phrase check is not defeated by capitalisation
// or by the line the phrase happens to wrap on.
func w2PkgDocComments(t *testing.T, src string) string {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "//") {
			continue
		}
		b.WriteString(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))))
		b.WriteString(" ")
	}
	return b.String()
}
