// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package codes is the domain facade over the alarm_codes store
// (docs/alarm-concept.md §11, migration 028): argon2id PIN hashing,
// code authentication with per-source rate limiting, and the parsed
// row projection consumed by keypad/remote intent routing. Facade
// implements the engine's CodeValidator port directly; it deliberately
// does not import package alarm (which constructs it) — the intent
// router's row shape lives there and is mapped from this package's Row
// by a small adapter at the construction site, avoiding an import
// cycle.
package codes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Kind classifies an alarm-code row (docs/alarm-concept.md §11).
// Mirrors alarm.CodeKind field-for-field so the service-layer adapter
// can convert without ambiguity.
type Kind string

// Kind values.
const (
	KindPIN        Kind = "pin"
	KindKeypadSlot Kind = "keypad_slot"
	KindRemoteKey  Kind = "remote_key"
)

// Perms are the per-code verb permissions (perms_json).
type Perms struct {
	Arm     bool `json:"arm"`
	Disarm  bool `json:"disarm"`
	Silence bool `json:"silence"`
}

// Binding is the parsed binding_json union for the hardware code
// kinds (docs/alarm-concept.md §11). keypad_slot uses
// Central/DeviceAddress/Slot plus the arm target (ArmMode/AreaID);
// remote_key uses Central/ChannelAddress/Parameter plus the action
// target (Action/AreaID).
type Binding struct {
	Central        string `json:"central,omitempty"`
	DeviceAddress  string `json:"device_address,omitempty"`
	Slot           int    `json:"slot,omitempty"`
	ArmMode        string `json:"arm_mode,omitempty"`
	ChannelAddress string `json:"channel_address,omitempty"`
	Parameter      string `json:"parameter,omitempty"`
	Action         string `json:"action,omitempty"`
	AreaID         string `json:"area_id,omitempty"`
}

// Row is one parsed alarm_codes row, with the argon2id hash stripped:
// hardware-code routing needs identity + binding, never the secret.
type Row struct {
	ID           string
	Name         string
	Kind         Kind
	Duress       bool
	Perms        Perms
	Areas        []string
	Binding      Binding
	ValidFromMS  int64
	ValidUntilMS int64
	Enabled      bool
}

// Store is the persistence surface the facade needs. Satisfied by
// *sqlitestore.AlarmCodeStore.
type Store interface {
	GetAll(ctx context.Context) ([]sqlitestore.AlarmCodeRow, error)
}

// Deps wires a Facade.
type Deps struct {
	Store Store
	// Journal receives invalid-code and lockout fault entries via the
	// engine's Journal port. A nil Journal disables journaling (the
	// facade still authenticates and rate-limits normally).
	Journal engine.Journal
	Clock   clock.Clock
	Logger  *slog.Logger
}

// Facade authenticates alarm codes and projects the parsed code rows
// for hardware-intent routing. It implements engine.CodeValidator.
type Facade struct {
	store   Store
	journal engine.Journal
	clk     clock.Clock
	log     *slog.Logger
	limiter *rateLimiter
}

