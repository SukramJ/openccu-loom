// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package payload

import (
	"errors"
	"testing"
	"time"
)

// TestParamTransition pins the one rule every service method reads a
// transition by: absent and zero are none, a positive number of seconds is
// the duration, a negative or non-numeric value is refused.
func TestParamTransition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		params  map[string]any
		want    *time.Duration
		invalid bool
	}{
		{name: "absent", params: map[string]any{}},
		{name: "zero is instant", params: map[string]any{TransitionKey: 0.0}},
		{name: "half a second", params: map[string]any{TransitionKey: 0.5}, want: new(500 * time.Millisecond)},
		{name: "numeric string", params: map[string]any{TransitionKey: "2"}, want: new(2 * time.Second)},
		{name: "negative", params: map[string]any{TransitionKey: -1.0}, invalid: true},
		{name: "not a number", params: map[string]any{TransitionKey: "slow"}, invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParamTransition(tc.params)
			if tc.invalid {
				if !errors.Is(err, ErrServiceInvalidParam) {
					t.Fatalf("err = %v, want ErrServiceInvalidParam", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("got %v, want no transition", *got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Fatalf("got %v, want %v", got, *tc.want)
			}
		})
	}
}
