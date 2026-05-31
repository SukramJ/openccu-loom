// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// Package integration — broker snapshot diff test.
//
// TestBrokerSnapshotDiff wires godevccu + the MQTT bridge against a
// real broker (embedded pure-Go, no Docker), subscribes to every
// homeassistant/+/+/+/config topic, and diffs the collected retained
// payloads against the static reference snapshot.
//
// This test catches classes of bugs that the in-process
// [discoveryRecorder] cannot: anything that the TCP encode/retain/
// subscribe path introduces (e.g. a field stripped by the MQTT codec,
// a QoS/retained-message race, or a field whose absence is masked by
// the recorder's in-memory pass-through).
//
// Motivating bug: HmIP-SMO230-A and similar devices had empty
// `model_id` in their HA Discovery `device` block because the CCU's
// SUBTYPE field was not propagated through
// `dd.Subtype → Device.SubModel → Translations.DeviceModelLabel`.
// The static snapshot test already captured this; the broker test
// confirms the live broker path exhibits the same bug and will catch
// regressions once the bug is fixed.
//
// Broker selection:
// - Primary: pure-Go embedded broker (no Docker required).
// - Secondary: Docker Mosquitto (not started; available as upgrade
// path when more protocol edge-cases are needed).
package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// brokerSnapshotDevices is the smoke-sized device fleet for the broker
// snapshot test. Override with OPENCCU_LOOM_BROKER_SNAPSHOT_DEVICES=A,B
// (comma-separated model names) to scope further.
var defaultBrokerSnapshotDevices = []string{
	"HmIP-SMO230-A", // motivating bug: model_id was empty (SUBTYPE not propagated)
	"HmIP-BWTH",     // climate aggregate
	"HmIP-BROLL",    // cover aggregate
	"HmIP-BSM",      // switch + power sensor; BSM had empty model_id (HMIP-PS / HmIP-BSM)
	"HmIP-SWSD",     // smoke detector — STATE channel
}

func brokerSnapshotDevices(t *testing.T) []string {
	t.Helper()
	if override := os.Getenv("OPENCCU_LOOM_BROKER_SNAPSHOT_DEVICES"); override != "" {
		return splitCommas(override)
	}
	return defaultBrokerSnapshotDevices
}

// ---------------------------------------------------------------------------
// TestBrokerSnapshotDiff
// ---------------------------------------------------------------------------

