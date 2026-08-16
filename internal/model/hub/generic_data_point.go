// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// HubDataPoint is the shared base for CCU "hub-level data point"
// entities: system variables ([Sysvar]) and programs ([Program]).
//
// It provides the common identity and metadata fields that both types
// Share, modelled after
//
// - Name / Description — the user-visible label and optional long text
// - EnabledDefault — whether the data point is shown in the UI by
// Default
// - StateUncertain — true until the first confirmed value is received
// from the CCU.
//
// HubDataPoint also embeds [datapoint.BaseDataPointFields]
// so it satisfies [datapoint.BaseDataPoint] through promotion. The
// promoted UniqueID() / Visible() methods come from BaseDataPointFields;
// EnabledByDefault() is shadowed here to respect the EnabledDefault field
// in addition to any forced-usage override.
//
// Callers should embed HubDataPoint by value inside concrete types
// (Sysvar, Program) so all fields are promoted without a pointer
// indirection. Use [NewHubDataPoint] to construct a properly
// initialised value.
//
// Semantically distinct hub aggregates (Connectivity, InstallMode,
// Update, AlarmMessages, ServiceMessages, Inbox, Metrics) are NOT
// modelled as HubDataPoints: they are coordinator-level aggregates
// that track CCU-infrastructure state rather than named, individually
// addressable values. This diverges intentionally from the Python
// class hierarchy where those aggregates sit outside
// GenericHubDataPoint as well; the comment is here to make the
// deliberate decision traceable.
//
//nolint:revive // HubDataPoint name is stable public API embedded by Sysvar and Program across many callers; renaming requires coordinated refactor.
type HubDataPoint struct {
	datapoint.BaseDataPointFields

	// Name is the canonical identifier of the data point, derived from
	// the CCU's legacy_name after slug-normalisation.
	Name string
	// Description is the optional human-readable description stored on
	// the CCU; may be empty. BaseDataPointFields does not carry
	// Description — it is specific to hub-level entities.
	Description string
	// EnabledDefault controls whether north-bound adapters should expose
	// this data point by default (e.g. MQTT discovery auto-enable).
	EnabledDefault bool

	mu sync.RWMutex
	// StateConfirmed is the inverse of
	// false (the Go zero value) means uncertain; true means confirmed.
	// Inverting the polarity avoids the need for a constructor call to
	// set the initial state — any newly zeroed HubDataPoint starts
	// "uncertain" by virtue of the zero value being false.
	stateConfirmed bool
	// channelRef is the optional channel ADDRESS (e.g. "0001ABCD:3")
	// associated with this hub data point. It is set by the southbound
	// wiring via SetChannel when the data point's legacy_name carries a
	// device/channel identifier that matches a registered device (see the
	// Python reference's `model/hub/data_point.py:84`). Empty until a match is
	// established; consumers derive the owning device address from the
	// part before the ":". Mirrors the reference `channel` link, but
	// stores the address rather than a Channel object so the model layer
	// stays free of a device-package import.
	channelRef string
}

// NewHubDataPoint constructs a HubDataPoint with a fully initialised
// [datapoint.BaseDataPointFields] embedded, so UniqueID(), Visible(),
// and EnabledByDefault() are meaningful from the first use.
//
// - central — the Unit name, used to scope UniqueID.
// - name — the sysvar/program name (becomes both HubDataPoint.Name
// and the KeyName inside BaseDataPointFields).
// - description — optional human-readable text.
// - enabledDefault — initial value for the EnabledDefault field.
func NewHubDataPoint(centralName, name, description string, enabledDefault bool) HubDataPoint {
	return HubDataPoint{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(centralName, "", name),
		Name:                name,
		Description:         description,
		EnabledDefault:      enabledDefault,
	}
}

// LegacyName returns the original (pre-slug) name of the data point as
// received from the CCU's Rega response. This is.
// (hub/data_point.py:85).
//
// In the Go model the Name field already carries the CCU-provided name;
// HubDataPoint.LegacyName aliases it so callers that need the explicit
// "legacy" semantic can read it via a stable method without reaching
// into the field directly. This mirrors the Python DelegatedProperty
// shape where `legacy_name` delegates to `self._legacy_name`.
func (h *HubDataPoint) LegacyName() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Name
}

// SetName replaces the canonical name of the data point.
//
// The name is mutable: an operator renames a system variable or a program
// on the CCU and the model updates the existing entry in place, so that
// subscribers wired at registration time keep working. Every mutation goes
// through this method so writers and the [Signature] / [FullName] /
// [LegacyName] readers agree on one lock — the data point's own. The
// containers that hold the data point (the hub's sysvar map, the program
// index) are guarded by a different mutex, which excludes nothing here.
func (h *HubDataPoint) SetName(name string) {
	h.mu.Lock()
	h.Name = name
	h.mu.Unlock()
}

