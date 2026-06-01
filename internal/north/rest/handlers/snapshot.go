// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// SnapshotEnvelope is the wire shape of `GET /api/v1/snapshot`.
// The endpoint dumps the daemon's structural state in one round
// trip — useful for cold-restore tooling, periodic external backups,
// and debugging session replays. Run-time values (current data
// point readings) live alongside metadata so the snapshot is
// genuinely self-contained.
type SnapshotEnvelope struct {
	GeneratedAt string           `json:"generated_at"`
	Devices     []DeviceSummary  `json:"devices"`
	Programs    []ProgramSummary `json:"programs,omitempty"`
	Sysvars     []SysvarSummary  `json:"sysvars,omitempty"`
	Rooms       []RoomEntry      `json:"rooms,omitempty"`
	Functions   []FunctionEntry  `json:"functions,omitempty"`
	Interfaces  []InterfaceState `json:"interfaces,omitempty"`
	// DeviceChannels is populated only when the caller opts in via
	// `?include=channels` (or `data_points`). It nests each device's
	// channels — and, with `data_points`, their data points — so an
	// external client can bootstrap structure + values in one round trip
	// instead of N×M per-channel calls. Kept parallel to Devices so the
	// flat summary list stays byte-identical for clients that do not ask
	// for nesting.
	DeviceChannels []SnapshotDeviceChannels `json:"device_channels,omitempty"`
}

// SnapshotDeviceChannels groups one device's channels under its address
// for the nested snapshot shape. A slice (not a map) keeps the ordering
// deterministic.
type SnapshotDeviceChannels struct {
	DeviceAddress string                 `json:"device_address"`
	Channels      []SnapshotChannelEntry `json:"channels"`
}

// SnapshotChannelEntry is a ChannelSummary plus, when `?include=data_points`
// is set, the channel's data points. The embedded summary's
// `data_points_count` stays alongside the expanded `data_points` list.
type SnapshotChannelEntry struct {
	ChannelSummary
	DataPoints []DataPointSummary `json:"data_points,omitempty"`
}

// snapshotChannelLine is the NDJSON `kind:"channel"` shape: a channel
// summary stamped with its parent device address so the stream stays
// self-routable line by line.
type snapshotChannelLine struct {
	DeviceAddress string `json:"device_address"`
	ChannelSummary
}

// snapshotDataPointLine is the NDJSON `kind:"data_point"` shape: a data
// point summary stamped with its parent channel address.
type snapshotDataPointLine struct {
	ChannelAddress string `json:"channel_address"`
	DataPointSummary
}

// SnapshotDeps bundles the indices Snapshot pulls from. Every field
// is optional — a missing source contributes an empty slice rather
// than failing the whole request.
type SnapshotDeps struct {
	Devices    DeviceIndex
	Hub        HubIndex
	Interfaces InterfaceIndex
	// Labels resolves channel-type and parameter labels for the nested
	// `?include=` shape. Nil is safe — entries then carry empty labels.
	Labels ParameterLabeler
}

// snapshotInclude captures which nested entities the caller asked for.
type snapshotInclude struct {
	channels   bool
	dataPoints bool
}

// Snapshot dumps the structural state. Negotiates response shape by
// Accept header:
//
//   - default (`application/json` or no Accept) — one envelope object
//     in a single JSON response (legacy behaviour, friendly for small
//     installations and quick `curl | jq`).
//   - `application/x-ndjson` — line-delimited stream: one entity per
//     line, each `{"kind": ..., "data": ...}`. Avoids holding the full
//     payload in memory on either side; the right shape for the 80k-DP
//     initial-sync use case external clients hit.
//
// Returns 200 + body; never 5xx — sources that aren't wired just
// contribute empty lists / no lines.
//
// Privacy: callers may pass `?anonymize=1` (or `true`) to receive an
// anonymised copy where device/channel names, sysvar names, free-text
// descriptions, room/function names, and operator-assigned labels are
// replaced with stable but non-identifying tokens. CCU addresses, numeric
// values, and structural relationships stay intact so the snapshot remains
// useful for diagnostics. Applies to both response shapes.
func Snapshot(deps SnapshotDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		env := buildSnapshotEnvelope(deps, snapshotIncludes(r))
		if wantsAnonymise(r) {
			anonymiseSnapshot(&env)
		}
		if wantsNDJSON(r) {
			writeSnapshotNDJSON(w, env)
			return
		}
		JSON(w, http.StatusOK, env)
	}
}

// snapshotIncludes parses the `?include=` query parameter. Tokens are
// comma-separated; `channels` nests each device's channels and
// `data_points` additionally expands each channel's data points (and
// implies `channels`, since a data point only makes sense under its
// channel). Unknown tokens are ignored.
func snapshotIncludes(r *http.Request) snapshotInclude {
	var inc snapshotInclude
	if r == nil {
		return inc
	}
	for _, tok := range strings.Split(r.URL.Query().Get("include"), ",") {
		switch strings.TrimSpace(tok) {
		case "channels":
			inc.channels = true
		case "data_points", "data-points":
			inc.dataPoints = true
			inc.channels = true
		}
	}
	return inc
}

