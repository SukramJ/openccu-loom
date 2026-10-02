// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// hideCCUCoordinates reports whether the caller must not see a configured
// CCU's network coordinates — its host, hostname, WebUI URL and
// per-interface ports. The serial follows its own, lower bar
// ([hideCCUSerial]).
//
// The rule is one policy applied in one place. [maskCentralRow] has narrowed
// `/centrals` for non-admins since the role model landed, but the same values
// were reachable through three sibling projections that never narrowed
// anything: the sanitized config snapshot, the CCU system report, and the
// LAN discovery list. Narrowing one door while three stand open is not a
// policy, it is an accident of which handler someone remembered.
//
// Why admin and not operator: an operator drives devices, an admin owns the
// deployment. Knowing which host answers on which port is deployment
// knowledge — it is what an attacker who has phished a viewer session needs to
// reach the CCU directly, bypassing this daemon's own authorization entirely.
//
// An absent identity means authentication is switched off for this daemon;
// there is no viewer to distinguish from an admin then, so nothing is hidden.
func hideCCUCoordinates(ctx context.Context) bool {
	id, ok := auth.IdentityFrom(ctx)
	return ok && !id.HasRole(auth.RoleAdmin)
}

// hideCCUSerial reports whether the caller must not see a configured CCU's
// serial. Operators see it, viewers do not.
//
// The serial identifies the appliance but does not say where it is reached,
// and a client that drives devices needs it as its stable key: the Home
// Assistant integration keys its config entry and every hub routing key on
// it. Paired credentials are never admin — neither the daemon's own pairing
// nor a box token paired at an openccu-lite box (ADR 0080) — so an admin-only
// serial left every paired client unable to finish its setup.
func hideCCUSerial(ctx context.Context) bool {
	id, ok := auth.IdentityFrom(ctx)
	return ok && !id.HasRole(auth.RoleOperator)
}
