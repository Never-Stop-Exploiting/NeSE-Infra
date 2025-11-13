# AwD Infra

> Attack-with-Defence (AwD) 自动化平台脚手架：Go + Vue3 打造的控制面、调度、分发、执行、提交流程，可直接连接 Postgres 与 Redis 形成一条端到端的攻防自动化链路。

## 功能特性

- **模块化服务**：Control Panel、Scheduler、Dispatcher、Runner、Flag Submitter、Vue 运维面板全部开箱即用，可按需横向扩展。
- **标签感知分发**：Scheduler 只需写入主 Run Stream，由 Dispatcher 读取并依据 Runner tags/并发压力路由到各 Runner 专属 Stream，彻底消除误抢任务导致的 Nack。
- **可观测与管控**：Control Panel 暴露 `/api/v1` REST 接口与 Web 面板，支持 Run/Flag/Runner 可视化、执行记录下载、Flag 重试及目标覆盖矩阵维护；结构化日志统一输出到 stdout，可直接通过 `docker compose logs` 观测。
- **安全执行**：Runner 在 chroot 工作区拉起 exploit，支持 artifact 缓存、run.sh 自动补全、Flag 文件/正则回退及远程取消；进程组会在超时或取消时被整组 `SIGKILL` 并由 PID 1 reaper 收尸。
- **流量治理**：Flag Submitter 通过 Redis Streams 消费队列并根据插件策略限速重试；Control Panel 负责 run 超时/runner 离线回收并定期 `XTRIM` 主/子 Stream，避免 Redis 无限膨胀。

## 核心组件

| 组件 | 说明 |
| --- | --- |
| **Control Panel (`cmd/control-panel`)** | 提供 `/api/v1` REST API、Runner/Submitter 心跳注册、Run/Flag 查询、Scheduler 设置、Target×Exploit Override 管理，并驱动队列 reclaim 与 stream trim；运行日志直接写 stdout。|
| **Scheduler (`cmd/scheduler`)** | 从 Postgres 读取 runnable work（目标状态、并发限制、Runner Tags、Overrides），按照 Interval/Continuous 策略打包 `jobs.JobPayload` 写入主 Run Stream。|
| **Dispatcher (`cmd/dispatcher`)** | 消费主 Run Stream，根据 Runner tag 集与实时负载挑选合适 Runner，并把 payload 转发到该 Runner 的专属 Stream + consumer group。|
| **Runner (`cmd/runner`)** | 在 chroot 工作区执行 exploit，支持 artifact 缓存、Flag 文件/正则提取、远程取消，并将 stdout/stderr 通过 Control Panel 打到 stdout，同时把捕获 flag 写入 Flag Stream；最新版本会为每次 run 创建独立进程组，并在容器 PID1 场景下主动 Wait4 清理僵尸进程。|
| **Flag Submitter (`cmd/flag-submitter`)** | 消费 Flag Stream，调用内置或自定义插件提交旗帜，并回传 `/flags/{id}/submit/start|finish`，失败时按 backoff 再排队。|
| **Web UI (`web/`)** | Vue3 + Vite + Pinia + Tailwind。提供 Dashboard、Targets、Exploits、Runs、Flags、Runners、Scheduler 视图；访问需提供 `CONTROL_PANEL_TOKEN`（浏览器本地缓存）。|

```
                   ┌────────────┐           ┌─────────────┐
                   │    Web     │<─────────>│ ControlPane │<───┐
                   │ (Vue UI)   │  REST API │  l + API    │    │
                   └────┬───────┘           └──────┬──────┘    │
                        │                           │          │
                        │ Scheduler settings        │          │
                        │                           │          ▼
                    ┌───▼──────┐             ┌──────▼──────┐  Redis
                    │Scheduler │  Enqueue    │ Dispatcher  │ Streams
                    └───┬──────┘  main run  └──────┬──────┘  (exp:pending, exp:runner:<id>, flag:pending)
                        │      stream              │
                        │                          │ routes per runner
                        ▼                          ▼
                 ┌─────────────┐            ┌─────────────┐
                 │ Runner (n)  │───────────►│ Flag Queue  │
                 │ (chroot)    │ flags/logs │ + Submitter │
                 └─────────────┘            └─────────────┘
```

## 快速开始

1. **基础依赖**：Docker / Docker Compose、Go 1.22+、Node 18+/pnpm（前端开发可选）。
2. **环境变量**：复制 `.env.example` 为 `.env`（或对各服务单独覆写）。至少需要设置 `CONTROL_PANEL_TOKEN`、`RUNNER_SECRET`、`SUBMITTER_SECRET`。默认 Redis/ Postgres 账号密码已经在示例文件中提供。
3. **启动核心链路**：
   ```bash
   docker compose up -d postgres redis
   docker compose up -d control-panel scheduler dispatcher web
   # 可视需求再起 submitter / runner
   ```
