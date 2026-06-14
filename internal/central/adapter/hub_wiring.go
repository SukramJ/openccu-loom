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

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/observer"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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
func WireHub( //nolint:funlen // composition/wiring: long sequential setup
	ctx context.Context,
	cc config.CentralConfig,
	unit *central.Unit,
	logger *slog.Logger,
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
	// The serial is the central-id slot of every canonical HA routing
	// key for hub / internal / virtual-remote data points — clients read
	// it off `GET /system/ccu` (SystemCCUEntry.Serial); an empty serial
	// makes them emit broken `loom__…` unique_ids. Failures are logged
	// but non-fatal: an incomplete identity block is preferable to a
	// dead daemon.
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
	if serial, serialErr := runner.GetSerial(ctx); serialErr != nil {
		logger.Warn("hub.serial.fetch_failed", slog.String("err", serialErr.Error()))
	} else if serial != "" && serial != "unknown" {
		si.Serial = serial
	}
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
				return loadPrograms(ctx, jc, runner, unit.HubModel, writer, scanOpts)
			},
			Sysvars: func(ctx context.Context) error {
				return loadSysvars(ctx, jc, runner, unit.HubModel, writer, scanOpts)
			},
			Inbox: func(ctx context.Context) error {
				return loadInbox(ctx, runner, unit)
			},
			ServiceMessages: func(ctx context.Context) error {
				return loadServiceMessages(ctx, runner, unit)
			},
			AlarmMessages: func(ctx context.Context) error {
				return loadAlarmMessages(ctx, runner, unit.HubModel)
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

// programDescriptions fetches per-program CCU descriptions via ReGa for
// marker filtering. Program.getAll omits descriptions, so the call is
// only made when program markers are configured (it costs an extra
// ReGa round-trip); otherwise an empty map is returned and descriptions
// stay blank.
func programDescriptions(ctx context.Context, runner *rega.Runner, markers []hmenum.DescriptionMarker) map[string]string {
	out := make(map[string]string)
	if runner == nil || len(markers) == 0 {
		return out
	}
	descs, err := runner.GetProgramDescriptions(ctx)
	if err != nil {
		return out
	}
	for _, d := range descs {
		if decoded, derr := url.QueryUnescape(d.Description); derr == nil {
			out[d.ID] = decoded
		} else {
			out[d.ID] = d.Description
		}
	}
	return out
}

func loadPrograms(ctx context.Context, jc *jsonrpc.Client, runner *rega.Runner, h *hub.Hub, writer hub.ProgramWriter, opts hubScanOptions) error {
	if !opts.enableProgramScan {
		return nil
	}
	var programs []programEntry
	if err := jc.Call(ctx, "Program.getAll", nil, &programs); err != nil {
		return err
	}
	descByID := programDescriptions(ctx, runner, opts.programMarkers)
	// Collect the fresh ID set for the stale-entry diff below.
	freshIDs := make(map[string]struct{}, len(programs))
	for _, p := range programs {
		if p.ID == "" {
			continue
		}
		if p.IsInternal && !opts.includeInternalPrograms {
			continue
		}
		desc := descByID[p.ID]
		if !markerMatch(desc, opts.programMarkers) {
			continue
		}
		freshIDs[p.ID] = struct{}{}
		if existing, ok := h.Program(p.ID); ok {
			// Update the existing pointer in-place so subscribers wired via
			// OnUpdate (e.g. MQTT publisher) remain valid across periodic
			// refreshes. Only create a new Program when the ID is genuinely new.
			existing.UpdateMetadata(p.Name, p.IsInternal, writer)
			existing.EnabledDefault = hubEnabledDefault(p.IsInternal, desc, opts.programMarkers)
			existing.OnActive(p.IsActive)
		} else {
			prog := hub.NewProgram(h.CentralName, p.ID, p.Name, desc, p.IsInternal, writer)
			prog.EnabledDefault = hubEnabledDefault(p.IsInternal, desc, opts.programMarkers)
			prog.OnActive(p.IsActive)
			h.PutProgram(prog)
		}
	}
	// Remove programs that are no longer present on the CCU or no longer
	// pass the internal / marker filters.
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
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Value       json.RawMessage `json:"value"`
	Unit        string          `json:"unit"`
	ValueList   string          `json:"valueList"`
	MaxValue    json.RawMessage `json:"maxValue"`
	MinValue    json.RawMessage `json:"minValue"`
	IsInternal  bool            `json:"isInternal"`
	Description string          `json:"description"`
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
	descByID := make(map[string]string)
	if runner != nil {
		if descs, err := runner.GetSystemVariableDescriptions(ctx); err == nil {
			for _, d := range descs {
				if decoded, derr := url.QueryUnescape(d.Description); derr == nil {
					descByID[d.ID] = decoded
				} else {
					descByID[d.ID] = d.Description
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
		desc, isExtended := parseSysvarDescription(rawDesc)
		if existing, ok := h.Sysvar(v.Name); ok {
			// Update the existing pointer in-place so subscribers wired via
			// OnUpdate (e.g. MQTT publisher) remain valid across periodic
			// refreshes. Only allocate a new Sysvar when the name is new.
			existing.Unit = v.Unit
			existing.ValueType = valueType
			existing.ValueList = valueList
			existing.Writer = writer
			existing.IsExtended = isExtended
			existing.IsInternal = v.IsInternal
			existing.Description = desc
			existing.EnabledDefault = hubEnabledDefault(v.IsInternal, rawDesc, opts.sysvarMarkers)
			if vid, err := strconv.Atoi(v.ID); err == nil {
				existing.Vid = vid
			}
			if pv, ok := parseSysvarValue(valueType, v.Value); ok {
				existing.OnValue(pv)
			}
		} else {
			sv := hub.NewSysvar(h.CentralName, v.Name, desc, valueType, writer)
			sv.Unit = v.Unit
			sv.ValueList = valueList
			sv.IsExtended = isExtended
			sv.IsInternal = v.IsInternal
			sv.EnabledDefault = hubEnabledDefault(v.IsInternal, rawDesc, opts.sysvarMarkers)
			if vid, err := strconv.Atoi(v.ID); err == nil {
				sv.Vid = vid
			}
			if pv, ok := parseSysvarValue(valueType, v.Value); ok {
				sv.OnValue(pv)
			}
			h.PutSysvar(sv)
		}
	}
	// Remove sysvars that are no longer present on the CCU.
	for _, existing := range h.Sysvars() {
		if _, ok := freshNames[existing.Name]; !ok {
			h.RemoveSysvar(existing.Name)
		}
	}
	return nil
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

// loadServiceMessages fetches active service messages via the ReGa script
// engine (get_service_messages) and refreshes unit.HubModel.ServiceMessages.
func loadServiceMessages(ctx context.Context, r *rega.Runner, unit *central.Unit) error {
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
		all = append(all, hub.ServiceMessage{
			ID:         id,
			Name:       decodeRegaField(m.Name),
			Address:    m.Address,
			DeviceName: decodeRegaField(m.DeviceName),
			Type:       hmenum.ServiceMessageType(m.Type),
			Counter:    m.Counter,
		})
	}
	unit.HubModel.ServiceMessages.Replace(all)
	return nil
}

// loadAlarmMessages fetches all active alarm messages via the ReGa script
// engine (get_alarm_messages) and refreshes h.Messages. The query is
// central-wide; no per-interface iteration is needed.
func loadAlarmMessages(ctx context.Context, r *rega.Runner, h *hub.Hub) error {
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
		})
	}
	h.Messages.Replace(msgs)
	return nil
}

// decodeRegaField URL-decodes a field emitted by the get_service_messages,
// get_alarm_messages and get_inbox_devices ReGa scripts, which percent-encode
// human-readable strings (names, device names, descriptions). On a decode
// error the raw value is returned unchanged so a single malformed field never
// drops the whole message.
func decodeRegaField(s string) string {
	if s == "" {
		return ""
	}
	if dec, err := url.QueryUnescape(s); err == nil {
		return dec
	}
	return s
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
// the declared value type.
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
	case hmenum.HubValueTypeNumber, hmenum.HubValueTypeFloat, hmenum.HubValueTypeInteger, hmenum.HubValueTypeList:
		if pv, err := hmtypes.NewParamValue(s); err == nil {
			return pv, true
		}
	case hmenum.HubValueTypeString:
		return hmtypes.StringValue(s), true
	}
	// Fallback: preserve as string so the caller at least sees something.
	return hmtypes.StringValue(s), true
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

// SetSysvar writes the sysvar via the Rega script. CCU JSON-RPC
// SysVar.set has inconsistent type coercion across firmwares; the
// Rega path matches the CCU WebUI.
func (w *hubJSONRPCWriter) SetSysvar(ctx context.Context, name string, value any) error {
	_, err := w.rega.Run(ctx, hmenum.RegaScriptSetSystemVariable, map[string]string{
		"name":  name,
		"value": fmt.Sprint(value),
	})
	return err
}

// CreateSysvar provisions a new sysvar.
//
// BOOL/FLOAT/ENUM without a custom unit go through the CCU's native
// JSON-RPC methods (`SysVar.createBool` / `createFloat` / `createEnum`)
// This matches what
// surface (UTF-8/BOM, escape rules). INTEGER, STRING and any sysvar
// that needs a `ValueUnit` fall back to the `create_system_variable`
// Rega script because the CCU's JSON-RPC has no equivalent for those.
func (w *hubJSONRPCWriter) CreateSysvar(
	ctx context.Context,
	name, valueType, unit, vmin, vmax string,
	valueList []string,
) error {
	if unit == "" {
		switch valueType {
		case "BOOL":
			return w.json.Call(ctx, "SysVar.createBool", map[string]any{
				"name":     name,
				"init_val": 0,
				"internal": 0,
				"chn_id":   -1,
			}, nil)
		case "FLOAT":
			params := map[string]any{
				"name":     name,
				"internal": 0,
				"chn_id":   -1,
			}
			if vmin != "" {
				params["min_value"] = vmin
			}
			if vmax != "" {
				params["max_value"] = vmax
			}
			return w.json.Call(ctx, "SysVar.createFloat", params, nil)
		case "ENUM":
			return w.json.Call(ctx, "SysVar.createEnum", map[string]any{
				"name":       name,
				"value_list": strings.Join(valueList, ";"),
				"internal":   0,
				"chn_id":     -1,
			}, nil)
		}
	}
	_, err := w.rega.Run(ctx, hmenum.RegaScriptCreateSystemVariable, map[string]string{
		"name":   name,
		"type":   valueType,
		"unit":   unit,
		"min":    vmin,
		"max":    vmax,
		"values": strings.Join(valueList, ";"),
	})
	return err
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

// UpdateSysvar patches the sysvar's metadata (unit, bounds, value
// list, description) without touching its type. Empty strings on
// the input map leave the corresponding CCU field untouched.
func (w *hubJSONRPCWriter) UpdateSysvar(
	ctx context.Context,
	name, unit, vmin, vmax, description string,
	valueList []string,
) error {
	_, err := w.rega.Run(ctx, hmenum.RegaScriptUpdateSystemVariable, map[string]string{
		"name":        name,
		"unit":        unit,
		"min":         vmin,
		"max":         vmax,
		"values":      strings.Join(valueList, ";"),
		"description": description,
	})
	return err
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
	_, err := w.rega.Run(ctx, hmenum.RegaScriptAcceptDeviceInInbox, map[string]string{
		"device_address": deviceAddress,
	})
	return err
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
