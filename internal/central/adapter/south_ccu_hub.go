// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/httpx"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// BringUpHub implements [SouthProfile]: the JSON-RPC session, the ReGa
// runner and the hub model, exactly as [WireHub] builds them.
func (p *ccuProfile) BringUpHub(ctx context.Context, in HubBringUpInput) (HubSession, error) {
	runner, data, closer, err := WireHub(ctx, *in.CC, in.Unit, in.Logger, in.Deps.Catalogs, in.Cfg.Locale)
	if err != nil {
		return nil, err
	}
	// The hub bring-up has resolved the product, which is what the one
	// model-dependent feature (recovery mode) needs.
	in.Unit.SetFeatures(ccuFeatures(in.Unit.SystemInformation().Model))
	return newCCUHubSession(*in.CC, in.Unit, runner, data, closer, p.readiness, in.Logger), nil
}

// ccuFeatures is what a CCU offers: everything the daemon has always done
// against one, so no CCU code path consults a feature it would find absent.
// Two keys are not unconditional. Rooms and functions are flat on a CCU, so
// nested taxonomy nodes are not supported. Recovery mode exists on OpenCCU
// firmware only: get_backend_info classifies anything that is not a stock CCU
// or debmatic as "OpenCCU", and an empty model means the product is not
// resolved yet, which must read as "not offered" rather than "offered".
func ccuFeatures(model string) central.Features {
	states := make(map[hmenum.Feature]central.FeatureState, len(hmenum.AllFeatures()))
	for _, k := range hmenum.AllFeatures() {
		states[k] = central.FeatureState{Available: true}
	}
	states[hmenum.FeatureTaxonomyTree] = central.FeatureState{Reason: hmenum.FeatureReasonNotSupported}
	switch model {
	case "":
		states[hmenum.FeatureSystemRecoveryMode] = central.FeatureState{Reason: hmenum.FeatureReasonNotReady}
	case "CCU":
		states[hmenum.FeatureSystemRecoveryMode] = central.FeatureState{Reason: hmenum.FeatureReasonNotSupported}
	}
	return central.NewFeatures(hmenum.SystemTypeCCU, states)
}

// ccuHubSession is one bring-up generation of a CCU: the JSON-RPC session
// (through the ReGa runner) is what the value seed, the metadata refresh,
// the backup restorer and the backends' JSON-only operations all share.
type ccuHubSession struct {
	cc        config.CentralConfig
	unit      *central.Unit
	runner    *rega.Runner // nil only in tests that exercise no JSON-RPC surface
	data      HubData
	closer    func()
	readiness ReadinessProbe
	logger    *slog.Logger

	loaderOnce sync.Once
	loader     *devicedetails.Loader
}

// newCCUHubSession wraps the products of one hub bring-up. runner may be nil
// for a test that wires interfaces without a JSON-RPC surface; every
// consumer then degrades the way it did before the session existed.
func newCCUHubSession(cc config.CentralConfig, unit *central.Unit, runner *rega.Runner, data HubData, closer func(), readiness ReadinessProbe, logger *slog.Logger) *ccuHubSession {
	if logger == nil {
		logger = slog.Default()
	}
	return &ccuHubSession{cc: cc, unit: unit, runner: runner, data: data, closer: closer, readiness: readiness, logger: logger}
}

// Data implements [HubSession].
func (s *ccuHubSession) Data() HubData { return s.data }

// Transports implements [HubSession].
func (s *ccuHubSession) Transports() InterfaceTransports {
	return &ccuTransports{cc: s.cc, runner: s.runner, readiness: s.readiness, logger: s.logger}
}

// ValueSeeder implements [HubSession].
func (s *ccuHubSession) ValueSeeder() ValueSeeder {
	return NewCCUValueSeeder(s.runner)
}

// RefreshMetadata implements [HubSession]: a forced reload of the central's
// DeviceDetails cache through the session's JSON-RPC client.
func (s *ccuHubSession) RefreshMetadata(ctx context.Context) error {
	if s.runner == nil || s.unit == nil {
		return nil
	}
	s.loaderOnce.Do(func() {
		s.loader = devicedetails.NewLoaderForJSONRPC(s.unit.DeviceDetails, s.runner.Client(), s.cc.Name, s.logger)
	})
	return s.loader.Load(ctx, true)
}

// Restorer implements [HubSession]. Each generation wraps its own JSON-RPC
// session, so a re-gate after a reconnect refreshes it.
func (s *ccuHubSession) Restorer() BackupRestorer {
	if s.runner == nil {
		return nil
	}
	return &HTTPBackupRestorer{
		BaseURL:               ccuBaseURLFor(s.cc),
		Session:               s.runner.Client(),
		InsecureSkipTLSVerify: s.cc.TLSInsecureSkipVerify,
	}
}

