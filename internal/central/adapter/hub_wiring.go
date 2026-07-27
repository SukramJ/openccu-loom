// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/observer"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// NameMap maps a CCU device or channel address to its operator-
// assigned human-readable name. Keys stay in the CCU's native form:
// upper-case hexadecimal for devices (e.g. "0001ABCD") and
// "<addr>:<channel>" for channels.
type NameMap map[string]string

// HubData bundles the metadata returned from the CCU during hub
// bootstrap that downstream wiring needs to stamp on devices and
// channels: human-readable names, room assignments, and function
// (Gewerk) assignments. Each map may be nil when the CCU did not
// provide the corresponding data — the pipeline tolerates that.
type HubData struct {
	Names     NameMap
	Rooms     AssignmentMap
	Functions AssignmentMap
}

// WireHub builds a JSON-RPC client against the CCU, logs in, pulls
// the program and system-variable catalogue, and seeds
// unit.HubModel. It returns the shared Rega runner so downstream
// southbound wiring (e.g. device-value seeding via
// `fetch_all_device_data`) reuses the same authenticated session,
// and a [HubData] bundle (names + room / function assignments) so
// the pipeline can stamp device + channel metadata during ingest.
// The returned closer tears the session down on daemon shutdown.
//
// Failures during program / sysvar / name / room / function load are
// logged but do not abort wiring — an empty hub view is preferable
// to a dead daemon when only the hub endpoint misbehaves.
// serialResolveAttempts bounds how many times [resolveCCUSerial] retries the
// CCU serial fetch before giving up and letting the bring-up gate re-wait. The
// CCU has already passed the readiness probe, so a non-empty serial is expected
// almost immediately; the retries only absorb a transient JSON-RPC blip.
const serialResolveAttempts = 5

// serialResolveBackoff is the wait between serial-fetch attempts.
const serialResolveBackoff = time.Second

// resolveCCUSerial fetches the CCU hardware serial with a bounded retry. It
// returns the resolved serial — the central-id slot of every hub / internal /
// virtual-remote canonical unique_id — or an error when it stays empty/unknown
// across every attempt (or ctx is cancelled). An empty serial is a failure on
// purpose: serving entities with an unresolved serial produces broken
// `loom__…` keys the client cannot rely on.
func resolveCCUSerial(ctx context.Context, runner *rega.Runner, logger *slog.Logger) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= serialResolveAttempts; attempt++ {
		serial, err := runner.GetSerial(ctx)
		switch {
		case err != nil:
			lastErr = err
			logger.Warn("hub.serial.fetch_failed",
				slog.Int("attempt", attempt), slog.String("err", err.Error()))
		case serial == "" || serial == "unknown":
			lastErr = errors.New("CCU returned an empty serial")
			logger.Warn("hub.serial.empty", slog.Int("attempt", attempt))
		default:
			return serial, nil
		}
		if attempt < serialResolveAttempts {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(serialResolveBackoff):
			}
		}
	}
	return "", lastErr
}

// WireHub establishes the central's JSON-RPC hub session, stamps its
// SystemInformation (URL, backend info, and the readiness-gated CCU serial via
// [resolveCCUSerial]), initialises the hub model and its remote operations, and
// loads the initial sysvar / program / name / room / function data. It returns
// the live ReGa runner, the loaded hub data, a closer that tears the session
// down, and an error when any prerequisite (including serial resolution) fails —
// in which case the bring-up gate re-waits.
func WireHub( //nolint:funlen // composition/wiring: long sequential setup
	ctx context.Context,
	cc config.CentralConfig,
	unit *central.Unit,
	logger *slog.Logger,
	catalogs *i18n.Catalogs,
	locale string,
) (*rega.Runner, HubData, func(), error) {
	endpoint := jsonrpcEndpoint(cc)
	hubComponent := "hub." + cc.Name
	jc, err := jsonrpc.New(jsonrpc.Config{
		Endpoint:   endpoint,
		Username:   cc.Username,
		Password:   cc.Password,
		Host:       cc.Host,
		HTTPClient: jsonrpcHTTPClient(cc),
		Logger:     logger.With(slog.String("transport", "jsonrpc")),
		Observer: observer.NewMulti(
			observer.NewLogging(observer.WithLogger(logger), observer.WithSlowThreshold(2*time.Second)),
			observer.NewHealth(unit.Health, observer.WithComponentName(hubComponent)),
		),
	})
	if err != nil {
		return nil, HubData{}, nil, fmt.Errorf("jsonrpc.New: %w", err)
	}
	if err := jc.Login(ctx); err != nil {
		return nil, HubData{}, nil, fmt.Errorf("jsonrpc.Login: %w", err)
	}

	runner, err := rega.NewRunner(rega.Config{Client: jc, Logger: logger})
	if err != nil {
		//nolint:contextcheck // error path cleanup: ctx may have been consumed; use a detached logout to ensure the session is closed
		_ = jc.Logout(context.Background())
		return nil, HubData{}, nil, fmt.Errorf("rega.NewRunner: %w", err)
	}

	writer := &hubJSONRPCWriter{json: jc, rega: runner}
	// Guarded setters (not direct field assignment) so the background WireHub
	// recovery can re-apply these without racing a concurrent hub write.
	unit.HubModel.SetMutator(writer)
	unit.HubModel.Update.SetFirmwareUpdater(writer)
	// Wire the single- and bulk-message acknowledgers so REST/WS/MQTT
	// acknowledge and acknowledge-all calls reach the CCU ReGa engine.
	msgAck := hubMessageAck{runner: runner}
	unit.HubModel.Messages.SetAcknowledgers(msgAck, msgAck)
	unit.HubModel.ServiceMessages.SetAcknowledgers(msgAck, msgAck)
	// Post-install progress monitor: after a triggered CCU system update, watch
	// the firmware version and clear the in-progress flag once it changes.
	// See launchSystemUpdateProgressMonitor.
	{
		upd := unit.HubModel.Update
		//nolint:contextcheck // detached monitor owns its own context lifecycle (must outlive WireHub), like runInitialSystemUpdateLoad
		upd.SetInstallMonitor(func() { launchSystemUpdateProgressMonitor(upd, runner) })
	}

	// Wire the JSON-RPC executor so HubCoordinator.ExecuteProgram
	// delegates to the same session used for every other hub operation.
	if unit.Hub != nil {
		unit.Hub.SetProgramExecutor(writer)
	}

	// Wire the per-interface connectivity aggregate so the Reconciler's
	// reconcileConnectivity pass (and the daemon-level Reconciler.Connectivity
	// slot) have a target to write. Without this the Reconciler short-circuits
	// and no ConnectivityChangedEvents are emitted for the slow-cadence sweep.
	connectivity := hub.NewConnectivity()
	unit.HubModel.SetConnectivity(connectivity)

	// Wire the connectivity PROBE alongside the target above. The
	// reconcileConnectivity pass needs BOTH the Connectivity cache (set
	// here) AND a Connect probe (the read-only Interface.listInterfaces
	// source); with only the target the pass short-circuits on a nil probe
	// and the slow-cadence connectivity-drift sync never fires. The probe
	// reuses this hub session, so it stays alive exactly as long as the
	// reconcile job can run. Reconciler is nil in WireHub-only tests that
	// run without the daemon bootstrap, hence the guard.
	if unit.Reconciler != nil {
		unit.Reconciler.SetConnect(NewJSONRPCConnectivityProbe(jc))
	}

	// Stamp the CCU-side metadata on the central's SystemInfo:
	// configuration URL (MQTT-Discovery `configuration_url`; without it
	// HA's "Visit device" link disappears), model / version / hostname
	// (get_backend_info.fn) and the hardware serial (get_serial.fn).
	si := unit.SystemInformation()
	si.URL = ccuBaseURLFor(cc)
	if info, infoErr := runner.GetBackendInfo(ctx); infoErr != nil {
		logger.Warn("hub.backend_info.fetch_failed", slog.String("err", infoErr.Error()))
	} else {
		si.Model = info.Product
		si.Version = info.Version
		si.Hostname = info.Hostname
		si.IsHaApp = info.IsHAApp
	}
	// The serial is the central-id slot of every canonical HA routing key for
	// hub / internal / virtual-remote data points; an empty serial yields broken
	// `loom__…` unique_ids. It is therefore a hard prerequisite of bring-up: we
	// resolve it (with a bounded retry for transient JSON-RPC blips — the CCU has
	// already passed the readiness probe) before any device is loaded into the
	// registry and served. If it cannot be resolved, WireHub fails and the
	// bring-up gate re-waits, so a central never serves entities with an
	// unresolved serial. Mirrors the readiness-gating philosophy
	// (ccu_readiness.go): availability of one central is held back rather than
	// publishing identity-broken entities the client cannot key on.
	serial, serialErr := resolveCCUSerial(ctx, runner, logger)
	if serialErr != nil {
		//nolint:contextcheck // error path cleanup: detached logout closes the session regardless of ctx state
		_ = jc.Logout(context.Background())
		return nil, HubData{}, nil, fmt.Errorf("hub serial unresolved (central-id slot of every hub/internal/virtual-remote unique_id): %w", serialErr)
	}
	si.Serial = serial
	unit.SetSystemInformation(si)

	// InitHub must run before the first refresh cycle so stale state from a
	// previous run (sysvars, programs) is cleared before new data arrives.
	if unit.Hub != nil {
		unit.Hub.InitHub() //nolint:contextcheck // InitHub has no ctx parameter by design; it clears stale in-memory state synchronously
	}

	scanOpts := hubScanOptionsFromConfig(cc)
	if err := loadPrograms(ctx, jc, runner, unit.HubModel, writer, scanOpts); err != nil {
		logger.Warn("hub.programs.load",
			slog.String("central", cc.Name),
			slog.String("err", err.Error()))
	} else {
		logger.Info("hub.programs.ok",
			slog.String("central", cc.Name),
			slog.Int("count", len(unit.HubModel.Programs())))
	}
	if err := loadSysvars(ctx, jc, runner, unit.HubModel, writer, scanOpts); err != nil {
		logger.Warn("hub.sysvars.load",
			slog.String("central", cc.Name),
			slog.String("err", err.Error()))
	} else {
		logger.Info("hub.sysvars.ok",
			slog.String("central", cc.Name),
			slog.Int("count", len(unit.HubModel.Sysvars())))
	}

	names, iseToAddress, err := loadDeviceNames(ctx, jc)
	if err != nil {
		logger.Warn("hub.device_names.load",
			slog.String("central", cc.Name),
			slog.String("err", err.Error()))
		names = NameMap{}
		iseToAddress = IseAddressMap{}
	} else {
		logger.Info("hub.device_names.ok",
			slog.String("central", cc.Name),
			slog.Int("count", len(names)))
	}

	rooms, err := loadRoomAssignments(ctx, jc, iseToAddress)
	if err != nil {
		logger.Warn("hub.rooms.load",
			slog.String("central", cc.Name),
			slog.String("err", err.Error()))
		rooms = AssignmentMap{}
	} else {
		logger.Info("hub.rooms.ok",
			slog.String("central", cc.Name),
			slog.Int("count", len(rooms)))
	}

	functions, err := loadFunctionAssignments(ctx, jc, iseToAddress)
	if err != nil {
		logger.Warn("hub.functions.load",
			slog.String("central", cc.Name),
			slog.String("err", err.Error()))
		functions = AssignmentMap{}
	} else {
		logger.Info("hub.functions.ok",
			slog.String("central", cc.Name),
			slog.Int("count", len(functions)))
	}

	// Populate the DeviceDetails cache from the already-loaded hub data.
	// This re-uses the three payloads fetched above (names+iseToAddress,
	// rooms, functions) — no extra round-trips. A periodic scheduler job
	// (see below) keeps the cache fresh for running-daemon renames and
	// room changes.
	// (store/dynamic/details.py:123-141).
	populateDeviceDetailsCache(unit.DeviceDetails, names, iseToAddress, rooms, functions)
	logger.Info("hub.device_details.ok",
		slog.String("central", cc.Name))

	// Register a 5-minute refresh job so the cache stays current after
	// operators rename devices or change room assignments via the CCU
	// WebUI. The loader uses the cache-age gate (≈3 s) so rapid-fire
	// reconnects skip redundant reloads automatically.
	loader := devicedetails.NewLoaderForJSONRPC(unit.DeviceDetails, jc, cc.Name, logger)
	if err := unit.Scheduler.Add(scheduler.Job{
		Name:       "devicedetails.refresh." + cc.Name,
		Interval:   5 * time.Minute,
		RunOnStart: false,
		Run: func(ctx context.Context) error {
			return loader.Load(ctx, false)
		},
	}); err != nil {
		// Validation failure (empty name, zero interval, nil Run) — should
		// not happen in practice. The initial population above means the
		// cache is not empty even when the periodic refresh cannot start.
		logger.Warn("hub.device_details.scheduler_add",
			slog.String("central", cc.Name),
			slog.String("err", err.Error()))
	}

	// Wire periodic-refresh hooks on the HubCoordinator. The background
	// scheduler's hub.*_refresh jobs delegate through c.Hub.Refresh*,
	// which call these closures. Without this step the scheduler jobs
	// are registered but run as no-ops because the inner hook is nil.
	if unit.Hub != nil {
		unit.Hub.SetRefreshHooks(coordinators.RefreshHooks{
			Programs: func(ctx context.Context) error {
				if err := loadPrograms(ctx, jc, runner, unit.HubModel, writer, scanOpts); err != nil {
					return err
				}
				// A refresh may add programs whose name carries a device
				// identifier; (re)establish the device links so discovery lands
				// on the right card.
				assignHubChannels(unit, logger)
				return nil
			},
			Sysvars: func(ctx context.Context) error {
				if err := loadSysvars(ctx, jc, runner, unit.HubModel, writer, scanOpts); err != nil {
					return err
				}
				assignHubChannels(unit, logger)
				return nil
			},
			Inbox: func(ctx context.Context) error {
				return loadInbox(ctx, runner, unit)
			},
			ServiceMessages: func(ctx context.Context) error {
				return loadServiceMessages(ctx, runner, unit, catalogs, locale)
			},
			AlarmMessages: func(ctx context.Context) error {
				return loadAlarmMessages(ctx, runner, unit.HubModel, catalogs, locale)
			},
			SystemUpdate: func(ctx context.Context) error {
				return loadSystemUpdate(ctx, runner, unit.HubModel)
			},
			InstallMode: func(ctx context.Context) error {
				return loadInstallMode(ctx, jc, unit)
			},
			Connectivity: func(ctx context.Context) error {
				return loadConnectivity(ctx, unit)
			},
			BidcosInterfaces: func(ctx context.Context) error {
				return loadBidcosInterfaces(ctx, jc, unit)
			},
		})
		// Initial system-update fetch. The reference stack's scheduler
		// runs every job immediately at start (next_run = now), so the
		// firmware state is on the wire right after boot. The Go
		// scheduler first fires hub.system_update_refresh after its
		// 60-minute slot, which left a cleanly booted central without
		// firmware data (observed=false) for up to an hour — only
		// centrals that happened to run a connection recovery (whose
		// pipeline calls RefreshSystemUpdate) had data early.
		// checkFirmwareUpdate.sh contacts the update server and can take
		// several seconds, so the fetch runs detached instead of
		// blocking the remaining hub wiring.
		go runInitialSystemUpdateLoad(unit.Hub, cc.Name, logger) //nolint:gosec,contextcheck // G118: deliberately detached from the wiring ctx — the fetch must survive WireHub returning
	}

	closer := func() { //nolint:contextcheck // shutdown path must not inherit the already-expired wiring ctx
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = jc.Logout(shutdownCtx)
	}
	return runner, HubData{Names: names, Rooms: rooms, Functions: functions}, closer, nil
}

