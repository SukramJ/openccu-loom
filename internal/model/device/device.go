// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Config is the construction record for a Device.
type Config struct {
	InterfaceID  string
	Interface    hmenum.Interface
	Address      string
	Model        string
	SubModel     string
	Name         string
	Manufacturer hmenum.Manufacturer
	ProductGroup hmenum.ProductGroup
	Rooms        []string
	Functions    []string
	RxModes      []hmenum.CommandRxMode
	Updatable    bool
	Firmware     FirmwareInfo

	// IseID is the CCU-internal numeric identifier for the device. Set by the
	// ingest pipeline from the Rega script device-details response
	// (`get_address_id`).
	IseID int

	// IgnoreForCustomDataPoint signals that the device model is explicitly
	// listed in the parameter-visibility provider's "ignore for custom DP" set.
	// When true, the custom-DP materialisation pass skips this device even if a
	// profile exists.
	IgnoreForCustomDataPoint bool

	// HasCustomDataPointDefinition marks that a registered custom-DP profile
	// exists for this device model AND the device is not in the ignore set.
	// Drives the "suppress generic DPs when a custom DP owns the channel" pass.
	HasCustomDataPointDefinition bool

	// IgnoreOnInitialLoad marks that the device model is in the "skip initial
	// value poll" list.
	IgnoreOnInitialLoad bool

	// SchemaVersion is the CCU-internal device-description schema version (the
	// wire `VERSION` integer field from `listDevices`). It is bumped by the CCU
	// whenever a device's paramset layout changes.
	SchemaVersion int
}

// Device is the runtime aggregate of a physical Homematic device:
// identity, channels, firmware, and derived availability. It is
// deliberately thinner than the Python reference — the wire layer
// and value-cache concerns live elsewhere (client, store).
type Device struct {
	InterfaceID  string              `payload:"info"`
	Interface    hmenum.Interface    `payload:"info"`
	Address      string              `payload:"info,alt=serial_number"`
	Model        string              `payload:"info"`
	ModelLabel   string              `payload:"info,alt=model_label"`
	ModelIcon    string              `payload:"info,alt=model_icon"`
	SubModel     string              `payload:"info,alt=sub_model"`
	Name         string              `payload:"info"`
	Manufacturer hmenum.Manufacturer `payload:"info"`
	ProductGroup hmenum.ProductGroup `payload:"info,alt=product_group"`
	Rooms        []string            `payload:"info"`
	Functions    []string            `payload:"info"`
	// Room is the device's single canonical room assignment. Set only when
	// [Rooms] contains exactly one entry; for zero or multi-room assignments it
	// stays empty. Drives MQTT-Discovery's `suggested_area` (HA accepts only a
	// single string) and any north-bound consumer that wants the unambiguous
	// case without re-implementing the slice-collapsing logic.
	Room string `payload:"info"`
	// Function is the device's single canonical function (Gewerk)
	// assignment, set under the same one-entry rule as [Room].
	Function  string                 `payload:"info"`
	RxModes   []hmenum.CommandRxMode `payload:"config,alt=rx_modes"`
	Updatable bool                   `payload:"config"`

	// IseID is the CCU-internal numeric identifier for this device. Set by the
	// ingest pipeline from the Rega script device-details response.
	IseID int `payload:"info,alt=ise_id"`

	// SchemaVersion is the CCU-internal device-description schema version (the
	// wire `VERSION` integer from `listDevices`). Populated during ingest from
	// DeviceDescription.Version.
	SchemaVersion int `payload:"info,alt=schema_version"`

	// IgnoreForCustomDataPoint signals that the device model is explicitly
	// listed in the parameter-visibility provider's ignore set. When true,
	// custom-DP materialisation is skipped.
	IgnoreForCustomDataPoint bool

	// HasCustomDataPointDefinition marks that a registered custom-DP profile
	// exists for this device model and the device is NOT in the ignore set.
	// Drives the "suppress generic DPs" pass.
	HasCustomDataPointDefinition bool

	// IgnoreOnInitialLoad marks that the device model is in the "skip initial
	// value poll" list.
	IgnoreOnInitialLoad bool

	firmware     *Firmware
	availability *Availability
	update       *Update

	mu       sync.RWMutex
	channels map[string]*Channel

	removedMu       sync.Mutex
	removedHandlers []func()

	updatedMu       sync.Mutex
	updatedHandlers []func()

	groupMu       sync.RWMutex
	groupChannels map[int]map[int]struct{}
	// channelToGroup is the reverse lookup: channel number → group number.
	// Populated in parallel with groupChannels by [AddChannelToGroup].
	channelToGroup map[int]int

	// loaderMu guards loader + cache. Both are installed via
	// [Device.SetValueLoader] after the device pipeline has wired the
	// south-bound backend; nil means "no on-demand value loading"
	// (typical in test fixtures and pre-bootstrap state).
	loaderMu sync.RWMutex
	loader   ValueLoader
	cache    *valueCache

	// rootChannel is the synthetic device-level channel that owns
	// MASTER parameters living on the device address itself (no
	// `:N` suffix).
	// Created lazily on first call to [Device.RootChannel] / by the
	// pipeline during ingest. Stored separately from [channels] so
	// adapter loops over real channels do not have to filter it out.
	rootMu      sync.RWMutex
	rootChannel *Channel

	// behaviorMu guards the per-central custom-DP rendering toggles.
	// They are stamped once by the device pipeline before custom-DP
	// materialisation and read by the light / cover factories. Both
	// default to true (see [New]) so an unstamped device — every test
	// fixture, any pre-pipeline state — keeps the historical behavior.
	behaviorMu                   sync.RWMutex
	lightLastBrightness          bool
	useGroupChannelForCoverState bool

	// scheduleSwitchesMu guards scheduleChannelSwitches.
	scheduleSwitchesMu sync.RWMutex
	// scheduleChannelSwitches is the set of per-channel boolean DPs that control
	// schedule participation. Set via [SetScheduleChannelSwitches] by the
	// weekprofile wiring code.
	scheduleChannelSwitches []*weekprofile.ChannelSwitch
}