// WireLate implements [HubSession]: the late-binding handlers that resolve
// the primary client/backend at call time.
func (*ccuHubSession) WireLate(unit *central.Unit, writer *client.ValueWriter) {
	WireSysvarCreator(unit, writer)
	WireBackupAndDownload(unit, writer)
	// Durable service-message suppression: routes the hub coordinator seam
	// and the ServiceMessages aggregate's Disable/Unsuppress path through the
	// per-interface backend's Interface.suppressServiceMessages call.
	WireServiceMessageSuppressor(unit, writer)
	// Per-interface install-mode data points: one per pairing-capable radio,
	// each writing to its own interface backend (no CCU-wide toggle exists).
	WireInstallModeDPs(unit, writer)
}

// Close implements [HubSession].
func (s *ccuHubSession) Close() {
	if s.closer != nil {
		s.closer()
	}
}

// ccuTransports is the CCU's per-interface transport strategy: XML-RPC to
// the interface port with Basic auth, `init` as the announcement, and the
// session's JSON-RPC client for the JSON-only backend operations.
type ccuTransports struct {
	cc        config.CentralConfig
	runner    *rega.Runner
	readiness ReadinessProbe
	logger    *slog.Logger
}

// Endpoint implements [InterfaceTransports].
func (t *ccuTransports) Endpoint(iface hmenum.Interface) (InterfaceEndpoint, error) {
	u, err := interfaceURL(t.cc, iface)
	if err != nil {
		return InterfaceEndpoint{}, err
	}
	return InterfaceEndpoint{URL: u, Username: t.cc.Username, Password: t.cc.Password}, nil
}

// Announcer implements [InterfaceTransports].
func (*ccuTransports) Announcer(c *xmlrpc.Client, _ hmenum.Interface) backends.Announcer {
	return newXMLRPCAnnouncer(c)
}

// BackendKind implements [InterfaceTransports].
func (*ccuTransports) BackendKind(iface hmenum.Interface) backends.Kind {
	return backends.KindFor(iface)
}

// JSONCaller implements [InterfaceTransports].
func (t *ccuTransports) JSONCaller() backends.Caller {
	if t.runner == nil {
		return nil
	}
	return &jsonrpcCaller{client: t.runner.Client()}
}

// ReconnectGate implements [InterfaceTransports]. Gate the reconnect
// re-registration on the same boot marker that gates the initial bring-up.
// Without it a reconnect racing a rebooting CCU registers a second time (see
// [client.Config]). Bounded, unlike the boot gate: the reconnect loop retries
// with backoff, so a long wait here would stall the client state machine
// instead of letting it cycle.
func (t *ccuTransports) ReconnectGate() func(context.Context) bool {
	return newReconnectReadinessGate(t.cc, t.readiness, t.logger)
}

// WrapCaller implements [InterfaceTransports]: a CCU's faults need no
// system-specific classification.
func (*ccuTransports) WrapCaller(next CallFunc) CallFunc { return next }

