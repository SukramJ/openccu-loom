// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Hub-entity discovery — sysvars, programs, alarm/service messages,
// install-mode, per-interface connectivity. Each maps to a single HA
// entity rooted on a synthetic device that represents the central
// Itself. The
// device block uses a stable identifier `openccu-loom_central_<name>`
// so HA groups every hub entity under one card.
//
// All builders return the canonical (component, nodeID, objectID,
// payload, ok) shape consumed by [Bridge.publishDiscovery].

// DiscoveryItem packages a built HA Discovery message so it can be
// passed as a single value (Go doesn't spread multi-return into a
// function call). `OK=false` is a quietly-skipped no-op when handed
// to [Bridge.PublishHubDiscovery].
type DiscoveryItem struct {
	Component string
	NodeID    string
	ObjectID  string
	Payload   []byte
	OK        bool
}

// HubSysvarSpec is the narrow read-side contract on a sysvar that the
// discovery builder needs. Mirrors the fields a sysvar would carry
// regardless of where it lives in the model layer — the bridge stays
// free of the `internal/model/hub` import.
type HubSysvarSpec struct {
	Name        string
	Description string
	Unit        string
	ValueList   []string
	ValueType   hmenum.HubValueType
	Writable    bool
	Min         *float64
	Max         *float64
}

// HubInfo carries the optional CCU metadata that enriches the synthetic
// HA device block for hub entities. All fields are optional — zero
// values fall back to static defaults. Populate via [DefaultDiscoveryBuilder.WithHubInfo]
// after querying the CCU's getVersion / system.getSystemInfo call.
type HubInfo struct {
	// Name overrides the central identifier as the HA device name.
	Name string
	// Model overrides the default "HomeMatic Central" model string.
	Model string
	// Version is included as `sw_version` when non-empty.
	Version string
	// Serial is included as `serial_number` when non-empty.
	Serial string
	// URL is included as `configuration_url` when non-empty.
	URL string
}

// hubDeviceBlock builds the synthetic HA `device` block that groups
// every hub entity belonging to one central. Identifier matches the
// `<central>_<addr>` shape we use elsewhere; here `<addr>` is the
// literal "central" so HA can distinguish the hub-device card from
// the per-physical-device cards.
// When info carries non-zero fields they override the static defaults.
func hubDeviceBlock(central string, info HubInfo) map[string]any {
	name := central
	if info.Name != "" {
		name = info.Name
	}
	model := "HomeMatic Central"
	if info.Model != "" {
		model = info.Model
	}
	block := map[string]any{
		"identifiers":  []string{"openccu-loom_central_" + safeLower(central)},
		"name":         name,
		"manufacturer": "eQ-3",
		"model":        model,
	}
	if info.Version != "" {
		block["sw_version"] = info.Version
	}
	if info.Serial != "" {
		block["serial_number"] = info.Serial
	}
	if info.URL != "" {
		block["configuration_url"] = info.URL
	}
	return block
}