// singleOrEmpty collapses a multi-value assignment slice into a
// single canonical string when exactly one entry is present, and to
// the empty string otherwise. Used to derive [Device.Room]
// [Device.Function] from the broader [Rooms] / [Functions] slices
// HA Discovery's `suggested_area` only accepts a single string, and
// surfacing the multi-room ambiguity there would mis-attribute
// devices that span rooms (e.g. a multi-channel actor wired into a
// hallway and the kitchen simultaneously).
func singleOrEmpty(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	return ""
}

// New constructs a Device.
func New(cfg Config) *Device {
	d := &Device{
		InterfaceID:                  cfg.InterfaceID,
		Interface:                    cfg.Interface,
		Address:                      cfg.Address,
		Model:                        cfg.Model,
		SubModel:                     cfg.SubModel,
		Name:                         cfg.Name,
		Manufacturer:                 cfg.Manufacturer,
		ProductGroup:                 cfg.ProductGroup,
		Rooms:                        append([]string(nil), cfg.Rooms...),
		Functions:                    append([]string(nil), cfg.Functions...),
		Room:                         singleOrEmpty(cfg.Rooms),
		Function:                     singleOrEmpty(cfg.Functions),
		RxModes:                      append([]hmenum.CommandRxMode(nil), cfg.RxModes...),
		Updatable:                    cfg.Updatable,
		IseID:                        cfg.IseID,
		SchemaVersion:                cfg.SchemaVersion,
		IgnoreForCustomDataPoint:     cfg.IgnoreForCustomDataPoint,
		HasCustomDataPointDefinition: cfg.HasCustomDataPointDefinition,
		IgnoreOnInitialLoad:          cfg.IgnoreOnInitialLoad,
		firmware:                     newFirmware(cfg.Firmware),
		channels:                     make(map[string]*Channel),
		// Custom-DP rendering toggles default to true; the device
		// pipeline overrides them per central before materialisation.
		lightLastBrightness:          true,
		useGroupChannelForCoverState: true,
	}
	d.availability = newAvailability(d)
	if cfg.Updatable {
		d.update = NewUpdate(d, nil, nil)
	}
	return d
}

