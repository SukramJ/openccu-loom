// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package ccuauth provides an [auth.UserStore] that validates logins
// against a CCU's own user database and maps the CCU permission level
// (UserLevel) to a Loom role. See ADR 0043.
//
// It is a thin policy layer over a CCU-side [Authenticator] port: the
// store sanitises the username, validates the credentials, reads the
// user level, maps it to a role, and returns an [auth.Identity]. Every
// failure mode — wrong credentials, a transient CCU outage, an unknown
// user, an under-privileged user — collapses to [auth.ErrUnauthenticated]
// so the store can sit safely in the [auth.ChainedUserStore] chain
// without ever short-circuiting it.
package ccuauth

import (
	"context"
	"errors"
	"log/slog"
	"regexp"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Authenticator is the CCU-side contract the store depends on.
// *adapter.CCUAuthDomain satisfies it in production.
type Authenticator interface {
	// ValidateCredentials proves (username, password) against the named
	// central. nil = valid. An error wrapping hmerr.ErrAuthFailure means
	// wrong credentials; any other error is a transient failure.
	ValidateCredentials(ctx context.Context, central, username, password string) error
	// UserLevel returns the CCU UserLevel (8/2/1/0) or -1 when unknown.
	UserLevel(ctx context.Context, central, username string) (int, error)
}

// usernamePattern bounds the username to the CCU's legal character set
// before it is interpolated into the user-level ReGa script. Anything
// outside this set is rejected up front (defence against ReGa injection).
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,64}$`)

// Store is an [auth.UserStore] backed by a CCU user database.
type Store struct {
	authn       Authenticator
	central     string
	minLevel    int
	roleMapping map[int]auth.Role // optional override; nil = defaults
	sem         chan struct{}     // concurrency bound on in-flight validations
	logger      *slog.Logger
}

// Config carries the resolved policy for [New].
type Config struct {
	Central       string
	MinUserLevel  int
	RoleMapping   map[int]auth.Role
	MaxConcurrent int // 0 ⇒ defaultMaxConcurrent
}

const defaultMaxConcurrent = 4

// New constructs a Store. authn and logger must be non-nil in production.
func New(authn Authenticator, cfg Config, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	n := cfg.MaxConcurrent
	if n <= 0 {
		n = defaultMaxConcurrent
	}
	minLevel := cfg.MinUserLevel
	if minLevel <= 0 {
		minLevel = 1 // UPL_GUEST; UPL_NONE (0) is always denied
	}
	return &Store{
		authn:       authn,
		central:     cfg.Central,
		minLevel:    minLevel,
		roleMapping: cfg.RoleMapping,
		sem:         make(chan struct{}, n),
		logger:      logger,
	}
}

// AuthenticateBasic validates the credentials against the CCU and maps
// the user level to a role. All failures return auth.ErrUnauthenticated.
func (s *Store) AuthenticateBasic(ctx context.Context, username, password string) (auth.Identity, error) {
	if username == "" || password == "" || !usernamePattern.MatchString(username) {
		return auth.Identity{}, auth.ErrUnauthenticated
	}

	// Bound concurrent CCU validations so a login storm cannot exhaust
	// the CCU's session pool. Respect cancellation while queued.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return auth.Identity{}, auth.ErrUnauthenticated
	}

	if err := s.authn.ValidateCredentials(ctx, s.central, username, password); err != nil {
		if errors.Is(err, hmerr.ErrAuthFailure) {
			s.logger.Debug("ccu-auth: credentials rejected", slog.String("user", username))
		} else {
			// Transient (CCU unreachable, timeout). Treat as "not this
			// store's user" so the chain is not short-circuited — but log
			// the real cause so an outage is diagnosable.
			s.logger.Warn("ccu-auth: validation unavailable",
				slog.String("user", username), slog.String("err", err.Error()))
		}
		return auth.Identity{}, auth.ErrUnauthenticated
	}

	level, err := s.authn.UserLevel(ctx, s.central, username)
	if err != nil {
		s.logger.Warn("ccu-auth: user-level lookup failed",
			slog.String("user", username), slog.String("err", err.Error()))
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	if level < s.minLevel {
		s.logger.Debug("ccu-auth: user below min level",
			slog.String("user", username), slog.Int("level", level), slog.Int("min", s.minLevel))
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	role, ok := s.roleForLevel(level)
	if !ok {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	// Report the canonical subject. The CCU keeps its own user namespace
	// and both calls above ask it about the name the caller typed, but on
	// the Loom side the subject is the key for sessions, bearer tokens,
	// per-user preferences and the audit trail — all of them addressed by
	// the single spelling [auth.CanonicalSubject] defines. Echoing the
	// typed casing here would file one operator under two identities, and
	// a revocation aimed at the canonical one would evict nothing.
	return auth.Identity{Subject: auth.CanonicalSubject(username), Scheme: auth.SchemeBasic, Role: role}, nil
}

// roleForLevel maps a CCU UserLevel to a Loom role: an explicit override
// wins; otherwise the ADR 0043 thresholds apply (≥8 admin, ≥2 operator,
// ≥1 viewer, else deny). Threshold form tolerates unforeseen CCU levels.
func (s *Store) roleForLevel(level int) (auth.Role, bool) {
	if r, ok := s.roleMapping[level]; ok {
		return r, true
	}
	switch {
	case level >= 8:
		return auth.RoleAdmin, true
	case level >= 2:
		return auth.RoleOperator, true
	case level >= 1:
		return auth.RoleViewer, true
	default:
		return "", false
	}
}
