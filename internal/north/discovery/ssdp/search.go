// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ssdp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	ssdpMulticastAddr = "239.255.255.250:1900"
	// ssdpMX is the M-SEARCH "maximum wait" in seconds the responders randomise
	// their reply over. We read for a little longer than MX to catch the tail.
	ssdpMX        = 2
	ssdpReadGrace = 1 * time.Second
	// searchTarget stays generic: the reference discovery filters on the
	// manufacturer in the device description, not on the SSDP ST, so a broad
	// search target finds CCUs that only answer rootdevice / ssdp:all probes.
	searchTarget = "ssdp:all"
)

// mSearchPayload builds the SSDP M-SEARCH datagram for the given search target.
func mSearchPayload(st string) []byte {
	lines := []string{
		"M-SEARCH * HTTP/1.1",
		"HOST: 239.255.255.250:1900",
		`MAN: "ssdp:discover"`,
		fmt.Sprintf("MX: %d", ssdpMX),
		"ST: " + st,
		"", "",
	}
	return []byte(strings.Join(lines, "\r\n"))
}

// searchFrom sends an M-SEARCH bound to the given source IP and returns the
// distinct LOCATION URLs of every responder seen before the deadline. A bound
// source IP makes the OS route the multicast out of that interface, so on a
// multi-homed host (e.g. the HA add-on with host networking) the probe leaves
// via the real LAN link rather than a container bridge.
func searchFrom(ctx context.Context, srcIP net.IP) ([]string, error) {
	raddr, err := net.ResolveUDPAddr("udp4", ssdpMulticastAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: srcIP, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", srcIP, err)
	}
	defer func() { _ = conn.Close() }()

	payload := mSearchPayload(searchTarget)
	// Send twice: SSDP is best-effort over UDP and the first datagram is
	// occasionally lost while the multicast group is still being joined.
	for range 2 {
		if _, err := conn.WriteToUDP(payload, raddr); err != nil {
			return nil, fmt.Errorf("send m-search: %w", err)
		}
	}

	deadline := time.Now().Add(ssdpMX*time.Second + ssdpReadGrace)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetReadDeadline(deadline)

	seen := make(map[string]struct{})
	var locations []string
	buf := make([]byte, 2048)
	for ctx.Err() == nil {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline reached or socket closed
		}
		if loc := locationHeader(buf[:n]); loc != "" {
			if _, dup := seen[loc]; !dup {
				seen[loc] = struct{}{}
				locations = append(locations, loc)
			}
		}
	}
	return locations, nil
}

// locationHeader extracts the LOCATION header value from an SSDP response. The
// header name is matched case-insensitively (responders vary the casing).
func locationHeader(resp []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(resp))
	for sc.Scan() {
		line := sc.Text()
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(line[:idx]), "location") {
			return strings.TrimSpace(line[idx+1:])
		}
	}
	return ""
}
