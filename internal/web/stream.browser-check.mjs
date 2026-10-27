// Real Go -> SSE -> browser checks with an isolated database and local worker.
// No external model, credentials or production data are used.
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
const data = await mkdtemp(join(tmpdir(), "ai-pm-stream-browser-"));
const output = resolve("test-results/chat-stream");
await mkdir(output, { recursive: true });
const worker = join(data, "worker.mjs");
await writeFile(worker, `
import readline from "node:readline";
const input = readline.createInterface({input:process.stdin});
const emit = (message) => process.stdout.write(JSON.stringify(message)+"\\n");
input.on("line", async (line) => {
  const message = JSON.parse(line);
  if (message.type === "stop") { emit({type:"done"}); process.exit(0); }
  if (message.type !== "start") return;
  emit({type:"title", text:"连续输出测试"});
  emit({type:"assistant_start"});
  emit({type:"delta", text:"先检查项目资料，确认图层的组合顺序。"});
  await new Promise((resolve) => setTimeout(resolve, 160));
  emit({type:"assistant_start"});
  emit({type:"delta", text:"## 检查结果\\n\\n已找到 **图层顺序** 的相关配置，继续核对调用位置。"});
  await new Promise((resolve) => setTimeout(resolve, 160));
  emit({type:"assistant_start"});
  let text = "# 连续输出测试\\n\\n" + Array.from({length:35}, (_, n) => "第 " + (n+1) + " 段历史分析，用于检查阅读位置。\\n\\n").join("");
  emit({type:"delta", text});
  // Scenario switches belong to the human message, not platform instructions
  // (which also describe failure recovery and must not change this fixture).
  const question = message.prompt.split("\\n用户输入：")[1]?.split("\\n以下是当前项目事实快照")[0] || message.prompt;
  const count = question.includes("失败") ? 8 : question.includes("恢复") ? 250 : 65;
  for (let n = 1; n <= count; n++) {
    await new Promise((resolve) => setTimeout(resolve, 60));
    const delta = "文字" + n + "🙂 ";
    text += delta;
    emit({type:"delta", text:delta});
  }
  if (question.includes("失败")) emit({type:"error", error:"模拟失败，已显示的过程仍需保留"});
  emit({type:"done", text});
  process.exit(0);
});
`);
const reservation = net.createServer().listen(0, "127.0.0.1");
await once(reservation, "listening");
const port = reservation.address().port;
await new Promise((done) => reservation.close(done));
const base = `http://127.0.0.1:${port}`;
const server = spawn(resolve(process.env.AI_PM_QA_BIN || "bin/ai-pm-chat-check"),
  ["-addr", `127.0.0.1:${port}`, "-data", data, "-sync-interval", "0", "-pi-worker", worker],
  { stdio: ["ignore", "ignore", "pipe"] });
