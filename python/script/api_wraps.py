from __future__ import annotations

import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Mapping, MutableMapping, Sequence, TypedDict
from typing import NotRequired

import requests

__all__ = [
    "ControlPanelAPI",
    "APIError",
    "APIConfig",
    "Challenge",
    "Target",
    "Exploit",
    "ExploitFileInfo",
    "RunSummary",
    "RunDetail",
    "SubmissionAttemptData",
    "FlagListItem",
    "FlagDetail",
    "LogRecord",
    "RunnerInfo",
    "SchedulerSettingsPayload",
    "SchedulerTargetStatus",
    "SchedulerTargetSummary",
    "SchedulerTargetDetail",
    "RunsResponse",
    "FlagsResponse",
    "LogsResponse",
    "SchedulerTargetsResponse",
    "OverviewResponse",
    "OverviewMatrixResponse",
    "ImportTargetsResult",
    "RunActionResponse",
    "FlagStatusResponse",
    "ExploitToggleResponse",
    "ExploitTimeoutResponse",
    "ExploitRunScriptResponse",
    "ExploitRevisionResponse",
    "RunCancelResult",
    "BulkRunCancelResponse",
    "FlagCancelResult",
    "BulkFlagCancelResponse",
]


class Challenge(TypedDict):
    id: str
    name: str
    category: NotRequired[str | None]
    serviceType: NotRequired[str | None]
    description: NotRequired[str | None]
    metadata: NotRequired[dict[str, Any] | None]
    createdAt: NotRequired[str]
    updatedAt: NotRequired[str]


class Target(TypedDict):
    id: str
    teamName: str
    ip: str
    port: int
    challengeId: NotRequired[str]
    status: NotRequired[str]
    concurrencyLimit: NotRequired[int]
    labels: NotRequired[dict[str, Any] | None]


class Exploit(TypedDict):
    id: str
    name: str
    enabled: bool
    challengeId: NotRequired[str]
    description: NotRequired[str]
    runnerTags: NotRequired[list[str]]
    scheduleWeight: NotRequired[int]
    timeoutSeconds: NotRequired[int]
    metadata: NotRequired[dict[str, Any] | None]
    entrypoint: NotRequired[str]
    runScript: NotRequired[str]
    updatedAt: NotRequired[str]


class ExploitFileInfo(TypedDict):
    name: str
    size: int
    md5: str
    modTime: NotRequired[str]


class RunSummary(TypedDict):
    id: str
    exploitId: str
    targetId: str
    status: str
    exploitName: NotRequired[str | None]
    targetLabel: NotRequired[str | None]
    runnerId: NotRequired[str | None]
    queuedAt: NotRequired[str | None]
    startedAt: NotRequired[str | None]
    finishedAt: NotRequired[str | None]
    errorMessage: NotRequired[str | None]


class RunDetail(RunSummary):
    flagId: NotRequired[str | None]
    stdout: NotRequired[str | None]
    stderr: NotRequired[str | None]
    flagValue: NotRequired[str | None]
    flagSource: NotRequired[str | None]


class SubmissionAttemptData(TypedDict):
    id: str
    flagId: str
    submitterId: str
    statusCode: int
    error: str
    submittedAt: NotRequired[str | None]
    nextRetryAt: NotRequired[str | None]


class FlagListItem(TypedDict):
    id: str
    value: str
    challengeId: str
    targetId: str
    status: str
    submissionCount: int
    challengeName: NotRequired[str | None]
    targetLabel: NotRequired[str | None]
    runId: NotRequired[str | None]
    createdAt: NotRequired[str | None]
    lastSubmittedAt: NotRequired[str | None]


class FlagDetail(FlagListItem):
    attempts: NotRequired[list[SubmissionAttemptData]]


class LogRecord(TypedDict):
    id: str
    timestamp: str
    level: str
    source: str
    message: str
    runId: NotRequired[str | None]
    flagId: NotRequired[str | None]
    fields: NotRequired[dict[str, Any] | None]


class RunnerInfo(TypedDict):
    id: str
    type: str
    hostname: str
    tags: list[str]
    activeJobs: int
    labels: NotRequired[dict[str, Any] | None]
    maxParallel: int
    lastHeartbeat: str
    status: str
    registeredAt: str


