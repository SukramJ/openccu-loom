// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package problem implements RFC 9457 problem+json responses. Every
// error the REST surface returns travels through [Write] so clients
// can rely on one shape.
package problem

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// ContentType is the RFC 9457 media type.
const ContentType = "application/problem+json"

// Base URL every type reference starts with. Deliberately
// placeholder-only: none of these URLs need to resolve for the MVP.
const typeBase = "https://openccu-loom.dev/errors/"

// Type tags one problem kind. The REST package promotes these to
// both the `type` member and the `X-Problem-Code` header.
type Type string

// Canonical problem types.
const (
	TypeValidation     Type = "validation"
	TypeNotFound       Type = "not_found"
	TypeConflict       Type = "conflict"
	TypeUnauthorized   Type = "unauthorized"
	TypeForbidden      Type = "forbidden"
	TypeUnsupported    Type = "unsupported"
	TypeRateLimited    Type = "rate_limited"
	TypeInternal       Type = "internal"
	TypeBadRequest     Type = "bad_request"
	TypeServiceUnready Type = "service_unready"
	// TypeUpstreamUnavailable is the explicit signal that the daemon
	// is temporarily refusing the call because the south-bound
	// CCU/Interface is in an unhealthy window — circuit-breaker open,
	// auth failing, or the CCU unreachable at the TCP/DNS layer.
	// Distinct from generic TypeInternal so the SPA can render a
	// friendlier "retry in a few seconds" hint.
	TypeUpstreamUnavailable Type = "upstream_unavailable"
	// TypeFeatureUnavailable is the answer for an operation the target
	// central does not offer right now: its system does not have it, its
	// credential lacks the scope, or it is not ready. The `feature` member
	// says which and why.
	TypeFeatureUnavailable Type = "feature_unavailable"
	// Pairing verbs (client token pairing, ADR 0076). Distinct types so
	// an asking client can branch without parsing prose: pairing_off and
	// pairing_not_local end the attempt, pairing_slow_down means poll
	// less often, plain rate_limited on a pairing route means give up.
	TypePairingOff      Type = "pairing_off"
	TypePairingNotLocal Type = "pairing_not_local"
	TypePairingSlowDown Type = "pairing_slow_down"
)

// FeatureRef names the feature a feature_unavailable problem is about.
type FeatureRef struct {
	Central string `json:"central"`
	Key     string `json:"key"`
	Reason  string `json:"reason"`
	Scope   string `json:"scope,omitempty"`
}

// FieldError is one entry of the `errors` extension — used for
// validation messages.
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
	Min    any    `json:"min,omitempty"`
	Max    any    `json:"max,omitempty"`
}

// Details is the serialised problem body.
type Details struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	Code     string       `json:"code,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`
	// Feature is set on a feature_unavailable problem.
	Feature *FeatureRef `json:"feature,omitempty"`
}

// Write renders p as problem+json. Status from p.Status or from the
// status argument when p.Status is zero.
func Write(w http.ResponseWriter, status int, p Details) {
	if p.Status == 0 {
		p.Status = status
	}
	w.Header().Set("Content-Type", ContentType)
	if p.Code != "" {
		w.Header().Set("X-Problem-Code", p.Code)
	}
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// New constructs a Details from a type tag. Title and detail are
// optional — zero values are omitted.
func New(t Type, r *http.Request, title, detail string) Details {
	d := Details{
		Type:   typeBase + string(t),
		Title:  title,
		Detail: detail,
		Code:   string(t),
	}
	if r != nil {
		d.Instance = r.URL.Path
	}
	return d
}

// WriteFeatureUnavailable writes the 422 feature_unavailable problem when
// err carries a *hmerr.FeatureUnavailableError and reports whether it
// did. It runs before any mapping of the legacy sentinel the error wraps,
// so a client learns which feature is missing and why rather than a bare
// "unsupported".
func WriteFeatureUnavailable(w http.ResponseWriter, r *http.Request, err error) bool {
	fe, ok := errors.AsType[*hmerr.FeatureUnavailableError](err)
	if !ok {
		return false
	}
	d := New(TypeFeatureUnavailable, r, "Feature not available", fe.Error())
	d.Feature = &FeatureRef{Central: fe.Central, Key: string(fe.Feature), Reason: string(fe.Reason), Scope: fe.Scope}
	Write(w, http.StatusUnprocessableEntity, d)
	return true
}

// WriteFromError is a convenience: inspects err, picks a
// reasonable default type, and writes.
func WriteFromError(w http.ResponseWriter, r *http.Request, err error) {
	if WriteFeatureUnavailable(w, r, err) {
		return
	}
	if errors.Is(err, ErrNotFound) {
		Write(w, http.StatusNotFound, New(TypeNotFound, r, "Not found", err.Error()))
		return
	}
	if errors.Is(err, ErrUnauthorized) {
		Write(w, http.StatusUnauthorized, New(TypeUnauthorized, r, "Unauthorized", err.Error()))
		return
	}
	if errors.Is(err, ErrForbidden) {
		Write(w, http.StatusForbidden, New(TypeForbidden, r, "Forbidden", err.Error()))
		return
	}
	if errors.Is(err, ErrValidation) {
		Write(w, http.StatusUnprocessableEntity, New(TypeValidation, r, "Validation failed", err.Error()))
		return
	}
	if IsUpstreamUnavailable(err) {
		Write(w, http.StatusBadGateway,
			New(TypeUpstreamUnavailable, r, "Upstream temporarily unavailable", err.Error()))
		return
	}
	Write(w, http.StatusInternalServerError, New(TypeInternal, r, "Internal error", err.Error()))
}

// IsUpstreamUnavailable reports whether err signals a transient
// south-bound failure — circuit-breaker open, upstream auth
// failing, or the CCU unreachable at the TCP/DNS layer. Handlers
// that catch generic backend errors call this so the caller can
// suggest a retry instead of treating it as a hard fault.
func IsUpstreamUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, hmerr.ErrCircuitBreakerOpen) ||
		errors.Is(err, hmerr.ErrAuthFailure) ||
		errors.Is(err, hmerr.ErrNoConnection)
}

// Sentinels used with [WriteFromError].
var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	// ErrValidation is an alias for [hmerr.ErrValidation] promoted to the
	// problem package so existing call sites need no import change.
	ErrValidation = hmerr.ErrValidation
)
