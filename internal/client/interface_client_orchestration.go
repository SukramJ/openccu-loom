// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package client provides IC-level orchestration methods: Reconnect, SetValue, PutParamset,
// fetch helpers, bulk-data discovery, and IC-level wrappers for backend
// operations (service messages, install mode, rooms, functions, etc.).
//
// These mirror the gap items from the Python reference parity analysis:
// - InterfaceClient.reconnect (Python interface_client.py:1001)
// - InterfaceClient.set_value / put_paramset (orchestration layer)
// - InterfaceClient.fetch_all_device_data / fetch_device_details
// fetch_paramset_descriptions
// - InterfaceClient._write_unconfirmed_value / last_value_send_tracker
// - IC-level delegate wrappers for all new backend Operations methods.
package client

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	paramconvert "github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ReconnectConfig controls the exponential-backoff reconnect loop.
type ReconnectConfig struct {
	// InitialDelay is the wait before the first reconnect attempt.
	// Default: 2s.
	InitialDelay time.Duration

	// MaxDelay caps the backoff delay. Default: 120s.
	MaxDelay time.Duration

	// BackoffFactor is the exponential growth factor per attempt.
	// Default: 2.0.
	BackoffFactor float64
}

func (c *ReconnectConfig) applyDefaults() {
	if c.InitialDelay <= 0 {
		c.InitialDelay = 2 * time.Second
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 120 * time.Second
	}
	if c.BackoffFactor <= 0 {
		c.BackoffFactor = 2.0
	}
}

// Reconnect performs the reconnect orchestration with exponential
// Backoff. It mirrors reconnect
// (client/interface_client.py:1001):
//
// 1. When current state is INITIALIZED, transition to DISCONNECTED
// first so the RECONNECTING transition is valid.
// 2. When CanReconnect() is true, transition to RECONNECTING.
// 3. Compute the backoff delay: initial * factor^attempts, capped at max.
// 4. Sleep the backoff, then call ReinitProxy on b.
// 5. On success: reset circuit breakers, clear attempt counter,
// return (true, nil).
// 6. On failure: increment attempt counter, return (false, err).
//
// b is the backend on which to call Deinit + Init during reinit.
// interfaceID and callbackURL are passed verbatim to ReinitProxy.
// cfg controls the backoff parameters (nil applies defaults).
// reconnectAttempts is an in/out pointer — the caller owns the counter
// so multiple sequential Reconnect calls accumulate correctly.
func (c *InterfaceClient) Reconnect(
	ctx context.Context,
	b backends.Operations,
	interfaceID, callbackURL string,
	cfg *ReconnectConfig,
	reconnectAttempts *int,
) (bool, error) {
	var rcfg ReconnectConfig
	if cfg != nil {
		rcfg = *cfg
	}
	rcfg.applyDefaults()

	// Walk a live state into the CanReconnect-friendly slot. Reconnect
	// is triggered from the recovery coordinator on circuit-breaker
	// events; the client's state machine may still report INITIALIZED
	// (boot path), CONNECTED (CCU went silent without anyone flipping
	// our state), or RECONNECTING (previous attempt failed without
	// returning the machine to DISCONNECTED). Transition to
	// DISCONNECTED so CanReconnect returns true. CREATED / INITIALIZING
	// are not addressed here — those are initial-connect concerns and
	// outside the reconnect contract.
	currentState := c.ClientState()
	switch currentState {
	case hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
		hmenum.ClientStateReconnecting:
		_ = c.TransitionTo(
			hmenum.ClientStateDisconnected,
			"reconnect preparing",
			true,
			hmenum.FailureReasonNetwork,
		)
	default:
		// CREATED, INITIALIZING, DISCONNECTED, STOPPING, STOPPED, FAILED:
		// either already in a terminal/idle state or handled by the
		// initial-connect path — no transition needed here.
	}

	if !c.CanReconnect() {
		c.cfg.Logger.Warn(
			"Reconnect: state machine refuses reconnect",
			slog.String("central", c.cfg.CentralName),
			slog.String("interface", string(c.cfg.Interface)),
			slog.String("from_state", string(currentState)),
			slog.String("current_state", string(c.ClientState())),
		)
		return false, nil
	}

	// Transition to RECONNECTING.
	if err := c.TransitionTo(
		hmenum.ClientStateReconnecting,
		"reconnect initiated",
		false,
		hmenum.FailureReasonNone,
	); err != nil {
		// Already in RECONNECTING or state machine disallows it — not fatal.
		c.cfg.Logger.Debug(
			"Reconnect: TransitionTo RECONNECTING failed",
			slog.String("central", c.cfg.CentralName),
			slog.String("interface", string(c.cfg.Interface)),
			slog.Any("err", err),
		)
	}

	// Compute exponential backoff delay.
	attempts := 0
	if reconnectAttempts != nil {
		attempts = *reconnectAttempts
	}
	delay := time.Duration(
		math.Min(
			float64(rcfg.InitialDelay)*math.Pow(rcfg.BackoffFactor, float64(attempts)),
			float64(rcfg.MaxDelay),
		),
	)

	c.cfg.Logger.Debug(
		"Reconnect: waiting before reconnect",
		slog.String("central", c.cfg.CentralName),
		slog.String("interface", string(c.cfg.Interface)),
		slog.Duration("delay", delay),
		slog.Int("attempt", attempts+1),
	)

	// Sleep with context awareness.
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(delay):
	}

	// Re-init the proxy.
	if err := c.ReinitProxy(ctx, b, interfaceID, callbackURL); err != nil {
		if reconnectAttempts != nil {
			*reconnectAttempts++
		}
		// Walk RECONNECTING → DISCONNECTED so the next recovery
		// trigger finds a CanReconnect-friendly state. Without this
		// every follow-up attempt fails at the guard above with
		// "CanReconnect returned false".
		_ = c.TransitionTo(
			hmenum.ClientStateDisconnected,
			"reconnect failed",
			true,
			hmenum.FailureReasonNetwork,
		)
		c.cfg.Logger.Warn(
			"Reconnect: ReinitProxy failed",
			slog.String("central", c.cfg.CentralName),
			slog.String("interface", string(c.cfg.Interface)),
			slog.Any("err", err),
		)
		return false, err
	}

	// Success path: reset circuit breakers, clear attempt counter,
	// and walk the state back into CONNECTED so the next outage finds
	// a CanReconnect-friendly state. Without the explicit walk the
	// client would sit in RECONNECTING and reject every subsequent
	// recovery.trigger with "CanReconnect returned false".
	c.ResetCircuitBreakers()
	if reconnectAttempts != nil {
		*reconnectAttempts = 0
	}
	_ = c.TransitionTo(hmenum.ClientStateConnecting, "reconnect: → connecting", false, hmenum.FailureReasonNone)
	_ = c.TransitionTo(hmenum.ClientStateConnected, "reconnect: → connected", false, hmenum.FailureReasonNone)
	c.cfg.Logger.Info(
		"Reconnect: reconnected",
		slog.String("central", c.cfg.CentralName),
		slog.String("interface", string(c.cfg.Interface)),
	)
	return true, nil
}

