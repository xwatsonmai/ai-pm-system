// Run against an isolated local binary with Playwright; no live API or model is used.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import net from "node:net";
import { once } from "node:events";

const load = createRequire(process.env.AI_PM_QA_NODE_MODULES
  ? join(process.env.AI_PM_QA_NODE_MODULES, "package.json") : import.meta.url);
const { chromium } = load("playwright");
const output = resolve("test-results/chat-ui");
await mkdir(output, { recursive: true });
const data = await mkdtemp(join(tmpdir(), "ai-pm-chat-browser-"));
const reservation = net.createServer().listen(0, "127.0.0.1");
await once(reservation, "listening");
const port = reservation.address().port;
await new Promise((done) => reservation.close(done));
const server = spawn(resolve(process.env.AI_PM_QA_BIN || "bin/ai-pm-chat-check"),
  ["-addr", `127.0.0.1:${port}`, "-data", data, "-sync-interval", "0"],
  { stdio: ["ignore", "ignore", "pipe"] });
const serverExit = once(server, "exit");
server.stderr.resume();
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const errors = [];
page.on("pageerror", (error) => errors.push(error.message));
const project = { id: "prj_chat_fixture", name: "星河商城 · 服务端", branch: "main",
  profile_id: "fixture", owner_id: "", description: "持续回复与 Markdown 验证", sha: "a".repeat(40) };
const markdown = "# 项目简介\n\n**星河商城**是 在线零售平台，仓库为 `mall_server`。\n\n"
  + "## 业务定位\n\n- 商家管理与设备归属\n- AI 生成图片及打印订单\n\n"
  + "| 模块 | 状态 |\n| --- | --- |\n| server | 已同步 |\n| mobile | 子模块 |\n\n"
  + "```js\nconst project = '<mall_server>';\n```\n\n"
  + "[仓库资料](https://example.test/repo)\n\n> 分析依据项目资料与当前代码。\n\n"
  + '[无效链接](javascript:alert(1))\n\n<img src=x onerror="window.markdownAttack=1">';
let runs = [{ id: "run_history", project_id: project.id, kind: "question", status: "succeeded",
  conversation_id: "conversation_fixture", owner_id: "local-owner", conversation_title: "项目简介", title_status: "ready",
  started_at: Date.now() - 30000, prompt: "概括这个项目", output: markdown, steps: [] }];
