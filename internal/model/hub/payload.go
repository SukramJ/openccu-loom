// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
)

// Compile-time guarantees that every hub-domain type satisfies the
// universal Source contract. ADR-0007 step 6 (Hub-DP migration).
//
// Program and Sysvar embed HubDataPoint + payload.ServiceRegistry and
// provide their own payload methods below; sub-type wrappers
// (ProgramDpButton, SysvarDpSwitch, SysvarDpBinarySensor, SysvarDpText)
// embed *Program / *Sysvar and therefore inherit Source by promotion.
var (
	_ payload.Source = (*Program)(nil)
	_ payload.Source = (*Sysvar)(nil)
	_ payload.Source = (*Update)(nil)
	_ payload.Source = (*AlarmMessages)(nil)
	_ payload.Source = (*ServiceMessages)(nil)
	_ payload.Source = (*InstallMode)(nil)
	_ payload.Source = (*Connectivity)(nil)
	_ payload.Source = (*Inbox)(nil)
	_ payload.Source = (*Metrics)(nil)
	_ payload.Source = (*Hub)(nil)
)

// --- Program ---

// CanonicalUniqueID builds the external, loom-namespaced unique_id for
// this program: loom_<serialSuffix>_program_<id>. The serialSuffix is
// supplied by the north boundary (central → serial); hub entities always
// carry it because their names repeat across CCUs.
//
// Keyed on the CCU program id rather than on the name, because the name is
// editable in the WebUI and a key built from it re-keys the consumer's
// entity on every rename — taking its history and every automation with it.
// Falls back to the name slug while the id is unresolved, which is the
// shape a consumer must accept during a rollover.
//
// See docs/external-clients/ha-unique-id-migration.md.
func (p *Program) CanonicalUniqueID(serialSuffix string) string {
	if p == nil {
		return ""
	}
	key := p.ID
	if key == "" {
		key = p.LegacyName()
	}
	return routingkey.CanonicalUniqueID(serialSuffix, "program", routingkey.HubSlug(key), "")
}

// Info returns identity-level fields for a Program.
func (p *Program) Info() payload.InfoPayload {
	if p == nil {
		return nil
	}
	return &payload.ProgramInfo{
		ID:          p.ID,
		Name:        p.LegacyName(),
		Description: p.Description,
		Category:    "program",
		UniqueID:    p.UniqueID(),
		IsInternal:  p.Internal(),
	}
}

// Config returns configuration-level fields for a Program.
func (p *Program) Config() payload.ConfigPayload {
	if p == nil {
		return nil
	}
	return &payload.ProgramConfig{
		EnabledDefault: p.EnabledDefault,
	}
}

// State returns the live program state.
func (p *Program) State() payload.StatePayload {
	if p == nil {
		return nil
	}
	out := &payload.ProgramState{
		StateUncertain: p.StateUncertain(),
		// Fail open: a program whose active flag has not been observed yet
		// is treated as runnable, so a consumer never greys out an action
		// on missing information. Once the CCU reports the flag — which the
		// program scan does on every pass — the real answer takes over.
		ExecuteAvailable: true,
	}
	if active, observed := p.Active(); observed {
		out.IsActive = &active
		out.ExecuteAvailable = active
	}
	if ts, ok := p.LastExecution(); ok {
		out.LastExecuted = ts.Format("2006-01-02T15:04:05Z07:00")
	}
	if success, observed := p.LastResult(); observed {
		out.LastResultSuccess = &success
	}
	return out
}

// --- Sysvar ---

// CanonicalUniqueID builds the external, loom-namespaced unique_id for
// this system variable: loom_<serialSuffix>_sysvar_<vid>.
//
// Keyed on the CCU's numeric variable id for the reason given on
// [Program.CanonicalUniqueID] — the name is editable and the id is not.
// Vid is 0 until a hub scan resolves it, and then the name slug stands in;
// [Sysvar.PathData] guards the same value the same way.
//
// See docs/external-clients/ha-unique-id-migration.md.
func (s *Sysvar) CanonicalUniqueID(serialSuffix string) string {
	if s == nil {
		return ""
	}
	if vid := s.Meta().Vid; vid != 0 {
		return routingkey.CanonicalUniqueID(serialSuffix, "sysvar", routingkey.HubSlug(strconv.Itoa(vid)), "")
	}
	return SysvarUniqueIDForName(serialSuffix, s.LegacyName())
}

// SysvarUniqueIDForName builds the name-keyed routing key of a sysvar, the
// same fallback [Sysvar.CanonicalUniqueID] uses before a hub scan has
// resolved a vid.
//
// It exists so a caller holding only a name — an event that arrived before
// the model saw the variable — reaches the model's own rule instead of
// rebuilding it. The key is the fallback, not the identity: a name is
// editable, so once a vid is known that is what the id keys on.
func SysvarUniqueIDForName(serialSuffix, name string) string {
	if serialSuffix == "" || name == "" {
		return ""
	}
	return routingkey.CanonicalUniqueID(serialSuffix, "sysvar", routingkey.HubSlug(name), "")
}

