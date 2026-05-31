// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// InfoPayload is the marker type returned by [Source.Info]. Concrete
// payloads are domain-typed structs (e.g. *ClimateInfo, *LockInfo,
// *GenericInfo). The empty interface boundary lets callers
// type-switch on the concrete value while keeping the Source
// interface symmetric across all three payload kinds.
type InfoPayload any

// ConfigPayload is the marker type returned by [Source.Config].
// Concrete payloads are domain-typed structs (e.g. *ClimateConfig,
// *LockConfig, *GenericConfig).
type ConfigPayload any

// StatePayload is the marker type returned by [Source.State].
// Concrete payloads are domain-typed structs (e.g. *ClimateState,
// *LockState, *GenericState).
type StatePayload any

// Source is the universal contract every domain object exposes to
// north-bound adapters. Implementations live in `internal/model` and
// `internal/central`; consumers live in `internal/north`.
//
// The five methods split read access (Info / Config / State) from
// write access (ServiceMethodNames / Invoke). Each read method
// returns a typed payload — callers type-switch on the concrete
// struct rather than poking at string keys.
//
// See docs/adr/0007-strong-model-source-interface.md for the full
// rationale.
//
// Read methods return nil for "no data of this kind". The bridge and
// REST adapters render nil as an absent JSON object.
//
// Service method names are returned in registration order so callers
// (HA-Discovery, REST OpenAPI generator) emit stable orderings across
// restarts.
//
// The write half is implemented by embedding [ServiceRegistry] and
// calling [ServiceRegistry.RegisterService] from the constructor for
// each method that should be reachable from outside the daemon.
type Source interface {
	// Info returns identity-level fields (name, description,
	// category, unique_id, …) as a typed [InfoPayload].
	Info() InfoPayload

	// Config returns configuration-level fields (capabilities,
	// ranges, available modes, …) as a typed [ConfigPayload].
	Config() ConfigPayload

	// State returns live state fields (value, position,
	// hvac_mode, …) as a typed [StatePayload].
	State() StatePayload

	// ServiceMethodNames returns the deterministically ordered list of
	// external service-method names this Source exposes. The slice is
	// safe to retain — implementations return a fresh copy on each call.
	ServiceMethodNames() []string

	// Invoke dispatches a service-method call. params carries the
	// JSON-decoded body the north-bound adapter received; the handler
	// validates and coerces to its real argument types. priority
	// propagates straight to the south-bound write so callers retain
	// control over queue placement.
	//
	// Invoke returns [ErrUnknownServiceMethod] (wrapped with the
	// offending name) when name is not registered. Handlers may wrap
	// their own errors freely.
	Invoke(ctx context.Context, name string, params map[string]any,
		priority hmenum.CommandPriority) error
}
