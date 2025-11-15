"""Default Python flag submitter plugin template.

This file doubles as both the built-in fallback plugin and a reference template
for custom implementations that can be uploaded through the Control Panel.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
import json
import sys

import requests
import time

@dataclass
class SubmissionResult:
    status: str
    status_code: int
    message: str = ""
    retry_after: float | None = None  # seconds
    error: str | None = None

    def to_dict(self) -> dict:
        return {
            "status": self.status,
            "status_code": self.status_code,
            "message": self.message,
            "retry_after": self.retry_after,
            "error": self.error,
        }


class FlagPlugin:
    """Minimal runtime contract understood by the Python submitter."""

    def submit(self, flag_value: str) -> SubmissionResult:  # pragma: no cover - interface
        raise NotImplementedError

    def id(self) -> str:
        return self.__class__.__name__.lower()


class DefaultHttpPlugin(FlagPlugin):
    """Simple HTTP implementation that posts flags to a fixed endpoint."""

    def __init__(self, timeout: float = 5.0) -> None:
        endpoint = "http://192.168.66.206:5000/upload_flag"
        self.endpoint = endpoint
        self.timeout = timeout if timeout > 0 else 5.0
        self.session = requests.Session()

    def submit(self, flag_value: str) -> SubmissionResult:  # type: ignore[override]
        try:
            response = self.session.post(
                self.endpoint + "?tmp=111",
                json={"flag": flag_value + "2333"},
                timeout=self.timeout,
            )
        except requests.RequestException as exc:  # type: ignore[attr-defined]
            return SubmissionResult(
                status="unknown",
                status_code=0,
                message=str(exc),
                error=str(exc),
                retry_after=30.0,
            )

        text = response.text.strip()
        code = response.status_code
        if 200 <= code < 300:
            return SubmissionResult(status="accepted", status_code=code, message=text or "success")
        if code in (409, 429):
            return SubmissionResult(status="duplicated", status_code=code, message=text or response.reason)
        if 400 <= code < 500:
            return SubmissionResult(status="failed", status_code=code, message=text or response.reason)
        return SubmissionResult(
            status="unknown",
            status_code=code,
            message=text or response.reason,
            retry_after=60.0,
        )

    def id(self) -> str:  # type: ignore[override]
        return "default-http"
    

if __name__ == "__main__":
    plugin = DefaultHttpPlugin()
    if len(sys.argv) < 2:
        print("Usage: python submit.py <flag_value>", file=sys.stderr)
        sys.exit(1)

    flag_value = sys.argv[1]
    result = plugin.submit(flag_value)
    print(json.dumps(result.to_dict()))
