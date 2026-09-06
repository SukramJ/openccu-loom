// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package mdns advertises the daemon's REST surface on the local
// network so zeroconf-aware clients (Home Assistant, mDNS browsers,
// the SPA hosted on another machine) can auto-discover it without
// manual host/port entry.
//
// One service type is published — `_openccu-loom._tcp.local.` — with
// the daemon's REST listen port and a small TXT bundle naming the
// API mount path, the wire-contract version (mirrors `/info`), and
// the TLS flag. See ADR 0021 for the design rationale and security
// trade-off.
//
// This package is intentionally separate from the Matter-side mDNS
// advertiser, which lives in the go-fabric module's `mdns` package
// and keeps its own copy of the interface filter — the two consumers have
// different lifecycle and tuning requirements (Matter advertises
// commissioning + operational records with subtype responders;
// daemon-discovery wants exactly one straightforward service). The
// underlying library (`github.com/grandcat/zeroconf`) is shared.
package mdns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"
)

// ServiceType is the IANA-style mDNS service type the daemon
// advertises under. Clients filter on this string.
const ServiceType = "_openccu-loom._tcp"

// Domain is the standard mDNS domain. Always "local." on the wire.
const Domain = "local."

// Service is the parameterisation an [Advertiser] needs to publish
// one record. Stable across implementations so the Noop and
// Multicast variants share their input shape.
type Service struct {
	// InstanceName is the leftmost label. Empty falls back to the
	// OS hostname (with any `.local` suffix stripped) — the same
	// behaviour the Matter mDNS layer applies for SRV targets.
	InstanceName string
	// Port is the REST listen port (TCP).
	Port int
	// TXT carries discovery metadata as "key=value" entries.
	// The advertiser passes them to zeroconf verbatim.
	TXT []string
}

// Validate enforces the constraints common to every Advertiser
// implementation.
func (s Service) Validate() error {
	if s.Port <= 0 || s.Port > 65535 {
		return fmt.Errorf("mdns: port out of range: %d", s.Port)
	}
	return nil
}

// resolvedInstanceName returns the configured InstanceName or the
// OS hostname when empty. Used by both [Noop] and [Multicast] so
// the published instance label is deterministic.
func (s Service) resolvedInstanceName() string {
	if s.InstanceName != "" {
		return s.InstanceName
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return strings.TrimSuffix(h, ".local")
	}
	return "openccu-loom"
}

// Advertiser is the runtime surface the daemon holds onto. Start
// publishes the record; Stop tears it down and sends a "goodbye"
// packet via the underlying library when supported. UpdateTXT
// re-announces the TXT bundle at runtime — CCU serials resolve during
// the readiness-gated bring-up and change on live adopt/remove, so
// the record published at boot cannot stay static.
type Advertiser interface {
	Start(ctx context.Context) error
	UpdateTXT(txt []string) error
	Stop() error
}

// ErrAlreadyStarted is returned by Start when the advertiser is
// already running. Callers should treat the prior Start as
// authoritative and not retry.
var ErrAlreadyStarted = errors.New("mdns: advertiser already started")

// ErrNotStarted is returned by UpdateTXT when the advertiser has not
// been started (or was stopped) — there is no live record to update.
var ErrNotStarted = errors.New("mdns: advertiser not started")

// Noop is an Advertiser that records the input service in memory
// without touching the network. Used by tests and by daemon paths
// that want to keep the wire silent (CI, headless smoke runs).
type Noop struct {
	mu      sync.Mutex
	svc     Service
	started bool
}

// NewNoop returns a Noop advertiser bound to svc.
func NewNoop(svc Service) *Noop { return &Noop{svc: svc} }

// Start records that the advertiser is logically running. Returns
// [ErrAlreadyStarted] on a second call without an interleaving Stop.
func (n *Noop) Start(_ context.Context) error {
	if err := n.svc.Validate(); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.started {
		return ErrAlreadyStarted
	}
	n.started = true
	return nil
}

// UpdateTXT records the new TXT bundle. Returns [ErrNotStarted] when
// the advertiser is not running so callers exercise the same contract
// as the Multicast implementation.
func (n *Noop) UpdateTXT(txt []string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.started {
		return ErrNotStarted
	}
	n.svc.TXT = append([]string(nil), txt...)
	return nil
}

// TXT returns the recorded TXT bundle. Test helper.
func (n *Noop) TXT() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.svc.TXT...)
}

// Stop marks the advertiser as no longer running. Idempotent.
func (n *Noop) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.started = false
	return nil
}

// Active reports whether Start has been called without a subsequent
// Stop. Test helper — production callers don't need this.
func (n *Noop) Active() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.started
}

// responder is the half of a live mDNS server the advertiser drives:
// it publishes on construction and stops on Shutdown. *zeroconf.Server
// satisfies it; tests substitute a fake so the failure paths can be
// exercised without a multicast wire.
type responder interface {
	Shutdown()
}

// registrar publishes svc and returns the live responder. The advertiser
// holds one so the register step is substitutable in tests.
type registrar func(svc Service) (responder, error)

// Multicast publishes the service over multicast DNS via
// grandcat/zeroconf. Start registers the record on every
// multicast-capable interface; Stop tears it down.
type Multicast struct {
	svc      Service
	register registrar
	logger   *slog.Logger

	mu     sync.Mutex
	server responder
	// started records the operator's intent rather than the wire state.
	// The two differ after a failed re-register: the daemon wants to be
	// advertised, but nothing is on the wire right now. Keeping the
	// intent lets the next UpdateTXT re-publish instead of reporting
	// [ErrNotStarted] forever.
	started bool
}