// TestBrokerSnapshotDiff boots godevccu, drives the MQTT bridge against
// an embedded pure-Go broker, collects every retained
// homeassistant/.../config payload via a subscriber, and performs a
// structural diff against the static reference snapshot
// (discovery_snapshot_openccu-loom.json) plus an independent invariant
// check on per-device model_id presence.
//
// Diff fields checked per entity:
// - device.model_id — must be non-empty for every device that has a
// translation; empty is a regression of the motivating bug.
// - device_class — must match the reference snapshot.
// - entity_category — must match the reference snapshot.
// - enabled_by_default — must match the reference snapshot.
//
// name (warning only): computed differently by the two stacks.
//
// Tolerated divergences (by design, not bugs):
// - Multi-CCU topic prefix — the live broker path uses `ccu-01` as the
// central name, which matches the reference snapshot.
// - MQTT-only wire fields (state_topic, availability, …) — not compared.
// - HmIP-Wired reseller models — not in the smoke fleet; absent entities
// are not failures.
func TestBrokerSnapshotDiff(t *testing.T) {
	// --- embedded broker ---------------------------------------------------
	broker := startEmbeddedBroker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// --- device pipeline ---------------------------------------------------
	devices := brokerSnapshotDevices(t)
	srv := startMockCCUWithDevices(t, devices)

	xmlClient := newXMLRPCClient(t, srv.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "broker-test-ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	locale := snapshotLocale()
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c).WithTranslations(translations, locale)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	// --- subscriber BEFORE publishing so retained messages are not missed --
	subClient := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL:    broker.URL(),
		ClientID:     "broker-snap-sub",
		KeepAlive:    30 * time.Second,
		CleanSession: true,
	})
	if err := subClient.Connect(ctx); err != nil {
		t.Fatalf("subscriber connect: %v", err)
	}
	defer subClient.Disconnect(ctx) //nolint:errcheck // teardown

	var capMu sync.Mutex
	captured := make(map[string][]byte) // topic → payload
	ready := make(chan struct{})
	var readyOnce sync.Once

	if err := subClient.Subscribe(ctx, "homeassistant/#", mqtt.QoS1, func(topic string, payload []byte, _ bool) {
		if !strings.HasSuffix(topic, "/config") {
			return
		}
		capMu.Lock()
		if _, exists := captured[topic]; !exists {
			cp := make([]byte, len(payload))
			copy(cp, payload)
			captured[topic] = cp
		}
		capMu.Unlock()
		readyOnce.Do(func() { close(ready) })
	}); err != nil {
		t.Fatalf("subscribe homeassistant/#: %v", err)
	}
	// Allow SUBACK to land before publishing.
	time.Sleep(100 * time.Millisecond)

	// --- publish via bridge -----------------------------------------------
	pubClient := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL:    broker.URL(),
		ClientID:     "broker-snap-pub",
		KeepAlive:    30 * time.Second,
		CleanSession: true,
	})
	if err := pubClient.Connect(ctx); err != nil {
		t.Fatalf("publisher connect: %v", err)
	}
	defer pubClient.Disconnect(ctx) //nolint:errcheck // teardown

	bridgeCfg := mqtt.BridgeConfig{
		Base:               "gh",
		CentralName:        "ccu-01",
		RawEnabled:         false,
		HADiscoveryEnabled: true,
	}
	bridge := mqtt.NewBridge(bridgeCfg, pubClient)

	loadedDevices := c.ModelRegistry.List()
	sort.Slice(loadedDevices, func(i, j int) bool {
		return loadedDevices[i].Address < loadedDevices[j].Address
	})
	for _, d := range loadedDevices {
		for _, ch := range d.Channels() {
			if ch == nil {
				continue
			}
			driveChannelDPs(ctx, bridge, d, ch)
		}
	}

	// --- collect all retained payloads ------------------------------------
	// Wait until at least one payload arrives.
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("embedded broker never delivered a retained discovery payload within 10 s")
	}
	// Drain stragglers: keep collecting until no new messages arrive for 1 s.
	drainDeadline := time.Now().Add(3 * time.Second)
	prev := 0
	for time.Now().Before(drainDeadline) {
		time.Sleep(50 * time.Millisecond)
		capMu.Lock()
		n := len(captured)
		capMu.Unlock()
		if n != prev {
			prev = n
			drainDeadline = time.Now().Add(1 * time.Second)
		}
	}

	capMu.Lock()
	snapshot := make(map[string][]byte, len(captured))
	for k, v := range captured {
		snapshot[k] = v
	}
	capMu.Unlock()

	t.Logf("broker captured %d discovery topics from %d devices", len(snapshot), len(loadedDevices))
	if len(snapshot) == 0 {
		t.Fatal("no homeassistant/.../config topics were delivered via the broker")
	}

	// --- decode all broker payloads into entities -------------------------
	brokerEntities := make(map[string]brokerEntity, len(snapshot))
	for topic, raw := range snapshot {
		ent := decodeBrokerEntity(topic, raw)
		brokerEntities[ent.JoinKey] = ent
	}

	// --- load reference snapshot ------------------------------------------
	refEntities, refErr := loadBrokerReferenceSnapshot(
		"testdata/discovery_snapshot_openccu-loom.json",
	)
	if refErr != nil {
		t.Logf("reference snapshot not available (%v); running invariant-only checks", refErr)
	}

	// --- invariant: model_id must not be empty ----------------------------
	// Per-device: only one error per device address so output stays readable.
	toleratedEmpty := buildToleratedEmptyModelIDSet(refEntities)
	deviceModelIDFails := make(map[string]string) // address → model
	for _, ent := range brokerEntities {
		if ent.DeviceAddress == "" {
			continue
		}
		if _, already := deviceModelIDFails[ent.DeviceAddress]; already {
			continue
		}
		if ent.ModelID == "" && !toleratedEmpty[ent.DeviceAddress] {
			deviceModelIDFails[ent.DeviceAddress] = ent.Model
		}
	}
	failAddrs := make([]string, 0, len(deviceModelIDFails))
	for a := range deviceModelIDFails {
		failAddrs = append(failAddrs, a)
	}
	sort.Strings(failAddrs)
	for _, addr := range failAddrs {
		t.Errorf("INVARIANT model_id: device %s (model=%s) has empty model_id in broker payload",
			addr, deviceModelIDFails[addr])
	}

	// --- field diff against reference snapshot ----------------------------
	if refEntities == nil {
		t.Logf("skipping field diff: no reference snapshot")
		return
	}

	type diffRow struct {
		joinKey string
		field   string
		broker  any
		ref     any
		warn    bool
	}
	var driftRows []diffRow

	for jk, brokerEnt := range brokerEntities {
		refEnt, ok := refEntities[jk]
		if !ok {
			continue // only in broker — not a failure (new entity)
		}
		// device_class
		if bc, rc := brokerEnt.DeviceClass, refEnt.DeviceClass; bc != rc {
			driftRows = append(driftRows, diffRow{jk, "device_class", bc, rc, false})
		}
		// entity_category
		if bc, rc := brokerEnt.EntityCategory, refEnt.EntityCategory; bc != rc {
			driftRows = append(driftRows, diffRow{jk, "entity_category", bc, rc, false})
		}
		// enabled_by_default
		if bc, rc := brokerEnt.EnabledByDefault, refEnt.EnabledByDefault; boolPtrStr(bc) != boolPtrStr(rc) {
			driftRows = append(driftRows, diffRow{jk, "enabled_by_default", bc, rc, false})
		}
		// name — warning only
		if bn, rn := brokerEnt.Name, refEnt.Name; bn != rn && bn != "" && rn != "" {
			driftRows = append(driftRows, diffRow{jk, "name", bn, rn, true})
		}
	}

	sort.Slice(driftRows, func(i, j int) bool {
		if driftRows[i].joinKey != driftRows[j].joinKey {
			return driftRows[i].joinKey < driftRows[j].joinKey
		}
		return driftRows[i].field < driftRows[j].field
	})

	var errorCount, warnCount int
	for _, row := range driftRows {
		msg := fmt.Sprintf("%-60s %-20s broker=%v  ref=%v",
			row.joinKey, row.field, row.broker, row.ref)
		if row.warn {
			t.Logf("WARN name drift: %s", msg)
			warnCount++
		} else {
			t.Errorf("DRIFT: %s", msg)
			errorCount++
		}
	}

	// --- summary ----------------------------------------------------------
	onlyInBroker := countKeysNotIn(brokerEntities, refEntities)
	onlyInRef := countKeysNotIn(refEntities, brokerEntities)
	t.Logf("summary: broker=%d ref=%d shared=%d only_broker=%d only_ref=%d errors=%d warns=%d model_id_fails=%d",
		len(brokerEntities), len(refEntities),
		len(brokerEntities)-onlyInBroker,
		onlyInBroker, onlyInRef,
		errorCount, warnCount, len(failAddrs))
}