let reads = 0, contexts = 0, frozen = false, longOutput = false, failRun = false;
let collaborations = [];
let activeRun;
function currentRun() {
  if (!activeRun) return undefined;
  if (failRun) return { ...activeRun, status: "failed", error: "模拟接口故障", output: markdown };
  const iteration = frozen ? 1 : reads;
  return { ...activeRun, status: "running", output: markdown + "\n\n## 持续更新\n\n"
    + (longOutput ? Array.from({ length: 45 }, (_, n) => `第 ${n + 1} 段项目分析，保留历史阅读位置。\n\n`).join("") : "")
    + `已完成第 ${iteration} 步分析。`, steps: [{ name: "repository_read", status: "done" }] };
}
await page.route("**/api/**", async (route) => {
  const request = route.request(), path = new URL(request.url()).pathname;
  let body;
  if (path === "/api/auth/session") body = { enabled: false };
  else if (path === "/api/projects") body = [project];
 else if (path === "/api/agent/status") body = {configured:true,profile_id:"fixture"};
  else if (path === "/api/profiles" || path === "/api/users") body = [];
  else if (path.endsWith("/collaborations")) body = collaborations;
  else if (path === "/api/collaborations/collab_fixture") body = collaborations[0];
  else if (path === "/api/collaborations/collab_fixture/cancel") { collaborations[0] = { ...collaborations[0], status: "canceled" }; body = { collaboration: collaborations[0] }; }
  else if (path.endsWith("/context")) body = { project, tasks: [], events: [],
    artifacts: [{ id: "external_update", kind: "analysis", body: `外部活动 ${++contexts}` }] };
  else if (path === `/api/projects/${project.id}/runs` && request.method() === "POST") {
    const input = request.postDataJSON();
    activeRun = { id: "run_stream", project_id: project.id, kind: "question", status: "running",
      conversation_id: "conversation_fixture", owner_id: "local-owner",
      started_at: Date.now(), prompt: input.prompt, output: markdown, steps: [] };
    body = activeRun;
  } else if (path === `/api/projects/${project.id}/runs`) body = activeRun ? [...runs, currentRun()] : runs;
  else if (path === "/api/runs/run_stream") { reads++; body = currentRun(); }
  // Exercise compatibility with a server/proxy that cannot stream.
  else if (path.endsWith("/events")) { await route.fulfill({ status: 503, body: "stream unavailable" }); return; }
  else throw new Error(`Unexpected fixture request: ${request.method()} ${path}`);
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
});
const report = {};
try {
  for (let n = 0; n < 100; n++) {
    try { if ((await fetch(`http://127.0.0.1:${port}/api/health`)).ok) break; } catch {}
    await new Promise((done) => setTimeout(done, 50));
  }
  await page.goto(process.env.AI_PM_QA_BASE_URL || `http://127.0.0.1:${port}`);
  await page.getByRole("button", { name: "项目问答", exact: true }).click();
  await page.locator(".answer-content h1").waitFor();
  assert.equal(await page.locator(".answer-content h1").textContent(), "项目简介");
  assert.equal(await page.locator(".answer-content table tbody tr").count(), 2);
  assert.equal(await page.locator(".answer-content pre code").textContent(), "const project = '<mall_server>';\n");
  assert.equal(await page.locator(".answer-content [href^='javascript:'], .answer-content img[onerror]").count(), 0);
  assert.equal(await page.evaluate(() => window.markdownAttack), undefined);
  report.markdown = "headings, bold, lists, code, table, links and blockquote passed";
  report.unsafe_markup = "removed or escaped";
  const input = page.locator("textarea[name=question]");
  await input.fill("**用户问题保留原文**");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.locator("#run-run_stream h1").waitFor();
  await page.evaluate(() => {
    window.chatRefs = { app: document.querySelector(".layout"), page: document.querySelector(".chat-page"),
      composer: document.querySelector("#chat-form"), input: document.querySelector("textarea[name=question]"),
      answer: document.querySelector("#run-run_stream"), heading: document.querySelector("#run-run_stream h1"),
      status: document.querySelector("#run-run_stream .run-status"), stop: document.querySelector("#run-run_stream .run-actions button") };
    window.chatMutations = 0;window.chatMutationDetails=[];
    window.chatObserver = new MutationObserver((records) => { window.chatMutations += records.length;window.chatMutationDetails.push(...records.map((r)=>({type:r.type,attribute:r.attributeName,target:r.target.outerHTML?.slice(0,260)||r.target.textContent?.slice(0,120)}))); });
    window.chatObserver.observe(document.querySelector("#app"), { subtree: true, childList: true, characterData: true, attributes: true });
  });
  await input.fill("下一条问题草稿");
  await input.focus();
  await page.evaluate(() => document.querySelector("textarea[name=question]").setSelectionRange(3, 5));
  await page.waitForTimeout(2200);
  assert.deepEqual(await page.evaluate(() => ({ focus: document.activeElement === chatRefs.input,
    value: chatRefs.input.value, start: chatRefs.input.selectionStart, end: chatRefs.input.selectionEnd,
    connected: Object.values(chatRefs).every((node) => node.isConnected) })),
    { focus: true, value: "下一条问题草稿", start: 3, end: 5, connected: true });
  report.composer = "node, draft, focus and selection preserved during polling";
  await input.fill("");
  await input.evaluate((element) => element.blur());
  const beforeRefresh = contexts;
  await page.waitForTimeout(6500);
  assert(contexts > beforeRefresh, "external background refresh should run");
  assert.equal(await page.evaluate(() => Object.values(chatRefs).every((node) => node.isConnected)), true);
  assert.equal(await page.evaluate(() => document.querySelector(".layout") === chatRefs.app
    && document.querySelector(".chat-page") === chatRefs.page
    && document.querySelector("#run-run_stream h1") === chatRefs.heading
    && document.querySelector("#run-run_stream .run-actions button") === chatRefs.stop), true);
  report.background_refresh = "layout, page, answer heading and stop button retained";
  frozen = true;
  await page.waitForTimeout(1200);
  await page.evaluate(() => { chatMutations = 0;chatMutationDetails=[]; });
  await page.waitForTimeout(2000);
  assert.equal(await page.evaluate(() => chatMutations), 0, JSON.stringify(await page.evaluate(() => chatMutationDetails)));
  report.unchanged_polls = "zero DOM mutations";
  longOutput = true;
  await page.waitForTimeout(1200);
  await page.locator(".chat-feed").evaluate((feed) => { feed.scrollTop = 260; });
  const scrollPosition = await page.locator(".chat-feed").evaluate((feed) => feed.scrollTop);
  frozen = false;
  await page.waitForTimeout(1800);
  assert.equal(await page.locator(".chat-feed").evaluate((feed) => feed.scrollTop), scrollPosition);
  report.history_scroll = "position retained during growing answer";
  longOutput = false;
  await page.waitForTimeout(1200);
  await page.locator(".chat-feed").evaluate((feed) => { feed.scrollTop = 0; });
  await page.screenshot({ path: join(output, "desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(250);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  assert.equal(await page.locator(".sidebar").evaluate((element) => element.getBoundingClientRect().right <= 1), true);
  await page.screenshot({ path: join(output, "mobile.png"), fullPage: true });
  report.mobile = "390px viewport without page overflow";
  failRun = true;
  await page.locator("#run-run_stream .run-error").filter({ hasText: "模拟接口故障" }).waitFor();
  assert.equal(await page.locator("#run-run_stream .run-actions button").textContent(), "重新分析");
  assert.equal(await page.evaluate(() => chatRefs.input.isConnected), true);
  report.failure_state = "error and retry shown without replacing composer";
  // Results discovered by the project refresh must arrive while typing, even
  // when there is no running model request left to poll.
  await page.setViewportSize({ width: 1440, height: 1000 });
  await input.fill("保留这份排查后的草稿");
  await input.focus();
  await input.evaluate((element) => element.setSelectionRange(2, 5));
  collaborations = [{ id: "collab_fixture", project_id: project.id, origin_run_id: "run_history",
    conversation_id: "conversation_fixture", title: "生产回调日志核查", brief: "只分享 trace-42 的排查摘要",
    environment: "production", scope: "只读调查，凭据留在本地", status: "pending", created_at: Date.now(), updates: [] }];
  await page.locator(".collaboration-card").waitFor({ timeout: 12000 });
  assert.match(await page.locator(".collaboration-card").textContent(), /待认领/);
  const auto = { id: "run_auto", project_id: project.id, kind: "question", status: "succeeded",
    conversation_id: "conversation_fixture", owner_id: "local-owner", trigger_type: "collaboration_result",
    source_event_id: "result_fixture", collaboration_id: "collab_fixture", prompt: "", started_at: Date.now(),
    output: "核查结论：配置不一致。证据 trace-42；尚未修复。", steps: [] };
  runs.push(auto);
  collaborations[0] = { ...collaborations[0], status: "completed", claimant_name: "本地开发 Agent",
    reply: { status: "delivered" }, result: { outcome: "confirmed", summary: "配置不一致", evidence: "trace-42",
      environment: "production", observed_at: "2026-10-14 10:00 +08:00", deployment_version: "abc123", limitations: "尚未修复" } };
  await page.locator("#run-run_auto").waitFor({ timeout: 12000 });
  assert.equal(await page.locator('[data-run-id="run_auto"] .user-question').count(), 0);
  assert.match(await page.locator("#run-run_auto").textContent(), /协同排查结果/);
  assert.deepEqual(await input.evaluate((element) => ({ value: element.value, start: element.selectionStart,
    end: element.selectionEnd, focus: document.activeElement === element })),
    { value: "保留这份排查后的草稿", start: 2, end: 5, focus: true });
  await page.getByRole("button", { name: "查看排查详情", exact: true }).click();
  await page.getByRole("dialog").waitFor();
  assert.match(await page.getByRole("dialog").textContent(), /abc123/);
  await page.locator('dialog [data-action="close"]').first().click();
  report.collaboration = "pending card, delayed assistant-only reply, evidence details, draft and selection preserved";
  runs.push({ ...auto, id: "run_other", source_event_id: "other-result", conversation_id: "other-conversation",
    conversation_title: "另一个排查", title_status: "ready", started_at: Date.now() + 1 });
  const other = page.locator('[data-action="select-conversation"][data-id="other-conversation"]');
  await other.locator(".reply-unread").waitFor({ timeout: 12000 });
  await other.click();
  assert.equal(await other.locator(".reply-unread").count(), 0);
  await page.locator('[data-action="select-conversation"][data-id="conversation_fixture"]').click();
  assert.equal(await input.inputValue(), "保留这份排查后的草稿");
  report.collaboration_unread = "other conversation gets unread badge and marks read when viewed";
  await page.locator(".chat-feed").evaluate((feed) => { feed.scrollTop = feed.scrollHeight; });
  await page.screenshot({ path: join(output, "collaboration-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(300);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  assert.equal(await page.locator(".sidebar").evaluate((element) => element.getBoundingClientRect().right <= 1), true);
  await page.screenshot({ path: join(output, "collaboration-mobile.png"), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.reload();
  await page.getByRole("button", { name: "项目问答", exact: true }).click();
  await page.locator("#run-run_auto").waitFor();
  assert.equal(await page.locator('[data-run-id="run_auto"] .user-question').count(), 0);
  report.collaboration_reload = "assistant-only result restored without page overflow";
  assert.deepEqual(errors, []);
  assert(reads < 32, "multiple concurrent polling loops were started for one run");
  report.browser_errors = errors;
  report.poll_reads = reads;
  await writeFile(join(output, "checks.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(JSON.stringify(report, null, 2));
} finally {
  await browser.close();
  server.kill("SIGTERM");
  await serverExit;
  await rm(data, { recursive: true, force: true });
}
