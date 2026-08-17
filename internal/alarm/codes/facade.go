// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package codes is the domain facade over the alarm_codes store
// (notes/concepts/alarm-concept.md §11, migration 028): argon2id PIN hashing,
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

// Kind classifies an alarm-code row (notes/concepts/alarm-concept.md §11).
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
// kinds (notes/concepts/alarm-concept.md §11). keypad_slot uses
// Central/DeviceAddress/Slot plus the arm target (ArmMode/ZoneID);
// remote_key uses Central/ChannelAddress/Parameter plus the action
// target (Action/ZoneID).
type Binding struct {
	Central        string `json:"central,omitempty"`
	DeviceAddress  string `json:"device_address,omitempty"`
	Slot           int    `json:"slot,omitempty"`
	ArmMode        string `json:"arm_mode,omitempty"`
	ChannelAddress string `json:"channel_address,omitempty"`
	Parameter      string `json:"parameter,omitempty"`
	Action         string `json:"action,omitempty"`
	ZoneID         string `json:"zone_id,omitempty"`
}

// Row is one parsed alarm_codes row, with the argon2id hash stripped:
// hardware-code routing needs identity + binding, never the secret.
type Row struct {
	ID           string
	Name         string
	Kind         Kind
	Duress       bool
	Perms        Perms
	Zones        []string
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
// for hardware-intent routing. It implements engine.CodeValidator and
// engine.DuressMatcher.
type Facade struct {
	store   Store
	journal engine.Journal
	clk     clock.Clock
	log     *slog.Logger
	limiter *rateLimiter
	// probes bounds the work MatchDuress does per source. It is a
	// separate ledger from limiter on purpose: a duress probe must
	// never move the code plane's lockout, and a locked-out code plane
	// must never mute the covert channel.
	probes *rateLimiter
	// memo narrows the per-attempt hash sweep of Validate to one row.
	memo *verifyMemo
}

// The engine resolves the duress matcher by interface assertion on the
// validator it was wired with, so a facade that stopped satisfying the
// port would silently lose duress detection on the no-op verbs rather
// than fail to compile.
var (
	_ engine.CodeValidator = (*Facade)(nil)
	_ engine.DuressMatcher = (*Facade)(nil)
)

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
		probes:  newRateLimiter(),
		memo:    newVerifyMemo(),
	}
}

// pinCandidate is the subset of a pin-kind row Validate needs,
// including the hash — never exposed outside this file.
type pinCandidate struct {
	id, name, hash string
	duress         bool
	perms          Perms
	zones          []string
	validFromMS    int64
	validUntilMS   int64
}

// Validate implements engine.CodeValidator (notes/concepts/alarm-concept.md
// §11). See the engine.CodeValidator doc comment for the full
// contract; in short: a correct code returns its identity + duress
// flag with a nil error, a wrong or missing-but-required code returns
// engine.ErrInvalidCode, and an empty code against an zone with no
// applicable enabled pin code is a no-op pass-through (nil error,
// empty identity) so a code policy can never lock everyone out when no
// codes exist.
func (f *Facade) Validate(ctx context.Context, zoneID, verb, code, source string) (identity string, duress bool, err error) {
	now := f.clk.Now()
	// Operator (break-glass) sources are exempt from rate limiting: the
	// session is already the authenticated factor, so a lockout protects
	// nothing — and short-circuiting here would silently suppress duress
	// detection on a valid duress code entered under coercion, exactly
	// when it matters most (notes/concepts/alarm-concept.md §11/§16).
	opSource := engine.IsOperatorSource(source)
	if !opSource {
		if allowed, remaining := f.limiter.allow(source, now); !allowed {
			f.journalFault(ctx, zoneID, source, "code_locked_out", map[string]any{
				"remaining_s": int(remaining.Seconds()),
			})
			return "", false, engine.ErrInvalidCode
		}
	}

	candidates, err := f.pinCandidates(ctx, zoneID, now.UnixMilli())
	if err != nil {
		return "", false, fmt.Errorf("codes: validate: %w", err)
	}

	if code == "" {
		if len(candidates) == 0 {
			// No applicable pin code exists for this zone: the "codes
			// exist" half of an effective RequireDisarm/RequireArm
			// policy resolves to inert.
			return "", false, nil
		}
		// A code is required here but none was supplied: the verb is
		// still refused, but an absent code is not a wrong guess and must
		// not charge the rate limiter. An HA aggregate ("master") panel
		// disarms code-free and the engine loops it across every zone;
		// each code-required zone would otherwise record a failure, and a
		// handful of zones on one code-free press would lock the source
		// out — refusing the operator's immediately following correct
		// per-zone PIN. Journal the fault for audit, charge nothing.
		f.recordInvalid(ctx, zoneID, source, "code_missing", opSource, false)
		return "", false, engine.ErrInvalidCode
	}

	if c := f.matchCandidate(candidates, code); c != nil {
		if !permits(c.perms, verb) {
			f.journalFault(ctx, zoneID, source, "code_permission_denied", map[string]any{
				"code_id": c.id, "verb": verb,
			})
			return "", false, engine.ErrInvalidCode
		}
		f.limiter.recordSuccess(source)
		return c.name, c.duress, nil
	}

	// A supplied code that matched no candidate is a genuine wrong guess:
	// it charges the limiter, preserving the brute-force lockout.
	f.recordInvalid(ctx, zoneID, source, "invalid_code", opSource, true)
	return "", false, engine.ErrInvalidCode
}