class SchedulerSettingsPayload(TypedDict):
    roundIntervalSeconds: int
    roundIntervalMinutes: int
    maxDispatchBatch: int
    continuousDelaySeconds: int
    strategy: str
    updatedAt: str
    enabled: bool


class SchedulerTargetStatus(TypedDict):
    exploitId: str
    exploitName: str
    enabled: bool
    defaultEnabled: bool


class SchedulerTargetSummary(TypedDict):
    total: int
    enabled: int
    disabled: int
    indicators: str


class SchedulerTargetDetail(TypedDict):
    target: Target
    exploits: list[SchedulerTargetStatus]
    summary: SchedulerTargetSummary
    challenge: NotRequired[Challenge]


class RunsResponse(TypedDict):
    items: list[RunSummary]
    nextCursor: str | None


class FlagsResponse(TypedDict):
    items: list[FlagListItem]
    nextCursor: str | None


class LogsResponse(TypedDict):
    items: list[LogRecord]


class SchedulerTargetsResponse(TypedDict):
    items: list[SchedulerTargetDetail]


class OverviewStats(TypedDict):
    exploitCount: int
    targetCount: int
    pendingFlags: int
    runningRuns: int
    flagStatuses: dict[str, int]


class OverviewResponse(TypedDict):
    overview: OverviewStats


class OverviewMatrixResponse(TypedDict):
    matrix: dict[str, Any]


class ImportTargetsResult(TypedDict):
    imported: int


class RunActionResponse(TypedDict):
    run_id: str
    status: str


class FlagStatusResponse(TypedDict):
    flag_id: str
    status: str


class ExploitToggleResponse(TypedDict):
    exploit_id: str
    enabled: bool


class ExploitTimeoutResponse(TypedDict):
    exploit_id: str
    timeoutSeconds: int


class ExploitRunScriptResponse(TypedDict):
    exploit_id: str
    runScript: str


class ExploitRevisionResponse(TypedDict):
    file: ExploitFileInfo


class RunCancelResult(TypedDict, total=False):
    run: RunSummary
    runId: str
    error: str


class BulkRunCancelResponse(TypedDict):
    results: list[RunCancelResult]


class FlagCancelResult(TypedDict, total=False):
    flagId: str
    status: str
    error: str


class BulkFlagCancelResponse(TypedDict):
    results: list[FlagCancelResult]


def _env_float(key: str, default: float) -> float:
    value = os.getenv(key)
    if value is None:
        return default
    try:
        return float(value)
    except ValueError:
        return default


@dataclass(frozen=True)
class APIConfig:
    base_url: str | None = None
    api_prefix: str | None = None
    timeout: float | None = None
    runner_secret: str | None = None
    admin_token: str | None = None
    headers: Mapping[str, str] | None = None

    @classmethod
    def from_env(cls) -> "APIConfig":
        return cls(
            base_url=os.getenv("CONTROL_PANEL_URL"),
            api_prefix=os.getenv("CONTROL_PANEL_API_PREFIX"),
            timeout=_env_float("CONTROL_PANEL_TIMEOUT", 15.0),
            runner_secret=os.getenv("RUNNER_SECRET"),
            admin_token=os.getenv("CONTROL_PANEL_TOKEN"),
        )


class APIError(RuntimeError):
    """Raised when the Control Panel responds with an error."""

    def __init__(self, status_code: int, message: str, payload: Any | None = None) -> None:
        super().__init__(f"API request failed ({status_code}): {message}")
        self.status_code = status_code
        self.message = message
        self.payload = payload


