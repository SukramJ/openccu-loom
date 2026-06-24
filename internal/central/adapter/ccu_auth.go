// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// ErrCCUAuthCentralNotFound is returned when the configured auth central
// cannot be resolved.
var ErrCCUAuthCentralNotFound = errors.New("ccu-auth: central not found")

// CCUAuthDomain validates login credentials against a CCU's own user
// database and reads a user's permission level. It is the production
// implementation of the CCU authentication port consumed by
// internal/auth/ccuauth.
//
// Credential validation opens a SHORT-LIVED JSON-RPC session with the
// end-user's credentials and logs it out immediately — it never touches
// the daemon's long-lived service session. The user-level lookup, by
// contrast, runs on the central's privileged service session (the
// service account is admin and may read ID_USERS).
type CCUAuthDomain struct {
	registry *central.Registry
	centrals []config.CentralConfig
	logger   *slog.Logger
}

// NewCCUAuthDomain constructs the domain. centrals provides the host /
// port / TLS used to build the transient validation client.
func NewCCUAuthDomain(r *central.Registry, centrals []config.CentralConfig, logger *slog.Logger) *CCUAuthDomain {
	if logger == nil {
		logger = slog.Default()
	}
	return &CCUAuthDomain{registry: r, centrals: centrals, logger: logger}
}

// centralConfig resolves the target CentralConfig by name; an empty name
// selects the first configured central.
func (d *CCUAuthDomain) centralConfig(name string) (config.CentralConfig, bool) {
	if len(d.centrals) == 0 {
		return config.CentralConfig{}, false
	}
	if name == "" {
		return d.centrals[0], true
	}
	for i := range d.centrals {
		if d.centrals[i].Name == name {
			return d.centrals[i], true
		}
	}
	return config.CentralConfig{}, false
}

// ValidateCredentials opens a transient CCU session with the supplied
// credentials to prove they are valid, then logs it out. Returns nil on
// success; on wrong credentials the underlying client returns an error
// wrapping hmerr.ErrAuthFailure; any other error is a transient failure
// (CCU unreachable, etc.) that the caller maps to "unauthenticated".
func (d *CCUAuthDomain) ValidateCredentials(ctx context.Context, centralName, username, password string) error {
	cc, ok := d.centralConfig(centralName)
	if !ok {
		return fmt.Errorf("%w: %q", ErrCCUAuthCentralNotFound, centralName)
	}
	// A fresh client per validation: its own session, its own login
	// back-off counter; never the shared service client.
	jc, err := jsonrpc.New(jsonrpc.Config{
		Endpoint:   jsonrpcEndpoint(cc),
		Username:   username,
		Password:   password,
		Host:       cc.Host,
		HTTPClient: jsonrpcHTTPClient(cc),
		Logger:     d.logger.With(slog.String("transport", "jsonrpc"), slog.String("purpose", "ccu-auth")),
	})
	if err != nil {
		return fmt.Errorf("ccu-auth: client: %w", err)
	}
	loginErr := jc.Login(ctx)
	// Release the CCU session slot regardless of the outcome. Detached
	// context so a cancelled request still frees the session.
	logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = jc.Logout(logoutCtx)
	return loginErr
}

// UserLevel reads the CCU UserLevel (8/2/1/0, or -1 when unknown) for
// username on the named central, via the privileged service session.
// username must be pre-sanitised by the caller.
func (d *CCUAuthDomain) UserLevel(ctx context.Context, centralName, username string) (int, error) {
	cc, ok := d.centralConfig(centralName)
	if !ok {
		return -1, fmt.Errorf("%w: %q", ErrCCUAuthCentralNotFound, centralName)
	}
	if d.registry == nil {
		return -1, ErrCCUAuthCentralNotFound
	}
	for _, u := range d.registry.List() {
		if u.Name() == cc.Name {
			return u.HubModel.UserLevelRemote(ctx, username)
		}
	}
	return -1, fmt.Errorf("%w: %q (no live unit)", ErrCCUAuthCentralNotFound, cc.Name)
}
