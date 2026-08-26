// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// SecurityDomain is the narrow facade the /security handlers drive.
// *security.Service satisfies it; a nil field unmounts the surface.
type SecurityDomain interface {
	Snapshot() security.Snapshot
	Faults() []security.Fault
	AcknowledgeFault(ctx context.Context, id, by string) (bool, error)
	Sources(ctx context.Context) []security.SourceView
	SetSourceOverride(ctx context.Context, central, interfaceID, channelAddress, parameter string,
		class hmenum.SecurityClass, included bool, note string) error
}

// GetSecuritySnapshot serves the whole domain state.
func GetSecuritySnapshot(d SecurityDomain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil {
			writeSecurityUnavailable(w, r)
			return
		}
		JSON(w, http.StatusOK, apiSecuritySnapshot(d.Snapshot()))
	}
}

// GetSecurityClass serves one class with its full source list.
func GetSecurityClass(d SecurityDomain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil {
			writeSecurityUnavailable(w, r)
			return
		}
		class := hmenum.SecurityClass(chi.URLParam(r, "class"))
		if !class.Valid() {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Unknown security class", string(class)))
			return
		}
		snap := d.Snapshot()
		st, ok := snap.Classes[class]
		if !ok {
			// The class is defined but this installation has no source
			// for it — a 404 says "not here", which is the truth.
			writeSecurityNotFound(w, r)
			return
		}
		JSON(w, http.StatusOK, apiSecurityClass(st))
	}
}

// ListSecurityFaults serves the standing fault ledger.
func ListSecurityFaults(d SecurityDomain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil {
			writeSecurityUnavailable(w, r)
			return
		}
		rows := d.Faults()
		out := make([]hmapi.SecurityFault, 0, len(rows))
		for i := range rows {
			out = append(out, apiSecurityFault(rows[i]))
		}
		JSON(w, http.StatusOK, out)
	}
}

// AcknowledgeSecurityFault marks a standing fault as seen. It never
// clears the fault: the condition is still there, the operator has
// merely stopped needing to be told.
func AcknowledgeSecurityFault(d SecurityDomain, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil {
			writeSecurityUnavailable(w, r)
			return
		}
		id := chi.URLParam(r, "id")
		ok, err := d.AcknowledgeFault(r.Context(), id, identityFromCtx(r.Context()))
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
				"Acknowledge fault failed", err)
			return
		}
		if !ok {
			writeSecurityNotFound(w, r)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "security_fault_ack="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeSecurityUnavailable(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusServiceUnavailable,
		problem.New(problem.TypeInternal, r, "Security domain unavailable",
			"the persistence tier is required for the fault ledger"))
}

func writeSecurityNotFound(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusNotFound, problem.New(problem.TypeNotFound, r, "Not found", ""))
}

func apiSecuritySnapshot(snap security.Snapshot) hmapi.SecuritySnapshot {
	out := hmapi.SecuritySnapshot{
		Severity:      string(snap.Severity),
		EngineHealthy: snap.EngineHealthy,
		IndexHealthy:  snap.IndexHealthy,
		LastAlarm:     apiSecurityNotification(snap.LastAlarm),
		LastFault:     apiSecurityNotification(snap.LastFault),
	}
	// Classes are emitted in the taxonomy's escalation order rather than
	// map order, so a client can render the list without sorting.
	for _, c := range hmenum.SecurityClasses() {
		if st, ok := snap.Classes[c]; ok {
			out.Classes = append(out.Classes, apiSecurityClass(st))
		}
	}
	if out.Classes == nil {
		out.Classes = []hmapi.SecurityClassState{}
	}
	// Zones live in a slug-keyed map, so ranging over it would hand the
	// client a different order on every request and reshuffle the
	// overview's zone cards on every refresh. Slug order is the one
	// deterministic ordering the projection still has — the operator's
	// zone position never reaches this snapshot.
	slugs := make([]string, 0, len(snap.Zones))
	for slug := range snap.Zones {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		out.Zones = append(out.Zones, apiSecurityZone(snap.Zones[slug]))
	}
	for i := range snap.Faults {
		out.Faults = append(out.Faults, apiSecurityFault(snap.Faults[i]))
	}
	return out
}

func apiSecurityClass(st security.ClassState) hmapi.SecurityClassState {
	return hmapi.SecurityClassState{
		Class:    string(st.Class),
		Active:   st.Active,
		Severity: string(st.Severity),
		Sources:  apiSecuritySources(st.Sources),
		Known:    st.Known,
		Centrals: st.Centrals,
		Since:    msToTime(st.SinceMS),
	}
}

func apiSecurityZone(z security.ZoneState) hmapi.SecurityZoneState {
	out := hmapi.SecurityZoneState{
		ID:         z.ID,
		Slug:       z.Slug,
		Name:       z.Name,
		State:      string(z.State),
		Mode:       string(z.Mode),
		Sources:    apiSecuritySources(z.Sources),
		IncidentID: z.IncidentID,
		Since:      msToTime(z.SinceMS),
	}
	if len(z.ByClass) > 0 {
		out.ByClass = make(map[string][]string, len(z.ByClass))
		for c, names := range z.ByClass {
			out.ByClass[string(c)] = names
		}
	}
	return out
}

func apiSecurityFault(f security.Fault) hmapi.SecurityFault {
	return hmapi.SecurityFault{
		ID:             f.ID,
		Class:          string(f.Class),
		Reason:         string(f.Reason),
		Severity:       string(f.Severity),
		Source:         apiSecuritySource(f.Source),
		Since:          msToTime(f.SinceMS),
		AcknowledgedAt: msToTime(f.AcknowledgedAtMS),
		AcknowledgedBy: f.AcknowledgedBy,
	}
}