// ---------------------------------------------------------------------------
// LastValueSendTracker — optimistic update tracking
// ---------------------------------------------------------------------------

// CommandTracker returns the command tracker attached to this client (the
// last_value_send_tracker in Python terms). It is constructed lazily on first
// access and stored in cfg.commandTracker (promoted via SetCommandTracker /
// CommandTracker accessor pair).
func (c *InterfaceClient) CommandTracker() *reliability.CommandTracker {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.commandTracker == nil {
		c.commandTracker = reliability.NewCommandTracker(
			string(c.cfg.Interface),
			reliability.CommandTrackerConfig{},
		)
	}
	return c.commandTracker
}

// WriteUnconfirmedValue records the sent value in the CommandTracker so
// north-bound adapters can return the optimistic value immediately before the
// CCU echoes back a callback.
//
// When parameter is in ConvertableParameters (COMBINED_PARAMETER or
// LEVEL_COMBINED), the combined wire string is decomposed into its constituent
// sub-parameters before recording — mirroring the Python add_set_value
// behaviour that routes convertable parameters through add_combined_parameter
// rather than recording the raw combined string. This ensures that subsequent
// north-bound reads on the constituent DPs (e.g. LEVEL, LEVEL_2) see the
// optimistic value rather than the opaque combined shorthand.
func (c *InterfaceClient) WriteUnconfirmedValue(
	channelAddress string,
	parameter hmenum.Parameter,
	paramsetKey hmenum.ParamsetKey,
	value any,
) {
	if paramconvert.IsConvertable(parameter) {
		if s, ok := value.(string); ok {
			c.CommandTracker().AddCombinedParameter(channelAddress, string(parameter), s)
			return
		}
	}
	c.CommandTracker().AddSetValue(channelAddress, parameter, paramsetKey, value)
}

// ---------------------------------------------------------------------------
// Discovery fetch helpers
// ---------------------------------------------------------------------------

