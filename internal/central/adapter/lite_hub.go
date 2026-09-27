// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// liteScopeRefreshInterval is how often an openccu-lite central re-reads
// what its token may do. A token narrowed or widened on the box shows up
// within this window without a restart.
const liteScopeRefreshInterval = 10 * time.Minute

// liteStatePageLimit is the page size of the state-store read; the box
// caps it at 5000.
const liteStatePageLimit = 5000

// liteSystemModel is the model an openccu-lite central reports.
const liteSystemModel = "openccu-lite"

// errLiteTokenRejected reports a token the box does not accept at all.
var errLiteTokenRejected = errors.New("openccu-lite: the API token is not accepted (revoked or mistyped)")

// BringUpHub implements [SouthProfile]: identity, what the token may do,
// the interfaces the box runs. There is no metadata yet — names come with
// the metadata stream — and no ReGa hub.
func (p *liteProfile) BringUpHub(ctx context.Context, in HubBringUpInput) (HubSession, error) {
	logger := in.Logger
	if logger == nil {
		logger = p.logger
	}
	unit := in.Unit
	if err := p.stampIdentity(ctx, unit); err != nil {
		return nil, err
	}
	if err := p.refreshFeatures(ctx, unit); err != nil {
		return nil, err
	}
	p.stampInterfaces(ctx, unit, logger)

	// The scope refresh closes over this generation; a re-init registers
	// the job name again while the scheduler keeps the old one ticking, so
	// the closer disarms it and only the newest generation talks to the box.
	active := new(atomic.Bool)
	active.Store(true)
	if unit.Scheduler != nil {
		if err := unit.Scheduler.Add(scheduler.Job{
			Name:     "lite.scopes." + p.cc.Name,
			Interval: liteScopeRefreshInterval,
			Run: func(ctx context.Context) error {
				if !active.Load() {
					return nil
				}
				return p.refreshFeatures(ctx, unit)
			},
		}); err != nil {
			logger.Warn("lite.scopes.scheduler_add", slog.String("central", p.cc.Name), slog.String("err", err.Error()))
		}
	}
	set := &liteBackendSet{}
	return &liteHubSession{
		profile: p,
		unit:    unit,
		logger:  logger,
		set:     set,
		close:   func() { active.Store(false) },
	}, nil
}

// stampIdentity resolves the central's identity from the box. The serial
// is a hard prerequisite, as on a CCU: it is the central-id slot of every
// hub and virtual-device routing key, and a central is held back rather
// than served with entities no client can key on. It goes through the same
// reduction SSDP discovery applies, so a discovered box and a configured
// central are recognised as the same system.
func (p *liteProfile) stampIdentity(ctx context.Context, unit *central.Unit) error {
	dev, err := p.client.UPnP(ctx)
	if err != nil {
		return fmt.Errorf("openccu-lite identity: %w", err)
	}
	serial := routingkey.CanonicalSerial(dev.SerialNumber)
	if serial == "" {
		return errors.New("openccu-lite identity: the box reports no serial number")
	}
	si := unit.SystemInformation()
	si.Serial = serial
	si.Model = liteSystemModel
	si.URL = ccuBaseURLFor(p.cc)
	si.Hostname = dev.FriendlyName
	if health, herr := p.client.Health(ctx); herr == nil {
		si.Version = health.Release
		if si.Version == "" {
			si.Version = health.Version
		}
	}
	// The status read needs system:read; without it the friendly name
	// stands in for the hostname and the time zone stays unknown.
	if status, serr := p.client.Status(ctx); serr == nil {
		if status.Hostname != "" {
			si.Hostname = status.Hostname
		}
		si.Timezone = status.Timezone
	}
	unit.SetSystemInformation(si)
	return nil
}

// refreshFeatures reads what the token may do and publishes the feature
// set; the central publishes a change event only when it differs.
func (p *liteProfile) refreshFeatures(ctx context.Context, unit *central.Unit) error {
	state, err := p.client.AuthState(ctx)
	if err != nil {
		return fmt.Errorf("openccu-lite auth state: %w", err)
	}
	if !state.AuthOff && !state.Authenticated {
		return errLiteTokenRejected
	}
	unit.SetFeatures(liteFeatures(state.Granted()))
	return nil
}

// stampInterfaces records the interfaces the box itself reports, for the
// status page's comparison with the configured ones. A failure is not
// fatal: the readiness probe has just seen the list.
func (p *liteProfile) stampInterfaces(ctx context.Context, unit *central.Unit, logger *slog.Logger) {
	ifaces, err := p.client.Interfaces(ctx)
	if err != nil {
		logger.Warn("lite.interfaces.fetch_failed", slog.String("central", p.cc.Name), slog.String("err", err.Error()))
		return
	}
	out := make([]central.CCUInterface, 0, len(ifaces))
	for _, i := range ifaces {
		out = append(out, central.CCUInterface{Type: i.Name, Address: i.Name, URL: i.URLPath})
	}
	unit.SetCCUInterfaces(out)
}

// liteHubSession is one bring-up generation of an openccu-lite central.
type liteHubSession struct {
	profile *liteProfile
	unit    *central.Unit
	logger  *slog.Logger
	set     *liteBackendSet
	close   func()
}

