"""Minimal Python client for the verdict `--json` protocol.

One spawn returns exactly one envelope on stdout. Human progress and usage
text stay on stderr; the exit code says which shape arrived.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
from typing import Any

from models import VerdictJsonEnvelope

SCHEMA_VERSION = "1"
DEFAULT_TIMEOUT = 300.0
MAX_STDOUT = 8 * 1024 * 1024
KEYS = ("schema_version", "verb", "repo", "pr", "at", "next", "warnings",
        "data", "error")


class VerdictError(Exception):
    """Exit 2/3, a signal, a timeout, or broken stdout."""

    def __init__(self, message: str, exit_code: int | None = None) -> None:
        super().__init__(message)
        self.exit_code = exit_code


class VerdictRefusal(VerdictError):
    """Exit 1: `data` is null and `error` carries code and message."""

    def __init__(self, code: str, message: str, warnings: list[str]) -> None:
        marker = "\ntry: "
        at = message.rfind(marker)
        self.code = code
        self.message = message[:at] if at >= 0 else message
        self.hint = message[at + 1 :] if at >= 0 else ""
        self.warnings = warnings
        super().__init__(f"{code}: {self.message}", 1)


class VerdictGateError(VerdictError):
    """Exit 1 with a success-shaped envelope: a `report --check` gate failed."""

    def __init__(self, envelope: VerdictJsonEnvelope) -> None:
        self.envelope = envelope
        super().__init__("gate failed", 1)


def run(verb: str, args: list[str], *, cwd: str | None = None,
        timeout: float = DEFAULT_TIMEOUT, binary: str | None = None,
        ) -> VerdictJsonEnvelope:
    """Spawn `verdict <verb> ... --json` and return the typed envelope."""
    exe = binary or os.environ.get("VERDICT_BIN") or shutil.which("verdict")
    if not exe:
        raise VerdictError("no verdict binary: set VERDICT_BIN or install it")
    try:
        proc = subprocess.run(
            [exe, verb, *args, "--json"], cwd=cwd,
            stdin=subprocess.DEVNULL, capture_output=True, text=True,
            timeout=timeout, check=False, shell=False,
        )
    except subprocess.TimeoutExpired as exc:
        raise VerdictError(f"verdict {verb} timed out after {timeout}s") from exc
    if len(proc.stdout.encode()) > MAX_STDOUT:
        raise VerdictError(f"stdout exceeded {MAX_STDOUT} bytes")
    if proc.returncode == 2:
        raise VerdictError(proc.stderr.strip(), 2)
    if proc.returncode not in (0, 1):
        raise VerdictError(f"verdict {verb}: exit {proc.returncode}\n{proc.stderr.strip()}")
    try:
        doc = json.loads(proc.stdout.strip())
    except json.JSONDecodeError as exc:
        raise VerdictError(f"stdout is not one envelope: {exc}") from exc
    if not isinstance(doc, dict) or any(key not in doc for key in KEYS):
        raise VerdictError("stdout is not the documented envelope")
    if doc["schema_version"] != SCHEMA_VERSION:
        raise VerdictError(f"unsupported schema_version {doc['schema_version']!r}")
    if proc.returncode == 1:
        error = doc["error"]
        if error is None and doc["data"] is not None:
            raise VerdictGateError(
                VerdictJsonEnvelope(**{key: doc[key] for key in KEYS})
            )
        if doc["data"] is not None or not isinstance(error, dict):
            raise VerdictError("refusal envelope has no typed error")
        raise VerdictRefusal(error["code"], error["message"], doc["warnings"])
    if doc["error"] is not None:
        raise VerdictError("exit 0 envelope carries an error")
    return VerdictJsonEnvelope(**{key: doc[key] for key in KEYS})
