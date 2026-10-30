# README 截图

这些图像于 2026-10-30 在本地演示环境中拍摄，用于展示真实界面的操作流程。图像未使用生成式图片替代界面，也未包含生产项目、真实员工、凭据或业务数据。

平台使用临时 SQLite 数据目录和本地确定性 Agent 协议回复。客户端使用独立的临时数据目录、本地假平台和假执行器。截图展示交互方式，不作为真实模型质量、线上排查结果或生产发布的证明。

| 图像 | 展示内容 |
| --- | --- |
| `review-direction.png` | 评审 Agent 提出方向性问题与待确认话题 |
| `review-answers.png` | 产品逐题回答、Agent 追问尚未填写的问题 |
| `review-proposal.png` | 话题完成后给出需求稿，等待产品确认 |
| `desktop-tasks.png` | 客户端读取工作列表并认领后的任务状态 |
| `desktop-plan.png` | 客户端中的方案与业务问题确认 |
| `collaboration-pending.png` | 问答 Agent 创建只读协查，等待开发方认领 |
| `collaboration-result.png` | 开发方通过 MCP 回传结果，Agent 在原会话补充回答 |
| `bug-proposal.png` | 根据用户反馈整理 Bug，等待确认 |
| `bug-created.png` | 确认后保存到需求池，提供事项入口 |
| `conversation-to-requirement.png` | 从产品讨论整理迭代需求与验收建议 |

## 更新截图

保留真实界面和有意义的示例对话，避免截取加载中、报错、敏感信息或与说明不一致的状态。修改对应流程后，应重拍并检查图文是否一致。

- 平台多轮评审可参考 [`internal/web/requirement-review.browser-check.mjs`](../../internal/web/requirement-review.browser-check.mjs)，用本地模型协议 fixture 演示提问、回答、需求稿和确认。
- 对话转需求可参考 [`internal/web/requirement-intake.browser-check.mjs`](../../internal/web/requirement-intake.browser-check.mjs)。本次展示的对话使用同样的本地隔离方式，并以简短业务示例完成建议与确认。
- 协查示例经过真实本地 API 创建，通过开发 MCP 认领、汇报并提交结果，等待原会话自动续答后拍摄。
- 客户端用 [ai-pm-desktop 的演示辅助工具](https://github.com/xwatsonmai/ai-pm-desktop/blob/main/test/helpers.ts)准备两个示例需求，再在实际 Electron 窗口中拍摄任务列表和方案确认页。

图片路径在主 README 中使用相对路径，以便 GitHub 和本地文档预览都能读取。
