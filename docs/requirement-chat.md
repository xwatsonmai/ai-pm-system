# 需求聊天室

一个需求使用同一份评审消息和同一个 Agent 会话，贯穿需求评审、开发、提测、产品测试、上线和验收。需求详情的「问 Agent」进入原有对话，原来的需求稿、确认、图片、成员发言与 @ 提及继续保留。需求变更不创建另一间聊天室，也不更换 Agent 会话。

## 交互与样式

沿用现有白色工作区、紫色操作色、成员头像、消息气泡、居中的系统提醒、原文抽屉和输入区。顶部显示需求当前阶段与修订；正式评审时保留原有话题和确认卡片，评审结束后的答疑不显示新的正式评审表单。继续使用现有菜单、抽屉切换、消息定位和草稿恢复，不增加独立聊天列表或另一套页面样式。

真实的认领、开发汇报、提测、复测、测试通过、上线和验收动作以系统提醒插入时间线。提醒来自已提交的事件，有稳定消息标识，旧需求也能读取历史事件；打开页面不会补写操作或改变任务。历史需求稿、来源快照和人工确认仍可回看，已关闭或已验收后也可继续问问题。

## 日常答疑与流程动作

评审阶段继续使用原有评审接口和确认流程；开发对齐仍由产品回复、原开发客户端确认完成。其他阶段的自由提问使用 `POST /api/tasks/{id}/requirement-review` 的 `action=discuss`。消息先保存，再由原 Agent 会话回答；失败可重试，重复 `request_id` 不重复追加消息。

答疑只允许只读查询和 `decision=answer`，服务端拒绝正式需求稿、驳回 Bug 和其他写工具。普通提问不修改需求正文、修订、状态、认领、提测或放行证据。已有提测版本时固定读取该分支的提测 SHA，不用主分支或开发分支后续尚未提测的代码解释已交付行为；版本变化会使旧回答失效，用户可重试。

确定要处理时，仍通过「提起 Bug」「变更当前需求」及原有确认表单执行。Agent 的建议或一句「转吧」不会冒充已经完成的操作。真正发起或确认变更后，原聊天室继续保存本轮评审与后续开发过程。

## 验证

```sh
make test
go build -o bin/ai-pm-requirements-check ./cmd/server
AI_PM_QA_NODE_MODULES=/path/to/node_modules node internal/web/requirement-discussion.browser-check.mjs
AI_PM_QA_NODE_MODULES=/path/to/node_modules node internal/web/requirement-review.browser-check.mjs
AI_PM_QA_NODE_MODULES=/path/to/node_modules node internal/web/testing-bugs.browser-check.mjs
AI_PM_QA_NODE_MODULES=/path/to/node_modules node internal/web/task-resolution.browser-check.mjs
```

浏览器验证使用临时 Git、SQLite、确定性本地评审 worker 和桌面/390px 浏览器。验证原会话延续、开发与提测系统提醒、图文答疑、响应丢失重试、固定代码版本、写权限隔离及显式变更确认，不连接生产数据或外部模型。数据沿用现有任务、评审和事件表，不增加数据库结构。
