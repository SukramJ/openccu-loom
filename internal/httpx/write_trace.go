// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package httpx

import (
	"fmt"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// WriteTracker records whether a request was written to the wire in full.
//
// A transport error alone does not say whether the remote saw the call: a
// refused dial and a connection that breaks after the request went out
// both surface from [http.Client.Do] as an error. For a read the
// difference does not matter; for a state-changing call it decides
// whether sending it again repeats an action the remote already took.
// The tracker is the one place that tells the two apart.
type WriteTracker struct {
	written atomic.Bool
}

// Trace returns req carrying a client trace that marks the tracker once
// the request — headers and body — has been written without error. A
// write that fails part-way leaves it unmarked: the remote never received
// a complete request and cannot have executed it.
func (w *WriteTracker) Trace(req *http.Request) *http.Request {
	trace := &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				w.written.Store(true)
			}
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
}

// Written reports whether the request reached the wire in full.
func (w *WriteTracker) Written() bool { return w.written.Load() }

// SendError wraps a transport error from sending the request or reading
// its reply: [hmerr.ErrReplyLost] once the request was written, plain
// [hmerr.ErrNoConnection] before that.
func (w *WriteTracker) SendError(err error) error {
	if w.Written() {
		return fmt.Errorf("%w: %w", hmerr.ErrReplyLost, err)
	}
	return fmt.Errorf("%w: %w", hmerr.ErrNoConnection, err)
}
