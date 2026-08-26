// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// hub_wiring_serial_test.go covers resolveCCUSerial in hub_wiring.go:
// immediate success, exhausted attempts on a permanently-empty serial
// (via context cancellation to avoid waiting 5×serialResolveBackoff),
// and prompt return on a cancelled context.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
)

// newSerialRunner builds a rega.Runner whose fake CCU always returns
// the given JSON string as the script result.
func newSerialRunner(t *testing.T, resultJSON string) *rega.Runner {
	t.Helper()
	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"ReGa.runScript": func(_ map[string]any) any {
			return resultJSON
		},
	})
	jc := newJSONRPCClient(t, srv.URL)
	r, err := rega.NewRunner(rega.Config{Client: jc})
	if err != nil {
		t.Fatalf("rega.NewRunner: %v", err)
	}
	return r
}

// TestResolveCCUSerialImmediateSuccess verifies that a non-empty, non-"unknown"
// serial is returned on the very first attempt.
func TestResolveCCUSerialImmediateSuccess(t *testing.T) {
	t.Parallel()

	r := newSerialRunner(t, `{"serial":"ABC1234567"}`)
	serial, err := resolveCCUSerial(context.Background(), r, slog.Default())
	if err != nil {
		t.Fatalf("resolveCCUSerial: %v", err)
	}
	if serial != "ABC1234567" {
		t.Errorf("serial = %q, want ABC1234567", serial)
	}
}

// TestResolveCCUSerialUnknownReturnsError verifies that a CCU that always
// returns "unknown" is treated as a failure. To avoid waiting 5×1 s the test
// uses a context with a short deadline so the first backoff is interrupted
// and the function returns ctx.Err() rather than the empty-serial error
// (both are non-nil failures; what matters is that it does not succeed).
func TestResolveCCUSerialUnknownReturnsError(t *testing.T) {
	t.Parallel()

	r := newSerialRunner(t, `{"serial":"unknown"}`)
	// Short deadline aborts after the first failed attempt during the backoff
	// wait, keeping the test sub-second while still exercising the error path.
	ctx, cancel := context.WithTimeout(context.Background(), 50*1000*1000) // 50 ms
	defer cancel()

	_, err := resolveCCUSerial(ctx, r, slog.Default())
	if err == nil {
		t.Fatal("resolveCCUSerial with always-unknown serial must return an error")
	}
}

// TestResolveCCUSerialEmptyReturnsError verifies that a CCU that always returns
// an empty serial is treated as a failure. Same short-deadline trick as above.
func TestResolveCCUSerialEmptyReturnsError(t *testing.T) {
	t.Parallel()

	r := newSerialRunner(t, `{"serial":""}`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*1000*1000) // 50 ms
	defer cancel()

	_, err := resolveCCUSerial(ctx, r, slog.Default())
	if err == nil {
		t.Fatal("resolveCCUSerial with always-empty serial must return an error")
	}
}

// TestResolveCCUSerialCancelledContextReturnsPromptly verifies that a
// pre-cancelled context causes resolveCCUSerial to return ctx.Err() without
// making a successful attempt (the fake would return a valid serial, but the
// context is already done before the first attempt can succeed via the
// underlying HTTP transport — or the function gates on ctx.Done() before
// the first attempt).
//
// Note: with a pre-cancelled context the JSON-RPC transport itself may
// surface the cancellation rather than the function's post-attempt gate.
// Either way, resolveCCUSerial must return a non-nil error and do so quickly.
func TestResolveCCUSerialCancelledContextReturnsPromptly(t *testing.T) {
	t.Parallel()

	r := newSerialRunner(t, `{"serial":"DEADBEEF01"}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := resolveCCUSerial(ctx, r, slog.Default())
	if err == nil {
		t.Fatal("resolveCCUSerial with cancelled context must return an error")
	}
}

// TestResolveCCUSerialTruncatesLongSerial verifies that a CCU serial longer
// than 10 characters is truncated to its last 10 characters (via GetSerial),
// so the returned value matches the canonical WebUI form.
func TestResolveCCUSerialTruncatesLongSerial(t *testing.T) {
	t.Parallel()

	r := newSerialRunner(t, `{"serial":"3014F711A0001F58A99BC0DE"}`)
	serial, err := resolveCCUSerial(context.Background(), r, slog.Default())
	if err != nil {
		t.Fatalf("resolveCCUSerial: %v", err)
	}
	if serial != "58A99BC0DE" {
		t.Errorf("serial = %q, want last-10 truncation 58A99BC0DE", serial)
	}
}