func apiSecurityNotification(n *security.Notification) *hmapi.SecurityNotification {
	if n == nil {
		return nil
	}
	return &hmapi.SecurityNotification{
		Class:      string(n.Class),
		Severity:   string(n.Severity),
		Verb:       string(n.Verb),
		Subject:    n.Subject,
		Message:    n.Message,
		I18nKey:    n.I18nKey,
		Args:       n.Args,
		Sources:    apiSecuritySources(n.Sources),
		ZoneID:     n.ZoneID,
		ZoneSlug:   n.ZoneSlug,
		ZoneName:   n.ZoneName,
		Mode:       string(n.Mode),
		IncidentID: n.IncidentID,
		Link:       n.Link,
		At:         msToTime(n.AtMS),
	}
}

// apiSecuritySources reuses the alarm source shape so one parser serves
// incidents and security state alike.
func apiSecuritySources(refs []hmevent.SecuritySourceRef) []hmapi.AlarmSource {
	if len(refs) == 0 {
		return nil
	}
	out := make([]hmapi.AlarmSource, 0, len(refs))
	for i := range refs {
		out = append(out, apiSecuritySource(refs[i]))
	}
	return out
}

func apiSecuritySource(r hmevent.SecuritySourceRef) hmapi.AlarmSource {
	return hmapi.AlarmSource{
		Ref:            r.Ref,
		Central:        r.Central,
		InterfaceID:    r.InterfaceID,
		ChannelAddress: r.ChannelAddress,
		DeviceAddress:  r.DeviceAddress,
		Parameter:      r.Parameter,
		SensorID:       r.SensorID,
		Name:           r.Name,
		SensorType:     string(r.SensorType),
		Class:          string(r.Class),
		At:             msToTime(r.AtMS),
	}
}

// ListSecuritySources serves the classified inventory.
//
// Filters narrow by class, central, zone and relevance. The unfiltered
// list is deliberately available: a source the classifier got wrong is
// invisible in every aggregate, so listing everything is the only way
// to find it.
func ListSecuritySources(d SecurityDomain) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil {
			writeSecurityUnavailable(w, r)
			return
		}
		q := r.URL.Query()
		class := q.Get("class")
		if class != "" && !hmenum.SecurityClass(class).Valid() {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Unknown security class", class))
			return
		}
		central := q.Get("central")
		zone := q.Get("zone_id")
		onlyRelevant := q.Get("relevant") == "true"
		onlyActive := q.Get("active") == "true"

		rows := d.Sources(r.Context())
		out := make([]hmapi.SecuritySourceView, 0, len(rows))
		for i := range rows {
			s := &rows[i]
			switch {
			case class != "" && string(s.Class) != class,
				central != "" && s.Central != central,
				zone != "" && s.ZoneID != zone,
				onlyRelevant && !s.Relevant,
				onlyActive && !s.Active:
				continue
			}
			out = append(out, apiSecuritySourceView(*s))
		}
		JSON(w, http.StatusOK, out)
	}
}

// PutSecuritySourceOverride records an operator decision about one data
// point. The ref is the routing key, URL-encoded.
func PutSecuritySourceOverride(d SecurityDomain, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil {
			writeSecurityUnavailable(w, r)
			return
		}
		// The router decodes the path exactly once (the rest package's
		// decodedPathRouting middleware), so chi hands over the final
		// value. Decoding again here rejects a ref carrying a literal
		// '%' and silently rewrites one whose component still looks like
		// an escape.
		ref := chi.URLParam(r, "ref")
		if ref == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid source reference", ""))
			return
		}
		parts := strings.SplitN(ref, "|", 4)
		if len(parts) != 4 {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid source reference",
					"expected <central>|<interface_id>|<channel_address>|<parameter>"))
			return
		}
		var in hmapi.SecuritySourceOverride
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		// An omitted `included` keeps the source included: the request
		// that only names a class must reclassify, never exclude.
		included := true
		if in.Included != nil {
			included = *in.Included
		}
		err := d.SetSourceOverride(r.Context(), parts[0], parts[1], parts[2], parts[3],
			hmenum.SecurityClass(in.Class), included, in.Note)
		switch {
		case err == nil:
		case errors.Is(err, security.ErrInvalidClass):
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Invalid source override", err.Error()))
			return
		default:
			// A persistence failure is not a validation error. Reporting
			// it as 422 with the raw driver text both misclassifies it
			// and hides it: problem.Write logs nothing.
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
				"Save source override failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "security_source="+ref)
		w.WriteHeader(http.StatusNoContent)
	}
}

func apiSecuritySourceView(s security.SourceView) hmapi.SecuritySourceView {
	view := hmapi.SecuritySourceView{
		Ref: s.Ref, Central: s.Central, InterfaceID: s.InterfaceID,
		ChannelAddress: s.ChannelAddress, DeviceAddress: s.DeviceAddress,
		Parameter: s.Parameter, Name: s.Name, Class: string(s.Class),
		Reason: string(s.Reason), Active: s.Active, Relevant: s.Relevant,
		ZoneID: s.ZoneID, Overridden: s.Overridden, Since: msToTime(s.SinceMS),
	}
	// Surface the stored inclusion only when an override exists, so the SPA
	// can seed its toggle from truth. Absent means "no override" — the
	// default-included behaviour holds. PUT semantics are unchanged.
	if s.Overridden {
		included := s.OverrideIncluded
		view.OverrideIncluded = &included
	}
	return view
}
