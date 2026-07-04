// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/setup"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// MatterCommissioning carries the bridge's discriminator / passcode /
// vendor / product so the setup-payload endpoint can emit Matter §5.7
// QR + manual codes. Pulled from the daemon config at router-construction
// time.
type MatterCommissioning struct {
	Discriminator uint16
	Passcode      uint32
	VendorID      uint16
	ProductID     uint16
}

// MatterSetupPayloadResponse is the body of GET /api/v1/matter/setup-payload.
type MatterSetupPayloadResponse struct {
	Discriminator uint16 `json:"discriminator"`
	Passcode      uint32 `json:"passcode"`
	VendorID      uint16 `json:"vendor_id"`
	ProductID     uint16 `json:"product_id"`
	// QRCode is the base38-encoded payload prefixed with "MT:" per
	// Matter §5.7.4. Suitable for QR rendering by the caller.
	QRCode string `json:"qr_code"`
	// ManualCode is the 11-digit (Verhoeff-checked) manual pairing
	// code per Matter §5.1.4.
	ManualCode string `json:"manual_code"`
}

// MatterSetupPayload returns the bridge's QR + manual code. Returns
// 503 service_unready when the bridge is not commissionable
// (passcode == 0).
func MatterSetupPayload(c MatterCommissioning) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if c.Passcode == 0 {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter commissioning not configured",
					"set north.matter.commissioning.passcode in the daemon config"))
			return
		}
		qr, err := setup.QRCode(setup.Payload{
			Version:       0,
			VendorID:      c.VendorID,
			ProductID:     c.ProductID,
			Discriminator: c.Discriminator,
			Passcode:      c.Passcode,
			DiscoveryCaps: setup.DiscoveryOnIP,
		})
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, req,
					"Failed to encode setup payload", err.Error()))
			return
		}
		manual, err := setup.ManualCode(c.Discriminator, c.Passcode)
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, req,
					"Failed to encode manual code", err.Error()))
			return
		}
		JSON(w, http.StatusOK, MatterSetupPayloadResponse{
			Discriminator: c.Discriminator,
			Passcode:      c.Passcode,
			VendorID:      c.VendorID,
			ProductID:     c.ProductID,
			QRCode:        qr,
			ManualCode:    manual,
		})
	}
}

// MatterCommissioningOpener is the narrow facade
// POST /api/v1/matter/commissioning/window needs. The daemon wires this
// to a method that drives AdministratorCommissioning cluster
// (0x003C) command 0x01 (OpenCommissioningWindow) on the bridge's root
// endpoint, generates a fresh ephemeral passcode + discriminator, and
// returns the resulting QR + manual code.
type MatterCommissioningOpener interface {
	OpenCommissioningWindow(ctx context.Context, durationSeconds uint16) (MatterCommissioningWindowResult, error)
}

// MatterCommissioningWindowResult carries what was generated when a
// commissioning window opens.
type MatterCommissioningWindowResult struct {
	Discriminator   uint16
	Passcode        uint32
	DurationSeconds uint16
	QRCode          string
	ManualCode      string
}

// MatterCommissioningWindowRequest is the request body for
// POST /api/v1/matter/commissioning/window. duration_seconds is in
// [180, 900] per Matter §11.19.8.1.
type MatterCommissioningWindowRequest struct {
	DurationSeconds uint16 `json:"duration_seconds"`
}

// MatterCommissioningWindowResponse mirrors Result for the wire.
type MatterCommissioningWindowResponse struct {
	Discriminator   uint16 `json:"discriminator"`
	Passcode        uint32 `json:"passcode"`
	DurationSeconds uint16 `json:"duration_seconds"`
	QRCode          string `json:"qr_code"`
	ManualCode      string `json:"manual_code"`
}

// ErrCommissioningInProgress signals that a commissioning window is
// already open. Surfaces as 409 Conflict.
var ErrCommissioningInProgress = errors.New("matter: commissioning window already open")

// ErrBridgeTopologyNotReady signals that the Matter bridge has not yet
// reassembled with at least one bridged endpoint — opening the
// commissioning window now would let a commissioner subscribe before
// the CCU snapshot is in the topology, and Apple's MTREndpointInfo
// would cache an empty Descriptor.PartsList on EP 0 (mapper bails on
// `MTREndpointInfo.mm:209-265`). Surfaces as 503 with a Retry-After.
var ErrBridgeTopologyNotReady = errors.New("matter: no bridged endpoints yet — either the CCU device load has not finished (retry shortly; the bridge reassembles automatically once devices are loaded) or no devices are enabled for Matter exposure")

