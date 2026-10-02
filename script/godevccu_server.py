# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
#
# godevccu_server.py — runs the godevccu simulator for the Python reference
# snapshot scripts (aiohomematic_snapshot.py, homematicip_local_snapshot.py,
# aiohomematic2mqtt_discovery_snapshot.py).
#
# godevccu is the simulator both stacks run against: the Go snapshot tests use
# the module pinned in go.mod, and these scripts build the binary from that same
# module, so the two sides always see one device catalogue. GODEVCCU_BIN points
# at a prebuilt binary instead.

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

_REPO_ROOT = Path(__file__).resolve().parent.parent
_MODULE_CMD = "github.com/SukramJ/godevccu/cmd/godevccu"
_START_TIMEOUT_S = 30.0
_STOP_TIMEOUT_S = 5.0


def godevccu_binary(work_dir: Path) -> str:
    """Return GODEVCCU_BIN, or build the godevccu binary pinned in go.mod into work_dir."""
    if path := os.environ.get("GODEVCCU_BIN"):
        return path
    binary = work_dir / "godevccu"
    subprocess.run(  # noqa: S603
        ["go", "build", "-o", str(binary), _MODULE_CMD],  # noqa: S607
        cwd=_REPO_ROOT,
        check=True,
    )
    return str(binary)


def godevccu_version(binary: str) -> str:
    """Return the version the binary reports, e.g. "0.8.0"."""
    out = subprocess.run([binary, "-version"], capture_output=True, text=True, check=True)  # noqa: S603
    return out.stdout.strip().removeprefix("godevccu").strip() or "unknown"


class GodevccuServer:
    """A godevccu subprocess in homegear mode on a fixed XML-RPC port."""

    def __init__(self, *, host: str, port: int, devices: list[str] | None) -> None:
        self._host = host
        self._port = port
        self._devices = devices
        self._tmp = tempfile.TemporaryDirectory(prefix="godevccu-")
        self._process: subprocess.Popen[bytes] | None = None
        self.binary = ""
        self.version = "unknown"

    def start(self) -> None:
        """Start godevccu and wait until its XML-RPC server listens."""
        work_dir = Path(self._tmp.name)
        self.binary = godevccu_binary(work_dir)
        self.version = godevccu_version(self.binary)
        ports_file = work_dir / "ports.json"
        args = [
            self.binary,
            "-mode", "homegear",
            "-host", self._host,
            "-xml-rpc-port", str(self._port),
            "-json-rpc-port", "0",
            "-ports-json", str(ports_file),
        ]  # fmt: skip
        if self._devices:
            args += ["-devices", ",".join(self._devices)]
        with (work_dir / "godevccu.log").open("wb") as log:
            self._process = subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT)  # noqa: S603
        deadline = time.monotonic() + _START_TIMEOUT_S
        while time.monotonic() < deadline:
            if (code := self._process.poll()) is not None:
                raise RuntimeError(f"godevccu exited with {code}: {self._log_tail()}")
            if ports_file.exists() and json.loads(ports_file.read_text()).get("xmlrpc"):
                return
            time.sleep(0.05)
        self.stop()
        raise RuntimeError(f"godevccu did not listen within {_START_TIMEOUT_S}s: {self._log_tail()}")

    def stop(self) -> None:
        """Terminate godevccu (kill it if it does not exit in time) and remove its work directory."""
        if self._process is not None and self._process.poll() is None:
            self._process.terminate()
            try:
                self._process.wait(timeout=_STOP_TIMEOUT_S)
            except subprocess.TimeoutExpired:
                self._process.kill()
                self._process.wait()
        self._tmp.cleanup()

    def _log_tail(self, lines: int = 20) -> str:
        log = Path(self._tmp.name) / "godevccu.log"
        if not log.exists():
            return ""
        return "\n".join(log.read_text(errors="replace").splitlines()[-lines:])
