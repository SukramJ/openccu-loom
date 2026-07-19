// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Command openccu-loom-remote is the OpenCCU-Loom Remote ingress proxy:
// it surfaces one or more remote OpenCCU-Loom instances through a single
// Home Assistant Ingress panel. See docs/adr/0054-remote-ingress-proxy-addon.md.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/remoteproxy"
)

func main() {
	os.Exit(run())
}

func run() int {
	optionsPath := flag.String("options", "/data/options.json", "path to the add-on options file")
	listen := flag.String("listen", ":8234", "listen address (must match the add-on's ingress_port)")
	flag.Parse()

	opts, err := remoteproxy.LoadOptions(*optionsPath)
	if err != nil {
		slog.Error("invalid configuration", "path", *optionsPath, "error", err)
		return 1
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel(opts.LogLevel)}))
	slog.SetDefault(log)
	log.Info("starting openccu-loom-remote",
		"version", build.Version, "instances", len(opts.Instances), "listen", *listen)

	srv, err := remoteproxy.New(opts, log)
	if err != nil {
		log.Error("building proxy failed", "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.Start(ctx)

	httpSrv := &http.Server{
		Addr:    *listen,
		Handler: srv.Handler(),
		// Header/idle limits only: proxied WebSockets and downloads are
		// long-lived, so no global read/write deadlines.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()

	select {
	case err := <-errCh:
		log.Error("listener failed", "error", err)
		return 1
	case <-ctx.Done():
	}
	// Release the signal registration before draining: a second signal
	// during the drain window then force-quits via default handling.
	stop()

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("shutdown incomplete", "error", err)
	}
	return 0
}

// logLevel maps the add-on option to a slog level; the zero value of an
// unknown/empty option is info.
func logLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
