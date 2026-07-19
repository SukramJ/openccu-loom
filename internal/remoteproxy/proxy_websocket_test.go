// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package remoteproxy

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// wsUpgradeUpstream answers a WebSocket handshake by hand: it rejects
// anything that does not carry the Upgrade/Connection pair, then hijacks
// the raw connection so it controls the exact bytes on the wire instead
// of going through the normal http.ResponseWriter status/header path.
// After the handshake it echoes a single line, enough to prove the
// connection stays a live, bidirectional pipe once switched.
func wsUpgradeUpstream(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		!strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		return
	}
	if err := buf.Flush(); err != nil {
		return
	}
	line, err := buf.ReadString('\n')
	if err != nil {
		return
	}
	if _, err := buf.WriteString(line); err != nil {
		return
	}
	_ = buf.Flush()
}

func TestInstanceProxyWebSocketUpgradePassthrough(t *testing.T) {
	upstream, capture := newProxyUpstreamFixture(t, wsUpgradeUpstream)
	proxy := newProxyServerFixture(t, []Instance{
		{Name: "a", URL: upstream.URL, Token: "tok-ws"},
		{Name: "b", URL: upstream.URL},
	})

	addr := strings.TrimPrefix(proxy.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	request := "GET /i/a/ws HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("write handshake request: %v", err)
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	if got, want := strings.TrimRight(statusLine, "\r\n"), "HTTP/1.1 101 Switching Protocols"; got != want {
		t.Fatalf("status line = %q, want %q", got, want)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read handshake headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	const echoLine = "hello over the wire\n"
	if _, err := conn.Write([]byte(echoLine)); err != nil {
		t.Fatalf("write echo line: %v", err)
	}
	got, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read echoed line: %v", err)
	}
	if got != echoLine {
		t.Errorf("echoed line = %q, want %q", got, echoLine)
	}

	if got, want := capture.Header().Get("Authorization"), "Bearer tok-ws"; got != want {
		t.Errorf("upstream saw Authorization = %q, want %q", got, want)
	}
}