func hubAvailability(t *TopicBuilder) []map[string]string {
	return []map[string]string{
		{
			"topic":                 t.BridgeStatus(),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
	}
}

func hubNodeID(central, kind string) string {
	return safeLower(central) + "_" + kind
}

// safeLower turns name into an HA-Discovery-safe identifier suitable
// for the `<node_id>` and `<object_id>` segments of
// `homeassistant/<component>/<node_id>/<object_id>/config` as well as
// the `unique_id` / device-identifier fields in the payload. HA only
// accepts `[A-Za-z0-9_-]+` for these segments — `:`, umlauts, spaces,
// and other punctuation that CCU sysvar names routinely carry
// (`Watchdog:_CCU-Jack`, `s0_Sensoren_Hülle_EG`, …) get HA to drop
// the discovery message with a warning, so the entity never appears.
//
// Rules:
//   - German umlauts and ß are transliterated (ü→ue, ö→oe, ä→ae,
//     ß→ss) before the case fold so meaningful identifiers survive
//     the slug step (`Hülle` → `huelle` rather than `h_lle`).
//   - All remaining bytes outside `[A-Za-z0-9_-]` collapse to a single
//     `_`; runs are de-duplicated; leading/trailing `_` are trimmed.
//   - Empty input or input that reduces to "" returns "x" so callers
//     never emit a zero-length segment that HA would reject.
func safeLower(s string) string {
	if s == "" {
		return "x"
	}
	var out strings.Builder
	out.Grow(len(s))
	prevUnderscore := false
	emit := func(r rune) {
		out.WriteRune(r)
		prevUnderscore = r == '_'
	}
	flush := func() {
		if !prevUnderscore {
			out.WriteByte('_')
			prevUnderscore = true
		}
	}
	for _, r := range s {
		switch r {
		case 'ä':
			emit('a')
			emit('e')
		case 'ö':
			emit('o')
			emit('e')
		case 'ü':
			emit('u')
			emit('e')
		case 'Ä':
			emit('a')
			emit('e')
		case 'Ö':
			emit('o')
			emit('e')
		case 'Ü':
			emit('u')
			emit('e')
		case 'ß':
			emit('s')
			emit('s')
		default:
			switch {
			case r >= 'A' && r <= 'Z':
				emit(r + ('a' - 'A'))
			case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_':
				emit(r)
			default:
				flush()
			}
		}
	}
	res := strings.Trim(out.String(), "_")
	if res == "" {
		return "x"
	}
	return res
}

// ----------------------------- Sysvar -----------------------------

// BuildSysvarDiscovery emits the HA Discovery payload for one sysvar.
// Component selection mirrors
// mapping:
//
// - LOGIC → switch (writable) / binary_sensor (read-only)
// - ALARM → binary_sensor (problem device_class)
// - LIST → select (writable) / sensor (read-only)
// - STRING → text (writable) / sensor
// - NUMBER, FLOAT,
// INTEGER → number (writable) / sensor
//
// The stable HA `unique_id` is `loom_<serial10>_sysvar_<slug>`,
// independent of the friendly description so renames in the CCU don't
// orphan HA history.
func (d *DefaultDiscoveryBuilder) BuildSysvarDiscovery(central string, sv HubSysvarSpec) DiscoveryItem {
	if sv.Name == "" {
		return DiscoveryItem{}
	}
	var component string
	stateTopic := naming.MQTTHubSysvarState(d.BridgeBase, central, sv.Name)
	commandTopic := naming.MQTTHubSysvarCommand(d.BridgeBase, central, sv.Name)
	uniqueID := routingkey.CanonicalUniqueID(d.serialSuffix(central), "sysvar", routingkey.HubSlug(sv.Name), "")

	body := map[string]any{
		"name":              displaySysvarName(sv),
		"unique_id":         uniqueID,
		"object_id":         uniqueID,
		"state_topic":       stateTopic,
		"availability":      hubAvailability(d.TopicBuilder),
		"availability_mode": "all",
		"device":            hubDeviceBlock(central, d.hubFor(central)),
		"origin":            BuildOriginInfo(),
	}

	switch sv.ValueType {
	case hmenum.HubValueTypeLogic:
		if sv.Writable {
			component = string(HAComponentSwitch)
			body["command_topic"] = commandTopic
			body["payload_on"] = "true"
			body["payload_off"] = "false"
			body["state_on"] = "true"
			body["state_off"] = "false"
			body["optimistic"] = false
		} else {
			component = string(HAComponentBinarySensor)
			body["payload_on"] = "true"
			body["payload_off"] = "false"
		}
	case hmenum.HubValueTypeAlarm:
		component = string(HAComponentBinarySensor)
		body["payload_on"] = "true"
		body["payload_off"] = "false"
		body["device_class"] = "problem"
	case hmenum.HubValueTypeList:
		if sv.Writable && len(sv.ValueList) > 0 {
			component = string(HAComponentSelect)
			body["command_topic"] = commandTopic
			body["options"] = sv.ValueList
			body["optimistic"] = false
			body["entity_category"] = EntityCategoryConfig
		} else {
			component = string(HAComponentSensor)
			if len(sv.ValueList) > 0 {
				body["device_class"] = "enum"
				body["options"] = sv.ValueList
			}
		}
	case hmenum.HubValueTypeString:
		// HA's `text` entity caps state payloads at 255 chars and warns
		// loudly on every overrun. CCU string sysvars (e.g.
		// `AlleServicemeldungen`) routinely exceed that with multi-line
		// service-message dumps, so we render every string sysvar as a
		// read-only `sensor` — no length cap, no truncation, no
		// inbound-write surface from HA. Operators who need to write a
		// string sysvar still have the CCU UI and our REST API.
		component = string(HAComponentSensor)
	case hmenum.HubValueTypeNumber, hmenum.HubValueTypeFloat, hmenum.HubValueTypeInteger:
		if sv.Writable {
			component = string(HAComponentNumber)
			body["command_topic"] = commandTopic
			body["optimistic"] = false
			if sv.ValueType == hmenum.HubValueTypeInteger {
				body["mode"] = "box"
				body["step"] = 1
			} else {
				body["mode"] = "auto"
				body["step"] = 0.01
			}
		} else {
			component = string(HAComponentSensor)
			body["state_class"] = "measurement"
		}
		// HA's `number` entity defaults to min=1, max=100 when the
		// discovery payload omits these fields — which is what
		// triggered the "Invalid value … (range 0.0 - 100.0)" warnings
		// for energy / sunshine counters delivering 10 ⁶+ readings.
		// Send a wide fallback range whenever the model does not carry
		// a declared bound; the sensor path (`!Writable`) is unaffected
		// because HA sensors have no min/max contract.
		if sv.Writable {
			if sv.Min != nil {
				body["min"] = *sv.Min
			} else {
				body["min"] = -1e9
			}
			if sv.Max != nil {
				body["max"] = *sv.Max
			} else {
				body["max"] = 1e9
			}
		} else {
			if sv.Min != nil {
				body["min"] = *sv.Min
			}
			if sv.Max != nil {
				body["max"] = *sv.Max
			}
		}
		if sv.Unit != "" {
			body["unit_of_measurement"] = sv.Unit
		}
	default:
		// Unknown value-type; surface as a plain sensor so the data
		// is at least visible — better than dropping the entity.
		component = string(HAComponentSensor)
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: component, NodeID: hubNodeID(central, "sysvars"), ObjectID: safeLower(sv.Name), Payload: buf, OK: true}
}

func displaySysvarName(sv HubSysvarSpec) string {
	if sv.Description != "" {
		return sv.Description
	}
	return sv.Name
}

// ----------------------------- Program ----------------------------

// BuildProgramDiscovery emits one HA `switch` per CCU program.
// `turn_on` triggers the program (write to /trigger); state reflects
// the most recent execution active flag.
func (d *DefaultDiscoveryBuilder) BuildProgramDiscovery(central, id, name string) DiscoveryItem {
	if id == "" {
		return DiscoveryItem{}
	}
	stateTopic := naming.MQTTHubProgramState(d.BridgeBase, central, id)
	commandTopic := naming.MQTTHubProgramTrigger(d.BridgeBase, central, id)
	// Programs are keyed by NAME (slug), not by ID. When no name is supplied,
	// fall back to the ID slug so the unique_id stays stable across renames.
	programSlug := routingkey.HubSlug(name)
	if programSlug == "" {
		programSlug = routingkey.HubSlug(id)
	}
	uniqueID := routingkey.CanonicalUniqueID(d.serialSuffix(central), "program", programSlug, "")
	displayName := name
	if displayName == "" {
		displayName = id
	}
	body := map[string]any{
		"name":              displayName,
		"unique_id":         uniqueID,
		"object_id":         uniqueID,
		"state_topic":       stateTopic,
		"command_topic":     commandTopic,
		"payload_on":        "true",
		"payload_off":       "false",
		"state_on":          "true",
		"state_off":         "false",
		"optimistic":        false,
		"availability":      hubAvailability(d.TopicBuilder),
		"availability_mode": "all",
		"device":            hubDeviceBlock(central, d.hubFor(central)),
		"origin":            BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentSwitch), NodeID: hubNodeID(central, "programs"), ObjectID: safeLower(id), Payload: buf, OK: true}
}

