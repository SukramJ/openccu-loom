// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// SouthProfile is a central's south-bound strategy: where the facts the
// bring-up depends on come from. It is selected once per central by
// [southProfileFor] from the central's system type, and it is the only place
// the system type is looked at — everything downstream of a source (the
// XML-RPC client, the description and paramset pipeline, the callback
// handlers, the model, every north-bound adapter) is shared.
//
// The boundary sits at "where does this fact come from", not at "which
// protocol": a CCU and an openccu-lite system speak the same XML-RPC to the
// same interface processes, but they signal readiness, deliver events and
// hold names in entirely different places.
type SouthProfile interface {
	// SystemType reports which system this profile talks to.
	SystemType() hmenum.SystemType
	// Readiness decides when the system serves names and devices. It gates
	// the boot bring-up, the pre-announce re-check, the recovery reconnect
	// and the InterfaceClient reconnect; each site keeps its own timeout.
	Readiness() ReadinessProbe
	// Liveness is the probe the MQTT hub publisher polls to decide whether
	// the central's hub plane is live. Nil means "no probe": the plane folds
	// to reachable on the interface state alone.
	Liveness() LivenessProbe
	// Events wires the central's inbound events. It is attached once per
	// central, when its bring-up handle is built, and outlives every
	// bring-up generation: the callback route (or the stream that replaces
	// it) survives a re-init.
	Events() EventIngress
	// BringUpHub is the first step of every bring-up generation: identity,
	// hub model and metadata. An error returns the central to the readiness
	// gate before anything else is wired, so a retry starts clean.
	BringUpHub(ctx context.Context, in HubBringUpInput) (HubSession, error)
}

// HubBringUpInput is what a profile's hub bring-up needs from the
// composition root.
type HubBringUpInput struct {
	Cfg    *config.Config
	CC     *config.CentralConfig
	Unit   *central.Unit
	Deps   WireDeps
	Logger *slog.Logger
}

// HubSession is one bring-up generation's south-side state: the metadata the
// pipeline stamps at ingest and the sources every interface wiring draws on.
// It lives until the generation is torn down ([HubSession.Close]).
type HubSession interface {
	// Data is the metadata snapshot the pipeline stamps at ingest.
	Data() HubData
	// Transports is the per-interface transport strategy.
	Transports() InterfaceTransports
	// ValueSeeder returns nil when the system offers no bulk value source;
	// data points then start empty and fill from events and on-demand reads.
	ValueSeeder() ValueSeeder
	// RefreshMetadata runs before a hot-plug ingest so a new device carries
	// its name. A failure is logged by the caller, never fatal.
	RefreshMetadata(ctx context.Context) error
	// Restorer returns nil when the system cannot restore a backup.
	Restorer() BackupRestorer
	// WireLate installs the per-central services that resolve an interface
	// backend at call time; it runs after every interface is wired.
	WireLate(unit *central.Unit, writer *client.ValueWriter)
	// Close releases the generation's hub resources.
	Close()
}

// InterfaceTransports is the per-interface transport strategy of a hub
// session: where an interface's XML-RPC endpoint is, how the daemon
// announces itself to it, and which backend kind speaks to it.
type InterfaceTransports interface {
	Endpoint(iface hmenum.Interface) (InterfaceEndpoint, error)
	Announcer(c *xmlrpc.Client, iface hmenum.Interface) backends.Announcer
	BackendKind(iface hmenum.Interface) backends.Kind
	// JSONCaller is the caller a backend dispatches JSON-only operations
	// on, or nil when the system has none.
	JSONCaller() backends.Caller
	// ReconnectGate is the readiness wait an InterfaceClient reconnect
	// performs before re-registering, or nil for none.
	ReconnectGate() func(context.Context) bool
	// ConfigureBackend applies system-specific extras to a freshly built
	// backend (script runner, HTTP transport, rename hooks).
	ConfigureBackend(unit *central.Unit, iface hmenum.Interface, b backends.Operations)
}

// InterfaceEndpoint is where one interface's XML-RPC calls go.
type InterfaceEndpoint struct {
	URL      string
	Username string // XML-RPC Basic auth; empty sends none
	Password string
	// HTTPClient overrides the XML-RPC client's own; nil keeps the default
	// built from the central's TLS posture.
	HTTPClient *http.Client
}

// ValueSeeder returns the current wire values of one interface, keyed by
// channel address, then parameter. Filtering (edge-trigger parameters) and
// applying stay in the pipeline, so every seeder feeds the model the same
// way.
type ValueSeeder interface {
	SeedValues(ctx context.Context, iface hmenum.Interface, depth SeedDepth) (map[string]map[string]any, error)
}

// SeedDepth tells a seeder how much a caller is willing to pay.
type SeedDepth int

const (
	// SeedCheap is the periodic reconciliation sweep.
	SeedCheap SeedDepth = iota
	// SeedFull is a boot ingest, a hot-plug or a post-resync reseed.
	SeedFull
)

// EventIngress constructs a central's [CallbackHandlers] and starts feeding
// them. callbackURL is what the per-interface announcers advertise ("" means
// read-through mode: nothing pushes). binRPCAddr is the BIN-RPC callback
// address (CUxD). handlers is nil when nothing can deliver events. detach is
// a permanent closer, nil when nothing was attached.
type EventIngress interface {
	Attach(cc *config.CentralConfig, unit *central.Unit, deps WireDeps, logger *slog.Logger) (
		handlers *CallbackHandlers, callbackURL, binRPCAddr string, detach func())
}

// ReadinessProbe performs ONE readiness check. reason names why the system is
// not ready ("checkrega.cgi answered 503", "unreachable: …") and is logged
// when the gate starts waiting.
type ReadinessProbe interface {
	Probe(ctx context.Context) (ready bool, reason string)
	// Target names what is probed, for the log line that reports a wait.
	Target() string
}

// LivenessProbe classifies one hub-plane liveness poll. It separates "the
// system answered that it is not serving" from "no answer" from "this
// system cannot be asked", because the hub publisher acts on each
// differently (see [regaLivenessTracker.observe]).
type LivenessProbe interface {
	Probe(ctx context.Context) systemProbeResult
}

// errSystemTypeNotSupported reports a system type this build cannot bring
// up yet. The central is kept visible with a degraded startup state rather
// than half brought up.
var errSystemTypeNotSupported = errors.New("system type not supported by this build yet")

// southProfileFor selects cc's south profile. It is the only place the
// daemon compares a system type; everything downstream works through the
// profile's ports.
func southProfileFor(cc *config.CentralConfig, _ *slog.Logger) (SouthProfile, error) {
	switch st := cc.SystemType.Normalize(); st {
	case hmenum.SystemTypeCCU:
		return newCCUProfile(cc), nil
	case hmenum.SystemTypeOpenCCULite, hmenum.SystemTypeAuto:
		return nil, fmt.Errorf("central %s: %s: %w", cc.Name, st, errSystemTypeNotSupported)
	default:
		return nil, fmt.Errorf("central %s: unknown system_type %q", cc.Name, cc.SystemType)
	}
}

// southLivenessFor is the hub-plane liveness probe of cc's profile, or nil
// when the profile has none or cannot be built.
func southLivenessFor(cc *config.CentralConfig, logger *slog.Logger) LivenessProbe {
	profile, err := southProfileFor(cc, logger)
	if err != nil {
		return nil
	}
	return profile.Liveness()
}