// Firmware returns the firmware tracker.
func (d *Device) Firmware() *Firmware { return d.firmware }

// SwVersion returns the current firmware version as plain string,
// or empty when the CCU has not reported one yet. Convenience for
// north-bound surfaces (MQTT-Discovery's `sw_version`, REST device
// info) that want the canonical scalar without unwrapping
// [FirmwareInfo].
func (d *Device) SwVersion() string {
	if d.firmware == nil {
		return ""
	}
	return d.firmware.Info().Current
}

// UpdateAvailable reports whether an installable firmware update exists for
// this device — i.e. the gated latest version (image delivered for HmIP-RF,
// available for BidCos) differs from the installed one. A newer firmware that
// the CCU knows about but has not yet pushed to the device is NOT counted, so
// the UI does not flag "update available" prematurely. Mirrors how the
// reference stack surfaces update.latest_firmware != firmware.
func (d *Device) UpdateAvailable() bool {
	if d.firmware == nil {
		return false
	}
	info := d.firmware.Info()
	latest := GatedLatestFirmware(d.Interface, info)
	return latest != "" && latest != info.Current
}

// Availability returns the availability tracker.
func (d *Device) Availability() *Availability { return d.availability }

// Available is a short-hand for `Availability().IsReachable()`.
func (d *Device) Available() bool { return d.availability.IsReachable() }

// Info returns the identity-level attributes (model, address,
// manufacturer …) plus the resolved `has_sub_devices` toggle so REST/WS
// consumers can mirror the same per-channel-group split the MQTT bridge
// applies under `sub_devices_enabled`. Channel-level group metadata
// lives on [Channel.Info].
func (d *Device) Info() payload.InfoPayload {
	return &payload.DeviceInfo{
		InterfaceID:   d.InterfaceID,
		Interface:     string(d.Interface),
		Address:       d.Address,
		Model:         d.Model,
		ModelLabel:    d.ModelLabel,
		ModelIcon:     d.ModelIcon,
		SubModel:      d.SubModel,
		Name:          d.Name,
		Manufacturer:  string(d.Manufacturer),
		ProductGroup:  string(d.ProductGroup),
		Rooms:         append([]string(nil), d.Rooms...),
		Functions:     append([]string(nil), d.Functions...),
		Room:          d.Room,
		Function:      d.Function,
		IseID:         d.IseID,
		SchemaVersion: d.SchemaVersion,
		HasSubDevices: d.HasSubDevices(),
	}
}

// Config returns the configuration-level attributes (rx_modes,
// updatable).
func (d *Device) Config() payload.ConfigPayload {
	rxModes := make([]string, len(d.RxModes))
	for i, m := range d.RxModes {
		rxModes[i] = m.String()
	}
	return &payload.DeviceConfig{
		RxModes:   rxModes,
		Updatable: d.Updatable,
	}
}

// State assembles dynamic state (available, firmware) that
// cannot be tagged on the static struct alone.
func (d *Device) State() payload.StatePayload {
	fw := d.firmware.Info()
	return &payload.DeviceState{
		Available:           d.availability.IsReachable(),
		Firmware:            fw.Current,
		AvailableFirmware:   fw.Available,
		FirmwareUpdateState: string(fw.UpdateState),
	}
}

// AvailabilityInfo is a short-hand for `Availability().Info()`.
func (d *Device) AvailabilityInfo() AvailabilityInfo { return d.availability.Info() }

// triggerableDP is satisfied by data points that can re-fire their update
// callbacks without a new wire value arriving. Used by [SetForcedAvailability]
// to wake north-bound subscribers after the device availability context changes.
type triggerableDP interface {
	TriggerUpdate()
}