// populateDeviceDetailsCache seeds the DeviceDetails cache from the
// already-loaded hub-wiring data (names + ise→address map + room
// assignments + function assignments). It avoids a second round-trip
// to the CCU by reusing the payloads fetched by loadDeviceNames,
// loadRoomAssignments, and loadFunctionAssignments above.
//
// Interface tags are NOT populated here because hub_wiring.go's
// loadDeviceNames does not read the "interface" field. The
// devicedetails.Loader (used by the periodic scheduler job) does
// fetch the interface field via Device.listAllDetail; until the first
// refresh fires, GetInterface falls back to BidCos-RF (cache.go:174).
func populateDeviceDetailsCache(
	cache *devicedetails.Cache,
	names NameMap,
	iseToAddress IseAddressMap,
	rooms AssignmentMap,
	functions AssignmentMap,
) {
	if cache == nil {
		return
	}
	cache.Clear()

	// : names and ISE-IDs from loadDeviceNames output.
	// iseToAddress has ISE-ID (string) → address; invert to get
	// address → ISE-ID for AddAddressISEID.
	for iseID, addr := range iseToAddress {
		// parseIntStr is defined in loader.go within the same package
		// use strconv here since we're in a different package.
		var id int64
		for _, c := range iseID {
			if c < '0' || c > '9' {
				id = 0
				break
			}
			id = id*10 + int64(c-'0')
		}
		if id > 0 {
			cache.AddAddressISEID(addr, int(id))
		}
	}
	for addr, name := range names {
		cache.AddName(addr, name)
	}

	// : room assignments.
	for addr, roomList := range rooms {
		for _, r := range roomList {
			cache.AddChannelRoom(addr, r)
		}
	}

	// : function assignments.
	for addr, fnList := range functions {
		for _, f := range fnList {
			cache.AddFunction(addr, f)
		}
	}

	cache.MarkRefreshed(time.Now())
}

// deviceDetailEntry is the subset of Device.listAllDetail the UI
// layer needs: an address, a display name, and the channel
// breakdown. The `ID` field is the CCU's internal ISE-ID used to
// resolve room / function assignments — Room.getAll and
// Subsection.getAll return channelIds keyed by ISE-ID, not by
// address.
type deviceDetailEntry struct {
	Address  string              `json:"address"`
	Name     string              `json:"name"`
	ID       string              `json:"id"`
	Channels []deviceDetailEntry `json:"channels,omitempty"`
}

// IseAddressMap maps a CCU internal ISE-ID (numeric, but transported
// as string by the JSON-RPC layer) to the corresponding device or
// channel address. Built once during hub bootstrap from
// Device.listAllDetail and consumed by the room / function loaders to
// resolve their channelIds back into addresses.
type IseAddressMap map[string]string

// AssignmentMap maps a CCU device or channel address to the set of
// names assigned to it. Built from Room.getAll / Subsection.getAll +
// IseAddressMap. The slice is alphabetically sorted for deterministic
// rendering downstream (MQTT-Discovery `suggested_area`, REST,
// Config-UI). Empty value slice is never present — addresses without
// assignments are simply absent from the map.
type AssignmentMap map[string][]string

// loadDeviceNames pulls Device.listAllDetail and flattens it into a lookup
// table mapping every device + channel address to its CCU- assigned name AND
// a parallel ISE-ID → address map.
//
// The two maps are produced from a single round-trip because both are derived
// from the same `Device.listAllDetail` payload — fetching twice would
// needlessly double the latency on slow CCUs.
func loadDeviceNames(ctx context.Context, jc *jsonrpc.Client) (NameMap, IseAddressMap, error) {
	var details []deviceDetailEntry
	if err := jc.Call(ctx, "Device.listAllDetail", nil, &details); err != nil {
		return nil, nil, err
	}
	names := make(NameMap, len(details)*8)
	ise := make(IseAddressMap, len(details)*8)
	for _, d := range details {
		if d.Address != "" && d.Name != "" {
			names[d.Address] = d.Name
		}
		if d.Address != "" && d.ID != "" {
			ise[d.ID] = d.Address
		}
		for _, ch := range d.Channels {
			if ch.Address != "" && ch.Name != "" {
				names[ch.Address] = ch.Name
			}
			if ch.Address != "" && ch.ID != "" {
				ise[ch.ID] = ch.Address
			}
		}
	}
	return names, ise, nil
}

// roomEntry / subsectionEntry mirror the JSON shape returned by
// Room.getAll and Subsection.getAll on the CCU. Both endpoints share
// The same `{id, name, channelIds}` envelope
// different methods because the CCU treats rooms and functions as
// separate but parallel taxonomies (rooms = locations, subsections =
// "Gewerke" / trade groups).
type roomEntry struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ChannelIDs []string `json:"channelIds"`
}

type subsectionEntry struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ChannelIDs []string `json:"channelIds"`
}

// loadRoomAssignments calls Room.getAll, resolves each entry's channelIds
// into addresses via iseToAddress, and aggregates them into a single
// AssignmentMap. Returns a non-nil empty map on a CCU with no rooms.
func loadRoomAssignments(ctx context.Context, jc *jsonrpc.Client, iseToAddress IseAddressMap) (AssignmentMap, error) {
	var rooms []roomEntry
	if err := jc.Call(ctx, "Room.getAll", nil, &rooms); err != nil {
		return nil, err
	}
	return buildAssignments(rooms, iseToAddress, func(r roomEntry) (string, []string) {
		return r.Name, r.ChannelIDs
	}), nil
}

// loadFunctionAssignments calls Subsection.getAll and aggregates the
// Result the same way as [loadRoomAssignments].
// `get_all_functions` (client/backends/ccu.py:232-241).
//
// "Subsection" is the CCU's internal name for what the UI labels
// "Gewerk"
// that vocabulary for consistency with the Python API.
func loadFunctionAssignments(ctx context.Context, jc *jsonrpc.Client, iseToAddress IseAddressMap) (AssignmentMap, error) {
	var fns []subsectionEntry
	if err := jc.Call(ctx, "Subsection.getAll", nil, &fns); err != nil {
		return nil, err
	}
	return buildAssignments(fns, iseToAddress, func(s subsectionEntry) (string, []string) {
		return s.Name, s.ChannelIDs
	}), nil
}

// buildAssignments aggregates a slice of {name, channelIds} entries
// into a deterministic address → []name map. Generic helper shared by
// loadRoomAssignments + loadFunctionAssignments.
//
// The resolver also stamps the **device** address with every name that
// Applies to *any* of its channels
// HA's MQTT-Discovery groups entities by device, so a "Wohnzimmer Licht"
// channel implies the device belongs to "Wohnzimmer" too.
func buildAssignments[T any](entries []T, iseToAddress IseAddressMap, decode func(T) (string, []string)) AssignmentMap {
	// First pass: collect names per address as a set (dedup).
	sets := make(map[string]map[string]struct{})
	for _, e := range entries {
		name, iseIDs := decode(e)
		if name == "" {
			continue
		}
		for _, iseID := range iseIDs {
			addr, ok := iseToAddress[iseID]
			if !ok {
				continue
			}
			if sets[addr] == nil {
				sets[addr] = make(map[string]struct{})
			}
			sets[addr][name] = struct{}{}
			// Stamp the parent device address too, so device-level
			// consumers (MQTT-Discovery, REST `/devices/<addr>`) see
			// the union of every channel's assignments.
			if i := strings.IndexByte(addr, ':'); i > 0 {
				devAddr := addr[:i]
				if sets[devAddr] == nil {
					sets[devAddr] = make(map[string]struct{})
				}
				sets[devAddr][name] = struct{}{}
			}
		}
	}
	// Second pass: collapse sets to sorted slices.
	out := make(AssignmentMap, len(sets))
	for addr, set := range sets {
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		out[addr] = names
	}
	return out
}

