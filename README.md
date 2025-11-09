# AwD Infra Scaffold

> Attack-with-Defence (AwD) 自动化平台脚手架，已经提供控制面 API、调度器、Runner、Flag Submitter 与 Vue 运维面板，可直接连接 Postgres + Redis 复现一条端到端的攻防自动化链路。

## 项目简介

AwD Infra 以 Go + Vue3 实现一套模块化的 AwD 平台：控制面负责题目/目标/Exploit 元数据和运行状态，调度器生成待执行任务并投递到 Redis Streams，Runner 在隔离工作区执行 Exploit、提取 Flag 并回传日志，Flag Submitter 针对捕获的 flag 实施限速与重试，Web UI 则提供运维视图。当前仓库同时内置 Postgres schema 迁移、artifact 存储、默认 HTTP flag 提交插件以及 Docker Compose 模板，方便本地或实验环境快速落地。

## 当前能力

- **Control Plane / REST API (`cmd/control-plane`)**：提供 `/api/v1` 端点，实现 Challenge/Target/Exploit CRUD、批量导入目标 (`/targets/import`)、artifact 文件/`run.sh` 管理、Run/Flag/日志查询、Run start/finish/cancel Hook、Flag 重试、Runner 注册与心跳、Scheduler 运行参数热更新以及 Target × Exploit 覆盖矩阵维护；捕获的 flag 会落库并写入 Flag 队列。
- **Scheduler (`cmd/scheduler`)**：按 `SchedulerSettings` 设置的策略（轮询批次或按 Target×Exploit 连续运行）生成 `RunnableWork`，结合 Target 并发限制、Exploit Runner Tags、Target-Exploit Override 计算可执行组合，打包 artifact（内联或 URI）后以 `jobs.JobPayload` 写入 Redis Streams（消费组 + 可见性超时），并在缺乏匹配 Runner Tags 时跳过任务。
- **Runner (`cmd/runner`)**：注册到控制面并定期心跳，使用 `RUN_QUEUE_NAME` 对应的 Redis Streams 消费组阻塞拉取任务；自动下载/解压 ZIP artifact 到 `/env/tmp/<run-id>`，若缺少 `run.sh` 会用 payload 中的 `runScript/entrypoint` 补齐；通过 `chroot` + `./run.sh <ip> <port>` 执行，收集 stdout/stderr，优先从指定 flag 文件读取 flag，失败时回退到正则；Run 状态会在 start/finish/cancel、心跳、取消检测时同步到控制面，同时把 flag 推送到 flag 队列。
- **Flag Submitter (`cmd/flag-submitter`)**：消费 `FLAG_QUEUE_NAME`，解析 `submitter.QueueMessage`，调用内置 HTTP 插件（可插拔接口在 `docs/flag-submitters.md`）执行提交，结合状态码区分 `accepted/duplicated/failed/unknown` 并写回 `flags`、`submission_attempts`、结构化日志，失败场景按配置的 backoff 进行 `Nack`+重排队。
- **持久化 (`internal/storage/postgres`)**：初始化完整 Postgres schema（Challenges/Targets/Exploits/Runs/Flags/Runners/Logs/Overrides 等），以本地文件夹存放 Exploit artifacts 与 `run.sh`，支持列出/上传/删除/打包，并保证 ZIP 不可逃逸工作目录。
- **Web UI (`web/`)**：Vue3 + Vite + Pinia + Axios，路由覆盖 Dashboard、Targets、Exploits、Runs、Flags、Logs、Runners、Scheduler 视图；开发阶段通过 `pnpm --filter ./web dev`（5173）并代理 `/api` 到控制面，生产由 `docker/web.Dockerfile` + Nginx 托管。

## 架构总览

