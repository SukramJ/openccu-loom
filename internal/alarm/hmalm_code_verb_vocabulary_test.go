// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// hmAlmCodeStore is a codes.Store over a fixed row set.
type hmAlmCodeStore struct{ rows []sqlitestore.AlarmCodeRow }

func (s hmAlmCodeStore) GetAll(context.Context) ([]sqlitestore.AlarmCodeRow, error) {
	return s.rows, nil
}

// hmAlmPINRow builds one enabled PIN row for zone "eg" granting exactly
// the permissions in perms.
func hmAlmPINRow(t *testing.T, pin string, perms codes.Perms) sqlitestore.AlarmCodeRow {
	t.Helper()
	hash, err := codes.HashPIN(pin)
	if err != nil {
		t.Fatalf("HashPIN: %v", err)
	}
	permsJSON, err := json.Marshal(perms)
	if err != nil {
		t.Fatalf("marshal perms: %v", err)
	}
	return sqlitestore.AlarmCodeRow{
		ID: "code1", Name: "Operator", Kind: string(codes.KindPIN), Hash: hash,
		PermsJSON: string(permsJSON), ZonesJSON: `["eg"]`, Enabled: true,
	}
}

// TestWiredCodeValidatorSpeaksTheEngineCodeVerbs pins the code-verb
// vocabulary across the CodeValidator port.
//
// The engine names the verbs ([engine.CodeVerbArm] and friends) and
// passes them as opaque strings; the validator closes the same set in a
// switch whose default is deny. Nothing makes the two agree at compile
// time, and a rename on either side fails closed and silently: every
// coded verb of that kind comes back as engine.ErrInvalidCode, which
// reaches the operator as "wrong code" on a code that is correct.
//
// The test drives the real facade with a code that grants exactly one
// verb, so it fails both ways: a verb the validator no longer
// recognises is denied although it is permitted, and a permission the
// code does not grant must still be refused.
func TestWiredCodeValidatorSpeaksTheEngineCodeVerbs(t *testing.T) {
	t.Parallel()

	const pin = "4711"
	cases := []struct {
		verb    string
		granted codes.Perms
	}{
		{verb: engine.CodeVerbArm, granted: codes.Perms{Arm: true}},
		{verb: engine.CodeVerbDisarm, granted: codes.Perms{Disarm: true}},
		{verb: engine.CodeVerbSilence, granted: codes.Perms{Silence: true}},
	}

	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			t.Parallel()
			var validator engine.CodeValidator = codes.New(codes.Deps{
				Store: hmAlmCodeStore{rows: []sqlitestore.AlarmCodeRow{hmAlmPINRow(t, pin, tc.granted)}},
			})

			// The permission this code grants must authenticate.
			identity, _, err := validator.Validate(t.Context(), "eg", tc.verb, pin, "mqtt")
			if err != nil {
				t.Fatalf("verb %q with the matching permission: %v — the validator does not "+
					"recognise the verb the engine passes", tc.verb, err)
			}
			if identity == "" {
				t.Errorf("verb %q authenticated without an identity", tc.verb)
			}

			// A code granting only this verb must refuse the others, so
			// the pass above cannot come from a validator that permits
			// everything.
			for _, other := range cases {
				if other.verb == tc.verb {
					continue
				}
				if _, _, err := validator.Validate(t.Context(), "eg", other.verb, pin, "mqtt"); !errors.Is(err, engine.ErrInvalidCode) {
					t.Errorf("verb %q against a code granting only %q: err = %v, want ErrInvalidCode",
						other.verb, tc.verb, err)
				}
			}
		})
	}
}
