// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package audit records user-initiated configuration changes so the SPA can
// render a change history. Persistence to SQLite can be layered on top later
// — the Recorder interface is stable.
package audit

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// Action is the kind of change being recorded. Stable strings so the
// SPA can localise / icon-map them.
type Action string

// Standard actions. New ones can be added without compat impact.
const (
	ActionParamsetWrite     Action = "paramset_write"
	ActionLinkParamsetWrite Action = "link_paramset_write"
	ActionLinkAdd           Action = "link_add"
	ActionLinkRemove        Action = "link_remove"
	ActionLinkUpdate        Action = "link_update"
	// ActionLinkActivate records a "test link at device" probe: the CCU is
	// asked to trigger the receiver as if the sender fired (short/long
	// keypress). It physically actuates the device. DeviceAddress = the
	// receiver device, Peer = the sender channel, Note = short|long.
	ActionLinkActivate   Action = "link_activate"
	ActionScheduleWrite  Action = "schedule_write"
	ActionActiveProfile  Action = "active_profile"
	ActionDataPointWrite Action = "data_point_write"

	// ActionDeviceInstallMode records a targeted pairing window opened
	// for one device. The Entry's DeviceAddress carries the target.
	ActionDeviceInstallMode Action = "device_install_mode"

	// ActionDeviceAssignment records a room / function (Gewerk)
	// assignment change on a device or a single channel. The Entry's
	// DeviceAddress carries the device or channel address; the Note the
	// assigned sets.
	ActionDeviceAssignment Action = "device_assignment"

	// ActionInstallMode records an interface-level pairing window
	// toggled on or off. The Note carries central, interface and
	// duration; DeviceAddress the optional targeted device.
	ActionInstallMode Action = "install_mode"

	// ActionInstallModeLocal records a keyserver-less HmIP LOCAL
	// teach-in (one-device SGTIN whitelist). DeviceAddress carries the
	// SGTIN; the device key is credential material and never recorded.
	ActionInstallModeLocal Action = "install_mode_local"

	// ActionDeviceConfigRestore records a re-transmit of the centrally
	// stored configuration to a device after a factory reset. The
	// Entry's DeviceAddress carries the target.
	ActionDeviceConfigRestore Action = "device_config_restore"

	// ActionDeviceReplace records a guided device replacement: the
	// Entry's DeviceAddress carries the old (replaced, now unpaired)
	// device; the Note the new device address.
	ActionDeviceReplace Action = "device_replace"

	// ActionDeviceSearch records a wired-bus device scan. The Note
	// carries the scanned interface.
	ActionDeviceSearch Action = "device_search"

	// ActionDeviceCommunicationTest records a per-device communication /
	// function test. The Entry's DeviceAddress carries the target.
	ActionDeviceCommunicationTest Action = "device_communication_test"

	// ActionDeviceTeamSet records a channel team assignment (or reset).
	// The Entry's DeviceAddress carries the channel address.
	ActionDeviceTeamSet Action = "device_team_set"

	// ActionRecordingToggle records an operator toggling per-datapoint
	// measurement recording (force on/off) or clearing the override back to
	// the glob policy. The Entry's DeviceAddress carries the channel and the
	// Note carries central/interface/parameter and the new state.
	ActionRecordingToggle Action = "recording_toggle"

	// ActionSystemCCUReboot records an operator-triggered reboot of a CCU
	// host. The Entry's Note carries the target central name.
	ActionSystemCCUReboot Action = "system_ccu_reboot"

	// ActionSystemFirmwareDownload records an operator-triggered CCU
	// firmware download: the CCU fetches a firmware image from a URL onto
	// the central so it can be staged for installation. The Entry's Note
	// carries the target central name and the source URL.
	ActionSystemFirmwareDownload Action = "system_firmware_download"

	// ActionRoomFunction records room / function (Gewerk) entity
	// lifecycle changes (create / rename / delete). The Note carries
	// the operation and target name.
	ActionRoomFunction Action = "room_function"

	// ActionDiagramConfig records create / update / delete of a named
	// multi-series diagram definition (SV03). The Note carries the
	// operation and the diagram name.
	ActionDiagramConfig Action = "diagram_config"

	// ActionProgramDelete records deletion of a CCU program. The Note
	// carries the program name and id.
	ActionProgramDelete Action = "program_delete"

	// ActionTLSCertUpload records a runtime replacement of the daemon's
	// TLS server certificate.
	ActionTLSCertUpload Action = "tls_cert_upload"

	// Matter bridge mutations (per docs/matter-ui-concept.md §6).
	ActionMatterExposureUpdate Action = "matter_exposure_update"
	ActionMatterExposureBulk   Action = "matter_exposure_bulk"
	ActionMatterFabricRevoke   Action = "matter_fabric_revoke"
	ActionMatterCommissioning  Action = "matter_commissioning"
	ActionMatterShare          Action = "matter_share"

	// Visibility surface mutations (docs/ui/unignore-concept.md).
	ActionUnIgnoreUpdate Action = "un_ignore_update"

	// Auth-token lifecycle. The Entry's Note carries
	// `subject=<subject> role=<role> id=<token-id>` so the audit
	// view can render who created or revoked what without exposing
	// the raw bearer token.
	ActionTokenCreate Action = "token_create"
	ActionTokenRevoke Action = "token_revoke"

	// User-management surface (Wave E). Note carries
	// `subject=<subject> role=<role>` so the audit view can render
	// who was created / updated / removed.
	ActionUserCreate Action = "user_create"
	ActionUserUpdate Action = "user_update"
	ActionUserDelete Action = "user_delete"

	// Live-edit config-section mutations (Wave C). Note carries
	// `section=<section> version=<n>`. The Entry's Changes slice
	// captures per-field before/after when the section editor
	// supplies them; section-replace operations leave it empty.
	ActionConfigSectionUpdate Action = "config_section_update"
	ActionConfigSectionDelete Action = "config_section_delete"

	// Central (CCU) CRUD mutations (Wave C). Note carries
	// `name=<central-name>`.
	ActionCentralCreate Action = "central_create"
	ActionCentralUpdate Action = "central_update"
	ActionCentralDelete Action = "central_delete"

	// ActionIncidentsClear records a bulk incident-store clear across
	// every registered central (DELETE /api/v1/incidents; shares the
	// domain call with the WS `incidents.clear` command).
	ActionIncidentsClear Action = "incidents_clear"

	// Alarm-system surface. Command actions (arm / disarm / silence /
	// acknowledge / walk test / output test) record who drove the panel
	// and from where; ActionAlarmConfigChange covers every area / sensor
	// / output CRUD mutation, and ActionAlarmCodeChange every alarm-code
	// CRUD mutation (kept distinct so a code change is auditable apart
	// from ordinary config edits — codes are security material, §11/§16).
	// The Entry's Note carries the target context (e.g.
	// `area=<id> mode=<mode>`).
	ActionAlarmArm          Action = "alarm_arm"
	ActionAlarmDisarm       Action = "alarm_disarm"
	ActionAlarmSilence      Action = "alarm_silence"
	ActionAlarmAcknowledge  Action = "alarm_acknowledge"
	ActionAlarmConfigChange Action = "alarm_config_change"
	ActionAlarmCodeChange   Action = "alarm_code_change"
	ActionAlarmWalkTest     Action = "alarm_walk_test"
	ActionAlarmOutputTest   Action = "alarm_output_test"
)

