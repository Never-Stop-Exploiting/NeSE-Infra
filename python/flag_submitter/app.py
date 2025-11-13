import json
import logging
import os
import random
import socket
import threading
import time
from collections import deque
from datetime import datetime, timezone
from typing import Any

import redis
import requests
from redis.backoff import ExponentialBackoff
from redis.exceptions import ResponseError, ConnectionError as RedisConnectionError, TimeoutError as RedisTimeoutError
from redis.retry import Retry

from plugin import FlagPlugin, HttpFlagPlugin, SubmissionResult


def env(key: str, default: str | None = None) -> str | None:
    return os.getenv(key, default)


def parse_duration(value: str | None, fallback: float) -> float:
    if not value:
        return fallback
    value = value.strip().lower()
    try:
        if value.endswith("ms"):
            return float(value[:-2]) / 1000.0
        if value.endswith("s"):
            return float(value[:-1])
        if value.endswith("m"):
            return float(value[:-1]) * 60.0
        return float(value)
    except ValueError:
        return fallback


def parse_int(value: str | None, fallback: int) -> int:
    if value is None:
        return fallback
    try:
        return int(value)
    except ValueError:
        return fallback


class MetricsTracker:
    def __init__(self, window_seconds: float = 60.0) -> None:
        self.window = window_seconds
        self.events: deque[tuple[float, float]] = deque()
        self.lock = threading.Lock()

    def record(self, latency: float) -> None:
        if latency <= 0:
            latency = 1.0
        now = time.time()
        with self.lock:
            self.events.append((now, latency))
            self._prune_locked(now)

    def snapshot(self) -> tuple[float, float, float, int, float]:
        now = time.time()
        with self.lock:
            self._prune_locked(now)
            count = len(self.events)
            latency_sum = sum(lat for _, lat in self.events)
        window = self.window if self.window > 0 else 60.0
        throughput = count / window
        avg = latency_sum / count if count else 1.0
        return throughput, avg, latency_sum, count, window

    def _prune_locked(self, now: float) -> None:
        cutoff = now - self.window
        while self.events and self.events[0][0] < cutoff:
            self.events.popleft()


