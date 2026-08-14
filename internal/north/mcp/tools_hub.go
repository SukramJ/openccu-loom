// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mcp

import (
	"context"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// This file holds the hub- and device-derived read tools that project the
// CCU domain aggregates (programs, sysvars, messages, inbox, system info)
// and the device topology (rooms, functions, channels). They follow the
// same naming taxonomy as the core surface — see registerReadTools — and
// reuse the already-wired HubResolver + CentralLister + DeviceLister
// seams, so no new dependency is introduced. Every central-spanning tool
// takes an optional central_name: set it to scope to one CCU, omit it to
// span every configured central.

// centralScopeIn is the shared input for the central-spanning read tools.
type centralScopeIn struct {
	CentralName string `json:"central_name,omitempty" jsonschema:"optional CCU name to scope the result; omit to span every central"`
}

// centralsToScan resolves the centrals a central-spanning read tool should
// iterate: just the named one when central_name is set, else every
// configured central. A named-but-unknown central yields an empty result
// (its HubFor lookup returns nil and is skipped by the caller).
func centralsToScan(d Deps, centralName string) []string {
	if d.Centrals == nil {
		return nil
	}
	if want := strings.TrimSpace(centralName); want != "" {
		return []string{want}
	}
	return d.Centrals.Names()
}

// rfc3339OrEmpty formats a timestamp as RFC3339, or "" for the zero value
// so an unobserved timestamp is omitted from the projection.
func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// --- programs ---------------------------------------------------------

type programSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Central      string `json:"central"`
	Active       *bool  `json:"active,omitempty"`
	LastExecuted string `json:"last_executed,omitempty"`
}

type listProgramsOut struct {
	Programs []programSummary `json:"programs"`
}

func registerListPrograms(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_programs",
		Description: "List CCU automation programs, optionally scoped to one central via central_name. Returns each program's id (pass it to trigger_program), name, and last-execution state. Internal Tmp_* programs are omitted.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in centralScopeIn) (*mcpsdk.CallToolResult, listProgramsOut, error) {
		out := listProgramsOut{Programs: []programSummary{}}
		for _, c := range centralsToScan(d, in.CentralName) {
			h := d.Hubs.HubFor(c)
			if h == nil {
				continue
			}
			for _, p := range h.Programs() {
				if p.IsInternal {
					continue
				}
				ps := programSummary{
					ID:           p.ID,
					Name:         p.Name,
					Description:  p.Description,
					Central:      c,
					LastExecuted: p.LastExecuteTimeString(),
				}
				if active, observed := p.Active(); observed {
					ps.Active = &active
				}
				out.Programs = append(out.Programs, ps)
			}
		}
		return nil, out, nil
	})
}

// --- system variables -------------------------------------------------

type sysvarSummary struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Value     any      `json:"value,omitempty"`
	Unit      string   `json:"unit,omitempty"`
	ValueList []string `json:"value_list,omitempty"`
	Central   string   `json:"central"`
}

type listSysvarsOut struct {
	Sysvars []sysvarSummary `json:"sysvars"`
}

func registerListSysvars(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_sysvars",
		Description: "List CCU system variables (sysvars), optionally scoped to one central via central_name. Returns each variable's name, type, current value, and unit. Internal sysvars are omitted.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in centralScopeIn) (*mcpsdk.CallToolResult, listSysvarsOut, error) {
		out := listSysvarsOut{Sysvars: []sysvarSummary{}}
		for _, c := range centralsToScan(d, in.CentralName) {
			h := d.Hubs.HubFor(c)
			if h == nil {
				continue
			}
			for _, sv := range h.Sysvars() {
				if sv.IsInternal {
					continue
				}
				ss := sysvarSummary{
					Name:      sv.Name,
					Type:      string(sv.ValueType),
					Unit:      sv.Unit,
					ValueList: sv.ValueList,
					Central:   c,
				}
				if v, ok := sv.Value(); ok {
					ss.Value = v.Unwrap()
				}
				out.Sysvars = append(out.Sysvars, ss)
			}
		}
		return nil, out, nil
	})
}

// --- service messages -------------------------------------------------

type serviceMessageSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Address    string `json:"address,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	Type       string `json:"type"`
	Timestamp  string `json:"timestamp,omitempty"`
	// LastTimestamp is when the message last recurred. Omitted when the
	// CCU reports no such occurrence.
	LastTimestamp string   `json:"last_timestamp,omitempty"`
	Rooms         []string `json:"rooms,omitempty"`
	Functions     []string `json:"functions,omitempty"`
	Quittable     bool     `json:"quittable"`
	Central       string   `json:"central"`
}

type listServiceMessagesOut struct {
	Messages []serviceMessageSummary `json:"messages"`
}

func registerListServiceMessages(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_service_messages",
		Description: "List active CCU service messages (e.g. low battery, sabotage, communication errors), optionally scoped to one central via central_name. These surface device-maintenance conditions that get_health does not report.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in centralScopeIn) (*mcpsdk.CallToolResult, listServiceMessagesOut, error) {
		out := listServiceMessagesOut{Messages: []serviceMessageSummary{}}
		for _, c := range centralsToScan(d, in.CentralName) {
			h := d.Hubs.HubFor(c)
			if h == nil || h.ServiceMessages == nil {
				continue
			}
			msgs := h.ServiceMessages.List()
			for i := range msgs {
				m := &msgs[i]
				out.Messages = append(out.Messages, serviceMessageSummary{
					ID:            m.ID,
					Name:          m.Name,
					Address:       m.Address,
					DeviceName:    m.DeviceName,
					Type:          m.Type.String(),
					Timestamp:     rfc3339OrEmpty(m.Timestamp),
					LastTimestamp: rfc3339OrEmpty(m.LastTimestamp),
					Rooms:         m.Rooms,
					Functions:     m.Functions,
					Quittable:     m.Quittable,
					Central:       c,
				})
			}
		}
		return nil, out, nil
	})
}

// --- alarm messages ---------------------------------------------------

// alarmMessageSummary describes one active alarm entry. An alarm entry
// has no device, channel or room — the CCU backs it by an alarm system
// variable, not a device datapoint — so this carries only identity and
// timing fields. See [hub.AlarmMessage].
type alarmMessageSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
	// LastTimestamp is when the backing alarm variable last changed.
	// Omitted when the CCU reports no such occurrence.
	LastTimestamp string `json:"last_timestamp,omitempty"`
	Central       string `json:"central"`
}

type listAlarmMessagesOut struct {
	Messages []alarmMessageSummary `json:"messages"`
}

func registerListAlarmMessages(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_alarm_messages",
		Description: "List active CCU alarm messages (the alarm set, distinct from service messages), optionally scoped to one central via central_name.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in centralScopeIn) (*mcpsdk.CallToolResult, listAlarmMessagesOut, error) {
		out := listAlarmMessagesOut{Messages: []alarmMessageSummary{}}
		for _, c := range centralsToScan(d, in.CentralName) {
			h := d.Hubs.HubFor(c)
			if h == nil || h.Messages == nil {
				continue
			}
			msgs := h.Messages.List()
			for i := range msgs {
				m := &msgs[i]
				out.Messages = append(out.Messages, alarmMessageSummary{
					ID:            m.ID,
					Name:          m.Name,
					Description:   m.Description,
					Timestamp:     rfc3339OrEmpty(m.Timestamp),
					LastTimestamp: rfc3339OrEmpty(m.LastTimestamp),
					Central:       c,
				})
			}
		}
		return nil, out, nil
	})
}

// --- inbox (pending devices) ------------------------------------------

type inboxDeviceSummary struct {
	Address      string `json:"address"`
	Name         string `json:"name,omitempty"`
	Model        string `json:"model,omitempty"`
	Interface    string `json:"interface,omitempty"`
	Serial       string `json:"serial,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Central      string `json:"central"`
}

type listInboxOut struct {
	Devices []inboxDeviceSummary `json:"devices"`
}

func registerListInbox(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_inbox",
		Description: "List devices in the CCU inbox — newly detected devices not yet accepted into the configuration — optionally scoped to one central via central_name.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in centralScopeIn) (*mcpsdk.CallToolResult, listInboxOut, error) {
		out := listInboxOut{Devices: []inboxDeviceSummary{}}
		for _, c := range centralsToScan(d, in.CentralName) {
			h := d.Hubs.HubFor(c)
			if h == nil || h.Inbox == nil {
				continue
			}
			for _, dev := range h.Inbox.List() {
				out.Devices = append(out.Devices, inboxDeviceSummary{
					Address:      dev.Address,
					Name:         dev.Name,
					Model:        dev.Model,
					Interface:    dev.Interface,
					Serial:       dev.Serial,
					Manufacturer: dev.Manufacturer,
					Central:      c,
				})
			}
		}
		return nil, out, nil
	})
}

// --- system info ------------------------------------------------------

type systemInfoCentral struct {
	Central           string `json:"central"`
	ProgramCount      int    `json:"program_count"`
	SysvarCount       int    `json:"sysvar_count"`
	CurrentFirmware   string `json:"current_firmware,omitempty"`
	AvailableFirmware string `json:"available_firmware,omitempty"`
	UpdateAvailable   bool   `json:"update_available"`
	UpdateInProgress  bool   `json:"update_in_progress"`
}

type getSystemInfoOut struct {
	DaemonVersion string              `json:"daemon_version"`
	Centrals      []systemInfoCentral `json:"centrals"`
}

