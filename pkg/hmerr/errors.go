// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hmerr defines the domain-wide error taxonomy used by every
// transport, backend, and coordinator in openccu-loom.
//
// Every transport-level error travels wrapped in a [*ContextualError]
// that carries [Context] — protocol, method, host, interface — so that
// logs, metrics, and circuit breakers can correlate failures without
// re-parsing error strings.
//
// Sentinels are compared with [errors.Is]; wire-level faults (XML-RPC,
// JSON-RPC) are exposed as typed errors that also satisfy [errors.Is]
// against their respective category sentinel.
package hmerr

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors. Wrap with fmt.Errorf("...: %w", ...); compare with
// errors.Is. The list mirrors SPECIFICATION §8.2 / §8.4 and is
// intentionally small — domain-specific conditions become new sentinels
// only when a concrete caller needs to branch on them.
var (
	// ErrAuthFailure signals invalid credentials or an expired session
	// at a south-bound transport (XML-RPC, BIN-RPC, JSON-RPC).
	ErrAuthFailure = errors.New("authentication failed")

	// ErrPermissionDenied signals that the request authenticated
	// successfully but the session's privilege level is too low for the
	// invoked method. The CCU reports this as JSON-RPC error code 400
	// ("access denied"); it is distinct from ErrAuthFailure (bad
	// credentials / expired session) so callers and logs can tell a
	// mis-configured user level apart from a genuine auth failure and
	// must not respond by re-logging-in (the credentials are valid).
	ErrPermissionDenied = errors.New("permission denied")

	// ErrNoConnection signals that the transport could not reach the
	// remote at all (DNS, TCP, TLS). Distinct from ErrAuthFailure.
	ErrNoConnection = errors.New("no connection")

	// ErrCircuitBreakerOpen signals that the client-side breaker is
	// holding requests off; the caller should not retry immediately.
	ErrCircuitBreakerOpen = errors.New("circuit breaker open")

	// ErrClientException is the catch-all wire-level failure used when
	// no more specific sentinel applies.
	ErrClientException = errors.New("client exception")

	// ErrInternalBackendException signals that the remote accepted the
	// call but failed internally (HTTP 5xx, JSON-RPC -32603, etc.).
	ErrInternalBackendException = errors.New("internal backend exception")

	// ErrUnsupported signals that an operation is not available on the
	// currently selected backend (e.g. CCU-Jack in v1.0).
	ErrUnsupported = errors.New("operation not supported")

	// ErrParameterHidden signals that a write was rejected because the
	// visibility gate determined the parameter is hidden (e.g.
	// builtInGlobalHides or a model-level hide rule). Callers should
	// respond with HTTP 403 / WS code "parameter_hidden".
	ErrParameterHidden = errors.New("parameter is hidden and may not be written")

	// ErrDescriptionNotFound signals that the requested device or channel
	// description was not found in the registry. Callers can use this to
	// distinguish "no such device" (404) from transport failures.
	ErrDescriptionNotFound = errors.New("description not found")

	// ErrNoClients signals that a coordinator or registry has no active
	// InterfaceClients available to serve the request — e.g. when all interfaces
	// are in a failed state or none have been initialised yet.
	ErrNoClients = errors.New("no clients available")

	// ErrCommandSuperseded signals that a queued or in-flight command was
	// discarded because a newer command for the same target arrived before the
	// first one completed. Callers that receive this error should not retry —
	// the newer command supersedes the dropped one.
	ErrCommandSuperseded = errors.New("command superseded by newer request")

	// ErrValidation signals that user-supplied input failed semantic validation
	// (parameter out of range, malformed address, constraint violation, etc.).
	// It is distinct from wire-transport failures so north-bound adapters can
	// map it to a 422 / Bad Request status rather than a 500 / 503.
	//
	// The REST problem-mapper in internal/north/rest/problem already shadows
	// this sentinel locally; the pkg-level sentinel makes it accessible to the
	// full error chain so any layer can wrap or test with errors.Is.
	ErrValidation = errors.New("validation failed")
)

// Context carries structured metadata for a transport error. Populated by the
// transport just before the error is returned; downstream code reads it via
// [ErrorContext].
//
// The optional Params field stores the outbound call parameters for log
// enrichment.
type Context struct {
	Protocol  string // "xml-rpc", "bin-rpc", "json-rpc"
	Method    string // remote method name
	Host      string // "ccu-host:2010" or similar
	Interface string // "HmIP-RF", "CUxD", ... (optional)
	Params    any    // outbound call parameters, for log enrichment
}

