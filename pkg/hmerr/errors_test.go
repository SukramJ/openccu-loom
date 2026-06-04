// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmerr

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestWithContextNilPassthrough(t *testing.T) {
	if err := WithContext(nil, Context{}); err != nil {
		t.Fatalf("WithContext(nil) = %v, want nil", err)
	}
}

func TestWithContextUnwrap(t *testing.T) {
	base := fmt.Errorf("dial tcp: %w", ErrNoConnection)
	wrapped := WithContext(base, Context{
		Protocol:  "xml-rpc",
		Method:    "init",
		Host:      "ccu:2010",
		Interface: "HmIP-RF",
	})
	if !errors.Is(wrapped, ErrNoConnection) {
		t.Fatalf("errors.Is(wrapped, ErrNoConnection) = false, want true")
	}

	ctx, ok := ErrorContext(wrapped)
	if !ok {
		t.Fatal("ErrorContext should have reported context presence")
	}
	if ctx.Protocol != "xml-rpc" || ctx.Method != "init" || ctx.Host != "ccu:2010" || ctx.Interface != "HmIP-RF" {
		t.Fatalf("unexpected context: %+v", ctx)
	}
}

func TestErrorContextMissing(t *testing.T) {
	if _, ok := ErrorContext(errors.New("bare")); ok {
		t.Fatal("ErrorContext should report false for plain errors")
	}
	if _, ok := ErrorContext(nil); ok {
		t.Fatal("ErrorContext(nil) should report false")
	}
}

func TestXMLRPCFaultIsClientException(t *testing.T) {
	f := &XMLRPCFault{Code: -1, Message: "unknown method"}
	if !errors.Is(f, ErrClientException) {
		t.Fatal("xml-rpc fault should be classified as ErrClientException")
	}
	if errors.Is(f, ErrAuthFailure) {
		t.Fatal("xml-rpc fault must not be ErrAuthFailure")
	}

	wrapped := WithContext(f, Context{Protocol: "xml-rpc", Method: "setValue"})
	if !errors.Is(wrapped, ErrClientException) {
		t.Fatal("wrapped fault should still classify as ErrClientException")
	}
	var unwrapped *XMLRPCFault
	if !errors.As(wrapped, &unwrapped) {
		t.Fatal("errors.As should recover the *XMLRPCFault")
	}
	if unwrapped.Code != -1 {
		t.Fatalf("fault code=%d, want -1", unwrapped.Code)
	}
}

func TestJSONRPCErrorClassification(t *testing.T) {
	internal := &JSONRPCError{Code: -32603, Message: "boom"}
	if !errors.Is(internal, ErrInternalBackendException) {
		t.Fatal("code -32603 should be ErrInternalBackendException")
	}
	if errors.Is(internal, ErrClientException) {
		t.Fatal("code -32603 must not also classify as ErrClientException")
	}

	client := &JSONRPCError{Code: -32600, Message: "bad request"}
	if !errors.Is(client, ErrClientException) {
		t.Fatal("non-internal code should classify as ErrClientException")
	}
	if errors.Is(client, ErrInternalBackendException) {
		t.Fatal("non-internal code must not classify as ErrInternalBackendException")
	}
}

func TestContextualErrorFormat(t *testing.T) {
	err := WithContext(ErrAuthFailure, Context{
		Protocol: "json-rpc",
		Method:   "login",
		Host:     "ccu",
	})
	const want = "json-rpc login @ ccu: authentication failed"
	if got := err.Error(); got != want {
		t.Fatalf("Error()=%q, want %q", got, want)
	}
}

// TestErrDescriptionNotFoundSentinel verifies that the sentinel
// is comparable via errors.Is and carries an appropriate message.
func TestErrDescriptionNotFoundSentinel(t *testing.T) {
	wrapped := fmt.Errorf("coordinator: %w", ErrDescriptionNotFound)
	if !errors.Is(wrapped, ErrDescriptionNotFound) {
		t.Fatal("errors.Is(wrapped, ErrDescriptionNotFound) = false, want true")
	}
	if ErrDescriptionNotFound.Error() == "" {
		t.Fatal("ErrDescriptionNotFound.Error() is empty")
	}
}

