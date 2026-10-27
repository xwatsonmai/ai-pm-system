# 首版架构与存储契约

## 分层

| 目录 | 职责 |
| --- | --- |
| `cmd/server` | 依赖装配、启动配置、定时同步与退出 |
| `internal/domain` | 项目、事项、提交、验收、证据、运行记录等领域类型 |
| `internal/app` | 权限、状态迁移、需求修订、幂等操作与业务校验 |
| `internal/attachment` | 附件内容校验、PDF/DOCX/文本提取和大小限制 |
| `internal/auth` | 统一登录配置、OAuth + PKCE、浏览器绑定和会话 |
| `internal/storage` | 与数据库无关的 Store 接口 |
| `internal/storage/sqlite` | SQL、驱动、建表、事务、查询和错误映射 |
| `internal/gitrepo` | 自有镜像、只读 Git 查询、分支观察、认证与连接测试 |
| `internal/credential` | 仓库秘密的 AES-GCM 加密与密钥文件管理 |
| `internal/agent` | 触发、排队、停止、运行记录、项目工具与 Pi 进程桥接 |
| `internal/server` | 本地 Web API 和开发 MCP 协议 |
| `internal/web` | 内嵌静态工作台 |
| `runtime` | Pi SDK 薄适配，不实现自己的模型/工具循环 |

## 状态与来源

任务有两种版本：`revision` 表示需求内容修订；`version` 用于并发写入比较。需求描述或验收条件变更会递增修订；普通汇报只更新并发版本。旧修订的交付必须重新核对要求再提交。

开发 Agent 认领后取得 `claim_id`。服务端同时验证 OAuth 授权、实时员工项目成员关系、认领主体与凭据。交接或关闭会使旧凭据失效。MCP 没有人工验收工具。

任务状态是应用服务校验后的事实；平台分析是独立记录，不通过自然语言猜测覆盖任务状态。验收绑定提交版本、需求修订和环境。测试、部署记录保留来源，属于调用者提交的证据，不等于平台已独立执行。

## SQLite 当前实现

为便于 MVP 迭代，领域对象主要使用 JSON 负载保存，保留项目 ID、类型和并发版本等必要索引列：

| 表 | 用途 |
| --- | --- |
| `documents` | 项目、模型元信息、分析运行、证据等类型化文档 |
| `tasks` | 事项负载、项目 ID、并发版本和认领凭据摘要 |
| `project_versions` | 发布计划、范围进度、不可改写的发布快照与变更历史 |
| `events` | 按单调序号追加的事项与仓库事件 |
| `receipts` | 请求指纹与成功响应，用于安全重试 |
| `secrets` | 模型密钥，不进入普通模型配置响应 |
| `tokens` | 仅本机免登录模式兼容的旧项目令牌 |
| `platform_admins` | 平台管理员成员 |
| `oauth_codes` | 5 分钟授权码摘要与 PKCE/客户端/回调/资源绑定 |
| `oauth_grants` | OAuth 授权、access/refresh 摘要与有效期 |
| `attachments` | 项目附件元信息 JSON、原文件 BLOB 和提取正文 |
| `login_attempts` | 有期限的 OAuth state 摘要、浏览器绑定与 PKCE verifier |
| `web_sessions` | 会话摘要、用户身份和过期时间 |
| `schema_migrations` | 数据库结构版本 |

SQLite 启用 WAL、忙等待与单写连接。业务层不查询 JSON 内部字段，不依赖 SQLite JSON 运算符、rowid 或自增实现细节。文档存储便于首版演进；后续有大量复杂查询时，可在适配器内部增加结构化列或拆表。

`CommitTask` 必须在一个事务中完成：检查请求回执、比较期望版本、保存事项、追加事件、保存响应。相同请求 ID/相同内容返回原响应；相同 ID/不同内容返回冲突。并发版本不一致时不能静默覆盖。

测试、部署、分析等证据不可覆写，同一个证据 ID 的相同重试返回原记录；Git 观察快照是明确允许更新的内部对象。事件增量读取最多返回 500 条，按游标继续读取；工作台上下文读取最近 500 条窗口。

## MySQL 替换路径

1. 新增 `internal/storage/mysql` 实现 `storage.Store`，不把数据库差异带入应用服务。
2. 使用 `BIGINT` 表达时间和事件序号，字符串 ID 建唯一索引；JSON 负载可用 JSON 或长文本保存。`secrets` 和令牌摘要仍分开处理。
3. 实现 MySQL 自己的迁移、事务及唯一键错误映射。通过 CAS 更新影响行数判断并发冲突；不能把冲突当作重试成功。
4. 将当前依赖 SQLite 的测试 setup 提炼为适配器工厂，使用相同的任务、幂等、证据、重启/重连、并发用例验证两种数据库。
5. 停写后导出/导入数据，保留原 ID、修订、事件序号、回执和令牌摘要；校验记录数量及关联关系后切换装配。Pi 会话与 Git 缓存不属于 SQL 迁移，分别保留。

当前只装配 SQLite。数据抽象已经存在；MySQL 驱动、适配器、迁移脚本与切换配置留待实际需要时实现。

统一登录的状态与会话由 `storage.Store` 持久化。`ConsumeLoginAttempt` 必须按 state 摘要、浏览器绑定和有效期进行原子读取/删除，MySQL 应使用事务和行锁实现相同的一次性语义；不可简单拆成无事务的先查询再删除。HTTP 认证中间件将会话用户转换为业务 Actor；MCP 通过个人 OAuth 授权并实时校验成员关系。

