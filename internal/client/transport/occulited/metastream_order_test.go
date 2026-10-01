// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestMetaStreamRevisionCoversTheDeliveredEvent pins that Revision already
// reflects an event at the moment it is delivered. A consumer that reads a
// change from Messages and then asks for Revision — to resume from it, or
// to compare it with the event — must not see the revision from before
// that event; with no Since the stream would even report that it has none.
func TestMetaStreamRevisionCoversTheDeliveredEvent(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"kind\":\"object.updated\",\"ref\":\"lamp\",\"revision\":7}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{BaseURL: srv.URL, Token: "olt_test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s := &MetaStream{c: c, opts: MetaStreamOptions{HeartbeatTimeout: time.Minute}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var delivered bool
	_, _ = s.connect(ctx, func(m MetaMessage) bool {
		if m.Kind != MetaChange {
			return true
		}
		delivered = true
		if rev, ok := s.Revision(); !ok || rev != 7 {
			t.Errorf("Revision while revision 7 is being delivered = %d, %v; want 7, true", rev, ok)
		}
		return false
	})
	if !delivered {
		t.Fatal("the change event was never delivered")
	}
}
