// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// A CCU that reads the whole call and drops the connection before
// answering has received it: the error must say the reply was lost, so the
// retry layer does not send a write again.
func TestClientReportsAReplyLostAfterTheCallWasSent(t *testing.T) {
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

	c, err := NewClient(Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = c.Call(ctx, "setValue", []Value{StringValue("0001D3C99C3C93:1"), StringValue("PRESS_SHORT"), BoolValue(true)})
	if !errors.Is(err, hmerr.ErrReplyLost) {
		t.Fatalf("Call = %v, want ErrReplyLost", err)
	}
}
