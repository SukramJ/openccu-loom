// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"context"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

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

// --- taxonomy -------------------------------------------------------

// The taxonomy is flattened: every node is listed with its path and its
// parent's path, so the tool's schema has no recursive type (the MCP SDK
// cannot describe one) and a reader still rebuilds the tree.

type taxonomyNodeOut struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	ParentPath string `json:"parent_path,omitempty"`
	Icon       string `json:"icon,omitempty"`
}

type taxonomyEnumOut struct {
	ID    string            `json:"id"`
	Names map[string]string `json:"names,omitempty"`
	Nodes []taxonomyNodeOut `json:"nodes"`
}

type taxonomyCentralOut struct {
	Central  string            `json:"central"`
	Revision uint64            `json:"revision"`
	Writable bool              `json:"writable"`
	Tree     bool              `json:"tree"`
	Enums    []taxonomyEnumOut `json:"enums"`
}

type getTaxonomyOut struct {
	Centrals []taxonomyCentralOut `json:"centrals"`
}

func flattenTaxonomyNodes(in []handlers.TaxonomyNode, parent string, out []taxonomyNodeOut) []taxonomyNodeOut {
	for _, n := range in {
		out = append(out, taxonomyNodeOut{Path: n.Path, Name: n.Name, ParentPath: parent, Icon: n.Icon})
		out = flattenTaxonomyNodes(n.Children, n.Path, out)
	}
	return out
}

func registerGetTaxonomy(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "get_taxonomy",
		Description: "Get each central's taxonomy — rooms, functions and any further enums — listing every node with its path " +
			"and its parent's path, including nodes nothing is assigned to. A node's path (e.g. eg/kueche) tells two rooms " +
			"of one name apart. Optionally scoped to one central via central_name.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in centralScopeIn) (*mcpsdk.CallToolResult, getTaxonomyOut, error) {
		want := strings.TrimSpace(in.CentralName)
		if want != "" && !centralKnown(d, want) {
			return nil, getTaxonomyOut{}, errUnknownCentral(d, want)
		}
		resp, ok := handlers.BuildTaxonomyResponse(d.Taxonomy, want)
		if !ok {
			return nil, getTaxonomyOut{}, errUnknownCentral(d, want)
		}
		out := getTaxonomyOut{Centrals: make([]taxonomyCentralOut, 0, len(resp.Centrals))}
		for _, c := range resp.Centrals {
			co := taxonomyCentralOut{
				Central: c.Central, Revision: c.Revision, Writable: c.Writable, Tree: c.Tree,
				Enums: make([]taxonomyEnumOut, 0, len(c.Enums)),
			}
			for _, e := range c.Enums {
				co.Enums = append(co.Enums, taxonomyEnumOut{
					ID: e.ID, Names: e.Names,
					Nodes: flattenTaxonomyNodes(e.Nodes, "", []taxonomyNodeOut{}),
				})
			}
			out.Centrals = append(out.Centrals, co)
		}
		return nil, out, nil
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

type listSchedulesIn struct {
	Device string `json:"device,omitempty" jsonschema:"optional device address; narrows the list to the schedules of that device"`
}

type listSchedulesOut struct {
	Schedules []hmapi.ScheduleDeviceSummary `json:"schedules"`
}

func registerListSchedules(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_schedules",
		Description: "List every week schedule across the fleet, one entry per schedule, with its device, channel and kind (week_profile or climate). A device with two schedules, such as a glass wall thermostat with a heating and a switch schedule, appears twice.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listSchedulesIn) (*mcpsdk.CallToolResult, listSchedulesOut, error) {
		items, err := d.Schedules.ListScheduleDevices(ctx)
		if err != nil {
			return nil, listSchedulesOut{}, fmt.Errorf("list schedules: %w", err)
		}
		if in.Device != "" {
			items = handlers.SchedulesOfDevice(items, in.Device)
		}
		if items == nil {
			items = []hmapi.ScheduleDeviceSummary{}
		}
		return nil, listSchedulesOut{Schedules: items}, nil
	})
}