// MatchDuress implements engine.DuressMatcher: it reports whether code
// is an enabled duress code for zoneID that is permitted to run verb,
// and does nothing observable otherwise. No fault is journaled, the
// code plane's lockout ledger is untouched, and no result other than
// the boolean leaves the facade — the engine calls it only where the
// verb is a no-op whatever the code is, so a wrong code must not be
// able to lock the plane out or fill the journal.
//
// The work it does per source is still bounded, through its own probe
// ledger: verifying an argon2id hash is deliberately expensive, and
// this path is reachable by anyone who can publish a disarm. An
// installation without a duress code does no hashing here at all — the
// early return below fires before any VerifyPIN call.
//
// Only a genuinely unknown code charges the probe ledger. A correct
// ordinary PIN is a legitimate action, not a failed duress guess: it
// clears the ledger like the code plane does. Charging it would let the
// household's routine no-op disarms (the aggregate/master panel republishes
// the correct PIN across zones) exhaust the probe budget and mute a real
// duress code entered during that window — the exact scenario the branch
// exists for. So when the code matches no duress row but does match some
// enabled PIN, the ordinary rows are verified too, purely to separate a
// known code from an unknown probe.
func (f *Facade) MatchDuress(ctx context.Context, zoneID, verb, code, source string) (identity string, duress bool) {
	if code == "" {
		return "", false
	}
	now := f.clk.Now()
	candidates, err := f.pinCandidates(ctx, zoneID, now.UnixMilli())
	if err != nil {
		f.log.Error("alarm duress match: load codes failed", "zone", zoneID, "error", err)
		return "", false
	}
	hasDuress := false
	for _, c := range candidates {
		if c.duress && permits(c.perms, verb) {
			hasDuress = true
			break
		}
	}
	if !hasDuress {
		return "", false
	}
	// Operator sources are exempt for the same reason they are exempt
	// from the code-plane limiter: the session is already the
	// authenticated factor, and throttling it would suppress duress
	// detection exactly when it matters.
	limited := !engine.IsOperatorSource(source)
	if limited {
		if allowed, _ := f.probes.allow(source, now); !allowed {
			return "", false
		}
	}
	// Duress-eligible candidates are verified exhaustively first: a
	// duress match fires the covert channel and returns immediately, and
	// checking every one of them before any ordinary row guarantees a
	// duress code is never missed behind an earlier ordinary match on
	// the same code string. Only once duress is ruled out is the
	// remaining set verified, and only until the first match —
	// knownCode only needs to know a code is valid, not which row it
	// matches, so it stops at the first hit instead of hashing every
	// enabled PIN in the installation.
	for _, c := range candidates {
		if !c.duress || !permits(c.perms, verb) {
			continue
		}
		if VerifyPIN(c.hash, code) {
			if limited {
				f.probes.recordSuccess(source)
			}
			return c.name, true
		}
	}
	knownCode := false
	for _, c := range candidates {
		if c.duress && permits(c.perms, verb) {
			continue // already verified above
		}
		if VerifyPIN(c.hash, code) {
			knownCode = true
			break
		}
	}
	if limited {
		if knownCode {
			f.probes.recordSuccess(source)
		} else {
			f.probes.recordFailure(source, now)
		}
	}
	return "", false
}

