"""Plugin implementations for flag submission."""

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass

import requests


@dataclass
class SubmissionResult:
    status: str
    status_code: int
    message: str = ""
    retry_after: float | None = None  # seconds


class FlagPlugin(ABC):
    """Abstract flag submitter plugin."""

    @abstractmethod
    def submit(self, flag_value: str) -> SubmissionResult:
        """Submit a flag and return a structured result."""


class HttpFlagPlugin(FlagPlugin):
    """HTTP implementation compatible with the Go submitter semantics."""

    def __init__(self, endpoint: str, timeout: float) -> None:
        self.endpoint = endpoint
        self.timeout = timeout
        self.session = requests.Session()

    def submit(self, flag_value: str) -> SubmissionResult:  # type: ignore[override]
        try:
            response = self.session.post(
                self.endpoint,
                json={"flag": flag_value},
                timeout=self.timeout,
            )
        except requests.RequestException as exc:  # type: ignore[attr-defined]
            return SubmissionResult(status="unknown", status_code=0, message=str(exc))

        text = response.text.strip()
        code = response.status_code
        if 200 <= code < 300:
            return SubmissionResult(status="accepted", status_code=code, message=text or "success")
        if code in (409, 429):
            return SubmissionResult(status="duplicated", status_code=code, message=text or response.reason)
        if 400 <= code < 500:
            return SubmissionResult(status="failed", status_code=code, message=text or response.reason)
        return SubmissionResult(status="unknown", status_code=code, message=text or response.reason, retry_after=30.0)