// buildSnapshotEnvelope assembles the envelope from the deps. Pulled
// out of [Snapshot] so the NDJSON path can reuse the same source-of-truth
// projection.
func buildSnapshotEnvelope(deps SnapshotDeps, inc snapshotInclude) SnapshotEnvelope {
	env := SnapshotEnvelope{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if deps.Devices != nil {
		devs := deps.Devices.Devices()
		sort.Slice(devs, func(i, j int) bool { return devs[i].Address < devs[j].Address })
		env.Devices = make([]DeviceSummary, 0, len(devs))
		for _, d := range devs {
			env.Devices = append(env.Devices, toDeviceSummary(d, deps.Devices.CentralOf(d.Address)))
		}
		if inc.channels {
			env.DeviceChannels = snapshotDeviceChannels(devs, deps.Labels, inc.dataPoints)
		}
		env.Rooms = snapshotRooms(deps.Devices)
		env.Functions = snapshotFunctions(deps.Devices)
	}
	if deps.Hub != nil {
		env.Programs = snapshotPrograms(deps.Hub)
		env.Sysvars = snapshotSysvars(deps.Hub)
	}
	if deps.Interfaces != nil {
		env.Interfaces = snapshotInterfaces(deps.Interfaces)
	}
	return env
}

// wantsNDJSON reports whether the client opts into the streaming
// shape. Recognises `application/x-ndjson` in any Accept entry
// (q-values ignored — first match wins).
func wantsNDJSON(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		mt := strings.TrimSpace(part)
		// Strip parameters (e.g. "application/x-ndjson; q=0.9").
		if i := strings.Index(mt, ";"); i >= 0 {
			mt = strings.TrimSpace(mt[:i])
		}
		if mt == "application/x-ndjson" || mt == "application/ndjson" {
			return true
		}
	}
	return false
}

// writeSnapshotNDJSON streams the envelope one entity per line.
// The first line is a `meta` record with `generated_at`; subsequent
// lines are `{kind, data}` per entity in deterministic order
// (interfaces → devices → rooms → functions → programs → sysvars).
// Order matches the legacy envelope's field declaration so consumers
// can buffer-then-merge if they want envelope semantics back.
func writeSnapshotNDJSON(w http.ResponseWriter, env SnapshotEnvelope) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)

	emit := func(kind string, data any) {
		_ = enc.Encode(map[string]any{"kind": kind, "data": data})
		if flusher != nil {
			flusher.Flush()
		}
	}

	emit("meta", map[string]any{"generated_at": env.GeneratedAt})
	for i := range env.Interfaces {
		emit("interface", env.Interfaces[i])
	}
	for i := range env.Devices {
		emit("device", env.Devices[i])
	}
	// Nested channels / data points (only present when the caller opted
	// in via ?include=). Each line carries the parent coordinate so a
	// stream consumer can route it without buffering the whole device.
	for i := range env.DeviceChannels {
		dc := &env.DeviceChannels[i]
		for j := range dc.Channels {
			ch := &dc.Channels[j]
			emit("channel", snapshotChannelLine{
				DeviceAddress:  dc.DeviceAddress,
				ChannelSummary: ch.ChannelSummary,
			})
			for k := range ch.DataPoints {
				emit("data_point", snapshotDataPointLine{
					ChannelAddress:   ch.Address,
					DataPointSummary: ch.DataPoints[k],
				})
			}
		}
	}
	for i := range env.Rooms {
		emit("room", env.Rooms[i])
	}
	for i := range env.Functions {
		emit("function", env.Functions[i])
	}
	for i := range env.Programs {
		emit("program", env.Programs[i])
	}
	for i := range env.Sysvars {
		emit("sysvar", env.Sysvars[i])
	}
}

// wantsAnonymise reports whether the request opts into the privacy
// mode. Accepts `1`, `true`, `yes` (case-insensitive) on the
// `anonymize` (US) and `anonymise` (UK) query parameters.
func wantsAnonymise(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, key := range []string{"anonymize", "anonymise"} {
		switch r.URL.Query().Get(key) {
		case "1", "true", "True", "TRUE", "yes", "Yes":
			return true
		}
	}
	return false
}