```
                      ┌──────────────────┐
                      │      Web UI      │
                      │ (Vue + Vite)     │
                      └────────┬─────────┘
                               │ REST /api + future WS
                       ┌───────▼────────┐
        Postgres ◄────►│  Control Plane │◄────► Redis (Queues / Cache)
                       │  (REST + Biz)  │
                       └───┬─────────┬──┘
                           │         │
                           │         │ Scheduler settings / overrides
                           │         │
                     ┌─────▼────┐    │
                     │Scheduler │    │
                     │(Go loop) │    │
                     └────┬─────┘    │
                          │ publishes Run jobs
                ┌─────────▼───────────┐
                │   Redis Streams     │
                │   (Run Queue)       │
                └─────────▲───────────┘
                          │ consumes
                    ┌─────┴─────┐
                    │ Runner(s) │───┐
                    │ (chroot)  │   │ emits flags/logs
                    └─────┬─────┘   │
                          │         │
                 ┌────────▼────────┐│
                 │ Redis Streams   ││
                 │  (Flag Queue)   ││
                 └────────▲────────┘│
                          │         │
                    ┌─────┴────────┐│
                    │ Flag Submitter│
                    │ (HTTP plugin) │
                    └───────────────┘
```

## 代码结构

```
cmd/                    # 控制面、调度器、Runner、Flag Submitter 入口
internal/
  config/               # 统一环境变量解析
  controlplane/         # HTTP server + handler
  scheduler/            # 调度主循环
  runner/               # Runner agent、控制面客户端
  submitter/            # flag submitter、插件、消息定义
  storage/postgres/     # Repository + artifact 管理
  queue/                # Redis Streams 实现 + noop 回落
web/                    # Vue3 前端
docs/                   # 扩展协议等文档
docker/                 # 服务 Dockerfile + web Nginx
```

## API 能力概览

| 资源 | 路径 | 能力 |
| --- | --- | --- |
| Challenges | `/api/v1/challenges` | GET/POST/PUT/PATCH/DELETE，维护题目元数据及 Metadata |
| Targets | `/api/v1/targets` | GET/POST + PATCH/DELETE，支持并发限制/状态更新 |
| Targets Import | `/api/v1/targets/import` | 依据基准 IP + 数量批量生成目标，自动填充队伍名称 |
| Exploits | `/api/v1/exploits` & `/api/v1/exploits/{id}` | 列表、创建（JSON 或 multipart）、启用/禁用、超时时间、Metadata 更新、删除、上传/下载 artifact/文件/`run.sh` |
| Runs | `/api/v1/runs` & `/api/v1/runs/{id}` | 游标查询、详情（含 stdout/stderr/flag）、start/finish/cancel Hook |
| Flags | `/api/v1/flags` & `/api/v1/flags/{id}` | 列表/过滤、详情（包含 attempts）、重试 (`/requeue`)、查看日志 (`/logs`) |
| Runners | `/api/v1/runners` | 列表、手动删除僵尸 Runner、`/register`、`/heartbeat` |
| Scheduler | `/api/v1/scheduler/settings` & `/api/v1/scheduler/targets` | 获取/修改调度参数、查看每个 Target 的 Exploit 覆盖、覆写 Target × Exploit 启停 |
| Logs | `/api/v1/logs` | 结构化日志查询，支持 level/source/runId/flagId/limit 过滤 |

## 快速开始

- **依赖准备**：Go 1.21+、pnpm（Node 18+）、Docker / Docker Compose、Redis 7、Postgres 15。如果使用本仓的 Compose 模板则无需额外安装 Redis/Postgres。
- **配置环境变量**：复制示例变量并根据实际修改。

  ```bash
  cp .env.example .env
  ```

- **启动基础依赖**（可选，若未使用 Compose 运行全栈）：  
  `docker compose up -d postgres redis`

- **运行控制面 / 调度器 / Flag Submitter / Runner（本地方式）**：

  ```bash
  go mod download
  go run ./cmd/control-plane &
  go run ./cmd/scheduler &
  go run ./cmd/flag-submitter &
  CONTROL_PLANE_URL=http://localhost:8080 go run ./cmd/runner &
  ```

- **运行 Web 控制台**：

  ```bash
  pnpm install --filter ./web
  pnpm --filter ./web dev
  # 浏览 http://localhost:5173（Vite 自动代理 /api -> http://localhost:8080）
  ```

- **端到端模式（推荐）**：单条命令启动 Postgres、Redis、控制面、调度器、Python flag submitter、Runner 以及 Web。

  ```bash
  docker compose up --build
  ```

## 配置项速查

