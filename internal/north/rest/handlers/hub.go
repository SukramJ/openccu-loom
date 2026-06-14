// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// rfc3339OrEmpty formats t in RFC3339 when non-zero; returns "" otherwise.
func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// HubIndex is the facade hub-level endpoints depend on. Multi-CCU: the list
// endpoints aggregate over every central via [HubIndex.Hubs], tagging each
// item with its central; the mutating endpoints route to a specific central
// via [HubIndex.HubFor] (selected by the `central` query parameter).
type HubIndex interface {
	// Hub returns the first central's hub (back-compat for single-CCU paths
	// and tests). Prefer Hubs/HubFor for multi-CCU correctness.
	Hub() *hub.Hub
	// Hubs returns every registered central's hub, in stable name order.
	Hubs() []NamedHub
	// HubFor returns the named central's hub, or nil when unknown.
	HubFor(centralName string) *hub.Hub
}

// NamedHub pairs a central name with its hub for multi-CCU aggregation.
type NamedHub struct {
	Central string
	Hub     *hub.Hub
}

// resolveHubForMutation picks the hub a mutating request targets. The
// `central` query parameter names it explicitly; when omitted it falls back
// to the sole central (single-CCU convenience) and is otherwise ambiguous
// (nil → the handler answers 400/404).
func resolveHubForMutation(idx HubIndex, centralName string) *hub.Hub {
	if idx == nil {
		return nil
	}
	if centralName != "" {
		return idx.HubFor(centralName)
	}
	hubs := idx.Hubs()
	if len(hubs) == 1 {
		return hubs[0].Hub
	}
	if h := idx.Hub(); len(hubs) == 0 && h != nil {
		return h
	}
	return nil
}

// ProgramSummary is one entry in `GET /api/v1/programs`.
type ProgramSummary struct {
	// Central is the CCU this program belongs to (multi-CCU grouping).
	Central     string `json:"central,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Active      *bool  `json:"active,omitempty"`
	// LastExecuted is the RFC3339 timestamp of the most recent execution,
	// omitted when no execution has been observed yet. Closes H-032.
	LastExecuted string `json:"last_executed,omitempty"`
	// IsInternal is true for Tmp_*-programs created internally by the CCU.
	IsInternal bool `json:"is_internal,omitempty"`
	// EnabledDefault is true when the program matched a configured description
	// marker (so clients enable its entity by default); false when no markers
	// are configured (entry included but disabled by default). Mirrors the
	// reference stack's marker-driven enabled-by-default resolution.
	EnabledDefault bool `json:"enabled_default,omitempty"`
}

// SysvarSummary is one entry in `GET /api/v1/sysvars`.
type SysvarSummary struct {
	// Central is the CCU this system variable belongs to.
	Central     string   `json:"central,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	ValueType   string   `json:"value_type"`
	Value       any      `json:"value,omitempty"`
	Observed    bool     `json:"observed"`
	ValueList   []string `json:"value_list,omitempty"`
	// Min / Max are the declared bounds from the CCU Rega response.
	// Omitted when the CCU does not declare bounds (e.g. BOOL sysvars).
	// Closes H-033.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// IsInternal mirrors the CCU's isInternal flag — internal variables
	// back CCU bookkeeping; clients skip them for HA entities unless
	// opted in (the reference stack's INTERNAL description marker).
	IsInternal bool `json:"is_internal,omitempty"`
	// IsExtended is true when the variable's description carried the
	// extended marker — clients then expose the writable entity flavour
	// (switch/number/select/text) instead of the read-only default.
	IsExtended bool `json:"is_extended,omitempty"`
	// Vid is the CCU-internal numeric variable ID (ise_id). Stable across
	// renames; clients use it to apply the reference stack's fixed-ID
	// exclusions (40 = alarm messages, 41 = service messages — both are
	// surfaced through dedicated hub singletons instead).
	Vid int `json:"vid,omitempty"`
	// EnabledDefault is true when the variable matched a configured description
	// marker (so clients enable its entity by default); false when no markers
	// are configured (entry included but disabled by default). Mirrors the
	// reference stack's marker-driven enabled-by-default resolution.
	EnabledDefault bool `json:"enabled_default,omitempty"`
}

// SysvarSetRequest is the body of `PUT /sysvars/{name}`.
type SysvarSetRequest struct {
	Value any `json:"value"`
}

// InstallModeState is the body of `GET/POST /install-mode`.
type InstallModeState struct {
	Active  bool `json:"active"`
	Seconds int  `json:"seconds,omitempty"`
}

