// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
	"github.com/SukramJ/openccu-loom/internal/restapi"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// rfc3339OrEmpty formats t in RFC3339 when non-zero; returns "" otherwise.
func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// HubIndex is an alias for the canonical interface in internal/restapi.
type HubIndex = restapi.HubIndex

// NamedHub is an alias for the canonical type in internal/restapi.
type NamedHub = restapi.NamedHub

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

// requireMutationHub resolves the hub a mutating request targets and
// writes the standard problem response when it cannot: a nil idx
// means "no hub wired" (503 service_unready); an idx that cannot
// resolve to exactly one hub for the `central` query parameter means
// the request is ambiguous across multiple CCUs (400 bad_request).
// Callers check ok and return immediately when it is false — the
// response has already been written.
func requireMutationHub(w http.ResponseWriter, r *http.Request, idx HubIndex) (*hub.Hub, bool) {
	if idx == nil {
		problem.Write(w, http.StatusServiceUnavailable,
			problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
		return nil, false
	}
	h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
	if h == nil {
		problem.Write(w, http.StatusBadRequest,
			problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
		return nil, false
	}
	return h, true
}

// resolveHubForRead picks the hub for a read-only request that identifies the
// resource by name. When `centralName` is supplied it is used directly.
// When absent and the named resource exists on exactly one central, that
// central is returned — the caller passes a resourceOnHub predicate that
// performs the membership test. Only genuine ambiguity (resource on >1
// central) causes nil to be returned.
func resolveHubForRead(idx HubIndex, centralName string, resourceOnHub func(*hub.Hub) bool) *hub.Hub {
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
	if len(hubs) == 0 {
		return idx.Hub()
	}
	// Multiple centrals: find the one hub on which the resource exists.
	var found *hub.Hub
	for _, nh := range hubs {
		if nh.Hub != nil && resourceOnHub(nh.Hub) {
			if found != nil {
				// Ambiguous: name appears on more than one central.
				return nil
			}
			found = nh.Hub
		}
	}
	return found
}

// ProgramSummary is one entry in `GET /api/v1/programs`.
type ProgramSummary struct {
	// Central is the CCU this program belongs to (multi-CCU grouping).
	Central string `json:"central,omitempty"`
	// UniqueID is the canonical loom-namespaced routing key for this program
	// (the [hub.Program.CanonicalUniqueID] result) — identical to the value on
	// the WS `hub.program_executed` payload. Lets a client seed its entity
	// registry from the summary without recomputing the algorithm. Always
	// present and non-empty (the central's serial is resolved before any
	// entity is served — see [DataPointSummary.UniqueID]).
	UniqueID    string `json:"unique_id"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Active      *bool  `json:"active,omitempty"`
	// ExecuteAvailable reports whether running the program would do
	// anything. A program the CCU has deactivated ignores its triggers and
	// refuses a manual run, so a consumer that offers "run now" as its own
	// control should render it unavailable rather than let the call fail.
	//
	// The daemon answers this rather than leaving each consumer to derive
	// it from `active`: it is CCU semantics, not presentation. True when
	// the flag has not been observed yet, so a control is never greyed out
	// on missing information.
	ExecuteAvailable bool `json:"execute_available"`
	// LastExecuted is the RFC3339 timestamp of the most recent execution,
	// omitted when no execution has been observed yet. Closes H-032.
	LastExecuted string `json:"last_executed,omitempty"`
	// ConditionSummary is a compact, language-neutral rendering of the
	// program's root-rule trigger conditions (object names joined by the
	// symbolic operators ==, >=, <=, >, <, &&, ||). Omitted when the
	// program has no rule or the CCU-side scan produced nothing.
	ConditionSummary string `json:"condition_summary,omitempty"`
	// ActivitySummary is a compact, language-neutral rendering of the
	// program's root-rule activities (object name := value, joined by
	// "; "). Omitted when the program has no rule.
	ActivitySummary string `json:"activity_summary,omitempty"`
	// IsInternal is true for Tmp_*-programs created internally by the CCU.
	IsInternal bool `json:"is_internal,omitempty"`
	// EnabledDefault is true when the program matched a configured description
	// marker (so clients enable its entity by default); false when no markers
	// are configured (entry included but disabled by default). Mirrors the
	// reference stack's marker-driven enabled-by-default resolution.
	EnabledDefault bool `json:"enabled_default,omitempty"`
	// Channel is the canonical channel address ("ADDR:idx") of the device
	// channel this program is associated with (resolved from a device
	// identifier in the program name). Empty when the program belongs to no
	// device — clients then attach the entity to the central hub device
	// instead of a physical device.
	Channel string `json:"channel,omitempty"`
	// DeviceAddress is the device part of Channel (before the ":").
	// Clients use it to group the entity under the owning physical device;
	// when empty the entity belongs on the hub card.
	DeviceAddress string `json:"device_address,omitempty"`
}

// SysvarSummary is one entry in `GET /api/v1/sysvars`.
type SysvarSummary struct {
	// Central is the CCU this system variable belongs to.
	Central string `json:"central,omitempty"`
	// UniqueID is the canonical loom-namespaced routing key for this system
	// variable (the [hub.Sysvar.CanonicalUniqueID] result) — identical to the
	// value on the WS `hub.sysvar_changed` payload. Lets a client seed its
	// entity registry from the summary without recomputing the algorithm.
	// Always present and non-empty (the central's serial is resolved before any
	// entity is served — see [DataPointSummary.UniqueID]).
	UniqueID    string   `json:"unique_id"`
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
	// IsVisible mirrors the CCU's isVisible flag (WebUI visibility);
	// IsLogged mirrors isLogged (whether the CCU archives value changes).
	// Both are always emitted (a real CCU reports them for every variable)
	// so a client can rely on the explicit false as "hidden"/"not logged"
	// rather than an absent field.
	IsVisible bool `json:"is_visible"`
	IsLogged  bool `json:"is_logged"`
	// ValueName0 / ValueName1 are the CCU-side false / true value labels
	// for a binary (LOGIC / ALARM) variable — the operator-visible text
	// for each state. Empty for non-binary variables.
	ValueName0 string `json:"value_name_0,omitempty"`
	ValueName1 string `json:"value_name_1,omitempty"`
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
	// Channel is the canonical channel address ("ADDR:idx") of the device
	// channel this system variable is associated with — either the explicit
	// CCU WebUI channel assignment ("Kanalzuordnung") or, failing that, a
	// device identifier resolved from the variable name. Empty when the
	// variable belongs to no device — clients then attach the entity to the
	// central hub device instead of a physical device.
	Channel string `json:"channel,omitempty"`
	// DeviceAddress is the device part of Channel (before the ":").
	// Clients use it to group the entity under the owning physical device;
	// when empty the entity belongs on the hub card.
	DeviceAddress string `json:"device_address,omitempty"`
}

// SysvarSetRequest is the body of `PUT /sysvars/{name}`.
type SysvarSetRequest struct {
	Value any `json:"value"`
}

// SysvarRefreshService is an alias for the canonical interface in pkg/interfaces.
type SysvarRefreshService = interfaces.SysvarRefreshService

// InboxDeviceDTO is one entry in `GET /api/v1/inbox`.
type InboxDeviceDTO struct {
	// Central is the CCU that reported this pending device.
	Central string `json:"central,omitempty"`
	Address string `json:"address"`
	Model   string `json:"model"`
	// Interface is the CCU interface the device was detected through.
	// The SPA hides the "replace existing device" action for HmIP
	// interfaces, which do not support the swap.
	Interface    string `json:"interface,omitempty"`
	Serial       string `json:"serial,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	FirstSeen    int64  `json:"first_seen,omitempty"`
	// PendingCreation marks an entry this daemon is holding back
	// (`delay_new_device_creation`): the device exists on the CCU but has
	// no data points here until the accept materialises it.
	PendingCreation bool `json:"pending_creation,omitempty"`
}

// AlarmMessageDTO is one entry in the alarm-messages list.
//
// An alarm entry has no device, channel or room — the CCU backs it by an
// alarm system variable, not a device datapoint — so this DTO carries
// only the identity and timing fields the CCU actually reports. See
// [hub.AlarmMessage].
type AlarmMessageDTO struct {
	// Central is the CCU this alarm message belongs to.
	Central     string `json:"central,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Timestamp is when the alarm was raised. Omitted on the rare CCU
	// report that carries no occurrence at all (see [hub.AlarmMessage]).
	Timestamp time.Time `json:"timestamp,omitzero"`
	// LastTimestamp is when the backing alarm variable last changed.
	// Omitted when the CCU reports no such occurrence.
	LastTimestamp time.Time `json:"last_timestamp,omitzero"`
	Counter       int       `json:"counter"`
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
	// Timestamp is when the message first appeared. Omitted on the rare
	// CCU report that carries no occurrence at all.
	Timestamp time.Time `json:"timestamp,omitzero"`
	// LastTimestamp is when the message last recurred. Omitted when the
	// CCU reports no such occurrence.
	LastTimestamp time.Time `json:"last_timestamp,omitzero"`
	Counter       int       `json:"counter"`
	Rooms         []string  `json:"rooms,omitempty"`
	Functions     []string  `json:"functions,omitempty"`
	Quittable     bool      `json:"quittable"`
}

// applyHubPagination slices items according to optional `page` / `per_page`
// query parameters and writes X-Total-Count with the full pre-slice count.
// When neither parameter is present the full slice is returned unchanged,
// preserving backward compatibility for callers that do not paginate.
func applyHubPagination[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	q := r.URL.Query()
	pageStr := q.Get("page")
	perPageStr := q.Get("per_page")
	total := len(items)
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	if pageStr == "" && perPageStr == "" {
		return items
	}
	page, perPage := parsePagination(r)
	start := (page - 1) * perPage
	end := start + perPage
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	return items[start:end]
}

// --- Program handlers ---

// ListPrograms renders the program catalogue aggregated across all centrals.
// Optional `page` / `per_page` query parameters paginate the result; when
// absent every program is returned. The response body is always a flat JSON
// array so existing clients are unaffected; X-Total-Count carries the full
// count.
func ListPrograms(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []ProgramSummary{})
			return
		}
		includeInternal, err := parseOptionalBoolQuery(r)
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid include_internal", err.Error()))
			return
		}
		var out []ProgramSummary
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			serial := idx.SerialSuffix(nh.Central)
			// The hub always holds internal programs; whether they are served
			// is resolved per central: an explicit include_internal wins,
			// otherwise the central's include_internal_programs config default.
			include := effectiveBool(includeInternal, nh.Hub.IncludeInternalProgramsDefault())
			for _, p := range nh.Hub.Programs() {
				if p.IsInternal && !include {
					continue
				}
				out = append(out, toProgramSummary(p, nh.Central, serial))
			}
		}
		if out == nil {
			out = []ProgramSummary{}
		}
		out = applyHubPagination(w, r, out)
		JSON(w, http.StatusOK, out)
	}
}

// parseOptionalBoolQuery reads the include_internal query parameter. It
// returns (nil, nil) when the parameter is absent or empty so callers can
// fall back to a per-request default, and a parse error (surfaced as 400)
// when a non-empty value is not a recognised boolean literal.
func parseOptionalBoolQuery(r *http.Request) (*bool, error) {
	raw := r.URL.Query().Get("include_internal")
	if raw == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// effectiveBool resolves an optional override against a default: the
// override wins when present, otherwise def is used.
func effectiveBool(override *bool, def bool) bool {
	if override != nil {
		return *override
	}
	return def
}

// toProgramSummary maps a hub program onto its REST DTO, tagging it with the
// owning central. Shared by the list endpoint and the single-program GET so
// both render identical shapes.
func toProgramSummary(p *hub.Program, central, serialSuffix string) ProgramSummary {
	active, observed := p.Active()
	e := ProgramSummary{
		Central:     central,
		UniqueID:    p.CanonicalUniqueID(serialSuffix),
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
	}
	e.ExecuteAvailable = true
	if observed {
		v := active
		e.Active = &v
		e.ExecuteAvailable = active
	}
	// H-032: expose last_executed when a run has been observed.
	if ts, hasTS := p.LastExecution(); hasTS {
		e.LastExecuted = rfc3339OrEmpty(ts)
	}
	// M-4: propagate IsInternal so north-bound can filter Tmp_*-programs.
	e.IsInternal = p.IsInternal
	e.EnabledDefault = p.EnabledByDefault()
	e.Channel = p.Channel()
	e.DeviceAddress = p.DeviceAddress()
	e.ConditionSummary, e.ActivitySummary = p.RuleSummary()
	return e
}

// GetProgram returns a single program by id. When `?central=` is supplied the
// request is routed to that central. When absent, the id is looked up across
// all centrals; if exactly one central owns it that central is used. Ambiguity
// (same id on multiple centrals) requires the caller to supply `?central=`.
// Mirrors the GET /sysvars/{name} shape.
func GetProgram(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		id := chi.URLParam(r, "id")
		h := resolveHubForRead(idx, r.URL.Query().Get("central"), func(hh *hub.Hub) bool {
			_, ok := hh.Program(id)
			return ok
		})
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		p, ok := h.Program(id)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Program not found", id))
			return
		}
		JSON(w, http.StatusOK, toProgramSummary(p, h.CentralName, idx.SerialSuffix(h.CentralName)))
	}
}

// ProgramSetEnabledRequest is the body for `PUT /programs/{id}`.
type ProgramSetEnabledRequest struct {
	Active bool `json:"active"`
}

// SetProgramEnabled toggles the program's enabled flag on the CCU.
func SetProgramEnabled(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
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
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := p.SetEnabled(r.Context(), req.Active); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Set enabled failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// SysvarCreateRequest is the body for `POST /sysvars`.
type SysvarCreateRequest struct {
	Name        string   `json:"name"`
	ValueType   string   `json:"value_type"` // BOOL|INTEGER|FLOAT|STRING|ENUM|ALARM
	Unit        string   `json:"unit,omitempty"`
	Min         string   `json:"min,omitempty"`
	Max         string   `json:"max,omitempty"`
	Description string   `json:"description,omitempty"`
	ValueList   []string `json:"value_list,omitempty"`
	// ValueName0 / ValueName1 set the false / true value labels of a
	// binary (BOOL / ALARM) variable. Empty adopts the CCU's own
	// "false" / "true" defaults; ignored for non-binary types.
	ValueName0 string `json:"value_name_0,omitempty"`
	ValueName1 string `json:"value_name_1,omitempty"`
	// ChannelAddress binds the new variable to a device channel
	// ("ADDR:idx", the CCU "Kanalzuordnung"). Empty leaves it unassigned.
	// An address the CCU cannot resolve is rejected with 422.
	ChannelAddress string `json:"channel_address,omitempty"`
}

// sysvarCreateTypes is the set of value_type codes the CCU's
// create_system_variable Rega script and the native JSON-RPC create
// methods understand. It is deliberately narrower than the read-side
// [hmenum.HubValueType] vocabulary (LOGIC/NUMBER/LIST): those are how
// the CCU reports existing variables, not how a new one is requested.
var sysvarCreateTypes = map[string]struct{}{
	"BOOL":    {},
	"INTEGER": {},
	"FLOAT":   {},
	"STRING":  {},
	"ENUM":    {},
	"ALARM":   {},
}

// CreateSysvar provisions a new sysvar on the CCU via the Rega
// `create_system_variable` script. The hub's mirror picks the new
// entry up on the next periodic sync.
func CreateSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
			return
		}
		var req SysvarCreateRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Name == "" || req.ValueType == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "name and value_type are required", ""))
			return
		}
		if _, ok := sysvarCreateTypes[req.ValueType]; !ok {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r,
					"value_type must be one of BOOL, INTEGER, FLOAT, STRING, ENUM, ALARM", req.ValueType))
			return
		}
		if err := h.CreateSysvarRemote(r.Context(), hub.SysvarCreateSpec{
			Name:        req.Name,
			ValueType:   req.ValueType,
			Unit:        req.Unit,
			Min:         req.Min,
			Max:         req.Max,
			Description: req.Description,
			ValueList:   req.ValueList,
			ValueName0:  req.ValueName0,
			ValueName1:  req.ValueName1,
			Channel:     req.ChannelAddress,
		}); err != nil {
			if errors.Is(err, hub.ErrSysvarChannelUnknown) {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "channel_address does not resolve to a device channel", req.ChannelAddress))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Sysvar create failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// SysvarPatchRequest is the body for `PATCH /sysvars/{name}`.
// Every field is optional — empty/missing fields leave the CCU's
// existing metadata untouched. A non-empty Name renames the variable.
// Type changes are not supported via this endpoint; rebuild the sysvar
// (DELETE + POST) instead.
type SysvarPatchRequest struct {
	Name        *string   `json:"name,omitempty"`
	Unit        *string   `json:"unit,omitempty"`
	Min         *string   `json:"min,omitempty"`
	Max         *string   `json:"max,omitempty"`
	ValueList   *[]string `json:"value_list,omitempty"`
	Description *string   `json:"description,omitempty"`
	// ValueName0 / ValueName1 rename the false / true value labels of a
	// binary (LOGIC / ALARM) variable. A present, empty string leaves the
	// label untouched (the CCU rejects a blank label).
	ValueName0 *string `json:"value_name_0,omitempty"`
	ValueName1 *string `json:"value_name_1,omitempty"`
	// IsVisible / IsLogged toggle the CCU WebUI-visibility and archive
	// (DPArchive) flags. Tri-state: an omitted field leaves the flag
	// untouched, a present true/false sets it.
	IsVisible *bool `json:"is_visible,omitempty"`
	IsLogged  *bool `json:"is_logged,omitempty"`
	// ChannelAddress reassigns the CCU "Kanalzuordnung". Tri-state: an
	// omitted field leaves the assignment untouched, an empty string clears
	// it, a channel address ("ADDR:idx") assigns it. An address the CCU
	// cannot resolve is rejected with 422.
	ChannelAddress *string `json:"channel_address,omitempty"`
}

// PatchSysvar updates a sysvar's metadata in place via the Rega
// `update_system_variable` script.
func PatchSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
			return
		}
		name := chi.URLParam(r, "name")
		var req SysvarPatchRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		spec := hub.SysvarUpdateSpec{
			Name:    name,
			Visible: req.IsVisible,
			Logged:  req.IsLogged,
		}
		if req.Name != nil {
			spec.NewName = *req.Name
		}
		if req.Unit != nil {
			spec.Unit = *req.Unit
		}
		if req.Min != nil {
			spec.Min = *req.Min
		}
		if req.Max != nil {
			spec.Max = *req.Max
		}
		if req.Description != nil {
			spec.Description = *req.Description
		}
		if req.ValueList != nil {
			spec.ValueList = *req.ValueList
		}
		if req.ValueName0 != nil {
			spec.ValueName0 = *req.ValueName0
		}
		if req.ValueName1 != nil {
			spec.ValueName1 = *req.ValueName1
		}
		if req.ChannelAddress != nil {
			spec.Channel = req.ChannelAddress
		}
		if err := h.UpdateSysvarRemote(r.Context(), spec); err != nil {
			if errors.Is(err, hub.ErrSysvarChannelUnknown) {
				detail := ""
				if req.ChannelAddress != nil {
					detail = *req.ChannelAddress
				}
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "channel_address does not resolve to a device channel", detail))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Sysvar update failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// DeleteSysvar removes a sysvar from the CCU and drops the local
// mirror once the call lands.
func DeleteSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
			return
		}
		name := chi.URLParam(r, "name")
		if err := h.DeleteSysvarRemote(r.Context(), name); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Sysvar delete failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// FetchSysvars force re-pulls the CCU sysvar catalogue and refreshes the
// hub model. The optional `?central=` query parameter scopes the refresh
// to one central; absent, every registered central is refreshed. Mirrors
// the Python reference's fetch_system_variables.
func FetchSysvars(svc SysvarRefreshService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Sysvar refresh unavailable", "no service wired"))
			return
		}
		central := r.URL.Query().Get("central")
		if err := svc.FetchSystemVariables(r.Context(), central); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Sysvar fetch failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// ProgramExecuteRequest is the optional body for
// `POST /programs/{id}/execute`.
type ProgramExecuteRequest struct {
	// CheckConditions gates execution on the program's "if" condition: when
	// true the CCU evaluates the condition and runs the program only when it
	// is satisfied. When false (the default) the program runs unconditionally.
	CheckConditions bool `json:"check_conditions"`
}

// ProgramExecuteResponse is the body returned by
// `POST /programs/{id}/execute`.
type ProgramExecuteResponse struct {
	// Executed reports whether the program actually ran. It is always true
	// for an unconditional execution (check_conditions=false); for a
	// condition-checked execution it is false when the condition was not met.
	Executed bool `json:"executed"`
}

// ExecuteProgram triggers a CCU program. The optional body carries
// check_conditions: when true the program's "if" condition is evaluated on
// the CCU and the program runs only when the condition is satisfied. The
// response reports whether the program executed.
func ExecuteProgram(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		p, ok := h.Program(id)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Program not found", id))
			return
		}
		var req ProgramExecuteRequest
		if err := decodeOptionalJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		// Stamp the surface so the program-execute audit/log subscriber
		// can attribute the run to the REST API.
		ctx := reqctx.WithOperation(r.Context(), "rest:program-execute")
		if req.CheckConditions {
			executed, err := p.ExecuteWithConditionCheck(ctx)
			if err != nil {
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Execute failed", err)
				return
			}
			JSON(w, http.StatusAccepted, ProgramExecuteResponse{Executed: executed})
			return
		}
		if err := p.Execute(ctx); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Execute failed", err)
			return
		}
		JSON(w, http.StatusAccepted, ProgramExecuteResponse{Executed: true})
	}
}

// DeleteProgram removes a program from the CCU and drops the local mirror
// once the call lands. Admin-gated (parity with DELETE /devices) because
// deletion is irreversible. Returns 404 when the program is unknown, 204
// on success. Records an audit entry on success.
func DeleteProgram(idx HubIndex, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		p, ok := h.Program(id)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Program not found", id))
			return
		}
		name := p.Name
		if err := h.DeleteProgramRemote(r.Context(), id); err != nil {
			writeProgramDeleteError(w, r, err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionProgramDelete,
				Note:   "delete program " + name + " (" + id + ")",
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeProgramDeleteError maps the hub sentinels a program delete can
// return onto HTTP problem responses: an unknown / already-gone program is
// 404, an execute-only writer is 503, and any other CCU-side failure is a
// 502 upstream error.
func writeProgramDeleteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, hub.ErrProgramNotFound):
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Program not found", err.Error()))
	case errors.Is(err, hub.ErrProgramDeleteUnsupported):
		problem.Write(w, http.StatusServiceUnavailable,
			problem.New(problem.TypeServiceUnready, r, "Program deletion unavailable", err.Error()))
	default:
		writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Program delete failed", err)
	}
}

// --- Sysvar handlers ---

// ListSysvars renders every registered sysvar aggregated across all centrals.
// Optional `page` / `per_page` query parameters paginate the result; when
// absent every sysvar is returned. The response body is always a flat JSON
// array so existing clients are unaffected; X-Total-Count carries the full
// count.
func ListSysvars(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []SysvarSummary{})
			return
		}
		var out []SysvarSummary
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			serial := idx.SerialSuffix(nh.Central)
			for _, s := range nh.Hub.Sysvars() {
				sum := toSysvarSummary(s, serial)
				sum.Central = nh.Central
				out = append(out, sum)
			}
		}
		if out == nil {
			out = []SysvarSummary{}
		}
		out = applyHubPagination(w, r, out)
		JSON(w, http.StatusOK, out)
	}
}

// GetSysvar returns a single sysvar. When `?central=` is supplied the request
// is routed to that central. When absent, the sysvar name is looked up across
// all centrals; if exactly one central owns it that central is used. Ambiguity
// (same name on multiple centrals) requires the caller to supply `?central=`.
func GetSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		name := chi.URLParam(r, "name")
		h := resolveHubForRead(idx, r.URL.Query().Get("central"), func(hh *hub.Hub) bool {
			_, ok := hh.Sysvar(name)
			return ok
		})
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		s, ok := h.Sysvar(name)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Sysvar not found", name))
			return
		}
		JSON(w, http.StatusOK, toSysvarSummary(s, idx.SerialSuffix(h.CentralName)))
	}
}

// SysvarUsageProgramDTO is one CCU program that references a system
// variable, enriched from the hub's program registry where available.
type SysvarUsageProgramDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// UniqueID is the canonical loom routing key when the program is known
	// to the hub registry; empty when only the ReGa-supplied name is known.
	UniqueID string `json:"unique_id,omitempty"`
	// Active is the observed enabled state; omitted when unknown.
	Active *bool `json:"active,omitempty"`
	// IsInternal marks Tmp_*-programs created internally by the CCU.
	IsInternal bool `json:"is_internal,omitempty"`
}

// SysvarUsageResponse is the body of GET /sysvars/{name}/usage.
type SysvarUsageResponse struct {
	Central  string                  `json:"central,omitempty"`
	Sysvar   string                  `json:"sysvar"`
	Programs []SysvarUsageProgramDTO `json:"programs"`
}

// GetSysvarUsage lists the CCU programs that reference a system variable
// (resolved from the program rules), enriched against the hub's program
// registry. Read-only; a delete-confirmation warning consumes it.
func GetSysvarUsage(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		name := chi.URLParam(r, "name")
		h := resolveHubForRead(idx, r.URL.Query().Get("central"), func(hh *hub.Hub) bool {
			_, ok := hh.Sysvar(name)
			return ok
		})
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		if _, ok := h.Sysvar(name); !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Sysvar not found", name))
			return
		}
		usage, err := h.SysvarUsageRemote(r.Context(), name)
		if err != nil {
			if errors.Is(err, hub.ErrNoSysvarUsageReader) {
				problem.Write(w, http.StatusServiceUnavailable,
					problem.New(problem.TypeServiceUnready, r, "Sysvar usage lookup not available", ""))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable,
				"Sysvar usage lookup failed", err)
			return
		}
		serial := idx.SerialSuffix(h.CentralName)
		programs := make([]SysvarUsageProgramDTO, 0, len(usage))
		for _, u := range usage {
			dto := SysvarUsageProgramDTO{ID: u.ID, Name: u.Name}
			active := u.Active
			if p, ok := h.Program(u.ID); ok {
				if p.Name != "" {
					dto.Name = p.Name
				}
				dto.UniqueID = p.CanonicalUniqueID(serial)
				dto.IsInternal = p.IsInternal
				if a, observed := p.Active(); observed {
					active = a
				}
			}
			dto.Active = &active
			programs = append(programs, dto)
		}
		JSON(w, http.StatusOK, SysvarUsageResponse{Central: h.CentralName, Sysvar: name, Programs: programs})
	}
}

// PutSysvar writes a sysvar, routing to the central named by the
// `?central=` query parameter.
func PutSysvar(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
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
			problem.Write(w, DecodeJSONStatus(err),
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
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Sysvar write failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func toSysvarSummary(s *hub.Sysvar, serialSuffix string) SysvarSummary {
	v, ok := s.Value()
	sum := SysvarSummary{
		Central:        s.Central(),
		UniqueID:       s.CanonicalUniqueID(serialSuffix),
		Name:           s.Name,
		Description:    s.Description,
		Unit:           s.Unit,
		ValueType:      string(s.ValueType),
		Observed:       ok,
		ValueList:      s.ValueList,
		IsInternal:     s.IsInternal,
		IsVisible:      s.IsVisible,
		IsLogged:       s.IsLogged,
		ValueName0:     s.ValueName0,
		ValueName1:     s.ValueName1,
		IsExtended:     s.IsExtended,
		Vid:            s.Vid,
		EnabledDefault: s.EnabledByDefault(),
		Channel:        s.Channel(),
		DeviceAddress:  s.DeviceAddress(),
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
			inbox := nh.Hub.Inbox.List()
			for i := range inbox {
				e := &inbox[i]
				out = append(out, InboxDeviceDTO{
					Central:         nh.Central,
					Address:         e.Address,
					Model:           e.Model,
					Interface:       e.Interface,
					Serial:          e.Serial,
					Manufacturer:    e.Manufacturer,
					FirstSeen:       e.FirstSeen,
					PendingCreation: e.PendingCreation,
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
// Optional `page` / `per_page` query parameters paginate the result; when
// absent every alarm is returned. The response body is always a flat JSON
// array so existing clients are unaffected; X-Total-Count carries the full
// count.
func ListAlarmMessages(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
					Central:       nh.Central,
					ID:            m.ID,
					Name:          m.Name,
					Description:   m.Description,
					Timestamp:     m.Timestamp,
					LastTimestamp: m.LastTimestamp,
					Counter:       m.Counter,
				})
			}
		}
		if out == nil {
			out = []AlarmMessageDTO{}
		}
		out = applyHubPagination(w, r, out)
		JSON(w, http.StatusOK, out)
	}
}

// ListServiceMessages renders the current service message set aggregated across all centrals.
// Optional `page` / `per_page` query parameters paginate the result; when
// absent every message is returned. The response body is always a flat JSON
// array so existing clients are unaffected; X-Total-Count carries the full
// count.
func ListServiceMessages(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
					Central:       nh.Central,
					ID:            m.ID,
					Name:          m.Name,
					Address:       m.Address,
					DeviceName:    m.DeviceName,
					Type:          m.Type.String(),
					Timestamp:     m.Timestamp,
					LastTimestamp: m.LastTimestamp,
					Counter:       m.Counter,
					Rooms:         m.Rooms,
					Functions:     m.Functions,
					Quittable:     m.Quittable,
				})
			}
		}
		if out == nil {
			out = []ServiceMessageDTO{}
		}
		out = applyHubPagination(w, r, out)
		JSON(w, http.StatusOK, out)
	}
}

// AckAlarmMessage acknowledges a single alarm message, routing to the
// central named by the `?central=` query parameter.
func AckAlarmMessage(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		if err := h.Messages.Acknowledge(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Acknowledge failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// AckServiceMessage acknowledges a single service message, routing to the
// central named by the `?central=` query parameter.
func AckServiceMessage(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		if err := h.ServiceMessages.Acknowledge(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Acknowledge failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// AckAllResult is the response body for the bulk acknowledge endpoints:
// the total number of messages acknowledged across the scoped centrals.
type AckAllResult struct {
	Acknowledged int `json:"acknowledged"`
}

// AckAllAlarmMessages acknowledges every active alarm message. An optional
// `?central=` query parameter scopes the operation to one CCU; when omitted
// every registered central is acknowledged. Returns the total count.
func AckAllAlarmMessages(idx HubIndex) http.HandlerFunc {
	return ackAllMessagesHandler(idx, func(ctx context.Context, h *hub.Hub) (int, error) {
		return h.Messages.AcknowledgeAll(ctx)
	})
}

// AckAllServiceMessages acknowledges every quittable service message across
// the scoped centrals. Central scoping matches [AckAllAlarmMessages].
func AckAllServiceMessages(idx HubIndex) http.HandlerFunc {
	return ackAllMessagesHandler(idx, func(ctx context.Context, h *hub.Hub) (int, error) {
		return h.ServiceMessages.AcknowledgeAll(ctx)
	})
}

// ackAllMessagesHandler is the shared body of the two bulk-acknowledge
// endpoints. It iterates the hubs the request targets (all registered
// centrals, or the single one named by `?central=`), sums the acknowledged
// counts, and returns them. A named-but-unknown central is a 400.
func ackAllMessagesHandler(idx HubIndex, ackAll func(context.Context, *hub.Hub) (int, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		central := r.URL.Query().Get("central")
		total := 0
		matched := false
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			if central != "" && nh.Central != central {
				continue
			}
			matched = true
			n, err := ackAll(r.Context(), nh.Hub)
			if err != nil {
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Acknowledge failed", err)
				return
			}
			total += n
		}
		if central != "" && !matched {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central unknown", ""))
			return
		}
		JSON(w, http.StatusOK, AckAllResult{Acknowledged: total})
	}
}

// DisableServiceMessage durably suppresses a single service message,
// routing to the central named by the `?central=` query parameter. It
// resolves the message's channel + service parameter and calls the CCU's
// Interface.suppressServiceMessages so the parameter stops raising
// service messages until it is unsuppressed
// ([UnsuppressServiceMessage]). Shares the domain call
// ([hub.ServiceMessages.Disable]) with the WS `service_messages.disable`
// command.
func DisableServiceMessage(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		if err := h.ServiceMessages.Disable(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Disable failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// SuppressedServiceMessageDTO is one entry in the suppressed-messages
// management list (`GET /api/v1/service-messages/suppressed`).
type SuppressedServiceMessageDTO struct {
	// Central is the CCU this suppression belongs to.
	Central string `json:"central,omitempty"`
	// Interface is the CCU interface the channel lives on (e.g. "HmIP-RF").
	Interface string `json:"interface,omitempty"`
	// Channel is the suppressed channel address ("ADDR:chn").
	Channel string `json:"channel"`
	// Parameter is the suppressed service parameter (e.g. "LOWBAT");
	// empty means every service parameter of the channel is suppressed.
	Parameter string `json:"parameter,omitempty"`
	// DeviceName is the human-readable channel/device name, when known.
	DeviceName string `json:"device_name,omitempty"`
	// Name is the raw CCU message name that was suppressed, when known.
	Name string `json:"name,omitempty"`
}

// ListSuppressedServiceMessages renders the durably-suppressed service
// messages aggregated across all centrals. The list is reconciled against
// each CCU's live Interface.getSuppressedServiceMessages so entries
// cleared elsewhere drop out. Returns an empty array when nothing is
// suppressed or no hub is wired.
func ListSuppressedServiceMessages(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []SuppressedServiceMessageDTO{})
			return
		}
		out := []SuppressedServiceMessageDTO{}
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil || nh.Hub.ServiceMessages == nil {
				continue
			}
			for _, s := range nh.Hub.ServiceMessages.Suppressed(r.Context()) {
				out = append(out, SuppressedServiceMessageDTO{
					Central:    nh.Central,
					Interface:  s.InterfaceID,
					Channel:    s.Channel,
					Parameter:  s.Parameter,
					DeviceName: s.DeviceName,
					Name:       s.Name,
				})
			}
		}
		JSON(w, http.StatusOK, out)
	}
}

// UnsuppressRequest is the body of `POST /service-messages/unsuppress`.
type UnsuppressRequest struct {
	Interface string `json:"interface,omitempty"`
	Channel   string `json:"channel"`
	Parameter string `json:"parameter,omitempty"`
}

// UnsuppressServiceMessage clears a durable suppression, routing to the
// central named by the `?central=` query parameter. The body names the
// channel (required) and the service parameter (empty = all parameters of
// the channel); the interface is optional and resolved from the stored
// suppression when omitted.
func UnsuppressServiceMessage(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := requireMutationHub(w, r, idx)
		if !ok {
			return
		}
		var req UnsuppressRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Channel == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "channel is required", ""))
			return
		}
		if err := h.ServiceMessages.Unsuppress(r.Context(), req.Interface, req.Channel, req.Parameter); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Unsuppress failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// --- Interfaces ---

// InterfaceState is an alias for the canonical DTO in pkg/hmapi.
type InterfaceState = hmapi.InterfaceState

// InterfaceIndex is an alias for the canonical interface in internal/restapi.
type InterfaceIndex = restapi.InterfaceIndex

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
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Reconnect failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
