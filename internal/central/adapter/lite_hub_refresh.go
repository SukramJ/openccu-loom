// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// liteHubRefresh is the periodic read side of an openccu-lite central's
// hub: service messages from the system API; install mode, the BidCos duty
// cycle and interface reachability from the interface processes and the
// box's interface list. Programs, system variables, the inbox and alarm
// messages are ReGa concepts and have no hook; their models stay
// unobserved and the feature set says why.
type liteHubRefresh struct {
	client   *occulited.Client
	unit     *central.Unit
	backends *liteBackendSet
	catalogs *i18n.Catalogs
	locale   string
}

// hooks returns the refresh hooks, each a no-op while active reports the
// generation torn down.
func (r *liteHubRefresh) hooks(active func() bool, system *liteSystem) coordinators.RefreshHooks {
	gated := func(fn func(context.Context) error) func(context.Context) error {
		return func(ctx context.Context) error {
			if !active() {
				return nil
			}
			return fn(ctx)
		}
	}
	return coordinators.RefreshHooks{
		SystemUpdate:     gated(system.refreshSystemUpdate),
		ServiceMessages:  gated(r.serviceMessages),
		InstallMode:      gated(r.installMode),
		BidcosInterfaces: gated(r.bidcosInterfaces),
		Connectivity: gated(func(ctx context.Context) error {
			if !r.unit.Features().Available(hmenum.FeatureConnectivity) {
				return nil
			}
			return loadConnectivity(ctx, r.unit)
		}),
	}
}

// serviceMessages reads the box's active service messages. A message is
// a service-flagged datapoint active on a channel; the box keeps no
// acknowledge, so none is quittable.
func (r *liteHubRefresh) serviceMessages(ctx context.Context) error {
	if r.unit.HubModel == nil || !r.unit.Features().Available(hmenum.FeatureHubServiceMessages) {
		return nil
	}
	ans, err := r.client.ServiceMessages(ctx)
	if err != nil {
		return fmt.Errorf("openccu-lite service messages: %w", err)
	}
	all := make([]hub.ServiceMessage, 0, len(ans.Messages))
	seen := make(map[string]struct{}, len(ans.Messages))
	for i := range ans.Messages {
		m := &ans.Messages[i]
		address := m.Address + ":" + m.Channel
		id := m.Interface + "." + address + "." + m.Key
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		msg := hub.ServiceMessage{
			ID:          id,
			Name:        id,
			Address:     address,
			Parameter:   m.Key,
			InterfaceID: m.Interface,
			Type:        liteServiceMessageType(m.Key),
			Timestamp:   m.Since,
			DisplayName: messageDisplayName(r.catalogs, r.locale, id),
		}
		if dev, ok := r.unit.ModelRegistry.Get(m.Address); ok && dev != nil {
			msg.DeviceName = dev.Name()
		}
		if r.unit.DeviceDetails != nil {
			msg.Rooms = r.unit.DeviceDetails.GetChannelRooms(address)
			msg.Functions = r.unit.DeviceDetails.GetFunctions(address)
		}
		all = append(all, msg)
	}
	r.unit.HubModel.ServiceMessages.Replace(all)
	return nil
}

// liteServiceMessageType classifies a service datapoint the way the CCU's
// service-message types do.
func liteServiceMessageType(key string) hmenum.ServiceMessageType {
	switch {
	case strings.HasPrefix(key, "STICKY_"):
		return hmenum.ServiceMessageTypeSticky
	case key == "CONFIG_PENDING":
		return hmenum.ServiceMessageTypeConfigPending
	case key == "UPDATE_PENDING":
		return hmenum.ServiceMessageTypeUpdatePending
	default:
		return hmenum.ServiceMessageTypeGeneric
	}
}

// installMode reads each pairing-capable interface's remaining install
// time from its interface process.
func (r *liteHubRefresh) installMode(ctx context.Context) error {
	if r.unit.HubModel == nil || r.unit.Hub == nil || !r.unit.Features().Available(hmenum.FeatureInstallMode) {
		return nil
	}
	var firstErr error
	for _, dp := range r.unit.HubModel.InstallModeDPs() {
		if dp == nil || dp.InterfaceID == "" {
			continue
		}
		b := r.backends.get(hmenum.Interface(dp.InterfaceID))
		if b == nil {
			continue
		}
		secs, err := b.GetInstallMode(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("openccu-lite install mode %s: %w", dp.InterfaceID, err)
			}
			continue
		}
		dp.OnState(secs > 0, time.Duration(secs)*time.Second)
	}
	r.unit.Hub.PublishInstallModeRefreshed()
	return firstErr
}