// HasPINCodes reports whether any enabled, in-validity pin code applies
// to zoneID — the "codes exist" half of the effective code requirement
// (notes/concepts/alarm-concept.md §11). MQTT discovery uses it to advertise
// code_arm_required / code_disarm_required exactly as the engine will
// enforce them: a policy default resolves to required only while such a
// code exists, so HA prompts for a code precisely when one is needed.
func (f *Facade) HasPINCodes(ctx context.Context, zoneID string) bool {
	cands, err := f.pinCandidates(ctx, zoneID, f.clk.Now().UnixMilli())
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

// matchCandidate returns the first candidate whose stored hash the
// supplied code verifies against, or nil when it matches none.
//
// The memo narrows the sweep to a single row for a code that has already
// been resolved against this exact candidate set, and answers "matches
// nothing" without deriving anything at all — a mistyped PIN otherwise
// costs one argon2id derivation per enabled code, every time it is typed.
// The verification itself is never skipped on the accept path: a match
// reported here always comes from a real hash comparison, so the memo can
// deny fast but can never authenticate on its own.
func (f *Facade) matchCandidate(candidates []pinCandidate, code string) *pinCandidate {
	scope := candidateScope(candidates)
	if rowID, known := f.memo.lookup(scope, code); known {
		if rowID == "" {
			return nil
		}
		for i := range candidates {
			if candidates[i].id == rowID && VerifyPIN(candidates[i].hash, code) {
				return &candidates[i]
			}
		}
		// The remembered row no longer verifies: fall through to the full
		// sweep rather than trust the memo about it.
	}
	for i := range candidates {
		if !VerifyPIN(candidates[i].hash, code) {
			continue
		}
		f.memo.remember(scope, code, candidates[i].id)
		return &candidates[i]
	}
	f.memo.remember(scope, code, "")
	return nil
}

// pinCandidates loads every enabled, in-validity-window pin-kind code
// applicable to zoneID (Zones empty means every zone).
func (f *Facade) pinCandidates(ctx context.Context, zoneID string, nowMS int64) ([]pinCandidate, error) {
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
		zones, err := parseZones(r.ZonesJSON)
		if err != nil {
			f.log.Error("alarm code zones_json malformed, skipping", "id", r.ID, "error", err)
			continue
		}
		if len(zones) > 0 && !slices.Contains(zones, zoneID) {
			continue
		}
		out = append(out, pinCandidate{
			id: r.ID, name: r.Name, hash: r.Hash, duress: r.Duress,
			perms: perms, zones: zones,
			validFromMS: r.ValidFromMS, validUntilMS: r.ValidUntilMS,
		})
	}
	return out, nil
}

// recordInvalid registers a wrong or missing-but-required attempt: it
// journals the fault and, when chargeLimiter is set, feeds the rate
// limiter and journals a second entry plus a warning log ("health
// note") if that attempt just engaged a new lockout. Only a genuinely
// wrong/unknown code charges the limiter; a required-but-absent code is
// journaled but never counted, so a code-free aggregate disarm cannot
// lock a source out. Operator sources journal only — their wrong
// attempts must never accumulate toward a lockout that would later
// suppress duress detection.
func (f *Facade) recordInvalid(ctx context.Context, zoneID, source, event string, operatorSource, chargeLimiter bool) {
	f.journalFault(ctx, zoneID, source, event, nil)
	if operatorSource || !chargeLimiter {
		return
	}
	lockout := f.limiter.recordFailure(source, f.clk.Now())
	if lockout <= 0 {
		return
	}
	f.log.Warn("alarm code source locked out", "source", source, "zone", zoneID, "lockout_s", lockout.Seconds())
	f.journalFault(ctx, zoneID, source, "code_lockout", map[string]any{
		"lockout_s": int(lockout.Seconds()),
	})
}

// journalFault appends a fault-class journal entry, logging (never
// blocking on) a journal failure. A nil Journal is a silent no-op.
func (f *Facade) journalFault(ctx context.Context, zoneID, source, event string, details map[string]any) {
	if f.journal == nil {
		return
	}
	if _, err := f.journal.Append(ctx, engine.JournalEntry{
		ZoneID: zoneID, Class: hmenum.AlarmJournalClassFault, Event: event,
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
	zones, err := parseZones(r.ZonesJSON)
	if err != nil {
		return Row{}, fmt.Errorf("zones_json: %w", err)
	}
	var binding Binding
	if r.BindingJSON != "" {
		if err := json.Unmarshal([]byte(r.BindingJSON), &binding); err != nil {
			return Row{}, fmt.Errorf("binding_json: %w", err)
		}
	}
	return Row{
		ID: r.ID, Name: r.Name, Kind: Kind(r.Kind), Duress: r.Duress,
		Perms: perms, Zones: zones, Binding: binding,
		ValidFromMS: r.ValidFromMS, ValidUntilMS: r.ValidUntilMS, Enabled: r.Enabled,
	}, nil
}

// parseZones decodes zones_json, treating an empty string the same as
// "[]" (every zone).
func parseZones(zonesJSON string) ([]string, error) {
	if zonesJSON == "" || zonesJSON == "[]" {
		return nil, nil
	}
	var zones []string
	if err := json.Unmarshal([]byte(zonesJSON), &zones); err != nil {
		return nil, err
	}
	return zones, nil
}