// ConfigureBackend implements [InterfaceTransports].
func (t *ccuTransports) ConfigureBackend(unit *central.Unit, _ hmenum.Interface, b backends.Operations) {
	// Wire the ReGa script runner and the plain-HTTP transport into the CCU
	// backend so the operations that need them (CreateBackupAndDownload and
	// the group editor) are reachable in production. Both setters are no-ops
	// on non-CCU backends; the type assertion ensures we only call them when
	// the concrete type is *backends.CcuBackend.
	if ccuBackend, ok := b.(*backends.CcuBackend); ok {
		if t.runner != nil {
			ccuBackend.SetScriptRunner(t.runner)
		}
		// The ReGa com-test timestamps are offset-free CCU-local wall clock,
		// so the backend needs the CCU's own zone to turn one into an
		// instant. WireHub has already stamped it from the CCU's time
		// configuration by the time any interface is wired.
		ccuBackend.SetCCUTimezone(unit.SystemInformation().Timezone)
		hc := jsonrpcHTTPClient(t.cc)
		if hc == nil {
			// No timeout here by design; the transport is ours either way.
			hc = &http.Client{Transport: httpx.NewTransport()}
		}
		jc := t.runner
		var sessionIDFn func() string
		if jc != nil {
			sessionIDFn = jc.Client().SessionID
			// The backup download (cp_security.cgi) authenticates by session
			// id and serves a login page under HTTP 200 for a stale one, so
			// make sure the session is usable first. EnsureSession renews the
			// live session rather than displacing it: a forced login here
			// would abandon the session the whole central is working with and
			// burn a slot in the CCU's small, WebUI-shared session pool on
			// every backup.
			rpcClient := jc.Client()
			ccuBackend.SetSessionRenewer(func(ctx context.Context) (string, error) {
				if err := rpcClient.EnsureSession(ctx); err != nil {
					return "", err
				}
				return rpcClient.SessionID(), nil
			})
		}
		ccuBackend.SetHTTPTransport(ccuBaseURLFor(t.cc), hc, sessionIDFn)

		// Persist device / channel renames to the CCU. The hook resolves
		// the address to its ReGa ISE-ID, then dispatches to Device.setName
		// for a device address or Channel.setName for a channel address
		// (one carrying a ":" channel suffix), both over JSON-RPC. Without
		// this hook a rename would only mutate the in-memory model and be
		// lost on the next device reload. Wired on the CCU backend because
		// the ReGa ISE-ID lookup and setName calls require JSON-RPC.
		renameBackend := ccuBackend
		unit.SetRenameDeviceFn(func(ctx context.Context, address, name string) error {
			iseID, err := renameBackend.GetIseIDByAddress(ctx, address)
			if err != nil {
				return fmt.Errorf("rename: resolve ise-id for %s: %w", address, err)
			}
			return dispatchRename(ctx, renameBackend, address, iseID, name)
		})
		// A device rename that carries its channels resolves the whole set
		// in one Device.listAllDetail instead of one listing per address —
		// the CCU has no address→ise-id method, so every resolve fetches
		// the complete inventory.
		unit.SetRenameDeviceBatchFn(func(ctx context.Context, rename central.DeviceRename) error {
			addresses := make([]string, 0, len(rename.Channels)+1)
			addresses = append(addresses, rename.Device.Address)
			for _, ch := range rename.Channels {
				addresses = append(addresses, ch.Address)
			}
			ids, err := renameBackend.GetIseIDsByAddresses(ctx, addresses)
			if err != nil {
				return fmt.Errorf("rename: resolve ise-ids for %s: %w", rename.Device.Address, err)
			}
			// The device goes first and short-circuits: renaming the
			// channels around a device name the CCU rejected would leave
			// the two out of step.
			if err := dispatchRename(ctx, renameBackend, rename.Device.Address, ids[rename.Device.Address], rename.Device.Name); err != nil {
				return err
			}
			var firstErr error
			for _, ch := range rename.Channels {
				if err := dispatchRename(ctx, renameBackend, ch.Address, ids[ch.Address], ch.Name); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		})
	}
}

// NewCCUValueSeeder returns the CCU's bulk value seeder over runner, or nil
// for a nil runner. It lets a caller outside the bring-up — an integration
// harness driving [DevicePipeline.IngestFromBackend] directly — seed values
// the way a CCU bring-up does.
func NewCCUValueSeeder(runner *rega.Runner) ValueSeeder {
	if runner == nil {
		return nil
	}
	return &ccuValueSeeder{runner: runner}
}

// ccuValueSeeder reads an interface's current values with the ReGa script
// fetch_all_device_data: one JSON object keyed by `<iface>.<channel>.<param>`
// (URL-encoded).
type ccuValueSeeder struct {
	runner *rega.Runner
}

// SeedValues implements [ValueSeeder]. The script is one call whatever the
// depth.
//
// The interface is passed by its BARE name ("HmIP-RF"): the CCU's
// `interfaces.Get(<name>)` only knows raw interface labels, not the
// daemon-internal `<central>-<iface>` wire id — with the wire id the script
// finds no interface and returns `{}`, and every data point stays unobserved
// at boot.
func (s *ccuValueSeeder) SeedValues(ctx context.Context, iface hmenum.Interface, _ SeedDepth) (map[string]map[string]any, error) {
	var values map[string]json.RawMessage
	if err := s.runner.RunJSON(ctx, hmenum.RegaScriptFetchAllDeviceData,
		map[string]string{"interface": string(iface)}, &values); err != nil {
		return nil, err
	}
	return decodeFetchAllDeviceData(values), nil
}

// decodeFetchAllDeviceData turns the script's flat, URL-encoded object into
// channel → parameter → value. Keys that do not decode or do not carry three
// parts are dropped, as are values that are not JSON.
func decodeFetchAllDeviceData(values map[string]json.RawMessage) map[string]map[string]any {
	out := make(map[string]map[string]any)
	for rawKey, raw := range values {
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			continue
		}
		parts := strings.SplitN(key, ".", 3)
		if len(parts) != 3 {
			continue
		}
		channelAddr, parameter := parts[1], parts[2]
		// Unmarshal the raw value; the script emits bare booleans,
		// numbers, or double-quoted strings — json.Unmarshal into any
		// handles all three without further branching.
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		// String-valued data points are wrapped in UriEncode() by the
		// script (fetch_all_device_data.fn) so an embedded quote or
		// control character cannot break the surrounding JSON envelope —
		// only booleans and numbers are emitted unencoded. That encoding
		// survives json.Unmarshal untouched (it is just the string's
		// content), so a string value must be decoded here the same way
		// the key already is above; skipping it left values such as an
		// IP_ADDRESS data point's "192.0.2.40" seeded into the model as
		// the literal "172%2E18%2E4%2E40".
		//
		// Decoding goes through the package's canonical ReGa decoder, not a
		// bare unescape: the CCU emits ISO-8859-1, so "Sp%FCle" unescapes to
		// a raw 0xFC byte that is invalid UTF-8. The value is seeded into the
		// live model and re-encoded by every north-bound plane, where
		// json.Marshal replaces it with U+FFFD — irreversible corruption of a
		// value the hub path renders correctly.
		if s, ok := v.(string); ok {
			v = decodeRegaField(s)
		}
		params := out[channelAddr]
		if params == nil {
			params = make(map[string]any)
			out[channelAddr] = params
		}
		params[parameter] = v
	}
	return out
}
