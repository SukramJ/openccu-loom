// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package cachereset implements the "clear CCU-derivable caches + readiness-
// gated re-pull" operation (ADR 0042). It clears only CCU-derivable persisted
// state — device descriptions, paramset descriptions, the persistent VALUES
// cache and persisted MASTER values — plus the in-memory value cache, then
// re-initializes the owning central(s) through the proven boot path so the
// data is re-pulled fresh from the CCU.
//
// It never touches operator-authored or system state (visibility rules,
// config, auth, Matter pairing, audit/incident history). The clear is recorded
// into the audit log; the surfaces (REST/WS/hmcli) are thin callers of
// [Service.Clear].
package cachereset

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ScopeKind selects the breadth of a clear.
type ScopeKind string

const (
	// ScopeGlobal clears every central's CCU-derivable caches.
	ScopeGlobal ScopeKind = "global"
	// ScopeCentral clears one central (all its interfaces).
	ScopeCentral ScopeKind = "central"
	// ScopeInterface clears one (central, interface).
	ScopeInterface ScopeKind = "interface"
	// ScopeDevice clears one device on one (central, interface).
	ScopeDevice ScopeKind = "device"
)

// Scope is the target of a clear. Central/Interface/Device are required only at
// or below their level (Central for ScopeCentral and finer, Interface for
// ScopeInterface and finer, Device for ScopeDevice).
type Scope struct {
	Kind      ScopeKind
	Central   string
	Interface string
	Device    string
}

// Validate reports whether the scope carries the fields its kind requires.
func (s Scope) Validate() error {
	switch s.Kind {
	case ScopeGlobal:
		return nil
	case ScopeCentral:
		if s.Central == "" {
			return fmt.Errorf("scope %q requires a central", s.Kind)
		}
	case ScopeInterface:
		if s.Central == "" || s.Interface == "" {
			return fmt.Errorf("scope %q requires a central and an interface", s.Kind)
		}
	case ScopeDevice:
		if s.Central == "" || s.Interface == "" || s.Device == "" {
			return fmt.Errorf("scope %q requires a central, interface and device", s.Kind)
		}
	default:
		return fmt.Errorf("unknown scope kind %q", s.Kind)
	}
	return nil
}

// String renders the scope for logs and the audit trail.
func (s Scope) String() string {
	switch s.Kind {
	case ScopeGlobal:
		return "global"
	case ScopeCentral:
		return "central:" + s.Central
	case ScopeInterface:
		return "interface:" + s.Central + "/" + s.Interface
	case ScopeDevice:
		return "device:" + s.Central + "/" + s.Interface + "/" + s.Device
	default:
		return string(s.Kind)
	}
}

// ── Narrow consumer-side ports (the concrete stores / manager satisfy them) ──

// DeviceClearer clears persisted device descriptions.
type DeviceClearer interface {
	Clear(ctx context.Context, centralName, ifaceID string) (int64, error)
	Delete(ctx context.Context, centralName, ifaceID, address string) (int64, error)
}

// ParamsetClearer clears persisted paramset descriptions.
type ParamsetClearer interface {
	ClearForInterface(ctx context.Context, centralName, ifaceID string) (int64, error)
	DeleteDevice(ctx context.Context, centralName, ifaceID, deviceAddress string) (int64, error)
}

// ValuesClearer clears the persistent VALUES cache.
type ValuesClearer interface {
	DeleteForInterface(ctx context.Context, centralName, interfaceID string) (int64, error)
	DeleteDevice(ctx context.Context, centralName, interfaceID, deviceAddress string) error
}

// MasterClearer clears persisted MASTER values.
type MasterClearer interface {
	DeleteForInterface(ctx context.Context, centralName, interfaceID string) (int64, error)
	DeleteDevice(ctx context.Context, centralName, interfaceID, deviceAddress string) error
}

// Topology enumerates the configured centrals and their interfaces so a
// coarse scope can be expanded to the (central, interface) units the stores
// clear at. Interfaces may be reported either bare (`HmIP-RF`, the form the
// config carries) or already canonical — [StoreInterfaceID] normalizes both.
type Topology interface {
	Centrals() []string
	Interfaces(centralName string) []string
}

