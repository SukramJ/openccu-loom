// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package observer

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Multi fans every transport callback out to a fixed list of
// observers. Each observer's [interfaces.RequestSpan] is collected
// into a slice that is handed back to the transport as a single
// opaque token; OnRequestEnd distributes the result back to each
// observer using its own span.
//
// Iteration order is preserved across Start / End so an observer
// expecting the same index across the pair stays correct.
type Multi struct {
	observers []interfaces.TransportObserver
}

// NewMulti composes the supplied observers. Nil entries are dropped
// silently — callers can pass `(... obs1, maybeNil, obs2 ...)`
// without an extra branch.
func NewMulti(observers ...interfaces.TransportObserver) *Multi {
	out := make([]interfaces.TransportObserver, 0, len(observers))
	for _, o := range observers {
		if o == nil {
			continue
		}
		out = append(out, o)
	}
	return &Multi{observers: out}
}

// OnRequestStart implements [interfaces.TransportObserver].
func (m *Multi) OnRequestStart(ctx context.Context, info interfaces.RequestInfo) interfaces.RequestSpan {
	if len(m.observers) == 0 {
		return nil
	}
	spans := make([]interfaces.RequestSpan, len(m.observers))
	for i, o := range m.observers {
		spans[i] = o.OnRequestStart(ctx, info)
	}
	return spans
}

// OnRequestEnd implements [interfaces.TransportObserver]. A nil or
// type-mismatched span is treated as "all observers receive nil",
// matching the [interfaces.NoopObserver] contract.
func (m *Multi) OnRequestEnd(span interfaces.RequestSpan, result interfaces.RequestResult) {
	spans, _ := span.([]interfaces.RequestSpan)
	for i, o := range m.observers {
		var s interfaces.RequestSpan
		if i < len(spans) {
			s = spans[i]
		}
		o.OnRequestEnd(s, result)
	}
}

// Compile-time assertion.
var _ interfaces.TransportObserver = (*Multi)(nil)