// Fmt formats the context as a human-readable string. When sanitize
// is true, the host field is passed through [SanitizeErrorMessage]
// to redact IP addresses and session tokens before the string is
// written to logs.
// (client/_rpc_errors.py:RpcContext.fmt).
func (c Context) Fmt(sanitize bool) string {
	host := c.Host
	if sanitize {
		host = SanitizeErrorMessage(host)
	}
	if c.Interface != "" {
		return fmt.Sprintf("[%s] %s @ %s (%s)", c.Protocol, c.Method, host, c.Interface)
	}
	return fmt.Sprintf("[%s] %s @ %s", c.Protocol, c.Method, host)
}

// FmtSanitized is a convenience wrapper for Fmt(sanitize=true). It
// always redacts the host portion of the context before returning the
// formatted string, making it safe to use in log sinks and API
// responses that may be visible to untrusted parties.
// (client/_rpc_errors.py:RpcContext.fmt_sanitized).
func (c Context) FmtSanitized() string {
	return c.Fmt(true)
}

// ContextualError wraps an error with its transport [Context].
//
// Construct with [WithContext]; retrieve the context with [ErrorContext].
// The wrapper preserves the original error via Unwrap so that
// [errors.Is] / [errors.As] continue to work against the underlying
// sentinel or typed error.
type ContextualError struct {
	Ctx Context
	Err error
}

// Error formats the context in a stable, greppable way.
func (e *ContextualError) Error() string {
	return fmt.Sprintf("%s %s @ %s: %v", e.Ctx.Protocol, e.Ctx.Method, e.Ctx.Host, e.Err)
}

// Unwrap returns the underlying error for errors.Is / errors.As.
func (e *ContextualError) Unwrap() error { return e.Err }

// WithContext wraps err in a [ContextualError]. Returns nil if err is nil.
func WithContext(err error, ctx Context) error {
	if err == nil {
		return nil
	}
	return &ContextualError{Ctx: ctx, Err: err}
}

// ErrorContext returns the [Context] carried by err (or its chain), and
// reports whether one was present.
func ErrorContext(err error) (Context, bool) {
	if ce, ok := errors.AsType[*ContextualError](err); ok {
		return ce.Ctx, true
	}
	return Context{}, false
}

// XMLRPCFaultCode names well-known CCU XML-RPC fault codes. Mirrors
// The constants
// retryable (transient — duty cycle, unreachable) or permanent
// (paramset rejected, backend offline). Additional codes may be
// emitted by exotic devices; treat unknown codes as permanent.
type XMLRPCFaultCode int

// Fault-code constants mirror
// frozenset (`client/command_retry.py`). All four CCU codes in that set
// are transient and worth retrying; unknown codes default to permanent.
const (
	// XMLRPCFaultUnreach — device or interface temporarily
	// unreachable; retry after the regular backoff.
	XMLRPCFaultUnreach XMLRPCFaultCode = -1
	// XMLRPCFaultTimeout — backend timed out talking to the device;
	// Retry. Not part of
	// transports as a generic timeout fault.
	XMLRPCFaultTimeout XMLRPCFaultCode = -2
	// XMLRPCFaultDutyCycle — RF duty-cycle exhausted (CCU code
	// "INSUFFICIENT_DUTYCYCLE"). Retryable after the throttle drains;
	// the retrier applies a fixed 40 s delay rather than the
	// exponential backoff to avoid making the duty-cycle window
	// worse.
	XMLRPCFaultDutyCycle XMLRPCFaultCode = -8
	// XMLRPCFaultDeviceOutOfRange — temporary RF problem
	// (CCU code "DEVICE_OUT_OF_RANGE"). Retryable.
	XMLRPCFaultDeviceOutOfRange XMLRPCFaultCode = -9
	// XMLRPCFaultTransmissionPending — CCU is already transmitting a
	// previous command for the same device (CCU code
	// "TRANSMISSION_PENDING"). Retryable; the retrier applies a fixed
	// 5 s delay to give the in-flight transmission room to complete.
	XMLRPCFaultTransmissionPending XMLRPCFaultCode = -10
	// XMLRPCFaultInvalidParameter — CCU validator rejected the call
	// with "Invalid parameter or value". For MASTER paramset writes on
	// SWITCH_WEEK_PROFILE channels (and possibly other channel types)
	// this fault is a documented false-positive: the CCU returns it
	// even when the underlying write actually succeeds. Callers that
	// know they hit this pattern (the schedules domain in particular)
	// may treat it as warning-only after verifying the wire-side
	// effect. Empirically confirmed against ReGaHss 3.87.6.20260509.
	XMLRPCFaultInvalidParameter XMLRPCFaultCode = -5
)

