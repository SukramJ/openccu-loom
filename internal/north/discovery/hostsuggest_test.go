// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package discovery

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestHostSuggesterSuggest(t *testing.T) {
	t.Parallel()

	errLookup := errors.New("resolver error")

	neverCalled := func(_ context.Context, _ string) ([]string, error) {
		t.Error("LookupAddr must not be called in this case")
		return nil, nil
	}

	cases := []struct {
		name       string
		supervised bool
		localIPs   []netip.Addr
		lookupAddr func(ctx context.Context, addr string) ([]string, error)
		rawHost    string
		want       string
	}{
		{
			name:       "local_ip_returns_localhost",
			supervised: false,
			localIPs:   []netip.Addr{netip.MustParseAddr("192.0.2.29")},
			lookupAddr: neverCalled,
			rawHost:    "192.0.2.29",
			want:       "localhost",
		},
		{
			name:       "supervised_docker_range_resolves_hostname",
			supervised: true,
			localIPs:   nil,
			lookupAddr: func(_ context.Context, addr string) ([]string, error) {
				return []string{"ccu-addon.local."}, nil
			},
			rawHost: "172.18.0.5",
			want:    "ccu-addon.local",
		},
		{
			name:       "supervised_docker_range_lookup_error_returns_raw",
			supervised: true,
			localIPs:   nil,
			lookupAddr: func(_ context.Context, _ string) ([]string, error) {
				return nil, errLookup
			},
			rawHost: "172.18.0.5",
			want:    "172.18.0.5",
		},
		{
			name:       "supervised_docker_range_lookup_returns_ip_skipped",
			supervised: true,
			localIPs:   nil,
			lookupAddr: func(_ context.Context, _ string) ([]string, error) {
				return []string{"172.18.0.5"}, nil
			},
			rawHost: "172.18.0.5",
			want:    "172.18.0.5",
		},
		{
			name:       "not_supervised_docker_range_returns_raw",
			supervised: false,
			localIPs:   nil,
			lookupAddr: neverCalled,
			rawHost:    "172.18.0.5",
			want:       "172.18.0.5",
		},
		{
			name:       "supervised_lan_ip_not_docker_range_returns_raw",
			supervised: true,
			localIPs:   nil,
			lookupAddr: neverCalled,
			rawHost:    "192.168.1.5",
			want:       "192.168.1.5",
		},
		{
			name:       "hostname_input_returned_unchanged",
			supervised: true,
			localIPs:   nil,
			lookupAddr: neverCalled,
			rawHost:    "my-ccu.fritz.box",
			want:       "my-ccu.fritz.box",
		},
		{
			name:       "empty_input_returned_unchanged",
			supervised: false,
			localIPs:   nil,
			lookupAddr: neverCalled,
			rawHost:    "",
			want:       "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := &HostSuggester{
				Supervised: tc.supervised,
				LocalIPs:   tc.localIPs,
				LookupAddr: tc.lookupAddr,
			}
			got := h.Suggest(context.Background(), tc.rawHost)
			if got != tc.want {
				t.Errorf("Suggest(%q) = %q, want %q", tc.rawHost, got, tc.want)
			}
		})
	}
}
