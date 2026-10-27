# 项目源码只读沙盒

本功能由项目设置中的「代码阅读方式」显式启用。已有项目保持原有 Git 查询；选择隔离阅读后，每次运行必须成功启动独立容器，任何准备或连接错误都会结束该运行，不会静默回退。实现、容器验收和正式部署分别记录。

## 数据与版本

Git 仍由平台使用原有项目凭据 clone/fetch 到裸仓库。每轮分析开始时将请求的 `code_ref`、项目 `agent_ref` 或默认分支（按此优先级）解析为完整 SHA。`code_ref` 由 `POST /api/projects/{id}/runs` 接收；项目的分析代码版本也可直接在设置中填写。每轮固定 SHA，新轮默认重新解析；上下文明确指出以前轮次的文件证据属于旧版本。

快照在 `data/snapshots/<project>/<sha>/source`，清单和最后使用标记在相邻目录。使用 Git tree 和 blob 逐个展开受跟踪文件，不经过 checkout filter、hooks 或 archive 的 export-ignore/export-subst。临时构建完成后原子发布，缓存最多 100,000 个条目、1 GiB 文件内容；失败不会发布半成品。默认维护周期清理超过七天且没有运行持有的快照，关闭定时维护时不自动清理。

`.git` 不进入快照。真实 gitlink 子模块记录路径和提交，第一版不递归拉取；LFS 指针、越界符号链接明确记录为阅读限制。普通服务目录完整展开。仓库内部相对链接保留，越界链接不展开。源码不进行依赖安装、构建或执行。

## 执行边界

Go 经受限 Unix socket 请求宿主机 runner，只传项目、会话、运行和快照标识。runner 自行计算挂载目录、校验清单和真实路径，使用管理员固定的镜像；请求不能传命令、镜像、挂载路径或 Docker 参数。socket 只对平台服务账号组开放，Worker 没有 socket 或 Docker 控制权。

- 每个运行一个独立容器；同项目可以并行读取不同 SHA。
- `/workspace` 仅挂当前快照且只读；根文件系统只读，非 root，移除 capabilities，启用 no-new-privileges。
- `/state` 是 64 MiB 临时目录，只有 `/state/sessions` 挂载当前会话已有的 Pi sessions 目录；无需复制旧会话。不会挂载其他会话、平台数据库、Git 凭据或认证配置。
- 模型配置和本次输入通过 stdin 传输。模型网络连接沿用平台配置；容器使用独立网络命名空间，不提供 Shell 工具。这是文件和执行隔离，并非断网环境。
- 默认上限：4 个容器并发，每个 1 CPU、1 GiB 内存、128 个进程、35 分钟 runner 生命周期。源码准备单独限时 2 分钟；模型每次请求首次内容和流式无响应分别限时 3 分钟，有新增内容会刷新后者；平台单次运行总时限为 30 分钟。若调大平台的 `-agent-total-timeout`，须同时把 runner 的 `-worker-timeout` 调至更大值，留出启动和清理时间。
- 完成、停止、超时和连接断开均回收容器。runner 重启时仅回收属于当前 data root 的遗留容器。

Pi 原生 `read/grep/find/ls` 保留原 schema、分页/搜索实现与截断提示。薄适配将所有输入解析为 `/workspace` 内的真实路径，再将规范化路径传给 Pi，阻止绝对路径、`..`、符号链接访问状态目录或其他项目。`cwd` 本身不是权限边界。镜像预装 `rg` 和 `fd`。

## 工具与证据

隔离模式不向模型提供 `repository_read`，文件阅读使用四个 Pi 原生工具。`repository_info` 提供固定 SHA、同步时间、真实子模块和未展开项；`repository_history` 仅支持针对本轮 SHA 的 log/diff，保留 Git 历史能力。