// IsRetryable reports whether c is a transient fault that the
// Retrier should re-issue. The set mirrors
// `_RETRYABLE_FAULT_CODES`. Defaults to false for unknown codes
// being conservative avoids hammering the CCU on permanent failures
// the operator only saw once.
func (c XMLRPCFaultCode) IsRetryable() bool {
	switch c {
	case XMLRPCFaultUnreach,
		XMLRPCFaultTimeout,
		XMLRPCFaultDutyCycle,
		XMLRPCFaultDeviceOutOfRange,
		XMLRPCFaultTransmissionPending:
		return true
	default:
		return false
	}
}

// XMLRPCFault is the typed representation of an XML-RPC <fault>. It
// implements [errors.Is] against [ErrClientException] so callers can
// treat any fault as a client-side wire error unless they want the
// specific code.
type XMLRPCFault struct {
	Code    int
	Message string
}

// FaultCode returns the typed representation of f.Code so retry-
// classifiers and telemetry can branch without sprinkling magic
// numbers across the codebase.
func (f *XMLRPCFault) FaultCode() XMLRPCFaultCode { return XMLRPCFaultCode(f.Code) }

// IsRetryable reports whether the fault classifies as transient.
// Combines the typed fault-code classification with a message-content
// override: even a normally-retryable code (e.g. -1 / Unreach) is
// treated as permanent when the message indicates a definitively
// permanent condition like "not found", "unknown" or "does not exist".
//
// Background: code -1 means "device unreachable" (transient) per the
// CCU classification, but the same code is also emitted by every CCU
// implementation in the wild as a catch-all for permanent conditions
// (paramset not registered on a channel, unknown method, missing
// interface). Without the message-text guard the retry loop spends
// ~7s per call retrying a failure that will never succeed — costing
// the entire startup-deadline budget when the fleet exposes many
// channels with sparse paramsets.
func (f *XMLRPCFault) IsRetryable() bool {
	if f == nil {
		return false
	}
	if isPermanentFaultMessage(f.Message) {
		return false
	}
	return f.FaultCode().IsRetryable()
}

// permanentFaultMessageMarkers enumerates substrings that, when found
// in an XML-RPC fault message, conclusively indicate a permanent
// failure. Lower-cased before comparison. The list is conservative;
// callers that need to refine it for a specific backend can do so via
// a separate classifier rather than relaxing this set.
var permanentFaultMessageMarkers = []string{
	"not found",
	"does not exist",
	"unknown method",
	"unknown parameter",
	"invalid address",
}

// isPermanentFaultMessage reports whether msg contains any of the
// well-known permanent-failure substrings.
func isPermanentFaultMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	for _, marker := range permanentFaultMessageMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// Error implements error.
func (f *XMLRPCFault) Error() string {
	return fmt.Sprintf("xml-rpc fault %d: %s", f.Code, f.Message)
}

// Is reports that every fault is-a ClientException.
func (f *XMLRPCFault) Is(target error) bool {
	return errors.Is(target, ErrClientException)
}

