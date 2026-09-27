// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmerr

import (
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrFeatureUnavailable matches every [*FeatureUnavailableError] under
// [errors.Is].
var ErrFeatureUnavailable = errors.New("feature unavailable")

// FeatureUnavailableError reports that a central cannot serve a request
// because the feature it needs is absent: not offered by the system behind
// the central, or not granted to the credential the daemon holds.
//
// It wraps the error the caller's code already branches on (Legacy — for
// example the "no inbox accepter" or "unsupported by backend" sentinel), so
// every existing [errors.Is] keeps matching, while a north-bound surface that
// knows this type can answer with the precise reason.
type FeatureUnavailableError struct {
	Central string
	Feature hmenum.Feature
	Reason  hmenum.FeatureReason
	// Scope is the missing credential scope when Reason is
	// [hmenum.FeatureReasonMissingScope].
	Scope string
	// Legacy is the pre-existing error this refusal stands in for; may be nil.
	Legacy error
}

// Error implements error.
func (e *FeatureUnavailableError) Error() string {
	msg := fmt.Sprintf("central %s: %s unavailable (%s)", e.Central, e.Feature, e.Reason)
	if e.Scope != "" {
		msg += ": missing scope " + e.Scope
	}
	return msg
}

// Is makes every FeatureUnavailableError match [ErrFeatureUnavailable].
func (e *FeatureUnavailableError) Is(target error) bool { return target == ErrFeatureUnavailable }

// Unwrap exposes the legacy error, so callers branching on it keep working.
func (e *FeatureUnavailableError) Unwrap() error { return e.Legacy }

// ErrScopeMissing matches every [*ScopeMissingError] under [errors.Is].
var ErrScopeMissing = errors.New("credential scope missing")

// ScopeMissingError reports that a token-authenticated system refused an
// operation because the daemon's credential lacks the scope it needs. The
// system is up and the request was well formed; only a credential with the
// scope can change the answer, so callers must not retry.
type ScopeMissingError struct {
	// Scope is the scope the system named ("rpc:operate", "power", …).
	Scope string
	// Operation is what was refused (an RPC method or an endpoint), for logs.
	Operation string
}

// Error implements error.
func (e *ScopeMissingError) Error() string {
	if e.Operation == "" {
		return "credential lacks scope " + e.Scope
	}
	return e.Operation + ": credential lacks scope " + e.Scope
}

// Is makes every ScopeMissingError match [ErrScopeMissing].
func (e *ScopeMissingError) Is(target error) bool { return target == ErrScopeMissing }
