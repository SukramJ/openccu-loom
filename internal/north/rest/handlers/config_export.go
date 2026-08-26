// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// ConfigExportService is the narrow facade the config-export / import
// endpoints depend on.  Implementations live in the central adapter
// layer; tests supply stubs.
//
// ReadParamset and WriteParamset carry the same semantics as
// [configui.ParamsetReader] / [configui.ParamsetWriter] — they are
// declared here so the handler layer does not import the adapter
// package directly.
type ConfigExportService interface {
	// ReadParamset fetches the named paramset for channelAddress on
	// centralName.  Returns a map of parameter name → value.
	ReadParamset(ctx context.Context, centralName, channelAddress, paramsetKey string) (map[string]any, error)
	// WriteParamset applies values to the named paramset on
	// channelAddress on centralName.
	WriteParamset(ctx context.Context, centralName, channelAddress, paramsetKey string, values map[string]any) error
}

// ChannelInfoReader resolves the device/channel metadata needed to
// populate the non-paramset fields of an [configui.ExportedConfiguration].
// Implemented by the central adapter's DeviceIndex.
type ChannelInfoReader interface {
	// ChannelMeta returns (deviceAddress, model, channelType, centralName)
	// for the given channelAddress, and false when the channel is unknown.
	ChannelMeta(channelAddress string) (deviceAddress, model, channelType, centralName string, ok bool)
}

// ExportChannelConfig handles
//
//	GET /api/v1/devices/{addr}/channels/{no}/config/export?paramset=MASTER
//
// It fetches the live paramset from the CCU, wraps it in an
// [configui.ExportedConfiguration] and returns it as JSON.
//
// Query parameter `paramset` selects the paramset key (default: MASTER).
// The channel address is assembled from the `addr` + `no` URL params.
func ExportChannelConfig(svc ConfigExportService, meta ChannelInfoReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Config export unavailable", "no backend wired"))
			return
		}

		addr := chi.URLParam(r, "addr")
		no := chi.URLParam(r, "no")
		if no == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Channel number required", ""))
			return
		}
		channelAddr := addr + ":" + no

		paramsetKey := r.URL.Query().Get("paramset")
		if paramsetKey == "" {
			paramsetKey = "MASTER"
		}
		if paramsetKey != "MASTER" && paramsetKey != "VALUES" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid paramset key",
					"paramset must be MASTER or VALUES"))
			return
		}

		// Resolve device / channel metadata.
		var deviceAddr, model, channelType, centralName string
		if meta != nil {
			var ok bool
			deviceAddr, model, channelType, centralName, ok = meta.ChannelMeta(channelAddr)
			if !ok {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Channel not found", channelAddr))
				return
			}
		} else {
			// Minimal fall-back: use the URL address; model/type unknown.
			deviceAddr = addr
		}

		cfg, err := configui.ExportConfiguration(r.Context(), configui.ExportInput{
			CentralName:    centralName,
			DeviceAddress:  deviceAddr,
			Model:          model,
			ChannelAddress: channelAddr,
			ChannelType:    channelType,
			ParamsetKey:    paramsetKey,
			Reader:         &configExportAdapter{svc: svc, centralName: centralName},
		})
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Export failed", err)
			return
		}
		JSON(w, http.StatusOK, cfg)
	}
}

// ImportChannelConfig handles
//
//	POST /api/v1/devices/{addr}/channels/{no}/config/import
//
// It parses the JSON body as an [configui.ExportedConfiguration], runs
// validation and applies it to the CCU.  The URL-derived channel
// address is checked against the payload's ChannelAddress — a mismatch
// is rejected with 400 to prevent accidental cross-channel writes.
func ImportChannelConfig(svc ConfigExportService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Config import unavailable", "no backend wired"))
			return
		}

		addr := chi.URLParam(r, "addr")
		no := chi.URLParam(r, "no")
		if no == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Channel number required", ""))
			return
		}
		// validate no is a number — consistent with other channel endpoints
		if _, err := strconv.Atoi(no); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Channel number must be an integer", no))
			return
		}
		channelAddr := addr + ":" + no

		// Cap the body like every JSON handler (write.go DecodeJSON): this
		// route reads the raw bytes directly, so without the ceiling a
		// multi-GB POST would pin a goroutine allocating unbounded heap.
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Cannot read request body", err.Error()))
			return
		}

		cfg, err := configui.ImportConfiguration(raw)
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid configuration payload", err.Error()))
			return
		}

		// Guard: the payload must target the same channel as the URL.
		if cfg.ChannelAddress != channelAddr {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r,
					"Channel address mismatch",
					"payload channel_address "+cfg.ChannelAddress+" does not match URL channel "+channelAddr))
			return
		}

		writer := &configExportAdapter{svc: svc, centralName: cfg.CentralName}
		if err := configui.ApplyConfiguration(r.Context(), cfg, writer); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Apply failed", err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// configExportAdapter bridges [ConfigExportService] to the
// [configui.ParamsetReader] and [configui.ParamsetWriter] interfaces.
// It captures the centralName so the configui functions receive the
// multi-CCU scope without it being threaded through every call.
type configExportAdapter struct {
	svc         ConfigExportService
	centralName string
}

// ReadParamset implements [configui.ParamsetReader].
func (a *configExportAdapter) ReadParamset(ctx context.Context, _, channelAddress, paramsetKey string) (map[string]any, error) {
	return a.svc.ReadParamset(ctx, a.centralName, channelAddress, paramsetKey)
}

// WriteParamset implements [configui.ParamsetWriter].
func (a *configExportAdapter) WriteParamset(ctx context.Context, _, channelAddress, paramsetKey string, values map[string]any) error {
	return a.svc.WriteParamset(ctx, a.centralName, channelAddress, paramsetKey, values)
}