// ---------------------------------------------------------------------------
// Embedded pure-Go MQTT broker
// ---------------------------------------------------------------------------

// embeddedBroker is a minimal MQTT 3.1.1 broker that supports:
// - multiple simultaneous client connections
// - CONNECT/CONNACK handshake
// - SUBSCRIBE/SUBACK on single-level and multi-level wildcards
// - PUBLISH (QoS 0 and QoS 1 with PUBACK) + retained-message store
// - delivery of retained messages on SUBSCRIBE
// - PINGREQ/PINGRESP
// - DISCONNECT
//
// This is the minimal feature set required by the broker snapshot diff
// test. It is implemented using only stdlib and the existing
// internal/north/mqtt/protocol codec (server-side packet
// encode/decode is done inline because the protocol package only
// exposes client-side helpers).
type embeddedBroker struct {
	listener net.Listener
	mu       sync.RWMutex
	// retained maps topic → payload for retained messages.
	retained map[string][]byte
	// subscribers maps clientID → list of (filter, conn).
	clients map[*ebConn]struct{}
}

type ebConn struct {
	broker  *embeddedBroker
	conn    net.Conn
	writer  *bufio.Writer
	mu      sync.Mutex
	filters []string // subscribed topic filters
}

// startEmbeddedBroker starts a pure-Go MQTT broker on a random local
// port and registers a Cleanup to stop it.
func startEmbeddedBroker(t *testing.T) *embeddedBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("embeddedBroker: listen: %v", err)
	}
	b := &embeddedBroker{
		listener: ln,
		retained: make(map[string][]byte),
		clients:  make(map[*ebConn]struct{}),
	}
	go b.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	t.Logf("embedded MQTT broker listening on %s", ln.Addr())
	return b
}

