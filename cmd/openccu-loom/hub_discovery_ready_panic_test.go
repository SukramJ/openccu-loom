// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestRunHubDiscoveryRestartRecordsTheStack pins that a panic in the
// hub-discovery re-wire is recorded with its stack.
//
// The re-wire runs on the debounce goroutine, the recover consumes the
// panic, and the daemon keeps going — with a hub plane that Start tore
// down and then failed to rebuild. The log line is the only trace that
// outlives the event, so it has to carry enough to locate the fault; a
// bare panic value tells an operator that something broke and nothing
// about where.
func TestRunHubDiscoveryRestartRecordsTheStack(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	runHubDiscoveryRestart(context.Background(), func(context.Context) {
		var publisher *panicProbe
		publisher.rewire() // nil-receiver dereference, the observed failure shape
	}, logger)

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log record is not valid JSON: %v (%q)", err, buf.String())
	}
	if rec["msg"] != "mqtt.hub_discovery.restart_on_ready.panic" {
		t.Fatalf("msg = %v, want mqtt.hub_discovery.restart_on_ready.panic", rec["msg"])
	}
	stack, _ := rec["stack"].(string)
	if !strings.Contains(stack, "runHubDiscoveryRestart") {
		t.Errorf("stack does not name the recovering frame, so it cannot locate the fault: %q", stack)
	}
}

// panicProbe stands in for a collaborator whose method dereferences its
// receiver — the shape every observed restart_on_ready panic has taken.
type panicProbe struct{ field int }

func (p *panicProbe) rewire() { p.field++ }