// Info returns identity-level fields for a Sysvar.
func (s *Sysvar) Info() payload.InfoPayload {
	if s == nil {
		return nil
	}
	m := s.Meta()
	out := &payload.SysvarInfo{
		Name:        s.LegacyName(),
		Description: m.Description,
		Category:    "sysvar",
		UniqueID:    s.UniqueID(),
		ValueType:   string(m.ValueType),
		Unit:        m.Unit,
		Vid:         m.Vid,
		IsExtended:  m.IsExtended,
	}
	if len(m.ValueList) > 0 {
		out.ValueList = m.ValueList
	}
	if m.Min != nil {
		out.Min = m.Min
	}
	if m.Max != nil {
		out.Max = m.Max
	}
	return out
}

// Config returns configuration-level fields for a Sysvar.
func (s *Sysvar) Config() payload.ConfigPayload {
	if s == nil {
		return nil
	}
	return &payload.SysvarConfig{
		EnabledDefault: s.EnabledByDefault(),
		Writable:       s.Writable(),
	}
}

// State returns the live sysvar state.
func (s *Sysvar) State() payload.StatePayload {
	if s == nil {
		return nil
	}
	out := &payload.SysvarState{
		StateUncertain: s.StateUncertain(),
		ValueType:      string(s.Meta().ValueType),
	}
	if v, ok := s.Value(); ok {
		out.Value = v.Unwrap()
	}
	if prev, ok := s.PreviousValue(); ok {
		out.PreviousValue = prev.Unwrap()
	}
	return out
}

// --- Update ---

// Info returns identity-level fields for an Update tracker.
func (u *Update) Info() payload.InfoPayload {
	if u == nil {
		return nil
	}
	return &payload.UpdateInfo{Category: "update"}
}

// Config returns configuration-level fields for an Update tracker.
func (u *Update) Config() payload.ConfigPayload {
	return nil
}

// State returns the live update state.
func (u *Update) State() payload.StatePayload {
	if u == nil {
		return nil
	}
	info, observed := u.UpdateInfo()
	if !observed {
		return &payload.UpdateState{Available: false}
	}
	return &payload.UpdateState{
		Available:            true,
		CurrentFirmware:      info.CurrentFirmware,
		LatestFirmware:       info.AvailableFirmware,
		UpdateAvailable:      info.UpdateAvailable,
		CheckScriptAvailable: info.CheckScriptAvailable,
	}
}

// --- AlarmMessages ---

// Info returns identity-level fields for AlarmMessages.
func (a *AlarmMessages) Info() payload.InfoPayload {
	if a == nil {
		return nil
	}
	return &payload.AlarmMessagesInfo{Category: "alarm_messages"}
}

// Config returns configuration-level fields for AlarmMessages.
func (a *AlarmMessages) Config() payload.ConfigPayload {
	return nil
}

// State returns the live alarm-messages state.
func (a *AlarmMessages) State() payload.StatePayload {
	if a == nil {
		return nil
	}
	items := a.List()
	rows := make([]payload.AlarmMessageRow, len(items))
	for i := range items {
		rows[i] = payload.AlarmMessageRow{
			ID:          items[i].ID,
			Name:        items[i].Name,
			Description: items[i].Description,
			Counter:     items[i].Counter,
		}
		// A zero time means the CCU reported no such occurrence. Emit an
		// empty string rather than formatting it, so subscribers never
		// see year 0001 presented as a real alarm date.
		if !items[i].Timestamp.IsZero() {
			rows[i].Timestamp = items[i].Timestamp.Format("2006-01-02T15:04:05Z07:00")
		}
		if !items[i].LastTimestamp.IsZero() {
			rows[i].LastTimestamp = items[i].LastTimestamp.Format("2006-01-02T15:04:05Z07:00")
		}
	}
	return &payload.AlarmMessagesState{
		Count:    a.Count(),
		Items:    rows,
		Observed: a.Observed(),
	}
}

// --- ServiceMessages ---

// Info returns identity-level fields for ServiceMessages.
func (s *ServiceMessages) Info() payload.InfoPayload {
	if s == nil {
		return nil
	}
	return &payload.ServiceMessagesInfo{Category: "service_messages"}
}

// Config returns configuration-level fields for ServiceMessages.
func (s *ServiceMessages) Config() payload.ConfigPayload {
	return nil
}