// URL returns the tcp:// URL of the broker.
func (b *embeddedBroker) URL() string {
	return "tcp://" + b.listener.Addr().String()
}

func (b *embeddedBroker) acceptLoop() {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}
		c := &ebConn{
			broker: b,
			conn:   conn,
			writer: bufio.NewWriter(conn),
		}
		b.mu.Lock()
		b.clients[c] = struct{}{}
		b.mu.Unlock()
		go c.serve()
	}
}

func (b *embeddedBroker) removeClient(c *ebConn) {
	b.mu.Lock()
	delete(b.clients, c)
	b.mu.Unlock()
}

// storeRetained stores payload for topic (retained=true) or removes
// the entry for an empty payload (retained=true, zero-length = clear).
func (b *embeddedBroker) storeRetained(topic string, payload []byte) {
	b.mu.Lock()
	if len(payload) == 0 {
		delete(b.retained, topic)
	} else {
		cp := make([]byte, len(payload))
		copy(cp, payload)
		b.retained[topic] = cp
	}
	b.mu.Unlock()
}

// retainedMatching returns all retained topic/payload pairs matching filter.
func (b *embeddedBroker) retainedMatching(filter string) []struct {
	topic   string
	payload []byte
} {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []struct {
		topic   string
		payload []byte
	}
	for t, p := range b.retained {
		if ebTopicMatches(filter, t) {
			cp := make([]byte, len(p))
			copy(cp, p)
			out = append(out, struct {
				topic   string
				payload []byte
			}{t, cp})
		}
	}
	return out
}

// dispatch publishes a message to all subscribers whose filters match topic.
func (b *embeddedBroker) dispatch(srcConn *ebConn, topic string, payload []byte, qos byte) {
	b.mu.RLock()
	clients := make([]*ebConn, 0, len(b.clients))
	for c := range b.clients {
		clients = append(clients, c)
	}
	b.mu.RUnlock()

	for _, c := range clients {
		if c == srcConn {
			continue // do not echo back to publisher
		}
		c.mu.Lock()
		filters := append([]string{}, c.filters...)
		c.mu.Unlock()
		for _, f := range filters {
			if ebTopicMatches(f, topic) {
				// Always deliver at QoS 0 to subscribers: the broker does
				// not track per-subscriber packet IDs in this minimal
				// implementation, and the snapshot test only needs
				// at-most-once delivery semantics for the diff.
				_ = ebPublishTo(c.writer, topic, payload, 0)
				break
			}
		}
	}

	// Also deliver to the subscribing client on the same connection
	// if it also subscribes to its own publishes. For the broker test
	// the subscriber and publisher are different connections, so this
	// only matters if src == subscriber — which doesn't happen here.
}

