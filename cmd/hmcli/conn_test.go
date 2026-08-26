// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

// TestConnFlagsRequestContextZeroTimeoutHasNoDeadline pins the meaning of
// `-timeout 0`. It is the documented "no deadline" spelling for the HTTP
// client and for `events tail`; handing it to context.WithTimeout would
// instead expire the context before the request is built, so every command
// would fail with "context deadline exceeded" without touching the network.
func TestConnFlagsRequestContextZeroTimeoutHasNoDeadline(t *testing.T) {
	t.Parallel()
	f := &connFlags{timeout: 0}
	ctx, cancel := f.requestContext()
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("zero timeout must not set a deadline")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("context is already done: %v", err)
	}
}

func TestConnFlagsRequestContextPositiveTimeoutSetsDeadline(t *testing.T) {
	t.Parallel()
	f := &connFlags{timeout: 30 * time.Second}
	ctx, cancel := f.requestContext()
	defer cancel()

	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("a positive timeout must set a deadline")
	}
}

// TestDevicesListWithZeroTimeoutReachesTheServer drives the flag through the
// real command path: with the defect present the request never leaves the
// process.
func TestDevicesListWithZeroTimeoutReachesTheServer(t *testing.T) {
	t.Parallel()
	served := false
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices": func(w http.ResponseWriter, _ *http.Request) {
			served = true
			writeJSON200(w, deviceListResponse{Items: []deviceSummary{}, Total: 0})
		},
	})

	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "list", "--host", ts.URL, "--timeout", "0"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !served {
		t.Error("the request never reached the server")
	}
}