const serverExit = once(server, "exit");
server.stderr.resume();
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const errors = [], requests = [];
page.on("pageerror", (error) => errors.push(error.message));
page.on("request", (request) => requests.push(new URL(request.url()).pathname));
const api = async (path, body, method = "POST") => {
  const response = await fetch(base + "/api" + path, {
    method, headers: { "Content-Type": "application/json" }, body: body && JSON.stringify(body),
  });
  const value = await response.json();
  assert.equal(response.ok, true, JSON.stringify(value));
  return value;
};
await page.addInitScript(() => {
  window.streamSources = [];
  window.streamEvents = [];
  const Native = window.EventSource;
  window.EventSource = class extends Native {
    constructor(url, options) {
      super(url, options);
      window.streamSources.push(this);
      this.addEventListener("run", (event) => {
        const update = JSON.parse(event.data);
        window.streamEvents.push({ at: performance.now(), ...update });
      });
    }
  };
});
const report = {};
try {
  for (let n = 0; n < 100; n++) {
    try { if ((await fetch(base + "/api/health")).ok) break; } catch {}
    await new Promise((done) => setTimeout(done, 50));
  }
  const profile = await api("/profiles", { name: "本地测试", provider: "openai", model: "fixture", key: "fixture-only" });
  await api("/platform/settings", { profile_id: profile.id }, "PUT");
  const project = await api("/projects", { name: "项目问答 · 流式验证" });
  await page.goto(base);
  await page.getByRole("button", { name: "项目问答", exact: true }).click();
  const input = page.locator("textarea[name=question]");
  await input.fill("检查连续输出");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.locator(".answer-content h1").waitFor();
  const process = page.locator(".run-process-details").first();
  assert(await process.evaluate((element) => element.open));
  assert.match(await process.locator(".process-content").innerText(), /先检查项目资料[\s\S]*图层顺序/);
  await process.locator("summary").click();
  await page.waitForTimeout(160);
  assert.equal(await process.evaluate((element) => element.open), false, "stream reopened a manually closed process");
  await process.locator("summary").click();
  await page.locator(".chat-feed").evaluate((feed) => { feed.scrollTop = feed.scrollHeight; });
  await page.evaluate(() => {
    window.refs = { input: document.querySelector("textarea[name=question]"), heading: document.querySelector(".answer-content h1") };
    window.paints = [];
    new MutationObserver(() => window.paints.push(performance.now())).observe(document.querySelector(".answer-content"), { childList: true, characterData: true, subtree: true });
  });
  await input.fill("下一条问题的草稿");
  await input.evaluate((element) => element.setSelectionRange(2, 5));
  await page.waitForTimeout(1100);
  const cadence = await page.evaluate(() => {
    const times = window.paints;
    const intervals = times.slice(1).map((at, i) => at - times[i]).sort((a, b) => a - b);
    const feed = document.querySelector(".chat-feed");
    return { updates: times.length, median_ms: intervals[Math.floor(intervals.length / 2)],
      bottom_gap: feed.scrollHeight - feed.clientHeight - feed.scrollTop,
      nodes_retained: refs.input.isConnected && refs.heading.isConnected,
      draft: refs.input.value, selection: [refs.input.selectionStart, refs.input.selectionEnd],
      focused: document.activeElement === refs.input };
  });
  assert(cadence.updates >= 10, JSON.stringify(cadence));
  assert(cadence.median_ms < 150, JSON.stringify(cadence));
  assert(cadence.bottom_gap < 5, JSON.stringify(cadence));
  assert(cadence.nodes_retained && cadence.focused);
  assert.deepEqual(cadence.selection, [2, 5]);
  assert.equal(cadence.draft, "下一条问题的草稿");
  report.cadence = cadence;
  await page.locator(".chat-feed").evaluate((feed) => { feed.scrollTop = 240; });
  await page.waitForTimeout(650);
  assert.equal(await page.locator(".chat-feed").evaluate((feed) => feed.scrollTop), 240);
  await page.locator(".run-status").filter({ hasText: "分析完成" }).waitFor();
  assert.equal(await process.evaluate((element) => element.open), false, "completion did not collapse the process");
  const runs = await api(`/projects/${project.id}/runs`, undefined, "GET");
  assert(runs[0].output.endsWith("文字65🙂 "));
  assert.match(runs[0].process_output, /先检查项目资料[\s\S]*图层顺序/);
  assert(!runs[0].output.includes("先检查项目资料"), "process text was duplicated in the final answer");
  assert((await page.locator(".answer-content").innerText()).includes("文字65🙂"));
  assert.equal(requests.filter((path) => /^\/api\/runs\/[^/]+$/.test(path)).length, 0);
  report.normal = "real SSE, no per-run polls; complete output persisted; draft, focus, selection and scroll retained";
  await page.locator(".chat-feed").evaluate((feed) => { feed.scrollTop = 0; });
  await page.screenshot({ path: join(output, "desktop.png"), fullPage: true });
  await process.locator("summary").click();
  assert.equal(await process.locator("strong").textContent(), "图层顺序");
  await page.waitForTimeout(6400);
  assert(await process.evaluate((element) => element.open), "background refresh closed expanded process");
  await page.screenshot({ path: join(output, "process-expanded.png"), fullPage: true });
  report.process = "public intermediate messages persisted; automatically collapsed on completion; expandable Markdown; manual state retained during refresh";

  await input.fill("检查断线恢复");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.waitForFunction(() => window.streamSources.length === 2 && window.streamEvents.filter((event) => event.run.id !== window.streamEvents[0].run.id).length > 3);
  // Disconnect an actual EventSource, then deliver the browser's network-error
  // notification. The server/model keeps producing output during recovery.
  await page.evaluate(() => {
    const source = window.streamSources.at(-1);
    source.close();
    source.dispatchEvent(new Event("error"));
  });
  await page.waitForFunction(() => window.streamSources.length >= 3, undefined, { timeout: 14000 });
  await page.waitForFunction(() => window.streamEvents.at(-1).run.output.includes("文字"));
  assert(requests.some((path) => /^\/api\/runs\/[^/]+$/.test(path)), "fallback polling did not run");
  await page.getByRole("button", { name: "停止分析", exact: true }).click();
  await page.locator(".run-status").filter({ hasText: "已停止" }).waitFor();
  assert.equal(await page.locator(".run-process-details").last().evaluate((element) => element.open), false);
  report.recovery = "disconnect -> fallback polls -> fresh SSE snapshot; stopping flushes final partial answer";
  const beforeReload = await page.locator(".answer-content").last().innerText();
  await page.reload();
  await page.getByRole("button", { name: "项目问答", exact: true }).click();
  assert.equal(await page.locator(".answer-content").last().innerText(), beforeReload);
  const restoredProcess = page.locator(".run-process-details").last();
  assert.equal(await restoredProcess.evaluate((element) => element.open), false);
  await restoredProcess.locator("summary").click();
  assert.match(await restoredProcess.locator(".process-content").innerText(), /先检查项目资料[\s\S]*图层顺序/);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(300);
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  assert(await page.locator(".sidebar").evaluate((element) => element.getBoundingClientRect().right <= 1));
  await page.screenshot({ path: join(output, "mobile.png"), fullPage: true });
  report.reload_mobile = "stopped answer survives reload; 390px viewport has no horizontal overflow";
  await page.setViewportSize({ width: 1440, height: 1000 });
  await input.fill("检查失败后的过程保留");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.locator(".run-status").filter({ hasText: "分析失败" }).waitFor();
  const failedProcess = page.locator(".run-process-details").last();
  assert.equal(await failedProcess.evaluate((element) => element.open), false);
  await failedProcess.locator("summary").click();
  assert.match(await failedProcess.innerText(), /先检查项目资料/);
  report.failed_process = "failed run retains expandable process and partial answer";
  assert.deepEqual(errors, []);
  report.browser_errors = errors;
  await writeFile(join(output, "checks.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(JSON.stringify(report, null, 2));
} finally {
  await browser.close();
  server.kill("SIGTERM");
  await serverExit;
  await rm(data, { recursive: true, force: true });
}
