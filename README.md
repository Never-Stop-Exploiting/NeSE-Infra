# AwD Infra Scaffold

This repository provides a starter implementation for an Attack-with-Defence (AwD) automation platform. The current snapshot contains service entry points, configuration plumbing, and placeholder logic that can be extended into a production-ready stack.

## Services

- `control-plane`: REST API surface responsible for challenge/target/exploit management and operator tooling.
- `scheduler`: Periodically evaluates registered exploits against reachable targets and enqueues run jobs while respecting concurrency policies, packaging exploit artifacts into base64-encoded job payloads for runners.
- `runner`: Lightweight agent that polls for run jobs, executes exploits in isolated workspaces, and reports results.
- `flag-submitter`: Dedicated worker that dequeues harvested flags, applies rate limits, and hands off to submitter plugins.
- `web` (Vue 3 + Vite, pnpm managed): Operator dashboard for challenges, targets, runs, and flags. Dev server proxies `/api` to the control plane.

Each service imports shared configuration, logging, queue, and storage packages from `internal/`.

## Development Workflow

1. Copy `.env.example` (to be added) and adjust connection strings, secrets, and rate limits.
2. Start dependencies with `docker compose up` (see `docker-compose.yml`). Redis Streams is now required for Scheduler/Runner/Submitter; default settings expect a Redis instance reachable at `$REDIS_ADDR`.
3. Launch individual services from the `cmd/` directory using `go run ./cmd/<service>`.
4. Install frontend dependencies with `pnpm install --filter ./web` and run the dashboard via `pnpm --filter ./web dev` (served on http://localhost:5173 with the Vite proxy forwarding `/api/*` to the control plane).

For an end-to-end Docker setup, run `docker compose up --build`. This spins up Postgres, Redis, control-plane, scheduler, flag-submitter, runner, and the web UI (served at http://localhost:5173).

## Deployment Notes

- The Vite dev server proxies `/api/*` to `http://localhost:8080` as configured in `web/vite.config.ts`.
- Production builds (`pnpm --filter ./web build`) are served by the Nginx container defined in `docker/web.Dockerfile`, which reverse proxies `/api/` back to the `control-plane` service. Adjust `docker/web.nginx.conf` if running behind an external ingress.

## Next Steps

- Implement persistent repositories backed by PostgreSQL and Redis.
- Harden the Redis Streams queue implementation (retry policies, dead-letter handling, metrics) or evaluate NATS JetStream for future scaling.
- Flesh out REST handlers, WebSocket protocols, and authentication.
- Build runner execution sandbox and flag submission adapters.
  - Runner containers ship with an `/env` chroot preloaded with language runtimes (e.g. pwntools). Each task arrives as a zipped payload; the runner extracts it under `/env/tmp/<run-id>`, executes `./run.sh <ip> <port>` inside the chroot, and captures stdout/stderr. Flags are taken from a `flag` file or stdout via regex.
  - Concurrency and extraction defaults are configurable via `RUNNER_MAX_PARALLEL`, `RUNNER_FLAG_FILE`, `RUNNER_FLAG_REGEX`, and `RUNNER_DEFAULT_TIMEOUT` environment variables.
- Connect the new Vue UI (`web/`) to control-plane APIs (REST + WebSocket) for live orchestration.

More details on writing flag submitter extensions can be found in `docs/flag-submitters.md`.