// SanitizeErrorMessage redacts IP addresses, hostnames that look like device
// addresses, and session IDs from msg to prevent credential or topology
// leakage in logs.
//
// The function applies a best-effort set of string redactions; it does not
// guarantee zero leakage but eliminates the most common patterns (IPv4,
// hexadecimal session tokens, CCU serial numbers).
func SanitizeErrorMessage(msg string) string {
	// Replace common IPv4 patterns (e.g. "192.168.0.55").
	out := make([]byte, 0, len(msg))
	i := 0
	for i < len(msg) {
		// Quick scan for digits that could start an IPv4 address.
		if isDigit(msg[i]) {
			if end, ok := redactIPv4(msg, i); ok {
				out = append(out, []byte("<ip>")...)
				i = end
				continue
			}
		}
		// Redact hex strings ≥ 16 chars (session IDs, serial numbers).
		if isHex(msg[i]) {
			if end, ok := redactHexToken(msg, i); ok {
				out = append(out, []byte("<token>")...)
				i = end
				continue
			}
		}
		out = append(out, msg[i])
		i++
	}
	return string(out)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// redactIPv4 tries to parse an IPv4 address starting at msg[start].
// Returns the end index and true when four dot-separated octet groups
// (0–255) are found; the caller is responsible for writing the
// replacement.
func redactIPv4(msg string, start int) (end int, ok bool) {
	pos := start
	for octet := range 4 {
		if octet > 0 {
			if pos >= len(msg) || msg[pos] != '.' {
				return 0, false
			}
			pos++
		}
		num := 0
		digits := 0
		for pos < len(msg) && isDigit(msg[pos]) {
			num = num*10 + int(msg[pos]-'0')
			digits++
			pos++
		}
		if digits == 0 || num > 255 {
			return 0, false
		}
	}
	return pos, true
}

// redactHexToken tries to match a contiguous run of hex characters
// starting at msg[start]. Returns the end index and true when the run
// is at least 16 characters long (session ID / serial number threshold).
func redactHexToken(msg string, start int) (end int, ok bool) {
	pos := start
	for pos < len(msg) && isHex(msg[pos]) {
		pos++
	}
	if pos-start < 16 {
		return 0, false
	}
	return pos, true
}

// ExceptionToFailureReason maps a Go error to the machine-readable
// [hmenum.FailureReason] enum that the state machine stores on FAILED
// transitions.
//
// loom:reachable:reason="called by the connection state-machine when recording a FAILED transition reason"
//
// The mapping uses errors.Is to walk the error chain: - [ErrAuthFailure] →
// [hmenum.FailureReasonAuth] - [ErrNoConnection] →
// [hmenum.FailureReasonNetwork] - [ErrCircuitBreakerOpen] →
// [hmenum.FailureReasonCircuitBreaker] - [ErrInternalBackendException] →
// [hmenum.FailureReasonInternal] - Timeout (errors.Is
// context.DeadlineExceeded) → [hmenum.FailureReasonTimeout] - nil →
// [hmenum.FailureReasonNone] - anything else → [hmenum.FailureReasonUnknown]
//
// The hmenum import is deliberate: the mapping is the authoritative
// cross-cutting converter between the transport error taxonomy and the
// state-machine taxonomy, so it lives in the same package as the error
// sentinels.
func ExceptionToFailureReason(err, timeoutErr error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, ErrAuthFailure) {
		return "auth"
	}
	if errors.Is(err, ErrNoConnection) {
		return "network"
	}
	if errors.Is(err, ErrCircuitBreakerOpen) {
		return "circuit_breaker"
	}
	if errors.Is(err, ErrInternalBackendException) {
		return "internal"
	}
	if timeoutErr != nil && errors.Is(err, timeoutErr) {
		return "timeout"
	}
	return "unknown"
}

// JSONRPCError is the typed representation of a JSON-RPC error object.
// Distinguishes -32603 ("internal error") from everything else via
// [errors.Is].
type JSONRPCError struct {
	Code    int
	Message string
	Data    string
}

// Error implements error.
func (e *JSONRPCError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("json-rpc error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

// Is maps -32603 to ErrInternalBackendException and the CCU's code 400
// ("access denied" — authenticated but privilege level too low) to
// ErrPermissionDenied, everything else to ErrClientException.
//
// Code 400 deliberately matches ErrPermissionDenied *and*
// ErrClientException: the former lets callers and logs single out a
// mis-configured user level (and short-circuit retry, see
// reliability.nonRetryable), the latter preserves the existing
// broad-classification behaviour for boundary/severity consumers.
func (e *JSONRPCError) Is(target error) bool {
	switch e.Code {
	case -32603:
		return errors.Is(target, ErrInternalBackendException)
	case 400:
		return errors.Is(target, ErrPermissionDenied) || errors.Is(target, ErrClientException)
	default:
		return errors.Is(target, ErrClientException)
	}
}
