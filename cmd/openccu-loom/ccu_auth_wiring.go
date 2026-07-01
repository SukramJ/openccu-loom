// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/ccuauth"
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// ccuAuthEnabled resolves the tri-state enable flag: an explicit value
// wins; unset defaults to the build's add-on stamp (on in the CCU
// add-on, off otherwise).
func ccuAuthEnabled(cc config.CCUAuthConfig) bool {
	if cc.Enabled != nil {
		return *cc.Enabled
	}
	return build.IsAddon()
}

// ccuAuthPrimary resolves the tri-state primary flag: explicit value
// wins; unset defaults to true (CCU is the primary source when on).
func ccuAuthPrimary(cc config.CCUAuthConfig) bool {
	if cc.Primary != nil {
		return *cc.Primary
	}
	return true
}

// buildCCUAuthStore returns an [auth.UserStore] that validates logins
// against the CCU's own user database (ADR 0043), or nil when the
// feature is disabled or no central registry is available. centrals is
// the persisted centrals store used to resolve a central's connection
// config live, so a runtime-adopted central is authenticatable without
// a daemon restart; it may be nil (e.g. persistence unavailable), in
// which case the resolver falls back to the boot-time cfg.Centrals
// snapshot.
func buildCCUAuthStore(cfg *config.Config, reg *central.Registry, centrals *sqlitestore.CentralsStore, logger *slog.Logger) auth.UserStore {
	cc := cfg.North.REST.Auth.CCU
	if !ccuAuthEnabled(cc) || reg == nil {
		return nil
	}
	domain := adapter.NewCCUAuthDomain(reg, newCCUAuthCentralResolver(cfg, centrals), logger)
	store := ccuauth.New(domain, ccuauth.Config{
		Central:      cc.Central,
		MinUserLevel: cc.MinUserLevel,
		RoleMapping:  parseCCURoleMapping(cc.RoleMapping, logger),
	}, logger)
	logger.Info("auth.ccu.enabled",
		slog.String("central", cc.Central),
		slog.Bool("primary", ccuAuthPrimary(cc)),
		slog.Int("min_user_level", cc.MinUserLevel))
	return store
}

// loginChainWithCCU builds the login chain (ADR 0043). When primary is
// true the CCU is tried first (local users are the break-glass
// fallback); when false the local stores come first and the CCU last.
// With no CCU store it degrades to the plain local chain. Break-glass
// holds in both orders because the CCU store maps every failure to
// "unauthenticated", so a local admin always falls through.
func loginChainWithCCU(sqUsers, memUsers, ccu auth.UserStore, primary bool) auth.UserStore {
	local := auth.ChainedUserStore{Primary: sqUsers, Secondary: memUsers}
	if ccu == nil {
		return local
	}
	if primary {
		return auth.ChainedUserStore{Primary: ccu, Secondary: local}
	}
	return auth.ChainedUserStore{
		Primary:   sqUsers,
		Secondary: auth.ChainedUserStore{Primary: memUsers, Secondary: ccu},
	}
}

// parseCCURoleMapping converts the string-keyed config override
// ("8"→"admin", …) into the typed map the store consumes. Invalid keys
// or roles are skipped with a warning so a typo cannot silently grant
// the wrong role.
func parseCCURoleMapping(m map[string]string, logger *slog.Logger) map[int]auth.Role {
	if len(m) == 0 {
		return nil
	}
	out := make(map[int]auth.Role, len(m))
	for k, v := range m {
		level, err := strconv.Atoi(k)
		if err != nil {
			logger.Warn("auth.ccu.role_mapping.bad_level", slog.String("key", k))
			continue
		}
		role := auth.Role(v)
		switch role {
		case auth.RoleAdmin, auth.RoleOperator, auth.RoleViewer:
			out[level] = role
		default:
			logger.Warn("auth.ccu.role_mapping.bad_role", slog.String("role", v))
		}
	}
	return out
}

// newCCUAuthCentralResolver builds the live [adapter.CentralConfigResolver]
// consumed by CCUAuthDomain. It resolves against the persisted centrals
// store — the same table a runtime central-adopt (POST/PUT
// /admin/centrals) writes to — so a central adopted after boot is
// authenticatable without a restart. When centrals is nil (persistence
// unavailable) it falls back to the boot-time cfg.Centrals snapshot,
// preserving pre-existing behaviour for that degraded mode.
//
// A disabled or unknown central, or any store error, resolves to
// (zero, false): the caller treats that as "central not found" and the
// login is rejected. This is the fail-closed direction and must not be
// widened.
func newCCUAuthCentralResolver(cfg *config.Config, centrals *sqlitestore.CentralsStore) adapter.CentralConfigResolver {
	return func(ctx context.Context, name string) (config.CentralConfig, bool) {
		if centrals == nil {
			return centralConfigFromSnapshot(cfg.Centrals, name)
		}
		if name != "" {
			row, err := centrals.Get(ctx, name)
			if err != nil || !row.Enabled {
				return config.CentralConfig{}, false
			}
			cc, _ := configstore.RowToCentralConfig(row, os.Getenv)
			return cc, true
		}
		rows, err := centrals.List(ctx)
		if err != nil {
			return config.CentralConfig{}, false
		}
		for i := range rows {
			row := &rows[i]
			if row.Enabled {
				cc, _ := configstore.RowToCentralConfig(*row, os.Getenv)
				return cc, true
			}
		}
		return config.CentralConfig{}, false
	}
}

// centralConfigFromSnapshot replicates the pre-store resolution rule
// (empty name → first entry; otherwise find-by-name) against a fixed
// []config.CentralConfig slice — the fallback path used when no
// centrals store is available.
func centralConfigFromSnapshot(centrals []config.CentralConfig, name string) (config.CentralConfig, bool) {
	if len(centrals) == 0 {
		return config.CentralConfig{}, false
	}
	if name == "" {
		return centrals[0], true
	}
	for i := range centrals {
		if centrals[i].Name == name {
			return centrals[i], true
		}
	}
	return config.CentralConfig{}, false
}