// FetchAllDeviceData fetches all current parameter values for all devices via
// the backend and returns the raw data map.
//
// The returned map is keyed by channelAddress → paramName → value. Returns
// nil, nil when the backend does not support the call (ErrUnsupported).
func (c *InterfaceClient) FetchAllDeviceData(ctx context.Context, b backends.Operations) (map[string]map[string]any, error) {
	data, err := b.GetAllDeviceData(ctx)
	if err != nil {
		if isUnsupported(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// FetchDeviceDetails fetches device-name / ISE-ID / interface details for all
// known device addresses and returns the raw slice.
//
// addresses is the list of addresses to resolve (backend may ignore it and
// return all at once — e.g. CcuBackend.GetDeviceDetails). Returns nil, nil
// when the backend does not support the call.
func (c *InterfaceClient) FetchDeviceDetails(ctx context.Context, b backends.Operations, addresses []string) ([]map[string]any, error) {
	details, err := b.GetDeviceDetails(ctx, addresses)
	if err != nil {
		if isUnsupported(err) {
			return nil, nil
		}
		return nil, err
	}
	return details, nil
}

// FetchParamsetDescriptions fetches the paramset descriptions for a single
// device description from the backend.
//
// It iterates over the PARAMSETS field of deviceDescription and fetches each
// non-LINK paramset description. Returns a map of channelAddress →
// paramsetKey → paramName → ParameterData.
//
// The data is NOT written to any store — the coordinator is responsible for
// persisting the results.
func (c *InterfaceClient) FetchParamsetDescriptions(
	ctx context.Context,
	b backends.Operations,
	deviceDescription map[string]any,
) (map[string]map[hmenum.ParamsetKey]map[string]any, error) {
	address, _ := deviceDescription["ADDRESS"].(string)
	if address == "" {
		return nil, nil
	}
	rawParamsets, _ := deviceDescription["PARAMSETS"].([]any)

	result := make(map[string]map[hmenum.ParamsetKey]map[string]any)
	result[address] = make(map[hmenum.ParamsetKey]map[string]any)

	for _, rawKey := range rawParamsets {
		pKeyStr, _ := rawKey.(string)
		if pKeyStr == "" {
			continue
		}
		pKey := hmenum.ParamsetKey(pKeyStr)
		// Skip LINK paramsets — they are only relevant for device linking
		// and are fetched dynamically.
		if pKey == hmenum.ParamsetKeyLink {
			continue
		}
		desc, err := b.GetParamsetDescription(ctx, address, pKey)
		if err != nil {
			if isUnsupported(err) {
				continue
			}
			c.cfg.Logger.Warn(
				"FetchParamsetDescriptions: GetParamsetDescription failed",
				slog.String("central", c.cfg.CentralName),
				slog.String("address", address),
				slog.String("paramset_key", string(pKey)),
				slog.Any("err", err),
			)
			continue
		}
		// Convert hmproto.ParameterData map to map[string]any for
		// compatibility with the coordinator store layer.
		flat := make(map[string]any, len(desc))
		for k := range desc {
			flat[k] = desc[k]
		}
		result[address][pKey] = flat
	}
	return result, nil
}

// GetDeviceDescriptionWithCoalescing fetches the raw device description for
// address using the request coalescer to deduplicate concurrent in-flight
// calls. Returns nil, nil on ErrUnsupported.
func (c *InterfaceClient) GetDeviceDescriptionWithCoalescing(
	ctx context.Context,
	b backends.Operations,
	address string,
) (map[string]any, error) {
	key := reliability.MakeCoalesceKey("getDeviceDescription", []any{address})
	result, err := c.cfg.Coalescer.Do(ctx, key, func(ctx context.Context) (any, error) {
		m, e := b.GetDeviceDescription(ctx, address)
		return m, e
	})
	if err != nil {
		if isUnsupported(err) {
			return nil, nil
		}
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	m, ok := result.(map[string]any)
	if !ok {
		return nil, nil
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// IC-level delegate wrappers for new backend Operations methods
// ---------------------------------------------------------------------------

// AcceptDeviceInInbox accepts a device from the pairing inbox. Returns false
// when the capability is not available.
func (c *InterfaceClient) AcceptDeviceInInbox(ctx context.Context, b backends.Operations, deviceAddress string) (bool, error) {
	if !b.Capabilities().InboxDevices {
		return false, nil
	}
	return b.AcceptDeviceInInbox(ctx, deviceAddress)
}

// GetInstallMode returns the remaining pairing-mode time in seconds. Returns
// 0 when the capability is not available.
func (c *InterfaceClient) GetInstallMode(ctx context.Context, b backends.Operations) (int, error) {
	if !b.Capabilities().InstallMode {
		return 0, nil
	}
	return b.GetInstallMode(ctx)
}

// SetInstallMode enables or disables CCU pairing mode. Returns false when the
// capability is not available.
func (c *InterfaceClient) SetInstallMode(ctx context.Context, b backends.Operations, on bool, duration, mode int, deviceAddress string) (bool, error) {
	if !b.Capabilities().InstallMode {
		return false, nil
	}
	err := b.SetInstallMode(ctx, on, duration, mode, deviceAddress)
	return err == nil, err
}

// GetServiceMessages returns all active service messages. Returns nil when
// the capability is not available.
func (c *InterfaceClient) GetServiceMessages(ctx context.Context, b backends.Operations, messageType string) ([]map[string]any, error) {
	if !b.Capabilities().ServiceMessages {
		return nil, nil
	}
	return b.GetServiceMessages(ctx, messageType)
}

// SuppressServiceMessage suppresses or unsuppresses a service message.
func (c *InterfaceClient) SuppressServiceMessage(ctx context.Context, b backends.Operations, channelAddress, parameterID string, suppress bool) (bool, error) {
	if !b.Capabilities().SuppressServiceMessage {
		return false, nil
	}
	err := b.SuppressServiceMessage(ctx, channelAddress, parameterID, suppress)
	return err == nil, err
}

// GetAlarmMessages returns all active alarm messages. Returns nil when the
// capability is not available.
func (c *InterfaceClient) GetAlarmMessages(ctx context.Context, b backends.Operations) ([]map[string]any, error) {
	if !b.Capabilities().AlarmMessages {
		return nil, nil
	}
	return b.GetAlarmMessages(ctx)
}

// GetAllRooms returns all CCU rooms. Returns nil when the capability is not
// available.
func (c *InterfaceClient) GetAllRooms(ctx context.Context, b backends.Operations) (map[string][]string, error) {
	if !b.Capabilities().Rooms {
		return nil, nil
	}
	return b.GetAllRooms(ctx)
}

// GetAllFunctions returns all CCU functions (Gewerke). Returns nil when the
// capability is not available.
func (c *InterfaceClient) GetAllFunctions(ctx context.Context, b backends.Operations) (map[string][]string, error) {
	if !b.Capabilities().Functions {
		return nil, nil
	}
	return b.GetAllFunctions(ctx)
}

// RenameDevice renames a device by ISE-ID. Returns false when the capability
// is not available.
func (c *InterfaceClient) RenameDevice(ctx context.Context, b backends.Operations, iseID int, newName string) (bool, error) {
	if !b.Capabilities().Rename {
		return false, nil
	}
	return b.RenameDevice(ctx, iseID, newName)
}

// RenameChannel renames a channel by ISE-ID. Returns false when the
// capability is not available.
func (c *InterfaceClient) RenameChannel(ctx context.Context, b backends.Operations, iseID int, newName string) (bool, error) {
	if !b.Capabilities().Rename {
		return false, nil
	}
	return b.RenameChannel(ctx, iseID, newName)
}

// ExecuteProgram executes a CCU program. Returns false when the capability is
// not available.
func (c *InterfaceClient) ExecuteProgram(ctx context.Context, b backends.Operations, iseID string) (bool, error) {
	if !b.Capabilities().ExecuteProgram {
		return false, nil
	}
	return b.ExecuteProgram(ctx, iseID)
}

// GetSystemVariable returns a single system variable by name.
func (c *InterfaceClient) GetSystemVariable(ctx context.Context, b backends.Operations, name string) (any, error) {
	return b.GetSystemVariable(ctx, name)
}

// GetAllSystemVariables returns all system variables.
func (c *InterfaceClient) GetAllSystemVariables(ctx context.Context, b backends.Operations) ([]map[string]any, error) {
	return b.GetAllSystemVariables(ctx)
}

// CreateBackupAndDownload triggers a CCU config backup and downloads it.
// Returns nil when the capability is not available.
func (c *InterfaceClient) CreateBackupAndDownload(ctx context.Context, b backends.Operations, maxWaitTime, pollInterval float64) ([]byte, error) {
	if !b.Capabilities().Backup {
		return nil, nil
	}
	return b.CreateBackupAndDownload(ctx, maxWaitTime, pollInterval)
}

// TriggerFirmwareUpdate triggers a CCU firmware update. Returns false when
// the capability is not available.
func (c *InterfaceClient) TriggerFirmwareUpdate(ctx context.Context, b backends.Operations) (bool, error) {
	if !b.Capabilities().FirmwareUpdate {
		return false, nil
	}
	return b.TriggerFirmwareUpdate(ctx)
}

// AddLink creates a direct link between two channels. No-op when the
// capability is not available.
func (c *InterfaceClient) AddLink(ctx context.Context, b backends.Operations, senderAddress, receiverAddress, name, description string) error {
	if !b.Capabilities().LinkOperations {
		return nil
	}
	return b.AddLink(ctx, senderAddress, receiverAddress, name, description)
}

// GetLinkPeers returns peer channel addresses. Returns nil when the
// capability is not available.
func (c *InterfaceClient) GetLinkPeers(ctx context.Context, b backends.Operations, channelAddress string) ([]string, error) {
	if !b.Capabilities().LinkOperations {
		return nil, nil
	}
	return b.GetLinkPeers(ctx, channelAddress)
}

// RemoveLink removes a direct link. No-op when the capability is not
// available.
func (c *InterfaceClient) RemoveLink(ctx context.Context, b backends.Operations, senderAddress, receiverAddress string) error {
	if !b.Capabilities().LinkOperations {
		return nil
	}
	return b.RemoveLink(ctx, senderAddress, receiverAddress)
}

// ---------------------------------------------------------------------------
// IC-level wrappers for new Operations methods
// ---------------------------------------------------------------------------

// DeleteSystemVariable deletes a CCU system variable by name. Returns false
// when the capability is not available.
func (c *InterfaceClient) DeleteSystemVariable(ctx context.Context, b backends.Operations, name string) (bool, error) {
	if !b.Capabilities().DeleteSystemVariable {
		return false, nil
	}
	return b.DeleteSystemVariable(ctx, name)
}

// GetIseIDByAddress resolves a device or channel address to its ReGa ISE-ID.
// Returns 0 when the capability is not available.
func (c *InterfaceClient) GetIseIDByAddress(ctx context.Context, b backends.Operations, address string) (int, error) {
	if !b.Capabilities().IseIDLookup {
		return 0, nil
	}
	return b.GetIseIDByAddress(ctx, address)
}

// GetLinkInfo returns the name and description of a direct link. Returns nil
// when the capability is not available.
func (c *InterfaceClient) GetLinkInfo(ctx context.Context, b backends.Operations, iface, senderAddress, receiverAddress string) (map[string]any, error) {
	if !b.Capabilities().LinkOperations {
		return nil, nil
	}
	return b.GetLinkInfo(ctx, iface, senderAddress, receiverAddress)
}

// SetLinkInfo sets the name and description of a direct link. Returns false
// when the capability is not available.
func (c *InterfaceClient) SetLinkInfo(ctx context.Context, b backends.Operations, iface, senderAddress, receiverAddress, name, description string) (bool, error) {
	if !b.Capabilities().LinkOperations {
		return false, nil
	}
	return b.SetLinkInfo(ctx, iface, senderAddress, receiverAddress, name, description)
}

// GetSuppressedServiceMessages returns the list of suppressed service message
// parameter IDs for channelAddress. Returns nil when the capability is not
// available.
func (c *InterfaceClient) GetSuppressedServiceMessages(ctx context.Context, b backends.Operations, iface, channelAddress string) ([]string, error) {
	if !b.Capabilities().SuppressServiceMessage {
		return nil, nil
	}
	return b.GetSuppressedServiceMessages(ctx, iface, channelAddress)
}

// HasProgramIDs reports whether the CCU program identified by iseID exists.
// Returns false when the capability is not available.
func (c *InterfaceClient) HasProgramIDs(ctx context.Context, b backends.Operations, iseID string) (bool, error) {
	if !b.Capabilities().GetAllPrograms {
		return false, nil
	}
	return b.HasProgramIDs(ctx, iseID)
}

// GetParamsetDescriptionOnDemand fetches a single paramset description on
// demand (without caching). Unlike FetchParamsetDescriptions this does NOT
// skip LINK paramsets and does NOT write to any store — the caller owns the
// result.
func (c *InterfaceClient) GetParamsetDescriptionOnDemand(
	ctx context.Context,
	b backends.Operations,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
) (map[string]any, error) {
	desc, err := b.GetParamsetDescription(ctx, channelAddress, paramsetKey)
	if err != nil {
		if isUnsupported(err) {
			return nil, nil
		}
		return nil, err
	}
	// Convert hmproto.ParameterData map to map[string]any.
	flat := make(map[string]any, len(desc))
	for k := range desc {
		flat[k] = desc[k]
	}
	return flat, nil
}

// ---------------------------------------------------------------------------
// SetValue / PutParamset — IC-level orchestration
// ---------------------------------------------------------------------------

// SetValue sends a single parameter value to the backend, passing through
// the throttle + retry stack. It records the sent value via the CommandTracker
// For optimistic-update feedback.
// InterfaceClient.set_value (interface_client.py:1125).
//
// Unlike the Python version this method does not implement wait_for_callback
// (that is a coordinator concern) nor check_against_pd validation (wire the
// parameter package for that). It is the minimal reliable dispatch layer.
//
// When skipRetry is true the call bypasses the backoff/retry tracking and
// executes the backend operation exactly once (single-shot). This.
// (e.g. virtual key presses) where a retry would cause a duplicate action.
func (c *InterfaceClient) SetValue(
	ctx context.Context,
	b backends.Operations,
	channelAddress string,
	parameter hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
	rxMode hmenum.CommandRxMode,
	skipRetry bool,
) error {
	throttle := c.cfg.WriteThrottle
	if err := throttle.Acquire(ctx, priority); err != nil {
		return err
	}
	defer throttle.Release()

	dpKey := hmtypes.DataPointKey{ChannelAddress: channelAddress, Parameter: string(parameter)}
	err := c.cfg.Circuit.Do(ctx, "setValue", func(ctx context.Context) error {
		if skipRetry {
			return c.cfg.Retrier.DoOnce(ctx, func(ctx context.Context, _ int) error {
				return b.SetValue(ctx, channelAddress, parameter, value, priority, rxMode)
			})
		}
		return c.cfg.Retrier.DoForKey(ctx, dpKey, func(ctx context.Context, _ int) error {
			return b.SetValue(ctx, channelAddress, parameter, value, priority, rxMode)
		})
	})
	if err != nil {
		return err
	}
	// Record the sent value for optimistic-update feedback.
	c.WriteUnconfirmedValue(channelAddress, parameter, hmenum.ParamsetKeyValues, value)
	// Notify the optional session-recorder hook so CacheCoordinator can
	// capture the CCU communication trace when recording is active.
	if hook := c.cfg.SessionRecorderHook; hook != nil {
		hook("xml-rpc", "setValue", []any{channelAddress, string(parameter), value}, nil)
	}
	return nil
}

// PutParamset sends a full paramset atomically to the backend, passing
// through the throttle + retry stack.
//
// paramsetKeyOrLinkAddress is either a [hmenum.ParamsetKey] (e.g. "MASTER")
// or a peer channel address for LINK paramsets. The method dispatches to
// PutLinkParamset when paramsetKeyOrLinkAddress looks like a channel address
// (contains ":"), otherwise to PutParamset.
//
// When skipRetry is true the call is executed exactly once without backoff or
// retry tracking.
func (c *InterfaceClient) PutParamset(
	ctx context.Context,
	b backends.Operations,
	channelAddress string,
	paramsetKeyOrLinkAddress string,
	values map[string]any,
	priority hmenum.CommandPriority,
	rxMode hmenum.CommandRxMode,
	skipRetry bool,
) error {
	throttle := c.cfg.WriteThrottle
	if err := throttle.Acquire(ctx, priority); err != nil {
		return err
	}
	defer throttle.Release()

	dpKey := hmtypes.DataPointKey{ChannelAddress: channelAddress, Parameter: paramsetKeyOrLinkAddress}
	err := c.cfg.Circuit.Do(ctx, "putParamset", func(ctx context.Context) error {
		if skipRetry {
			return c.cfg.Retrier.DoOnce(ctx, func(ctx context.Context, _ int) error {
				if isChannelAddress(paramsetKeyOrLinkAddress) {
					return b.PutLinkParamset(ctx, channelAddress, paramsetKeyOrLinkAddress, values)
				}
				pKey := hmenum.ParamsetKey(paramsetKeyOrLinkAddress)
				return b.PutParamset(ctx, channelAddress, pKey, values, rxMode)
			})
		}
		return c.cfg.Retrier.DoForKey(ctx, dpKey, func(ctx context.Context, _ int) error {
			// If the second arg contains ":" it is a channel address → LINK paramset.
			if isChannelAddress(paramsetKeyOrLinkAddress) {
				return b.PutLinkParamset(ctx, channelAddress, paramsetKeyOrLinkAddress, values)
			}
			pKey := hmenum.ParamsetKey(paramsetKeyOrLinkAddress)
			return b.PutParamset(ctx, channelAddress, pKey, values, rxMode)
		})
	})
	if err != nil {
		return err
	}
	// Notify the optional session-recorder hook.
	if hook := c.cfg.SessionRecorderHook; hook != nil {
		hook("xml-rpc", "putParamset", []any{channelAddress, paramsetKeyOrLinkAddress, values}, nil)
	}
	return nil
}

// isChannelAddress reports whether addr looks like a channel address
// (contains ":"— e.g. "MEQ0123456:1").
func isChannelAddress(addr string) bool {
	for _, r := range addr {
		if r == ':' {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// ModifiedAt — last-modification timestamp on IC
// ---------------------------------------------------------------------------

// ModifiedAt returns the timestamp of the last modification on this client.
// The zero value is returned when the client has not been touched yet.
func (c *InterfaceClient) ModifiedAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.modifiedAt
}

// SetModifiedAt updates the last-modification timestamp. Called from the
// central's event path whenever a DataPoint value is received for this
// interface.
func (c *InterfaceClient) SetModifiedAt(t time.Time) {
	c.mu.Lock()
	c.modifiedAt = t
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// MarkAllDevicesForced — forced-availability marking
// ---------------------------------------------------------------------------

// ForcedAvailability describes the forced availability mode for all devices
// on an interface.
type ForcedAvailability int

const (
	// ForcedAvailabilityNone means no forced override — use the real state.
	ForcedAvailabilityNone ForcedAvailability = 0
	// ForcedAvailabilityTrue forces all devices to report as available.
	ForcedAvailabilityTrue ForcedAvailability = 1
	// ForcedAvailabilityFalse forces all devices to report as unavailable.
	ForcedAvailabilityFalse ForcedAvailability = 2
)

// MarkAllDevicesForced stores the forced-availability mode for this interface
// so the domain layer can override real availability. The actual
// device-marking logic is left to the caller (coordinator / device registry)
// which holds the device list. This method stores the requested mode so
// coordinators can query it.
func (c *InterfaceClient) MarkAllDevicesForced(mode ForcedAvailability) {
	c.mu.Lock()
	c.forcedAvailability = mode
	c.mu.Unlock()
}

// ForcedAvailabilityMode returns the current forced-availability mode.
// Coordinators call this on reconnect / disconnect events to learn what
// availability state to push to the device registry.
func (c *InterfaceClient) ForcedAvailabilityMode() ForcedAvailability {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.forcedAvailability
}

// ---------------------------------------------------------------------------
// UpdateParamsetDescriptions — convenience wrapper (L09)
// ---------------------------------------------------------------------------

// DeviceDescriptionFinder resolves a device address to its raw device
// description map. The consumer-side interface placed here mirrors the
// Go convention of defining interfaces in the consumer package; the
// concrete implementation is the central's CacheCoordinator
// (DeviceDescriptionRegistry).
//
// Returns (nil, nil) when the address is unknown — matching Python's
// None return from find_device_description.
type DeviceDescriptionFinder interface {
	FindDeviceDescription(ctx context.Context, address string) (map[string]any, error)
}

// ParamsetDescriptionPersister is called after new paramset descriptions have
// been fetched to trigger a durable write.
type ParamsetDescriptionPersister interface {
	PersistParamsetDescriptions(ctx context.Context) error
}

// UpdateParamsetDescriptions is a convenience wrapper that:
//
//  1. Looks up the device description for deviceAddress via finder.
//  2. When found, fetches fresh paramset descriptions from the backend via
//     [FetchParamsetDescriptions].
//  3. Persists the result via persister (mirrors Python
//     `central.save_files(save_paramset_descriptions=True)`).
//
// Returns nil silently when the address is not found in the finder — this
// Py:1284). Mirrors
// Update_paramset_descriptions
// (interface_client.py:1280). Closes parity-audit gap L09.
func (c *InterfaceClient) UpdateParamsetDescriptions(
	ctx context.Context,
	b backends.Operations,
	finder DeviceDescriptionFinder,
	persister ParamsetDescriptionPersister,
	deviceAddress string,
) error {
	desc, err := finder.FindDeviceDescription(ctx, deviceAddress)
	if err != nil {
		return fmt.Errorf("UpdateParamsetDescriptions: find device description: %w", err)
	}
	if desc == nil {
		// Address unknown — silent no-op, mirrors Python's guard check.
		return nil
	}
	if _, err := c.FetchParamsetDescriptions(ctx, b, desc); err != nil {
		return fmt.Errorf("UpdateParamsetDescriptions: fetch: %w", err)
	}
	if err := persister.PersistParamsetDescriptions(ctx); err != nil {
		return fmt.Errorf("UpdateParamsetDescriptions: persist: %w", err)
	}
	return nil
}

// isUnsupported returns true when err is or wraps ErrUnsupported.
func isUnsupported(err error) bool {
	return err != nil && err.Error() == backends.ErrUnsupported.Error()
}

// ---------------------------------------------------------------------------
// GetValue — direct single-parameter read
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// GetAllPrograms with client-side marker filtering
// ---------------------------------------------------------------------------

// GetAllProgramsFiltered returns all CCU automation programs, optionally
// filtered by markers. When markers is non-empty only programs whose
// description contains at least one of the marker strings are returned.
// Returns nil when the capability is not available.
func (c *InterfaceClient) GetAllProgramsFiltered(
	ctx context.Context,
	b backends.Operations,
	markers []string,
) ([]map[string]any, error) {
	if !b.Capabilities().GetAllPrograms {
		return nil, nil
	}
	all, err := b.GetAllPrograms(ctx)
	if err != nil {
		return nil, err
	}
	if len(markers) == 0 {
		return all, nil
	}
	out := make([]map[string]any, 0, len(all))
	for _, p := range all {
		desc, _ := p["description"].(string)
		if containsAnyMarker(desc, markers) {
			out = append(out, p)
		}
	}
	return out, nil
}

// containsAnyMarker returns true when s contains at least one marker as
// A substring. Client-side filter mirrors
// in json_rpc.py:get_all_programs.
func containsAnyMarker(s string, markers []string) bool {
	for _, m := range markers {
		if m != "" && containsSubstring(s, m) {
			return true
		}
	}
	return false
}

func containsSubstring(s, sub string) bool {
	if sub == "" || len(s) < len(sub) {
		return sub == ""
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// GetAllSystemVariablesFiltered with markedOnly filter
// ---------------------------------------------------------------------------

// GetAllSystemVariablesFiltered returns all CCU system variables, optionally
// filtering to only those whose name starts with a marker prefix. When
// markedOnly is false all variables are returned.
func (c *InterfaceClient) GetAllSystemVariablesFiltered(
	ctx context.Context,
	b backends.Operations,
	markedOnly bool,
	markers []string,
) ([]map[string]any, error) {
	all, err := b.GetAllSystemVariables(ctx)
	if err != nil {
		return nil, err
	}
	if !markedOnly || len(markers) == 0 {
		return all, nil
	}
	out := make([]map[string]any, 0, len(all))
	for _, sv := range all {
		name, _ := sv["name"].(string)
		if containsAnyMarker(name, markers) {
			out = append(out, sv)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// GetValue — direct single-parameter read
// ---------------------------------------------------------------------------

// GetValue reads one parameter's current value directly from the CCU
// (bypassing the event-coordinator cache). Used for refresh + initial
// load.
func (c *InterfaceClient) GetValue(
	ctx context.Context,
	b backends.Operations,
	channelAddress string,
	parameter hmenum.Parameter,
) (any, error) {
	return b.GetValue(ctx, channelAddress, parameter)
}

// ---------------------------------------------------------------------------
// UpdateDeviceFirmware — delegate to backend UpdateFirmware
// ---------------------------------------------------------------------------

// UpdateDeviceFirmware requests a firmware update for the device at
// deviceAddress. Returns ErrUnsupported on backends without that capability.
func (c *InterfaceClient) UpdateDeviceFirmware(
	ctx context.Context,
	b backends.Operations,
	deviceAddress string,
) error {
	return b.UpdateFirmware(ctx, deviceAddress)
}

// ---------------------------------------------------------------------------
// ReportValueUsage — central-link event routing
// ---------------------------------------------------------------------------

// ReportValueUsage tells the CCU that an event-parameter on the given channel
// is consumed by a logic peer. The CCU uses the ref-counter to decide whether
// to deliver press events to the central. Delegates to
// [backends.Operations.ReportValueUsage]. Returns [backends.ErrUnsupported]
// on backends that do not support central-link routing (e.g. CUxD).
func (c *InterfaceClient) ReportValueUsage(
	ctx context.Context,
	b backends.Operations,
	channelAddress, valueID string,
	refCounter int,
) error {
	return b.ReportValueUsage(ctx, channelAddress, valueID, refCounter)
}

// ---------------------------------------------------------------------------
// GetMetadata / SetMetadata — device-metadata operations
// ---------------------------------------------------------------------------

// GetMetadata reads a metadata blob attached to a device. Delegates to
// [backends.Operations.GetMetadata]. Returns [backends.ErrUnsupported] on
// backends that do not support metadata (all non-Homegear backends).
func (c *InterfaceClient) GetMetadata(
	ctx context.Context,
	b backends.Operations,
	address, dataID string,
) (any, error) {
	return b.GetMetadata(ctx, address, dataID)
}

// SetMetadata writes a metadata blob for a device.
// Delegates to [backends.Operations.SetMetadata]. Returns
// [backends.ErrUnsupported] on backends that do not support metadata.
func (c *InterfaceClient) SetMetadata(
	ctx context.Context,
	b backends.Operations,
	address, dataID string,
	value any,
) error {
	return b.SetMetadata(ctx, address, dataID, value)
}

// ---------------------------------------------------------------------------
// CreateSysvar* wrappers
// ---------------------------------------------------------------------------

// CreateSystemVariableBool creates a new boolean system variable on the CCU.
// Returns nil when the capability is not available.
func (c *InterfaceClient) CreateSystemVariableBool(
	ctx context.Context,
	b backends.Operations,
	name string,
	initVal bool,
) (map[string]any, error) {
	if !b.Capabilities().CreateSystemVariable {
		return nil, nil
	}
	return b.CreateSystemVariableBool(ctx, name, initVal)
}

// CreateSystemVariableEnum creates a new enum system variable on the CCU.
// Returns nil when the capability is not available.
func (c *InterfaceClient) CreateSystemVariableEnum(
	ctx context.Context,
	b backends.Operations,
	name string,
	valueList []string,
) (map[string]any, error) {
	if !b.Capabilities().CreateSystemVariable {
		return nil, nil
	}
	return b.CreateSystemVariableEnum(ctx, name, valueList)
}

// CreateSystemVariableFloat creates a new float system variable on the CCU.
// Returns nil when the capability is not available.
func (c *InterfaceClient) CreateSystemVariableFloat(
	ctx context.Context,
	b backends.Operations,
	name string,
	minValue, maxValue float64,
) (map[string]any, error) {
	if !b.Capabilities().CreateSystemVariable {
		return nil, nil
	}
	return b.CreateSystemVariableFloat(ctx, name, minValue, maxValue)
}
