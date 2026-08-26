// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package interfaces

import "testing"

// TestAcceptInboxOptions_HasConfig locks in the "untouched vs. cleared"
// semantics documented on AcceptInboxOptions: a nil Rooms/Functions slice
// means "leave untouched" (excluded from HasConfig), while an explicit
// empty slice still counts as configuration to apply (the operator asked
// to clear the assignment). IncludeChannels alone never counts — it only
// matters together with Name.
func TestAcceptInboxOptions_HasConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts AcceptInboxOptions
		want bool
	}{
		{name: "zero value", opts: AcceptInboxOptions{}, want: false},
		{
			name: "include_channels alone does not count",
			opts: AcceptInboxOptions{IncludeChannels: true},
			want: false,
		},
		{
			name: "name set",
			opts: AcceptInboxOptions{Name: "Kitchen Switch"},
			want: true,
		},
		{
			name: "nil rooms and functions leave config untouched",
			opts: AcceptInboxOptions{Rooms: nil, Functions: nil},
			want: false,
		},
		{
			name: "non-nil empty rooms slice still counts (explicit clear)",
			opts: AcceptInboxOptions{Rooms: []string{}},
			want: true,
		},
		{
			name: "non-nil empty functions slice still counts (explicit clear)",
			opts: AcceptInboxOptions{Functions: []string{}},
			want: true,
		},
		{
			name: "populated rooms",
			opts: AcceptInboxOptions{Rooms: []string{"Kitchen"}},
			want: true,
		},
		{
			name: "populated functions",
			opts: AcceptInboxOptions{Functions: []string{"Lights"}},
			want: true,
		},
		{
			name: "every field set",
			opts: AcceptInboxOptions{
				Name:            "Kitchen Switch",
				IncludeChannels: true,
				Rooms:           []string{"Kitchen"},
				Functions:       []string{"Lights"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.opts.HasConfig(); got != tt.want {
				t.Errorf("HasConfig() = %v, want %v (opts=%+v)", got, tt.want, tt.opts)
			}
		})
	}
}
