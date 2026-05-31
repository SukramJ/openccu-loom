// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package filter provides outbound adapter filters that gate what the
// north-bound layer exposes to clients. Visibility is the primary concern:
// the [VisibilitySet] interface answers "should this (model, channelType,
// paramset, parameter) tuple be surfaced to the user?"
//
// Every outbound adapter (REST list-endpoints, MQTT publish path) consults
// a [VisibilitySet] by default. The nil value is explicitly safe and means
// "no filter — expose everything", which preserves backward compatibility
// for tests and bare wiring paths that have no visibility registry wired.
//
// Decision: ADR 0007 (supersedes ADR 0005 §"Reads stay ungated").
package filter

import (
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// VisibilitySet is the narrow question every outbound adapter asks:
// "Is this parameter in the user-facing visible-set?"
//
// A parameter is in the visible-set iff it is not hidden by the
// [visibility.Rules] OR is explicitly un-ignored via an UnIgnore entry.
// Concretely: a call returns true iff [visibility.Registry.IsAllowed]
// would return true for the same arguments.
//
// Adapters inject this interface rather than the full [*visibility.Registry]
// so they do not take a dependency on the un-ignore loading machinery.
type VisibilitySet interface {
	// Visible reports whether the (model, channelType, paramset, parameter)
	// tuple should be surfaced to API clients. A nil receiver must be
	// treated as "everything visible" (no filter configured).
	//
	// Callers that know the concrete channel number should prefer
	// [VisibilitySet.VisibleForChannel] for more precise MASTER
	// channel-whitelist filtering.
	Visible(model, channelType string, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool

	// VisibleForChannel is like [Visible] but accepts the concrete channel
	// number so the MASTER channel-whitelist gating can make a precise
	// decision. Callers that have the channel number available MUST use
	// this method instead of [Visible].
	VisibleForChannel(model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool
}

// Adapter wraps a [*visibility.Registry] as a [VisibilitySet]. Nil-safe:
// when the receiver or the underlying registry is nil every call returns
// true (no filter = everything visible).
type Adapter struct {
	reg *visibility.Registry
}

// NewAdapter constructs an [Adapter] around reg. Passing nil is valid and
// produces a pass-through filter (everything visible).
func NewAdapter(reg *visibility.Registry) *Adapter {
	return &Adapter{reg: reg}
}

// Visible implements [VisibilitySet]. Delegates to [visibility.Registry.IsAllowed]
// (channel-number unknown — uses the wildcard path).
// Returns true when a is nil or the underlying registry is nil so that
// un-wired code paths see every parameter.
//
// Callers that know the concrete channel number should use [Adapter.VisibleForChannel]
// for more precise MASTER channel-whitelist filtering.
func (a *Adapter) Visible(model, channelType string, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	if a == nil || a.reg == nil {
		return true
	}
	return a.reg.IsAllowed(model, channelType, paramset, p)
}

// VisibleForChannel implements [VisibilitySet]. Delegates to
// [visibility.Registry.IsAllowedForChannel] with the concrete channel number
// so the MASTER channel-whitelist gating can make a precise decision.
// Returns true when a is nil or the underlying registry is nil.
func (a *Adapter) VisibleForChannel(model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	if a == nil || a.reg == nil {
		return true
	}
	return a.reg.IsAllowedForChannel(model, channelType, channelNo, paramset, p)
}