// Channel returns the optional channel-address association for this hub data
// point, or "" when the data point's legacy_name did not match any device.
// The association is established externally by the southbound wiring (see
// [SetChannel]); DeviceAddress derives the owning device from it.
func (h *HubDataPoint) Channel() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.channelRef
}

// DeviceAddress returns the owning device address of the associated channel
// (the part before the ":"), or "" when no channel is associated. A channel
// address without a ":" (a device-level address) is returned unchanged.
func (h *HubDataPoint) DeviceAddress() string {
	ch := h.Channel()
	if ch == "" {
		return ""
	}
	if i := strings.IndexByte(ch, ':'); i >= 0 {
		return ch[:i]
	}
	return ch
}

// SetChannel stores the optional channel-address association (or "" to clear
// it). Called by the southbound wiring's assignment pass when a device/channel
// identifier in the data point's legacy_name matches — or no longer matches —
// a registered device. Idempotent: safe to re-run after every device ingest
// and hub refresh.
func (h *HubDataPoint) SetChannel(channel string) {
	h.mu.Lock()
	h.channelRef = channel
	h.mu.Unlock()
}

// EnabledByDefault shadows the promoted method from
// [datapoint.BaseDataPointFields]. When no forced-usage override has
// been installed, it returns the EnabledDefault field value. When a
// forced usage is set, it delegates to the BaseDataPointFields logic
// (NoCreate / CDPSecondary → false; user-facing categories → true).
//
// This shadow is necessary because BaseDataPointFields.EnabledByDefault()
// defaults to true when there is no forced usage, whereas hub DPs may
// have EnabledDefault=false.
func (h *HubDataPoint) EnabledByDefault() bool {
	_, hasForced := h.ForcedUsage()
	if !hasForced {
		return h.EnabledDefault
	}
	// Delegate to the BaseDataPointFields logic for forced usages.
	return h.BaseDataPointFields.EnabledByDefault()
}

// SetForcedUsage installs a forced [hmenum.DataPointUsage] override
// and propagates it to the embedded [datapoint.BaseDataPointFields].
// This is a convenience forwarder so callers don't need to reach into
// the embedded struct.
func (h *HubDataPoint) SetForcedUsage(usage hmenum.DataPointUsage) {
	h.BaseDataPointFields.SetForcedUsage(usage)
}

// StateUncertain reports whether the data point has not yet received a
// confirmed value from the CCU. Starts true (the zero value of the unexported
// stateConfirmed bool is false, so !false = true); cleared on the first
// successful write_value / update_data cycle.
func (h *HubDataPoint) StateUncertain() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return !h.stateConfirmed
}

// markCertain clears the uncertain flag. Called by concrete subtypes
// once they have processed a confirmed value.
func (h *HubDataPoint) markCertain() {
	h.mu.Lock()
	h.stateConfirmed = true
	h.mu.Unlock()
}

// markUncertain sets the uncertain flag. Used by Sysvar to signal that
// an unconfirmed (optimistic) write is in flight.
func (h *HubDataPoint) markUncertain() {
	h.mu.Lock()
	h.stateConfirmed = false
	h.mu.Unlock()
}

// Signature returns the canonical log/debug identifier for the data
// point in the form "name". Concrete subtypes may override this by
// providing a method with the same name on their own type — because Go
// does not support virtual dispatch, the override must be called
// explicitly on the concrete type. This base implementation is
// sufficient for the common read path.
func (h *HubDataPoint) Signature() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Name
}

// Available reports whether the data point is ready to serve data. In Go the
// hub does not own a CCU availability reference, so Available returns true
// once the data point has been confirmed (i.e. StateUncertain is false).
func (h *HubDataPoint) Available() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.stateConfirmed
}

// FullName returns a human-readable identifier composed of the Name field.
// For hub data points the full name equals the Name because there is no
// channel address or device context. Go returns just the Name since the
// central context is available separately via UniqueID.
func (h *HubDataPoint) FullName() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Name
}

// TranslationKey returns the HA entity translation key for this hub data
// point. The base implementation returns "" — subtypes or factory callers
// that know the concrete HA entity kind should override or set this per
// instance.
func (h *HubDataPoint) TranslationKey() string { return "" }

// HubDataPointer is the interface satisfied by any type that embeds
// (or wraps) a HubDataPoint and exposes its shared surface. It enables
// polymorphic handling of Sysvar and Program in north-bound adapters
// and coordinators without resorting to any.
//
// HubDataPointer extends [datapoint.BaseDataPoint] so
// hub DPs participate in the unified DP identity/visibility contract.
//
// The interface is defined here, in the producer package, because it
// models a semantic type (a CCU hub data point) rather than a narrow
// consumer-defined dependency.
//
//nolint:revive // HubDataPointer name pairs with HubDataPoint; renaming requires coordinated refactor of all callers.
type HubDataPointer interface {
	datapoint.BaseDataPoint
	// Signature returns the canonical debug/log identifier.
	Signature() string
	// StateUncertain returns true until the first confirmed value
	// is received from the CCU.
	StateUncertain() bool
}