// jsonrpcEndpoint composes the CCU's JSON-RPC URL. The CCU exposes
// the endpoint at `/api/homematic.cgi` on ports 80 (plain) / 443
// (TLS). The per-central TLS flag drives the scheme.
func jsonrpcEndpoint(cc config.CentralConfig) string {
	return ccuBaseURLFor(cc) + "/api/homematic.cgi"
}

// ccuBaseURLFor returns the CCU's base URL (scheme + host + port,
// no path). Used by the JSON-RPC endpoint helper and by the backup
// restorer's `/config/cp_security.cgi` POST. cc.JSONRPCPort wins
// over the per-scheme default when set; tests against an in-process CCU
// simulator and non-standard reverse-proxy deployments rely on that override.
func ccuBaseURLFor(cc config.CentralConfig) string {
	scheme := "http"
	port := 80
	if cc.TLS {
		scheme = "https"
		port = 443
	}
	if cc.JSONRPCPort > 0 {
		port = cc.JSONRPCPort
	}
	return fmt.Sprintf("%s://%s:%d", scheme, cc.Host, port)
}

// jsonrpcHTTPClient returns an http.Client honouring the central's
// TLSInsecureSkipVerify flag. The JSON-RPC transport uses its own
// default client when nil is passed, which does not allow self-signed
// certificates — so this override exists purely for the insecure path.
func jsonrpcHTTPClient(cc config.CentralConfig) *http.Client {
	if !cc.TLS || !cc.TLSInsecureSkipVerify {
		return nil
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // explicit opt-in via config; see #20
			},
		},
	}
}

// programEntry mirrors the fields Program.getAll surfaces that we
// care about. Unknown fields are ignored by the JSON decoder.
type programEntry struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	IsActive        bool            `json:"isActive"`
	IsInternal      bool            `json:"isInternal"`
	LastExecuteTime json.RawMessage `json:"lastExecuteTime"`
}

// hubScanOptions carries the per-central hub-scan toggles resolved
// from [config.CentralBehavior] into the load functions.
type hubScanOptions struct {
	enableSysvarScan        bool
	enableProgramScan       bool
	includeInternalSysvars  bool
	includeInternalPrograms bool
	sysvarMarkers           []hmenum.DescriptionMarker
	programMarkers          []hmenum.DescriptionMarker
}

// hubScanOptionsFromConfig resolves the hub-scan toggles for a central.
func hubScanOptionsFromConfig(cc config.CentralConfig) hubScanOptions {
	return hubScanOptions{
		enableSysvarScan:        cc.Behavior.EnableSysvarScanEnabled(),
		enableProgramScan:       cc.Behavior.EnableProgramScanEnabled(),
		includeInternalSysvars:  cc.Behavior.IncludeInternalSysvarsEnabled(),
		includeInternalPrograms: cc.Behavior.IncludeInternalProgramsEnabled(),
		sysvarMarkers:           cc.Behavior.SysvarMarkers,
		programMarkers:          cc.Behavior.ProgramMarkers,
	}
}

// markerMatch reports whether desc carries one of the marker tokens
// (prefix match on the trimmed description). An empty marker list
// matches everything. Mirrors the reference stack's
// description-marker hub filter.
func markerMatch(desc string, markers []hmenum.DescriptionMarker) bool {
	if len(markers) == 0 {
		return true
	}
	d := strings.TrimSpace(desc)
	for _, m := range markers {
		if m != "" && strings.HasPrefix(d, string(m)) {
			return true
		}
	}
	return false
}

// hubEnabledDefault resolves the enabled-by-default flag for a sysvar or
// program that has passed the inclusion filter. It mirrors the reference
// stack: an entry is enabled by default only when a marker is configured
// and it matched (internal entries require the INTERNAL marker). With no
// markers configured every included entry is disabled by default, so the
// operator opts in per entity.
func hubEnabledDefault(isInternal bool, desc string, markers []hmenum.DescriptionMarker) bool {
	if len(markers) == 0 {
		return false
	}
	if isInternal {
		for _, m := range markers {
			if m == hmenum.DescriptionMarkerInternal {
				return true
			}
		}
		return false
	}
	return markerMatch(desc, markers)
}

// programMeta carries the per-program metadata decoded from the
// get_program_descriptions ReGa script: the human-readable PrgInfo
// description used for marker filtering, plus the compact rule summaries
// surfaced to north-bound clients.
type programMeta struct {
	description      string
	conditionSummary string
	activitySummary  string
}

// ruleSummaryMaxRunes caps a rule summary so a program with a large
// rule tree cannot bloat the /programs payload or the UI column. The
// script already renders only the root rule; this is the display safety
// net. See truncateRuleSummary.
const ruleSummaryMaxRunes = 200

// programMetadata fetches per-program metadata (PrgInfo description plus
// the root-rule summaries) via the get_program_descriptions ReGa script.
// Program.getAll omits these fields, so the script costs one extra ReGa
// round-trip; it is issued whenever a runner is available because the
// rule summaries are surfaced regardless of whether program markers are
// configured. Returns an empty map when no runner is wired or the script
// fails — callers then fall back to blank descriptions and summaries.
func programMetadata(ctx context.Context, runner *rega.Runner) map[string]programMeta {
	out := make(map[string]programMeta)
	if runner == nil {
		return out
	}
	descs, err := runner.GetProgramDescriptions(ctx)
	if err != nil {
		return out
	}
	for _, d := range descs {
		out[d.ID] = programMeta{
			description:      decodeRegaField(d.Description),
			conditionSummary: truncateRuleSummary(decodeRegaField(d.ConditionSummary)),
			activitySummary:  truncateRuleSummary(decodeRegaField(d.ActivitySummary)),
		}
	}
	return out
}

// truncateRuleSummary caps a rule summary at ruleSummaryMaxRunes runes,
// appending a single-character ellipsis when it overflows. Rune-aware so
// a multibyte channel name is never split mid-character.
func truncateRuleSummary(s string) string {
	r := []rune(s)
	if len(r) <= ruleSummaryMaxRunes {
		return s
	}
	return string(r[:ruleSummaryMaxRunes]) + "…"
}

func loadPrograms(ctx context.Context, jc *jsonrpc.Client, runner *rega.Runner, h *hub.Hub, writer hub.ProgramWriter, opts hubScanOptions) error {
	if !opts.enableProgramScan {
		return nil
	}
	// The fetch is complete: internal (Tmp_*, prgEnergyCounter_*) programs
	// are always loaded into the hub so the daemon knows them. The
	// include_internal_programs config only steers the *delivery* default,
	// recorded here so northbound list responses that omit an explicit
	// override reproduce the historical (hide-by-default) behaviour.
	h.SetIncludeInternalProgramsDefault(opts.includeInternalPrograms)
	var programs []programEntry
	if err := jc.Call(ctx, "Program.getAll", nil, &programs); err != nil {
		return err
	}
	metaByID := programMetadata(ctx, runner)
	// Collect the fresh ID set for the stale-entry diff below.
	freshIDs := make(map[string]struct{}, len(programs))
	for _, p := range programs {
		if p.ID == "" {
			continue
		}
		meta := metaByID[p.ID]
		if !markerMatch(meta.description, opts.programMarkers) {
			continue
		}
		// The description field stays coupled to marker filtering: it is only
		// exposed when program markers are configured (mirroring the prior
		// behaviour), whereas the rule summaries below are surfaced
		// unconditionally.
		desc := ""
		if len(opts.programMarkers) > 0 {
			desc = meta.description
		}
		freshIDs[p.ID] = struct{}{}
		if existing, ok := h.Program(p.ID); ok {
			// Update the existing pointer in-place so subscribers wired via
			// OnUpdate (e.g. MQTT publisher) remain valid across periodic
			// refreshes. Only create a new Program when the ID is genuinely new.
			existing.UpdateMetadata(p.Name, p.IsInternal, writer)
			existing.EnabledDefault = hubEnabledDefault(p.IsInternal, meta.description, opts.programMarkers)
			existing.OnActive(p.IsActive)
			existing.SetRuleSummary(meta.conditionSummary, meta.activitySummary)
		} else {
			prog := hub.NewProgram(h.CentralName, p.ID, p.Name, desc, p.IsInternal, writer)
			prog.EnabledDefault = hubEnabledDefault(p.IsInternal, meta.description, opts.programMarkers)
			prog.OnActive(p.IsActive)
			prog.SetRuleSummary(meta.conditionSummary, meta.activitySummary)
			h.PutProgram(prog)
		}
	}
	// Remove programs that are no longer present on the CCU or no longer
	// pass the marker filter. Internal programs are kept unconditionally;
	// their visibility is a delivery-time concern, not a fetch one.
	for _, existing := range h.Programs() {
		if _, ok := freshIDs[existing.ID]; !ok {
			h.RemoveProgram(existing.ID)
		}
	}
	return nil
}

// sysvarEntry mirrors SysVar.getAll. The CCU sends numeric values as
// strings so we keep Value as RawMessage and coerce per-variable.
type sysvarEntry struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Value      json.RawMessage `json:"value"`
	Unit       string          `json:"unit"`
	ValueList  string          `json:"valueList"`
	MaxValue   json.RawMessage `json:"maxValue"`
	MinValue   json.RawMessage `json:"minValue"`
	IsInternal bool            `json:"isInternal"`
	// IsVisible / IsLogged mirror the CCU-WebUI visibility and archive
	// flags. IsLogged is backed by the CCU-side DPArchive setting.
	IsVisible bool `json:"isVisible"`
	IsLogged  bool `json:"isLogged"`
	// ValueName0 / ValueName1 are the false / true value labels the CCU
	// reports for LOGIC and ALARM variables only.
	ValueName0  string `json:"valueName0"`
	ValueName1  string `json:"valueName1"`
	Description string `json:"description"`
}

// sysvarDescriptionMarker is the CCU-side marker that indicates a
// sysvar was created by an extended integration. Its presence in the
// description field sets IsExtended on the resulting Sysvar.
const sysvarDescriptionMarker = hmenum.DescriptionMarkerHAHM

// parseSysvarDescription strips the known marker tokens from the raw CCU
// description and returns the cleaned string plus whether the HAHM marker
// was present.
func parseSysvarDescription(raw string) (cleaned string, isExtended bool) {
	markers := []hmenum.DescriptionMarker{
		hmenum.DescriptionMarkerHAHM,
		hmenum.DescriptionMarkerHX,
		hmenum.DescriptionMarkerInternal,
		hmenum.DescriptionMarkerMQTT,
	}
	isExtended = false
	out := raw
	for _, m := range markers {
		if strings.Contains(out, string(m)) {
			if m == sysvarDescriptionMarker {
				isExtended = true
			}
			out = strings.ReplaceAll(out, string(m), "")
		}
	}
	return strings.TrimSpace(out), isExtended
}

// sysvarIsExcluded reports whether a CCU system variable never enters the
// hub model. Mirrors the reference stack's two hard filters, applied at
// fetch time so every plane (REST, MQTT discovery, Matter, external
// clients) sees the same catalogue:
//   - names carrying "OldVal"/"pcCCUID" are CCU calculation scratch values
//     (model/hub/hub.py `_EXCLUDED`)
//   - the fixed IDs 40/41 (alarm/service messages) are surfaced through
//     dedicated hub singletons (const.py `IGNORE_SYSVARS_BY_ID`)
func sysvarIsExcluded(name, id string) bool {
	if id == "40" || id == "41" {
		return true
	}
	for _, token := range []string{"OldVal", "pcCCUID"} {
		if strings.Contains(name, token) {
			return true
		}
	}
	return false
}

