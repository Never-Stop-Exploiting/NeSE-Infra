# AwD Infra TODO

## 已完成
- **架构设计**：明确 Control Plane、Scheduler、Runner、Flag Submitter、Storage、Observability 模块职责；采用 PostgreSQL + Redis（规划阶段）、对象存储用于日志。
- **Go 服务骨架**：Control Plane、Scheduler、Runner、Flag Submitter 四个服务均已搭建，使用共享配置、日志、队列、存储接口。
- **Runner 功能**：支持 chroot `/env` 环境下执行 `./run.sh ip port`，控制并发、超时、输出采集，并从 flag 文件或 stdout 正则提取 flag。
- **Job Payload**：定义统一的作业协议（Base64 artifact、timeout、flag 文件/正则），Scheduler/Runner 共用。
- **内存仓库**：提供挑战、目标、Exploit、Run、Flag、Runner、Log 的读写操作，并注入示例数据供 UI 演示。
- **Redis 队列 Broker**：实现 Redis Streams 支持，Scheduler/Runner/Flag Submitter 默认使用，可在连接失败时降级到 Noop。
- **REST API**：`/api/v1` 下实现 targets、exploits、flags、runs、logs、runners 等 CRUD/操作端点（含导入 targets、上传 exploit revision、flag 重试等）。
- **Web UI**：基于 Vue3 + Vite（pnpm 管理），包含总览、Targets、Exploits、Flags、Runs、Runners、Logs 页面，支持上传/开启/调整 Exploit、批量导入目标、查看 flag/log/run 状态等；提供 dev/prod 反向代理配置。
- **Exploit 运行约定增强**：Control Plane 支持记录 entrypoint / run script，Runner 若缺失 `run.sh` 会自动生成，满足“文件 a + 默认 run.sh” 的约定。
- **Flag Submitter**：基于 Redis 队列 + HTTP 提交（默认对接测试 flagserver），记录 SubmissionAttempt 与日志，Control Plane requeue 会立即入队。
- **Docker 化**：各服务 Dockerfile、docker-compose 编排，以及 Web 前端生产 Nginx 镜像。
- **测试**：新增 jobs payload、Runner workspace/flag 提取等单元测试可在 Go 1.21 容器中运行。

## 约定
- **API 前缀**：所有前端请求均走 `/api/v1/**`，Nginx/Vite 统一转发到 Control Plane。
- **Runner 执行约定**：
  - Env：所有 exploit 在 `chroot /env` 下执行，临时目录 `/env/tmp/<run-id>`。
  - 入口：运行 `./run.sh <ip> <port>`；exp 负责依赖隔离。
  - Flag 来源：优先读取 `flag` 文件（可配置），否则按正则从 stdout 匹配。
- **Job 消息结构**：RunID、Exploit/Target 信息、Base64 ZIP、超时、Flag regex/file。
- **日志结构**：LogEntry 包含 level/source/message/runId/flagId/fields，可按筛选条件分页；UI 默认 100 条。
- **队列接口**：Scheduler/Runner/Submitter 依赖抽象 `queue.Broker`，默认使用 Redis Streams（`queue/redis`），可切换为其他实现。
- **配置**：使用 env（`.env.example`）配置数据库、Redis、Runner 选项、Flag 提交策略；gofmt/Go 1.21 作为默认工具链。
- **前端**：Pinia 管理状态、Axios 统一客户端、表格/表单使用暗色主题；依赖 pnpm 安装，开发端口 5173。

## 待办 / 下一步
1. **持久化实现**：将内存仓库替换为 PostgreSQL/Redis 版本，实现 targets/exploits/runs/logs 等真实读写。 
2. **Scheduler/Runner 集成**：在 Redis 队列上实现完整任务生命周期（重试、超时、死信等），完善 RunWorkItem 生成、并发调度与结果回写。
3. **Flag Submitter 插件**：实现提交插件加载机制（Go plugin / gRPC / HTTP），补全 Flag 提交状态流转。
4. **日志/指标收集**：接入 Loki/Prometheus/OpenTelemetry，完善日志落地与指标暴露。
5. **认证授权**：在 Control Plane/API/Web UI 引入身份验证、角色权限与审计。
6. **测试覆盖**：增加后端 handler、仓储、调度逻辑单测；提供 e2e 测试验证 Frontend ↔ API ↔ Runner 全链路。
7. **运行器注册协议**：实现 Runner WebSocket 注册、心跳、容量更新与下线处理。
8. **Artifact 存储**：引入对象存储服务（MinIO/S3）保存 exploit revision、stdout/stderr logs，并通过 Control Plane 提供下载。
9. **前端实时性**：通过 WebSocket/SSE 实时刷新 runs、flags、日志；优化 UI 表格/筛选、支持导出。
10. **部署完善**：提供 Kubernetes manifests/Helm chart、CI/CD 流水线、环境配置文档等。