// ------------------- AlarmMessages / ServiceMessages -------------

// BuildAlarmMessagesDiscovery exposes the CCU alarm-message list as a HA
// `sensor` whose value is the message count and whose `json_attributes_topic`
// carries the full list. `device_class: problem` belongs to binary_sensor and
// would be rejected on a sensor entity, so it is intentionally omitted.
func (d *DefaultDiscoveryBuilder) BuildAlarmMessagesDiscovery(central string) DiscoveryItem {
	topic := naming.MQTTHubAlarmMessages(d.BridgeBase, central)
	uniqueID := hubAggregateUniqueID(d.serialSuffix(central), "alarm_messages")
	body := map[string]any{
		"name":                     "Alarm Messages",
		"unique_id":                uniqueID,
		"object_id":                uniqueID,
		"state_topic":              topic,
		"value_template":           "{{ value_json | length }}",
		"json_attributes_topic":    topic,
		"json_attributes_template": `{"messages": {{ value_json | tojson }} }`,
		"state_class":              "measurement",
		"entity_category":          "diagnostic",
		"availability":             hubAvailability(d.TopicBuilder),
		"availability_mode":        "all",
		"device":                   hubDeviceBlock(central, d.hubFor(central)),
		"origin":                   BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentSensor), NodeID: hubNodeID(central, "messages"), ObjectID: "alarm", Payload: buf, OK: true}
}