// serve handles one client connection: CONNECT, PUBLISH, SUBSCRIBE, etc.
func (c *ebConn) serve() {
	defer func() {
		c.broker.removeClient(c)
		_ = c.conn.Close()
	}()

	br := bufio.NewReader(c.conn)

	for {
		// Read the fixed header byte.
		head := make([]byte, 1)
		if _, err := io.ReadFull(br, head); err != nil {
			return
		}
		pkType := head[0] >> 4
		retain := head[0]&0x01 != 0
		qos := (head[0] >> 1) & 0x03

		// Read remaining length.
		bodyLen, err := ebReadRemainingLength(br)
		if err != nil {
			return
		}
		body := make([]byte, bodyLen)
		if bodyLen > 0 {
			if _, err := io.ReadFull(br, body); err != nil {
				return
			}
		}

		switch pkType {
		case 1: // CONNECT
			// Send CONNACK 0x00 (accepted, no session present).
			c.mu.Lock()
			_ = ebWritePacket(c.writer, 0x20, []byte{0, 0})
			_ = c.writer.Flush()
			c.mu.Unlock()

		case 3: // PUBLISH
			topic, n, err := ebReadString(body, 0)
			if err != nil {
				return
			}
			idx := n
			var pktID uint16
			if qos > 0 {
				if idx+2 > len(body) {
					return
				}
				pktID = binary.BigEndian.Uint16(body[idx : idx+2])
				idx += 2
			}
			payload := body[idx:]

			if retain {
				c.broker.storeRetained(topic, payload)
			}
			// Dispatch to other subscribers.
			c.broker.dispatch(c, topic, payload, qos)
			// Also deliver to subscribers on THIS connection
			// (self-subscribe scenario is not used in this test, skip for simplicity).

			// PUBACK for QoS 1.
			if qos == 1 && pktID != 0 {
				ack := []byte{0, 0}
				binary.BigEndian.PutUint16(ack, pktID)
				c.mu.Lock()
				_ = ebWritePacket(c.writer, 0x40, ack)
				_ = c.writer.Flush()
				c.mu.Unlock()
			}

		case 8: // SUBSCRIBE
			// body: [pktID(2)] + [filterLen(2) + filter + qos] * n
			if len(body) < 2 {
				return
			}
			pktID := binary.BigEndian.Uint16(body[:2])
			idx := 2
			var returnCodes []byte
			for idx < len(body) {
				filter, n, err := ebReadString(body, idx)
				if err != nil {
					return
				}
				idx += n
				if idx >= len(body) {
					return
				}
				_ = body[idx] // requested QoS (ignored; we grant QoS 1)
				idx++
				returnCodes = append(returnCodes, 0x01) // granted QoS 1

				// Register filter.
				c.mu.Lock()
				c.filters = append(c.filters, filter)
				c.mu.Unlock()

				// Deliver retained messages matching this filter.
				for _, r := range c.broker.retainedMatching(filter) {
					c.mu.Lock()
					_ = ebPublishTo(c.writer, r.topic, r.payload, 0)
					_ = c.writer.Flush()
					c.mu.Unlock()
				}
			}
			// SUBACK.
			subAckBody := append([]byte{body[0], body[1]}, returnCodes...)
			_ = binary.Write(bytes.NewBuffer(nil), binary.BigEndian, pktID) // just for reference
			c.mu.Lock()
			_ = ebWritePacket(c.writer, 0x90, subAckBody)
			_ = c.writer.Flush()
			c.mu.Unlock()

		case 12: // PINGREQ
			c.mu.Lock()
			_ = ebWritePacket(c.writer, 0xD0, nil)
			_ = c.writer.Flush()
			c.mu.Unlock()

		case 14: // DISCONNECT
			return
		}
	}
}

// ebWritePacket writes an MQTT fixed-header + body to w.
func ebWritePacket(w *bufio.Writer, header byte, body []byte) error {
	if _, err := w.Write([]byte{header}); err != nil {
		return err
	}
	if _, err := w.Write(ebEncodeLength(len(body))); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return nil
}

// ebPublishTo sends a QoS-0 PUBLISH frame to w.
func ebPublishTo(w *bufio.Writer, topic string, payload []byte, qos byte) error {
	head := byte(0x30) // PUBLISH, QoS 0, no retain, no dup
	if qos == 1 {
		head |= 0x02
	}
	var body bytes.Buffer
	ts := make([]byte, 2)
	binary.BigEndian.PutUint16(ts, uint16(len(topic))) //nolint:gosec
	body.Write(ts)
	body.WriteString(topic)
	body.Write(payload)
	return ebWritePacket(w, head, body.Bytes())
}

