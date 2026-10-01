// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ssdp

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
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
			resp: "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nLOCATION: http://192.0.2.29/upnp/basic_dev.cgi\r\nST: ssdp:all\r\n\r\n",
			want: "http://192.0.2.29/upnp/basic_dev.cgi",
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

// TestSearchFromReturnsPromptlyOnCancel pins that cancelling the context
// ends a running M-SEARCH right away. The read loop blocks in ReadFromUDP
// until the socket's read deadline (MX plus the grace period), so a search
// that only checked ctx between reads kept Discoverer.Stop — and with it
// every daemon shutdown and restart — waiting out the rest of that window.
func TestSearchFromReturnsPromptlyOnCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		_, err := searchFrom(ctx, net.IPv4zero)
		done <- result{err, time.Since(start)}
	}()
	// Let the probe go out and the read loop block before cancelling.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case r := <-done:
		if r.err != nil {
			t.Skipf("this host cannot send an SSDP probe (%v); the cancel path is not reachable", r.err)
		}
		if r.elapsed > time.Second {
			t.Fatalf("searchFrom returned %v after its start, want it to stop right after the cancel at 200ms", r.elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("searchFrom did not return")
	}
}
