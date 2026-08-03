// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// subjectMaxRunes bounds a subject so it survives a notification title
// on every platform without being truncated mid-word by the consumer.
const subjectMaxRunes = 120

// maxNamedSources bounds how many detector names a message enumerates
// before it switches to a count. Six names is already a long sentence;
// beyond that the count carries more information than the list.
const maxNamedSources = 6

// renderer turns a domain fact into a rendered report.
//
// It renders once, into the daemon's configured locale, and carries the
// key and arguments alongside so a consumer in another locale can
// re-render rather than translate prose. That split is what lets MQTT
// and the webhook — which have no request locale — ship readable text
// while the SPA still answers in the user's language.
type renderer struct {
	cat    *i18n.Catalogs
	locale string
	// baseURL is the operator-facing base of the config UI; empty
	// suppresses the deep link rather than emitting a broken one.
	baseURL string
}

func newRenderer(cat *i18n.Catalogs, locale, baseURL string) *renderer {
	if locale == "" && cat != nil {
		locale = cat.DefaultLocale
	}
	return &renderer{cat: cat, locale: locale, baseURL: strings.TrimSuffix(baseURL, "/")}
}

// reportInput is everything the renderer needs. It is deliberately a
// struct rather than a long parameter list: the call sites differ in
// which fields they can fill, and positional arguments would make a
// missing zone indistinguishable from a missing mode.
type reportInput struct {
	Class      hmenum.SecurityClass
	Verb       hmenum.SecurityVerb
	Sources    []hmevent.SecuritySourceRef
	ZoneID     string
	ZoneSlug   string
	ZoneName   string
	Mode       hmenum.AlarmMode
	IncidentID int64
	Reason     hmenum.SecurityFaultReason
	At         time.Time
	Fault      bool
	Retainable bool
}

// render builds the report.
func (r *renderer) render(in reportInput) security.Notification {
	args := r.args(in)
	key := messageKey(in.Class, in.Verb)
	subject := clampRunes(r.cat.TF(r.locale, subjectKey(in.Class, in.Verb), args), subjectMaxRunes)
	message := r.cat.TF(r.locale, key, args)

	return security.Notification{
		Class:      in.Class,
		Severity:   severityFor(in),
		Verb:       in.Verb,
		Subject:    subject,
		Message:    message,
		I18nKey:    key,
		Args:       args,
		Sources:    in.Sources,
		ZoneID:     in.ZoneID,
		ZoneSlug:   in.ZoneSlug,
		ZoneName:   in.ZoneName,
		Mode:       in.Mode,
		IncidentID: in.IncidentID,
		Link:       r.link(in),
		AtMS:       in.At.UnixMilli(),
		Retainable: in.Retainable,
	}
}

// args builds the placeholder set. Every key is always present, even
// when empty, so a catalogue entry can reference any of them without
// the risk of a stray literal `{zone}` reaching a user.
func (r *renderer) args(in reportInput) map[string]string {
	names := sourceNames(in.Sources)
	args := map[string]string{
		"zone":    in.ZoneName,
		"mode":    in.Mode.String(),
		"count":   strconv.Itoa(len(in.Sources)),
		"time":    in.At.Format("15:04"),
		"date":    in.At.Format("2006-01-02"),
		"reason":  string(in.Reason),
		"class":   string(in.Class),
		"central": firstCentral(in.Sources),
		"sensor":  "",
		"sensors": "",
	}
	if len(names) > 0 {
		args["sensor"] = names[0]
		args["sensors"] = joinNames(names)
	}
	return args
}

// link builds the deep link into the config UI. A zone-scoped report
// links to its zone; everything else lands on the domain overview.
func (r *renderer) link(in reportInput) string {
	if r.baseURL == "" {
		return ""
	}
	if in.ZoneSlug != "" && !in.Fault {
		return r.baseURL + "/app/#/security/zones/" + in.ZoneSlug
	}
	if in.Fault {
		return r.baseURL + "/app/#/security/faults"
	}
	return r.baseURL + "/app/#/security"
}

// severityFor folds the input onto a severity. A cleared condition is
// always OK regardless of class — the class only says what ended.
func severityFor(in reportInput) hmenum.SecuritySeverity {
	if in.Verb == hmenum.SecurityVerbCleared {
		return hmenum.SecuritySeverityOK
	}
	if in.Verb == hmenum.SecurityVerbTest {
		return hmenum.SecuritySeverityInfo
	}
	return hmenum.SeverityForClass(in.Class)
}

// subjectKey and messageKey derive the catalogue keys. Keying on
// (class, verb) rather than on an event type keeps the catalogue at one
// entry per meaningful pair instead of one per producer.
func subjectKey(c hmenum.SecurityClass, v hmenum.SecurityVerb) string {
	return "security.subject." + string(c) + "." + string(v)
}

func messageKey(c hmenum.SecurityClass, v hmenum.SecurityVerb) string {
	return "security.message." + string(c) + "." + string(v)
}

// sourceNames extracts display names, falling back to the channel
// address so a source is never rendered as an empty string.
func sourceNames(refs []hmevent.SecuritySourceRef) []string {
	out := make([]string, 0, len(refs))
	for i := range refs {
		switch {
		case refs[i].Name != "":
			out = append(out, refs[i].Name)
		case refs[i].ChannelAddress != "":
			out = append(out, refs[i].ChannelAddress)
		}
	}
	return out
}

// joinNames renders a name list, switching to a count once the list
// stops being readable as a sentence.
func joinNames(names []string) string {
	if len(names) <= maxNamedSources {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s (+%d)", strings.Join(names[:maxNamedSources], ", "), len(names)-maxNamedSources)
}

// firstCentral returns the central of the first source that names one.
func firstCentral(refs []hmevent.SecuritySourceRef) string {
	for i := range refs {
		if refs[i].Central != "" {
			return refs[i].Central
		}
	}
	return ""
}

// clampRunes truncates on a rune boundary. Byte truncation would split
// a multi-byte character and produce invalid UTF-8 in a notification
// title — German subjects reach the limit with umlauts in them.
func clampRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return strings.TrimRight(string(runes[:maxRunes-1]), " ") + "…"
}