class FlagSubmitter:
    def __init__(self) -> None:
        redis_addr = env("REDIS_ADDR", "localhost:6379")
        host, _, port = redis_addr.partition(":")
        socket_timeout = parse_duration(env("REDIS_SOCKET_TIMEOUT", "5s"), 5.0)
        health_interval = max(1, int(parse_duration(env("REDIS_HEALTH_CHECK_INTERVAL", "30s"), 30.0)))
        self._redis_kwargs = {
            "host": host or "localhost",
            "port": int(port or 6379),
            "password": env("REDIS_PASSWORD"),
            "db": parse_int(env("REDIS_DB", "0"), 0),
            "decode_responses": False,
            "socket_keepalive": True,
            "socket_timeout": socket_timeout,
            "health_check_interval": health_interval,
            "retry_on_timeout": True,
        }
        self._redis_lock = threading.Lock()
        self.redis = self._create_redis_client()

        control_panel = env("SUBMITTER_CONTROL_PANEL_URL", env("CONTROL_PANEL_URL"))
        if not control_panel:
            raise RuntimeError("SUBMITTER_CONTROL_PANEL_URL or CONTROL_PANEL_URL required")
        self.control_panel = control_panel.rstrip("/")
        self.secret = env("SUBMITTER_SECRET", "dev-submitter-secret")
        if not self.secret:
            raise RuntimeError("SUBMITTER_SECRET is required")

        submitter_id = env("SUBMITTER_ID")
        if not submitter_id:
            hostname = socket.gethostname().split(".")[0]
            submitter_id = f"py-{hostname}-{random.randint(1000, 9999)}"
        self.submitter_id = submitter_id

        self.stream = env("FLAG_QUEUE_NAME", "flag:pending")
        self.group = env("FLAG_SUBMITTER_GROUP", "submitter")
        consumer_suffix = socket.gethostname().split(".")[0]
        self.consumer_base = env("FLAG_SUBMITTER_CONSUMER", f"py-{consumer_suffix}")

        self.endpoint = env("FLAG_SUBMIT_URL", "http://flagserver:5000/upload_flag")
        self.timeout = parse_duration(env("FLAG_SUBMIT_TIMEOUT", "5s"), 5.0)
        self.retry_backoff = parse_duration(env("FLAG_SUBMIT_RETRY_BACKOFF", "30s"), 30.0)
        self.submitter_type = env("FLAG_SUBMITTER_TYPE", "python-http")

        self.block_ms = int(parse_duration(env("FLAG_QUEUE_BLOCK", "5s"), 5.0) * 1000)
        self.plugin: FlagPlugin = HttpFlagPlugin(self.endpoint, self.timeout)
        self.concurrency = max(1, parse_int(env("FLAG_SUBMIT_CONCURRENCY", "1"), 1))

        self.session = requests.Session()
        self.heartbeat_interval = parse_duration(env("SUBMITTER_HEARTBEAT_INTERVAL", "10s"), 10.0)
        self.metrics = MetricsTracker()
        self.in_flight = 0
        self.lock = threading.Lock()

    # Lifecycle -------------------------------------------------------------

    def ensure_group(self) -> None:
        try:
            self.redis_call("xgroup_create", self.stream, self.group, id="0", mkstream=True)
        except ResponseError as exc:
            if "BUSYGROUP" not in str(exc):
                raise

    def register(self) -> None:
        payload = {
            "id": self.submitter_id,
            "type": self.submitter_type,
            "hostname": socket.gethostname(),
            "secret": self.secret,
            "concurrency": self.concurrency,
            "status": "online",
        }
        resp = self.session.post(
            f"{self.control_panel}/api/v1/submitters/register",
            json=payload,
            timeout=5,
        )
        if resp.status_code >= 300:
            raise RuntimeError(f"register failed: {resp.status_code} {resp.text}")

    def heartbeat_loop(self) -> None:
        while True:
            try:
                throughput, avg_latency, latency_sum, count, window = self.metrics.snapshot()
                demand = latency_sum
                capacity = max(self.concurrency, 1) * window
                pressure = demand / capacity if capacity > 0 else 0.0
                payload = {
                    "id": self.submitter_id,
                    "secret": self.secret,
                    "status": "online",
                    "type": self.submitter_type,
                    "concurrency": self.concurrency,
                    "inFlight": self.current_in_flight(),
                    "pending": 0,
                    "recentThroughput": throughput,
                    "avgLatencyMs": avg_latency * 1000,
                    "pressure": pressure,
                    "windowSeconds": window,
                    "latencySumSeconds": latency_sum,
                    "processedCount": count,
                }
                self.session.post(
                    f"{self.control_panel}/api/v1/submitters/heartbeat",
                    json=payload,
                    timeout=5,
                )
            except Exception as exc:  # pylint: disable=broad-except
                logging.warning("heartbeat failed: %s", exc)
            time.sleep(self.heartbeat_interval)

    # Workers ---------------------------------------------------------------

    def run(self) -> None:
        self.ensure_group()
        self.register()
        logging.info(
            "python flag submitter started id=%s workers=%d",
            self.submitter_id,
            self.concurrency,
        )
        threading.Thread(target=self.heartbeat_loop, name="heartbeat", daemon=True).start()

        threads: list[threading.Thread] = []
        for idx in range(self.concurrency):
            consumer = self.consumer_base if self.concurrency == 1 else f"{self.consumer_base}-{idx + 1}"
            thread = threading.Thread(
                target=self.worker_loop,
                args=(consumer,),
                name=f"flag-submit-worker-{idx + 1}",
                daemon=True,
            )
            thread.start()
            threads.append(thread)
        for thread in threads:
            thread.join()

    def worker_loop(self, consumer: str) -> None:
        while True:
            try:
                self.consume_once(consumer)
            except Exception as exc:  # pylint: disable=broad-except
                logging.exception("submitter worker error: %s", exc)
                time.sleep(1)

    def consume_once(self, consumer: str) -> None:
        try:
            messages = self.redis_call(
                "xreadgroup",
                self.group,
                consumer,
                {self.stream: ">"},
                count=1,
                block=self.block_ms,
            )
        except ResponseError as exc:
            if "NOGROUP" in str(exc).upper():
                logging.warning("redis group missing, recreating stream=%s group=%s", self.stream, self.group)
                self.ensure_group()
                return
            raise
        if not messages:
            return
        for _, entries in messages:
            for message_id, data in entries:
                body = data.get(b"data") or data.get("data")
                if not body:
                    logging.warning("missing payload body for %s", message_id)
                    self.redis_call("xack", self.stream, self.group, message_id)
                    continue
                try:
                    payload = json.loads(body.decode() if isinstance(body, (bytes, bytearray)) else body)
                except json.JSONDecodeError as exc:
                    logging.error("decode payload failed: %s", exc)
                    self.redis_call("xack", self.stream, self.group, message_id)
                    continue
                retry = False
                try:
                    retry = self.process_payload(payload)
                except Exception as exc:  # pylint: disable=broad-except
                    logging.exception("process payload failed: %s", exc)
                    retry = True
                if retry:
                    delay = max(self.retry_backoff, 5.0)
                    self.nack_and_reschedule(message_id, body, delay)
                else:
                    self.redis_call("xack", self.stream, self.group, message_id)

    def process_payload(self, payload: dict[str, Any]) -> bool:
        flag_id = payload.get("flag_id")
        if not flag_id:
            raise ValueError("invalid payload")

        start_resp = self.submit_start(flag_id)
        if start_resp is None:
            return False

        flag_value = start_resp.get("value") or payload.get("flag_value")
        challenge_id = start_resp.get("challengeId") or payload.get("challenge_id")
        target_id = start_resp.get("targetId") or payload.get("target_id")

        self.increment_in_flight(1)
        started = time.time()
        result = self.plugin.submit(flag_value)
        latency = max(time.time() - started, 0.001)
        self.metrics.record(latency)
        self.increment_in_flight(-1)

        status = result.status or "unknown"
        status_code = result.status_code
        message = result.message or (result.error or "")
        retry_after = int(result.retry_after) if result.retry_after else int(self.retry_backoff)

        finish_payload = {
            "secret": self.secret,
            "submitterId": self.submitter_id,
            "status": status,
            "statusCode": status_code,
            "message": message,
            "latencyMs": latency * 1000,
            "retryAfterSeconds": retry_after,
            "payload": {"plugin": self.plugin.id()},
        }
        if result.error:
            finish_payload["payload"]["error"] = result.error

        finish_resp = self.submit_finish(flag_id, finish_payload)
        if finish_resp is None:
            return True
        return finish_resp.get("retry", False)

    def submit_start(self, flag_id: str) -> dict[str, Any] | None:
        payload = {"secret": self.secret, "submitterId": self.submitter_id}
        resp = self.session.post(
            f"{self.control_panel}/api/v1/flags/{flag_id}/submit/start",
            json=payload,
            timeout=10,
        )
        if resp.status_code in (404, 409):
            logging.info("flag %s skipped (start %s)", flag_id, resp.status_code)
            return None
        resp.raise_for_status()
        data = resp.json()
        return data.get("flag", {})

    def submit_finish(self, flag_id: str, payload: dict[str, Any]) -> dict[str, Any] | None:
        resp = self.session.post(
            f"{self.control_panel}/api/v1/flags/{flag_id}/submit/finish",
            json=payload,
            timeout=10,
        )
        resp.raise_for_status()
        return resp.json()

    def nack_and_reschedule(self, message_id: str, body: bytes, delay: float) -> None:
        self.redis_call("xack", self.stream, self.group, message_id)

        def _requeue() -> None:
            time.sleep(delay)
            try:
                self.redis_call("xadd", self.stream, {"data": body})
            except Exception as exc:  # pylint: disable=broad-except
                logging.error("failed to requeue flag: %s", exc)

        threading.Thread(target=_requeue, name="submitter-requeue", daemon=True).start()

    def increment_in_flight(self, delta: int) -> None:
        with self.lock:
            self.in_flight = max(0, self.in_flight + delta)

    def current_in_flight(self) -> int:
        with self.lock:
            return self.in_flight

    # Redis helpers --------------------------------------------------------

    def _create_redis_client(self) -> redis.Redis:
        kwargs = dict(self._redis_kwargs)
        kwargs["retry"] = Retry(ExponentialBackoff(cap=10, base=1), 5)
        return redis.Redis(**kwargs)

    def _reconnect_redis(self) -> None:
        with self._redis_lock:
            try:
                self.redis.close()
            except Exception:  # pylint: disable=broad-except
                pass
            self.redis = self._create_redis_client()
            logging.info("re-created redis client for submitter %s", self.submitter_id)

    def redis_call(self, method: str, *args, **kwargs):
        attempt = 0
        while True:
            try:
                client = self.redis
                func = getattr(client, method)
                return func(*args, **kwargs)
            except (RedisConnectionError, RedisTimeoutError) as exc:
                attempt += 1
                logging.warning("redis %s failed (attempt %d): %s", method, attempt, exc)
                self._reconnect_redis()
                time.sleep(min(2 ** attempt, 5))
            except ResponseError:
                raise



if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    submitter = FlagSubmitter()
    submitter.run()
