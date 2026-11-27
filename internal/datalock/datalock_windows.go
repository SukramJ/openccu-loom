// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build windows

package datalock

import (
	"errors"
	"fmt"
	"syscall"
)

// errorSharingViolation is ERROR_SHARING_VIOLATION: the file is open
// without sharing elsewhere.
const errorSharingViolation syscall.Errno = 32

// lockFile opens path with no sharing allowed: while the handle is open, no
// other open of the file succeeds, and the system closes it with the
// process.
func lockFile(path string) (func() error, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("datalock: %w", err)
	}
	h, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // no sharing: the lock
		nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, errorSharingViolation) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("datalock: open %s: %w", path, err)
	}
	return func() error { return syscall.CloseHandle(h) }, nil
}