// bidcosInterfaces refreshes the BidCos duty cycle through the shared
// loader, reading the gateways from the interface process itself.
func (r *liteHubRefresh) bidcosInterfaces(ctx context.Context) error {
	if !r.unit.Features().Available(hmenum.FeatureRadioDutyCycle) {
		return nil
	}
	return loadBidcosInterfaces(ctx, liteBidcosLister{backends: r.backends}, r.unit)
}

// bidcosGatewayLister is the gateway listing a lite backend offers: the
// XML-RPC answer, already renamed to the keys the CCU's JSON-RPC wrapper
// uses.
type bidcosGatewayLister interface {
	ListBidcosInterfaces(ctx context.Context, iface string) ([]map[string]any, error)
}

// liteBidcosLister adapts the lite backend's gateway listing to the
// loader's [bidcosInterfaceLister].
type liteBidcosLister struct{ backends *liteBackendSet }

// ListBidcosInterfaces implements [bidcosInterfaceLister].
func (l liteBidcosLister) ListBidcosInterfaces(ctx context.Context, iface string) ([]jsonrpc.BidcosInterface, error) {
	b, ok := l.backends.get(hmenum.Interface(iface)).(bidcosGatewayLister)
	if !ok {
		return nil, nil
	}
	raw, err := b.ListBidcosInterfaces(ctx, iface)
	if err != nil {
		return nil, err
	}
	return jsonrpc.DecodeBidcosInterfaces(raw), nil
}

// liteConnectivityProbe reports each interface process as reachable while
// the box says it runs.
type liteConnectivityProbe struct{ client *occulited.Client }

// Probe implements [coordinators.ConnectivityProbe].
func (p liteConnectivityProbe) Probe(ctx context.Context) ([]coordinators.InterfaceReachability, error) {
	ifaces, err := p.client.Interfaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]coordinators.InterfaceReachability, 0, len(ifaces))
	for _, i := range ifaces {
		out = append(out, coordinators.InterfaceReachability{InterfaceID: i.Name, Reachable: i.Running})
	}
	return out, nil
}

// liteAcknowledger refuses acknowledging messages: the box's service
// messages clear when the device clears them, and there are no alarm
// messages without ReGa.
type liteAcknowledger struct {
	unit    *central.Unit
	name    string
	feature hmenum.Feature
	legacy  error
}

func (a liteAcknowledger) refuse() error {
	return a.unit.Features().Require(a.name, a.feature, a.legacy)
}

// AcknowledgeMessage implements [hub.MessageAcknowledger].
func (a liteAcknowledger) AcknowledgeMessage(context.Context, string) error { return a.refuse() }

// AcknowledgeAllServiceMessages implements [hub.BulkMessageAcknowledger].
func (a liteAcknowledger) AcknowledgeAllServiceMessages(context.Context) (int, error) {
	return 0, a.refuse()
}

// AcknowledgeAllAlarmMessages implements [hub.BulkMessageAcknowledger].
func (a liteAcknowledger) AcknowledgeAllAlarmMessages(context.Context) (int, error) {
	return 0, a.refuse()
}

// liteReGaRefusal stands in for the hub coordinator's ReGa-backed writers
// — program runs, system-variable writes and creation — so a request is
// refused with the reason rather than with "not wired".
type liteReGaRefusal struct {
	unit *central.Unit
	name string
}

func (r liteReGaRefusal) refuse(k hmenum.Feature, legacy error) error {
	return r.unit.Features().Require(r.name, k, legacy)
}

// ExecuteProgram implements [coordinators.ProgramExecutor].
func (r liteReGaRefusal) ExecuteProgram(context.Context, string) error {
	return r.refuse(hmenum.FeatureHubPrograms, coordinators.ErrNoProgramExecutor)
}

// SetSysvar implements [coordinators.SysvarValueWriter].
func (r liteReGaRefusal) SetSysvar(context.Context, string, any) error {
	return r.refuse(hmenum.FeatureHubSysvars, coordinators.ErrNoSysvarWriter)
}

// CreateSysvarBool implements [coordinators.SysvarCreator].
func (r liteReGaRefusal) CreateSysvarBool(context.Context, string, bool) (map[string]any, error) {
	return nil, r.refuse(hmenum.FeatureHubSysvars, hub.ErrNoSysvarMutator)
}

// CreateSysvarEnum implements [coordinators.SysvarCreator].
func (r liteReGaRefusal) CreateSysvarEnum(context.Context, string, []string) (map[string]any, error) {
	return nil, r.refuse(hmenum.FeatureHubSysvars, hub.ErrNoSysvarMutator)
}

// CreateSysvarFloat implements [coordinators.SysvarCreator].
func (r liteReGaRefusal) CreateSysvarFloat(context.Context, string, float64, float64) (map[string]any, error) {
	return nil, r.refuse(hmenum.FeatureHubSysvars, hub.ErrNoSysvarMutator)
}
