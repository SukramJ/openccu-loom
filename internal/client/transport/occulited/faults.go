// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"errors"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// The lite-rpc proxy refuses two kinds of call with an XML-RPC fault of
// code -1 over HTTP 200 instead of an HTTP error: init (always, alone or
// inside system.multicall) and any method whose tier the credential
// lacks. Both are recognised by their exact text.

// refusalFaultCode is the fault code of both refusals.
const refusalFaultCode = -1

// InitRefusalText is the exact fault string the proxy answers init with.
// The document it cites does not exist on the box; clients match the
// text as it is.
const InitRefusalText = "init is not available remotely on openccu-lite: " +
	"subscribe to /api/rpc/v1/events - see docs/rpc-remote.md"

// tierFaultPrefix and tierFaultNeeds frame the tier refusal
// "not permitted: <method> needs rpc:<tier>".
const (
	tierFaultPrefix = "not permitted: "
	tierFaultNeeds  = " needs "
)

// ErrInitRefused marks the proxy's init refusal: the box owns the
// daemon subscription, and events arrive on the event stream instead.
var ErrInitRefused = errors.New("occulited: init is refused remotely; use the event stream")

// IsInitRefusal reports whether err carries the proxy's init refusal.
func IsInitRefusal(err error) bool {
	f, ok := errors.AsType[*hmerr.XMLRPCFault](err)
	return ok && f.Code == refusalFaultCode && f.Message == InitRefusalText
}

// ParseTierFault decodes a tier refusal message into the refused method
// and the scope it needs ("rpc:operate"). ok is false for any other
// text.
func ParseTierFault(msg string) (method, scope string, ok bool) {
	rest, found := strings.CutPrefix(msg, tierFaultPrefix)
	if !found {
		return "", "", false
	}
	i := strings.LastIndex(rest, tierFaultNeeds)
	if i <= 0 {
		return "", "", false
	}
	method, scope = rest[:i], rest[i+len(tierFaultNeeds):]
	tier, isRPC := strings.CutPrefix(scope, "rpc:")
	if !isRPC || tier == "" || strings.ContainsAny(method, " \t") || strings.ContainsAny(tier, " \t") {
		return "", "", false
	}
	return method, scope, true
}

// ClassifyFault refines an error from the lite-rpc proxy. The init
// refusal comes back wrapping [ErrInitRefused]; a tier refusal comes
// back wrapping a [*hmerr.ScopeMissingError] naming the scope, so the
// caller stops retrying and can tell the operator what to grant. The
// original error stays in the chain either way. Any other error is
// returned unchanged.
func ClassifyFault(err error) error {
	// An error classified once already (by a layer below) is returned as it
	// is, so a refusal is never wrapped twice.
	if err == nil || errors.Is(err, ErrInitRefused) || errors.Is(err, hmerr.ErrScopeMissing) {
		return err
	}
	// errors.AsType, not hmerr.FaultFromError: the latter synthesises a
	// code -1 fault for any error, which would make every transport
	// failure look like a refusal candidate.
	f, ok := errors.AsType[*hmerr.XMLRPCFault](err)
	if !ok || f.Code != refusalFaultCode {
		return err
	}
	if f.Message == InitRefusalText {
		return fmt.Errorf("%w: %w", ErrInitRefused, err)
	}
	if method, scope, ok := ParseTierFault(f.Message); ok {
		return fmt.Errorf("%w: %w", &hmerr.ScopeMissingError{Scope: scope, Operation: method}, err)
	}
	return err
}
