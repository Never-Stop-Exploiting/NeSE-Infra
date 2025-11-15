# Submitter Packages & Workflow

Flag submitters execute the user-provided `submit.sh` inside `/env` and report the JSON result back to the Control Panel. Packages are managed centrally and pushed to every submitter instance.

## Package Lifecycle

1. **Edit & Upload**
   - Use the **Submitter** page in the web UI (UI mirrors the Exploit file manager) to upload helper files and edit `submit.sh`.
   - Files live under `/tmp/submitter` inside the chroot. `submit.sh` is executable and receives the flag value as its first argument.

2. **Sync**
   - Click **Sync Submitters** to compute the package hash, broadcast an update via `FLAG_SUBMITTER_PACKAGE_CHANNEL` (default `submitter:package:update`), and expose the ZIP at `/api/v1/submitter/package/artifact`.
   - Submitters subscribe to the channel; when an event arrives they download the ZIP, extract it into `/env/tmp/submitter`, and use it for subsequent submissions. No in-process hot reload is required.

3. **Execution contract**
   - At runtime the supervisor executes:

     ```sh
     chroot /env /tmp/submitter/submit.sh "$FLAG"
     ```

   - Environment variables:
     - `FLAG_VALUE`, `FLAG_ID`
     - `TARGET_ID`, `CHALLENGE_ID`
     - `SUBMIT_META_<KEY>` for every metadata item passed by the Control Panel
   - `stdout` **must** be a single JSON object:

     ```json
     {
       "status": "accepted|failed|duplicated|unknown",
       "statusCode": 200,
       "message": "optional text",
       "retryAfter": 30
     }
     ```

     `stderr` is treated as log output and forwarded to the supervisor logs.

## REST API

- `GET /api/v1/submitter/package/files` – list current files.
- `POST /api/v1/submitter/package/files` – upload (multipart or JSON base64). `submit.sh` uploads overwrite the script.
- `GET /api/v1/submitter/package/files/{name}` – download a file.
- `DELETE /api/v1/submitter/package/files/{name}` – delete a file (except `submit.sh`).
- `GET /api/v1/submitter/package/script` / `POST .../script` – read or update `submit.sh`.
- `GET /api/v1/submitter/package/artifact` – download the ZIP (requires `X-Submitter-Secret` header); response contains `X-Submitter-Version`.
- `POST /api/v1/submitter/package/sync` – broadcast to all submitters; response includes the new version hash.
- `GET /api/v1/submitter/package/version` – return the latest computed version hash.

Submitters authenticate artifact/version requests with the `X-Submitter-Secret` header (same secret used for heartbeat/register).

## Submitter Supervisor Behavior

- Maintains a persistent workspace (default `/env/tmp/submitter`).
- Subscribes to `FLAG_SUBMITTER_PACKAGE_CHANNEL` via Redis PUB/SUB; messages use `{"type":"submitter-package","version":"...","timestamp":"..."}`.
- Downloads the ZIP from the Control Panel and atomically replaces the workspace.
- Spawns `submit.sh` per flag inside the chroot and parses the JSON result.
- Aggregates stderr into logs; stdout must be valid JSON.

This design keeps the supervisor thin, isolates user scripts inside `/env`, and allows the Control Panel to manage package contents centrally with a single click.***