func loadSysvars(ctx context.Context, jc *jsonrpc.Client, runner *rega.Runner, h *hub.Hub, writer hub.SysvarWriter, opts hubScanOptions) error {
	if !opts.enableSysvarScan {
		return nil
	}
	var vars []sysvarEntry
	if err := jc.Call(ctx, "SysVar.getAll", nil, &vars); err != nil {
		return err
	}
	// SysVar.getAll does not carry descriptions — the reference stack reads
	// them through a dedicated ReGa script (sv.DPInfo() per variable) and
	// derives the extended/INTERNAL markers from the decoded text. Without
	// this call is_extended never fires and extended variables spawn as
	// read-only sensors instead of switch/select/number/text. Best-effort:
	// a failed script run degrades to the (empty) getAll description.
	//
	// The same script also reports the channel each variable is explicitly
	// assigned to in the CCU WebUI ("Kanalzuordnung"); that assignment is
	// the primary input for the device-link resolution in
	// [assignHubChannels]. haveDescs gates the explicit-channel updates so
	// a transiently failing script run keeps the last known assignments
	// instead of clearing them.
	descByID := make(map[string]string)
	chanByID := make(map[string]string)
	haveDescs := false
	if runner != nil {
		if descs, err := runner.GetSystemVariableDescriptions(ctx); err == nil {
			haveDescs = true
			for _, d := range descs {
				descByID[d.ID] = decodeRegaField(d.Description)
				if d.ChannelAddress == "" {
					continue
				}
				if decoded := decodeRegaField(d.ChannelAddress); decoded != "" {
					chanByID[d.ID] = decoded
				} else {
					chanByID[d.ID] = d.ChannelAddress
				}
			}
		}
	}
	// Collect the fresh name set for the stale-entry diff below.
	freshNames := make(map[string]struct{}, len(vars))
	for i := range vars {
		v := &vars[i]
		if v.Name == "" || sysvarIsExcluded(v.Name, v.ID) {
			continue
		}
		if v.IsInternal && !opts.includeInternalSysvars {
			continue
		}
		valueType := inferSysvarType(v.Type, v.Value)
		var valueList []string
		if v.ValueList != "" {
			valueList = strings.Split(v.ValueList, ";")
		}
		rawDesc := v.Description
		if d, ok := descByID[v.ID]; ok && d != "" {
			rawDesc = d
		}
		if !markerMatch(rawDesc, opts.sysvarMarkers) {
			continue
		}
		freshNames[v.Name] = struct{}{}
		upsertSysvar(h, v, writer, opts, rawDesc, valueType, valueList, chanByID[v.ID], haveDescs)
	}
	// Remove sysvars that are no longer present on the CCU.
	for _, existing := range h.Sysvars() {
		if _, ok := freshNames[existing.Name]; !ok {
			h.RemoveSysvar(existing.Name)
		}
	}
	return nil
}

// upsertSysvar creates or in-place-updates the [hub.Sysvar] for one
// SysVar.getAll entry. Existing pointers are updated in place so
// subscribers wired via OnUpdate (e.g. the MQTT publisher) remain valid
// across periodic refreshes; a new Sysvar is only allocated when the
// name is new. haveDescs gates the explicit-channel update so a
// transiently failing description script keeps the last known
// assignment instead of clearing it (a fresh Sysvar always applies the
// current value — there is no prior state to preserve).
func upsertSysvar(h *hub.Hub, v *sysvarEntry, writer hub.SysvarWriter, opts hubScanOptions, rawDesc string, valueType hmenum.HubValueType, valueList []string, explicitChannel string, haveDescs bool) {
	desc, isExtended := parseSysvarDescription(rawDesc)
	existing, ok := h.Sysvar(v.Name)
	if !ok {
		existing = hub.NewSysvar(h.CentralName, v.Name, desc, valueType, writer)
	}
	existing.Unit = v.Unit
	existing.ValueType = valueType
	existing.ValueList = valueList
	existing.Writer = writer
	existing.IsExtended = isExtended
	existing.IsInternal = v.IsInternal
	existing.IsVisible = v.IsVisible
	existing.IsLogged = v.IsLogged
	existing.ValueName0 = v.ValueName0
	existing.ValueName1 = v.ValueName1
	existing.Description = desc
	existing.EnabledDefault = hubEnabledDefault(v.IsInternal, rawDesc, opts.sysvarMarkers)
	if haveDescs || !ok {
		existing.SetExplicitChannel(explicitChannel)
	}
	if vid, err := strconv.Atoi(v.ID); err == nil {
		existing.Vid = vid
	}
	if pv, pok := parseSysvarValue(valueType, v.Value); pok {
		existing.OnValue(pv)
	}
	if !ok {
		h.PutSysvar(existing)
	}
}

// assignHubChannels associates every system variable and program with its
// owning channel and clears the association for those that no longer match.
// When any association changes it publishes a
// [hmevent.HubChannelsAssignedEvent] so north-bound adapters re-publish the
// affected discovery.
//
// Resolution precedence per system variable:
//
//  1. The explicit CCU WebUI channel assignment ("Kanalzuordnung",
//     [hub.Sysvar.ExplicitChannel]) — but only when the address resolves to
//     a device channel registered on THIS central. An unresolvable explicit
//     assignment (device filtered out, on another central, or removed) is
//     logged at debug level and falls through to name matching.
//  2. Name matching: a legacy_name carrying a device or channel identifier —
//     an address suffix, a channel ise_id, or a device ise_id — resolves via
//     [registry.ModelRegistry.IdentifyChannel].
//  3. Otherwise the variable stays unassigned (hub card).
//
// Programs have no CCU-side channel assignment (the WebUI offers no
// Kanalzuordnung for programs), so they resolve by name matching only.
//
// The name-matching half is the openccu-loom equivalent of the Python
// reference's channel_lookup.identify_channel wiring at hub data-point
// construction (`model/hub/data_point.py:84`). openccu-loom materialises
// devices AFTER the hub is scanned (WireHub runs before the per-interface
// IngestFromBackend), so the link cannot be resolved at construction time; it
// is re-established here as an idempotent post-pass, invoked after each device
// ingest and after each periodic sysvar/program refresh. Re-running is cheap
// and safe: every data point is (re)set to its current best match or cleared,
// and the per-entity assignment log fires only when the resolution CHANGED —
// repeated no-op passes stay silent.
func assignHubChannels(unit *central.Unit, logger *slog.Logger) {
	if unit == nil || unit.HubModel == nil || unit.ModelRegistry == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	resolveByName := func(legacyName string) string {
		if _, ch, ok := unit.ModelRegistry.IdentifyChannel(legacyName); ok {
			return ch.Address
		}
		return ""
	}
	changed := false
	for _, sv := range unit.HubModel.Sysvars() {
		next, source := "", "none"
		if explicit := sv.ExplicitChannel(); explicit != "" {
			if channelRegistered(unit.ModelRegistry, explicit) {
				next, source = explicit, "explicit"
			} else {
				logger.Debug("hub.sysvar.explicit_channel_unresolved",
					slog.String("central", unit.Name()),
					slog.String("name", sv.Name),
					slog.String("channel_address", explicit))
			}
		}
		if next == "" {
			if byName := resolveByName(sv.LegacyName()); byName != "" {
				next, source = byName, "name"
			}
		}
		if next != sv.Channel() {
			sv.SetChannel(next)
			changed = true
			logger.Info("hub.sysvar.channel_assigned",
				slog.String("central", unit.Name()),
				slog.String("name", sv.Name),
				slog.String("source", source),
				slog.String("channel", next))
		}
	}
	for _, p := range unit.HubModel.Programs() {
		next, source := "", "none"
		if byName := resolveByName(p.LegacyName()); byName != "" {
			next, source = byName, "name"
		}
		if next != p.Channel() {
			p.SetChannel(next)
			changed = true
			logger.Info("hub.program.channel_assigned",
				slog.String("central", unit.Name()),
				slog.String("name", p.Name),
				slog.String("source", source),
				slog.String("channel", next))
		}
	}
	if changed && unit.EventBus != nil {
		// Signal north-bound adapters (MQTT discovery) to re-publish the
		// affected hub-entity discovery so linked entities move to the right
		// device card. A single per-central event is enough — consumers
		// re-read the current links from the hub model.
		events.Publish(unit.EventBus, hmevent.HubChannelsAssignedEvent{
			Base:        hmevent.NewBase(),
			CentralName: unit.Name(),
		})
	}
}

// channelRegistered reports whether address resolves to a device channel
// registered on this central. The device part (before the ":") selects the
// device; a device-level address without a ":" counts as registered when the
// device itself exists. Used by [assignHubChannels] to validate an explicit
// CCU channel assignment before trusting it — the CCU may reference a device
// that this central never materialised.
func channelRegistered(reg *registry.ModelRegistry, address string) bool {
	if reg == nil || address == "" {
		return false
	}
	deviceAddr := address
	if i := strings.IndexByte(address, ':'); i >= 0 {
		deviceAddr = address[:i]
	}
	d, ok := reg.Get(deviceAddr)
	if !ok || d == nil {
		return false
	}
	if deviceAddr == address {
		return true
	}
	return d.Channel(address) != nil
}

// loadInbox fetches the pending-device inbox via the ReGa script engine and
// refreshes unit.HubModel.Inbox. The inbox is a central-wide ReGa query
// (get_inbox_devices), not a per-interface JSON-RPC poll — the reference
// stack reads it the same way.
func loadInbox(ctx context.Context, r *rega.Runner, unit *central.Unit) error {
	if r == nil || unit == nil || unit.HubModel == nil {
		return nil
	}
	devices, err := r.GetInboxDevices(ctx)
	if err != nil {
		return fmt.Errorf("loadInbox: %w", err)
	}
	all := make([]hub.InboxDevice, 0, len(devices))
	for i := range devices {
		d := &devices[i]
		if d.Address == "" {
			continue
		}
		if isVirtualInboxDevice(d.Address, d.Interface) {
			// The CCU's inbox query lists every not-yet-configured object,
			// which includes the virtual backing devices of heating groups
			// (and other VirtualDevices entries). Those are created and
			// managed through their own flows, never accepted as pairing
			// candidates — filtering them here keeps them out of the pairing
			// inbox instead of surfacing an entry the operator cannot accept.
			continue
		}
		all = append(all, hub.InboxDevice{
			DeviceID:  d.DeviceID,
			Address:   d.Address,
			Name:      decodeRegaField(d.Name),
			Model:     d.DeviceType,
			Interface: d.Interface,
		})
	}
	unit.HubModel.Inbox.Replace(all)
	return nil
}

// isVirtualInboxDevice reports whether an inbox entry is a CCU-internal virtual
// device rather than a physical pairing candidate. Heating-group backing
// devices carry an "INT"-prefixed address (serial = "INT" + zero-padded group
// id); everything on the VirtualDevices interface is likewise virtual. Neither
// is ever accepted through the pairing inbox.
func isVirtualInboxDevice(address, iface string) bool {
	return strings.HasPrefix(address, "INT") || iface == string(hmenum.InterfaceVirtualDevices)
}

