# AwD Infra 设计说明

## 1. 系统概览
AwD Infra 是 Attack-with-Defence 竞赛的自动化平台，帮助队伍协调不同题目的漏洞利用、自动提交 flag，并提供可视化控制台监控整体运行状况。整体采用模块化微服务设计，通过消息队列与数据库/缓存实现解耦。

```
┌──────────────┐     ┌─────────────┐     ┌────────────┐
│ ControlPanel │◄───►│ Scheduler   │◄───►│ Queue/Broker│
└─────▲────────┘     └────┬────────┘     └─────▲──────┘
      │                    │                      │
      │ REST/WebSocket     │ 任务投递             │Run/Flag/Log
      ▼                    ▼                      │
┌──────────────┐     ┌─────────────┐         ┌────▼──────┐
│ Web UI       │     │ Runner(s)   │         │ Flag      │
│ (Vue + Vite) │     │ (Docker)    │         │ Submitter │
└──────────────┘     └─────────────┘         └───────────┘
```

### 关键组件
- **Control Panel / API**：统一配置中心，管理挑战、目标、Exploit、Runner、Flag 等元数据；提供 REST API，未来扩展 WebSocket。
- **Scheduler**：基于「目标 × Exploit」组合生成待执行任务，执行并发控制、排队策略、重试回退；将任务投递到消息队列。
- **Runner**：在隔离环境（Docker+chroot `/env`）执行 exploit；下载并解压 artifact，调用 `./run.sh <ip> <port>`，收集 stdout/stderr 并解析 flag。
- **Flag Submitter**：从 Redis 队列获取已捕获的 flag，执行去重、限速和重试策略；当前实现通过 HTTP（默认 `FLAG_SUBMIT_URL`，例如测试 flagserver）提交，记录提交历史和日志。
- **存储层**：PostgreSQL（结构化配置与运行数据）、Redis（队列/锁/限流/缓存）以及对象存储（日志、依赖包）——当前实现为内存仓库。
- **观测性**：结构化日志（后续接入 Loki / ELK）、指标（Prometheus）、分布式追踪（OpenTelemetry）规划。
- **Web UI**：Vue3 + Vite + Pinia 构建的运维界面，支持配置管理、状态监控、日志查看等功能。

## 2. 数据模型
核心领域模型定义在 `internal/domain/models.go`：
- `Challenge`：题目信息（类别、类型、描述）。
- `Target`：被攻击服务实例（队伍标识、IP、端口、并发限制）。
- `Exploit` / `ExploitRevision`：Exploit 基本信息、Runner 类型适配、启用状态、最新构建 artifact。
- `Run`：一次 Exploit 对 Target 的执行记录；包含状态、时间戳、关联 Runner/Flag。
- `Flag` / `SubmissionAttempt`：Flag 捕获及提交历史；记录提交状态、错误信息、下次重试时间。
- `Runner`：注册 Runner 的类型、能力、心跳状态。
- `LogEntry`：结构化日志条目，支持按 level/source/runId/flagId 过滤。
- `RunWorkItem`：Scheduler 投递给 Runner 的调度任务（包含 Artifact、Flag 解析规则等）。

## 3. 运行流程
1. **配置阶段**：Control Panel 维护挑战、目标、Exploit 等信息；可从 UI 导入/创建。
2. **调度阶段**：Scheduler 定期从仓库查询可运行的目标与启用的 Exploit，依据策略生成 `RunWorkItem` 并投递到 Redis Streams 队列。
3. **执行阶段**：Runner 作为 Redis Streams consumer group 成员阻塞等待任务，解压 Artifact 至 `/env/tmp/<run-id>`，执行 `./run.sh ip port`，捕获输出、解析 flag、上传执行结果。
4. **Flag 管理**：捕获 flag 存入 `flag` 表，Flag Submitter 统一限速、去重、重试后输出最终状态。
5. **可视化/监控**：Web UI 通过 `/api/v1` REST 接口（未来可扩展 WebSocket）展示 targets/exploits/runs/flags/logs 状态；日志、metrics 计划接入 Loki/Prometheus。

## 4. 配置与约定
- 所有服务通过 `internal/config` 读取环境变量（`.env`），统一控制 Postgres/Redis 连接、Runner 参数、Flag 提交流程。
- Runner 环境：所有 exploit 在 `/env` chroot 内运行，临时目录 `/env/tmp/<run-id>`；artifact 为 zip 压缩，解压后必须包含 `run.sh`。
- Flag 提取策略：优先读取 `flag` 文件（可通过 payload/配置覆盖），否则使用正则（默认 `flag\{[a-zA-Z0-9_\-]+\}`）匹配 stdout。
- API 前缀：统一使用 `/api/v1/**`；Nginx（生产）与 Vite（开发）均将该前缀代理到 Control Panel。
- 队列：默认使用 Redis Streams（`internal/queue/redis`），通过 `queue.Broker` 抽象可扩展到 NATS/Kafka 等实现。

## 5. Web UI
- **技术栈**：Vue3 + TypeScript + Vite + Pinia + Axios；pnpm 管理依赖。
- **主要页面**：
  - Dashboard：展示各类计数、Runner 负载、Flag 状态概览。
  - Targets：支持单个添加、批量导入（IP 递增、队伍名称列表）并预览。
  - Exploits：创建、启用/禁用、设置 timeout、上传 revision。
  - Flags：查看提交状态、日志、点击重试。
  - Runs：查看执行队列、RunDetail（stdout/stderr）、状态流转。
  - Runners：展示在线 Runner 及并发能力。
  - Logs：支持多条件筛选的结构化日志列表。
- **开发/部署**：
  - 开发：`pnpm install --filter ./web`；`pnpm --filter ./web dev` (5173)。
  - 生产：`pnpm --filter ./web build`，由 `docker/web.Dockerfile` 使用 Nginx 服务静态资源并反向代理 `/api/`。

## 6. 部署
- **Docker Compose**：提供 Postgres、Redis、Control Panel、Scheduler、Runner、Flag Submitter、Web UI 等容器；Runner/ Scheduler 已使用 Redis 队列，存储层仍为内存实现，可逐步替换为数据库。
- **CI/CD 规划**：构建 Go 二进制、运行 `go test ./...`、构建前端、生成容器镜像；未来可接入 GitHub Actions/GitLab CI。
- **Kubernetes 目标**（TODO）：以 Deployment/StatefulSet 形式部署各组件，利用 Service/Ingress/VPA/HPA 做扩缩容；使用 ConfigMap/Secret 管理配置与密钥。

## 7. 下一步计划
详见 `TODO.md`，重点包括：
- 引入真实数据库/缓存实现（PostgreSQL + Redis）；
- 集成真实消息队列，完善 Scheduler↔Runner 全链路；
- Flag 提交插件框架与外部服务连接；
- Runner 注册/心跳协议，支持动态容量管理；
- 更完善的日志/指标/追踪体系；
- Web UI 实时刷新（WebSocket/SSE）与权限体系；
- 自动化测试、CI/CD、Kubernetes 部署方案。