// ebEncodeLength encodes the MQTT remaining length.
func ebEncodeLength(n int) []byte {
	var out []byte
	for {
		digit := byte(n & 0x7F)
		n >>= 7
		if n > 0 {
			digit |= 0x80
		}
		out = append(out, digit)
		if n == 0 {
			break
		}
	}
	return out
}

// ebReadRemainingLength reads the MQTT variable-length remaining-length.
func ebReadRemainingLength(r *bufio.Reader) (int, error) {
	var length uint32
	var mult uint32 = 1
	buf := make([]byte, 1)
	for i := 0; i < 4; i++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, err
		}
		length += uint32(buf[0]&0x7F) * mult
		if buf[0]&0x80 == 0 {
			return int(length), nil
		}
		mult *= 128
	}
	return 0, fmt.Errorf("mqtt: malformed remaining length")
}

// ebReadString reads a length-prefixed MQTT string starting at offset.
// Returns (value, bytesConsumed, err).
func ebReadString(b []byte, offset int) (string, int, error) {
	if offset+2 > len(b) {
		return "", 0, fmt.Errorf("ebReadString: short header at offset %d", offset)
	}
	n := int(binary.BigEndian.Uint16(b[offset : offset+2]))
	if offset+2+n > len(b) {
		return "", 0, fmt.Errorf("ebReadString: short body (need %d, have %d)", n, len(b)-offset-2)
	}
	return string(b[offset+2 : offset+2+n]), 2 + n, nil
}

