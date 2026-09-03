// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package linkprofile_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
)

// The specificity score decides which of several matching profiles the
// operator is shown as active: fixed constraints gain one point, loose ones
// (list / range) subtract a hundred, so an all-fixed profile beats one with
// any loose constraint however many parameters the latter pins. The rule is
// stated twice — here, and again over the SPA link schema's own raw-JSON
// constraint type in internal/central/adapter — and nothing measures that the
// two still agree. Every case below is chosen so a change to the weight, the
// sign or the tie order flips the answer rather than merely narrowing a
// margin; the tests over the matching half (fixed / list / range arms) are in
// store_test.go and say nothing about scoring.
//
// Scores are not observable from outside the package, so each case asserts
// the id [Store.MatchActiveProfile] returns — the answer the WS link plane
// actually publishes — rather than the number behind it.
func TestW2StoMatchActiveProfile_SpecificityOrdersMatchingProfiles(t *testing.T) {
	t.Parallel()

	fixed := func(v float64) linkprofile.ParamConstraint {
		return linkprofile.ParamConstraint{ConstraintType: "fixed", Value: &v}
	}
	list := func(vs ...float64) linkprofile.ParamConstraint {
		return linkprofile.ParamConstraint{ConstraintType: "list", Values: vs}
	}
	openRange := func() linkprofile.ParamConstraint {
		return linkprofile.ParamConstraint{ConstraintType: "range"}
	}

	cases := []struct {
		name     string
		receiver string
		sender   string
		profiles []linkprofile.Profile
		values   map[string]any
		want     int
		why      string
	}{
		{
			name:     "one fixed constraint beats three fixed plus one loose",
			receiver: "W2STO_RCV_A",
			sender:   "W2STO_SND_A",
			profiles: []linkprofile.Profile{
				{ID: 1, Params: map[string]linkprofile.ParamConstraint{"A": fixed(1)}},
				{ID: 2, Params: map[string]linkprofile.ParamConstraint{
					"A": fixed(1), "B": fixed(2), "C": fixed(3), "L": list(1, 2),
				}},
			},
			values: map[string]any{"A": 1.0, "B": 2.0, "C": 3.0, "L": 1.0},
			want:   1,
			why: "a single loose constraint costs a hundred points, so profile 2 " +
				"scores 3-100 against profile 1's 1; had the loose penalty been a " +
				"count rather than a weight, profile 2's three fixed constraints " +
				"would have won",
		},
		{
			name:     "among all-fixed profiles the one pinning more parameters wins",
			receiver: "W2STO_RCV_B",
			sender:   "W2STO_SND_B",
			profiles: []linkprofile.Profile{
				{ID: 3, Params: map[string]linkprofile.ParamConstraint{"A": fixed(1)}},
				{ID: 4, Params: map[string]linkprofile.ParamConstraint{"A": fixed(1), "B": fixed(2)}},
			},
			values: map[string]any{"A": 1.0, "B": 2.0},
			want:   4,
			why:    "fixed constraints are additive and both profiles match",
		},
		{
			name:     "among profiles carrying loose constraints the one with fewer wins",
			receiver: "W2STO_RCV_C",
			sender:   "W2STO_SND_C",
			profiles: []linkprofile.Profile{
				{ID: 5, Params: map[string]linkprofile.ParamConstraint{
					"A": fixed(1), "R1": openRange(), "R2": openRange(),
				}},
				{ID: 6, Params: map[string]linkprofile.ParamConstraint{
					"A": fixed(1), "R1": openRange(),
				}},
			},
			values: map[string]any{"A": 1.0, "R1": 99.0, "R2": 99.0},
			want:   6,
			why:    "the penalty is per loose constraint, so 1-200 loses to 1-100",
		},
		{
			name:     "a profile whose constraints do not match cannot win on score",
			receiver: "W2STO_RCV_D",
			sender:   "W2STO_SND_D",
			profiles: []linkprofile.Profile{
				{ID: 7, Params: map[string]linkprofile.ParamConstraint{"A": fixed(1), "B": fixed(2)}},
				{ID: 8, Params: map[string]linkprofile.ParamConstraint{"A": fixed(1)}},
			},
			// B is 9, so the higher-scoring profile 7 is not a match at all.
			values: map[string]any{"A": 1.0, "B": 9.0},
			want:   8,
			why:    "scoring ranks the matches; it never promotes a non-match",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := linkprofile.New()
			s.Register(tc.receiver, tc.sender, tc.profiles)
			if got := s.MatchActiveProfile(tc.receiver, tc.sender, tc.values); got != tc.want {
				t.Errorf("MatchActiveProfile = %d, want %d — %s. "+
					"This is the score internal/central/adapter restates for the SPA "+
					"link schema; changing it here without changing it there makes the "+
					"WS link plane and the UI schema name different active profiles for "+
					"the same link.", got, tc.want, tc.why)
			}
		})
	}
}