// Entry is one recorded change. The User field is filled by the REST
// layer from the auth context; everything else by the producer
// (adapter / handler).
type Entry struct {
	Timestamp     time.Time `json:"timestamp"`
	User          string    `json:"user,omitempty"`
	Action        Action    `json:"action"`
	DeviceAddress string    `json:"device_address,omitempty"`
	ChannelNo     int       `json:"channel_no,omitempty"`
	Paramset      string    `json:"paramset,omitempty"`
	Peer          string    `json:"peer,omitempty"`
	Parameter     string    `json:"parameter,omitempty"`
	// Changes lists per-parameter before/after pairs for paramset
	// writes. Empty for boolean actions like link_remove.
	Changes []Change `json:"changes,omitempty"`
	// Note carries free-form context (e.g. profile id when an
	// active_profile change happened).
	Note string `json:"note,omitempty"`
}

// Query is a filtered, paginated audit read request used by the
// durable read path (SQLite). Zero-value time bounds mean "unbounded";
// Limit <= 0 lets the store apply its own cap. Offset enables
// page-by-page retrieval over the full history rather than a fixed
// in-memory window.
type Query struct {
	Device string    // device-address prefix (case-insensitive)
	Since  time.Time // inclusive lower bound (zero = no bound)
	Until  time.Time // exclusive upper bound (zero = no bound)
	Limit  int
	Offset int
}

// Change is one parameter delta within an Entry.
type Change struct {
	Parameter string `json:"parameter"`
	Before    any    `json:"before,omitempty"`
	After     any    `json:"after,omitempty"`
}

// Recorder is the interface domain code uses to push entries. The
// daemon wires a single concrete buffer; tests substitute a noop.
type Recorder interface {
	Record(entry Entry)
	List(limit int) []Entry
}

// Buffer is a thread-safe ring buffer of Entry. Head is most-recent.
// Capacity defaults to 500; uses the same number.
type Buffer struct {
	mu      sync.RWMutex
	entries []Entry
	cap     int
	clk     clock.Clock
}

// NewBuffer returns a buffer with the given capacity (>= 1) using the
// real wall clock. Use [NewBufferWithClock] when timestamps need to be
// deterministic for tests.
func NewBuffer(capacity int) *Buffer {
	return NewBufferWithClock(capacity, clock.New())
}

// NewBufferWithClock returns a buffer that takes its timestamps from
// clk. Pass a [clock.Fake] in tests so audit timestamps are stable
// against wall-clock drift; in production use [NewBuffer]. A nil clk
// falls back to [clock.New].
func NewBufferWithClock(capacity int, clk clock.Clock) *Buffer {
	if capacity < 1 {
		capacity = 500
	}
	if clk == nil {
		clk = clock.New()
	}
	return &Buffer{cap: capacity, clk: clk}
}

// Record stores e. The newest entry is at index 0; older entries are
// pushed back; entries beyond capacity are dropped.
func (b *Buffer) Record(e Entry) {
	if e.Timestamp.IsZero() {
		e.Timestamp = b.clk.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append([]Entry{e}, b.entries...)
	if len(b.entries) > b.cap {
		b.entries = b.entries[:b.cap]
	}
}

// List returns up to `limit` most-recent entries (or all when limit
// <= 0). The result is a snapshot — safe to mutate.
func (b *Buffer) List(limit int) []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > len(b.entries) {
		limit = len(b.entries)
	}
	out := make([]Entry, limit)
	copy(out, b.entries[:limit])
	return out
}

// Len reports the current entry count.
func (b *Buffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.entries)
}

// noopRecorder swallows entries — useful as a default when audit is
// disabled, so call sites can stay nil-safe.
type noopRecorder struct{}

// NoopRecorder returns a Recorder that drops every entry.
func NoopRecorder() Recorder { return noopRecorder{} }

func (noopRecorder) Record(Entry)     {}
func (noopRecorder) List(int) []Entry { return nil }