// SetForcedAvailability proxies to the availability tracker and returns true
// when the effective reachability flipped. When the availability mode changes,
// every generic data point on the device is asked to re-fire its update
// callbacks so north-bound subscribers (MQTT, REST, Matter) reflect the new
// availability context without waiting for the next CCU push.
//
// Mirrors the Python behaviour where set_forced_availability iterates
// self.generic_data_points and calls publish_data_point_updated_event() on
// each one after the availability mode is updated.
func (d *Device) SetForcedAvailability(v hmenum.ForcedDeviceAvailability) bool {
	if d.availability.Forced() == v {
		return false
	}
	changed := d.availability.SetForced(v)
	// Re-fire update callbacks on every generic DP so subscribers observe the
	// changed availability context. This matches the Python path that calls
	// publish_data_point_updated_event() for each dp in generic_data_points.
	for _, dp := range d.AllDataPoints() {
		if t, ok := dp.(triggerableDP); ok {
			t.TriggerUpdate()
		}
	}
	return changed
}

// Update returns the firmware-update entity. Nil when the device
// was constructed with `Updatable=false`.
func (d *Device) Update() *Update { return d.update }

// AttachUpdate wires a firmware updater and refresher to the
// device-level Update entity. Must be called before [Update.Start]
// can dispatch. When the device was constructed with
// `Updatable=false`, the update entity is lazily created here.
func (d *Device) AttachUpdate(updater FirmwareUpdater, refresher FirmwareRefresher) *Update {
	if d.update == nil {
		d.update = NewUpdate(d, updater, refresher)
		return d.update
	}
	d.update.updater = updater
	d.update.refresher = refresher
	return d.update
}

// AddChannel registers (or replaces) a channel under its address.
// The new channel is returned. `channelType` is the CCU-reported
// CHANNEL_TYPE string (e.g. "SHUTTER_TRANSMITTER") that downstream
// layers need for metadata keying.
func (d *Device) AddChannel(address string, number int, channelType string, paramset hmenum.ParamsetKey) *Channel {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch := &Channel{
		Address:    address,
		Number:     number,
		Type:       channelType,
		ParamsetIn: paramset,
		device:     d,
	}
	d.channels[address] = ch
	return ch
}

// Channel returns the channel bound to address, or nil when no such
// channel exists. The lookup also honours the device-root pseudo-
// channel (when address == d.Address — no `:N` suffix), so callers
// can use a single Channel(addr) call for both real channels and the
// device-level paramset container.
func (d *Device) Channel(address string) *Channel {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if ch, ok := d.channels[address]; ok {
		return ch
	}
	if address == d.Address {
		d.rootMu.RLock()
		defer d.rootMu.RUnlock()
		return d.rootChannel
	}
	return nil
}

// RootChannel returns the device-root pseudo-channel — the synthetic
// container for parameters living on the device address itself (without a
// `:N` suffix). The channel has [ChannelNumberDevice] as its Number. Lazily
// created via [Device.EnsureRootChannel]; returns nil before that call.
//
// Used by the device pipeline to hydrate the device-level MASTER paramset
// that classic HM thermostats (HM-CC-RT-DN) carry their week-profile on.
func (d *Device) RootChannel() *Channel {
	d.rootMu.RLock()
	defer d.rootMu.RUnlock()
	return d.rootChannel
}

// EnsureRootChannel creates the device-root pseudo-channel if one
// does not exist yet, and returns the (possibly pre-existing) value.
// Idempotent — repeated calls return the same Channel instance.
func (d *Device) EnsureRootChannel() *Channel {
	d.rootMu.Lock()
	defer d.rootMu.Unlock()
	if d.rootChannel != nil {
		return d.rootChannel
	}
	d.rootChannel = &Channel{
		Address:    d.Address,
		Number:     ChannelNumberDevice,
		Type:       "DEVICE_ROOT",
		ParamsetIn: hmenum.ParamsetKeyMaster,
		device:     d,
	}
	return d.rootChannel
}

// RemoveChannel removes and tears down the channel registered under address.
// Returns true when the channel existed and was removed; false when the address
// was unknown. [Channel.Remove] is called before the channel is dropped from
// the map so all its subscriber hooks run cleanly.
//
// Mirrors the per-channel teardown performed by Python's Device.remove()
// (model/device.py) which calls channel.remove() for each channel before
// clearing the channel collection.
func (d *Device) RemoveChannel(address string) bool {
	d.mu.Lock()
	ch, ok := d.channels[address]
	if ok {
		delete(d.channels, address)
	}
	d.mu.Unlock()
	if !ok {
		return false
	}
	ch.Remove()
	return true
}