func registerGetSystemInfo(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_system_info",
		Description: "Report the daemon version and, per central (optionally scoped via central_name), the program/sysvar counts and CCU firmware-update state (current/available firmware, whether an update is available or running).",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in centralScopeIn) (*mcpsdk.CallToolResult, getSystemInfoOut, error) {
		out := getSystemInfoOut{DaemonVersion: d.Version, Centrals: []systemInfoCentral{}}
		for _, c := range centralsToScan(d, in.CentralName) {
			h := d.Hubs.HubFor(c)
			if h == nil {
				continue
			}
			sic := systemInfoCentral{
				Central:      c,
				ProgramCount: len(h.Programs()),
				SysvarCount:  len(h.Sysvars()),
			}
			if h.Update != nil {
				if info, ok := h.Update.UpdateInfo(); ok {
					sic.CurrentFirmware = info.CurrentFirmware
					sic.AvailableFirmware = info.AvailableFirmware
					sic.UpdateAvailable = info.UpdateAvailable
				}
				sic.UpdateInProgress = h.Update.InProgress()
			}
			out.Centrals = append(out.Centrals, sic)
		}
		return nil, out, nil
	})
}

// --- rooms / functions (device topology) ------------------------------

type namedGroupSummary struct {
	Name        string `json:"name"`
	DeviceCount int    `json:"device_count"`
}

type listRoomsOut struct {
	Rooms []namedGroupSummary `json:"rooms"`
}

type listFunctionsOut struct {
	Functions []namedGroupSummary `json:"functions"`
}

// countGroups tallies how many devices reference each group label, scoped
// to one central when want is set. selector pulls the room or function
// labels off a device; it receives the device value directly so the tally
// never depends on a stable iteration order across separate Devices()
// calls.
func countGroups(d Deps, want string, selector func(dev *device.Device) []string) []namedGroupSummary {
	if d.Devices == nil {
		return []namedGroupSummary{}
	}
	want = strings.TrimSpace(want)
	counts := map[string]int{}
	order := []string{}
	for _, dev := range d.Devices.Devices() {
		if want != "" && d.Devices.CentralOf(dev.Address) != want {
			continue
		}
		for _, label := range selector(dev) {
			if label == "" {
				continue
			}
			if _, seen := counts[label]; !seen {
				order = append(order, label)
			}
			counts[label]++
		}
	}
	out := make([]namedGroupSummary, 0, len(order))
	for _, name := range order {
		out = append(out, namedGroupSummary{Name: name, DeviceCount: counts[name]})
	}
	return out
}

func registerListRooms(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_rooms",
		Description: "List the configured rooms with the number of devices assigned to each, optionally scoped to one central via central_name.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in centralScopeIn) (*mcpsdk.CallToolResult, listRoomsOut, error) {
		rooms := countGroups(d, in.CentralName, func(dev *device.Device) []string { return dev.Rooms })
		return nil, listRoomsOut{Rooms: rooms}, nil
	})
}

func registerListFunctions(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_functions",
		Description: "List the configured functions (Gewerke) with the number of devices assigned to each, optionally scoped to one central via central_name.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in centralScopeIn) (*mcpsdk.CallToolResult, listFunctionsOut, error) {
		funcs := countGroups(d, in.CentralName, func(dev *device.Device) []string { return dev.Functions })
		return nil, listFunctionsOut{Functions: funcs}, nil
	})
}

// --- channels ---------------------------------------------------------

type channelSummary struct {
	Address     string `json:"address"`
	Number      int    `json:"number"`
	Type        string `json:"type"`
	TypeLabel   string `json:"type_label,omitempty"`
	Name        string `json:"name,omitempty"`
	Room        string `json:"room,omitempty"`
	ParamsetKey string `json:"paramset_key,omitempty"`
	DataPoints  int    `json:"data_points"`
}

type listChannelsIn struct {
	Address string `json:"address" jsonschema:"the device address / serial, e.g. 0001D3C99C1234 (device-level, not a channel address)"`
}

type listChannelsOut struct {
	Found    bool             `json:"found"`
	Central  string           `json:"central,omitempty"`
	Channels []channelSummary `json:"channels"`
}

func registerListChannels(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_channels",
		Description: "List the channels of a device by its address, so an agent can discover channel addresses (<device>:<n>) before calling read_paramset. Returns each channel's address, number, type, name, room, and data-point count.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in listChannelsIn) (*mcpsdk.CallToolResult, listChannelsOut, error) {
		out := listChannelsOut{Channels: []channelSummary{}}
		if d.Devices == nil {
			return nil, out, nil
		}
		dev, ok := d.Devices.Device(strings.TrimSpace(in.Address))
		if !ok {
			return nil, out, nil
		}
		out.Found = true
		out.Central = d.Devices.CentralOf(dev.Address)
		for _, ch := range dev.Channels() {
			out.Channels = append(out.Channels, channelSummary{
				Address:     ch.Address,
				Number:      ch.Number,
				Type:        ch.Type,
				TypeLabel:   ch.TypeTranslation(),
				Name:        ch.Name(),
				Room:        ch.Room(),
				ParamsetKey: string(ch.ParamsetIn),
				DataPoints:  len(ch.DataPoints()),
			})
		}
		return nil, out, nil
	})
}
