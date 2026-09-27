// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
)

// liteAccountLevels maps an openccu-lite account level to the CCU user
// level the daemon's role mapping takes (8 admin, 2 user, 1 guest). An
// unknown level maps to 0, which no role accepts.
var liteAccountLevels = map[string]int{
	"administer": 8,
	"configure":  2,
	"operate":    2,
	"read":       1,
}

// liteAccountVerifier checks an account against the box's own accounts:
// it logs in with the account's credentials, reads the level from the
// answer and closes the session again straight away. The box has no way
// to read another account's level, so the check and the level are one
// step. A wrong password matches hmerr.ErrAuthFailure.
type liteAccountVerifier struct {
	client *occulited.Client
}

// Verify implements [central.AccountVerifier].
func (v liteAccountVerifier) Verify(ctx context.Context, username, password string) (int, error) {
	res, err := v.client.Login(ctx, username, password)
	if err != nil {
		return -1, err
	}
	// The session is only proof; release it even when the request that
	// asked has gone away.
	logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = v.client.Logout(logoutCtx, res.SID)
	return liteAccountLevels[res.Level], nil
}
