// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build !windows

package addonupdate

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the installer in its own session so it survives the
// daemon exiting mid-install: the add-on installer's last act is to restart
// this very daemon, so a child tied to our process group would be torn down
// by the shutdown it just triggered. Same effect as a shell's `setsid cmd &`.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