// StoreInterfaceID returns the canonical `<central>-<interface>` identifier
// every persisted cache row is keyed by, from a possibly-bare interface name.
//
// The four stores this package clears (device descriptions, paramset
// descriptions, VALUES cache, MASTER values) all record the interface id the
// device pipeline stamps onto its devices, which carries the central-name
// prefix (ADR-0024 option B) so addresses repeating across CCUs stay distinct.
// The configured topology, in contrast, names interfaces bare — clearing with
// that name produced exact-match DELETEs that matched no row at all, so an
// operator's "clear caches and re-pull" reported zeros and the re-init
// re-hydrated from exactly the rows they asked to discard.
//
// Normalization is idempotent: an id that already carries the prefix (a
// client or the CLI passing the canonical form for an interface- or
// device-scoped clear) is returned unchanged. An empty central name yields
// the bare interface, matching how the wire id is composed without one.
//
// A bare interface token is prefixed unconditionally, before the
// already-prefixed test runs. Three of the tokens carry a hyphen themselves
// (`HmIP-RF`, `BidCos-RF`, `BidCos-Wired`), so a central legitimately named
// `HmIP` or `BidCos` makes the bare name satisfy a plain prefix test — and
// returning it unchanged reproduces exactly the zero-row clear this function
// exists to prevent.
func StoreInterfaceID(centralName, iface string) string {
	if centralName == "" {
		return iface
	}
	if _, bare := hmenum.InterfacesSupportingRPCCallback[hmenum.Interface(iface)]; bare {
		return centralName + "-" + iface
	}
	if strings.HasPrefix(iface, centralName+"-") {
		return iface
	}
	return centralName + "-" + iface
}

// Reiniter re-initializes a central's south-bound (teardown -> clear model ->
// readiness-gated re-pull). Satisfied by adapter.BringUpManager.
type Reiniter interface {
	ReinitCentral(ctx context.Context, centralName string) bool
}

// Deps bundles the collaborators. Any may be nil — a nil store/clearer is
// skipped (the operation degrades rather than failing), which keeps the
// surface usable in reduced builds and tests.
type Deps struct {
	Devices         DeviceClearer
	Paramsets       ParamsetClearer
	Values          ValuesClearer
	Master          MasterClearer
	Topology        Topology
	Reiniter        Reiniter
	ClearValueCache func(centralName string) // in-memory value cache, per central
	Audit           func(ctx context.Context, scope Scope, report Report)
	Logger          *slog.Logger
}

// Service performs scoped cache clears + re-pull.
type Service struct {
	d      Deps
	logger *slog.Logger
}

// New builds a Service. A nil logger falls back to slog.Default().
func New(d Deps) *Service {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{d: d, logger: logger}
}

// Report summarises a clear: rows removed per store and the centrals that were
// re-initialized. Counts are -1 where the underlying store does not report a
// row count (device-scoped VALUES/MASTER deletes).
type Report struct {
	Scope          Scope    `json:"scope"`
	Devices        int64    `json:"devices"`
	Paramsets      int64    `json:"paramsets"`
	Values         int64    `json:"values"`
	Master         int64    `json:"master"`
	CentralsReinit []string `json:"centrals_reinit"`
	Errors         []string `json:"errors,omitempty"`
}

// Clear executes the scoped clear and triggers the re-pull. It is idempotent:
// clearing already-empty caches is a no-op. Per-store errors are collected into
// the report (the operation continues, clearing what it can) and also returned
// as a single joined error when any occurred, so a caller can both report the
// partial result and signal failure.
func (s *Service) Clear(ctx context.Context, scope Scope) (Report, error) {
	rep := Report{Scope: scope, Devices: 0, Paramsets: 0, Values: 0, Master: 0}
	if err := scope.Validate(); err != nil {
		return rep, err
	}

	units := s.expand(scope)
	s.logger.Info("cachereset.begin",
		slog.String("scope", scope.String()),
		slog.Int("interface_units", len(units)))

	seen := map[string]struct{}{}
	var affected []string // deduped, in first-seen order
	for _, u := range units {
		if _, ok := seen[u.central]; !ok {
			seen[u.central] = struct{}{}
			affected = append(affected, u.central)
		}
		s.clearUnit(ctx, scope, u, &rep)
	}

	// In-memory value cache per affected central, then re-pull, then audit —
	// the audit row must see CentralsReinit as it stands after the re-pull,
	// otherwise every recorded entry reads reinit=[] regardless of outcome.
	for _, central := range affected {
		if s.d.ClearValueCache != nil {
			s.d.ClearValueCache(central)
		}
	}
	rep.CentralsReinit = s.reinit(ctx, affected)
	if s.d.Audit != nil {
		s.d.Audit(ctx, scope, rep)
	}

	s.logger.Info("cachereset.done",
		slog.String("scope", scope.String()),
		slog.Int64("devices", rep.Devices),
		slog.Int64("paramsets", rep.Paramsets),
		slog.Int64("values", rep.Values),
		slog.Int64("master", rep.Master),
		slog.Int("errors", len(rep.Errors)))

	if len(rep.Errors) > 0 {
		return rep, fmt.Errorf("cachereset %s: %s", scope.String(), strings.Join(rep.Errors, "; "))
	}
	return rep, nil
}

