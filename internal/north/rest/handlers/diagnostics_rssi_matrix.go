// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// defaultReceiverMarginDB is the proposal margin when the caller names
// none; it matches the service-side default so an omitted query parameter
// and an explicit default answer identically.
const defaultReceiverMarginDB = 6

// maxReceiverMarginDB bounds margin_db: beyond it no realistic pair of
// readings can clear the margin, so a larger value is a caller mistake.
const maxReceiverMarginDB = 30

// rssiMatrixResponse is the `RSSIMatrixResponse` wire body.
type rssiMatrixResponse struct {
	Items []rssiMatrixCentralDTO `json:"items"`
}

type rssiMatrixCentralDTO struct {
	Central     string                   `json:"central"`
	InterfaceID string                   `json:"interface_id"`
	Interfaces  []rssiMatrixInterfaceDTO `json:"interfaces"`
	Devices     []rssiMatrixDeviceDTO    `json:"devices"`
	Error       string                   `json:"error,omitempty"`
}

type rssiMatrixInterfaceDTO struct {
	Address     string `json:"address"`
	Description string `json:"description"`
	Connected   bool   `json:"connected"`
	Default     bool   `json:"default"`
	DutyCycle   int    `json:"duty_cycle"`
}

type rssiMatrixDeviceDTO struct {
	Address  string                 `json:"address"`
	Name     string                 `json:"name,omitempty"`
	Partners []rssiMatrixPartnerDTO `json:"partners"`
}

type rssiMatrixPartnerDTO struct {
	Address string `json:"address"`
	RxDBm   *int   `json:"rx_dbm"`
	TxDBm   *int   `json:"tx_dbm"`
}

// receiverProposalResponse is the `ReceiverProposalResponse` wire body.
type receiverProposalResponse struct {
	Items []receiverProposalDTO `json:"items"`
}

type receiverProposalDTO struct {
	Address          string `json:"address"`
	Name             string `json:"name,omitempty"`
	Central          string `json:"central"`
	CurrentInterface string `json:"current_interface"`
	BestInterface    string `json:"best_interface"`
	CurrentRxDBm     *int   `json:"current_rx_dbm"`
	BestRxDBm        *int   `json:"best_rx_dbm"`
	Roaming          bool   `json:"roaming"`
	Verdict          string `json:"verdict"`
}

// DiagnosticsRSSIMatrix serves GET /diagnostics/rssi/matrix — the
// BidCos-RF daemon's pairwise reception matrix, read live from every
// central's BidCos-RF interface. A direction without information is null.
func DiagnosticsRSSIMatrix(svc interfaces.RSSIMatrixService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagnostics unavailable", "no RSSI matrix source"))
			return
		}
		centrals, err := svc.RSSIMatrix(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "RSSI matrix query failed", err)
			return
		}
		resp := rssiMatrixResponse{Items: make([]rssiMatrixCentralDTO, 0, len(centrals))}
		for _, c := range centrals {
			dto := rssiMatrixCentralDTO{
				Central:     c.Central,
				InterfaceID: c.InterfaceID,
				Interfaces:  make([]rssiMatrixInterfaceDTO, 0, len(c.Interfaces)),
				Devices:     make([]rssiMatrixDeviceDTO, 0, len(c.Devices)),
				Error:       c.Error,
			}
			for _, g := range c.Interfaces {
				dto.Interfaces = append(dto.Interfaces, rssiMatrixInterfaceDTO(g))
			}
			for _, d := range c.Devices {
				dev := rssiMatrixDeviceDTO{
					Address:  d.Address,
					Name:     d.Name,
					Partners: make([]rssiMatrixPartnerDTO, 0, len(d.Partners)),
				}
				for _, p := range d.Partners {
					dev.Partners = append(dev.Partners, rssiMatrixPartnerDTO(p))
				}
				dto.Devices = append(dto.Devices, dev)
			}
			resp.Items = append(resp.Items, dto)
		}
		JSON(w, http.StatusOK, resp)
	}
}

// ReceiverProposalHandler serves GET /diagnostics/rssi/receiver-proposal —
// the best-gateway dry run over the current matrix. margin_db (0..30,
// default 6) is the minimum advantage before a switch is proposed. Nothing
// is written.
func ReceiverProposalHandler(svc interfaces.RSSIMatrixService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagnostics unavailable", "no RSSI matrix source"))
			return
		}
		margin := defaultReceiverMarginDB
		if raw := r.URL.Query().Get("margin_db"); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 0 || v > maxReceiverMarginDB {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeValidation, r, "Invalid margin_db", "margin_db must be an integer between 0 and 30"))
				return
			}
			margin = v
		}
		proposals, err := svc.ReceiverProposal(r.Context(), margin)
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Receiver proposal failed", err)
			return
		}
		resp := receiverProposalResponse{Items: make([]receiverProposalDTO, 0, len(proposals))}
		for _, p := range proposals {
			resp.Items = append(resp.Items, receiverProposalDTO(p))
		}
		JSON(w, http.StatusOK, resp)
	}
}

// DeviceRFInterfacePort assigns a BidCos-RF device to an RF gateway.
// *adapter.DeviceAdminDomain satisfies it; the call resolves the owning
// backend by address and issues `setBidcosInterface`.
type DeviceRFInterfacePort interface {
	AssignRFInterface(ctx context.Context, address, interfaceAddress string, roaming bool) error
}

// rfInterfaceAssignRequest is the `RFInterfaceAssignRequest` body. Roaming
// is a pointer so an omitted flag is rejected rather than read as false.
type rfInterfaceAssignRequest struct {
	InterfaceAddress string `json:"interface_address"`
	Roaming          *bool  `json:"roaming"`
}

// AssignRFInterface serves `POST /devices/{addr}/rf-interface`: it pins a
// BidCos-RF device to the named RF gateway, or with roaming lets the
// BidCos-RF daemon re-assign it by signal strength. The call completes
// synchronously, so success answers 204. Devices on any other interface
// answer 422.
func AssignRFInterface(svc DeviceRFInterfacePort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "RF interface assignment unwired", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if addr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing address", "addr path parameter is required"))
			return
		}
		var req rfInterfaceAssignRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		req.InterfaceAddress = strings.TrimSpace(req.InterfaceAddress)
		if req.InterfaceAddress == "" || req.Roaming == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid request", "interface_address and roaming are required"))
			return
		}
		if err := svc.AssignRFInterface(r.Context(), addr, req.InterfaceAddress, *req.Roaming); err != nil {
			if problem.WriteFeatureUnavailable(w, r, err) {
				return
			}
			if errors.Is(err, backends.ErrUnsupported) {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "RF interface assignment not supported on this interface", ""))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "RF interface assignment failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:          identityFromCtx(r.Context()),
				Action:        audit.ActionDeviceRFInterfaceAssign,
				DeviceAddress: addr,
				Note:          "interface " + req.InterfaceAddress + ", roaming " + strconv.FormatBool(*req.Roaming),
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