// loadServiceMessages fetches active service messages via the ReGa script
// engine (get_service_messages) and refreshes unit.HubModel.ServiceMessages.
func loadServiceMessages(ctx context.Context, r *rega.Runner, unit *central.Unit, catalogs *i18n.Catalogs, locale string) error {
	if r == nil || unit == nil || unit.HubModel == nil {
		return nil
	}
	raw, err := r.GetServiceMessages(ctx)
	if err != nil {
		return fmt.Errorf("loadServiceMessages: %w", err)
	}
	seen := make(map[string]struct{}, len(raw))
	all := make([]hub.ServiceMessage, 0, len(raw))
	for i := range raw {
		m := &raw[i]
		id := m.ID
		if id == "" {
			id = m.Address
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		decodedName := decodeRegaField(m.Name)
		all = append(all, hub.ServiceMessage{
			ID:          id,
			Name:        decodedName,
			Address:     m.Address,
			DeviceName:  decodeRegaField(m.DeviceName),
			Parameter:   serviceMessageParameter(decodedName),
			InterfaceID: interfaceForChannel(unit, m.Address),
			Type:        hmenum.ServiceMessageType(m.Type),
			Counter:     m.Counter,
			DisplayName: messageDisplayName(catalogs, locale, decodedName),
		})
	}
	unit.HubModel.ServiceMessages.Replace(all)
	return nil
}

// loadAlarmMessages fetches all active alarm messages via the ReGa script
// engine (get_alarm_messages) and refreshes h.Messages. The query is
// central-wide; no per-interface iteration is needed.
func loadAlarmMessages(ctx context.Context, r *rega.Runner, h *hub.Hub, catalogs *i18n.Catalogs, locale string) error {
	if r == nil || h == nil {
		return nil
	}
	raw, err := r.GetAlarmMessages(ctx)
	if err != nil {
		return fmt.Errorf("loadAlarmMessages: %w", err)
	}
	msgs := make([]hub.AlarmMessage, 0, len(raw))
	for i := range raw {
		m := &raw[i]
		if m.ID == "" {
			continue
		}
		msgs = append(msgs, hub.AlarmMessage{
			ID:          m.ID,
			Name:        decodeRegaField(m.Name),
			Description: decodeRegaField(m.Description),
			DeviceName:  decodeRegaField(m.DeviceName),
			Counter:     m.Counter,
			LastTrigger: m.LastTrigger,
			DisplayName: messageDisplayName(catalogs, locale, decodeRegaField(m.Name)),
		})
	}
	h.Messages.Replace(msgs)
	return nil
}

// messageDisplayName extracts the message code from rawName (the segment after
// the last dot) and returns the translated string from catalogs using the
// given locale. Falls back to the raw code when catalogs is nil or the key is
// absent, and to rawName itself when no dot is present.
func messageDisplayName(catalogs *i18n.Catalogs, locale, rawName string) string {
	code := rawName
	if idx := strings.LastIndex(rawName, "."); idx >= 0 {
		code = rawName[idx+1:]
	}
	if catalogs == nil || code == "" {
		return code
	}
	key := "message.code." + code
	translated := catalogs.T(locale, key)
	// T returns the key itself when no translation is found.
	if translated == key {
		return code
	}
	return translated
}

// serviceMessageParameter extracts the service parameter from a raw CCU
// service-message name. The name format is "AL-ADDRESS:CHANNEL.PARAM";
// the parameter is the segment after the last dot. Device / channel
// addresses never contain a dot, so the split is unambiguous. Returns ""
// when the name carries no parameter segment — the caller then suppresses
// every service parameter of the channel.
func serviceMessageParameter(rawName string) string {
	if idx := strings.LastIndex(rawName, "."); idx >= 0 {
		return rawName[idx+1:]
	}
	return ""
}

// interfaceForChannel resolves the CCU interface (e.g. "HmIP-RF") that
// owns channelAddress by looking up the device (the part before the ":")
// in the model registry. Returns "" when the device is not registered;
// the suppressor then re-resolves the interface at call time. Multi-CCU-
// safe: the lookup is scoped to unit's own registry.
func interfaceForChannel(unit *central.Unit, channelAddress string) string {
	if unit == nil || unit.ModelRegistry == nil || channelAddress == "" {
		return ""
	}
	deviceAddr := channelAddress
	if i := strings.IndexByte(channelAddress, ':'); i >= 0 {
		deviceAddr = channelAddress[:i]
	}
	if d, ok := unit.ModelRegistry.Get(deviceAddr); ok && d != nil {
		return string(d.Interface)
	}
	return ""
}

// decodeRegaField URL-decodes a field emitted by the get_service_messages,
// get_alarm_messages, get_inbox_devices and get_program_descriptions ReGa
// scripts, which percent-encode human-readable strings (names, device names,
// descriptions, rule summaries). On a decode error the raw value is returned
// unchanged so a single malformed field never drops the whole message.
func decodeRegaField(s string) string {
	if s == "" {
		return ""
	}
	if dec, err := url.QueryUnescape(s); err == nil {
		s = dec
	}
	// ReGa object names are ISO-8859-1 on the CCU, so after URL-unescape a
	// umlaut is a raw Latin-1 high byte — invalid UTF-8 that renders as U+FFFD
	// ("Sp�le" for "Spüle" in a program's condition/activity summary).
	// Transcode when the decoded value is not already valid UTF-8.
	if !utf8.ValidString(s) {
		s = latin1ToUTF8String(s)
	}
	return s
}

// latin1ToUTF8String reinterprets an ISO-8859-1 string's bytes as Unicode
// code points, producing valid UTF-8. ASCII bytes map 1:1, so a mixed string
// keeps its structure while high bytes become correct multi-byte runes.
func latin1ToUTF8String(s string) string {
	runes := make([]rune, len(s))
	for i := range len(s) {
		runes[i] = rune(s[i])
	}
	return string(runes)
}

// systemUpdateRefresher is the narrow slice of the HubCoordinator the
// initial system-update load consumes. Declared as an interface so the
// boot-time path is testable without a full coordinator rig.
type systemUpdateRefresher interface {
	RefreshSystemUpdate(ctx context.Context) error
}

// initialSystemUpdateTimeout bounds the boot-time firmware fetch.
// checkFirmwareUpdate.sh contacts the vendor update server, which can
// stall on networks without internet access; two minutes is generous
// without keeping a goroutine alive forever.
const initialSystemUpdateTimeout = 2 * time.Minute

// systemUpdateProgressCheckInterval + systemUpdateProgressMaxPolls mirror
// schedule_timer_config.system_update_progress_check_interval and
// _timeout (const.py:224,227 = 30 s / 1800 s): after a triggered CCU system
// update, poll the firmware version every 30 s for up to 30 min, then give up.
const (
	systemUpdateProgressCheckInterval = 30 * time.Second
	systemUpdateProgressMaxPolls      = 60 // 30 s × 60 = 30 min
)

// runInitialSystemUpdateLoad performs the one-shot boot-time
// system-update fetch through the coordinator's refresh hook (which
// serialises against the scheduler's hub.system_update_refresh job).
// Runs on its own context — callers detach it with `go` so hub wiring
// is not blocked by the CCU's update-server round-trip.
func runInitialSystemUpdateLoad(h systemUpdateRefresher, centralName string, logger *slog.Logger) {
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), initialSystemUpdateTimeout)
	defer cancel()
	if err := h.RefreshSystemUpdate(ctx); err != nil {
		if logger != nil {
			logger.Warn("hub.system_update.initial_load",
				slog.String("central", centralName),
				slog.String("err", err.Error()))
		}
		return
	}
	if logger != nil {
		logger.Info("hub.system_update.ok",
			slog.String("central", centralName))
	}
}

// launchSystemUpdateProgressMonitor spawns the detached, deadline-bounded
// goroutine that watches the CCU firmware version after a triggered system
// update and clears the in-progress flag once the version changes. Detached on
// purpose — it must outlive WireHub — and self-bounded by the 30 min deadline.
// As a root function (no inherited context) it owns the context lifecycle.
// Mirrors install() spawning _monitor_update_progress
// (model/hub/update.py:127,175; const.py:224,227 = 30 s / 30 min).
func launchSystemUpdateProgressMonitor(upd *hub.Update, r *rega.Runner) {
	ctx, cancel := context.WithTimeout(context.Background(),
		systemUpdateProgressCheckInterval*time.Duration(systemUpdateProgressMaxPolls)+time.Minute)
	go func() {
		defer cancel()
		upd.MonitorProgress(ctx, func(ctx context.Context) (string, error) {
			info, err := r.GetSystemUpdateInfo(ctx)
			if err != nil {
				return "", err
			}
			return info.CurrentFirmware, nil
		}, systemUpdateProgressCheckInterval, systemUpdateProgressMaxPolls)
	}()
}

// loadSystemUpdate fetches the CCU's firmware-update state via the ReGa
// script engine and refreshes h.Update.
//
// The get_system_update_info script derives current_firmware from
// `grep VERSION= /VERSION` via system.Exec. ReGaHss occasionally
// returns an empty exec output while the rest of the payload is valid
// (observed once on a remote OpenCCU 3.87.6 right after a reconnect;
// direct re-runs of the same script over both tclrega and JSON-RPC
// returned the version normally). An empty current_firmware therefore
// never overwrites a previously observed non-empty value — the next
// scheduled refresh re-delivers the real one anyway.
func loadSystemUpdate(ctx context.Context, r *rega.Runner, h *hub.Hub) error {
	if h == nil {
		return nil
	}
	info, err := r.GetSystemUpdateInfo(ctx)
	if err != nil {
		return fmt.Errorf("loadSystemUpdate: %w", err)
	}
	if info.CurrentFirmware == "" {
		if prev, observed := h.Update.UpdateInfo(); observed && prev.CurrentFirmware != "" {
			info.CurrentFirmware = prev.CurrentFirmware
		}
	}
	h.Update.OnInfo(hub.UpdateInfo{
		CurrentFirmware:      info.CurrentFirmware,
		AvailableFirmware:    info.AvailableFirmware,
		UpdateAvailable:      info.UpdateAvailable,
		CheckScriptAvailable: info.CheckScriptAvailable,
	})
	return nil
}

// loadInstallMode reads the remaining install-mode countdown for each
// registered install-mode data point via Interface.getInstallMode and
// calls OnState on each. After all polls, PublishInstallModeRefreshed
// fires events so north-bound adapters pick up the refreshed values.
//
// Data points are read directly from unit.HubModel (the domain model)
// rather than through the coordinator, because HubCoordinator.SetHubModel
// is not called during standard daemon wiring — the model is the
// authoritative registry.
func loadInstallMode(ctx context.Context, jc *jsonrpc.Client, unit *central.Unit) error {
	if unit == nil || unit.HubModel == nil || unit.Hub == nil {
		return nil
	}
	dps := unit.HubModel.InstallModeDPs()
	var firstErr error
	for _, dp := range dps {
		if dp == nil || dp.InterfaceID == "" {
			continue
		}
		secs, err := jc.GetInstallMode(ctx, dp.InterfaceID)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("loadInstallMode %s: %w", dp.InterfaceID, err)
			}
			continue
		}
		enabled := secs > 0
		dp.OnState(enabled, time.Duration(secs)*time.Second)
	}
	unit.Hub.PublishInstallModeRefreshed()
	return firstErr
}

// loadConnectivity probes the CCU's interface-reachability state via the
// Reconciler and updates the per-interface connectivity data points.
// When no Reconciler or Connectivity aggregate is wired, this is a no-op.
func loadConnectivity(ctx context.Context, unit *central.Unit) error {
	if unit == nil || unit.Reconciler == nil {
		return nil
	}
	return unit.Reconciler.Reconcile(ctx)
}

// bidcosInterfaceLister is the south-bound read surface loadBidcosInterfaces
// depends on. [jsonrpc.Client] satisfies it; tests supply a fake.
type bidcosInterfaceLister interface {
	ListBidcosInterfaces(ctx context.Context, iface string) ([]jsonrpc.BidcosInterface, error)
}

