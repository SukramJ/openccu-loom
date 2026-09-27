// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// liteProfile is the south profile of an openccu-lite system: everything
// goes through the occulited HTTP API with the central's token — the
// XML-RPC proxy to the interface processes, the event stream that replaces
// the callback registration, the metadata and system APIs.
type liteProfile struct {
	cc        config.CentralConfig
	client    *occulited.Client
	readiness *liteReadinessProbe
	liveness  *liteLivenessProbe
	ingress   *liteEventIngress
	logger    *slog.Logger
	// eventsOptions tunes the event stream reader (heartbeat deadline,
	// backoff); zero fields take the client's defaults.
	eventsOptions occulited.EventsOptions
}

// newLiteProfile builds the profile for cc. It fails only for a
// configuration the client cannot use (a malformed pin).
func newLiteProfile(cc *config.CentralConfig, logger *slog.Logger) (*liteProfile, error) {
	if logger == nil {
		logger = slog.Default()
	}
	client, err := occulited.New(occulited.Config{
		BaseURL:            ccuBaseURLFor(*cc),
		Token:              cc.APIToken,
		TLSFingerprint:     cc.TLSFingerprint,
		InsecureSkipVerify: cc.TLSInsecureSkipVerify,
		Logger:             logger.With(slog.String("central", cc.Name)),
	})
	if err != nil {
		return nil, fmt.Errorf("central %s: occulited client: %w", cc.Name, err)
	}
	p := &liteProfile{
		cc:        *cc,
		client:    client,
		readiness: &liteReadinessProbe{client: client, interfaces: configuredInterfaceNames(cc)},
		liveness:  &liteLivenessProbe{client: client},
		logger:    logger,
	}
	p.ingress = &liteEventIngress{profile: p}
	return p, nil
}

// SystemType implements [SouthProfile].
func (*liteProfile) SystemType() hmenum.SystemType { return hmenum.SystemTypeOpenCCULite }

// Readiness implements [SouthProfile].
func (p *liteProfile) Readiness() ReadinessProbe { return p.readiness }

// Liveness implements [SouthProfile].
func (p *liteProfile) Liveness() LivenessProbe { return p.liveness }

// Events implements [SouthProfile]: the same ingress for the profile's
// whole life, because the announcers of every bring-up generation wait on
// its stream.
func (p *liteProfile) Events() EventIngress { return p.ingress }

// liteReadinessProbe decides when an openccu-lite system serves: occulited
// answers its health read, and at least one of the central's interfaces
// runs. The reason names what is missing, in the words an operator acts on
// (a starting box, a rejected token, a token without the read tier).
type liteReadinessProbe struct {
	client     *occulited.Client
	interfaces []string
}

// Probe implements [ReadinessProbe].
func (p *liteReadinessProbe) Probe(ctx context.Context) (ready bool, reason string) {
	health, err := p.client.Health(ctx)
	if err != nil {
		return false, liteRefusalReason(err)
	}
	if !health.OK {
		return false, "occulited reports itself unhealthy"
	}
	ifaces, err := p.client.Interfaces(ctx)
	if err != nil {
		return false, liteRefusalReason(err)
	}
	for _, i := range ifaces {
		if i.Running && slices.Contains(p.interfaces, i.Name) {
			return true, ""
		}
	}
	return false, "no configured interface is running"
}

// Target implements [ReadinessProbe].
func (p *liteReadinessProbe) Target() string {
	return p.client.BaseURL() + "/api/system/v1/health"
}

// liteRefusalReason turns an occulited answer into a readiness reason.
func liteRefusalReason(err error) string {
	var apiErr *occulited.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status == http.StatusServiceUnavailable && apiErr.Code == occulited.CodeStarting:
			return "occulited starting (503)"
		case apiErr.Status == http.StatusUnauthorized:
			return "token rejected (401)"
		case apiErr.Status == http.StatusForbidden && apiErr.Scope != "":
			return "token lacks " + apiErr.Scope + " (403)"
		default:
			return "occulited answered " + strconv.Itoa(apiErr.Status)
		}
	}
	return "unreachable: " + err.Error()
}

// liteLivenessProbe classifies the hub plane's liveness poll from the
// health read. A box that cannot be asked (the token was revoked) is not
// evidence either way, like a CCU whose readiness CGI is behind auth.
type liteLivenessProbe struct {
	client *occulited.Client
}

// Probe implements [LivenessProbe].
func (p *liteLivenessProbe) Probe(ctx context.Context) systemProbeResult {
	health, err := p.client.Health(ctx)
	if err != nil {
		var apiErr *occulited.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden) {
			return regaProbeUnsupported
		}
		return regaProbeNoAnswer
	}
	if !health.OK {
		return regaProbeNotServing
	}
	return regaProbeServing
}

// liteTransports is the per-interface transport strategy of an
// openccu-lite session: XML-RPC through the box's proxy with the token,
// the lite backend, and the backends recorded for the value seeder.
type liteTransports struct {
	cc        config.CentralConfig
	client    *occulited.Client
	readiness ReadinessProbe
	ingress   *liteEventIngress
	logger    *slog.Logger
	backends  *liteBackendSet
}

// Endpoint implements [InterfaceTransports].
func (t *liteTransports) Endpoint(iface hmenum.Interface) (InterfaceEndpoint, error) {
	return InterfaceEndpoint{URL: t.client.XMLRPCURL(string(iface)), HTTPClient: t.client.HTTPClient()}, nil
}

// Announcer implements [InterfaceTransports]. The box owns the interface
// subscriptions; announcing waits for the event stream to report the
// interface up.
func (t *liteTransports) Announcer(_ *xmlrpc.Client, iface hmenum.Interface) backends.Announcer {
	return &liteAnnouncer{ingress: t.ingress, iface: iface}
}

// BackendKind implements [InterfaceTransports].
func (*liteTransports) BackendKind(hmenum.Interface) backends.Kind { return backends.KindOpenCCULite }

// JSONCaller implements [InterfaceTransports]: the box has no JSON-RPC.
func (*liteTransports) JSONCaller() backends.Caller { return nil }

// ReconnectGate implements [InterfaceTransports].
func (t *liteTransports) ReconnectGate() func(context.Context) bool {
	return newReconnectReadinessGate(t.cc, t.readiness, t.logger)
}

// ConfigureBackend implements [InterfaceTransports]: it records the
// backend for the value seeder's per-channel reads.
func (t *liteTransports) ConfigureBackend(_ *central.Unit, iface hmenum.Interface, b backends.Operations) {
	t.backends.put(iface, b)
}

// WrapCaller implements [InterfaceTransports]: the proxy's refusals arrive
// as XML-RPC faults; classifying them below the reliability stack keeps a
// missing scope from being retried or counted by the circuit breaker.
func (*liteTransports) WrapCaller(next CallFunc) CallFunc {
	return func(ctx context.Context, method string, args ...any) (any, error) {
		v, err := next(ctx, method, args...)
		return v, occulited.ClassifyFault(err)
	}
}

// liteBackendSet holds a session's per-interface backends.
type liteBackendSet struct {
	mu sync.RWMutex
	m  map[hmenum.Interface]backends.Operations
}

func (s *liteBackendSet) put(iface hmenum.Interface, b backends.Operations) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[hmenum.Interface]backends.Operations)
	}
	s.m[iface] = b
}

func (s *liteBackendSet) get(iface hmenum.Interface) backends.Operations {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[iface]
}
