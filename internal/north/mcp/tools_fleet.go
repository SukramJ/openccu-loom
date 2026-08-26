// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// getMeasurementsDefaultBuckets/getMeasurementsMaxBuckets mirror the
// REST GET /history handler's historyDefaultBuckets/historyMaxBuckets
// (internal/north/rest/handlers/history.go), which are unexported —
// get_measurements applies the same clamp on its own copy.
const (
	getMeasurementsDefaultBuckets = 200
	getMeasurementsMaxBuckets     = 2000
)

// parseRequiredRFC3339 parses an RFC3339 timestamp for a required field,
// mirroring the REST history/energy handlers' own required-time parsing
// (internal/north/rest/handlers/history.go parseRequiredTime), which is
// unexported.
func parseRequiredRFC3339(value, field string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: invalid RFC3339 timestamp %q", field, value)
	}
	return t, nil
}

// This file holds the eight read tools resolving the MCP/REST parity
// backlog declared in tests/contract/mcp_rest_parity_test.go
// (restDomainsAwaitingMCPTools): groups, areas, interfaces, history,
// visibility, energy, links, schedules. Each follows the same shape as
// the hub-derived tools in tools_hub.go — one registerX function, a
// typed ...Out struct, and a seam that leaves the tool unregistered
// when nil rather than answering "unavailable" for a wired absence.

// --- groups -------------------------------------------------------

type listGroupsIn struct {
	centralScopeIn
}

type listGroupsOut struct {
	Centrals []handlers.GroupCentralEntry `json:"centrals"`
}

func registerListGroups(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_groups",
		Description: "List CCU heating groups (roster and members), one entry per central, optionally scoped to one central via central_name.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listGroupsIn) (*mcpsdk.CallToolResult, listGroupsOut, error) {
		want := strings.TrimSpace(in.CentralName)
		if want != "" && !centralKnown(d, want) {
			return nil, listGroupsOut{}, errUnknownCentral(d, want)
		}
		entries, err := d.Groups.List(ctx, want)
		if err != nil {
			return nil, listGroupsOut{}, fmt.Errorf("list groups: %w", err)
		}
		if entries == nil {
			entries = []handlers.GroupCentralEntry{}
		}
		return nil, listGroupsOut{Centrals: entries}, nil
	})
}

// --- areas ----------------------------------------------------------

type listAreasIn struct {
	centralScopeIn
}

type listAreasOut struct {
	Areas []hmapi.Area `json:"areas"`
}

func registerListAreas(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_areas",
		Description: "List operator-defined areas (room groupings one level above the CCU's flat room list) with their assigned rooms, optionally scoped to one central via central_name.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listAreasIn) (*mcpsdk.CallToolResult, listAreasOut, error) {
		want := strings.TrimSpace(in.CentralName)
		if want != "" && !centralKnown(d, want) {
			return nil, listAreasOut{}, errUnknownCentral(d, want)
		}
		rows, err := d.Areas.GetAll(ctx)
		if err != nil {
			return nil, listAreasOut{}, fmt.Errorf("list areas: %w", err)
		}
		assignments, err := d.Areas.ListAssignments(ctx)
		if err != nil {
			return nil, listAreasOut{}, fmt.Errorf("list area rooms: %w", err)
		}
		roomsByArea := make(map[string][]hmapi.AreaRoomRef, len(rows))
		for _, a := range assignments {
			if want != "" && a.CentralName != want {
				continue
			}
			roomsByArea[a.AreaID] = append(roomsByArea[a.AreaID], hmapi.AreaRoomRef{Central: a.CentralName, Room: a.RoomName})
		}
		out := listAreasOut{Areas: make([]hmapi.Area, 0, len(rows))}
		for _, row := range rows {
			out.Areas = append(out.Areas, hmapi.Area{
				ID:       row.ID,
				Name:     row.Name,
				Position: row.Position,
				Rooms:    roomsByArea[row.ID],
			})
		}
		return nil, out, nil
	})
}

// --- interfaces -------------------------------------------------------

type listInterfacesIn struct{}

type listInterfacesOut struct {
	Interfaces []hmapi.InterfaceState `json:"interfaces"`
}