// loadBidcosInterfaces polls the CCU's listBidcosInterfaces method for every
// BidCos radio interface the central owns and refreshes the HubCoordinator's
// per-interface duty-cycle / carrier-sense cache. It is a read-only JSON-RPC
// query (no radio traffic). HmIP and wired interfaces carry no BidCos gateway
// and are skipped; their device-level DUTY_CYCLE data points cover them.
//
// When a BidCos interface reports several gateways (the CCU antenna plus LAN
// gateways), the default gateway wins; absent a default, the highest duty
// cycle is chosen so the north-bound warning reflects the worst case.
func loadBidcosInterfaces(ctx context.Context, lister bidcosInterfaceLister, unit *central.Unit) error {
	if unit == nil || unit.Hub == nil || unit.Clients == nil || lister == nil {
		return nil
	}
	snapshot := make(map[string]coordinators.BidcosInterfaceInfo)
	var firstErr error
	for _, e := range unit.Clients.List() {
		if e == nil || e.Interface != hmenum.InterfaceBidCosRF {
			continue
		}
		gateways, err := lister.ListBidcosInterfaces(ctx, e.InterfaceID)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("loadBidcosInterfaces %s: %w", e.InterfaceID, err)
			}
			continue
		}
		if info, ok := aggregateBidcosGateways(gateways); ok {
			snapshot[e.InterfaceID] = info
		}
	}
	unit.Hub.SetBidcosInterfaces(snapshot)
	return firstErr
}

// aggregateBidcosGateways collapses the gateway list of one BidCos interface
// into a single utilisation snapshot. The default gateway is preferred; when
// none is flagged default, the gateway with the highest duty cycle wins.
// Returns false when the list is empty.
func aggregateBidcosGateways(gateways []jsonrpc.BidcosInterface) (coordinators.BidcosInterfaceInfo, bool) {
	var (
		chosen jsonrpc.BidcosInterface
		found  bool
	)
	for _, g := range gateways {
		switch {
		case !found:
			chosen, found = g, true
		case g.Default && !chosen.Default:
			chosen = g
		case g.Default == chosen.Default && g.DutyCycle > chosen.DutyCycle:
			chosen = g
		}
	}
	if !found {
		return coordinators.BidcosInterfaceInfo{}, false
	}
	return coordinators.BidcosInterfaceInfo{
		Address:      chosen.Address,
		Type:         chosen.Type,
		DutyCycle:    chosen.DutyCycle,
		CarrierSense: chosen.CarrierSense,
		Connected:    chosen.Connected,
	}, true
}

// stringField extracts a string value from a raw map[string]any. Returns ""
// when the key is absent or the value is not a string.
func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// inferSysvarType resolves the effective HubValueType for a sysvar. When the
// CCU declares type NUMBER it does not distinguish float from integer; the raw
// value string carries that information via the presence of a decimal point.
// All other declared types are used as-is.
//
// Mirrors the NUMBER→FLOAT/INTEGER split in json_rpc.py:_build_sysvar_record.
func inferSysvarType(declaredType string, rawValue json.RawMessage) hmenum.HubValueType {
	vt := hmenum.HubValueType(strings.ToUpper(declaredType))
	if vt != hmenum.HubValueTypeNumber {
		return vt
	}
	// For NUMBER: inspect the raw string value for a decimal point.
	var s string
	if err := json.Unmarshal(rawValue, &s); err == nil {
		if strings.Contains(s, ".") {
			return hmenum.HubValueTypeFloat
		}
		return hmenum.HubValueTypeInteger
	}
	return vt
}

// parseSysvarValue converts the raw JSON payload the CCU sends (a
// quoted string for most types) into a [hmtypes.ParamValue] matching
// the declared value type. The numeric branches must parse the string
// content — the declared type, not the wire shape, decides the value
// kind: every downstream type dispatch keys on it, most visibly the
// LIST index→label resolution on the MQTT state topic
// (sysvarStateForMQTT), which HA validates against the discovery's
// enum options. Mirrors Python `parse_sys_var`
// (support/__init__.py:116-126).
func parseSysvarValue(vt hmenum.HubValueType, raw json.RawMessage) (hmtypes.ParamValue, bool) {
	if len(raw) == 0 {
		return hmtypes.ParamValue{}, false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		// Fall back to bare numeric / bool shape.
		var n json.Number
		if err := json.Unmarshal(raw, &n); err == nil {
			s = n.String()
		} else {
			return hmtypes.ParamValue{}, false
		}
	}
	switch vt { //nolint:exhaustive // ALARM collapses to bool, unknown types fall through
	case hmenum.HubValueTypeLogic, hmenum.HubValueTypeAlarm:
		switch strings.ToLower(s) {
		case "true", "1":
			return hmtypes.BoolValue(true), true
		case "false", "0":
			return hmtypes.BoolValue(false), true
		}
	case hmenum.HubValueTypeInteger, hmenum.HubValueTypeList:
		// LIST carries the zero-based index into the value list.
		// bitSize 32 bounds the parse so the int conversion stays safe
		// on 32-bit builds (armv7).
		if n, err := strconv.ParseInt(s, 10, 32); err == nil {
			return hmtypes.IntValue(int(n)), true
		}
	case hmenum.HubValueTypeNumber, hmenum.HubValueTypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return hmtypes.FloatValue(f), true
		}
	case hmenum.HubValueTypeString:
		return hmtypes.StringValue(s), true
	}
	// Fallback: preserve as string so the caller at least sees something.
	return hmtypes.StringValue(s), true
}

// hubMessageAck adapts the ReGa [rega.Runner] to the model's
// [hub.MessageAcknowledger] and [hub.BulkMessageAcknowledger] contracts.
// Single-message acknowledge drops the runner's confirmation boolean —
// a false with no error means the message was already gone, which the
// model treats as success. The bulk methods forward the acknowledged
// count unchanged.
type hubMessageAck struct {
	runner *rega.Runner
}

func (a hubMessageAck) AcknowledgeMessage(ctx context.Context, id string) error {
	_, err := a.runner.AcknowledgeMessage(ctx, id)
	return err
}

func (a hubMessageAck) AcknowledgeAllServiceMessages(ctx context.Context) (int, error) {
	return a.runner.AcknowledgeAllServiceMessages(ctx)
}

func (a hubMessageAck) AcknowledgeAllAlarmMessages(ctx context.Context) (int, error) {
	return a.runner.AcknowledgeAllAlarmMessages(ctx)
}

// hubJSONRPCWriter implements [hub.ProgramWriter] + [hub.SysvarWriter]
// against the CCU JSON-RPC endpoint. Program.execute and Rega-backed
// Sysvar writes mirror
type hubJSONRPCWriter struct {
	json *jsonrpc.Client
	rega *rega.Runner
}

// ExecuteProgram runs the given program synchronously.
func (w *hubJSONRPCWriter) ExecuteProgram(ctx context.Context, id string) error {
	return w.json.Call(ctx, "Program.execute", map[string]any{"id": id}, nil)
}

// ExecuteProgramConditional evaluates the program's "if" condition via a
// ReGa script and runs it only when the condition is satisfied. The CCU's
// JSON-RPC Program.execute runs unconditionally, so the condition-gated
// variant has to go through ReGa. Returns whether the program executed.
func (w *hubJSONRPCWriter) ExecuteProgramConditional(ctx context.Context, id string) (bool, error) {
	return w.rega.ExecuteProgramConditional(ctx, id)
}

// SetProgramEnabled toggles the active flag via a ReGa script. The
// CCU's JSON-RPC API does not expose Program.setActive cleanly, so the
// Rega route is the portable choice.
func (w *hubJSONRPCWriter) SetProgramEnabled(ctx context.Context, id string, enabled bool) error {
	state := "0"
	if enabled {
		state = "1"
	}
	_, err := w.rega.Run(ctx, hmenum.RegaScriptSetProgramState, map[string]string{
		"id":    id,
		"state": state,
	})
	return err
}

// DeleteProgram removes the program from the CCU via the delete_program
// ReGa script. The CCU exposes Program.deleteProgramByName (by name) over
// JSON-RPC but no delete-by-id, so the ID-keyed ReGa route is the portable
// choice — the same reason SetProgramEnabled goes through ReGa. A "0"
// script result (id no longer resolves to a program) maps to
// [hub.ErrProgramNotFound].
func (w *hubJSONRPCWriter) DeleteProgram(ctx context.Context, id string) error {
	out, err := w.rega.Run(ctx, hmenum.RegaScriptDeleteProgram, map[string]string{"id": id})
	if err != nil {
		return err
	}
	return parseMutateResult(out, hub.ErrProgramNotFound)
}

// SetSysvar writes the sysvar with per-type wire dispatch, mirroring
// the reference JSON-RPC client: bool → SysVar.setBool, numeric values
// (including enum/list indices) → SysVar.setFloat, and only strings go
// through the Rega script. The script's own guard writes string-typed
// variables ONLY and emits nothing when it declines (sysvar missing or
// not string-typed) — routing a non-string value through it silently
// drops the write, and a decline must surface as an error, not as a
// silent no-op.
func (w *hubJSONRPCWriter) SetSysvar(ctx context.Context, name string, value any) error {
	s, isString := value.(string)
	if !isString {
		return w.json.SetSystemVariable(ctx, name, value)
	}
	out, err := w.rega.Run(ctx, hmenum.RegaScriptSetSystemVariable, map[string]string{
		"name":  name,
		"value": s,
	})
	if err != nil {
		return err
	}
	// Success echoes the written value; clearing to "" legitimately
	// echoes empty and must not count as a decline.
	if out == "" && s != "" {
		return fmt.Errorf("sysvar %q: rega write declined (missing or not string-typed)", name)
	}
	return nil
}

// CreateSysvar provisions a new sysvar.
//
// BOOL/FLOAT/ENUM without a custom unit, description or value labels go
// through the CCU's native JSON-RPC methods (`SysVar.createBool` /
// `createFloat` / `createEnum`), which own the exact CCU surface
// (UTF-8/BOM, escape rules). INTEGER, STRING, ALARM and any sysvar that
// needs a `ValueUnit`, a description or custom binary value labels fall
// back to the `create_system_variable` Rega script because the CCU's
// JSON-RPC has no equivalent for those (createBool / createFloat /
// createEnum carry no description or value-name parameters, and there is
// no native createAlarm — the script backs an ALARM line with an
// OT_ALARMDP object so it stays acknowledgeable).
func (w *hubJSONRPCWriter) CreateSysvar(ctx context.Context, spec hub.SysvarCreateSpec) error {
	// A channel address binds the variable to a device channel; -1 leaves it
	// unassigned. The address is resolved to the channel's ReGa ise id (the
	// value SysVar.create* and oNew.Channel() expect) before it hits the CCU.
	chnID := -1
	if spec.Channel != "" {
		id, err := w.resolveChannelISEID(ctx, spec.Channel)
		if err != nil {
			return err
		}
		chnID = id
	}
	// Custom binary value labels only apply to BOOL/ALARM and force the
	// Rega path — the native createBool has no value-name parameter.
	customLabels := spec.ValueName0 != "" || spec.ValueName1 != ""
	if spec.Unit == "" && spec.Description == "" && !customLabels {
		switch spec.ValueType {
		case "BOOL":
			return w.json.Call(ctx, "SysVar.createBool", map[string]any{
				"name":     spec.Name,
				"init_val": 0,
				"internal": 0,
				"chn_id":   chnID,
			}, nil)
		case "FLOAT":
			params := map[string]any{
				"name":     spec.Name,
				"internal": 0,
				"chn_id":   chnID,
			}
			if spec.Min != "" {
				params["min_value"] = spec.Min
			}
			if spec.Max != "" {
				params["max_value"] = spec.Max
			}
			return w.json.Call(ctx, "SysVar.createFloat", params, nil)
		case "ENUM":
			return w.json.Call(ctx, "SysVar.createEnum", map[string]any{
				"name":       spec.Name,
				"value_list": strings.Join(spec.ValueList, ";"),
				"internal":   0,
				"chn_id":     chnID,
			}, nil)
		}
	}
	// The Rega script sets ValueName0/1 for BOOL and ALARM; supply the
	// CCU's own "false"/"true" defaults when the caller left a label empty
	// so the script never writes a blank label over the default.
	vn0, vn1 := spec.ValueName0, spec.ValueName1
	if spec.ValueType == "BOOL" || spec.ValueType == "ALARM" {
		if vn0 == "" {
			vn0 = "false"
		}
		if vn1 == "" {
			vn1 = "true"
		}
	}
	_, err := w.rega.Run(ctx, hmenum.RegaScriptCreateSystemVariable, map[string]string{
		"name":        spec.Name,
		"type":        spec.ValueType,
		"unit":        spec.Unit,
		"min":         spec.Min,
		"max":         spec.Max,
		"values":      strings.Join(spec.ValueList, ";"),
		"description": spec.Description,
		"valuename0":  vn0,
		"valuename1":  vn1,
		"channel":     strconv.Itoa(chnID),
	})
	return err
}

