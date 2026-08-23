// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// hideCCUCoordinates reports whether the caller must not see a configured
// CCU's network coordinates — its host, serial, hostname, WebUI URL and
// per-interface ports.
//
// The rule is one policy applied in one place. [maskCentralRow] has narrowed
// `/centrals` for non-admins since the role model landed, but the same values
// were reachable through three sibling projections that never narrowed
// anything: the sanitized config snapshot, the CCU system report, and the
// LAN discovery list. Narrowing one door while three stand open is not a
// policy, it is an accident of which handler someone remembered.
//
// Why admin and not operator: an operator drives devices, an admin owns the
// deployment. Knowing which host answers on which port, and the serial that
// identifies the appliance, is deployment knowledge — it is what an attacker
// who has phished a viewer session needs to reach the CCU directly, bypassing
// this daemon's own authorization entirely.
//
// An absent identity means authentication is switched off for this daemon;
// there is no viewer to distinguish from an admin then, so nothing is hidden.
func hideCCUCoordinates(ctx context.Context) bool {
	id, ok := auth.IdentityFrom(ctx)
	return ok && !id.HasRole(auth.RoleAdmin)
}
