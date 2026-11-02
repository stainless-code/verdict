"""Turn envelope `data` into the generated dataclasses (report shown here)."""

from __future__ import annotations

from dataclasses import fields, is_dataclass
from typing import Any, get_args, get_origin, get_type_hints

from models import ReportData, VerdictJsonEnvelope
from verdict_client import VerdictError, run


def decode(tp: Any, value: Any) -> Any:
    """Build generated dataclasses recursively; unknown data passes through."""
    if is_dataclass(tp) and value is not None:
        hints = get_type_hints(tp)
        return tp(**{f.name: decode(hints[f.name], value[f.name])
                     for f in fields(tp) if f.name in value})
    if get_origin(tp) is list:
        return [decode(get_args(tp)[0], item) for item in value]
    return value


def report(repo: str, pr: str, **kwargs: Any) -> tuple[VerdictJsonEnvelope, ReportData]:
    """Convenience wrapper: report's `data` has a pinned schema."""
    env = run("report", [repo, pr], **kwargs)
    if not isinstance(env.data, dict):
        raise VerdictError("report returned data null")
    return env, decode(ReportData, env.data)