附件通过 `storage.Store.PutAttachment/GetAttachment` 持久化，应用层不直接访问文件表。MySQL 适配时原文件可使用 `LONGBLOB`，提取正文使用长文本。SQLite 结构版本 2 以新增表方式迁移，不改写已有业务数据。

## Pi SDK 的使用边界

固定依赖 `@earendil-works/pi-coding-agent` 1.1.0。使用 SDK 的 `createAgentSession`、`ModelRuntime`、`SessionManager`、流式事件和 `abort`。

Go 通过标准输入/输出 JSON 消息与 Node worker 通信。Go 负责持久化、项目作用域、工具权限和运行生命周期；Pi 负责模型调用、工具循环、会话恢复和上下文压缩。没有另建一套 Agent 推理循环。

问答先通过 `POST /api/projects/{id}/attachments` 上传单个 multipart `file`，再向运行接口提交 `attachment_ids`。Go 检查附件所属项目、数量和总量，运行记录只保存元信息。执行时读取正文或图片内容：文档附在提示词中并明确标为资料，图片通过 Pi 的 `session.prompt(text, {images})` 传入；图片不会仅作为文件名描述。自定义模型用 `supports_images` 声明视觉输入能力，内置模型按 Pi 模型目录判断。原文件下载与图片预览都检查项目范围；文档以下载方式返回，不直接执行上传内容。

平台按运行类型提供项目上下文、分析保存、协同和需求评审工具；`create_bug` 仅允许 Bug 初诊。已有项目使用 `repository_read` 查询 Git。项目启用隔离阅读后，每轮固定源码 SHA，在独立只读容器中使用 Pi 原生 `read/grep/find/ls`，Git 元信息和历史由 `repository_info/repository_history` 提供。业务工具仍由 Go 执行。不加载仓库扩展、外部技能、AGENTS 指令或通用 Shell/写文件工具。执行边界、启用与回退见 [项目源码只读沙盒](readonly-sandbox.md)。

每次模型运行最长 3 分钟，沙盒源码准备另限 2 分钟；原生与业务工具共用每轮 30 次预算，模型瞬时失败最多重试一次。连续事件按项目/事项合并 3 秒后触发，同一事项的后台分析串行；项目问答按会话串行。停止先调用 Pi 的 `abort`，2 秒后仍未退出则终止该 worker；沙盒 runner 回收对应容器。重启时将遗留运行标记为中断，保留已保存记录，用户可重新分析。

长期项目的全局检索、分层摘要和运行并发配额还需要在真实使用规模下进一步完善。当前上下文来自项目事项、最近事件、保存的证据和按需读取的代码。

## 仓库凭据与成员授权

SQLite 结构版本 4 新增 `repository_credentials`；用户目录以 documents 的 user 类型持久化。项目以稳定认证用户 ID 保存负责人和成员，凭据另存密文，通过 ID 引用。HTTP 路由与应用服务检查项目权限，独立事项/运行先解析其项目；MCP 保持员工与项目授权作用域。规则、迁移边界及 MySQL 适配要求见 [仓库与成员权限](repository-access.md)。

## 平台权限与个人 OAuth

SQLite 结构版本 5 增加平台管理员与 MCP OAuth 表，平台配置、个人开通状态和外部成员目录快照保存在 documents。外部登录扩展可以把目录成员与登录身份映射到同一稳定内部 ID，从而在成员首次登录前授权；内置账号直接以账号作为目录。SQLite 结构版本 13 新增 `accounts` 表保存内置账号，见 [登录方式](login.md)。项目 membership 保存 user_id 和 developer/product/admin 角色，旧 owner/access_members 作为迁移兼容读入。首次迁移将最近一个带模型选择的旧项目模型作为平台默认值，以后只读取平台设置。

OAuth 授权码通过所有绑定字段匹配后的 DELETE RETURNING 原子消费；refresh token 使用原摘要条件更新，成功后同时轮换 access/refresh，并发刷新只成功一次。关闭个人 MCP 在一个数据库事务内删除授权码及全部授权。客户端元数据不产生外部请求，注册接口忽略非必需验证字段。MySQL 适配必须保留这些一次性和事务语义。

## 当前里程碑与时间线

SQLite 结构版本 10 引入独立的 `project_versions`，版本 11 增加 `current_milestones` 和事件时间索引。数据模型允许多个版本，当前产品策略通过当前指针 CAS 仅开放一个进行中的里程碑，不使用数据库唯一活跃状态限制。需求创建/问答转需求、修订、评审确认与重开共用 `MilestoneBatch`，将必要的自动建版、归属、范围、事件、需求和回执放在同一事务。普通需求推进在原事务同步范围进度，发布快照始终不可变。迁移补齐缺失关联，保留原文、修订、状态、认领及原发布计划；非当前计划保留待开始。

时间线接口输出精简需求与版本摘要，按时间分页读取事件，不输出原始事件载荷。Git 轨道读取已同步镜像所有分支的真实提交，以已校验的完整 SHA 分支头固定翻页快照；GET 不拉取远程仓库。仅明确的开发提交/发布快照 SHA 建立需求关联，仓库读取失败单独报告。产品行为与 API 见 [当前里程碑与时间线](project-versions.md)。

## 需求迭代上下文

SQLite schema 12 新增 `requirement_iterations`，按项目、根需求和时间索引各轮关系。Task JSON 保存上一轮已验收快照和最初需求基线，快照剥离嵌套迭代字段，以固定深度保存；原评审、证据和历史通过父子关联读取。`CommitVersionPlan` 在同一事务校验起点状态及并发版本、写入新需求与关系索引、当前里程碑范围、历史事件和回执，失败整批回滚。原单保持已验收，不增加修订或恢复旧认领。行为见 [需求追加迭代](requirement-iterations.md)。