// resolveChannelISEID resolves a device or channel address to its ReGa ise
// id via the canonical Interface.getIseIDByAddress JSON-RPC method — the
// numeric id oSv.Channel() / SysVar.create* bind for the "Kanalzuordnung".
// A CCU-side fault or a non-positive id means the address does not name a
// channel on this CCU; both surface as [hub.ErrSysvarChannelUnknown] so the
// REST handler answers 422 (bad channel address) rather than 502. A genuine
// transport failure propagates unchanged.
func (w *hubJSONRPCWriter) resolveChannelISEID(ctx context.Context, address string) (int, error) {
	var raw any
	if err := w.json.Call(ctx, "Interface.getIseIDByAddress", map[string]any{"address": address}, &raw); err != nil {
		var rpcErr *hmerr.JSONRPCError
		if errors.As(err, &rpcErr) {
			return 0, fmt.Errorf("%w: %s", hub.ErrSysvarChannelUnknown, address)
		}
		return 0, err
	}
	id := 0
	switch v := raw.(type) {
	case float64:
		id = int(v)
	case string:
		id, _ = strconv.Atoi(strings.TrimSpace(v))
	}
	if id <= 0 {
		return 0, fmt.Errorf("%w: %s", hub.ErrSysvarChannelUnknown, address)
	}
	return id, nil
}

// DeleteSysvar removes a sysvar via the CCU's native JSON-RPC method
// `SysVar.deleteSysVarByName`. The Rega `delete_system_variable.fn`
// fallback was removed because it offered no behavior the JSON-RPC
// call doesn't already cover and added one extra encoding hop.
func (w *hubJSONRPCWriter) DeleteSysvar(ctx context.Context, name string) error {
	return w.json.Call(ctx, "SysVar.deleteSysVarByName", map[string]any{
		"name": name,
	}, nil)
}

// SysvarUsagePrograms lists the CCU programs that reference the named
// sysvar via the variable's native DPEnumUsagePrograms (usage_by_sysvar.fn).
// The ReGa-supplied program name is URL-encoded; it is decoded here.
func (w *hubJSONRPCWriter) SysvarUsagePrograms(ctx context.Context, name string) ([]hub.SysvarUsage, error) {
	raw, err := w.rega.SysvarUsagePrograms(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]hub.SysvarUsage, 0, len(raw))
	for _, p := range raw {
		out = append(out, hub.SysvarUsage{ID: p.ID, Name: decodeRegaField(p.Name), Active: p.Active})
	}
	return out, nil
}

// UpdateSysvar patches the sysvar's metadata (name, unit, bounds, value
// list, description, binary value labels, visibility and archive flags)
// without touching its type. Empty strings on the input map leave the
// corresponding CCU field untouched; a non-empty newname renames the
// variable in place. The visibility / archive flags are tri-state: an
// empty string leaves the flag as-is, "true"/"false" sets it. Type
// changes are unsafe at the CCU level — callers wanting that must delete
// + recreate.
func (w *hubJSONRPCWriter) UpdateSysvar(ctx context.Context, spec hub.SysvarUpdateSpec) error {
	// Channel assignment is tri-state: nil leaves it untouched (""), an empty
	// string clears it (ise id -1), an address resolves to the channel's ise
	// id. The update script's `if (sChannel != "")` guard skips the "" case.
	channelParam := ""
	if spec.Channel != nil {
		if *spec.Channel == "" {
			channelParam = "-1"
		} else {
			id, err := w.resolveChannelISEID(ctx, *spec.Channel)
			if err != nil {
				return err
			}
			channelParam = strconv.Itoa(id)
		}
	}
	_, err := w.rega.Run(ctx, hmenum.RegaScriptUpdateSystemVariable, map[string]string{
		"name":        spec.Name,
		"newname":     spec.NewName,
		"unit":        spec.Unit,
		"min":         spec.Min,
		"max":         spec.Max,
		"values":      strings.Join(spec.ValueList, ";"),
		"description": spec.Description,
		"valuename0":  spec.ValueName0,
		"valuename1":  spec.ValueName1,
		"visible":     boolFlagParam(spec.Visible),
		"logged":      boolFlagParam(spec.Logged),
		"channel":     channelParam,
	})
	return err
}

// boolFlagParam renders a tri-state flag for a Rega script parameter:
// "" when the pointer is nil (leave the CCU value untouched), otherwise
// "true"/"false".
func boolFlagParam(b *bool) string {
	if b == nil {
		return ""
	}
	if *b {
		return "true"
	}
	return "false"
}

// SetDeviceRooms replaces the device's room assignments. `rooms` is
// taken verbatim; the Rega script joins them with newlines so empty
// strings clear the assignment.
func (w *hubJSONRPCWriter) SetDeviceRooms(
	ctx context.Context, deviceAddress string, rooms []string,
) error {
	_, err := w.rega.Run(ctx, hmenum.RegaScriptSetDeviceRooms, map[string]string{
		"address": deviceAddress,
		"rooms":   strings.Join(rooms, "\n"),
	})
	return err
}

// SetDeviceFunctions replaces the device's function (Gewerk)
// assignments via the analogous Rega script.
func (w *hubJSONRPCWriter) SetDeviceFunctions(
	ctx context.Context, deviceAddress string, functions []string,
) error {
	_, err := w.rega.Run(ctx, hmenum.RegaScriptSetDeviceFunctions, map[string]string{
		"address":   deviceAddress,
		"functions": strings.Join(functions, "\n"),
	})
	return err
}

// CreateRoom creates a room entity on the CCU via the create_room Rega
// script. Returns the new object ID, or hub.ErrRoomExists when a room
// with that name already exists.
func (w *hubJSONRPCWriter) CreateRoom(ctx context.Context, name string) (int, error) {
	out, err := w.rega.Run(ctx, hmenum.RegaScriptCreateRoom, map[string]string{"name": name})
	if err != nil {
		return 0, err
	}
	return parseCreateResult(out, hub.ErrRoomExists)
}

// RenameRoom renames a room entity via the rename_room Rega script.
func (w *hubJSONRPCWriter) RenameRoom(ctx context.Context, oldName, newName string) error {
	out, err := w.rega.Run(ctx, hmenum.RegaScriptRenameRoom, map[string]string{
		"oldname": oldName,
		"newname": newName,
	})
	if err != nil {
		return err
	}
	return parseMutateResult(out, hub.ErrRoomNotFound)
}

// DeleteRoom deletes a room entity via the delete_room Rega script.
func (w *hubJSONRPCWriter) DeleteRoom(ctx context.Context, name string) error {
	out, err := w.rega.Run(ctx, hmenum.RegaScriptDeleteRoom, map[string]string{"name": name})
	if err != nil {
		return err
	}
	return parseMutateResult(out, hub.ErrRoomNotFound)
}

// CreateFunction creates a function (Gewerk) entity via create_function.
func (w *hubJSONRPCWriter) CreateFunction(ctx context.Context, name string) (int, error) {
	out, err := w.rega.Run(ctx, hmenum.RegaScriptCreateFunction, map[string]string{"name": name})
	if err != nil {
		return 0, err
	}
	return parseCreateResult(out, hub.ErrFunctionExists)
}

// RenameFunction renames a function entity via rename_function.
func (w *hubJSONRPCWriter) RenameFunction(ctx context.Context, oldName, newName string) error {
	out, err := w.rega.Run(ctx, hmenum.RegaScriptRenameFunction, map[string]string{
		"oldname": oldName,
		"newname": newName,
	})
	if err != nil {
		return err
	}
	return parseMutateResult(out, hub.ErrFunctionNotFound)
}

// DeleteFunction deletes a function entity via delete_function.
func (w *hubJSONRPCWriter) DeleteFunction(ctx context.Context, name string) error {
	out, err := w.rega.Run(ctx, hmenum.RegaScriptDeleteFunction, map[string]string{"name": name})
	if err != nil {
		return err
	}
	return parseMutateResult(out, hub.ErrFunctionNotFound)
}

// GetUserLevel reads a CCU user's UserLevel via the get_user_level Rega
// script, run on this writer's privileged service session. username must
// be pre-sanitised by the caller (it is interpolated into the script).
func (w *hubJSONRPCWriter) GetUserLevel(ctx context.Context, username string) (int, error) {
	out, err := w.rega.Run(ctx, hmenum.RegaScriptGetUserLevel, map[string]string{"username": username})
	if err != nil {
		return -1, err
	}
	n, perr := strconv.Atoi(strings.TrimSpace(out))
	if perr != nil {
		return -1, fmt.Errorf("rega: unexpected user-level output %q: %w", out, perr)
	}
	return n, nil
}

// parseCreateResult maps a create script's integer output to (id, err):
// >0 is the new object ID, -2 maps to existsErr, anything else is a
// generic failure.
func parseCreateResult(out string, existsErr error) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("rega: unexpected create output %q: %w", out, err)
	}
	switch {
	case n > 0:
		return n, nil
	case n == -2:
		return 0, existsErr
	default:
		return 0, errors.New("rega: create failed")
	}
}

// parseMutateResult maps a rename/delete script's integer output: 1 is
// success, 0 maps to notFoundErr.
func parseMutateResult(out string, notFoundErr error) error {
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return fmt.Errorf("rega: unexpected output %q: %w", out, err)
	}
	if n == 1 {
		return nil
	}
	return notFoundErr
}

// TriggerBackup kicks off the OpenCCU backup script. The .sbk file
// lands at /usr/local/tmp/last_backup.sbk; status is polled via
// `create_backup_status`.
func (w *hubJSONRPCWriter) TriggerBackup(ctx context.Context) error {
	_, err := w.rega.Run(ctx, hmenum.RegaScriptCreateBackupStart, map[string]string{})
	return err
}

// BackupStatus reports the current state of an in-flight backup.
func (w *hubJSONRPCWriter) BackupStatus(ctx context.Context) (string, error) {
	return w.rega.Run(ctx, hmenum.RegaScriptCreateBackupStatus, map[string]string{})
}