// Channels returns a stable snapshot sorted by channel address.
func (d *Device) Channels() []*Channel {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]*Channel, 0, len(d.channels))
	for _, ch := range d.channels {
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// DataPoint is a short-hand for Channel(address).Parameter(p).
// Returns nil when either the channel or the parameter is absent.
func (d *Device) DataPoint(channelAddress string, p hmenum.Parameter) ParameterDataPoint {
	ch := d.Channel(channelAddress)
	if ch == nil {
		return nil
	}
	return ch.Parameter(p)
}

// GetDataPointValue returns the current raw value of the data point identified
// by channelAddress and parameter name. It resolves the channel by address
// (including the device-root pseudo-channel for parameters without a `:N`
// suffix) and returns the RawValue of the first matching data point.
// Returns (nil, false) when no channel carries a data point for the given
// address+parameter pair.
//
// Both VALUES-paramset and MASTER-paramset data points are searched:
// VALUES is tried first, then MASTER as a fallback.
func (d *Device) GetDataPointValue(channelAddress, parameter string) (any, bool) {
	ch := d.Channel(channelAddress)
	if ch == nil {
		return nil, false
	}
	p := hmenum.Parameter(parameter)
	if dp := ch.Parameter(p); dp != nil {
		return dp.RawValue()
	}
	if dp := ch.MasterParameter(p); dp != nil {
		return dp.RawValue()
	}
	return nil, false
}

// HasWeekProfile reports whether at least one channel of the device
// (including the device-root pseudo-channel) has a
// [weekprofile.ProfileDataPoint] attached via the pipeline's
// schedule-detection pass.
//
// The previous heuristic walked the MASTER-paramset DPs looking for
// `ENDTIME_*` / `TEMPERATURE_<DAY>_*` names — that path is no longer reliable
// because the per-slot parameters (P1_*..P6_*) are filtered out of the
// hydrated DPs by design (they would surface as ~84 ghost MQTT topics per
// thermostat). Schedule presence is now tracked authoritatively on the
// channel via [Channel.AttachWeekProfile].
func (d *Device) HasWeekProfile() bool {
	d.mu.RLock()
	for _, ch := range d.channels {
		if ch.HasWeekProfile() {
			d.mu.RUnlock()
			return true
		}
	}
	d.mu.RUnlock()
	if root := d.RootChannel(); root != nil && root.HasWeekProfile() {
		return true
	}
	return false
}

// AllDataPoints returns every VALUES-paramset data point across every channel
// of the device, sorted first by channel address then by parameter name.
func (d *Device) AllDataPoints() []ParameterDataPoint {
	d.mu.RLock()
	channels := make([]*Channel, 0, len(d.channels))
	for _, ch := range d.channels {
		channels = append(channels, ch)
	}
	d.mu.RUnlock()
	sort.Slice(channels, func(i, j int) bool { return channels[i].Address < channels[j].Address })
	out := make([]ParameterDataPoint, 0, len(channels)*4)
	for _, ch := range channels {
		out = append(out, ch.DataPoints()...)
	}
	return out
}

// AllMasterDataPoints returns every MASTER-paramset data point across
// every channel, in the same stable order as [AllDataPoints]. Useful
// for diagnostics endpoints that surface device configuration in one
// flat list.
func (d *Device) AllMasterDataPoints() []ParameterDataPoint {
	d.mu.RLock()
	channels := make([]*Channel, 0, len(d.channels)+1)
	for _, ch := range d.channels {
		channels = append(channels, ch)
	}
	d.mu.RUnlock()
	if root := d.RootChannel(); root != nil {
		channels = append(channels, root)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].Address < channels[j].Address })
	out := make([]ParameterDataPoint, 0, len(channels)*4)
	for _, ch := range channels {
		out = append(out, ch.MasterDataPoints()...)
	}
	return out
}

// DataPointCount reports the total number of VALUES-paramset data
// points on the device. Convenient for `/info`-style endpoints that
// need a single number rather than the full list.
func (d *Device) DataPointCount() int {
	d.mu.RLock()
	channels := make([]*Channel, 0, len(d.channels))
	for _, ch := range d.channels {
		channels = append(channels, ch)
	}
	d.mu.RUnlock()
	total := 0
	for _, ch := range channels {
		total += ch.Len()
	}
	return total
}