// InboxDeviceDTO is one entry in `GET /api/v1/inbox`.
type InboxDeviceDTO struct {
	// Central is the CCU that reported this pending device.
	Central      string `json:"central,omitempty"`
	Address      string `json:"address"`
	Model        string `json:"model"`
	Serial       string `json:"serial,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	FirstSeen    int64  `json:"first_seen,omitempty"`
}

// AlarmMessageDTO is one entry in the alarm-messages list.
type AlarmMessageDTO struct {
	// Central is the CCU this alarm message belongs to.
	Central     string `json:"central,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	DeviceName  string `json:"device_name,omitempty"`
	// Address is the CCU channel address that generated the alarm.
	// Omitted when unavailable (legacy CCUs). Closes H-034.
	Address string `json:"address,omitempty"`
	// StateValue is the raw alarm state string from the CCU Rega script.
	// Closes H-034.
	StateValue  string    `json:"state_value,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Counter     int       `json:"counter"`
	LastTrigger string    `json:"last_trigger,omitempty"`
	Rooms       []string  `json:"rooms,omitempty"`
}

// ServiceMessageDTO is one entry in the service-messages list.
type ServiceMessageDTO struct {
	// Central is the CCU this service message belongs to.
	Central    string `json:"central,omitempty"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Address    string `json:"address,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	Type       string `json:"type,omitempty"`
	// Description is the optional human-readable message text. Closes H-034.
	Description string `json:"description,omitempty"`
	// Priority is the integer priority level (0 = normal). Closes H-034.
	Priority  int       `json:"priority,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Counter   int       `json:"counter"`
	Quittable bool      `json:"quittable"`
}

// InstallModeController is the facade `install-mode` endpoints use.
type InstallModeController interface {
	InstallModeState() (active bool, remaining time.Duration)
	SetInstallMode(ctx context.Context, on bool, duration time.Duration) error
}

// --- Program handlers ---

// ListPrograms renders the program catalogue aggregated across all centrals.
func ListPrograms(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []ProgramSummary{})
			return
		}
		var out []ProgramSummary
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			for _, p := range nh.Hub.Programs() {
				active, observed := p.Active()
				e := ProgramSummary{
					Central:     nh.Central,
					ID:          p.ID,
					Name:        p.Name,
					Description: p.Description,
				}
				if observed {
					v := active
					e.Active = &v
				}
				// H-032: expose last_executed when a run has been observed.
				if ts, hasTS := p.LastExecution(); hasTS {
					e.LastExecuted = rfc3339OrEmpty(ts)
				}
				// M-4: propagate IsInternal so north-bound can filter Tmp_*-programs.
				e.IsInternal = p.IsInternal
				e.EnabledDefault = p.EnabledByDefault()
				out = append(out, e)
			}
		}
		if out == nil {
			out = []ProgramSummary{}
		}
		JSON(w, http.StatusOK, out)
	}
}

// ProgramSetEnabledRequest is the body for `PUT /programs/{id}`.
type ProgramSetEnabledRequest struct {
	Active bool `json:"active"`
}

// SetProgramEnabled toggles the program's enabled flag on the CCU.
func SetProgramEnabled(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		id := chi.URLParam(r, "id")
		p, ok := h.Program(id)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Program not found", id))
			return
		}
		var req ProgramSetEnabledRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := p.SetEnabled(r.Context(), req.Active); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Set enabled failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// SysvarCreateRequest is the body for `POST /sysvars`.
type SysvarCreateRequest struct {
	Name      string   `json:"name"`
	ValueType string   `json:"value_type"` // BOOL|INTEGER|FLOAT|STRING|ENUM
	Unit      string   `json:"unit,omitempty"`
	Min       string   `json:"min,omitempty"`
	Max       string   `json:"max,omitempty"`
	ValueList []string `json:"value_list,omitempty"`
}

// CreateSysvar provisions a new sysvar on the CCU via the Rega
// `create_system_variable` script. The hub's mirror picks the new
// entry up on the next periodic sync.
func CreateSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		var req SysvarCreateRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Name == "" || req.ValueType == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "name and value_type are required", ""))
			return
		}
		if err := h.CreateSysvarRemote(r.Context(),
			req.Name, req.ValueType, req.Unit, req.Min, req.Max, req.ValueList); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Sysvar create failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// SysvarPatchRequest is the body for `PATCH /sysvars/{name}`.
