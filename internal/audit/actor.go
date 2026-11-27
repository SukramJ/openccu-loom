// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package audit

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// Actor is who a request acts as, as far as the change log is concerned.
//
// User is the authenticated principal: the credential that opened the
// request, and the only identity authorization ever looks at. OnBehalfOf is
// what that credential asserted about the person behind it — a relay that
// forwards a Home Assistant user's edit names that user here. The daemon has
// no way to check the assertion, so it is recorded as asserted and never read
// for a decision.
type Actor struct {
	User       string
	OnBehalfOf string
}

type actorKey struct{}

// WithActor attaches a to ctx. The REST layer sets it once per request, after
// authentication, so every row a request produces — in a handler or deep in
// an adapter — names the same principal.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom returns the actor attached to ctx. The second value is false for
// work no request started (a scheduled job, a boot step).
func ActorFrom(ctx context.Context) (Actor, bool) {
	if ctx == nil {
		return Actor{}, false
	}
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

// RecordFrom stamps entry with the actor of ctx and records it. A User the
// producer set itself wins — a few producers record on behalf of a system
// task or name the account an action targets — while OnBehalfOf always comes
// from ctx, so no producer can attach an assertion the request did not carry.
// A Central the producer set (the central it resolved the device on) wins
// too; otherwise the row takes the central the request is bound to
// ([hmreqctx.CentralScope]), so a change made through a central-bound route
// is recorded against that central. A nil rec records nothing.
//
// Every producer outside this package records through here rather than
// calling [Recorder.Record] directly, so the "on behalf of" column cannot be
// missing from one code path while present on another.
func RecordFrom(ctx context.Context, rec Recorder, entry Entry) {
	if rec == nil {
		return
	}
	entry.OnBehalfOf = ""
	if a, ok := ActorFrom(ctx); ok {
		if entry.User == "" {
			entry.User = a.User
		}
		entry.OnBehalfOf = a.OnBehalfOf
	}
	if entry.Central == "" {
		entry.Central, _ = hmreqctx.CentralScope(ctx)
	}
	rec.Record(entry)
}
