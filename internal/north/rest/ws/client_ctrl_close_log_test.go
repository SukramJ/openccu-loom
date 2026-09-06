// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestCtrlOverflowWarnsDuringAnOpenGapEpisode pins that a connection
// dropped for control-plane backpressure always leaves a log line — the
// correlated case (a domain-event overflow episode already open) is
// exactly when a shared one-shot flag would swallow it and make the
// connection vanish silently.
func TestCtrlOverflowWarnsDuringAnOpenGapEpisode(t *testing.T) {
	c, logs, _ := newBackpressureClient(t, 1)

	// Open a domain-overflow episode first; it consumes its own flag.
	c.signalGap("device", 1)
	if n := countCtrlWarnings(t, logs); n != 0 {
		t.Fatalf("gap episode produced %d control warnings, want 0", n)
	}

	// Now overflow the control queue while that episode is still open.
	c.enqueueCtrl(opText, []byte(`{"op":"a"}`))
	c.enqueueCtrl(opText, []byte(`{"op":"b"}`))

	if n := countCtrlWarnings(t, logs); n != 1 {
		t.Fatalf("control-plane close warnings = %d, want 1\nlogs:\n%s", n, logs.String())
	}
}

// countCtrlWarnings counts ws.backpressure records carrying kind=control.
func countCtrlWarnings(t *testing.T, logs *bytes.Buffer) int {
	t.Helper()
	n := 0
	for line := range bytes.SplitSeq(bytes.TrimRight(logs.Bytes(), "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line is not valid JSON: %v (%q)", err, line)
		}
		if rec["msg"] == "ws.backpressure" && rec["kind"] == "control" {
			n++
		}
	}
	return n
}