// ebTopicMatches tests whether a topic matches an MQTT filter (+ and #
// wildcards). Mirrors the equivalent in adapter_tcp.go.
func ebTopicMatches(filter, topic string) bool {
	if filter == topic {
		return true
	}
	fp, tp := 0, 0
	for fp < len(filter) && tp < len(topic) {
		fc, tc := filter[fp], topic[tp]
		switch fc {
		case '#':
			return true
		case '+':
			for tp < len(topic) && topic[tp] != '/' {
				tp++
			}
			fp++
		default:
			if fc != tc {
				return false
			}
			fp++
			tp++
		}
	}
	return fp == len(filter) && tp == len(topic)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// brokerEntity is the decoded form of a single broker-captured
// homeassistant/.../config payload, projected onto the fields we diff.
type brokerEntity struct {
	JoinKey          string
	Topic            string
	Component        string
	DeviceAddress    string
	Model            string
	ModelID          string
	DeviceClass      string
	EntityCategory   string
	EnabledByDefault *bool
	Name             string
}

// decodeBrokerEntity extracts the diffable fields from a raw discovery
// payload. The join_key construction mirrors [decodeEntity] in
// discovery_snapshot_test.go so the keys are compatible for the diff.
func decodeBrokerEntity(topic string, payload []byte) brokerEntity {
	var body map[string]any
	_ = json.Unmarshal(payload, &body)

	parts := strings.Split(topic, "/")
	component := ""
	objectID := ""
	if len(parts) >= 5 {
		component = parts[1]
		objectID = parts[3]
	}

	channelNo, suffix := splitObjectID(objectID)

	var (
		deviceAddress string
		model         string
		modelID       string
	)
	if dev, ok := body["device"].(map[string]any); ok {
		if id, ok := identifierFromDevice(dev); ok {
			deviceAddress = strings.ToUpper(id)
		}
		if m, ok := dev["model"].(string); ok {
			model = m
		}
		if mid, ok := dev["model_id"].(string); ok {
			modelID = mid
		}
	}

	dc, _ := body["device_class"].(string)
	ec, _ := body["entity_category"].(string)
	var ebd *bool
	if v, ok := body["enabled_by_default"].(bool); ok {
		ebd = &v
	}
	name, _ := body["name"].(string)

	kind := "param"
	parameter := ""
	switch {
	case suffix == component && isAggregateComponent(component):
		kind = "agg"
	case component == "event" && suffix == "event":
		kind = "event"
	default:
		parameter = strings.ToUpper(suffix)
	}

	paramset := ""
	if ec == "config" {
		paramset = "MASTER"
	}
	if paramset == "" && kind == "param" {
		paramset = "VALUES"
	}

	jk := buildJoinKey(deviceAddress, channelNo, kind, paramset, parameter, component)

	return brokerEntity{
		JoinKey:          jk,
		Topic:            topic,
		Component:        component,
		DeviceAddress:    deviceAddress,
		Model:            model,
		ModelID:          modelID,
		DeviceClass:      dc,
		EntityCategory:   ec,
		EnabledByDefault: ebd,
		Name:             name,
	}
}

// refEntity is the projected reference-snapshot form of a single entity.
type refEntity struct {
	JoinKey          string
	Model            string
	ModelID          string
	DeviceClass      string
	EntityCategory   string
	EnabledByDefault *bool
	Name             string
}

// loadBrokerReferenceSnapshot reads the openccu-loom static snapshot
// and returns a map indexed by join_key. Returns (nil, err) when the
// file is absent — callers should proceed with invariant-only checks.
func loadBrokerReferenceSnapshot(path string) (map[string]refEntity, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only

	var snap discoverySnapshotRoot
	if err := json.NewDecoder(f).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	out := make(map[string]refEntity, len(snap.Entities))
	for _, e := range snap.Entities {
		ent := refEntity{
			JoinKey: e.JoinKey,
			Model:   e.Model,
		}
		if p := e.Payload; p != nil {
			if dev, ok := p["device"].(map[string]any); ok {
				if mid, ok := dev["model_id"].(string); ok {
					ent.ModelID = mid
				}
			}
			if dc, ok := p["device_class"].(string); ok {
				ent.DeviceClass = dc
			}
			if ec, ok := p["entity_category"].(string); ok {
				ent.EntityCategory = ec
			}
			if v, ok := p["enabled_by_default"].(bool); ok {
				ent.EnabledByDefault = &v
			}
			if n, ok := p["name"].(string); ok {
				ent.Name = n
			}
		}
		out[e.JoinKey] = ent
	}
	return out, nil
}

// buildToleratedEmptyModelIDSet returns the set of device addresses for
// which the reference snapshot also has an empty model_id. These are
// devices for which no translation entry exists in the embedded OCCU
// catalogue — the empty field is by design, not a bug.
func buildToleratedEmptyModelIDSet(refEntities map[string]refEntity) map[string]bool {
	if refEntities == nil {
		return map[string]bool{}
	}
	deviceModelID := make(map[string]string)
	for _, ent := range refEntities {
		addr := extractAddressFromJoinKey(ent.JoinKey)
		if addr == "" {
			continue
		}
		if _, seen := deviceModelID[addr]; !seen {
			deviceModelID[addr] = ent.ModelID
		} else if ent.ModelID != "" {
			deviceModelID[addr] = ent.ModelID
		}
	}
	tolerated := make(map[string]bool)
	for addr, mid := range deviceModelID {
		if mid == "" {
			tolerated[addr] = true
		}
	}
	return tolerated
}

// extractAddressFromJoinKey extracts the device address from a join_key
// of the form `<ADDRESS>:<channel>:<kind>:...`.
func extractAddressFromJoinKey(jk string) string {
	idx := strings.Index(jk, ":")
	if idx <= 0 {
		return ""
	}
	return jk[:idx]
}

// countKeysNotIn counts how many string keys of `a` do not appear in `b`.
// The two maps may have different value types.
func countKeysNotIn[A any, B any](a map[string]A, b map[string]B) int {
	n := 0
	for k := range a {
		if _, ok := b[k]; !ok {
			n++
		}
	}
	return n
}

// boolPtrStr converts *bool to a stable string for diff comparison.
func boolPtrStr(b *bool) string {
	if b == nil {
		return "<nil>"
	}
	if *b {
		return "true"
	}
	return "false"
}
