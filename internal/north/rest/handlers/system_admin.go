// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
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
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "load startup capture", err.Error()))
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
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "invalid body", err.Error()))
			return
		}
		if cfg.DurationS < 0 {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "duration_seconds must be >= 0", ""))
			return
		}
		if err := svc.Save(cfg); err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "save startup capture", err.Error()))
			return
		}
		if rec != nil {
			action := "diagnostics.startup_capture_disabled"
			if cfg.Enabled {
				action = "diagnostics.startup_capture_enabled"
			}
			rec.Record(audit.Entry{
				Action: audit.Action(action),
			})
		}
		JSON(w, http.StatusOK, cfg)
	}
}

// restartTriggerOnce guards against double-fire when an impatient
// operator double-clicks the SPA button. Without the lock the second
// call would send a second SIGTERM after the first one already
// scheduled the shutdown, producing a confusing 500 in the log.
var restartTriggerOnce sync.Mutex

// Restart sends a SIGTERM to the daemon's own process and returns
// immediately. The response body acknowledges the request; the
// daemon's signal handler drives the graceful shutdown. Re-launch is
// the supervisor's job (systemd, Docker restart-policy, the wrapper
// shell script in dev).
//
// The endpoint is admin-gated by the router; this handler runs only
// after the role middleware has accepted the request.
func Restart(rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		restartTriggerOnce.Lock()
		defer restartTriggerOnce.Unlock()
		// Acknowledge before the signal lands — once SIGTERM fires
		// the shutdown sequence may close the connection before we
		// can send the response.
		JSON(w, http.StatusAccepted, map[string]any{
			"status": "shutdown_signalled",
			"at":     time.Now().UTC().Format(time.RFC3339Nano),
		})
		if rec != nil {
			rec.Record(audit.Entry{
				Action: audit.Action("system.restart_requested"),
			})
		}
		// Send the signal on a goroutine so the response writer
		// finishes flushing first. The 100 ms gap is comfortably
		// above any normal flush latency without making the
		// operator wait noticeably.
		go func() {
			time.Sleep(100 * time.Millisecond)
			p, err := os.FindProcess(os.Getpid())
			if err != nil {
				return
			}
			_ = p.Signal(syscall.SIGTERM)
		}()
	}
}