// registerListInterfaces implements `list_interfaces`, a read-only
// projection of the configured CCU interfaces (connectivity, duty
// cycle, carrier sense). Reconnect is deliberately not projected: it
// actuates the radio link, the same argument that keeps install-mode
// off the assistant surface.
func registerListInterfaces(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_interfaces",
		Description: "List the configured CCU interfaces with their connectivity state (connected, duty cycle, carrier sense). Read-only: reconnecting an interface actuates the radio link and is not exposed here.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ listInterfacesIn) (*mcpsdk.CallToolResult, listInterfacesOut, error) {
		ifaces := d.Interfaces.Interfaces()
		if ifaces == nil {
			ifaces = []hmapi.InterfaceState{}
		}
		return nil, listInterfacesOut{Interfaces: ifaces}, nil
	})
}

// --- history / measurements -------------------------------------------

type getMeasurementsIn struct {
	Central        string `json:"central" jsonschema:"the CCU that owns the data point"`
	InterfaceID    string `json:"interface_id" jsonschema:"the CCU interface id the data point is reported on"`
	ChannelAddress string `json:"channel" jsonschema:"the channel address, e.g. 0001D3C99C1234:1"`
	Parameter      string `json:"parameter" jsonschema:"the parameter name, e.g. TEMPERATURE"`
	From           string `json:"from" jsonschema:"RFC3339 start of the time window (required)"`
	To             string `json:"to" jsonschema:"RFC3339 end of the time window (required)"`
	Buckets        int    `json:"buckets,omitempty" jsonschema:"maximum number of evenly spaced buckets to aggregate into (default 200, max 2000)"`
}

type getMeasurementsOut struct {
	Buckets []handlers.HistoryBucket `json:"buckets"`
	// Tier names the source resolution the answer was assembled from
	// ("raw", "hour", "day"), mirroring the REST X-History-Tier header.
	Tier string `json:"tier,omitempty"`
}

func registerGetMeasurements(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "get_measurements",
		Description: "Read a data point's recorded measurement history, server-bucketed into evenly spaced points over a required time window " +
			"(central, interface_id, channel, parameter, from, to are all required; there is no default window).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getMeasurementsIn) (*mcpsdk.CallToolResult, getMeasurementsOut, error) {
		central := strings.TrimSpace(in.Central)
		interfaceID := strings.TrimSpace(in.InterfaceID)
		channel := strings.TrimSpace(in.ChannelAddress)
		parameter := strings.TrimSpace(in.Parameter)
		if central == "" || interfaceID == "" || channel == "" || parameter == "" {
			return nil, getMeasurementsOut{}, errors.New("central, interface_id, channel and parameter are required")
		}
		from, err := parseRequiredRFC3339(in.From, "from")
		if err != nil {
			return nil, getMeasurementsOut{}, err
		}
		to, err := parseRequiredRFC3339(in.To, "to")
		if err != nil {
			return nil, getMeasurementsOut{}, err
		}
		if !to.After(from) {
			return nil, getMeasurementsOut{}, errors.New("to must be after from")
		}
		buckets := in.Buckets
		if buckets <= 0 {
			buckets = getMeasurementsDefaultBuckets
		}
		if buckets > getMeasurementsMaxBuckets {
			buckets = getMeasurementsMaxBuckets
		}
		q := handlers.HistoryQuery{
			Central:        central,
			InterfaceID:    interfaceID,
			ChannelAddress: channel,
			Parameter:      parameter,
			From:           from,
			To:             to,
			Buckets:        buckets,
		}
		rows, tier, err := d.History.Query(ctx, q)
		if err != nil {
			return nil, getMeasurementsOut{}, fmt.Errorf("query history: %w", err)
		}
		if rows == nil {
			rows = []handlers.HistoryBucket{}
		}
		return nil, getMeasurementsOut{Buckets: rows, Tier: tier}, nil
	})
}

// --- visibility / hidden parameters ------------------------------------

type listHiddenParametersIn struct {
	centralScopeIn
}

