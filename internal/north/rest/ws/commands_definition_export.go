// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/SukramJ/openccu-loom/internal/model/device/definitionexport"
)

// DefinitionExporter produces an anonymised device-definition zip for a
// device, byte-compatible with the Python reference's export_device_definition.
// *adapter.DefinitionExportDomain satisfies it.
type DefinitionExporter interface {
	ExportDefinition(ctx context.Context, deviceAddress string) (model string, zip []byte, err error)
}

// definitionExportHandler backs the `devices.export_definition` command. WS
// frames are JSON, so the zip archive is returned base64-encoded alongside the
// device model and a suggested filename.
func definitionExportHandler(svc DefinitionExporter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args deviceAddrArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address required")
		}
		model, archive, err := svc.ExportDefinition(ctx, args.Address)
		if err != nil {
			if errors.Is(err, definitionexport.ErrDeviceNotFound) {
				return nil, NewCommandError("not_found", "no device at "+args.Address)
			}
			return nil, NewCommandError(CommandErrorInternal, "export_definition: "+err.Error())
		}
		return map[string]any{
			"model":    model,
			"filename": model + ".zip",
			"encoding": "base64",
			"data":     base64.StdEncoding.EncodeToString(archive),
		}, nil
	}
}