// ifaceUnit is one (central, interface) the stores clear at. iface always
// holds the canonical [StoreInterfaceID] the persisted rows are keyed by, not
// the bare name the scope and the topology carry.
type ifaceUnit struct {
	central string
	iface   string
}

// expand turns a scope into the set of (central, interface) units to clear.
// Every unit's interface is normalized to the canonical store id, so an
// operator-supplied bare interface and a client-supplied wire id both reach
// the stores in the one form the rows carry.
func (s *Service) expand(scope Scope) []ifaceUnit {
	switch scope.Kind {
	case ScopeInterface, ScopeDevice:
		return []ifaceUnit{{
			central: scope.Central,
			iface:   StoreInterfaceID(scope.Central, scope.Interface),
		}}
	case ScopeCentral:
		return s.unitsForCentral(scope.Central)
	case ScopeGlobal:
		var out []ifaceUnit
		if s.d.Topology != nil {
			for _, c := range s.d.Topology.Centrals() {
				out = append(out, s.unitsForCentral(c)...)
			}
		}
		return out
	default:
		return nil
	}
}

func (s *Service) unitsForCentral(central string) []ifaceUnit {
	var out []ifaceUnit
	if s.d.Topology != nil {
		for _, iface := range s.d.Topology.Interfaces(central) {
			out = append(out, ifaceUnit{
				central: central,
				iface:   StoreInterfaceID(central, iface),
			})
		}
	}
	return out
}

// clearUnit clears one (central, interface) — or one device within it for a
// device scope — across all four CCU-derivable stores, accumulating counts and
// errors into rep.
func (s *Service) clearUnit(ctx context.Context, scope Scope, u ifaceUnit, rep *Report) {
	device := scope.Kind == ScopeDevice

	if s.d.Devices != nil {
		var n int64
		var err error
		if device {
			n, err = s.d.Devices.Delete(ctx, u.central, u.iface, scope.Device)
		} else {
			n, err = s.d.Devices.Clear(ctx, u.central, u.iface)
		}
		s.account(rep, &rep.Devices, "devices", u, n, err)
	}
	if s.d.Paramsets != nil {
		var n int64
		var err error
		if device {
			n, err = s.d.Paramsets.DeleteDevice(ctx, u.central, u.iface, scope.Device)
		} else {
			n, err = s.d.Paramsets.ClearForInterface(ctx, u.central, u.iface)
		}
		s.account(rep, &rep.Paramsets, "paramsets", u, n, err)
	}
	if s.d.Values != nil {
		if device {
			if err := s.d.Values.DeleteDevice(ctx, u.central, u.iface, scope.Device); err != nil {
				s.account(rep, nil, "values", u, 0, err)
			} else {
				rep.Values = -1 // store does not report a device-scoped count
			}
		} else {
			n, err := s.d.Values.DeleteForInterface(ctx, u.central, u.iface)
			s.account(rep, &rep.Values, "values", u, n, err)
		}
	}
	if s.d.Master != nil {
		if device {
			if err := s.d.Master.DeleteDevice(ctx, u.central, u.iface, scope.Device); err != nil {
				s.account(rep, nil, "master", u, 0, err)
			} else {
				rep.Master = -1
			}
		} else {
			n, err := s.d.Master.DeleteForInterface(ctx, u.central, u.iface)
			s.account(rep, &rep.Master, "master", u, n, err)
		}
	}
}

// account adds n to the running total (when total != nil and there was no
// error) and records an error string otherwise.
func (s *Service) account(rep *Report, total *int64, store string, u ifaceUnit, n int64, err error) {
	if err != nil {
		msg := fmt.Sprintf("%s[%s/%s]: %v", store, u.central, u.iface, err)
		rep.Errors = append(rep.Errors, msg)
		s.logger.Warn("cachereset.store_error",
			slog.String("store", store),
			slog.String("central", u.central),
			slog.String("interface", u.iface),
			slog.String("err", err.Error()))
		return
	}
	if total != nil && *total >= 0 {
		*total += n
	}
}

// reinit re-initializes each affected central through the proven boot path and
// returns the names that were actually re-initialized, preserving the given
// order.
func (s *Service) reinit(ctx context.Context, affected []string) []string {
	if s.d.Reiniter == nil {
		return nil
	}
	var done []string
	for _, central := range affected {
		if s.d.Reiniter.ReinitCentral(ctx, central) {
			done = append(done, central)
		} else {
			s.logger.Warn("cachereset.reinit_skipped",
				slog.String("central", central),
				slog.String("reason", "not managed"))
		}
	}
	return done
}
