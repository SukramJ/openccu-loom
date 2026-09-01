// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build windows

package addonupdate

import "os/exec"

// detachProcess is a no-op on Windows, which has no session concept and no
// Setsid field on syscall.SysProcAttr — referencing it does not compile.
//
// Nothing is lost: self-update is gated on the CCU / OpenCCU add-on
// capability (ADR 0057), so this runner never executes on Windows. The
// platform only has to build, which is what the nightly cross-platform job
// checks. Should a Windows install path ever exist, the equivalent is
// CREATE_NEW_PROCESS_GROUP via syscall.SysProcAttr.CreationFlags — left
// unimplemented deliberately rather than shipped untested.
func detachProcess(_ *exec.Cmd) {}
