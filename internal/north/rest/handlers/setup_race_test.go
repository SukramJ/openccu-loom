// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestSetup_ConcurrentFirstRunPostsAdmitExactlyOne pins the single-shot
// guarantee under concurrency. The first-run probe is a live user count and
// finalize is an upsert behind a bcrypt hash (cost 12, hundreds of
// milliseconds); without serialisation two POSTs both pass the probe and
// both land — two admins with different names, or one silently overwriting
// the other's password. Exactly one caller may win.
func TestSetup_ConcurrentFirstRunPostsAdmitExactlyOne(t *testing.T) {
	svc := newFullSetupService(t)
	// The real probe: setup is required while no user exists.
	svc.Required = func(ctx context.Context) bool {
		n, err := svc.Users.Count(ctx)
		return err == nil && n == 0
	}
	handler := Setup(svc)

	const callers = 4
	codes := make([]int, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := strings.NewReader(`{
				"admin":  {"username":"admin` + string(rune('a'+i)) + `","password":"password123"},
				"locale": {"locale":"de","theme":"light"}
			}`)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", body)
			w := httptest.NewRecorder()
			<-start
			handler.ServeHTTP(w, req)
			codes[i] = w.Code
		}(i)
	}
	close(start)
	wg.Wait()

	admitted := 0
	for _, c := range codes {
		switch c {
		case http.StatusNoContent:
			admitted++
		case http.StatusConflict:
		default:
			t.Errorf("unexpected status %d in %v", c, codes)
		}
	}
	if admitted != 1 {
		t.Fatalf("%d concurrent first-run POSTs were admitted (statuses %v), want exactly 1", admitted, codes)
	}
	n, err := svc.Users.Count(context.Background())
	if err != nil {
		t.Fatalf("Users.Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("user count = %d after %d concurrent first-run POSTs, want 1", n, callers)
	}
}
