## 修改建议

1. 首先challenge没有管理，需要增加challenge的CRUD接口和UI页面。需要先创建challenge，之后可以在challenge添加多个target和exploit。

2. exp创建逻辑不对，可以在challenge管理里面创建exp，也可以在外部，下拉菜单选择对应的challenge进行创建。创建的时候，需要上传附件和填写run.sh脚本内容。

3. Flag requeue貌似没有起作用，应该要看到attempt更新，以及log里面有重新提交的记录，可以在同一个logs中append新的log。

## 修改建议

1. exp的约定有两个文件，其中一个是文件a，另外一个是run.sh，是个run.sh具有默认模版，不修改可以按照默认。文件a是一个占位符，可以是zip也可以是py文件。举个例子，我上传一个文件a是exp.py，然后run.sh默认就是执行`python exp.py $1 $2`，如果用户不想用默认的run.sh，可以提供一个窗口修改run.sh的内容。

Runner是一个Docker Template，是一个已有的基础，当一个机子启动Runner的时候可以这样
```
FROM python-3.12:xxx AS base

RUN pip install pwntools

FROM runner-base:latest

COPY --from=base / /env
```
这样可以创建一个新的具有具体能力的Runner
随后可以

```
service:
  runner-xyz:
    build:
      context: .
      dockerfile: Dockerfile.runner-xyz
    environment:
      - RUNNER_ID=xyz
      - RUNNER_TAGS=tag1,tag2
      - CONTROL_PANEL_URL=http://control-panel:8000
      ...
    ...
```
RUNNER_TAGS就可以标明这个runner可以跑哪些类型的exp（比如pwntools、web-requests这些标签，这些标签能自动注册到服务器，exp可以选择对应的）

随后Runner需要从control-panel拉取exp和target进行执行。（control-panel需要提供下载exp的接口）
(exp的组成是，一个是文件a，另外一个是run.sh脚本），runner拉取下来之后，缓存，当接收到ip port需求，就解压到/env/tmp/独立的文件夹内，然后chroot /env sh -c "cd /tmp/xxxx && ./run.sh ip port"执行脚本运行exp（文件a是多种的，可以是exp.py也可以是exp.zip，这个不需要infra考虑，这个在run.sh会对这个做解析）

## 最新实现简述

- 新增 `docker/runner-base.Dockerfile` 作为 Runner 的基础镜像模版，默认打包 Runner 二进制并准备好 `/env` chroot 目录，业务 Runner 可以按上述方式 `FROM runner-base:latest` 继续扩展语言运行环境。
- 后端提供 `/api/v1/runners/heartbeat` 接口，Runner 使用 `RUNNER_SECRET` 周期性汇报状态、标签（`RUNNER_TAGS`）、并行能力以及活跃任务数；首次心跳若尚未登记会自动创建 Runner 记录。
- Runner 服务新增 `CONTROL_PANEL_URL`、`RUNNER_TAGS` 等配置，会在启动时自动向 Control Panel 注册自身，同时心跳失败会记录日志。
- 控制台 Runner 页面展示 Runner 标签与活动任务，便于按照标签调度能力。
- Exploit 列表新增「Upload Artifact」「Edit run.sh」分离操作，run.sh 可在前端弹窗里直接编辑或单独上传文本文件。
