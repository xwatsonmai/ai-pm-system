# 工作台导航与项目配置

工作台使用统一的页面标识。菜单展示、页面直达和服务端页面权限引用相同的标识；折叠菜单只负责组织页面，不授予子页权限。

| 分组 | 页面标识 | 页面 |
| --- | --- | --- |
| 项目概览 | `overview` | 项目概览 |
| 项目需求 | `tasks`、`reviews`、`versions`、`timeline` | 需求池、验收与交付、项目版本、项目时间线 |
| 项目协作 | `chat`、`code` | 项目问答、代码与变更 |
| 项目管理 | `settings`、`git`、`access` | 项目设置、Git 设置、角色与权限 |

「我的 MCP」是个人入口；「平台设置」仍使用独立的平台管理员身份，不由项目角色授予。

需求池默认折叠已有的需求、关联 Bug 和追加迭代关系，并保留平铺切换。层级展示、筛选上下文与分层分页规则见 [需求池层级展示](requirement-hierarchy.md)。

## 导航

项目需求与项目管理支持展开、折叠。折叠偏好按当前用户、项目保存在浏览器中；直接进入某页时展开其所在分组。没有可访问子页的分组不显示。导航按钮支持键盘操作，折叠按钮通过 `aria-expanded`、`aria-controls` 标识状态。

地址中的 `page` 与 `project` 保存当前位置，例如 `/?page=git&project=<项目ID>`。刷新、前进、后退恢复对应页面，并保留部署子路径。既有 `?task=<需求ID>` 链接继续打开需求详情。无权页面不能通过地址访问，工作台回到第一个获准页面；服务端仍逐请求校验，不以菜单是否隐藏作为授权依据。

## 独立配置

- 项目设置：名称和说明，通过 `PATCH /api/projects/{id}/settings` 保存。
- Git 设置：仓库、分支、认证和源码阅读配置，通过 `PATCH /api/projects/{id}/git` 保存。密钥和 Token 不回显，留空保留现有凭据。
- 角色与权限：角色、页面/操作授权、成员和待办职责。页面只决定可见范围，提需求、Bug、协查和启动 Agent 等发起能力单独勾选。角色列表与成员列表分别分页，成员角色选择支持搜索、翻页并保留跨页选择。

编辑一页时只发送其负责的字段；服务端从当前数据合并并核对并发修订，避免其他配置被清空。原项目创建入口仍可以一次填写项目和仓库资料。

## 本地验证

执行 `make test`，再构建用于隔离检查的二进制：

```sh
go build -o bin/ai-pm-overall-check ./cmd/server
AI_PM_QA_BIN=bin/ai-pm-overall-check \
AI_PM_QA_NODE_MODULES=<含Playwright的node_modules目录> \
node internal/web/overall-optimization.browser-check.mjs
```

浏览器检查使用 loopback 服务、临时 SQLite 和独立的管理员/普通成员会话，覆盖配置保存、角色维护、成员职责、菜单与直达限制、分页和手机宽度。截图与结果写入 `test-results/overall-optimization/`，不包含线上资料。分页增量刷新与多版本另有专项回归。测试不会执行部署。