// AcceptDeviceInInbox flips the device's `ReadyConfig` flag so the
// CCU promotes it from the inbox into the running registry.
func (w *hubJSONRPCWriter) AcceptDeviceInInbox(
	ctx context.Context, deviceAddress string,
) error {
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := w.rega.RunJSON(ctx, hmenum.RegaScriptAcceptDeviceInInbox, map[string]string{
		"device_address": deviceAddress,
	}, &resp); err != nil {
		return err
	}
	if resp.Success {
		// Accepted now, or already accepted — the script reports both as success.
		return nil
	}
	// The script ran but reported a structured failure. "Device not found"
	// means the address is no longer in the CCU inbox (settled or removed) — a
	// stale entry, not an upstream fault — so surface the dedicated sentinel
	// that REST maps to 404 instead of 502.
	if strings.Contains(strings.ToLower(resp.Error), "not found") {
		return fmt.Errorf("%w: %s", interfaces.ErrInboxDeviceNotFound, deviceAddress)
	}
	detail := resp.Error
	if detail == "" {
		detail = "accept rejected"
	}
	return fmt.Errorf("rega.AcceptDeviceInInbox(%s): %s", deviceAddress, detail)
}

// TriggerFirmwareUpdate runs OpenCCU's `checkFirmwareUpdate.sh` with
// `-a -r` (apply + reboot). The CCU stages the update and reboots
// once it's downloaded.
func (w *hubJSONRPCWriter) TriggerFirmwareUpdate(ctx context.Context) error {
	_, err := w.rega.Run(ctx, hmenum.RegaScriptTriggerFirmwareUpdate, map[string]string{})
	return err
}

// --- SysvarCreator adapter wiring --------------------------------

// ErrSysvarCreatorNoPrimary is returned by [clientSysvarCreator] when
// no primary client or backend is available at call time.
var ErrSysvarCreatorNoPrimary = errors.New("sysvar_creator: no primary client available")

// clientSysvarCreator implements [coordinators.SysvarCreator] by resolving
// the primary InterfaceClient and its registered backend at call time, then
// delegating to the IC-level CreateSystemVariable* wrappers. This
// late-binding approach keeps hub wiring decoupled from interface wiring
// order: the primary client is determined at the first actual CreateSysvar*
// call, not at construction time.
type clientSysvarCreator struct {
	unit   *central.Unit
	writer *clientpkg.ValueWriter
}

// primaryBackend resolves the primary client entry and its registered
// backend. Returns (nil, nil, ErrSysvarCreatorNoPrimary) when the unit
// has no registered clients or the backend is not yet registered.
func (c *clientSysvarCreator) primaryBackend() (*clientpkg.InterfaceClient, backends.Operations, error) {
	return primaryBackendOf(c.unit, c.writer)
}

// primaryBackendOf resolves a central's primary InterfaceClient and its
// registered backend at call time. Returns [ErrSysvarCreatorNoPrimary] when
// the unit has no registered clients or the primary backend is not yet
// registered. Shared by the sysvar-creator and backup-and-download wiring so
// both bind late and stay independent of interface-wiring order.
func primaryBackendOf(unit *central.Unit, writer *clientpkg.ValueWriter) (*clientpkg.InterfaceClient, backends.Operations, error) {
	if unit == nil || unit.Clients == nil {
		return nil, nil, ErrSysvarCreatorNoPrimary
	}
	entries := unit.Clients.List()
	if len(entries) == 0 {
		return nil, nil, ErrSysvarCreatorNoPrimary
	}
	// entries are sorted by interface ID — take the first (= primary).
	entry := entries[0]
	if entry.Client == nil {
		return nil, nil, ErrSysvarCreatorNoPrimary
	}
	if writer == nil {
		return nil, nil, ErrSysvarCreatorNoPrimary
	}
	b, ok := writer.Backend(unit.Name(), entry.InterfaceID)
	if !ok {
		return nil, nil, fmt.Errorf("%w: backend not registered for %s/%s",
			ErrSysvarCreatorNoPrimary, unit.Name(), entry.InterfaceID)
	}
	return entry.Client, b, nil
}

// CreateSysvarBool implements [coordinators.SysvarCreator].
func (c *clientSysvarCreator) CreateSysvarBool(ctx context.Context, name string, initVal bool) (map[string]any, error) {
	ic, b, err := c.primaryBackend()
	if err != nil {
		return nil, err
	}
	return ic.CreateSystemVariableBool(ctx, b, name, initVal)
}

// CreateSysvarEnum implements [coordinators.SysvarCreator].
func (c *clientSysvarCreator) CreateSysvarEnum(ctx context.Context, name string, valueList []string) (map[string]any, error) {
	ic, b, err := c.primaryBackend()
	if err != nil {
		return nil, err
	}
	return ic.CreateSystemVariableEnum(ctx, b, name, valueList)
}

// CreateSysvarFloat implements [coordinators.SysvarCreator].
func (c *clientSysvarCreator) CreateSysvarFloat(ctx context.Context, name string, minValue, maxValue float64) (map[string]any, error) {
	ic, b, err := c.primaryBackend()
	if err != nil {
		return nil, err
	}
	return ic.CreateSystemVariableFloat(ctx, b, name, minValue, maxValue)
}

// WireSysvarCreator installs a SysvarCreator on the hub coordinator of
// unit that delegates CreateSysvar* calls to the primary
// InterfaceClient's backend via the IC orchestration layer.
//
// Call this after [WireCentrals] has registered all interface clients
// so the primary client is available when the first CreateSysvar* call
// arrives. Nil arguments are safe — SetSysvarCreator is skipped when
// unit or unit.Hub is nil.
func WireSysvarCreator(unit *central.Unit, writer *clientpkg.ValueWriter) {
	if unit == nil || unit.Hub == nil {
		return
	}
	unit.Hub.SetSysvarCreator(&clientSysvarCreator{
		unit:   unit,
		writer: writer,
	})
}

// WireBackupAndDownload installs the create-and-download backup handler on
// unit. It resolves the primary InterfaceClient + backend at call time and
// runs the full reference flow (start → poll status → download the archive),
// returning the .sbk bytes. The REST BackupAdapter persists those bytes to
// local storage so the backup appears in the list and is downloadable —
// unlike the trigger-only path, which left the archive stranded on the CCU.
//
// Call this after [WireCentrals] has registered all interface clients, for
// the same late-binding reason as [WireSysvarCreator]. Passing 0 for both
// poll parameters selects the backend defaults (300 s wait, 5 s interval).
func WireBackupAndDownload(unit *central.Unit, writer *clientpkg.ValueWriter) {
	if unit == nil {
		return
	}
	unit.SetCreateBackupFn(func(ctx context.Context) ([]byte, error) {
		ic, b, err := primaryBackendOf(unit, writer)
		if err != nil {
			return nil, err
		}
		return ic.CreateBackupAndDownload(ctx, b, 0, 0)
	})
}

// ErrServiceMessageSuppressorNoClient is returned when the service-message
// suppressor cannot resolve an interface client or its backend for the
// requested interface — either no client is registered yet or the
// backend has not been wired.
var ErrServiceMessageSuppressorNoClient = errors.New("service_message_suppressor: no interface client available")

// clientServiceMessageSuppressor routes CCU service-message suppression
// through the per-interface [client.InterfaceClient] backend. It resolves
// the interface client + backend for the target interface at call time so
// it stays decoupled from interface-wiring order (the same late-binding
// approach as [clientSysvarCreator]).
//
// It satisfies three interfaces at once — [coordinators.ServiceMessageSuppressor],
// [coordinators.ServiceMessageReader], and [hub.ServiceMessageSuppressor] —
// so a single instance backs both the hub coordinator seam and the
// [hub.ServiceMessages] aggregate's [hub.ServiceMessages.Disable] path.
type clientServiceMessageSuppressor struct {
	unit   *central.Unit
	writer *clientpkg.ValueWriter
}

// resolveInterface returns interfaceID unchanged when non-empty; otherwise
// it looks the interface up from channelAddress via the model registry.
func (c *clientServiceMessageSuppressor) resolveInterface(interfaceID, channelAddress string) string {
	if interfaceID != "" {
		return interfaceID
	}
	return interfaceForChannel(c.unit, channelAddress)
}

// backendFor resolves the interface client and its registered backend for
// interfaceID (the bare CCU interface name, e.g. "HmIP-RF"). The client
// registry is keyed by the composite wire id, so the lookup translates the
// bare name through [WireInterfaceID].
func (c *clientServiceMessageSuppressor) backendFor(interfaceID string) (*clientpkg.InterfaceClient, backends.Operations, error) {
	if c.unit == nil || c.unit.Clients == nil || c.writer == nil || interfaceID == "" {
		return nil, nil, ErrServiceMessageSuppressorNoClient
	}
	wireID := WireInterfaceID(c.unit.Name(), hmenum.Interface(interfaceID))
	entry, ok := c.unit.Clients.Get(wireID)
	if !ok || entry == nil || entry.Client == nil {
		return nil, nil, fmt.Errorf("%w: interface %q", ErrServiceMessageSuppressorNoClient, interfaceID)
	}
	b, ok := c.writer.Backend(c.unit.Name(), wireID)
	if !ok {
		return nil, nil, fmt.Errorf("%w: backend not registered for %s/%s",
			ErrServiceMessageSuppressorNoClient, c.unit.Name(), wireID)
	}
	return entry.Client, b, nil
}

// SuppressServiceMessage implements [coordinators.ServiceMessageSuppressor]
// and [hub.ServiceMessageSuppressor]. The backend's own interface type is
// sent as the JSON-RPC `interface` parameter, so it must match interfaceID
// — which it does because the backend is looked up by that interface.
func (c *clientServiceMessageSuppressor) SuppressServiceMessage(ctx context.Context, interfaceID, channelAddress, parameterID string, suppress bool) error {
	iface := c.resolveInterface(interfaceID, channelAddress)
	ic, b, err := c.backendFor(iface)
	if err != nil {
		return err
	}
	_, err = ic.SuppressServiceMessage(ctx, b, channelAddress, parameterID, suppress)
	return err
}

// GetSuppressedServiceMessages implements [coordinators.ServiceMessageReader]
// and [hub.ServiceMessageSuppressor]. Returns the CCU's current suppressed
// service-parameter list for channelAddress on the resolved interface.
func (c *clientServiceMessageSuppressor) GetSuppressedServiceMessages(ctx context.Context, interfaceID, channelAddress string) ([]string, error) {
	iface := c.resolveInterface(interfaceID, channelAddress)
	ic, b, err := c.backendFor(iface)
	if err != nil {
		return nil, err
	}
	return ic.GetSuppressedServiceMessages(ctx, b, iface, channelAddress)
}

// WireServiceMessageSuppressor installs the durable service-message
// suppressor on unit. The same instance backs the hub-coordinator seam
// ([coordinators.HubCoordinator.SetServiceMessageSuppressor] /
// [coordinators.HubCoordinator.SetServiceMessageReader]) and the
// [hub.ServiceMessages] aggregate's [hub.ServiceMessages.Disable] /
// [hub.ServiceMessages.Unsuppress] path, so REST / WS calls actually reach
// the CCU's Interface.suppressServiceMessages instead of being no-ops.
//
// Call this after [WireCentrals] has registered all interface clients so
// the target interface's client is available when the first suppress call
// arrives — the same late-binding reason as [WireSysvarCreator]. Nil
// arguments are safe.
func WireServiceMessageSuppressor(unit *central.Unit, writer *clientpkg.ValueWriter) {
	if unit == nil {
		return
	}
	s := &clientServiceMessageSuppressor{unit: unit, writer: writer}
	if unit.Hub != nil {
		unit.Hub.SetServiceMessageSuppressor(s)
		unit.Hub.SetServiceMessageReader(s)
	}
	if unit.HubModel != nil && unit.HubModel.ServiceMessages != nil {
		unit.HubModel.ServiceMessages.SetSuppressor(s)
	}
}