// TestContextParamsField verifies that hmerr.Context carries the Params
// field and that it is preserved through WithContext.
func TestContextParamsField(t *testing.T) {
	params := map[string]any{"interface": "HmIP-RF", "callbackURL": "http://host:8120/cb"}
	ctx := Context{
		Protocol:  "xml-rpc",
		Method:    "init",
		Host:      "192.168.1.1:2010",
		Interface: "HmIP-RF",
		Params:    params,
	}
	wrapped := WithContext(errors.New("test"), ctx)
	got, ok := ErrorContext(wrapped)
	if !ok {
		t.Fatal("ErrorContext should be present")
	}
	if got.Params == nil {
		t.Fatal("Params field should not be nil")
	}
	gotMap, ok := got.Params.(map[string]any)
	if !ok {
		t.Fatalf("Params should be map[string]any, got %T", got.Params)
	}
	if gotMap["interface"] != "HmIP-RF" {
		t.Fatalf("Params interface = %q; want HmIP-RF", gotMap["interface"])
	}
}

// TestContextFmt verifies Context.Fmt formats correctly with and
// without sanitization.
func TestContextFmt(t *testing.T) {
	ctx := Context{
		Protocol:  "xml-rpc",
		Method:    "init",
		Host:      "192.168.1.55:2010",
		Interface: "HmIP-RF",
	}
	plain := ctx.Fmt(false)
	if plain == "" {
		t.Fatal("Fmt(false) returned empty string")
	}
	// Plain format should include the raw IP.
	if !contains(plain, "192.168.1.55") {
		t.Errorf("Fmt(false) = %q; expected to contain raw IP", plain)
	}
	// Sanitized format should NOT include the raw IP.
	sanitized := ctx.Fmt(true)
	if contains(sanitized, "192.168.1.55") {
		t.Errorf("Fmt(true) = %q; expected IP to be redacted", sanitized)
	}
	if !contains(sanitized, "xml-rpc") {
		t.Errorf("Fmt(true) = %q; expected protocol tag", sanitized)
	}
}

// TestContextFmtSanitized verifies FmtSanitized is equivalent to
// Fmt(true).
func TestContextFmtSanitized(t *testing.T) {
	ctx := Context{
		Protocol:  "json-rpc",
		Method:    "Session.login",
		Host:      "10.0.0.1",
		Interface: "BidCos-RF",
	}
	if got, want := ctx.FmtSanitized(), ctx.Fmt(true); got != want {
		t.Errorf("FmtSanitized() = %q; want %q", got, want)
	}
}

// TestContextFmtNoInterface verifies that Fmt omits the interface clause
// when Interface is empty.
func TestContextFmtNoInterface(t *testing.T) {
	ctx := Context{Protocol: "xml-rpc", Method: "ping", Host: "ccu:2010"}
	f := ctx.Fmt(false)
	// Should not contain a trailing "()" from empty interface.
	if contains(f, "()") {
		t.Errorf("Fmt() = %q; unexpected empty interface parentheses", f)
	}
	if !contains(f, "ping") {
		t.Errorf("Fmt() = %q; missing method name", f)
	}
}

// contains is a helper for substring checks so we don't import strings.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	}()
}

func TestErrNoClients_IsDistinct(t *testing.T) {
	if errors.Is(ErrNoClients, ErrClientException) {
		t.Error("ErrNoClients must not match ErrClientException")
	}
}

func TestErrNoClients_WrappedIsDetectable(t *testing.T) {
	wrapped := fmt.Errorf("coordinator: %w", ErrNoClients)
	if !errors.Is(wrapped, ErrNoClients) {
		t.Error("errors.Is(wrapped, ErrNoClients) = false, want true")
	}
}

func TestErrCommandSuperseded_IsDistinct(t *testing.T) {
	if errors.Is(ErrCommandSuperseded, ErrClientException) {
		t.Error("ErrCommandSuperseded must not match ErrClientException")
	}
}

func TestErrCommandSuperseded_WrappedIsDetectable(t *testing.T) {
	wrapped := fmt.Errorf("retry: %w", ErrCommandSuperseded)
	if !errors.Is(wrapped, ErrCommandSuperseded) {
		t.Error("errors.Is(wrapped, ErrCommandSuperseded) = false, want true")
	}
}

func TestErrValidation_IsDistinct(t *testing.T) {
	if errors.Is(ErrValidation, ErrClientException) {
		t.Error("ErrValidation must not match ErrClientException")
	}
	if errors.Is(ErrValidation, ErrNoConnection) {
		t.Error("ErrValidation must not match ErrNoConnection")
	}
}

