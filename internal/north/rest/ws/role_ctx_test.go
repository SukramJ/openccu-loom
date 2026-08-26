// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// Identity fixtures shared by every role-gate test in this package,
// both the direct Router.Dispatch(ctx, ...) unit tests and the
// real-connection live_test.go suite (which stamps a *client's
// identity directly via SetIdentity rather than a bare context — see
// client.go handleCommand, which carries c.Identity() into the
// dispatch context).
var (
	testOperatorIdentity = auth.Identity{Subject: "test-op", Role: auth.RoleOperator}
	testAdminIdentity    = auth.Identity{Subject: "test-admin", Role: auth.RoleAdmin}
	testViewerIdentity   = auth.Identity{Subject: "test-viewer", Role: auth.RoleViewer}
)

// opCtx returns a context carrying an operator identity, the minimum
// role most write commands in writeCommandRoles require. Tests that
// dispatch a state-changing command use this instead of
// context.Background() now that Router.Dispatch enforces the
// per-command role gate.
func opCtx() context.Context {
	return auth.ContextWithIdentity(context.Background(), testOperatorIdentity)
}

// adminCtx returns a context carrying an admin identity, required for
// the admin-tier commands in writeCommandRoles (backup.trigger,
// ccu.cache_clear).
func adminCtx() context.Context {
	return auth.ContextWithIdentity(context.Background(), testAdminIdentity)
}

// viewerCtx returns a context carrying a viewer identity — enough to
// pass the "authenticated" check but below every write command's
// minimum role, so it exercises the forbidden branch of the gate.
func viewerCtx() context.Context {
	return auth.ContextWithIdentity(context.Background(), testViewerIdentity)
}

// ctxForCommand returns the minimum-role context that command needs to
// clear the writeCommandRoles gate in Router.Dispatch, or a bare
// context.Background() for an ungated (read) command. Shared
// dispatch-wrapper test helpers that take the command name as a
// parameter (and so serve both gated and ungated commands from one
// call site) use this instead of hard-coding a single identity —
// dispatching a read command through them stays byte-for-byte
// unauthenticated, matching pre-gate behavior.
func ctxForCommand(command string) context.Context {
	switch writeCommandRoles[command] {
	case auth.RoleAdmin:
		return adminCtx()
	case auth.RoleOperator:
		return opCtx()
	default:
		return context.Background()
	}
}
