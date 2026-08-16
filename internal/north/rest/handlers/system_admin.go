// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// Audit actions for the startup-capture toggle. Arming a capture changes
// what the daemon records about its own bootstrap, so the change is
// traceable like the other operator-diagnostics verbs. The dotted strings
// are the values already written to the audit store, kept verbatim so
// existing rows keep resolving.
const (
	actionStartupCaptureEnabled  audit.Action = "diagnostics.startup_capture_enabled"
	actionStartupCaptureDisabled audit.Action = "diagnostics.startup_capture_disabled"
)

// StartupCaptureService is the narrow facade the
// `/system/startup-capture` endpoints depend on. The composition root
// satisfies it with a closure over the data directory.
type StartupCaptureService interface {
	Load() (diagnostics.StartupCaptureConfig, error)
	Save(diagnostics.StartupCaptureConfig) error
}

// NewStartupCaptureFileService returns a [StartupCaptureService] that
// reads / writes `<dataDir>/startup_capture.json`.
func NewStartupCaptureFileService(dataDir string) StartupCaptureService {
	return &startupCaptureFileService{dataDir: dataDir}
}

type startupCaptureFileService struct {
	dataDir string
}

func (s *startupCaptureFileService) Load() (diagnostics.StartupCaptureConfig, error) {
	return diagnostics.LoadStartupCapture(s.dataDir)
}

func (s *startupCaptureFileService) Save(cfg diagnostics.StartupCaptureConfig) error {
	return diagnostics.SaveStartupCapture(s.dataDir, cfg)
}

// GetStartupCapture renders the persisted startup-capture config.
func GetStartupCapture(svc StartupCaptureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "startup-capture service unwired", ""))
			return
		}
		cfg, err := svc.Load()
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "load startup capture", err)
			return
		}
		JSON(w, http.StatusOK, cfg)
	}
}

// PutStartupCapture persists a new startup-capture config. The change
// takes effect on the next daemon boot — pair it with the
// `/system/restart` endpoint for an end-to-end "capture the bootstrap"
// workflow.
func PutStartupCapture(svc StartupCaptureService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "startup-capture service unwired", ""))
			return
		}
		var cfg diagnostics.StartupCaptureConfig
		if err := DecodeJSON(r, &cfg); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "invalid body", err.Error()))
			return
		}
		if cfg.DurationS < 0 {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "duration_seconds must be >= 0", ""))
			return
		}
		if err := svc.Save(cfg); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "save startup capture", err)
			return
		}
		if rec != nil {
			action := actionStartupCaptureDisabled
			if cfg.Enabled {
				action = actionStartupCaptureEnabled
			}
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: action,
			})
		}
		JSON(w, http.StatusOK, cfg)
	}
}

// restartSignalledAt holds the wall-clock nanos of the last SIGTERM this
// endpoint sent, or 0 when none was sent yet. The signal goes out from a
// detached goroutine after the response has been flushed, so a lock held
// for the handler's duration would not stop an impatient double-click
// from firing a second SIGTERM into a shutdown that is already running.
//
// The suppression is time-bounded rather than permanent: a graceful
// shutdown that does not terminate the process (a south-bound call that
// will not unblock, an aborted sequence) must not leave the endpoint a
// no-op for the rest of the daemon's life — that is precisely the state
// in which an operator needs the retry. After [restartGrace] the next
// request signals again.
var restartSignalledAt atomic.Int64

// restartGrace is how long an accepted restart suppresses further
// signals. It covers the daemon's graceful-shutdown budget with room to
// spare, so a second signal inside the window can only be a duplicate
// request, while one after it means the shutdown did not finish.
const restartGrace = 30 * time.Second

// restartNow is the clock seam for the grace window, so the retry path
// can be exercised without waiting out [restartGrace].
var restartNow = time.Now

// claimRestart reports whether this request owns the next SIGTERM. It
// returns true exactly once per grace window: the compare-and-swap keeps
// two requests that arrive at the same instant from both claiming it.
func claimRestart(now time.Time) bool {
	prev := restartSignalledAt.Load()
	// The stamp is wall-clock, so a clock stepped backwards (NTP after a
	// boot without an RTC — the common case on the hardware this daemon
	// runs next to) can make the window look open-ended. A negative
	// elapsed time counts as expired rather than as "still shutting down".
	if elapsed := now.Sub(time.Unix(0, prev)); prev != 0 && elapsed >= 0 && elapsed < restartGrace {
		return false
	}
	return restartSignalledAt.CompareAndSwap(prev, now.UnixNano())
}

// restartSignal asks the daemon's own process to shut down. It is a
// variable so the double-fire latch can be exercised without
// terminating the test process.
var restartSignal = func() {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return
	}
	_ = p.Signal(syscall.SIGTERM)
}

// Restart sends a SIGTERM to the daemon's own process and returns
// immediately. The response body acknowledges the request; the
// daemon's signal handler drives the graceful shutdown. Re-launch is
// the supervisor's job (systemd, Docker restart-policy, the wrapper
// shell script in dev).
//
// The `status` field distinguishes the two outcomes: `shutdown_signalled`
// means this request sent the signal, `shutdown_in_progress` means a
// shutdown signalled less than [restartGrace] ago is still running and
// no second signal was sent. Both are 202 — the request was accepted
// either way — but the operator is told which one happened instead of
// reading a success line for a no-op.
//
// The endpoint is admin-gated by the router; this handler runs only
// after the role middleware has accepted the request.
func Restart(rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := restartNow()
		signalling := claimRestart(now)
		status := "shutdown_in_progress"
		if signalling {
			status = "shutdown_signalled"
		}
		// Acknowledge before the signal lands — once SIGTERM fires
		// the shutdown sequence may close the connection before we
		// can send the response.
		JSON(w, http.StatusAccepted, map[string]any{
			"status": status,
			"at":     now.UTC().Format(time.RFC3339Nano),
		})
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionSystemRestartRequested,
				Note:   status,
			})
		}
		if !signalling {
			// A shutdown is already running: the request is
			// acknowledged and audited, but a second signal would only
			// interrupt the graceful sequence.
			return
		}
		// Send the signal on a goroutine so the response writer
		// finishes flushing first. The 100 ms gap is comfortably
		// above any normal flush latency without making the
		// operator wait noticeably.
		go func() {
			time.Sleep(100 * time.Millisecond)
			restartSignal()
		}()
	}
}
