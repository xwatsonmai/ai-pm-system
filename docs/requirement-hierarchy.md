# 需求池层级展示

需求池默认按已有关系折叠展示，保留「平铺展示」切换。层级与版本分组互相独立：可以按版本分组查看层级，也可以取消版本分组。

列表以需求标题为主要阅读入口。所有顶层需求使用相同字号、字重、间距和分隔线，不因是否包含子项而增加卡片、底色、边框或标题强调。展开后，仅通过连续树线、缩进和关系标签表达归属；追加迭代的子项继续嵌套在该迭代下。

关系只标记一次「关联 Bug」或「追加迭代」，状态、负责人和更新时间退到次要列。跨版本子项保留自己的版本名称。手机上内容纵向排布，标题、状态和负责人无需通过横向滚动才能阅读。展开与收起支持键盘操作；减少动态效果偏好会关闭转动与加载动画，未变化的轮询不会重新播放动画。

- 需求的关联测试、验收 Bug 挂在 `parent_requirement_id` 指向的需求下，标记「关联 Bug」。
- 追加迭代挂在 `iteration.parent_task_id` 指向的上一轮需求下，标记「追加迭代」。迭代自己的关联 Bug 可以继续展开。
- 没有上述关系的需求、独立 Bug 各自作为顶层条目。不会根据标题、描述或 Agent 推断关系；本次不增加手工父子关系编辑。
- 展开偏好按用户、项目保存在浏览器。只请求展开分支，轮询按业务 ID 更新行，保留展开状态和子项页码。

## 筛选与分页

顶层按需求组分页，默认每页 20 组；每个展开分支的直接子项独立按 20 项分页。只有子项超过一页时才显示该分支的分页控件，控件紧邻分支内容。页脚分别显示匹配条目数与顶层组数，避免把分页后的局部列表误认为完整关系树。

搜索、状态、类型和目标版本筛选会保留命中条目的祖先路径，未命中的祖先标记为「父级上下文」。筛选时自动展开顶层和上下文祖先，最多自动展开 8 层；更深层仍可手动展开。关联条目可能跨版本，祖先和子项分别显示自己的实际版本，筛选不会修改版本归属。

顶层及同级分支按其命中后代的最近更新时间排序，再按 ID 排序。根分页的总数是匹配的需求组数；每层总数是该层保留的直接条目数。末页失效时返回当前最后一页。

日常列表不显示选择框，也不预留选择列。点击「安排目标版本」后，在弹窗内选择可调整的需求；选单在服务端先筛选可调整状态再分页，跨页保留勾选和草稿。取消或完成后回到普通列表。批量安排使用弹窗中明确选择的 ID，沿用服务端权限与并发校验；需求池的浏览筛选不会隐式选中任务。

## 查询接口

```text
GET /api/projects/{id}/tasks?view=tree&page=1&page_size=20
GET /api/projects/{id}/tasks?view=tree&tree_parent_id={taskID}&page=1&page_size=20
```

`tree_parent_id` 留空查询顶层；非空查询指定节点的直接子项。两类查询使用一致的 `q`、`status`、`kind`、`project_version_id` 筛选。接口继续使用原需求池的页面授权，不增加访问能力。

响应保留列表投影、`requirement_reviews`、`version_options`，不读取完整正文、附件和冻结的迭代原文。分页字段仍为 `items`、`total`、`page`、`page_size`，另有 `view: "tree"` 和整个项目当前筛选命中的 `matching_total`。每个条目增加：

| 字段 | 含义 |
| --- | --- |
| `tree_parent_id` | 本层级中的父 ID，顶层为空 |
| `tree_relation` | `bug` 或 `iteration`，顶层为空 |
| `matches_filter` | 本条目是否直接命中筛选 |
| `children_count` | 筛选后保留的直接子项数 |
| `matching_count` | 含自身的分支命中条目数 |
| `pending_count` | 含自身的分支命中条目中待确认的数量 |

不带 `view=tree` 的分页接口及原数组接口保持兼容。SQLite 查询在读事务中完成计数和分页，所有用户参数使用绑定值，不增加数据库迁移。失效或跨项目父引用按孤立顶层条目处理，跨项目父查询返回未找到；异常循环关系会在最小 ID 节点切断一条父边，避免无限递归和重复展示。

## 隔离回归

```sh
make test
go build -o bin/ai-pm-hierarchy-check ./cmd/server
AI_PM_QA_NODE_MODULES=<含Playwright的node_modules目录> \
node internal/web/task-hierarchy.browser-check.mjs
AI_PM_QA_BIN=bin/ai-pm-hierarchy-check \
AI_PM_QA_NODE_MODULES=<含Playwright的node_modules目录> \
node internal/web/requirement-pool.browser-check.mjs
```

专项检查启动临时 SQLite 和 loopback 服务，覆盖 73 项、26 个顶层组、46 个直接子项的三页遍历、嵌套关系、筛选路径、列表无常驻选择框、安排版本弹窗的跨页选择、无变化轮询零 DOM 改动、变化行保留身份、过期响应隔离、键盘展开和 390px 布局。截图、报告写入忽略的 `test-results/task-hierarchy/`，不访问生产、模型或真实推送渠道。

同时运行原有 `pagination.browser-check.mjs`、`project-versions.browser-check.mjs`、`requirement-iterations.browser-check.mjs` 验证平铺兼容、版本调整、追加迭代、历史分页和图文评审。

界面专项使用两个带关联事项的主需求、一层追加迭代和独立条目，检查顶层需求的视觉权重一致、子项确实位于对应父级容器、树线可见、跨版本名称保留、标题占据主要宽度、键盘打开与安排版本弹窗选择，以及 1600px、1024px、390px 下的布局。折叠、展开和平铺截图写入 `test-results/requirement-pool/`，视觉检查应同时查看完整桌面页和手机页，不能用接口测试代替。