// MatterCommissioningWindow opens a Matter commissioning window via
// the bridge. When publisher is non-nil the handler emits
// `matter.commissioning_window_opened` after a successful open.
//
// Audit recording for the open is wired through the
// [MatterCommissioningWindowAudit] middleware in the daemon — the
// handler itself stays publisher-only so its existing test surface
// (15+ tests in matter_test.go) does not need a third parameter.
func MatterCommissioningWindow(opener MatterCommissioningOpener, publisher MatterEventPublisher) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if opener == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter bridge not enabled",
					"north.matter.enabled is false in the daemon config"))
			return
		}
		var body MatterCommissioningWindowRequest
		if err := DecodeJSON(req, &body); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, req,
					"Invalid request body", err.Error()))
			return
		}
		duration := body.DurationSeconds
		if duration == 0 {
			duration = 900 // Matter §11.19.8.1 default 15 minutes.
		}
		if duration < 180 || duration > 900 {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, req,
					"duration_seconds out of range",
					fmt.Sprintf("got %d, want 180..900", duration)))
			return
		}
		res, err := opener.OpenCommissioningWindow(req.Context(), duration)
		if err != nil {
			status := http.StatusInternalServerError
			pType := problem.TypeInternal
			switch {
			case errors.Is(err, ErrCommissioningInProgress):
				status = http.StatusConflict
				pType = problem.TypeConflict
			case errors.Is(err, ErrBridgeTopologyNotReady):
				status = http.StatusServiceUnavailable
				pType = problem.TypeServiceUnready
				w.Header().Set("Retry-After", "30")
			}
			problem.Write(w, status,
				problem.New(pType, req,
					"Failed to open commissioning window", err.Error()))
			return
		}
		response := MatterCommissioningWindowResponse(res)
		publishMatterEvent(req.Context(), publisher, MatterTopicCommissioningWindowOpened, response)
		JSON(w, http.StatusOK, response)
	}
}

// MatterFabricStore is the narrow facade GET /api/v1/matter/fabrics
// needs. Implemented by *matter/store.Store; tests use a fake.
type MatterFabricStore interface {
	ListFabrics(ctx context.Context) ([]store.FabricRecord, error)
}

// MatterFabricResponse is one entry in GET /api/v1/matter/fabrics.
// CompressedID + RootPublicKey are hex-encoded for transport-safety.
type MatterFabricResponse struct {
	FabricIndex   uint8  `json:"fabric_index"`
	FabricID      uint64 `json:"fabric_id"`
	NodeID        uint64 `json:"node_id"`
	VendorID      uint16 `json:"vendor_id"`
	Label         string `json:"label,omitempty"`
	CompressedID  string `json:"compressed_id"`
	RootPublicKey string `json:"root_public_key"`
}

// MatterFabricList is the body of GET /api/v1/matter/fabrics.
type MatterFabricList struct {
	Fabrics []MatterFabricResponse `json:"fabrics"`
}

// MatterFabrics serves the list of currently commissioned fabrics.
// Returns 503 service_unready when the underlying store is not wired
// (Matter feature disabled in this deployment).
func MatterFabrics(s MatterFabricStore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if s == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter bridge not enabled",
					"north.matter.enabled is false in the daemon config"))
			return
		}
		recs, err := s.ListFabrics(req.Context())
		if err != nil {
			problem.WriteFromError(w, req, err)
			return
		}
		out := make([]MatterFabricResponse, 0, len(recs))
		for _, r := range recs {
			out = append(out, MatterFabricResponse{
				FabricIndex:   r.FabricIndex,
				FabricID:      r.FabricID,
				NodeID:        r.NodeID,
				VendorID:      r.VendorID,
				Label:         r.Label,
				CompressedID:  hex.EncodeToString(r.CompressedID[:]),
				RootPublicKey: hex.EncodeToString(r.RootPublicKey),
			})
		}
		JSON(w, http.StatusOK, MatterFabricList{Fabrics: out})
	}
}