| 变量 | 说明 | 默认值（`.env.example`） |
| --- | --- | --- |
| `POSTGRES_DSN` | 控制面/调度器/submitter 使用的 PostgreSQL 连接串 | `postgres://awd:awd@postgres:5432/awd?sslmode=disable` |
| `POSTGRES_ARTIFACT_DIR` | Exploit artifact+脚本存放目录 | `/var/lib/awd/artifacts` |
| `REDIS_ADDR` | Redis 地址（用于 Run/Flag 队列） | `redis:6379` |
| `RUN_QUEUE_NAME` / `FLAG_QUEUE_NAME` | 队列名称与可见性、重试间隔 | `exp:pending` / `flag:pending`（在 `internal/config` 中有默认值） |
| `SCHEDULER_POLL_INTERVAL` / `SCHEDULER_MAX_BATCH` | 调度轮询间隔与单轮派发上限 | `5s` / `50` |
| `RUNNER_SECRET` | Runner 调用 `/runs/*`、`/runners/*` 的共享密钥 | `dev-runner-secret` |
| `CONTROL_PLANE_TOKEN` | 访问 `/api/v1/**` 运维接口时需要携带的 `Authorization: Bearer` Token | _无默认值，必须显式设置_ |
| `CONTROL_PLANE_URL` | Runner 注册/下载 artifact 时访问的控制面地址 | `http://control-plane:8080` |
| `RUNNER_CHROOT_PATH` / `RUNNER_WORKSPACE_ROOT` | Runner chroot 根目录与临时工作区 | `/env` / `/env/tmp` |
| `RUNNER_FLAG_FILE` / `RUNNER_FLAG_REGEX` | Flag 文件名与匹配正则 | `flag` / `flag\{[a-zA-Z0-9_\-]+\}` |
| `FLAG_SUBMIT_URL` / `FLAG_SUBMIT_TIMEOUT` | 默认 HTTP 提交端点与超时 | `http://flagserver:5000/upload_flag` / `10s` |

更多变量可参考 `internal/config/config.go` 中的默认值解释。

## 调度与执行流程

1. Operator 通过 Web UI 或直接调用 Control Plane API 维护 Challenge、Target、Exploit，并可批量导入目标或上传/下载 artifact。
2. 调度器读取可运行的 RunWorkItem（结合 Target 状态、并发限制、Override、Runner Tags），遵循 `RoundInterval` 和 `MaxDispatchBatch` 将任务编码成 `jobs.JobPayload` 并推送到 Redis Streams。
3. Runner 按心跳间隔抢占可用 slot，下载 artifact、在 chroot 工作区执行 `run.sh`，并把 stdout/stderr、Flag、错误信息上报到 Control Plane；若检测到 Run 被取消会中止执行。
4. Runner 将解析出的 Flag 经结构化消息写入 Flag 队列，Control Plane 同步更新日志。
5. Flag Submitter 轮询队列，调用插件提交 flag，记录 `submission_attempts`、更新 Flag 状态并将结果写入日志；未知状态会重试并按 backoff 延迟。

## Web 控制台

- Targets 支持单个新增、批量导入、状态编辑以及并发上限调节。
- Exploits 列表可上传 ZIP/二进制、查看/替换 `run.sh`、配置 Runner Tags、Schedule Weight、Timeout、Metadata。
- Runs 页面展示最新队列，进入详情可查看 stdout/stderr/Flag，必要时可手动取消。
- Flags 页面展示状态、Run 关联、提交历史，支持一键重试与查看日志。
- Scheduler 视图列出每个 Target 能匹配的 Exploit 并可针对单个 Target 覆写启停。
- Logs 与 Runners 视图分别提供结构化日志筛选、Runner 心跳状态监控。
- 访问 Web UI 时需先在登录页输入 `CONTROL_PLANE_TOKEN`，浏览器会缓存该 Token 并自动为后续请求附加 `Authorization: Bearer` 头。

## 测试与质量

- Go 代码：`go test ./...`
- Web 前端：`pnpm --filter ./web lint`、`pnpm --filter ./web build`
- 推荐在提交前运行 `docker compose build` 验证 Dockerfile 没有缺依赖。

## 文档与后续

- 架构细节请参考 `design.md`。
- Flag Submitter 插件协议详见 `docs/flag-submitters.md`。
- 持续规划、待办收敛在 `TODO.md`，包括真实消息队列、观测性、Kubernetes 部署等主题。
