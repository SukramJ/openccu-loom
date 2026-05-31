// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build cuxd_live

// Package integration — live wire-compatibility test of the BIN-RPC
// codec against a real CUxD instance.
//
// # Purpose
//
// The local self-roundtrip tests in
// internal/client/transport/binrpc/client_test.go prove encoder ↔ decoder
// symmetry. They do not prove that what we put on the wire is what CUxD
// actually expects. This test closes that gap: it dials a live CUxD
// daemon and exchanges read-only frames with it.
//
// # Running
//
// Not part of `make test`, `make integration`, or CI. Run explicitly:
//
//	OPENCCU_LOOM_LIVE_CUXD_ADDR=172.18.X.XX:8701 \
//	    go test -tags=cuxd_live -timeout=30s \
//	      ./tests/integration/... -run TestCuxdLive -v
//
// # Required environment variables
//
//	OPENCCU_LOOM_LIVE_CUXD_ADDR  host:port of the CUxD BIN-RPC endpoint
//	                            (e.g. "172.18.X.XX:8701" — CUxD's default
//	                            BIN-RPC port). If unset, every
//	                            TestCuxdLive* is skipped.
//
// # Scope and safety
//
// Read-only. The test calls only:
//
//   - system.listMethods       — returns []string, exercises Array+String decode
//   - system.methodHelp("ping") — returns string, exercises String decode and
//     argument round-trip
//
// No setValue, no putParamset, no init() (which would register us as a
// CUxD callback target). The test never mutates CCU/CUxD state.
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// liveCuxdAddr returns the configured CUxD address or skips the test if
// the env var is unset.
func liveCuxdAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("OPENCCU_LOOM_LIVE_CUXD_ADDR")
	if addr == "" {
		t.Skip("set OPENCCU_LOOM_LIVE_CUXD_ADDR (e.g. 172.18.4.39:8701) to enable live-CUxD smoke")
	}
	return addr
}

func newLiveCuxdClient(t *testing.T) *binrpc.Client {
	t.Helper()
	c, err := binrpc.NewClient(binrpc.Config{
		Addr:        liveCuxdAddr(t),
		Interface:   "CUxD",
		IOTimeout:   10 * time.Second,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestCuxdLive_ListMethods proves that our request encoder produces a
// frame CUxD accepts and that our response decoder parses CUxD's reply
// without error. It also asserts a few invariants on the returned method
// catalogue so a silently-empty response cannot pass.
func TestCuxdLive_ListMethods(t *testing.T) {
	c := newLiveCuxdClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	v, err := c.Call(ctx, "system.listMethods", nil)
	if err != nil {
		t.Fatalf("system.listMethods: %v", err)
	}

	arr, err := xmlrpc.AsArray(v)
	if err != nil {
		t.Fatalf("response not an array: %v (raw=%v)", err, v)
	}
	if len(arr) == 0 {
		t.Fatal("system.listMethods returned empty array")
	}

	seen := make(map[string]bool, len(arr))
	for i, elem := range arr {
		s, err := xmlrpc.AsString(elem)
		if err != nil {
			t.Fatalf("element %d not a string: %v", i, err)
		}
		seen[s] = true
	}

	// Methods every CUxD install must expose. The set was confirmed
	// against CUxD 1.18 — these are part of CUxD's canonical XML-RPC
	// surface and have not changed for years.
	for _, mustHave := range []string{
		"system.listMethods",
		"system.methodHelp",
		"ping",
		"init",
		"listDevices",
		"getValue",
	} {
		if !seen[mustHave] {
			t.Errorf("CUxD did not advertise %q in system.listMethods (got %d methods)", mustHave, len(arr))
		}
	}
	t.Logf("CUxD at %s advertises %d methods", os.Getenv("OPENCCU_LOOM_LIVE_CUXD_ADDR"), len(arr))
}

// TestCuxdLive_MethodHelp exercises the string-argument round-trip and
// string-response decode path against a method every CUxD install
// supports. CUxD returns whatever help text the method has — often an
// empty string. We only assert that the call completes, the response is
// a string (so the type tag was correct), and there is no transport
// error. This proves the empty-length string decode path also works on
// the wire.
func TestCuxdLive_MethodHelp(t *testing.T) {
	c := newLiveCuxdClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	v, err := c.Call(ctx, "system.methodHelp", []xmlrpc.Value{
		xmlrpc.StringValue("ping"),
	})
	if err != nil {
		t.Fatalf("system.methodHelp(ping): %v", err)
	}

	s, err := xmlrpc.AsString(v)
	if err != nil {
		t.Fatalf("response not a string: %v (raw=%v)", err, v)
	}
	t.Logf("system.methodHelp(ping) = %q (length=%d)", s, len(s))
}
