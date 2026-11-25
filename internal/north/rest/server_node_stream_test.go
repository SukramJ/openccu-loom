// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package rest

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"thingfabric.dev/go-fabric-ui/fabricui"
)

// TestServerShutdownEndsOpenNodeStreams: an open go-fabric-ui node event
// stream (GET /node/stream) ends only when its client leaves or
// fabricui.Options.Done closes. The composition root closes Done from the
// server's shutdown hook (Server.RegisterOnShutdown), so a graceful
// shutdown returns at once instead of waiting for the stream until its
// deadline. Without the hook the shutdown below runs into its deadline.
func TestServerShutdownEndsOpenNodeStreams(t *testing.T) {
	for _, tc := range []struct {
		name     string
		withHook bool
	}{
		{"hook closes Done", true},
		{"no hook", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			h, err := NodeAPI(fabricui.Options{
				Prefix: "/api/v1",
				Admin:  func(*http.Request) bool { return true },
				Done:   done,
			})
			if err != nil {
				t.Fatalf("NodeAPI: %v", err)
			}
			srv := NewServer("127.0.0.1:0", h, nil)
			if tc.withHook {
				srv.RegisterOnShutdown(sync.OnceFunc(func() { close(done) }))
			} else {
				t.Cleanup(func() { close(done) })
			}
			served := make(chan error, 1)
			go func() { served <- srv.Start() }()
			addr := waitBound(t, srv)

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/api/v1/node/stream", http.NoBody)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			req.Header.Set(fabricui.RequestHeader, "1")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET /node/stream: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /node/stream = %d, want 200 (an open stream)", resp.StatusCode)
			}
			// The stream writes its first frame once it is registered.
			if _, err := bufio.NewReader(resp.Body).ReadByte(); err != nil {
				t.Fatalf("read the stream's first byte: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
			defer cancel()
			shutErr := srv.Shutdown(ctx)
			switch {
			case tc.withHook && shutErr != nil:
				t.Fatalf("Shutdown with the hook = %v, want nil — the open node stream held the shutdown", shutErr)
			case !tc.withHook && !errors.Is(shutErr, context.DeadlineExceeded):
				t.Fatalf("Shutdown without the hook = %v, want the deadline — the control does not measure the hook", shutErr)
			}
			if err := <-served; err != nil {
				t.Fatalf("Start returned %v", err)
			}
		})
	}
}

func waitBound(t *testing.T, srv *Server) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a := srv.Addr(); a != "127.0.0.1:0" && a != "" {
			return a
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not bind within 2s")
	return ""
}