项目事实、保存分析、协同和需求评审工具继续由 Go 执行，维持原有身份和流程控制。`create_bug` 仅在 triage 模式出现。保存代码分析时，SHA 由平台绑定，模型不能覆盖。原生调用与业务调用共用每轮 30 次预算，读取路径、行范围、SHA、截断元信息和错误写入运行步骤；不在步骤中复制完整源码或图片。

无仓库项目仍可以在沙盒中问答和评审业务资料，但不开放文件阅读工具。仓库未同步、指定版本不存在、runner 不可用都有明确错误，不替换成其他版本。

## 安装与启用（需单独发布）

1. 用 `deploy/package.sh` 打包；包中包含 Go 服务、Linux runner、两个 Dockerfile、Compose override 和 systemd unit。
2. 在发布目录构建应用镜像与 Worker 镜像：`docker build -f Worker.Dockerfile -t ai-pm-worker:<release> .`。Worker 构建必须通过镜像中的全部 Node 测试，包含原生 grep/find。依赖按 lockfile 安装；离线环境可由构建系统提供同一 lockfile、经完整性校验的 npm 缓存。
3. 安装 `ai-pm-sandbox-runner` 到 `/usr/local/bin`。在 `/etc/ai-pm-sandbox.env` 设置 `AI_PM_WORKER_IMAGE` 为固定发布镜像（推荐镜像 digest）。安装 `ai-pm-sandbox.service`。示例 unit 的 Group=1000 对应当前应用 UID/GID；不同部署需同步修改。
4. 使用 `compose.yaml` 和 `compose.sandbox.yaml` 启动应用。后者仅挂 runner socket 目录并设置 `-sandbox-socket`，不会挂 Docker socket。应用和 runner 的 data root 必须指向同一个宿主机数据目录。
5. 在一个测试项目选择「隔离源码阅读」，执行下述验收后再逐步启用其他项目。

本地 macOS 开发使用 Docker Desktop；runner 支持 `-worker-uid`、`-worker-gid` 指定当前非 root 用户以匹配本地绑定目录的所有权。生产默认保持 1000:1000。Docker 不可用时不绕过沙盒。

回退先将项目显式改为「原有 Git 查询」并等待/停止运行，再切回旧应用；新字段均为兼容 JSON 扩展，原数据库和 Pi 会话继续保留。删除快照不删除历史对话或分析结果。

## 验证

常规 `make test` 验证 Git 快照、旧工具兼容、业务权限、原生工具边界和前端。宿主缺少 fd 时仅本机 grep/find 组合用例跳过，Worker 镜像内预装 rg/fd，此用例必须执行。

真实容器验收：

```sh
docker build -f deploy/Worker.Dockerfile -t ai-pm-sandbox:local-check .
AI_PM_SANDBOX_IMAGE=ai-pm-sandbox:local-check go test -race -timeout 180s ./internal/agent -run TestDockerSandboxEndToEnd -v
```

该用例使用真实 Git、SQLite、Pi SDK、Docker 和本地模型测试桩，覆盖原生工具发现/读取、私有状态拒绝、只读挂载、业务分析保存、模型伪造 SHA 拒绝、跨轮版本切换、固定旧提交、会话恢复、取消与容器回收，以及无仓库项目继续问答。Docker Desktop 默认通过 host.docker.internal 访问模型测试桩；Linux 可通过 `AI_PM_DOCKER_HOST` 指定宿主机在 Docker 网络内可达的地址。

浏览器验证使用本地临时数据库，真实保存项目设置，模拟已完成运行检查版本与工具记录展示，再向真实 API 验证缺少 runner 时的错误。需要可加载的 Playwright（可用 `AI_PM_QA_NODE_MODULES` 指向其 node_modules）：

```sh
go build -o bin/ai-pm-sandbox-check ./cmd/server
node internal/web/sandbox.browser-check.mjs
```

截图和报告保存在忽略目录 `test-results/sandbox-ui/`。

自然语言判断效果仍需独立的真实模型回放；测试桩验收不代表真实模型不会误判，更不代表业务故障修复或生产发布。