type hiddenParameterEntry struct {
	Pattern   string `json:"pattern"`
	Central   string `json:"central"`
	UpdatedAt string `json:"updated_at,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

type listHiddenParametersOut struct {
	Patterns []hiddenParameterEntry `json:"patterns"`
}

func registerListHiddenParameters(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_hidden_parameters",
		Description: "List the un-ignore patterns that promote otherwise-hidden parameters into the visible data-point surface, optionally scoped to one central via central_name.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listHiddenParametersIn) (*mcpsdk.CallToolResult, listHiddenParametersOut, error) {
		scan, err := centralsToScan(d, in.CentralName)
		if err != nil {
			return nil, listHiddenParametersOut{}, err
		}
		out := listHiddenParametersOut{Patterns: []hiddenParameterEntry{}}
		for _, c := range scan {
			entries, err := d.Visibility.List(ctx, c)
			if err != nil {
				return nil, listHiddenParametersOut{}, fmt.Errorf("list hidden parameters for %q: %w", c, err)
			}
			for _, e := range entries {
				out.Patterns = append(out.Patterns, hiddenParameterEntry{
					Pattern:   e.Pattern,
					Central:   c,
					UpdatedAt: rfc3339OrEmpty(e.UpdatedAt),
					UpdatedBy: e.UpdatedBy,
				})
			}
		}
		return nil, out, nil
	})
}

// --- energy -------------------------------------------------------------

type getEnergyIn struct {
	Central string `json:"central" jsonschema:"the CCU to aggregate energy readings for (required)"`
	Device  string `json:"device,omitempty" jsonschema:"optional device address; omit for every energy device on the central"`
	From    string `json:"from" jsonschema:"RFC3339 start of the time window (required)"`
	To      string `json:"to" jsonschema:"RFC3339 end of the time window (required)"`
	Group   string `json:"group,omitempty" jsonschema:"bucket size: hour, day, or month (default day)"`
}

// getEnergyOut is an alias for the canonical REST response DTO — its
// JSON shape is already sized for a caller (per-device buckets plus
// totals), so get_energy returns it unchanged.
type getEnergyOut = handlers.EnergyResponse

func registerGetEnergy(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_energy",
		Description: "Report per-device power/energy aggregation over a required time window (central, from, to are required; group defaults to day; device scopes to one device, omitted lists every energy device on the central).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getEnergyIn) (*mcpsdk.CallToolResult, getEnergyOut, error) {
		central := strings.TrimSpace(in.Central)
		if central == "" {
			return nil, getEnergyOut{}, errors.New("central is required")
		}
		from, err := parseRequiredRFC3339(in.From, "from")
		if err != nil {
			return nil, getEnergyOut{}, err
		}
		to, err := parseRequiredRFC3339(in.To, "to")
		if err != nil {
			return nil, getEnergyOut{}, err
		}
		if !to.After(from) {
			return nil, getEnergyOut{}, errors.New("to must be after from")
		}
		group := strings.TrimSpace(in.Group)
		switch group {
		case "":
			group = "day"
		case "hour", "day", "month":
		default:
			return nil, getEnergyOut{}, fmt.Errorf("group must be one of hour, day, month, got %q", in.Group)
		}
		q := handlers.EnergyQuery{
			Central: central,
			Device:  strings.TrimSpace(in.Device),
			From:    from,
			To:      to,
			Group:   group,
		}
		resp, err := d.Energy.Energy(ctx, q)
		if err != nil {
			return nil, getEnergyOut{}, fmt.Errorf("query energy: %w", err)
		}
		if resp.Devices == nil {
			resp.Devices = []handlers.EnergyDevice{}
		}
		return nil, resp, nil
	})
}

// --- links ---------------------------------------------------------------

// linksDefaultLocale mirrors the REST GET /links default when no
// `?locale=` query parameter is given.
const linksDefaultLocale = "en"

type listLinksIn struct {
	centralScopeIn
}

type listLinksOut struct {
	Links []hmapi.Link `json:"links"`
}

func registerListLinks(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_links",
		Description: "List direct device-to-device links across every configured central, optionally scoped to one central via central_name.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listLinksIn) (*mcpsdk.CallToolResult, listLinksOut, error) {
		want := strings.TrimSpace(in.CentralName)
		if want != "" && !centralKnown(d, want) {
			return nil, listLinksOut{}, errUnknownCentral(d, want)
		}
		links, err := d.Links.ListAllLinks(ctx, want, linksDefaultLocale)
		if err != nil {
			return nil, listLinksOut{}, fmt.Errorf("list links: %w", err)
		}
		if links == nil {
			links = []hmapi.Link{}
		}
		return nil, listLinksOut{Links: links}, nil
	})
}

// --- schedules -------------------------------------------------------------

type listSchedulesIn struct{}

type listSchedulesOut struct {
	Schedules []hmapi.ScheduleDeviceSummary `json:"schedules"`
}

func registerListSchedules(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_schedules",
		Description: "List every device across the fleet that carries a week schedule, with its schedule kind (week_profile or climate).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ listSchedulesIn) (*mcpsdk.CallToolResult, listSchedulesOut, error) {
		items, err := d.Schedules.ListScheduleDevices(ctx)
		if err != nil {
			return nil, listSchedulesOut{}, fmt.Errorf("list schedules: %w", err)
		}
		if items == nil {
			items = []hmapi.ScheduleDeviceSummary{}
		}
		return nil, listSchedulesOut{Schedules: items}, nil
	})
}
