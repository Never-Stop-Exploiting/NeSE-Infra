import json
import logging
import os
import secrets
import socket
import time
from datetime import datetime, timedelta, timezone

import psycopg
from psycopg.types.json import Json
import redis
from redis.exceptions import ResponseError
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


def generate_id(prefix: str) -> str:
    return f"{prefix}-{secrets.token_hex(16)}"


class FlagSubmitter:
    def __init__(self) -> None:
        redis_addr = env("REDIS_ADDR", "localhost:6379")
        host, _, port = redis_addr.partition(":")
        self.redis = redis.Redis(
            host=host,
            port=int(port or 6379),
            password=env("REDIS_PASSWORD"),
            decode_responses=False,
        )

        dsn = env("POSTGRES_DSN")
        if not dsn:
            raise RuntimeError("POSTGRES_DSN is required")
        self.dsn = dsn

        self.stream = env("FLAG_QUEUE_NAME", "flag:pending")
        self.group = env("FLAG_SUBMITTER_GROUP", "submitter")
        consumer_suffix = socket.gethostname().split(".")[0]
        self.consumer = env("FLAG_SUBMITTER_CONSUMER", f"python-{consumer_suffix}")

        self.endpoint = env("FLAG_SUBMIT_URL", "http://flagserver:5000/upload_flag")
        self.timeout = parse_duration(env("FLAG_SUBMIT_TIMEOUT", "5s"), 5.0)
        self.retry_backoff = parse_duration(env("FLAG_SUBMIT_RETRY_BACKOFF", "30s"), 30.0)
        self.submitter_id = env("FLAG_SUBMITTER_ID", "python-http")

        self.block_ms = int(parse_duration(env("FLAG_QUEUE_BLOCK", "5s"), 5.0) * 1000)
        self.plugin: FlagPlugin = HttpFlagPlugin(self.endpoint, self.timeout)

    def ensure_group(self) -> None:
        try:
            self.redis.xgroup_create(self.stream, self.group, id="0", mkstream=True)
        except ResponseError as exc:
            if "BUSYGROUP" in str(exc):
                return
            raise

    def run(self) -> None:
        self.ensure_group()
        logging.info("python flag submitter started")
        while True:
            try:
                self.consume_once()
            except Exception as exc:  # pylint: disable=broad-except
                logging.exception("submitter loop error: %s", exc)
                time.sleep(1)

    def consume_once(self) -> None:
        messages = self.redis.xreadgroup(
            self.group,
            self.consumer,
            {self.stream: ">"},
            count=1,
            block=self.block_ms,
        )
        if not messages:
            return
        for _, entries in messages:
            for message_id, data in entries:
                body = data.get(b"data") or data.get("data")
                if not body:
                    logging.warning("missing payload body for %s", message_id)
                    self.redis.xack(self.stream, self.group, message_id)
                    continue
                try:
                    payload = json.loads(body.decode() if isinstance(body, (bytes, bytearray)) else body)
                except json.JSONDecodeError as exc:
                    logging.error("decode payload failed: %s", exc)
                    self.redis.xack(self.stream, self.group, message_id)
                    continue
                try:
                    should_retry = self.process_payload(payload)
                except Exception as exc:  # pylint: disable=broad-except
                    logging.exception("process payload failed: %s", exc)
                    should_retry = True
                if should_retry:
                    self.nack_and_reschedule(message_id, body)
                else:
                    self.redis.xack(self.stream, self.group, message_id)

    def process_payload(self, payload: dict) -> bool:
        flag_id = payload.get("flag_id")
        flag_value = payload.get("flag_value")
        if not flag_id or not flag_value:
            raise ValueError("invalid payload")

        result: SubmissionResult = self.plugin.submit(flag_value)
        status = result.status
        status_code = result.status_code
        message = result.message
        now = datetime.now(timezone.utc)
        retry_delay = result.retry_after if result.retry_after is not None else self.retry_backoff
        next_retry_at = now + timedelta(seconds=retry_delay) if status == "unknown" else None

        self.record_attempt(flag_id, status_code, message, next_retry_at, now)
        self.update_flag(flag_id, status, now)
        self.append_log(flag_id, status, status_code, message)

        logging.info("flag submitted flag_id=%s status=%s code=%s message=%s", flag_id, status, status_code, message)
        return status == "unknown"

    def record_attempt(
        self,
        flag_id: str,
        status_code: int,
        message: str,
        next_retry_at: datetime | None,
        submitted_at: datetime,
    ) -> None:
        query_attempt = (
            "INSERT INTO submission_attempts (id, flag_id, submitter_id, status_code, error, "
            "submitted_at, next_retry_at, payload) VALUES (%s,%s,%s,%s,%s,%s,%s,%s)"
        )
        query_update_count = "UPDATE flags SET submission_count = submission_count + 1 WHERE id = %s"
        query_flag_log = "INSERT INTO flag_logs (flag_id, message, created_at) VALUES (%s,%s,%s)"
        attempt_id = generate_id("attempt")
        payload_json = Json({"submitter": self.submitter_id})
        flag_message = f"{submitted_at.isoformat()} status={status_code} {message}" if message else submitted_at.isoformat()

        with psycopg.connect(self.dsn) as conn, conn.cursor() as cur:
            cur.execute(
                query_attempt,
                (
                    attempt_id,
                    flag_id,
                    self.submitter_id,
                    status_code,
                    message or None,
                    submitted_at,
                    next_retry_at,
                    payload_json,
                ),
            )
            cur.execute(query_update_count, (flag_id,))
            cur.execute(query_flag_log, (flag_id, flag_message, submitted_at))
            conn.commit()

    def update_flag(self, flag_id: str, status: str, submitted_at: datetime) -> None:
        with psycopg.connect(self.dsn) as conn, conn.cursor() as cur:
            cur.execute(
                "UPDATE flags SET status = %s, last_submitted_at = %s WHERE id = %s",
                (status, submitted_at, flag_id),
            )
            conn.commit()

    def append_log(self, flag_id: str, status: str, status_code: int, message: str) -> None:
        log_id = generate_id("log")
        fields = Json({
            "status": status,
            "status_code": status_code,
            "message": message,
        })
        timestamp = datetime.now(timezone.utc)
        with psycopg.connect(self.dsn) as conn, conn.cursor() as cur:
            cur.execute(
                "INSERT INTO log_entries (id, timestamp, level, source, message, run_id, flag_id, fields) "
                "VALUES (%s,%s,%s,%s,%s,%s,%s,%s)",
                (
                    log_id,
                    timestamp,
                    "info" if status == "accept" else ("error" if status == "unknown" else "warn"),
                    "flag-submitter",
                    f"flag {flag_id} submitted",
                    None,
                    flag_id,
                    fields,
                ),
            )
            conn.commit()

    def nack_and_reschedule(self, message_id: str, body: bytes) -> None:
        logging.info("nack message %s delaying %.1fs", message_id, self.retry_backoff)
        self.redis.xack(self.stream, self.group, message_id)
        time.sleep(self.retry_backoff)
        self.redis.xadd(self.stream, {"data": body})


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    submitter = FlagSubmitter()
    submitter.run()


if __name__ == "__main__":
    main()
