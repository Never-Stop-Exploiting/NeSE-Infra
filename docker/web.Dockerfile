FROM node:20 AS build
WORKDIR /app

COPY package.json pnpm-workspace.yaml ./
COPY web/package.json web/tsconfig.json web/tsconfig.node.json web/vite.config.ts web/index.html web/README.md ./web/
COPY web/src ./web/src

RUN corepack enable && corepack prepare pnpm@latest --activate && pnpm install --filter ./web && pnpm --filter ./web build

FROM nginx:1.25-alpine
COPY --from=build /app/web/dist /usr/share/nginx/html
COPY docker/web.nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