func TestErrValidation_WrappedIsDetectable(t *testing.T) {
	wrapped := fmt.Errorf("parameter range: %w", ErrValidation)
	if !errors.Is(wrapped, ErrValidation) {
		t.Error("errors.Is(wrapped, ErrValidation) = false, want true")
	}
}

func TestErrValidation_MessageIsStable(t *testing.T) {
	if ErrValidation.Error() != "validation failed" {
		t.Errorf("ErrValidation.Error() = %q; want %q", ErrValidation.Error(), "validation failed")
	}
}

// ---------------------------------------------------------------------------
// Sentinel round-trips
// ---------------------------------------------------------------------------

func TestSentinelRoundTrips(t *testing.T) {
	t.Parallel()

	sentinels := []error{
		ErrAuthFailure,
		ErrNoConnection,
		ErrCircuitBreakerOpen,
		ErrClientException,
		ErrInternalBackendException,
		ErrUnsupported,
		ErrParameterHidden,
	}

	for _, s := range sentinels {
		t.Run(s.Error(), func(t *testing.T) {
			t.Parallel()
			// Direct match.
			if !errors.Is(s, s) {
				t.Fatalf("errors.Is(s, s) = false for %v", s)
			}
			// Wrapped match.
			wrapped := fmt.Errorf("transport: %w", s)
			if !errors.Is(wrapped, s) {
				t.Fatalf("errors.Is(wrapped, %v) = false", s)
			}
			// Different sentinel must not match.
			other := errors.New("other")
			if errors.Is(s, other) {
				t.Fatalf("errors.Is(%v, other) = true, want false", s)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// XMLRPCFaultCode.IsRetryable
// ---------------------------------------------------------------------------

func TestXMLRPCFaultCodeIsRetryableTrue(t *testing.T) {
	t.Parallel()

	retryable := []XMLRPCFaultCode{
		XMLRPCFaultUnreach,
		XMLRPCFaultTimeout,
		XMLRPCFaultDutyCycle,
		XMLRPCFaultDeviceOutOfRange,
		XMLRPCFaultTransmissionPending,
	}

	for _, c := range retryable {
		t.Run(fmt.Sprintf("code_%d", int(c)), func(t *testing.T) {
			t.Parallel()
			if !c.IsRetryable() {
				t.Fatalf("XMLRPCFaultCode(%d).IsRetryable() = false, want true", int(c))
			}
		})
	}
}

func TestXMLRPCFaultCodeIsRetryableFalse(t *testing.T) {
	t.Parallel()

	nonRetryable := []XMLRPCFaultCode{
		0,
		1,
		-999,
		42,
	}

	for _, c := range nonRetryable {
		t.Run(fmt.Sprintf("code_%d", int(c)), func(t *testing.T) {
			t.Parallel()
			if c.IsRetryable() {
				t.Fatalf("XMLRPCFaultCode(%d).IsRetryable() = true, want false", int(c))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// XMLRPCFault methods
// ---------------------------------------------------------------------------

func TestXMLRPCFaultError(t *testing.T) {
	t.Parallel()

	f := &XMLRPCFault{Code: -8, Message: "INSUFFICIENT_DUTYCYCLE"}
	want := "xml-rpc fault -8: INSUFFICIENT_DUTYCYCLE"
	if got := f.Error(); got != want {
		t.Fatalf("XMLRPCFault.Error() = %q, want %q", got, want)
	}
}

func TestXMLRPCFaultFaultCode(t *testing.T) {
	t.Parallel()

	f := &XMLRPCFault{Code: -9, Message: "DEVICE_OUT_OF_RANGE"}
	if got := f.FaultCode(); got != XMLRPCFaultDeviceOutOfRange {
		t.Fatalf("FaultCode() = %d, want %d", int(got), int(XMLRPCFaultDeviceOutOfRange))
	}
}

func TestXMLRPCFaultIsRetryable(t *testing.T) {
	t.Parallel()

	t.Run("retryable", func(t *testing.T) {
		t.Parallel()
		f := &XMLRPCFault{Code: -1, Message: "unreachable"}
		if !f.IsRetryable() {
			t.Fatal("IsRetryable() = false for code -1, want true")
		}
	})

	t.Run("not_retryable", func(t *testing.T) {
		t.Parallel()
		f := &XMLRPCFault{Code: 0, Message: "unknown"}
		if f.IsRetryable() {
			t.Fatal("IsRetryable() = true for code 0, want false")
		}
	})
}

func TestXMLRPCFaultAllRetryableCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code    int
		message string
		want    bool
	}{
		{-1, "unreach", true},
		{-2, "timeout", true},
		{-8, "duty cycle", true},
		{-9, "out of range", true},
		{-10, "transmission pending", true},
		{-3, "other", false},
		{0, "ok", false},
		{100, "permanent", false},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("code_%d", tc.code), func(t *testing.T) {
			t.Parallel()
			f := &XMLRPCFault{Code: tc.code, Message: tc.message}
			if got := f.IsRetryable(); got != tc.want {
				t.Fatalf("XMLRPCFault{Code: %d}.IsRetryable() = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// JSONRPCError.Error formatting
// ---------------------------------------------------------------------------

func TestJSONRPCErrorErrorWithData(t *testing.T) {
	t.Parallel()

	e := &JSONRPCError{Code: -32600, Message: "Invalid Request", Data: "extra detail"}
	want := "json-rpc error -32600: Invalid Request (extra detail)"
	if got := e.Error(); got != want {
		t.Fatalf("JSONRPCError.Error() with Data = %q, want %q", got, want)
	}
}

func TestJSONRPCErrorErrorWithoutData(t *testing.T) {
	t.Parallel()

	e := &JSONRPCError{Code: -32601, Message: "Method not found"}
	want := "json-rpc error -32601: Method not found"
	if got := e.Error(); got != want {
		t.Fatalf("JSONRPCError.Error() without Data = %q, want %q", got, want)
	}
}

func TestJSONRPCErrorInternalErrorWithData(t *testing.T) {
	t.Parallel()

	e := &JSONRPCError{Code: -32603, Message: "Internal error", Data: "stack trace"}
	want := "json-rpc error -32603: Internal error (stack trace)"
	if got := e.Error(); got != want {
		t.Fatalf("JSONRPCError.Error() with internal code + Data = %q, want %q", got, want)
	}
	if !errors.Is(e, ErrInternalBackendException) {
		t.Fatal("code -32603 with Data should still be ErrInternalBackendException")
	}
}

// ---------------------------------------------------------------------------
// ContextualError.Error with various Context field combinations
// ---------------------------------------------------------------------------

func TestContextualErrorFormatVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ctx  Context
		err  error
		want string
	}{
		{
			name: "all_fields",
			ctx:  Context{Protocol: "bin-rpc", Method: "setValue", Host: "ccu:2122", Interface: "CUxD"},
			err:  ErrNoConnection,
			want: "bin-rpc setValue @ ccu:2122: no connection",
		},
		{
			name: "no_interface",
			ctx:  Context{Protocol: "xml-rpc", Method: "getParamset", Host: "homematic:2010"},
			err:  ErrAuthFailure,
			want: "xml-rpc getParamset @ homematic:2010: authentication failed",
		},
		{
			name: "empty_host",
			ctx:  Context{Protocol: "json-rpc", Method: "login", Host: ""},
			err:  ErrCircuitBreakerOpen,
			want: "json-rpc login @ : circuit breaker open",
		},
		{
			name: "empty_protocol_method",
			ctx:  Context{Protocol: "", Method: "", Host: "ccu"},
			err:  ErrUnsupported,
			want: "  @ ccu: operation not supported",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wrapped := WithContext(tc.err, tc.ctx)
			if got := wrapped.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ErrorContext — ContextualError in a deeper chain
// ---------------------------------------------------------------------------

func TestErrorContextDeepChain(t *testing.T) {
	t.Parallel()

	inner := WithContext(ErrNoConnection, Context{Protocol: "xml-rpc", Method: "init", Host: "ccu"})
	outer := fmt.Errorf("dial: %w", inner)

	ctx, ok := ErrorContext(outer)
	if !ok {
		t.Fatal("ErrorContext should find context through fmt.Errorf wrapping")
	}
	if ctx.Protocol != "xml-rpc" {
		t.Fatalf("ctx.Protocol = %q, want %q", ctx.Protocol, "xml-rpc")
	}
}

// ---------------------------------------------------------------------------
// ExceptionToFailureReason
// ---------------------------------------------------------------------------

func TestExceptionToFailureReason_NilIsNone(t *testing.T) {
	t.Parallel()
	if got := ExceptionToFailureReason(nil, nil); got != "none" {
		t.Fatalf("nil err => %q, want %q", got, "none")
	}
}

func TestExceptionToFailureReason_AuthFailure(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("transport: %w", ErrAuthFailure)
	if got := ExceptionToFailureReason(err, nil); got != "auth" {
		t.Fatalf("auth err => %q, want %q", got, "auth")
	}
}

func TestExceptionToFailureReason_NoConnection(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("dial: %w", ErrNoConnection)
	if got := ExceptionToFailureReason(err, nil); got != "network" {
		t.Fatalf("no-connection err => %q, want %q", got, "network")
	}
}

func TestExceptionToFailureReason_CircuitBreaker(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("client: %w", ErrCircuitBreakerOpen)
	if got := ExceptionToFailureReason(err, nil); got != "circuit_breaker" {
		t.Fatalf("cb err => %q, want %q", got, "circuit_breaker")
	}
}

func TestExceptionToFailureReason_Internal(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("rpc: %w", ErrInternalBackendException)
	if got := ExceptionToFailureReason(err, nil); got != "internal" {
		t.Fatalf("internal err => %q, want %q", got, "internal")
	}
}

func TestExceptionToFailureReason_Timeout(t *testing.T) {
	t.Parallel()
	err := context.DeadlineExceeded
	if got := ExceptionToFailureReason(err, context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("timeout err => %q, want %q", got, "timeout")
	}
}

func TestExceptionToFailureReason_Unknown(t *testing.T) {
	t.Parallel()
	err := errors.New("some unexpected error")
	if got := ExceptionToFailureReason(err, nil); got != "unknown" {
		t.Fatalf("unknown err => %q, want %q", got, "unknown")
	}
}

func TestExceptionToFailureReason_ClientExceptionUnknown(t *testing.T) {
	t.Parallel()
	// A bare ErrClientException (not auth/no-conn/cb/internal) maps to "unknown"
	err := ErrClientException
	if got := ExceptionToFailureReason(err, nil); got != "unknown" {
		t.Fatalf("ErrClientException => %q, want %q", got, "unknown")
	}
}

// ---------------------------------------------------------------------------
// RPC fault-code mapping — the canonical CCU codes from the spec
// ---------------------------------------------------------------------------

func TestRPCFaultCodeMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code      int
		retryable bool
		label     string
	}{
		{-1, true, "unreach"},
		{-2, true, "timeout"},
		{-8, true, "duty_cycle"},
		{-9, true, "out_of_range"},
		{-10, true, "transmission_pending"},
		{0, false, "ok"},
		{-3, false, "other_negative"},
		{-32603, false, "json_internal"},
		{100, false, "large_positive"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("code_%d_%s", tc.code, tc.label), func(t *testing.T) {
			t.Parallel()
			f := &XMLRPCFault{Code: tc.code, Message: "test"}
			if got := f.IsRetryable(); got != tc.retryable {
				t.Fatalf("XMLRPCFault{Code:%d}.IsRetryable() = %v, want %v", tc.code, got, tc.retryable)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// errors.Is / errors.As chain coverage
// ---------------------------------------------------------------------------

func TestErrorChainXMLRPCFaultWrappedInContextual(t *testing.T) {
	t.Parallel()
	fault := &XMLRPCFault{Code: -9, Message: "DEVICE_OUT_OF_RANGE"}
	wrapped := WithContext(fault, Context{
		Protocol:  "xml-rpc",
		Method:    "setValue",
		Host:      "192.168.0.1:2010",
		Interface: "HmIP-RF",
	})

	// Must satisfy ErrClientException through chain.
	if !errors.Is(wrapped, ErrClientException) {
		t.Fatal("wrapped fault must satisfy ErrClientException")
	}

	// errors.As must recover the concrete *XMLRPCFault.
	var recovered *XMLRPCFault
	if !errors.As(wrapped, &recovered) {
		t.Fatal("errors.As(*XMLRPCFault) must succeed")
	}
	if recovered.Code != -9 {
		t.Fatalf("recovered code=%d, want -9", recovered.Code)
	}
	if !recovered.IsRetryable() {
		t.Fatal("code -9 must be retryable")
	}
}

func TestErrorChainJSONRPCError32603(t *testing.T) {
	t.Parallel()
	e := &JSONRPCError{Code: -32603, Message: "Internal error"}
	wrapped := WithContext(e, Context{Protocol: "json-rpc", Method: "Session.login", Host: "ccu"})

	if !errors.Is(wrapped, ErrInternalBackendException) {
		t.Fatal("json-rpc -32603 must be ErrInternalBackendException through chain")
	}
	if errors.Is(wrapped, ErrClientException) {
		t.Fatal("json-rpc -32603 must NOT match ErrClientException")
	}

	// ExceptionToFailureReason must map to "internal".
	if got := ExceptionToFailureReason(wrapped, nil); got != "internal" {
		t.Fatalf("failure reason = %q, want %q", got, "internal")
	}
}

func TestErrorChainJSONRPCErrorNonInternal(t *testing.T) {
	t.Parallel()
	e := &JSONRPCError{Code: -32600, Message: "Invalid Request"}
	wrapped := WithContext(e, Context{Protocol: "json-rpc", Method: "getParamset", Host: "ccu"})

	if !errors.Is(wrapped, ErrClientException) {
		t.Fatal("non-internal JSON-RPC error must be ErrClientException")
	}
	if errors.Is(wrapped, ErrInternalBackendException) {
		t.Fatal("non-internal JSON-RPC error must NOT be ErrInternalBackendException")
	}

	// ExceptionToFailureReason must map to "unknown" (ErrClientException is not auth/network/cb/internal).
	if got := ExceptionToFailureReason(wrapped, nil); got != "unknown" {
		t.Fatalf("failure reason = %q, want %q", got, "unknown")
	}
}

// ---------------------------------------------------------------------------
// Error escalation: ContextualError context surviving fmt.Errorf wrapping
// ---------------------------------------------------------------------------

func TestContextualErrorSurvivesFmtWrapping(t *testing.T) {
	t.Parallel()
	inner := WithContext(ErrAuthFailure, Context{Protocol: "xml-rpc", Method: "init", Host: "ccu:2010", Interface: "BidCos-RF"})
	outer := fmt.Errorf("coordinator: interface=%s: %w", "BidCos-RF", inner)

	if !errors.Is(outer, ErrAuthFailure) {
		t.Fatal("auth failure must propagate through fmt.Errorf wrapping")
	}

	ctx, ok := ErrorContext(outer)
	if !ok {
		t.Fatal("context must survive fmt.Errorf wrapping")
	}
	if ctx.Protocol != "xml-rpc" {
		t.Fatalf("ctx.Protocol = %q, want xml-rpc", ctx.Protocol)
	}
	if ctx.Interface != "BidCos-RF" {
		t.Fatalf("ctx.Interface = %q, want BidCos-RF", ctx.Interface)
	}

	// ExceptionToFailureReason must still see auth.
	if got := ExceptionToFailureReason(outer, nil); got != "auth" {
		t.Fatalf("failure reason = %q, want auth", got)
	}
}

// ---------------------------------------------------------------------------
// SanitizeErrorMessage edge cases
// ---------------------------------------------------------------------------

func TestSanitizeErrorMessage_NoIP(t *testing.T) {
	t.Parallel()
	msg := "connection refused"
	if got := SanitizeErrorMessage(msg); got != msg {
		t.Fatalf("SanitizeErrorMessage(%q) = %q, want unchanged", msg, got)
	}
}

func TestSanitizeErrorMessage_IPv4Redacted(t *testing.T) {
	t.Parallel()
	msg := "dial tcp 192.168.1.100:2010"
	got := SanitizeErrorMessage(msg)
	if got == msg {
		t.Fatalf("SanitizeErrorMessage did not redact IP: %q", got)
	}
	// The raw IP must not appear.
	if containsStr(got, "192.168.1.100") {
		t.Fatalf("SanitizeErrorMessage still contains raw IP: %q", got)
	}
}

func TestSanitizeErrorMessage_HexTokenRedacted(t *testing.T) {
	t.Parallel()
	token := "abcdef0123456789" // 16-char hex → must be redacted
	msg := "session=" + token
	got := SanitizeErrorMessage(msg)
	if containsStr(got, token) {
		t.Fatalf("SanitizeErrorMessage still contains hex token: %q", got)
	}
}

func TestSanitizeErrorMessage_ShortHexKept(t *testing.T) {
	t.Parallel()
	// Fewer than 16 hex chars → not a session token, must be kept.
	msg := "code=abc123"
	got := SanitizeErrorMessage(msg)
	if !containsStr(got, "abc123") {
		t.Fatalf("SanitizeErrorMessage incorrectly redacted short hex: %q", got)
	}
}

func containsStr(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