// BuildServiceMessagesDiscovery is the maintenance-list counterpart
// to [BuildAlarmMessagesDiscovery]. Diagnostic category, no
// device_class (HA shows a neutral icon).
func (d *DefaultDiscoveryBuilder) BuildServiceMessagesDiscovery(central string) DiscoveryItem {
	topic := naming.MQTTHubServiceMessages(d.BridgeBase, central)
	uniqueID := hubAggregateUniqueID(d.serialSuffix(central), "service_messages")
	body := map[string]any{
		"name":                     "Service Messages",
		"unique_id":                uniqueID,
		"object_id":                uniqueID,
		"state_topic":              topic,
		"value_template":           "{{ value_json | length }}",
		"json_attributes_topic":    topic,
		"json_attributes_template": `{"messages": {{ value_json | tojson }} }`,
		"entity_category":          "diagnostic",
		"availability":             hubAvailability(d.TopicBuilder),
		"availability_mode":        "all",
		"device":                   hubDeviceBlock(central, d.hubFor(central)),
		"origin":                   BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentSensor), NodeID: hubNodeID(central, "messages"), ObjectID: "service", Payload: buf, OK: true}
}

// ------------------------- Inbox ----------------------------------

// BuildInboxDiscovery exposes the pending-device inbox count as a HA
// sensor. The state topic carries the full inbox list; a
// value_template extracts the count. A json_attributes_topic exposes
// the raw device list for automations that need the details.
// Mirrors the inbox sensor in the hub model (translation_key="inbox",
// state_class="measurement", enabled_default=True).
func (d *DefaultDiscoveryBuilder) BuildInboxDiscovery(central string) DiscoveryItem {
	topic := naming.MQTTHubInbox(d.BridgeBase, central)
	uniqueID := hubAggregateUniqueID(d.serialSuffix(central), "inbox")
	body := map[string]any{
		"name":                     "Inbox",
		"unique_id":                uniqueID,
		"object_id":                uniqueID,
		"state_topic":              topic,
		"value_template":           "{{ value_json | length }}",
		"json_attributes_topic":    topic,
		"json_attributes_template": `{"devices": {{ value_json | tojson }} }`,
		"state_class":              "measurement",
		"icon":                     "mdi:tray-arrow-down",
		"availability":             hubAvailability(d.TopicBuilder),
		"availability_mode":        "all",
		"device":                   hubDeviceBlock(central, d.hubFor(central)),
		"origin":                   BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentSensor), NodeID: hubNodeID(central, "messages"), ObjectID: "inbox", Payload: buf, OK: true}
}