// HasReadableDataPoint reports whether at least one data point on the device
// advertises READ in its operations bitmask.
func (d *Device) HasReadableDataPoint() bool {
	for _, dp := range d.AllDataPoints() {
		if dp.ParameterData().Operations.IsReadable() {
			return true
		}
	}
	return false
}

// IdentifyChannel finds the channel referenced by text — a CCU system-variable
// or program name — so a variable whose name carries a device/channel
// identifier can be associated with that device. A channel matches when:
//
//   - text ends with the channel address (e.g. "…VCU0000123:1"), or
//   - the channel's ise_id appears in text as a standalone token, or
//   - the owning device's ise_id appears in text as a standalone token
//     (associated with the device via its lowest-addressed channel).
//
// Returns nil when no channel matches.
//
// Mirrors the Python reference's `model/device.py:742-752`
// (Device.identify_channel). Two deliberate refinements keep the Go port
// deterministic: channels are scanned in sorted-address order (Go map
// iteration is unordered), and channel-specific matches (address suffix,
// channel ise_id) take precedence over the device-wide ise_id fallback
// rather than depending on channel insertion order.
func (d *Device) IdentifyChannel(text string) *Channel {
	if text == "" {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	addrs := make([]string, 0, len(d.channels))
	for addr := range d.channels {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)

	for _, addr := range addrs {
		ch := d.channels[addr]
		if addr != "" && strings.HasSuffix(text, addr) {
			return ch
		}
		if ch.IseID != 0 && containsWord(text, strconv.Itoa(ch.IseID)) {
			return ch
		}
	}
	// Device-wide match: the name carries the device's own ise_id rather than
	// a specific channel's. Attach it to the device via its first channel.
	if d.IseID != 0 && len(addrs) > 0 && containsWord(text, strconv.Itoa(d.IseID)) {
		return d.channels[addrs[0]]
	}
	return nil
}

// containsWord reports whether word appears in text as a standalone token —
// bounded by a non-word character (or a string boundary) on both sides. This
// stops an ise_id like "123" from matching inside a larger number such as
// "41234". A word character is a letter, a digit, or an underscore.
//
// Mirrors the Python reference's `model/device.py:169` (_contains_word); the
// rune-aware boundary check keeps non-ASCII variable names (e.g. German
// umlauts) intact where Python relies on str.isalnum().
func containsWord(text, word string) bool {
	if word == "" {
		return false
	}
	for start := 0; ; {
		i := strings.Index(text[start:], word)
		if i < 0 {
			return false
		}
		i += start
		beforeOK := true
		if i > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:i])
			beforeOK = !isWordChar(r)
		}
		afterOK := true
		if end := i + len(word); end < len(text) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			afterOK = !isWordChar(r)
		}
		if beforeOK && afterOK {
			return true
		}
		start = i + 1
	}
}

// isWordChar reports whether r is alphanumeric or an underscore. Mirrors the
// Python reference's `model/device.py` (_is_word_char): char.isalnum()
// or char == "_".
func isWordChar(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// LastUpdated is the most recent ModifiedAt across every data point
// on every channel. Returns zero time when nothing has been observed.
func (d *Device) LastUpdated() time.Time {
	d.mu.RLock()
	channels := make([]*Channel, 0, len(d.channels))
	for _, ch := range d.channels {
		channels = append(channels, ch)
	}
	d.mu.RUnlock()

	var latest time.Time
	for _, ch := range channels {
		for _, dp := range ch.DataPoints() {
			if t := dp.ModifiedAt(); t.After(latest) {
				latest = t
			}
		}
	}
	return latest
}

// OnRemoved registers a lifecycle hook fired when [NotifyRemoved] is
// called. Returns an idempotent unsubscribe closure.
func (d *Device) OnRemoved(fn func()) func() {
	d.removedMu.Lock()
	d.removedHandlers = append(d.removedHandlers, fn)
	idx := len(d.removedHandlers) - 1
	d.removedMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.removedMu.Lock()
			defer d.removedMu.Unlock()
			if idx < len(d.removedHandlers) {
				d.removedHandlers[idx] = nil
			}
		})
	}
}

