// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Caller is the minimal transport-agnostic RPC surface every backend
// depends on. Adapters for XML-RPC, BIN-RPC, JSON-RPC satisfy this
// interface by decoding the wire-level value into a plain Go value.
//
// The concrete encoding (xmlrpc.Value vs binrpc.Value vs JSON) is
// invisible to the backend — callers have already normalised into
// bool / int / int64 / float64 / string / []any / map[string]any.
type Caller interface {
	Call(ctx context.Context, method string, args ...any) (any, error)
}

// Announcer is the optional init/deinit contract some transports
// need (XML-RPC + BIN-RPC). JSON-RPC backends typically leave
// [Announcer] nil and short-circuit `Init`.
type Announcer interface {
	Init(ctx context.Context, interfaceID, callbackURL string) error
	// Deinit takes the callbackURL — see [Operations.Deinit].
	Deinit(ctx context.Context, callbackURL string) error
}

// ScriptRunner is the subset of rega.Runner that CcuBackend needs.
// Declared here (consumer package) to keep backends free of a hard
// import of the rega package.
type ScriptRunner interface {
	Run(ctx context.Context, script hmenum.RegaScript, params map[string]string) (string, error)
	RunJSON(ctx context.Context, script hmenum.RegaScript, params map[string]string, v any) error
}
