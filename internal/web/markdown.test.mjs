import test from "node:test";
import assert from "node:assert/strict";
import { markdownHTML, documentMarkdown } from "./static/markdown.mjs";
import { mergeRunRefresh } from "./static/dom-update.mjs";

test('imported requirement line breaks render as a document without rewriting code escapes', () => {
  const source = '## 目标\\n允许恢复旧轮质检\\n\\n## 背景\\n- 保留历史\\n- 重新评审';
  const html = markdownHTML(documentMarkdown(source));
  assert.ok(html.includes('<h2>目标</h2>'));
  assert.ok(html.includes('<h2>背景</h2>'));
  assert.ok(html.includes('<li>保留历史</li>'));
  const code = '## 示例\\n\\n`String.raw` 和 `C:\\new\\notes`\n\n```js\nconst newline = "\\n";\n```';
  const normalized = documentMarkdown(code);
  assert.ok(normalized.includes('`C:\\new\\notes`'));
  assert.ok(normalized.includes('const newline = "\\n";'));
  for (const ordinary of ['C:\\new\\notes', '正则中的 \\n 表示换行', '## 正常标题\n\n正文', '```js\nconst x="\\n\\n";\n```']) assert.equal(documentMarkdown(ordinary),ordinary);
});

test("agent Markdown renders headings, emphasis, lists, code and tables", () => {
  const html = markdownHTML("# 项目简介\n\n**项目**：`mall_server`\n\n- 服务端\n- 客户端\n\n```js\nconst name = '<项目>';\n```\n\n| 模块 | 状态 |\n| --- | --- |\n| server | 已同步 |\n\n[仓库](https://example.test/repo)");
  for (const fragment of ["<h1>项目简介</h1>", "<strong>项目</strong>", "<code>mall_server</code>", "<ul>", "<li>服务端</li>", '<code class="language-js">', "&lt;项目&gt;", "<table>", 'href="https://example.test/repo"']) {
    assert.ok(html.includes(fragment), fragment);
  }
});

test("streamed Markdown tolerates unfinished fences and escaped raw HTML", () => {
  const html = markdownHTML("\uFEFF## 分析\n\n```html\n<script>alert(1)</script>");
  assert.ok(html.includes("<h2>分析</h2>"));
  assert.ok(html.includes("&lt;script&gt;alert(1)&lt;/script&gt;"));
  assert.ok(!markdownHTML('<img src=x onerror="alert(1)">').includes("<img"));
});

test("external refresh retains newer streamed output and newly submitted runs", () => {
  const before = { id: "one", output: "开始", status: "running" };
  const latest = { ...before, output: "开始分析，已经完成", status: "succeeded" };
  const submitted = { id: "two", output: "", status: "queued" };
  const incoming = { ...before, output: "开始分析" };
  assert.deepEqual(mergeRunRefresh([incoming], [latest, submitted], new Map([[before.id, before]])), [latest, submitted]);
  assert.deepEqual(mergeRunRefresh([incoming], [before], new Map([[before.id, before]])), [incoming]);
});

test("a checkpoint cannot roll back live text while the model pauses", () => {
  const live = { id: "one", status: "running", output: "完整的实时文本" };
  const checkpoint = { ...live, output: "完整", conversation_title: "新标题" };
  const before = new Map([[live.id, live]]);
  assert.deepEqual(mergeRunRefresh([checkpoint], [live], before), [{ ...checkpoint, output: live.output }]);
  assert.deepEqual(mergeRunRefresh([{ ...checkpoint, status: "succeeded" }], [live], before), [{ ...checkpoint, status: "succeeded" }]);
});

test("checkpoints cannot undo an assistant message boundary", () => {
  const old = { id: "one", status: "running", output: "前面的分析说明", output_version: 1 };
  const latest = { ...old, process_output: old.output, output: "结论", output_version: 3 };
  assert.deepEqual(mergeRunRefresh([old], [latest], new Map([[old.id, latest]])), [latest]);
  assert.deepEqual(mergeRunRefresh([latest], [old], new Map([[old.id, old]])), [latest]);
});
