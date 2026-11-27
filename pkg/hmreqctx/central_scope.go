// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package hmreqctx

import "context"

// centralScopeKey keys the central a request is bound to. It is a key of its
// own rather than a field of [RequestContext]: RequestContext.CentralName
// only enriches log records and is set from many places, while the scope
// decides which CCU a device address resolves on. Keeping the two apart means
// a log enricher can never re-route a write.
type centralScopeKey struct{}

// WithCentralScope binds ctx to one central. Device addresses are not unique
// across CCUs — the virtual-remote buses, the INT000* internal devices and
// CUxD devices repeat verbatim on every CCU — so a request that names its
// CCU must resolve every address on that CCU alone.
//
// Only a caller that has already checked that the central exists and holds
// the address it serves sets the scope; the REST device-route guard is that
// caller. An empty name leaves ctx unscoped.
func WithCentralScope(ctx context.Context, central string) context.Context {
	if central == "" {
		return ctx
	}
	return context.WithValue(ctx, centralScopeKey{}, central)
}

// CentralScope reports the central ctx is bound to. The second value is false
// for an unscoped context, in which an address resolves on the first central
// that holds it.
func CentralScope(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	name, ok := ctx.Value(centralScopeKey{}).(string)
	return name, ok && name != ""
}
