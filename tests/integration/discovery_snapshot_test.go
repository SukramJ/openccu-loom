// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDiscoverySnapshotDumpAgainstGodevccu produces the openccu-loom
// side of the cross-stack HA-Discovery snapshot diff described in
// `docs/parity/discovery_snapshot_schema.md`. It boots godevccu in
// HOMEGEAR mode, hydrates the openccu-loom device pipeline, walks every
// Generic-DP (VALUES + MASTER paramsets) on every channel, drives each
// through the MQTT bridge with HA-Discovery enabled, captures the
// resulting `homeassistant/.../config` publishes through a recording
// [mqtt.Publisher], and writes a sorted JSON snapshot to
// `tests/integration/testdata/discovery_snapshot_openccu-loom.json`.
//
// The
// (`script/aiohomematic2mqtt_discovery_snapshot.py`) emits the same
// Shape against
// `script/discovery_snapshot_diff.py` performs the structural diff.
//
// The test always succeeds (it dumps; it does not assert). The diff
// script produces the pass / fail signal in CI.
func TestDiscoverySnapshotDumpAgainstGodevccu(t *testing.T) {
	srv := startMockCCUWithDevices(t, snapshotDevices(t))

	xmlClient := newXMLRPCClient(t, srv.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "snapshot-ccu"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	locale := snapshotLocale()
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("translations: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c).WithTranslations(translations, locale)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	devices := c.ModelRegistry.List()
	sort.Slice(devices, func(i, j int) bool { return devices[i].Address < devices[j].Address })

	// Recording publisher captures every MQTT publish. We only retain
	// the homeassistant/* (Discovery) topics — raw-plane publishes are
	// out of scope for this snapshot.
	rec := newDiscoveryRecorder()

	bridgeCfg := mqtt.BridgeConfig{
		Base:               "gh",
		CentralName:        "ccu-01",
		RawEnabled:         false,
		HADiscoveryEnabled: true,
	}
	bridge := mqtt.NewBridge(bridgeCfg, rec)

	// Drive every Generic-DP through the bridge so the discovery
	// builder has a chance to emit a config payload. The bridge dedups
	// per-topic, so iterating every parameter on an aggregated channel
	// (climate / cover / light / lock / valve / siren) ends up with one
	// Payload per channel — the same contract
	// where one CallbackDataPoint = one config publish.
	for _, d := range devices {
		for _, ch := range d.Channels() {
			if ch == nil {
				continue
			}
			driveChannelDPs(ctx, bridge, d, ch)
		}
	}

	entities := rec.Entities()
	sort.Slice(entities, func(i, j int) bool { return entities[i].JoinKey < entities[j].JoinKey })

	snapshot := discoverySnapshotRoot{
		Stack:         "openccu-loom",
		StackVersion:  stackVersion(),
		Devccu:        "godevccu",
		DevccuVersion: godevccuVersion(),
		Locale:        locale,
		CapturedAt:    time.Now().UTC().Format(time.RFC3339),
		Entities:      entities,
	}

	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	out := filepath.Join("testdata", "discovery_snapshot_openccu-loom.json")
	var bb bytes.Buffer
	enc := json.NewEncoder(&bb)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snapshot); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(out, bb.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("discovery snapshot written: %s (entities=%d)", out, len(entities))
}

// driveChannelDPs builds an [mqtt.Event] for every Generic-DP on the
// channel (both VALUES and MASTER paramsets) and pushes it through the
// bridge's HA-Discovery path. The recorder captures the resulting
// publishes; per-topic deduplication in the bridge ensures we get one
// entity per unique discovery topic regardless of how many DPs share
// it.
func driveChannelDPs(ctx context.Context, bridge *mqtt.Bridge, d *device.Device, ch *device.Channel) {
	model := d.Model
	iface := string(d.Interface)
	addr := d.Address

	// Mirror the EventBridge: surface the channel's CustomDataPoint as
	// `ev.Source` so the channel-aware aggregator (climate / cover /
	// light / lock / siren / valve) can fire. Without this every
	// custom-domain channel falls through to the per-parameter path
	// and the snapshot misses every aggregate entity.
	var source payload.Source
	if cdp := ch.CustomDataPoint(); cdp != nil {
		if src, ok := cdp.(payload.Source); ok && src != nil {
			source = src
		}
	}

	common := mqtt.Event{
		Central:        "ccu-01",
		Interface:      iface,
		DeviceAddress:  addr,
		DeviceName:     d.Name,
		Model:          model,
		ChannelNo:      ch.Number,
		ChannelAddress: ch.Address,
		ChannelType:    ch.Type,
		Channel:        ch,
		Device:         d,
		Source:         source,
	}

	// VALUES paramset — primary surface. Mirror the EventBridge's
	// visibility gate: a DP whose runtime Usage was forced to
	// NO_CREATE (e.g. SuppressUndefinedGenericDataPoints on linked
	// virtual channels) is invisible and must not produce a Discovery
	// payload — without this gate the snapshot reports thousands of
	// over-emitted entities relative to the Production-EventBridge.
	for _, dp := range ch.DataPoints() {
		if !visibleForDiscovery(dp) {
			continue
		}
		ev := buildEvent(common, hmenum.ParamsetKeyValues, dp)
		_ = bridge.PublishState(ctx, ev)
	}
	// MASTER paramset — config-category surface.
	for _, dp := range ch.MasterDataPoints() {
		if !visibleForDiscovery(dp) {
			continue
		}
		ev := buildEvent(common, hmenum.ParamsetKeyMaster, dp)
		_ = bridge.PublishState(ctx, ev)
	}
}

// visibleForDiscovery mirrors the EventBridge visibility gate
// (`internal/central/adapter/eventbridge.go:444-446`). DPs whose
// `Visible()` returns false (typically because the runtime usage
// pipeline forced them to NO_CREATE) never reach the Production
// MQTT bridge; the snapshot test must filter them out to mirror
// Production behaviour.
func visibleForDiscovery(dp device.ParameterDataPoint) bool {
	if v, ok := dp.(interface{ Visible() bool }); ok {
		return v.Visible()
	}
	return true
}

// buildEvent fills the descriptor-derived fields on a base Event so
// the discovery builder has min / max / default / value_list /
// writability / category — mirroring what the EventBridge populates
// at runtime. The Category propagation enables the model-driven
// component resolution ().
func buildEvent(base mqtt.Event, paramset hmenum.ParamsetKey, dp device.ParameterDataPoint) mqtt.Event {
	desc := dp.ParameterData()
	ev := base
	ev.Parameter = string(dp.Parameter())
	ev.Writable = desc.IsWritable()
	if cd, ok := dp.(interface {
		Category() hmenum.DataPointCategory
	}); ok {
		ev.Category = cd.Category()
	}
	gc := &payload.GenericConfig{
		Paramset:  paramset,
		Unit:      generic.CleanupUnit(dp.Parameter(), desc.Unit),
		ValueList: append([]string(nil), desc.ValueList...),
		Type:      desc.Type,
	}
	if v, ok := decodeFloat(desc.Min); ok {
		gc.Min = &v
	}
	if v, ok := decodeFloat(desc.Max); ok {
		gc.Max = &v
	}
	if v, ok := decodeFloat(desc.Default); ok {
		gc.Default = &v
	}
	ev.Descriptor = gc
	return ev
}

func decodeFloat(rm json.RawMessage) (float64, bool) {
	if len(rm) == 0 {
		return 0, false
	}
	var v any
	if err := json.Unmarshal(rm, &v); err != nil {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if x == "" {
			return 0, false
		}
		var f float64
		if err := json.Unmarshal([]byte(x), &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Recording publisher
// ---------------------------------------------------------------------------

// discoveryEntry tracks a single homeassistant/.../config publish.
type discoveryEntry struct {
	Topic    string
	Payload  []byte
	Event    mqtt.Event
	Paramset hmenum.ParamsetKey
}

type discoveryRecorder struct {
	byTopic map[string]*discoveryEntry
}

func newDiscoveryRecorder() *discoveryRecorder {
	return &discoveryRecorder{byTopic: make(map[string]*discoveryEntry, 1024)}
}

// Publish satisfies [mqtt.Publisher]. We only retain Discovery topics
// (homeassistant/<comp>/<node>/<obj>/config). The bridge calls Publish
// for both raw-plane and Discovery topics; raw-plane is suppressed by
// BridgeConfig.RawEnabled = false in the test, so in practice this
// only sees Discovery topics — but we filter explicitly for safety.
func (r *discoveryRecorder) Publish(_ context.Context, topic string, payload []byte, _ mqtt.QoS, _ bool) error {
	if !strings.HasPrefix(topic, "homeassistant/") {
		return nil
	}
	if !strings.HasSuffix(topic, "/config") {
		return nil
	}
	// Only the FIRST event for a topic — the bridge dedups identical
	// payloads internally, but we want the first contributor for join-
	// key construction.
	if _, exists := r.byTopic[topic]; exists {
		return nil
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	r.byTopic[topic] = &discoveryEntry{Topic: topic, Payload: cp}
	return nil
}

// SetCurrentEvent is a hook the test caller sets before each
// Publish — but since Publisher.Publish does not see the Event
// directly, we instead enrich entries after the fact via Entities().
// In practice we rely on the topic structure to derive join_key.

// Entities decodes every captured publish into a [snapshotEntity]
// suitable for JSON serialisation.
func (r *discoveryRecorder) Entities() []snapshotEntity {
	out := make([]snapshotEntity, 0, len(r.byTopic))
	for topic, entry := range r.byTopic {
		ent := decodeEntity(topic, entry.Payload)
		out = append(out, ent)
	}
	return out
}

// decodeEntity parses the Discovery topic + payload into the
// snapshot's schema.
func decodeEntity(topic string, payload []byte) snapshotEntity {
	parts := strings.Split(topic, "/")
	// Expected: homeassistant/<component>/<node_id>/<object_id>/config
	component := ""
	nodeID := ""
	objectID := ""
	if len(parts) >= 5 {
		component = parts[1]
		nodeID = parts[2]
		objectID = parts[3]
	}

	var body map[string]any
	_ = json.Unmarshal(payload, &body)
	keySorted := sortKeys(body)

	uniqueID := ""
	if v, ok := body["unique_id"].(string); ok {
		uniqueID = v
	}

	channelNo, suffix := splitObjectID(objectID)
	addr := ""
	parameter := ""
	chType := ""
	model := ""
	paramset := ""

	if v, ok := body["device"].(map[string]any); ok {
		if name, ok := v["model"].(string); ok {
			model = name
		}
		if id, ok := identifierFromDevice(v); ok {
			addr = id
		}
	}

	// Aggregator object_id is `<channel>_<component>` — suffix matches
	// the topic-level component string (e.g. `4_cover`,
	// `1_climate`). Per-parameter object_id is `<channel>_<lowerparam>`
	// where the suffix is the lowercase parameter name (e.g. `4_level`,
	// `0_low_bat`). The two cases share an arity but differ on whether
	// suffix == component.
	kind := "param"
	switch {
	case suffix == component && isAggregateComponent(component):
		kind = "agg"
		parameter = ""
	case component == "event" && suffix == "event":
		kind = "event"
		parameter = ""
	default:
		parameter = strings.ToUpper(suffix)
	}

	if cat, ok := body["entity_category"].(string); ok && cat == "config" {
		paramset = "MASTER"
	}
	if paramset == "" && kind == "param" {
		paramset = "VALUES"
	}

	addrUpper := strings.ToUpper(addr)
	join := buildJoinKey(addrUpper, channelNo, kind, paramset, parameter, component)

	return snapshotEntity{
		JoinKey:        join,
		Kind:           kind,
		DiscoveryTopic: topic,
		Component:      component,
		NodeID:         nodeID,
		ObjectID:       objectID,
		UniqueID:       uniqueID,
		DeviceAddress:  addrUpper,
		ChannelNo:      channelNo,
		ChannelType:    chType,
		Model:          model,
		ParamsetKey:    paramset,
		Parameter:      parameter,
		Payload:        keySorted,
	}
}

// identifierFromDevice extracts a CCU device address from the
// `identifiers` block. Both stacks emit a list with one or more
// entries; the trailing segment after the last `_` is typically the
// CCU address (`openccu-loom_vcu0001234` → `vcu0001234`).
func identifierFromDevice(dev map[string]any) (string, bool) {
	ids, ok := dev["identifiers"].([]any)
	if !ok || len(ids) == 0 {
		return "", false
	}
	first, ok := ids[0].(string)
	if !ok {
		return "", false
	}
	// Strip a leading "openccu-loom_" prefix if present.
	first = strings.TrimPrefix(first, "openccu-loom_")
	// CCU addresses are uppercase; lower-case forms in unique_ids are
	// converted back later.
	return first, true
}

// splitObjectID separates `<channel>_<suffix>` into (channel, suffix).
// Returns (0, s) when the string does not match the expected shape.
func splitObjectID(s string) (int, string) {
	idx := strings.Index(s, "_")
	if idx <= 0 || idx >= len(s)-1 {
		return 0, s
	}
	n, err := strconv.Atoi(s[:idx])
	if err != nil {
		return 0, s
	}
	return n, s[idx+1:]
}

func isAggregateComponent(c string) bool {
	switch c {
	case "climate", "cover", "light", "lock", "valve", "siren":
		return true
	}
	return false
}

func buildJoinKey(addr string, ch int, kind, paramset, parameter, component string) string {
	switch kind {
	case "param":
		return fmt.Sprintf("%s:%d:param:%s.%s", addr, ch, paramset, parameter)
	case "agg":
		return fmt.Sprintf("%s:%d:agg:%s", addr, ch, component)
	case "event":
		return fmt.Sprintf("%s:%d:event:channel", addr, ch)
	}
	return fmt.Sprintf("%s:%d:%s:%s", addr, ch, kind, parameter)
}

// sortKeys returns a deterministic, key-sorted copy of body. Maps are
// sorted recursively. Lists are preserved in their original order
// (semantic positional order matters for HA — `modes`, `preset_modes`,
// `options`, etc. drive the UI dropdown order).
func sortKeys(body map[string]any) map[string]any {
	if body == nil {
		return nil
	}
	out := make(map[string]any, len(body))
	for k, v := range body {
		out[k] = sortValue(v)
	}
	return out
}

func sortValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return sortKeys(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = sortValue(item)
		}
		return out
	default:
		return v
	}
}

// ---------------------------------------------------------------------------
// Snapshot shapes — keep in sync with docs/parity/discovery_snapshot_schema.md
// and with script/aiohomematic2mqtt_discovery_snapshot.py.
// ---------------------------------------------------------------------------

type discoverySnapshotRoot struct {
	Stack         string           `json:"stack"`
	StackVersion  string           `json:"stack_version"`
	Devccu        string           `json:"devccu"`
	DevccuVersion string           `json:"devccu_version"`
	Locale        string           `json:"locale"`
	CapturedAt    string           `json:"captured_at"`
	Entities      []snapshotEntity `json:"entities"`
}

type snapshotEntity struct {
	JoinKey        string         `json:"join_key"`
	Kind           string         `json:"kind"`
	DiscoveryTopic string         `json:"discovery_topic"`
	Component      string         `json:"component"`
	NodeID         string         `json:"node_id"`
	ObjectID       string         `json:"object_id"`
	UniqueID       string         `json:"unique_id"`
	DeviceAddress  string         `json:"device_address"`
	ChannelNo      int            `json:"channel_no"`
	ChannelType    string         `json:"channel_type,omitempty"`
	Model          string         `json:"model,omitempty"`
	ParamsetKey    string         `json:"paramset_key,omitempty"`
	Parameter      string         `json:"parameter,omitempty"`
	Payload        map[string]any `json:"payload"`
}