// NotifyRemoved fires every registered removal hook. Called by the
// device coordinator when the device disappears from the CCU.
func (d *Device) NotifyRemoved() {
	d.removedMu.Lock()
	handlers := make([]func(), len(d.removedHandlers))
	copy(handlers, d.removedHandlers)
	d.removedHandlers = nil
	d.removedMu.Unlock()
	for _, h := range handlers {
		if h != nil {
			h()
		}
	}
}

// ─── SetScheduleChannelSwitches / ScheduleChannelSwitches ────────────

// SetScheduleChannelSwitches stores the per-channel schedule-enable switch
// data points created by the weekprofile wiring layer.
//
// def set_schedule_channel_switches(self, *, switches):
// self._schedule_channel_switches = switches
func (d *Device) SetScheduleChannelSwitches(switches []*weekprofile.ChannelSwitch) {
	d.scheduleSwitchesMu.Lock()
	d.scheduleChannelSwitches = append([]*weekprofile.ChannelSwitch(nil), switches...)
	d.scheduleSwitchesMu.Unlock()
}

// ScheduleChannelSwitches returns a stable copy of the schedule-channel
// switch data points stored by [SetScheduleChannelSwitches]. Returns nil when
// none have been registered.
func (d *Device) ScheduleChannelSwitches() []*weekprofile.ChannelSwitch {
	d.scheduleSwitchesMu.RLock()
	defer d.scheduleSwitchesMu.RUnlock()
	if len(d.scheduleChannelSwitches) == 0 {
		return nil
	}
	out := make([]*weekprofile.ChannelSwitch, len(d.scheduleChannelSwitches))
	copy(out, d.scheduleChannelSwitches)
	return out
}

// ─── ReloadDeviceConfig ──────────────────────────────────────────────

// ReloadDeviceConfig runs the full config-change cascade for every channel
// of this device. It is the Go equivalent of Python's
// `Device.reload_device_config` (device.py:847) which calls
// `on_config_changed()` on every channel and then publishes a device-updated
// event.
//
// Per channel, [Channel.OnConfigChanged] reloads the MASTER paramset
// description, re-triggers link-peer refresh, and notifies every derived
// data point (generic, event, calculated, custom) of the config change.
//
// Channels whose refresher is not yet wired (ErrNoChannelRefresher) are
// silently skipped — they are not yet connected to a backend.  All other
// errors are returned immediately.
func (d *Device) ReloadDeviceConfig(ctx context.Context) error {
	for _, ch := range d.Channels() {
		if err := ch.OnConfigChanged(ctx); err != nil {
			return err
		}
	}
	return nil
}

// SetCustomDPBehavior stamps the per-central custom-DP rendering
// toggles onto the device. Called by the device pipeline before
// custom-DP materialisation; the light / cover factories read the
// values back through [Device.LightLastBrightness] and
// [Device.UseGroupChannelForCoverState].
func (d *Device) SetCustomDPBehavior(lightLastBrightness, useGroupChannelForCoverState bool) {
	d.behaviorMu.Lock()
	defer d.behaviorMu.Unlock()
	d.lightLastBrightness = lightLastBrightness
	d.useGroupChannelForCoverState = useGroupChannelForCoverState
}

// LightLastBrightness reports whether a plain light turn-on restores
// the last non-zero brightness (true) or turns on at full (false).
// Defaults to true until the pipeline stamps a per-central value.
func (d *Device) LightLastBrightness() bool {
	d.behaviorMu.RLock()
	defer d.behaviorMu.RUnlock()
	return d.lightLastBrightness
}

// UseGroupChannelForCoverState reports whether a cover with a
// group-channel LEVEL reports its position from the group channel
// (true) or its own channel (false). Defaults to true until the
// pipeline stamps a per-central value.
func (d *Device) UseGroupChannelForCoverState() bool {
	d.behaviorMu.RLock()
	defer d.behaviorMu.RUnlock()
	return d.useGroupChannelForCoverState
}