// anonymiseSnapshot replaces sensitive labels in env in place.
// Identifiers used as join keys (addresses, IDs) stay intact so the
// envelope is still self-consistent; only operator-assigned strings
// (names, descriptions, room/function labels) are tokenised.
//
// Tokens are stable hashes (12 hex chars from SHA-256) so the same
// input always yields the same anonymised output — diagnostics that
// span two snapshots can still correlate "device foo".
func anonymiseSnapshot(env *SnapshotEnvelope) {
	for i := range env.Devices {
		d := &env.Devices[i]
		d.Name = anonToken("device", d.Name)
		for j := range d.Rooms {
			d.Rooms[j] = anonToken("room", d.Rooms[j])
		}
		for j := range d.Functions {
			d.Functions[j] = anonToken("fn", d.Functions[j])
		}
	}
	// Nested channels carry operator-assigned channel + sub-device names;
	// tokenise them like the flat device names above. Data-point fields
	// are CCU-derived (parameter names, descriptor labels) and stay intact.
	for i := range env.DeviceChannels {
		for j := range env.DeviceChannels[i].Channels {
			ch := &env.DeviceChannels[i].Channels[j]
			ch.Name = anonToken("channel", ch.Name)
			ch.SubDeviceName = anonToken("channel", ch.SubDeviceName)
		}
	}
	for i := range env.Programs {
		p := &env.Programs[i]
		p.Name = anonToken("program", p.Name)
		p.Description = ""
	}
	for i := range env.Sysvars {
		s := &env.Sysvars[i]
		s.Name = anonToken("sysvar", s.Name)
	}
	for i := range env.Rooms {
		env.Rooms[i].Name = anonToken("room", env.Rooms[i].Name)
	}
	for i := range env.Functions {
		env.Functions[i].Name = anonToken("fn", env.Functions[i].Name)
	}
}

// anonToken returns a stable 12-char SHA-256 prefix for value, scoped
// by kind so the same string in different fields yields different
// tokens. Empty inputs return an empty string so structurally-empty
// fields stay empty.
func anonToken(kind, value string) string {
	if value == "" {
		return ""
	}
	h := sha256.Sum256([]byte(kind + ":" + value))
	return kind + "_" + hex.EncodeToString(h[:6])
}

// snapshotDeviceChannels nests each device's channels (and, when
// withDataPoints is set, their data points) using the same projection
// helpers the per-channel REST endpoints use. devs is expected pre-sorted
// by the caller so the output ordering is deterministic.
func snapshotDeviceChannels(devs []*device.Device, labels ParameterLabeler, withDataPoints bool) []SnapshotDeviceChannels {
	out := make([]SnapshotDeviceChannels, 0, len(devs))
	for _, d := range devs {
		chans := d.Channels()
		entries := make([]SnapshotChannelEntry, 0, len(chans))
		for _, ch := range chans {
			entry := SnapshotChannelEntry{ChannelSummary: toChannelSummary(ch, labels)}
			if withDataPoints {
				dps := ch.DataPoints()
				entry.DataPoints = make([]DataPointSummary, 0, len(dps))
				for _, dp := range dps {
					entry.DataPoints = append(entry.DataPoints, toDataPointSummary(dp, labels, ch.Type))
				}
			}
			entries = append(entries, entry)
		}
		out = append(out, SnapshotDeviceChannels{DeviceAddress: d.Address, Channels: entries})
	}
	return out
}

// snapshotRooms mirrors ListRooms's aggregation. Kept inline rather
// than extracted to a shared helper because the list-endpoint and
// the snapshot may diverge later (e.g. snapshot wants device-address
// lists per room, while the list view stays at counts).
func snapshotRooms(idx DeviceIndex) []RoomEntry {
	counts := map[string]int{}
	for _, d := range idx.Devices() {
		for _, r := range d.Rooms {
			counts[r]++
		}
	}
	out := make([]RoomEntry, 0, len(counts))
	for name, c := range counts {
		out = append(out, RoomEntry{Name: name, DeviceCount: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// snapshotFunctions is the function-side counterpart to
// snapshotRooms.
func snapshotFunctions(idx DeviceIndex) []FunctionEntry {
	counts := map[string]int{}
	for _, d := range idx.Devices() {
		for _, f := range d.Functions {
			counts[f]++
		}
	}
	out := make([]FunctionEntry, 0, len(counts))
	for name, c := range counts {
		out = append(out, FunctionEntry{Name: name, DeviceCount: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// snapshotPrograms reuses the existing list-programs projection so
// SnapshotEnvelope.Programs and `/programs` always agree.
func snapshotPrograms(idx HubIndex) []ProgramSummary {
	if idx.Hub() == nil {
		return nil
	}
	progs := idx.Hub().Programs()
	out := make([]ProgramSummary, 0, len(progs))
	for _, p := range progs {
		active, observed := p.Active()
		entry := ProgramSummary{ID: p.ID, Name: p.Name, Description: p.Description}
		if observed {
			v := active
			entry.Active = &v
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// snapshotSysvars projects the hub's current sysvars into the wire
// summary. Mirrors what `GET /sysvars` returns.
func snapshotSysvars(idx HubIndex) []SysvarSummary {
	if idx.Hub() == nil {
		return nil
	}
	vars := idx.Hub().Sysvars()
	out := make([]SysvarSummary, 0, len(vars))
	for _, s := range vars {
		out = append(out, toSysvarSummary(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// snapshotInterfaces drains the InterfaceIndex into the wire
// state used by the existing `/interfaces` handler.
func snapshotInterfaces(idx InterfaceIndex) []InterfaceState {
	out := idx.Interfaces()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
