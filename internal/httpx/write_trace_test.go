// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package httpx

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// A server that reads the whole request and then closes the connection
// without answering is the CCU's web server dropping a keep-alive
// connection as the call arrives: the call went out, the reply is lost.
func TestWriteTrackerReportsAReplyLostAfterTheRequestWasWritten(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"method":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	var sent WriteTracker
	resp, err := NewClient(5 * time.Second).Do(sent.Trace(req))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("Do succeeded, want the dropped connection's error")
	}
	if !sent.Written() {
		t.Fatal("Written() = false after the server read the whole request")
	}
	got := sent.SendError(err)
	if !errors.Is(got, hmerr.ErrReplyLost) {
		t.Errorf("SendError = %v, want it to wrap ErrReplyLost", got)
	}
	if !errors.Is(got, hmerr.ErrNoConnection) {
		t.Errorf("SendError = %v, want it to still wrap ErrNoConnection", got)
	}
}

// A refused dial never put the call on the wire, so it must not read as a
// lost reply: the retry layer repeats it for every method.
func TestWriteTrackerKeepsARefusedDialANoConnection(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var sent WriteTracker
	resp, err := NewClient(5 * time.Second).Do(sent.Trace(req))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("Do succeeded against a closed port")
	}
	got := sent.SendError(err)
	if errors.Is(got, hmerr.ErrReplyLost) {
		t.Errorf("SendError = %v, a refused dial must not wrap ErrReplyLost", got)
	}
	if !errors.Is(got, hmerr.ErrNoConnection) {
		t.Errorf("SendError = %v, want ErrNoConnection", got)
	}
}