// New constructs a Facade over deps.
func New(deps Deps) *Facade {
	clk := deps.Clock
	if clk == nil {
		clk = clock.New()
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Facade{
		store:   deps.Store,
		journal: deps.Journal,
		clk:     clk,
		log:     logger,
		limiter: newRateLimiter(),
	}
}

// pinCandidate is the subset of a pin-kind row Validate needs,
// including the hash — never exposed outside this file.
type pinCandidate struct {
	id, name, hash string
	duress         bool
	perms          Perms
	areas          []string
	validFromMS    int64
	validUntilMS   int64
}

// Validate implements engine.CodeValidator (docs/alarm-concept.md
// §11). See the engine.CodeValidator doc comment for the full
// contract; in short: a correct code returns its identity + duress
// flag with a nil error, a wrong or missing-but-required code returns
// engine.ErrInvalidCode, and an empty code against an area with no
// applicable enabled pin code is a no-op pass-through (nil error,
// empty identity) so a code policy can never lock everyone out when no
// codes exist.
func (f *Facade) Validate(ctx context.Context, areaID, verb, code, source string) (identity string, duress bool, err error) {
	now := f.clk.Now()
	// Operator (break-glass) sources are exempt from rate limiting: the
	// session is already the authenticated factor, so a lockout protects
	// nothing — and short-circuiting here would silently suppress duress
	// detection on a valid duress code entered under coercion, exactly
	// when it matters most (docs/alarm-concept.md §11/§16).
	opSource := engine.IsOperatorSource(source)
	if !opSource {
		if allowed, remaining := f.limiter.allow(source, now); !allowed {
			f.journalFault(ctx, areaID, source, "code_locked_out", map[string]any{
				"remaining_s": int(remaining.Seconds()),
			})
			return "", false, engine.ErrInvalidCode
		}
	}

	candidates, err := f.pinCandidates(ctx, areaID, now.UnixMilli())
	if err != nil {
		return "", false, fmt.Errorf("codes: validate: %w", err)
	}

	if code == "" {
		if len(candidates) == 0 {
			// No applicable pin code exists for this area: the "codes
			// exist" half of an effective RequireDisarm/RequireArm
			// policy resolves to inert.
			return "", false, nil
		}
		f.recordInvalid(ctx, areaID, source, "code_missing", opSource)
		return "", false, engine.ErrInvalidCode
	}

	for _, c := range candidates {
		if !VerifyPIN(c.hash, code) {
			continue
		}
		if !permits(c.perms, verb) {
			f.journalFault(ctx, areaID, source, "code_permission_denied", map[string]any{
				"code_id": c.id, "verb": verb,
			})
			return "", false, engine.ErrInvalidCode
		}
		f.limiter.recordSuccess(source)
		return c.name, c.duress, nil
	}

	f.recordInvalid(ctx, areaID, source, "invalid_code", opSource)
	return "", false, engine.ErrInvalidCode
}

// HasPINCodes reports whether any enabled, in-validity pin code applies
// to areaID — the "codes exist" half of the effective code requirement
// (docs/alarm-concept.md §11). MQTT discovery uses it to advertise
// code_arm_required / code_disarm_required exactly as the engine will
// enforce them: a policy default resolves to required only while such a
// code exists, so HA prompts for a code precisely when one is needed.
func (f *Facade) HasPINCodes(ctx context.Context, areaID string) bool {
	cands, err := f.pinCandidates(ctx, areaID, f.clk.Now().UnixMilli())
	return err == nil && len(cands) > 0
}

// Rows returns every parsed alarm-code row for hardware-intent routing
// (keypad_slot / remote_key kinds; pin rows are included too, minus
// their hash). A row whose JSON columns fail to parse is skipped and
// logged rather than failing the whole call.
func (f *Facade) Rows(ctx context.Context) ([]Row, error) {
	dbRows, err := f.store.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("codes: load rows: %w", err)
	}
	out := make([]Row, 0, len(dbRows))
	for i := range dbRows {
		row, err := parseRow(&dbRows[i])
		if err != nil {
			f.log.Error("alarm code row malformed, skipping", "id", dbRows[i].ID, "error", err)
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

// pinCandidates loads every enabled, in-validity-window pin-kind code
// applicable to areaID (Areas empty means every area).
func (f *Facade) pinCandidates(ctx context.Context, areaID string, nowMS int64) ([]pinCandidate, error) {
	dbRows, err := f.store.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("load rows: %w", err)
	}
	var out []pinCandidate
	for i := range dbRows {
		r := &dbRows[i]
		if r.Kind != string(KindPIN) || !r.Enabled {
			continue
		}
		if r.ValidFromMS != 0 && nowMS < r.ValidFromMS {
			continue
		}
		if r.ValidUntilMS != 0 && nowMS > r.ValidUntilMS {
			continue
		}
		var perms Perms
		if r.PermsJSON != "" {
			if err := json.Unmarshal([]byte(r.PermsJSON), &perms); err != nil {
				f.log.Error("alarm code perms_json malformed, skipping", "id", r.ID, "error", err)
				continue
			}
		}
		areas, err := parseAreas(r.AreasJSON)
		if err != nil {
			f.log.Error("alarm code areas_json malformed, skipping", "id", r.ID, "error", err)
			continue
		}
		if len(areas) > 0 && !slices.Contains(areas, areaID) {
			continue
		}
		out = append(out, pinCandidate{
			id: r.ID, name: r.Name, hash: r.Hash, duress: r.Duress,
			perms: perms, areas: areas,
			validFromMS: r.ValidFromMS, validUntilMS: r.ValidUntilMS,
		})
	}
	return out, nil
}

// recordInvalid registers a wrong or missing-but-required attempt: it
// journals the fault, feeds the rate limiter, and journals a second
// entry plus a warning log ("health note") if that attempt just
// engaged a new lockout. Operator sources journal only — their wrong
// attempts must never accumulate toward a lockout that would later
// suppress duress detection.
func (f *Facade) recordInvalid(ctx context.Context, areaID, source, event string, operatorSource bool) {
	f.journalFault(ctx, areaID, source, event, nil)
	if operatorSource {
		return
	}
	lockout := f.limiter.recordFailure(source, f.clk.Now())
	if lockout <= 0 {
		return
	}
	f.log.Warn("alarm code source locked out", "source", source, "area", areaID, "lockout_s", lockout.Seconds())
	f.journalFault(ctx, areaID, source, "code_lockout", map[string]any{
		"lockout_s": int(lockout.Seconds()),
	})
}

// journalFault appends a fault-class journal entry, logging (never
// blocking on) a journal failure. A nil Journal is a silent no-op.
func (f *Facade) journalFault(ctx context.Context, areaID, source, event string, details map[string]any) {
	if f.journal == nil {
		return
	}
	if _, err := f.journal.Append(ctx, engine.JournalEntry{
		AreaID: areaID, Class: hmenum.AlarmJournalClassFault, Event: event,
		Source: source, Details: details,
	}); err != nil {
		f.log.Error("alarm code journal append failed", "event", event, "error", err)
	}
}

// permits reports whether p grants verb ("arm" | "disarm" | "silence").
// An unrecognized verb is denied by default.
func permits(p Perms, verb string) bool {
	switch verb {
	case "arm":
		return p.Arm
	case "disarm":
		return p.Disarm
	case "silence":
		return p.Silence
	default:
		return false
	}
}

// parseRow decodes one raw store row into its Row projection, minus
// the hash.
func parseRow(r *sqlitestore.AlarmCodeRow) (Row, error) {
	var perms Perms
	if r.PermsJSON != "" {
		if err := json.Unmarshal([]byte(r.PermsJSON), &perms); err != nil {
			return Row{}, fmt.Errorf("perms_json: %w", err)
		}
	}
	areas, err := parseAreas(r.AreasJSON)
	if err != nil {
		return Row{}, fmt.Errorf("areas_json: %w", err)
	}
	var binding Binding
	if r.BindingJSON != "" {
		if err := json.Unmarshal([]byte(r.BindingJSON), &binding); err != nil {
			return Row{}, fmt.Errorf("binding_json: %w", err)
		}
	}
	return Row{
		ID: r.ID, Name: r.Name, Kind: Kind(r.Kind), Duress: r.Duress,
		Perms: perms, Areas: areas, Binding: binding,
		ValidFromMS: r.ValidFromMS, ValidUntilMS: r.ValidUntilMS, Enabled: r.Enabled,
	}, nil
}

// parseAreas decodes areas_json, treating an empty string the same as
// "[]" (every area).
func parseAreas(areasJSON string) ([]string, error) {
	if areasJSON == "" || areasJSON == "[]" {
		return nil, nil
	}
	var areas []string
	if err := json.Unmarshal([]byte(areasJSON), &areas); err != nil {
		return nil, err
	}
	return areas, nil
}