// ----------------------- InstallMode ------------------------------

// BuildInstallModeDiscovery is the remaining-seconds counter for the
// CCU's pairing-install mode. Surfaced as a `sensor` (not a number)
// because the value is read-only — actual install-mode toggling
// happens through REST.
func (d *DefaultDiscoveryBuilder) BuildInstallModeDiscovery(central string) DiscoveryItem {
	topic := naming.MQTTHubInstallMode(d.BridgeBase, central)
	uniqueID := routingkey.CanonicalUniqueID(d.serialSuffix(central), "install_mode", "", "")
	body := map[string]any{
		"name":                "Install Mode",
		"unique_id":           uniqueID,
		"object_id":           uniqueID,
		"state_topic":         topic,
		"device_class":        "duration",
		"unit_of_measurement": "s",
		"state_class":         "measurement",
		"entity_category":     "diagnostic",
		"availability":        hubAvailability(d.TopicBuilder),
		"availability_mode":   "all",
		"device":              hubDeviceBlock(central, d.hubFor(central)),
		"origin":              BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentSensor), NodeID: hubNodeID(central, "central"), ObjectID: "install_mode", Payload: buf, OK: true}
}

// ---------------------- Connectivity ------------------------------

// BuildConnectivityDiscovery is one HA `binary_sensor` per
// CCU-interface (HmIP-RF, BidCos-RF, …). `device_class: connectivity`
// flips the icon between connected / disconnected.
func (d *DefaultDiscoveryBuilder) BuildConnectivityDiscovery(central, iface string) DiscoveryItem {
	if iface == "" {
		return DiscoveryItem{}
	}
	topic := naming.MQTTHubConnectivity(d.BridgeBase, central, iface)
	uniqueID := hubAggregateUniqueID(d.serialSuffix(central), "connectivity_"+safeLower(iface))
	body := map[string]any{
		"name":              fmt.Sprintf("Connectivity %s", iface),
		"unique_id":         uniqueID,
		"object_id":         uniqueID,
		"state_topic":       topic,
		"payload_on":        "true",
		"payload_off":       "false",
		"device_class":      "connectivity",
		"entity_category":   "diagnostic",
		"availability":      hubAvailability(d.TopicBuilder),
		"availability_mode": "all",
		"device":            hubDeviceBlock(central, d.hubFor(central)),
		"origin":            BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentBinarySensor), NodeID: hubNodeID(central, "connectivity"), ObjectID: safeLower(iface), Payload: buf, OK: true}
}

// ----------------------- System-Health / Latency ------------------

// BuildSystemHealthDiscovery emits a HA `sensor` for the openccu-loom
// system-health score (0–100). The score is published on
// `<base>/<central>/system/health_score`. Mirrors the entry in
// `hubDescriptionsByKind["system_health"]` (entity_descriptions.go:404).
func (d *DefaultDiscoveryBuilder) BuildSystemHealthDiscovery(central string) DiscoveryItem {
	if central == "" {
		return DiscoveryItem{}
	}
	uniqueID := hubAggregateUniqueID(d.serialSuffix(central), "system_health_score")
	topic := d.TopicBuilder.Base + "/" + safeLower(central) + "/system/health_score"
	body := map[string]any{
		"name":                        "System Health Score",
		"unique_id":                   uniqueID,
		"object_id":                   uniqueID,
		"state_topic":                 topic,
		"unit_of_measurement":         "%",
		"state_class":                 "measurement",
		"entity_category":             "diagnostic",
		"icon":                        "mdi:heart-pulse",
		"suggested_display_precision": 1,
		"enabled_by_default":          true,
		"availability":                hubAvailability(d.TopicBuilder),
		"availability_mode":           "all",
		"device":                      hubDeviceBlock(central, d.hubFor(central)),
		"origin":                      BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentSensor), NodeID: hubNodeID(central, "system"), ObjectID: "health_score", Payload: buf, OK: true}
}