class ControlPanelAPI:
    """Lightweight wrapper around the Control Panel /api/v1 REST endpoints."""

    def __init__(
        self,
        config: APIConfig | None = None,
        *,
        base_url: str | None = None,
        api_prefix: str | None = None,
        timeout: float | None = None,
        runner_secret: str | None = None,
        default_headers: Mapping[str, str] | None = None,
        session: requests.Session | None = None,
    ) -> None:
        cfg = config or APIConfig.from_env()
        self.base_url = (base_url or cfg.base_url or "http://localhost:8080").rstrip("/")
        prefix = api_prefix or cfg.api_prefix or "/api/v1"
        self.api_prefix = "/" + prefix.strip("/")
        self._root = f"{self.base_url}{self.api_prefix}"
        self.timeout = timeout or cfg.timeout or _env_float("CONTROL_PANEL_TIMEOUT", 15.0)
        self.runner_secret = runner_secret or cfg.runner_secret
        self.session = session or requests.Session()
        headers: dict[str, str] = {"Accept": "application/json"}
        if cfg.admin_token:
            headers["Authorization"] = f"Bearer {cfg.admin_token}"
        if cfg.headers:
            headers.update(cfg.headers)
        if default_headers:
            headers.update(default_headers)
        self.default_headers = headers

    # --- internal helpers -------------------------------------------------

    def _make_url(self, path: str) -> str:
        if path.startswith("http://") or path.startswith("https://"):
            return path
        path = path.lstrip("/")
        return f"{self._root}/{path}" if path else self._root

    def _request(
        self,
        method: str,
        path: str,
        *,
        params: Mapping[str, Any] | None = None,
        json_body: Any | None = None,
        data: Mapping[str, Any] | None = None,
        files: Mapping[str, Any] | None = None,
        headers: Mapping[str, str] | None = None,
        expected_status: Sequence[int] | None = None,
        stream: bool = False,
        timeout: float | None = None,
        expect_json: bool = True,
    ) -> Any:
        url = self._make_url(path)
        merged_headers: MutableMapping[str, str] = dict(self.default_headers)
        if headers:
            merged_headers.update(headers)
        if files:
            merged_headers.pop("Content-Type", None)
        response = self.session.request(
            method.upper(),
            url,
            params=params,
            json=json_body,
            data=data,
            files=files,
            headers=merged_headers,
            timeout=timeout or self.timeout,
            stream=stream,
        )
        expected = tuple(expected_status) if expected_status else (200, 201, 202, 204)
        if response.status_code not in expected:
            payload = self._safe_json(response)
            message = payload.get("error") if isinstance(payload, dict) else response.text
            raise APIError(response.status_code, message or "unexpected response", payload)
        if stream:
            return response
        if not expect_json:
            return response.content
        if response.status_code == 204 or response.content == b"":
            return None
        payload = self._safe_json(response)
        if payload is None:
            raise APIError(response.status_code, "response is not JSON", response.text)
        return payload

    @staticmethod
    def _safe_json(response: requests.Response) -> Any | None:
        try:
            return response.json()
        except json.JSONDecodeError:
            return None

    def _runner_secret(self, secret: str | None) -> str:
        final = secret or self.runner_secret
        if not final:
            raise ValueError("runner secret is required; set RUNNER_SECRET or pass secret=...")
        return final

    # --- low-level verbs --------------------------------------------------

    def get(self, path: str, **kwargs: Any) -> Any:
        return self._request("GET", path, **kwargs)

    def post(self, path: str, **kwargs: Any) -> Any:
        return self._request("POST", path, **kwargs)

    def patch(self, path: str, **kwargs: Any) -> Any:
        return self._request("PATCH", path, **kwargs)

    def put(self, path: str, **kwargs: Any) -> Any:
        return self._request("PUT", path, **kwargs)

    def delete(self, path: str, **kwargs: Any) -> Any:
        return self._request("DELETE", path, **kwargs)

    # --- challenge endpoints ---------------------------------------------

    def list_challenges(self) -> list[Challenge]:
        payload = self.get("challenges") or {}
        return list(payload.get("items", []))

    def get_challenge(self, challenge_id: str) -> Challenge:
        payload = self.get(f"challenges/{challenge_id}") or {}
        return payload.get("challenge", {})

    def create_challenge(
        self,
        *,
        name: str,
        category: str | None = None,
        service_type: str | None = None,
        description: str | None = None,
        metadata: Mapping[str, Any] | None = None,
    ) -> Challenge:
        body = {
            "name": name,
            "category": category,
            "serviceType": service_type,
            "description": description,
            "metadata": dict(metadata) if metadata else None,
        }
        payload = self.post("challenges", json_body=body)
        return payload.get("challenge", {})

    def update_challenge(self, challenge_id: str, **fields: Any) -> Challenge:
        payload = self.patch(f"challenges/{challenge_id}", json_body=fields)
        return payload.get("challenge", {})

    def delete_challenge(self, challenge_id: str) -> None:
        self.delete(f"challenges/{challenge_id}")

    # --- target endpoints -------------------------------------------------

    def list_targets(self) -> list[Target]:
        payload = self.get("targets") or {}
        return list(payload.get("items", []))

    def create_target(
        self,
        *,
        challenge_id: str,
        team_name: str,
        ip: str,
        port: int,
        concurrency_limit: int = 1,
        status: str = "active",
    ) -> Target:
        body = {
            "challengeId": challenge_id,
            "teamName": team_name,
            "ip": ip,
            "port": port,
            "concurrencyLimit": concurrency_limit,
            "status": status,
        }
        payload = self.post("targets", json_body=body)
        return payload.get("target", {})

    def import_targets(
        self,
        *,
        challenge_id: str,
        base_ip: str,
        port: int,
        count: int,
        team_names: Sequence[str] | None = None,
    ) -> ImportTargetsResult:
        body = {
            "challengeId": challenge_id,
            "baseIp": base_ip,
            "port": port,
            "count": count,
            "teamNames": list(team_names) if team_names else None,
        }
        return self.post("targets/import", json_body=body)

    def update_target(
        self,
        target_id: str,
        *,
        concurrency_limit: int | None = None,
        status: str | None = None,
    ) -> Target:
        body: dict[str, Any] = {}
        if concurrency_limit is not None:
            body["concurrencyLimit"] = concurrency_limit
        if status is not None:
            body["status"] = status
        payload = self.patch(f"targets/{target_id}", json_body=body)
        return payload.get("target", {})

    def delete_target(self, target_id: str) -> None:
        self.delete(f"targets/{target_id}")

    # --- exploit endpoints ------------------------------------------------

    def list_exploits(self) -> list[Exploit]:
        payload = self.get("exploits") or {}
        return list(payload.get("items", []))

    def create_exploit(
        self,
        *,
        name: str,
        challenge_id: str | None = None,
        description: str | None = None,
        runner_tags: Sequence[str] | None = None,
        schedule_weight: int | None = None,
        timeout_seconds: int | None = None,
        entrypoint: str | None = None,
        run_script: str | None = None,
        artifact_path: str | os.PathLike[str] | None = None,
        artifact_name: str | None = None,
        artifact_base64: str | None = None,
    ) -> Exploit:
        base_body = {
            "challengeId": challenge_id,
            "name": name,
            "description": description,
            "runnerTags": list(runner_tags) if runner_tags else None,
            "scheduleWeight": schedule_weight,
            "timeoutSeconds": timeout_seconds,
            "entrypoint": entrypoint,
            "runScript": run_script,
            "artifactBase64": artifact_base64,
            "artifactName": artifact_name,
        }
        if artifact_path:
            form = {k: v for k, v in base_body.items() if v not in (None, [], "")}
            if "runnerTags" in form:
                form["runnerTags"] = ",".join(form["runnerTags"])
            if schedule_weight is not None:
                form["scheduleWeight"] = str(schedule_weight)
            if timeout_seconds is not None:
                form["timeoutSeconds"] = str(timeout_seconds)
            file_path = Path(artifact_path)
            filename = artifact_name or file_path.name
            with file_path.open("rb") as handle:
                files = {"artifact": (filename, handle)}
                payload = self.post("exploits", data=form, files=files)
        else:
            payload = self.post("exploits", json_body=base_body)
        return payload.get("exploit", {})

    def delete_exploit(self, exploit_id: str) -> None:
        self.delete(f"exploits/{exploit_id}")

    def set_exploit_enabled(self, exploit_id: str, enabled: bool) -> ExploitToggleResponse:
        action = "enable" if enabled else "disable"
        return self.post(f"exploits/{exploit_id}/{action}")

    def set_exploit_timeout(self, exploit_id: str, timeout_seconds: int) -> ExploitTimeoutResponse:
        body = {"timeoutSeconds": timeout_seconds}
        return self.post(f"exploits/{exploit_id}/timeout", json_body=body)

    def get_exploit_run_script(self, exploit_id: str) -> str:
        payload = self.get(f"exploits/{exploit_id}/run-script") or {}
        return payload.get("runScript", "")

    def update_exploit_run_script(self, exploit_id: str, run_script: str) -> ExploitRunScriptResponse:
        body = {"runScript": run_script}
        return self.post(f"exploits/{exploit_id}/run-script", json_body=body)

    def upload_exploit_revision(
        self,
        exploit_id: str,
        file_path: str | os.PathLike[str],
        *,
        filename: str | None = None,
    ) -> ExploitRevisionResponse:
        path = Path(file_path)
        display = filename or path.name
        with path.open("rb") as handle:
            files = {"file": (display, handle)}
            return self.post(f"exploits/{exploit_id}/revisions", files=files)

    def download_exploit_artifact(self, exploit_id: str) -> bytes:
        return self.get(f"exploits/{exploit_id}/artifact", expect_json=False)

    def list_exploit_files(self, exploit_id: str) -> list[ExploitFileInfo]:
        payload = self.get(f"exploits/{exploit_id}/files") or {}
        return list(payload.get("files", []))

    # --- run endpoints ----------------------------------------------------

    def list_runs(
        self,
        *,
        limit: int | None = None,
        cursor: str | None = None,
        status: str | None = None,
        exploit_id: str | None = None,
        target_id: str | None = None,
        exploit_name: str | None = None,
        target_name: str | None = None,
    ) -> RunsResponse:
        params = {
            "limit": limit,
            "cursor": cursor,
            "status": status,
            "exploitId": exploit_id,
            "targetId": target_id,
            "exploitName": exploit_name,
            "targetName": target_name,
        }
        params = {k: v for k, v in params.items() if v not in (None, "")}
        return self.get("runs", params=params)

    def get_run(self, run_id: str) -> RunDetail:
        payload = self.get(f"runs/{run_id}") or {}
        return payload.get("run", {})

    def start_run(self, run_id: str, *, runner_id: str | None = None, secret: str | None = None) -> RunActionResponse:
        body = {"secret": self._runner_secret(secret), "runnerId": runner_id}
        return self.post(f"runs/{run_id}/start", json_body=body)

    def finish_run(
        self,
        run_id: str,
        *,
        status: str,
        runner_id: str | None = None,
        error: str | None = None,
        flag_value: str | None = None,
        flag_source: str | None = None,
        stdout: str | None = None,
        stderr: str | None = None,
        secret: str | None = None,
    ) -> RunActionResponse:
        body = {
            "secret": self._runner_secret(secret),
            "runnerId": runner_id,
            "status": status,
            "error": error,
            "flagValue": flag_value,
            "flagSource": flag_source,
            "stdout": stdout,
            "stderr": stderr,
        }
        return self.post(f"runs/{run_id}/finish", json_body=body)

    def cancel_run(self, run_id: str, reason: str | None = None) -> RunDetail:
        body = {"reason": reason} if reason else None
        payload = self.post(f"runs/{run_id}/cancel", json_body=body)
        return payload.get("run", {})

    def bulk_cancel_runs(self, run_ids: Sequence[str], reason: str | None = None) -> BulkRunCancelResponse:
        body = {"runIds": list(run_ids), "reason": reason}
        return self.post("runs/cancel", json_body=body)

    # --- flag endpoints ---------------------------------------------------

    def list_flags(self, *, limit: int | None = None, cursor: str | None = None, status: str | None = None) -> FlagsResponse:
        params = {"limit": limit, "cursor": cursor, "status": status}
        params = {k: v for k, v in params.items() if v not in (None, "")}
        return self.get("flags", params=params)

    def get_flag(self, flag_id: str) -> FlagDetail:
        payload = self.get(f"flags/{flag_id}") or {}
        return payload.get("flag", {})

    def retry_flag(self, flag_id: str) -> FlagStatusResponse:
        return self.post(f"flags/{flag_id}/requeue")

    def flag_logs(self, flag_id: str) -> list[str]:
        payload = self.get(f"flags/{flag_id}/logs") or {}
        return list(payload.get("logs", []))

    def cancel_flag(self, flag_id: str, reason: str | None = None) -> FlagStatusResponse:
        body = {"reason": reason} if reason else None
        return self.post(f"flags/{flag_id}/cancel", json_body=body)

    def bulk_cancel_flags(self, flag_ids: Sequence[str], reason: str | None = None) -> BulkFlagCancelResponse:
        body = {"flagIds": list(flag_ids), "reason": reason}
        return self.post("flags/cancel", json_body=body)

    # --- log endpoints ----------------------------------------------------

    def list_logs(
        self,
        *,
        level: str | None = None,
        source: str | None = None,
        run_id: str | None = None,
        flag_id: str | None = None,
        limit: int | None = None,
    ) -> list[LogRecord]:
        params = {
            "level": level,
            "source": source,
            "runId": run_id,
            "flagId": flag_id,
            "limit": limit,
        }
        params = {k: v for k, v in params.items() if v not in (None, "")}
        payload = self.get("logs", params=params) or {}
        return list(payload.get("items", []))

    # --- runner endpoints -------------------------------------------------

    def list_runners(self) -> list[RunnerInfo]:
        payload = self.get("runners") or {}
        return list(payload.get("items", []))

    def delete_runner(self, runner_id: str) -> None:
        self.delete("runners", params={"id": runner_id})

    def register_runner(
        self,
        *,
        runner_id: str,
        runner_type: str | None = None,
        hostname: str | None = None,
        tags: Sequence[str] | None = None,
        labels: Mapping[str, str] | None = None,
        max_parallel: int | None = None,
        status: str | None = None,
        secret: str | None = None,
    ) -> RunnerInfo:
        body = {
            "id": runner_id,
            "type": runner_type,
            "hostname": hostname,
            "secret": self._runner_secret(secret),
            "tags": list(tags) if tags else None,
            "labels": dict(labels) if labels else None,
            "maxParallel": max_parallel,
            "status": status,
        }
        payload = self.post("runners/register", json_body=body, expected_status=(201,))
        return payload.get("runner", {})

    def runner_heartbeat(
        self,
        *,
        runner_id: str,
        status: str | None = None,
        runner_type: str | None = None,
        hostname: str | None = None,
        tags: Sequence[str] | None = None,
        labels: Mapping[str, str] | None = None,
        max_parallel: int | None = None,
        active_jobs: int | None = None,
        secret: str | None = None,
    ) -> RunnerInfo:
        body = {
            "id": runner_id,
            "secret": self._runner_secret(secret),
            "status": status,
            "type": runner_type,
            "hostname": hostname,
            "tags": list(tags) if tags else None,
            "labels": dict(labels) if labels else None,
            "maxParallel": max_parallel,
            "activeJobs": active_jobs,
        }
        payload = self.post("runners/heartbeat", json_body=body)
        return payload.get("runner", {})

    # --- scheduler endpoints ---------------------------------------------

    def get_scheduler_settings(self) -> SchedulerSettingsPayload:
        payload = self.get("scheduler/settings") or {}
        return payload.get("settings", {})

    def update_scheduler_settings(
        self,
        *,
        round_interval_seconds: int | None = None,
        round_interval_minutes: int | None = None,
        max_dispatch_batch: int | None = None,
        enabled: bool | None = None,
        strategy: str | None = None,
        continuous_delay_seconds: int | None = None,
    ) -> SchedulerSettingsPayload:
        body = {
            "roundIntervalSeconds": round_interval_seconds,
            "roundIntervalMinutes": round_interval_minutes,
            "maxDispatchBatch": max_dispatch_batch,
            "enabled": enabled,
            "strategy": strategy,
            "continuousDelaySeconds": continuous_delay_seconds,
        }
        payload = self.post("scheduler/settings", json_body=body)
        return payload.get("settings", {})

    def list_scheduler_targets(self) -> list[SchedulerTargetDetail]:
        payload = self.get("scheduler/targets") or {}
        return list(payload.get("items", []))

    def get_scheduler_target(self, target_id: str) -> SchedulerTargetDetail:
        payload = self.get(f"scheduler/targets/{target_id}") or {}
        return payload.get("target", {})

    def update_scheduler_target_exploits(
        self,
        target_id: str,
        statuses: Sequence[tuple[str, bool]] | Sequence[Mapping[str, Any]],
    ) -> SchedulerTargetDetail:
        normalized: list[dict[str, Any]] = []
        for entry in statuses:
            if isinstance(entry, Mapping):
                exploit_id = entry.get("exploitId")
                enabled = bool(entry.get("enabled"))
            else:
                exploit_id, enabled = entry
            if not exploit_id:
                raise ValueError("exploitId is required in scheduler status payloads")
            normalized.append({"exploitId": exploit_id, "enabled": bool(enabled)})
        body = {"statuses": normalized}
        payload = self.post(f"scheduler/targets/{target_id}/exploits", json_body=body)
        return payload.get("target", {})

    # --- overview endpoints ----------------------------------------------

    def overview(self) -> OverviewResponse:
        return self.get("overview") or {}

    def overview_matrix(self) -> OverviewMatrixResponse:
        return self.get("overview/matrix") or {}

    # --- context manager support -----------------------------------------

    def close(self) -> None:
        self.session.close()

    def __enter__(self) -> "ControlPanelAPI":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:  # type: ignore[override]
        self.close()