// Every field is optional — empty/missing fields leave the CCU's
// existing metadata untouched. Type changes are not supported via
// this endpoint; rebuild the sysvar (DELETE + POST) instead.
type SysvarPatchRequest struct {
	Unit        *string   `json:"unit,omitempty"`
	Min         *string   `json:"min,omitempty"`
	Max         *string   `json:"max,omitempty"`
	ValueList   *[]string `json:"value_list,omitempty"`
	Description *string   `json:"description,omitempty"`
}

// PatchSysvar updates a sysvar's metadata in place via the Rega
// `update_system_variable` script.
func PatchSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		name := chi.URLParam(r, "name")
		var req SysvarPatchRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		unit, vmin, vmax, desc := "", "", "", ""
		var valueList []string
		if req.Unit != nil {
			unit = *req.Unit
		}
		if req.Min != nil {
			vmin = *req.Min
		}
		if req.Max != nil {
			vmax = *req.Max
		}
		if req.Description != nil {
			desc = *req.Description
		}
		if req.ValueList != nil {
			valueList = *req.ValueList
		}
		if err := h.UpdateSysvarRemote(
			r.Context(), name, unit, vmin, vmax, desc, valueList,
		); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Sysvar update failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// DeleteSysvar removes a sysvar from the CCU and drops the local
// mirror once the call lands.
func DeleteSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		name := chi.URLParam(r, "name")
		if err := h.DeleteSysvarRemote(r.Context(), name); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Sysvar delete failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// ExecuteProgram triggers a CCU program.
func ExecuteProgram(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		id := chi.URLParam(r, "id")
		p, ok := h.Program(id)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Program not found", id))
			return
		}
		if err := p.Execute(r.Context()); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Execute failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// --- Sysvar handlers ---

// ListSysvars renders every registered sysvar aggregated across all centrals.
func ListSysvars(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []SysvarSummary{})
			return
		}
		var out []SysvarSummary
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			for _, s := range nh.Hub.Sysvars() {
				sum := toSysvarSummary(s)
				sum.Central = nh.Central
				out = append(out, sum)
			}
		}
		if out == nil {
			out = []SysvarSummary{}
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetSysvar returns a single sysvar, routing to the central named by
// the `?central=` query parameter.
func GetSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		name := chi.URLParam(r, "name")
		s, ok := h.Sysvar(name)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Sysvar not found", name))
			return
		}
		JSON(w, http.StatusOK, toSysvarSummary(s))
	}
}

// PutSysvar writes a sysvar, routing to the central named by the
// `?central=` query parameter.
func PutSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		name := chi.URLParam(r, "name")
		s, ok := h.Sysvar(name)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Sysvar not found", name))
			return
		}
		var req SysvarSetRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		v, err := hmtypes.NewParamValue(req.Value)
		if err != nil {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Value not supported", err.Error()))
			return
		}
		if err := s.Set(r.Context(), v); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Sysvar write failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func toSysvarSummary(s *hub.Sysvar) SysvarSummary {
	v, ok := s.Value()
	sum := SysvarSummary{
		Central:        s.Central(),
		Name:           s.Name,
		Description:    s.Description,
		Unit:           s.Unit,
		ValueType:      string(s.ValueType),
		Observed:       ok,
		ValueList:      s.ValueList,
		IsInternal:     s.IsInternal,
		IsExtended:     s.IsExtended,
		Vid:            s.Vid,
		EnabledDefault: s.EnabledByDefault(),
	}
	if ok {
		sum.Value = v.Unwrap()
	}
	// H-033: expose declared bounds when the CCU provided them.
	if s.Min != nil {
		f := s.Min.Float
		sum.Min = &f
	}
	if s.Max != nil {
		f := s.Max.Float
		sum.Max = &f
	}
	return sum
}

// ListInbox renders pending-pairing candidates aggregated across all centrals.
// Returns an empty array when no hub is wired or the inbox is empty —
// the SPA renders the same empty-state in both cases.
func ListInbox(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []InboxDeviceDTO{})
			return
		}
		var out []InboxDeviceDTO
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil || nh.Hub.Inbox == nil {
				continue
			}
			for _, e := range nh.Hub.Inbox.List() {
				out = append(out, InboxDeviceDTO{
					Central:      nh.Central,
					Address:      e.Address,
					Model:        e.Model,
					Serial:       e.Serial,
					Manufacturer: e.Manufacturer,
					FirstSeen:    e.FirstSeen,
				})
			}
		}
		if out == nil {
			out = []InboxDeviceDTO{}
		}
		JSON(w, http.StatusOK, out)
	}
}