4. **测试运行链路**：仓库附带 `test_env/`，包含示例挑战、flag server 以及 requests/pwntools Runner：
   ```bash
   cd test_env
   docker compose up --build
   ```
   Web UI 默认暴露在 `http://localhost:5173`，Token 填写 `.env.example` 的 `CONTROL_PANEL_TOKEN`。

## 配置环境变量

以下仅列出仍在代码中使用的关键变量，更多可查阅 `internal/config/config.go`。

### 通用

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `APP_NAME` | 日志/标签展示名称 | `awd-infra` |
| `LOG_LEVEL` | slog 等级 | `info` |
| `CONTROL_PANEL_TOKEN` | 访问 `/api/v1/**` 与 Web UI 时的 Bearer Token | **必填** |
| `CONTROL_PANEL_LOG_OUTPUT_LIMIT` | Run stdout/stderr 保留的最大字节数，超出会截断 | `4096` |
| `RUNNER_SECRET` / `SUBMITTER_SECRET` | Runner/Submitter 调用控制面 API 的共享密钥 | `dev-runner-secret` / `dev-submitter-secret` |
| `POSTGRES_*` | DSN、连接池配置、artifact 存储目录 | 见 `.env.example` |
| `REDIS_ADDR` / `REDIS_PASSWORD` | Redis 连接信息 | `redis:6379` / `dev-redis-password` |

### Run / Flag 队列

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `RUN_QUEUE_NAME` / `FLAG_QUEUE_NAME` | 主 Run Stream、Flag Stream 名称 | `exp:pending` / `flag:pending` |
| `RUN_QUEUE_VISIBILITY` | 消费可见性（Control Panel reclaim 超时阈值） | `30s` |
| `RUN_QUEUE_RETRY_DELAY` | Dispatcher/Runner `NACK` 再排队延迟 | `5s` |
| `RUN_QUEUE_RUNNER_PREFIX` | 生成 per-runner stream 的前缀 | `<RUN_QUEUE_NAME>:runner` |
| `RUN_QUEUE_TRIM_MAXLEN` | Stream 保留的最大条目数；超出会 `XTRIM` | `50000` |
| `FLAG_QUEUE_VISIBILITY` / `FLAG_QUEUE_RETRY_DELAY` | Flag Stream 可见性/重试间隔 | `30s` / `10s` |

### Scheduler

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `SCHEDULER_POLL_INTERVAL` | Interval 策略基础间隔 | `5s` |
| `SCHEDULER_MAX_BATCH` | 单轮派发上限 | `50` |

### Dispatcher

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `DISPATCHER_ID` | Dispatcher 消费者 ID（Redis group=dispatcher） | `dispatcher` |
| `DISPATCHER_MAX_BATCH` | `XREADGROUP` 每次获取消息上限 | `32` |
| `DISPATCHER_BLOCK_TIMEOUT` | 阻塞读取最大时长 | `5s` |
| `DISPATCHER_REFRESH_INTERVAL` | 缓存 Runner 列表的刷新周期 | `10s` |

### Runner

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `RUNNER_ID` | Runner 唯一 ID，用于 stream/心跳 | 自动生成 |
| `RUNNER_TYPE` | Redis consumer group 名（`runner-<type>`） | `default` |
| `RUNNER_TAGS` | 逗号分隔标签，Dispatcher 会基于此路由 | - |
| `RUNNER_MAX_PARALLEL` | 并发 slot 数量 | `2` |
| `RUNNER_LOG_OUTPUT_LIMIT` | Runner 上报 stdout/stderr 的最大字节数 | `4096` |
| `RUNNER_WORKSPACE_ROOT` / `RUNNER_CHROOT_PATH` | 临时目录与 chroot 根 | `/env/tmp` / `/env` |
| `CONTROL_PANEL_URL` | 控制面地址（下载 artifact / 心跳） | - |
| `RUNNER_FLAG_FILE` / `RUNNER_FLAG_REGEX` | Flag 文件名 / 正则 | `flag` / `flag\{[a-zA-Z0-9_\-]+\}` |
| `RUNNER_ARTIFACT_CACHE_*` | artifact 缓存路径/TTL/容量 | `/env/tmp/cache`, `30m`, `2GiB` |

### Flag Submitter

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `FLAG_SUBMIT_URL` / `FLAG_SUBMIT_TIMEOUT` | HTTP 提交端点与超时 | `http://flagserver:5000/upload_flag` / `5s` |
| `FLAG_SUBMIT_GLOBAL_RATE` / `FLAG_SUBMIT_PER_CHALLENGE` | 限速 | `10` / `5` |
| `FLAG_SUBMIT_MAX_ATTEMPTS` / `FLAG_SUBMIT_RETRY_BACKOFF` | 重试次数与退避 | `5` / `30s` |
| `FLAG_SUBMIT_CONCURRENCY` | Submitter 并发 | `1` |

