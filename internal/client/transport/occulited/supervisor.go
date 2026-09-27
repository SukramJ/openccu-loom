// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Stream end reasons shared by both stream readers.
var (
	// ErrHeartbeatTimeout reports a stream that stayed silent (not even a
	// heartbeat comment) for longer than the reader's heartbeat timeout.
	ErrHeartbeatTimeout = errors.New("occulited: stream silent past its heartbeat timeout")
	// ErrStreamEnded reports a stream the server closed; the box ends a
	// stream without a closing frame (overflow, revoked credential,
	// restart).
	ErrStreamEnded = errors.New("occulited: stream ended")
)

// Backoff is the reconnect policy of the stream readers.
type Backoff struct {
	// Initial and Max bound the exponential delay after a failure that
	// has no specific rule; Jitter is its random spread (0.2 = ±20 %).
	Initial time.Duration
	Max     time.Duration
	Jitter  float64
	// Unauthorized is the delay after 401 or 403: the token is invalid
	// or lacks the stream's scope, which only an operator can change.
	Unauthorized time.Duration
	// TooManyStreams is the delay after 429 too-many-streams when the
	// answer carries no Retry-After.
	TooManyStreams time.Duration
	// Starting is the delay after 503 (occulited starting, or down) when
	// the answer carries no Retry-After.
	Starting time.Duration
}

// DefaultBackoff returns the production policy: 1 s doubling to 60 s
// with ±20 % jitter, 60 s after an auth refusal, 30 s after a stream
// limit, 5 s while the box is starting.
func DefaultBackoff() Backoff {
	return Backoff{
		Initial:        time.Second,
		Max:            60 * time.Second,
		Jitter:         0.2,
		Unauthorized:   60 * time.Second,
		TooManyStreams: 30 * time.Second,
		Starting:       5 * time.Second,
	}
}

// withDefaults fills every zero field from [DefaultBackoff].
func (b Backoff) withDefaults() Backoff {
	d := DefaultBackoff()
	if b.Initial <= 0 {
		b.Initial = d.Initial
	}
	if b.Max <= 0 {
		b.Max = d.Max
	}
	if b.Jitter < 0 || b.Jitter >= 1 {
		b.Jitter = d.Jitter
	}
	if b.Unauthorized <= 0 {
		b.Unauthorized = d.Unauthorized
	}
	if b.TooManyStreams <= 0 {
		b.TooManyStreams = d.TooManyStreams
	}
	if b.Starting <= 0 {
		b.Starting = d.Starting
	}
	return b
}

// Delay returns the wait before the next attempt after err, the
// failures-th consecutive failure (1-based).
func (b Backoff) Delay(err error, failures int) time.Duration {
	b = b.withDefaults()
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden:
			return b.Unauthorized
		case errors.Is(apiErr, ErrTooManyStreams):
			return orDefault(apiErr.RetryAfter, b.TooManyStreams)
		case apiErr.Status == http.StatusServiceUnavailable:
			return orDefault(apiErr.RetryAfter, b.Starting)
		}
	}
	if failures < 1 {
		failures = 1
	}
	d := float64(b.Initial) * math.Pow(2, float64(min(failures-1, 30)))
	d = min(d, float64(b.Max))
	if b.Jitter > 0 {
		d *= 1 + b.Jitter*(2*rand.Float64()-1) //nolint:gosec // reconnect jitter, not a secret
	}
	return time.Duration(d)
}

func orDefault(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// connectFunc runs one connection: it delivers messages through send
// until the connection ends, and reports whether the connection got far
// enough to count as live (which resets the failure count).
type connectFunc[M any] func(ctx context.Context, send func(M) bool) (live bool, err error)

// supervise runs connect in a loop until ctx ends: after every ended
// connection it sends closed(err, delay) and waits delay. It closes out
// when it returns. M is the reader's message type.
func supervise[M any](ctx context.Context, logger *slog.Logger, name string, b Backoff,
	out chan<- M, connect connectFunc[M], closed func(err error, retryIn time.Duration) M,
) {
	defer close(out)
	send := func(m M) bool {
		select {
		case out <- m:
			return true
		case <-ctx.Done():
			return false
		}
	}
	failures := 0
	for {
		live, err := connect(ctx, send)
		if ctx.Err() != nil {
			return
		}
		if live {
			failures = 0
		}
		failures++
		delay := b.Delay(err, failures)
		logger.Debug("occulited: stream ended", slog.String("stream", name),
			slog.Any("error", err), slog.Duration("retry_in", delay))
		if !send(closed(err, delay)) {
			return
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// openStream issues a stream GET and returns the answer for a 200;
// anything else becomes an error (an *APIError for an HTTP answer).
func (c *Client) openStream(ctx context.Context, path string, q map[string][]string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(path, q), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("occulited: build request: %w", err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.stream.Do(req)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}
		return nil, fmt.Errorf("occulited: GET %s: %w: %w", path, hmerr.ErrNoConnection, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		return nil, parseAPIError(resp, req, raw)
	}
	return resp, nil
}

// watchdog fires when a stream stays silent for its timeout. stamp
// re-arms it after every byte and every delivered message; pause holds
// it while a message waits for the consumer, so a slow consumer does not
// read as a silent server.
type watchdog struct {
	t *time.Timer
	d time.Duration
}

func newWatchdog(d time.Duration, fire func()) *watchdog {
	return &watchdog{t: time.AfterFunc(d, fire), d: d}
}

func (w *watchdog) stamp() { w.t.Reset(w.d) }
func (w *watchdog) pause() { w.t.Stop() }

// stampReader re-arms a watchdog whenever bytes arrive.
type stampReader struct {
	r  io.Reader
	wd *watchdog
}

func (s stampReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.wd.stamp()
	}
	return n, err
}

// endError explains why a connection's read loop stopped.
func endError(connCtx, ctx context.Context, err error) error {
	if errors.Is(context.Cause(connCtx), ErrHeartbeatTimeout) {
		return ErrHeartbeatTimeout
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrLineTooLong) {
		return fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	if errors.Is(err, io.EOF) {
		return ErrStreamEnded
	}
	return fmt.Errorf("%w: %w", ErrStreamEnded, err)
}