// BuildConnectionLatencyDiscovery emits a HA `sensor` (duration, ms) for
// the per-interface round-trip latency. The measurement topic is
// `<base>/<central>/system/latency/<iface>`. Mirrors
// `hubDescriptionsByKind["connection_latency"]` (entity_descriptions.go:409).
func (d *DefaultDiscoveryBuilder) BuildConnectionLatencyDiscovery(central, iface string) DiscoveryItem {
	if central == "" || iface == "" {
		return DiscoveryItem{}
	}
	nodeID := hubNodeID(central, "latency")
	objID := safeLower(iface)
	uniqueID := hubAggregateUniqueID(d.serialSuffix(central), "latency_"+objID)
	topic := d.TopicBuilder.Base + "/" + safeLower(central) + "/system/latency/" + objID
	body := map[string]any{
		"name":                        fmt.Sprintf("Latency %s", iface),
		"unique_id":                   uniqueID,
		"object_id":                   uniqueID,
		"state_topic":                 topic,
		"unit_of_measurement":         "ms",
		"state_class":                 "measurement",
		"entity_category":             "diagnostic",
		"icon":                        "mdi:timer",
		"suggested_display_precision": 1,
		"enabled_by_default":          true,
		"availability":                hubAvailability(d.TopicBuilder),
		"availability_mode":           "all",
		"device":                      hubDeviceBlock(central, d.hubFor(central)),
		"origin":                      BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentSensor), NodeID: nodeID, ObjectID: objID, Payload: buf, OK: true}
}

// ----------------------- System Update ---------------------------

// BuildHubUpdateDiscovery exposes the CCU's firmware-update state as
// a HA `update` entity. The state topic carries a JSON object with
// `installed_version`, `latest_version`, and `in_progress` fields.
// Mirrors the hub update entity (Category=HubUpdate,
// translation_key="update", enabled_default=True).
func (d *DefaultDiscoveryBuilder) BuildHubUpdateDiscovery(central string) DiscoveryItem {
	topic := naming.MQTTHubUpdate(d.BridgeBase, central)
	uniqueID := hubAggregateUniqueID(d.serialSuffix(central), "update")
	body := map[string]any{
		"name":                    "System Update",
		"unique_id":               uniqueID,
		"object_id":               uniqueID,
		"state_topic":             topic,
		"value_template":          "{{ value_json.installed_version }}",
		"latest_version_topic":    topic,
		"latest_version_template": "{{ value_json.latest_version }}",
		"in_progress_template":    "{{ value_json.in_progress }}",
		"entity_category":         "diagnostic",
		"enabled_by_default":      true,
		"availability":            hubAvailability(d.TopicBuilder),
		"availability_mode":       "all",
		"device":                  hubDeviceBlock(central, d.hubFor(central)),
		"origin":                  BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentUpdate), NodeID: hubNodeID(central, "system"), ObjectID: "update", Payload: buf, OK: true}
}

// ----------------------- Bridge plumbing --------------------------

// PublishHubDiscovery is the Bridge-side helper that takes the
// builder output and pushes it to the broker through the same
// dedup-cache path as the per-DP discovery. An [DiscoveryItem] with
// `OK=false` is a quiet no-op — the caller can pipe builder output
// straight in without checking.
func (b *Bridge) PublishHubDiscovery(ctx context.Context, item DiscoveryItem) error {
	if !item.OK || !b.cfg.HADiscoveryEnabled {
		return nil
	}
	return b.publishDiscovery(ctx, item.Component, item.NodeID, item.ObjectID, item.Payload)
}
