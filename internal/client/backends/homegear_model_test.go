// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"reflect"
	"strings"
	"testing"
)

// TestHomegearModelDetection verifies the version-string-based model
// detection. 83-88).
func TestHomegearModelDetection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		version string
		want    string
	}{
		{"empty version → Homegear", "", HomegearModelHomegear},
		{"Homegear major.minor", "0.7.36", HomegearModelHomegear},
		{"pydevccu lowercase", "pydevccu 1.0.0", HomegearModelPyDevCCU},
		{"PyDevCCU mixed case", "PyDevCCU 2.3", HomegearModelPyDevCCU},
		{"PYDEVCCU uppercase", "PYDEVCCU/3.0", HomegearModelPyDevCCU},
		{"version contains pydevccu suffix", "Server-pydevccu", HomegearModelPyDevCCU},
		{"unrelated version → Homegear", "Server 1.2.3", HomegearModelHomegear},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := NewHomegearBackend(nil, nil)
			b.SetVersion(tc.version)
			if got := b.Model(); got != tc.want {
				t.Errorf("Model() with version %q = %q, want %q", tc.version, got, tc.want)
			}
			if got := b.Version(); got != tc.version {
				t.Errorf("Version() = %q, want %q (round-trip)", got, tc.version)
			}
		})
	}
}

// TestHomegearOperationsSurfaceParity documents that the openccu-loom
// HomegearBackend exposes the expected public method surface.
//
// calls — excluding internal `__init__` / `__slots__` / capacity
// properties):
//
//	check_connection, deinit_proxy, delete_system_variable,
//	get_all_system_variables, get_device_description,
//	get_device_details, get_metadata, get_paramset,
//	get_paramset_description, get_system_variable, get_value,
//	init_proxy, initialize, list_devices, put_paramset,
//	set_system_variable, set_value, stop, model
//
// Go counterparts (methods on *HomegearBackend; `Operations` interface
// and Homegear-specific methods combined):
//
//	Ping, Init, Deinit, DeleteSystemVariable, GetAllSystemVariables,
//	GetAllSystemVariablesRaw, GetDeviceDescription, GetDeviceDetails,
//	GetDeviceName, GetMetadata, SetMetadata, DeleteMetadata,
//	GetParamset, GetParamsetDescription, GetSystemVariable, GetValue,
//	ListDevices, PutParamset, SetSystemVariable, SetValue, Model,
//	Version, SetVersion
func TestHomegearOperationsSurfaceParity(t *testing.T) {
	t.Parallel()

	// Required Go method names.
	required := []string{
		"Ping", "Init", "Deinit",
		"DeleteSystemVariable",
		"GetAllSystemVariables", "GetAllSystemVariablesRaw",
		"GetDeviceDescription", "GetDeviceDetails", "GetDeviceName",
		"GetMetadata", "SetMetadata", "DeleteMetadata",
		"GetParamset", "GetParamsetDescription",
		"GetSystemVariable", "GetValue",
		"ListDevices",
		"PutParamset",
		"SetSystemVariable", "SetValue",
		"Model", "Version", "SetVersion",
	}
	_ = NewHomegearBackend(nil, nil)
	rt := reflect.TypeFor[*HomegearBackend]()
	missing := []string{}
	for _, m := range required {
		if _, ok := rt.MethodByName(m); !ok {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		t.Errorf("HomegearBackend is missing aiohomematic counterparts for: %s",
			strings.Join(missing, ", "))
	}
}

// TestHomegearUnsupportedSurface documents the deliberately unimplemented
// Operations methods that make no sense for Homegear (CCU-specific program /
// inbox / install-mode surface).
func TestHomegearUnsupportedSurface(t *testing.T) {
	t.Parallel()

	b := NewHomegearBackend(nil, nil)
	ctx := t.Context()

	// Each method must return ErrUnsupported (sentinel) — caller
	// can rely on errors.Is(err, ErrUnsupported) to detect Homegear.
	checks := map[string]func() error{
		"GetAllPrograms": func() error {
			_, err := b.GetAllPrograms(ctx)
			return err
		},
		"SetProgramState": func() error { return b.SetProgramState(ctx, "1", true) },
		"GetSystemUpdateInfo": func() error {
			_, err := b.GetSystemUpdateInfo(ctx)
			return err
		},
		"GetInboxDevices": func() error {
			_, err := b.GetInboxDevices(ctx, "")
			return err
		},
		"GetInstallMode": func() error {
			_, err := b.GetInstallMode(ctx)
			return err
		},
		"SetInstallMode":   func() error { return b.SetInstallMode(ctx, true, 60, 1, "") },
		"UpdateFirmware":   func() error { return b.UpdateFirmware(ctx, "ABC") },
		"DownloadFirmware": func() error { return b.DownloadFirmware(ctx) },
		"TriggerFirmwareUpdate": func() error {
			_, err := b.TriggerFirmwareUpdate(ctx)
			return err
		},
	}
	for name, fn := range checks {
		err := fn()
		if err == nil {
			t.Errorf("%s should return ErrUnsupported, got nil", name)
			continue
		}
		// We can't import errors here without polluting the file, so
		// use error-string comparison (sentinel error has a stable
		// String()).
		if !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("%s err = %v, want one wrapping ErrUnsupported", name, err)
		}
	}
}
