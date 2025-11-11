# AwD Infra Web UI

Vue 3 + Vite front-end for the Attack-with-Defence control panel.

## Prerequisites

- Node.js 18+
- `pnpm` (recommended package manager)

Enable pnpm via corepack if required:

```bash
corepack enable
corepack prepare pnpm@latest --activate
```

## Scripts

```bash
pnpm install      # install dependencies
pnpm dev          # start dev server (http://localhost:5173)
pnpm build        # production build
pnpm preview      # preview built assets
pnpm lint         # run eslint
```

The dev server proxies `/api/*` to `http://localhost:8080` for local control-panel integration. Update `vite.config.ts` if the API host differs.

## Networking & Deployment

- **Development:** Vite listens on `5173` and, via the proxy in `vite.config.ts`, forwards any `/api/*` request to the Go control-panel running on `localhost:8080`. Use relative paths such as `fetch('/api/v1/targets')` so the proxy handles routing.
- **Production:** Build the static assets with `pnpm --filter ./web build`. The `docker/web.Dockerfile` image serves the compiled site with Nginx and reverse proxies `/api/` to the `control-panel` container (see `docker/web.nginx.conf`). When deploying behind another ingress/controller, replicate that proxy rule so API traffic reaches the control-panel service.

## Next Steps

- Wire views to actual REST endpoints:
  - **Exploits**: create, toggle, upload revisions, adjust timeouts.
  - **Targets**: single add or bulk import with helper preview.
  - **Flags**: inspect submissions, retry, read logs.
  - **Runs**: monitor queue, inspect stdout/stderr per execution.
  - **Runners**/**Logs**: view fleet status and filter structured logs.
- Subscribe to WebSocket channels for live updates (runs, logs, runner heartbeats).
- Implement authentication/authorization once available in the control-panel API.
