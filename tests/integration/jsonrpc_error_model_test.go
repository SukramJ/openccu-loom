// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// jsonrpc_error_model_test.go covers what the daemon does with the
// error envelope a CCU actually sends.
//
// A CCU answers JSON-RPC in the 1.1 shape — `{name: "JSONRPCError",
// code, message}` with its own codes — not the 2.0 codes the simulator
// used to return. The daemon maps code 400 onto a permission failure
// and short-circuits retry on it, and that mapping had never seen a
// CCU-shaped error: the simulator could not produce one, so the
// privilege path was reachable only by constructing the error value in
// a unit test.

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// startErrorModelCCU boots a simulator answering in the CCU's own
// JSON-RPC envelope (`version: "1.1"`, `{name: "JSONRPCError", code}`)
// when ccuEnvelope is set, and in the simulator's 2.0 default
// otherwise.
func startErrorModelCCU(t *testing.T, ccuEnvelope bool) *godevccu.VirtualCCU {
	t.Helper()

	v, err := godevccu.New(godevccu.Config{
		Mode:          godevccu.BackendModeCCU,
		Host:          "127.0.0.1",
		XMLRPCPort:    godevccu.EphemeralPort,
		JSONRPCPort:   godevccu.EphemeralPort,
		Username:      "Admin",
		Password:      "secret",
		AuthEnabled:   true,
		Devices:       defaultMockDevices,
		SetupDefaults: true,
		Realism:       godevccu.Realism{ErrorModel: ccuEnvelope},
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })
	return v
}

// TestWrongCredentialsSurfaceAsAnAuthFailure pins the classification a
// refused login gets, under both error envelopes a CCU may answer in.
//
// The client logs in on demand, so a call never reaches the wire
// unauthenticated — the refusal an operator actually hits is a wrong
// password, from a changed CCU account or a typo in the config. It must
// arrive as an authentication failure, because that is what the retrier
// short-circuits on and what the health surface reports. Classified as
// an ordinary client error it becomes a login retried through the full
// backoff, and the operator reads a slow, flaky CCU instead of a
// credential they can fix.
//
// Both envelopes are exercised deliberately. Which one a CCU sends is
// not something the daemon chooses, and the auth path must not depend
// on it — this test would have passed against the 2.0 codes alone,
// which is exactly why it runs against the CCU's own too.
func TestWrongCredentialsSurfaceAsAnAuthFailure(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ccuEnvelope bool
	}{
		{"CCU 1.1 error model", true},
		{"JSON-RPC 2.0 default", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertWrongPasswordIsAuthFailure(t, tc.ccuEnvelope)
		})
	}
}

// assertWrongPasswordIsAuthFailure drives one login refusal.
func assertWrongPasswordIsAuthFailure(t *testing.T, ccuEnvelope bool) {
	t.Helper()

	v := startErrorModelCCU(t, ccuEnvelope)

	client, err := jsonrpc.New(jsonrpc.Config{
		Endpoint: "http://" + v.JSONRPCAddr().String() + "/api/homematic.cgi",
		Username: "Admin",
		Password: "the-wrong-one",
	})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	callErr := client.Login(ctx)
	if callErr == nil {
		t.Fatal("login with a wrong password succeeded; the simulator is not checking " +
			"credentials, so this test would assert nothing")
	}

	if !errors.Is(callErr, hmerr.ErrAuthFailure) {
		var jsonErr *hmerr.JSONRPCError
		code := 0
		if errors.As(callErr, &jsonErr) {
			code = jsonErr.Code
		}
		t.Errorf("a wrong password surfaced as %v (json-rpc code %d), not as an "+
			"authentication failure — the retrier repeats it through the backoff and the "+
			"operator reads a flaky CCU instead of a credential they can fix", callErr, code)
	}
	if errors.Is(callErr, hmerr.ErrInternalBackendException) {
		t.Error("a wrong password classifies as an internal backend fault; the CCU is fine, " +
			"the credential is not")
	}
}