// State returns the live service-messages state.
func (s *ServiceMessages) State() payload.StatePayload {
	if s == nil {
		return nil
	}
	items := s.List()
	rows := make([]payload.ServiceMessageRow, len(items))
	for i := range items {
		rows[i] = payload.ServiceMessageRow{
			ID:         items[i].ID,
			Name:       items[i].Name,
			Address:    items[i].Address,
			DeviceName: items[i].DeviceName,
			Type:       items[i].Type.String(),
			Counter:    items[i].Counter,
			Rooms:      items[i].Rooms,
			Functions:  items[i].Functions,
			Quittable:  items[i].Quittable,
		}
		// A zero time means the CCU reported no such occurrence. Emit an
		// empty string rather than formatting it, so subscribers never
		// see year 0001 presented as a real message date — mirrors
		// AlarmMessages.State.
		if !items[i].Timestamp.IsZero() {
			rows[i].Timestamp = items[i].Timestamp.Format("2006-01-02T15:04:05Z07:00")
		}
		if !items[i].LastTimestamp.IsZero() {
			rows[i].LastTimestamp = items[i].LastTimestamp.Format("2006-01-02T15:04:05Z07:00")
		}
	}
	return &payload.ServiceMessagesState{
		Count:          s.Count(),
		QuittableCount: s.QuittableCount(),
		Items:          rows,
		Observed:       s.Observed(),
	}
}

// --- InstallMode ---

// Info returns identity-level fields for an InstallMode tracker.
func (m *InstallMode) Info() payload.InfoPayload {
	if m == nil {
		return nil
	}
	return &payload.InstallModeInfo{
		Category:    "install_mode",
		InterfaceID: m.InterfaceID,
	}
}

// Config returns configuration-level fields for an InstallMode tracker.
func (m *InstallMode) Config() payload.ConfigPayload {
	return nil
}

// State returns the live install-mode state.
func (m *InstallMode) State() payload.StatePayload {
	if m == nil {
		return nil
	}
	enabled, remaining, observed := m.InstallState()
	return &payload.InstallModeState{
		Active:           enabled,
		SecondsRemaining: int(remaining.Seconds()),
		Observed:         observed,
	}
}

// --- Connectivity ---

// Info returns identity-level fields for a Connectivity tracker.
func (c *Connectivity) Info() payload.InfoPayload {
	if c == nil {
		return nil
	}
	return &payload.ConnectivityInfo{Category: "connectivity"}
}

// Config returns configuration-level fields for a Connectivity tracker.
func (c *Connectivity) Config() payload.ConfigPayload {
	return nil
}

// State returns the live connectivity state.
func (c *Connectivity) State() payload.StatePayload {
	if c == nil {
		return nil
	}
	interfaces := c.List()
	rows := make([]payload.ConnectivityInterfaceRow, len(interfaces))
	for i, ir := range interfaces {
		rows[i] = payload.ConnectivityInterfaceRow{
			InterfaceID: ir.InterfaceID,
			Reachable:   ir.Reachable,
		}
	}
	allReachable, observed := c.AllReachable()
	return &payload.ConnectivityState{
		AllReachable: allReachable,
		Interfaces:   rows,
		Observed:     observed,
	}
}

// --- Inbox ---

// Info returns identity-level fields for an Inbox.
func (i *Inbox) Info() payload.InfoPayload {
	if i == nil {
		return nil
	}
	return &payload.InboxInfo{Category: "inbox"}
}

// Config returns configuration-level fields for an Inbox.
func (i *Inbox) Config() payload.ConfigPayload {
	return nil
}

// State returns the live inbox state.
func (i *Inbox) State() payload.StatePayload {
	if i == nil {
		return nil
	}
	devices := i.List()
	rows := make([]payload.InboxDeviceRow, len(devices))
	for j := range devices {
		d := &devices[j]
		rows[j] = payload.InboxDeviceRow{
			Address:         d.Address,
			Model:           d.Model,
			Serial:          d.Serial,
			FirstSeen:       d.FirstSeen,
			Manufacturer:    d.Manufacturer,
			PendingCreation: d.PendingCreation,
		}
	}
	return &payload.InboxState{
		Count:    i.Count(),
		Devices:  rows,
		Observed: i.Observed(),
	}
}

// --- Metrics ---

// Info returns identity-level fields for a Metrics aggregator.
func (m *Metrics) Info() payload.InfoPayload {
	if m == nil {
		return nil
	}
	return &payload.MetricsInfo{Category: "metrics"}
}

// Config returns configuration-level fields for a Metrics aggregator.
func (m *Metrics) Config() payload.ConfigPayload {
	return nil
}

// State returns a snapshot of all observed metrics.
func (m *Metrics) State() payload.StatePayload {
	if m == nil {
		return nil
	}
	snap := m.Snapshot()
	out := make(payload.MetricsState, len(snap))
	for kind, sample := range snap {
		out[string(kind)] = payload.MetricsSample{
			Value: sample.Value,
			When:  sample.When.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	return out
}

// --- Hub ---

// Info returns identity-level fields for the Hub aggregate.
func (h *Hub) Info() payload.InfoPayload {
	if h == nil {
		return nil
	}
	return &payload.HubInfo{
		CentralName: h.CentralName,
		Category:    "hub",
	}
}

// Config returns configuration-level fields for the Hub aggregate.
func (h *Hub) Config() payload.ConfigPayload {
	return nil
}

// State returns a summary of the Hub aggregate state.
func (h *Hub) State() payload.StatePayload {
	if h == nil {
		return nil
	}
	return &payload.HubState{
		ProgramCount: len(h.Programs()),
		SysvarCount:  len(h.Sysvars()),
	}
}
