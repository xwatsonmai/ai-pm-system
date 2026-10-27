# 需求测试与关联 Bug

开发提交后，需求与独立平台 Bug 进入 `testing`（测试中）。产品在实际测试环境确认通过后进入 `pending_release`（待上线）；上线后的业务验收继续独立保留。历史待上线、已上线和已验收记录保持原状态，不补造历史测试结果。独立平台 Bug 测试失败时，产品可用「修复无效，退回 Bug」退回该 Bug 修复；需求测试失败仍必须提起关联 Bug。

## 需求与 Bug 的关系

需求的测试流程：`doing → testing → fixing → retest → testing → pending_release → review → done`。

- `testing` 为测试中；需求详情提供「提起 Bug」和「确认测试通过」。测试缺陷不能通过 `rework` 直接退回原需求。
- 从需求提起的 Bug 保存 `parent_requirement_id` 和 `bug_context`。一个 Bug 只归属同项目的一条需求，创建后不能换绑或改成需求；独立平台 Bug 不填写归属，沿用原创建方式。
- 提 Bug 时原子保存关联并使需求进入 `fixing`（待修复）。待修复和待复测仍可提起其他 Bug，以便继续登记问题。
- 关联 Bug 的待评审、评审失败、待补充、驳回待确认、待认领、处理中和待验证均阻塞需求；只有 `resolved`（产品验证已解决）或产品明确结束的 Bug 才解除阻塞。
- 全部关联 Bug 解决或关闭后，需求进入 `retest`（待复测），不会自动开始测试或自动通过。产品明确选择包含全部修复的实际代码版本与环境，再点击「开始复测」。
- Bug 的创建、重开、解决与需求状态同步在同一 SQLite 事务提交；测试放行与新建 Bug 互斥，过期表单不能覆盖新状态。有关联未结 Bug 时不能直接结束需求，需先处理各 Bug。

## 图文录入和评审

「提起 Bug」复用富文本编辑器，支持文字、正文图片、粘贴截图、拖拽附件、浏览器草稿与幂等重试。每条需求的 Bug 新建草稿单独保存，不与普通新建需求混用。保存成功后后台评审；模型失败不撤销 Bug，也不解除阻塞。

`bug_context` 固定提起时的需求修订、原文、验收条件、开发分支及提测提交 SHA。提交结果时需登记实际开发分支；评审前确认指定 SHA 可读取且属于该分支。隔离源码和原有 Git 查询两种模式均读取固定 SHA，不能回退到 `main` 或项目默认分支。代码未推送、仓库未同步或分支不存在时显示评审失败，修复后可重试。

Agent 的结论有三条路径：

1. `ready`：确认问题存在且范围明确，保存修复说明与验收条件，自动进入 `ready`（待认领），不再要求产品重复确认成立。
2. `clarify`：细节不足，生成可选填写表单；产品可直接输入文字、粘贴图片或纠正理解，继续评审。
3. `reject_bug`：有依据表明不存在问题或符合原设计，评审状态为 `rejection_pending`（驳回待确认）。产品同意后通过 `confirm_rejection` 正式关闭；不同意可自由补充后重新评审。Agent 不能自行关闭。

旧轮次、旧修订及已开始修复的 Bug 不接受过期评审结果；补充后旧驳回确认入口失效。

## 修复与复测

关联 Bug 复用现有待认领、开发对齐和开发提交接口。客户端方案分析与实际修复工作区均使用 `bug_context` 的固定提测 SHA。新工作区从该 SHA 创建；已有工作区必须包含该 SHA，发现原先从主分支错误创建的工作区时阻塞并保留现场，不自动重置。开发应基于 `bug_context` 的需求代码工作，修复需合入所属需求开发分支；提交修复后进入 `verifying`（待验证）。产品实际验证后调用 `verify_bug`，记为 `resolved`；无效则用 `return_bug` 填写原因退回这个 Bug 的开发。已解决的 Bug 在需求仍处于测试流程时，也可退回修复并重新阻塞需求。

产品确认解决与测试通过记录保存 `revision / sha / environment / note / actor / at`，开发或平台 Agent 无权代填确认。开始需求复测时，服务端检查实际 SHA 属于需求分支，并包含全部已验证修复的提交；不能拿未合入修复的旧版本复测。当前实现按 Git 提交祖先关系核验，修复应保留提交关系合入；squash 或 cherry-pick 后须重新提交并验证对应的新 SHA。

单条上线和里程碑批量上线都只能消费待上线需求。关联 Bug 随需求整体验证、发布，不单独走生产上线和业务验收，也不把“已解决”算作生产已发布。

## 接口与客户端

- `POST /api/projects/{id}/tasks` 新增 `parent_requirement_id / parent_version`，仅工作台人类用户可创建关联测试 Bug；关联信息由服务端从需求读取。
- `POST /api/tasks/{id}/actions` 新增 `test_pass / verify_bug / return_bug / start_retest`。必须携带最新 `version` 和唯一 `request_id`；除退回 Bug 外还需 `test_verification` 的 `revision / sha / environment / note`。
- `POST /api/tasks/{id}/requirement-review` 增加 `confirm_rejection`，需当前 `generation`，与普通评审一样支持幂等重试。
- MCP `task_submit_result` 保持原参数，返回状态变化为 `testing` 或关联 Bug 的 `verifying`。产品测试确认仍通过工作台执行，MCP 工具不会自动授予部署或产品确认权限。
- 客户端需识别 `testing / fixing / retest / verifying / resolved`，停止占用已经完成的开发执行槽位，保留原会话及认领映射，在产品验证退回 Bug 后恢复对应开发会话。只有待上线项进入原发布处理。

## 验证

运行 `make test`，以及：

```sh
go build -o bin/ai-pm-requirements-check ./cmd/server
AI_PM_QA_NODE_MODULES=/path/to/node_modules node internal/web/testing-bugs.browser-check.mjs
AI_PM_QA_NODE_MODULES=/path/to/node_modules node internal/web/release.browser-check.mjs
```

浏览器专项使用临时仓库、数据库与本地确定性评审 worker，验证粘贴图片、分支基线、评审表单、产品确认驳回、修复验证、关联 Bug 阻塞和复测闭环；不使用生产项目或付费模型。