// NewMulticast returns a Multicast advertiser bound to svc. The
// service is not published until Start is called.
func NewMulticast(svc Service) *Multicast {
	return &Multicast{svc: svc, register: registerZeroconf, logger: slog.Default()}
}

// Start registers the service on the local multicast wire. Safe
// to call once per Advertiser instance. Returns [ErrAlreadyStarted]
// on re-entry; returns the underlying zeroconf error when the
// register call fails (typical causes: port-already-bound on a
// host with another mDNS responder hogging UDP 5353).
// Start also re-publishes an advertiser whose record was lost by a failed
// [Multicast.UpdateTXT] — that state reports no live server, so it is not
// [ErrAlreadyStarted].
func (m *Multicast) Start(_ context.Context) error {
	if err := m.svc.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		return ErrAlreadyStarted
	}
	server, err := m.registerLocked()
	if err != nil {
		return err
	}
	m.server = server
	m.started = true
	return nil
}

// registerLocked publishes m.svc and returns the new responder. The
// caller holds m.mu.
func (m *Multicast) registerLocked() (responder, error) {
	if m.register == nil {
		return registerZeroconf(m.svc)
	}
	return m.register(m.svc)
}

// log returns the advertiser's logger, defaulting for a zero-value
// Multicast that was not built through [NewMulticast].
func (m *Multicast) log() *slog.Logger {
	if m.logger == nil {
		return slog.Default()
	}
	return m.logger
}

// registerZeroconf is the production [registrar]: it publishes svc on the
// local multicast wire.
func registerZeroconf(svc Service) (responder, error) {
	// Advertise only routable LAN addresses in the A/AAAA records, but keep
	// broadcasting on every multicast interface (ifaces=nil) so peers on a
	// container bridge — e.g. Home Assistant Core on the `hassio` network —
	// still receive the packet. Without this, zeroconf.Register would put the
	// daemon's container-bridge address (e.g. the hassio gateway 172.30.232.1)
	// into the A-record and a discovering client would resolve the daemon to an
	// address it cannot reach (a 404/connection failure). See routableAdvertiseIPs.
	var (
		server *zeroconf.Server
		err    error
	)
	if ips := routableAdvertiseIPs(); len(ips) > 0 {
		server, err = zeroconf.RegisterProxy(
			svc.resolvedInstanceName(),
			ServiceType,
			Domain,
			svc.Port,
			svc.proxyHost(),
			ips,
			svc.TXT,
			nil, // nil → broadcast on all multicast interfaces
		)
	} else {
		// No routable address survived the filter (host with only container
		// interfaces, or enumeration failed) — fall back to library
		// auto-detection so discovery still works rather than going silent.
		server, err = zeroconf.Register(
			svc.resolvedInstanceName(),
			ServiceType,
			Domain,
			svc.Port,
			svc.TXT,
			nil,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("mdns: zeroconf register: %w", err)
	}
	return server, nil
}

// proxyHost returns the host label for the SRV/A records. RegisterProxy
// (unlike Register) requires a non-empty host, so fall back to the
// resolved instance name when the OS hostname is unavailable.
func (s Service) proxyHost() string {
	if h, err := os.Hostname(); err == nil {
		if h = strings.TrimSuffix(h, ".local"); h != "" {
			return h
		}
	}
	return s.resolvedInstanceName()
}

// UpdateTXT re-announces the TXT bundle by republishing the record: the old
// server is shut down and a fresh one registered with the new TXT set.
//
// The obvious alternative — zeroconf's Server.SetText — assigns the service's
// Text field with no synchronisation while the responder goroutines read it
// to compose query answers, so mutating the live record is a data race that no
// lock on this side can close. Republishing costs one goodbye/announce cycle
// but keeps every field the responder reads immutable for the lifetime of the
// server it belongs to.
//
// Republishing has one failure mode the in-place update did not: the new
// register can fail (another responder holding UDP 5353, a transient bind
// race) after the old record is already withdrawn, which would take the
// daemon off the network for good. Three things keep that from being
// terminal: the previous bundle is re-registered so the record comes back
// with stale TXT rather than not at all, the failure is logged at warn level
// here — the caller's own logging is not relied upon — and the advertiser
// stays "started", so the next refresh (every hub-ready and every
// reconnect-ready event fires one) publishes again instead of reporting
// [ErrNotStarted] for the rest of the process.
func (m *Multicast) UpdateTXT(txt []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		return ErrNotStarted
	}
	prev := m.svc.TXT
	if m.server != nil && slices.Equal(prev, txt) {
		// Republishing costs a goodbye packet: every browser drops the
		// service from its cache before the new announcement arrives. The
		// refresh runs on every hub-ready and every reconnect-ready event,
		// most of which carry the bundle that is already published, so an
		// unchanged bundle must not move the record at all.
		return nil
	}
	if m.server != nil {
		m.server.Shutdown()
		m.server = nil
	}
	m.svc.TXT = append([]string(nil), txt...)
	server, err := m.registerLocked()
	if err != nil {
		m.svc.TXT = prev
		restored, restoreErr := m.registerLocked()
		if restoreErr == nil {
			m.server = restored
		}
		m.log().Warn("mdns.txt_refresh_failed",
			slog.String("err", err.Error()),
			slog.Bool("record_restored", restoreErr == nil))
		return err
	}
	m.server = server
	return nil
}

// Stop tears down the published record. Idempotent.
func (m *Multicast) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = false
	if m.server == nil {
		return nil
	}
	m.server.Shutdown()
	m.server = nil
	return nil
}