## API 速览

所有请求均需在 Header 中带上 `Authorization: Bearer <CONTROL_PANEL_TOKEN>`。

### Challenge / Target

- `GET /api/v1/challenges` 、`POST /api/v1/challenges`、`GET /api/v1/challenges/{id}`、`DELETE /api/v1/challenges/{id}`
- `GET /api/v1/targets`、`POST /api/v1/targets`、`POST /api/v1/targets/import`（CSV 批量导入）、`PATCH /api/v1/targets/{id}`（状态/并发限制）

### Exploit 及 Artifact

- `GET /api/v1/exploits`、`POST /api/v1/exploits`、`PATCH /api/v1/exploits/{id}`（标签、权重、超时、metadata）
- `POST /api/v1/exploits/{id}/run-script`、`GET /api/v1/exploits/{id}/run-script`
- `POST /api/v1/exploits/{id}/revisions`（上传新的 ZIP/二进制）、`GET /api/v1/exploits/{id}/artifact`
- `POST /api/v1/exploits/{id}/test-run`：对指定 target 触发一次标准 run，**会完整进入 Dispatcher → Runner → Flag Submitter 流程，因此若 exploit 成功会真实提交 flag**。

### Runs

- `GET /api/v1/runs`：支持 `status` / `exploitId` / `targetId` / `page` / `pageSize` 查询。
- `GET /api/v1/runs/{id}`：返回 run 详情、stdout/stderr、flag 信息。
- `POST /api/v1/runs/{id}/cancel`：请求取消当前 run，Runner 会在下一次取消轮询时杀掉进程组。
- `POST /api/v1/runs/cancel`：批量接受 run_id 列表并取消。

### Flags

- `GET /api/v1/flags`：按状态/挑战筛选。
- `POST /api/v1/flags/retry`、`POST /api/v1/flags/cancel`：批量重试或取消。
- `GET /api/v1/flags/{id}`、`GET /api/v1/flags/{id}/logs`：查看提交历史。

### Runner / Submitter

- `GET /api/v1/runners`：展示所有注册 Runner，包括 tags、心跳时间、活跃 job、`RUN_QUEUE_NAME` 等。
- `DELETE /api/v1/runners/{id}`：删除 offline Runner 记录。
- `GET /api/v1/submitters`、`DELETE /api/v1/submitters/{id}`。

### Scheduler

- `GET /api/v1/scheduler/settings`、`POST /api/v1/scheduler/settings`：查询/更新轮询策略。
- `GET /api/v1/scheduler/targets`、`PATCH /api/v1/scheduler/targets/{targetId}`：针对某个 target 覆写是否允许调度、指定 exploit 白/黑名单。

### 日志

结构化日志不再落库/展示在 Web UI，Control Panel 与 Runner/Submitter 会直接把事件写到 stdout，建议通过 `docker compose logs control-panel`、`docker compose logs runner` 等方式查看。`GET /api/v1/logs?level=info&runId=...` 接口仅为兼容旧脚本而保留，默认返回空列表。

## 开发与测试

- **Go 单元测试**：`go test ./...`（确保 `$HOME/.cache/go-build` 可写）。
- **前端调试**：
  ```bash
  pnpm install
  pnpm --filter ./web dev   # http://localhost:5173
  pnpm --filter ./web build # 产出静态资源
  ```
- **Docker 本地镜像**：`docker compose build control-panel scheduler dispatcher runner flag-submitter web`

## 运维/调试贴士

- Control Panel 的 maintenance loop 会在每个 `RUN_QUEUE_VISIBILITY/2`：
  1. reclaim 掉超时但未 ack 的 run，更新数据库状态并重新排队；
  2. 对主 stream 以及每个 `RUN_QUEUE_RUNNER_PREFIX:<runnerId>` stream 执行 `XTRIM`，若存在 pending 则只删除早于最老 pending 的 entry，否则按 `RUN_QUEUE_TRIM_MAXLEN` 近似裁剪。
- Dispatcher 默认组名为 `dispatcher`，可用 `redis-cli XPENDING exp:pending dispatcher` 观察积压。
- Runner 使用 `Setpgid` + `SIGKILL` + child reaper，若仍观察到僵尸建议确认 Runner 是否运行在 PID1，以及内核是否允许 `wait4`。
- `POST /api/v1/exploits/{id}/test-run` 触发的 run 会进入 Flag Submitter，因此在真实靶场使用时请谨慎。

## 相关文档

- `design.md`：总体架构、队列模型与未来规划。
- `docs/flag-submitters.md`：自定义 Flag Submitter 插件协议。
- `TODO.md`：待办与长期优化清单。

欢迎在 Issue/PR 中讨论新的调度策略、Runner 沙箱、更加细粒度的指标与观测方案。