// --- Alarm / service messages ---

// ListAlarmMessages renders the current alarm set aggregated across all centrals.
func ListAlarmMessages(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []AlarmMessageDTO{})
			return
		}
		var out []AlarmMessageDTO
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			msgs := nh.Hub.Messages.List()
			for i := range msgs {
				m := &msgs[i]
				out = append(out, AlarmMessageDTO{
					Central:     nh.Central,
					ID:          m.ID,
					Name:        m.Name,
					Description: m.Description,
					DeviceName:  m.DeviceName,
					// H-034: address and state_value from model.
					Address:     m.Address,
					StateValue:  m.StateValue,
					Timestamp:   m.Timestamp,
					Counter:     m.Counter,
					LastTrigger: m.LastTrigger,
					Rooms:       m.Rooms,
				})
			}
		}
		if out == nil {
			out = []AlarmMessageDTO{}
		}
		JSON(w, http.StatusOK, out)
	}
}

// ListServiceMessages renders the current service message set aggregated across all centrals.
func ListServiceMessages(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []ServiceMessageDTO{})
			return
		}
		var out []ServiceMessageDTO
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			msgs := nh.Hub.ServiceMessages.List()
			for i := range msgs {
				m := &msgs[i]
				out = append(out, ServiceMessageDTO{
					Central:    nh.Central,
					ID:         m.ID,
					Name:       m.Name,
					Address:    m.Address,
					DeviceName: m.DeviceName,
					Type:       m.Type.String(),
					// H-034: description and priority from model.
					Description: m.Description,
					Priority:    m.Priority,
					Timestamp:   m.Timestamp,
					Counter:     m.Counter,
					Quittable:   m.Quittable,
				})
			}
		}
		if out == nil {
			out = []ServiceMessageDTO{}
		}
		JSON(w, http.StatusOK, out)
	}
}

// AckAlarmMessage acknowledges a single alarm message, routing to the
// central named by the `?central=` query parameter.
func AckAlarmMessage(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		id := chi.URLParam(r, "id")
		if err := h.Messages.Acknowledge(r.Context(), id); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Acknowledge failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// AckServiceMessage acknowledges a single service message, routing to the
// central named by the `?central=` query parameter.
func AckServiceMessage(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		id := chi.URLParam(r, "id")
		if err := h.ServiceMessages.Acknowledge(r.Context(), id); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Acknowledge failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// --- Install mode ---

// GetInstallMode returns the current install-mode state.
func GetInstallMode(ctrl InstallModeController) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ctrl == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Install mode unavailable", "no controller wired"))
			return
		}
		active, remaining := ctrl.InstallModeState()
		JSON(w, http.StatusOK, InstallModeState{Active: active, Seconds: int(remaining.Seconds())})
	}
}

// PostInstallMode toggles install mode.
func PostInstallMode(ctrl InstallModeController) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ctrl == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Install mode unavailable", "no controller wired"))
			return
		}
		var req InstallModeState
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Seconds < 0 {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "seconds must be >= 0", ""))
			return
		}
		if err := ctrl.SetInstallMode(r.Context(), req.Active, time.Duration(req.Seconds)*time.Second); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Install mode write failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// --- Interfaces ---

// InterfaceState is one entry in `GET /interfaces`.
type InterfaceState struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Interface string `json:"interface"`
	CentralID string `json:"central_id,omitempty"`
	Host      string `json:"host,omitempty"`
	Note      string `json:"note,omitempty"`
}

// InterfaceIndex is the facade `interfaces` endpoints use.
type InterfaceIndex interface {
	Interfaces() []InterfaceState
	Interface(id string) (InterfaceState, bool)
	Reconnect(ctx context.Context, id string) error
}

// ListInterfaces renders every configured CCU interface.
func ListInterfaces(idx InterfaceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []InterfaceState{})
			return
		}
		JSON(w, http.StatusOK, idx.Interfaces())
	}
}

// GetInterface returns one interface by id.
func GetInterface(idx InterfaceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Interfaces unavailable", ""))
			return
		}
		iface, ok := idx.Interface(chi.URLParam(r, "id"))
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Interface not found", chi.URLParam(r, "id")))
			return
		}
		JSON(w, http.StatusOK, iface)
	}
}

// ReconnectInterface triggers a reconnect.
func ReconnectInterface(idx InterfaceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Interfaces unavailable", ""))
			return
		}
		if err := idx.Reconnect(r.Context(), chi.URLParam(r, "id")); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Reconnect failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
