// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ssdp

import (
	"strings"
	"testing"
)

// TestLocationHeader verifies extraction of the LOCATION header from an SSDP
// response, including case-insensitive matching.
func TestLocationHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		resp string
		want string
	}{
		{
			name: "uppercase LOCATION",
			resp: "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nLOCATION: http://172.18.4.29/upnp/basic_dev.cgi\r\nST: ssdp:all\r\n\r\n",
			want: "http://172.18.4.29/upnp/basic_dev.cgi",
		},
		{
			name: "title-case Location",
			resp: "HTTP/1.1 200 OK\r\nLocation: http://192.168.1.5/upnp/basic_dev.cgi\r\nST: ssdp:all\r\n\r\n",
			want: "http://192.168.1.5/upnp/basic_dev.cgi",
		},
		{
			name: "lowercase location",
			resp: "HTTP/1.1 200 OK\r\nlocation: http://10.0.0.1/upnp/basic_dev.cgi\r\nST: ssdp:all\r\n\r\n",
			want: "http://10.0.0.1/upnp/basic_dev.cgi",
		},
		{
			name: "no location header",
			resp: "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nST: ssdp:all\r\n\r\n",
			want: "",
		},
		{
			name: "empty response",
			resp: "",
			want: "",
		},
		{
			name: "value is trimmed",
			resp: "HTTP/1.1 200 OK\r\nLOCATION:   http://10.0.0.2/upnp/basic_dev.cgi   \r\nST: ssdp:all\r\n\r\n",
			want: "http://10.0.0.2/upnp/basic_dev.cgi",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := locationHeader([]byte(tc.resp))
			if got != tc.want {
				t.Errorf("locationHeader() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMSearchPayload verifies that the M-SEARCH datagram contains the
// required SSDP fields for a given search target.
func TestMSearchPayload(t *testing.T) {
	t.Parallel()

	target := "ssdp:all"
	payload := string(mSearchPayload(target))

	checks := []struct {
		desc    string
		contain string
	}{
		{"request line", "M-SEARCH * HTTP/1.1"},
		{"MAN header", `MAN: "ssdp:discover"`},
		{"ST header", "ST: " + target},
		{"CRLF line ending", "\r\n"},
	}

	for _, c := range checks {
		if !strings.Contains(payload, c.contain) {
			t.Errorf("mSearchPayload missing %s: want %q in:\n%s", c.desc, c.contain, payload)
		}
	}
}

// TestMSearchPayload_CustomTarget ensures the search-target is embedded
// verbatim in the ST header.
func TestMSearchPayload_CustomTarget(t *testing.T) {
	t.Parallel()

	target := "urn:schemas-upnp-org:device:Basic:1"
	payload := string(mSearchPayload(target))
	if want := "ST: " + target; !strings.Contains(payload, want) {
		t.Errorf("ST header missing: want %q in payload", want)
	}
}