// Data implements [HubSession]. Names arrive with the metadata stream.
func (*liteHubSession) Data() HubData { return HubData{} }

// Transports implements [HubSession].
func (s *liteHubSession) Transports() InterfaceTransports {
	return &liteTransports{
		cc:        s.profile.cc,
		client:    s.profile.client,
		readiness: s.profile.readiness,
		ingress:   s.profile.ingress,
		logger:    s.logger,
		backends:  s.set,
	}
}

// ValueSeeder implements [HubSession].
func (s *liteHubSession) ValueSeeder() ValueSeeder {
	return &liteValueSeeder{client: s.profile.client, unit: s.unit, centralName: s.profile.cc.Name, backends: s.set, logger: s.logger}
}

// RefreshMetadata implements [HubSession]: nothing to pull yet.
func (*liteHubSession) RefreshMetadata(context.Context) error { return nil }

// Restorer implements [HubSession]: backup restore is wired separately.
func (*liteHubSession) Restorer() BackupRestorer { return nil }

// WireLate implements [HubSession]: the per-interface install-mode data
// points resolve their backend at call time, which is a lite backend here.
func (*liteHubSession) WireLate(unit *central.Unit, writer *client.ValueWriter) {
	WireInstallModeDPs(unit, writer)
}

// Close implements [HubSession].
func (s *liteHubSession) Close() { s.close() }

// liteValueSeeder reads an interface's current values: the box's state
// store first (one or two calls for the datapoints it keeps), then — for a
// full seed — a VALUES read of every channel with a readable parameter the
// store did not deliver.
type liteValueSeeder struct {
	client      *occulited.Client
	unit        *central.Unit
	centralName string
	backends    *liteBackendSet
	logger      *slog.Logger
}

// SeedValues implements [ValueSeeder].
func (s *liteValueSeeder) SeedValues(ctx context.Context, iface hmenum.Interface, depth SeedDepth) (map[string]map[string]any, error) {
	out, err := s.fromStateStore(ctx, iface)
	if err != nil {
		return nil, err
	}
	if depth == SeedFull {
		s.fillFromParamsets(ctx, iface, out)
	}
	return out, nil
}

// fromStateStore pages through the box's state store. Entries restored
// from disk and not confirmed since are skipped: the box itself says they
// must not be acted on.
func (s *liteValueSeeder) fromStateStore(ctx context.Context, iface hmenum.Interface) (map[string]map[string]any, error) {
	out := make(map[string]map[string]any)
	after := ""
	for {
		page, err := s.client.State(ctx, occulited.StateQuery{Interface: string(iface), Limit: liteStatePageLimit, After: after})
		if err != nil {
			return nil, err
		}
		for _, e := range page.Entries {
			if !e.Confirmed || e.Source == "restored" {
				continue
			}
			v, ok := decodeJSONValue(e.Value)
			if !ok {
				continue
			}
			params := out[e.Address]
			if params == nil {
				params = make(map[string]any)
				out[e.Address] = params
			}
			params[e.Datapoint] = v
		}
		if page.Next == "" || page.Next == after {
			return out, nil
		}
		after = page.Next
	}
}

// fillFromParamsets reads the VALUES paramset of every channel of iface
// that has a readable parameter the state store did not deliver. A failed
// read is logged and skipped: seeding is best effort.
func (s *liteValueSeeder) fillFromParamsets(ctx context.Context, iface hmenum.Interface, out map[string]map[string]any) {
	b := s.backends.get(iface)
	if b == nil {
		return
	}
	wireID := WireInterfaceID(s.centralName, iface)
	for _, dev := range s.unit.ModelRegistry.List() {
		if dev == nil || dev.InterfaceID != wireID {
			continue
		}
		for _, ch := range dev.Channels() {
			if !s.missingReadable(wireID, ch.Address, out[ch.Address]) {
				continue
			}
			values, err := b.GetParamset(ctx, ch.Address, hmenum.ParamsetKeyValues)
			if err != nil {
				s.logger.Debug("lite.seed.paramset_failed",
					slog.String("channel", ch.Address), slog.String("err", err.Error()))
				continue
			}
			params := out[ch.Address]
			if params == nil {
				params = make(map[string]any, len(values))
				out[ch.Address] = params
			}
			for k, v := range values {
				if _, have := params[k]; !have {
					params[k] = v
				}
			}
		}
	}
}

// missingReadable reports whether the channel's VALUES description has a
// readable, non-edge-trigger parameter that have does not carry.
func (s *liteValueSeeder) missingReadable(wireID, channel string, have map[string]any) bool {
	ps, ok := s.unit.ParamsetReg.Get(hmtypes.ParseWireInterfaceID(wireID), channel, hmenum.ParamsetKeyValues)
	if !ok {
		return false
	}
	for name := range ps {
		if ps[name].Operations&hmenum.OperationsRead == 0 || hmenum.IsEdgeTriggerParameter(hmenum.Parameter(name)) {
			continue
		}
		if _, ok := have[name]; !ok {
			return true
		}
	}
	return false
}

// decodeJSONValue decodes one JSON value as the data points take it:
// booleans, float64 numbers and strings, the same shape the CCU's bulk
// value script delivers. A value that is not JSON is dropped.
func decodeJSONValue(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var v any // wire-decoded JSON before the data point's typed coercion
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	return v, true
}
