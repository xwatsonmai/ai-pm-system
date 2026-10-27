// Full local HTTP/SQLite/browser flow with a deterministic worker fixture.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import net from "node:net";
import { once } from "node:events";
const load = createRequire(
  process.env.AI_PM_QA_NODE_MODULES
    ? join(process.env.AI_PM_QA_NODE_MODULES, "package.json")
    : import.meta.url,
);
const { chromium } = load("playwright");
const output = resolve("test-results/requirement-iterations-ui");
await mkdir(output, { recursive: true });
const data = await mkdtemp(join(tmpdir(), "ai-pm-iterations-")),
  worker = join(data, "worker.mjs");
await writeFile(
  worker,
  `
import readline from 'node:readline';
import { prepareReviewInput } from ${JSON.stringify(new URL('../../runtime/review-context.mjs',import.meta.url).href)};
const emit=v=>console.log(JSON.stringify(v));
readline.createInterface({input:process.stdin}).on('line',line=>{
 const m=JSON.parse(line);
 if(m.type==='tool_result'){emit({type:'done',text:'评审结果已保存'});process.exit(0);}
 if(m.type==='stop'){emit({type:'done'});process.exit(0);}
 if(m.type!=='start')return;
 const source=m.review_context;
 const task=source.requirement;
 const prepared=prepareReviewInput(m,[]);
 if(task.iteration && (!prepared.prompt.includes('source_document_unique_417') || prepared.images.length!==1 || !task.iteration.source.review?.accepted)){
  emit({type:'error',error:'迭代评审没有收到上一轮的文档正文或截图'});emit({type:'done'});process.exit(0);
 }
 emit({type:'tool_call',id:'review',name:'submit_requirement_review',args:{reply:'已阅读上一轮已验收的需求、交付、文档和截图，本轮只补齐新增变化。',decision:'ready',agenda:[],proposal:{description:task.description,acceptance:['本轮新增规则生效']}}});
});
`,
);
const reserve = net.createServer().listen(0, "127.0.0.1");
await once(reserve, "listening");
const port = reserve.address().port;
await new Promise((done) => reserve.close(done));
const base = `http://127.0.0.1:${port}`;
const server = spawn(
  resolve(process.env.AI_PM_QA_BIN || "bin/ai-pm-iterations-check"),
  [
    "-addr",
    `127.0.0.1:${port}`,
    "-data",
    data,
    "-pi-worker",
    worker,
    "-sync-interval",
    "0",
  ],
  { stdio: ["ignore", "ignore", "pipe"] },
);
const exit = once(server, "exit");
server.stderr.resume();
const browser = await chromium.launch({ headless: true }),
  page = await browser.newPage({
    viewport: { width: 1440, height: 1000 },
    reducedMotion: "reduce",
  }),
  errors = [];
page.on("pageerror", (e) => errors.push(e.message));
const id = () => crypto.randomUUID();
async function api(path, body, method) {
  const r = await fetch(base + "/api" + path, {
    method: method || (body ? "POST" : "GET"),
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  const v = await r.json();
  assert(r.ok, JSON.stringify(v));
  return v;
}
const close = () => page.locator("#overlays [data-action=close]").click();
const current = async (task) => (await api(`/tasks/${task.id}`)).task;
async function finish(task) {
  for (const stage of ["ready", "claim", "submit", "test_pass", "mark_deployed", "accept"]) {
    if (stage === "ready" && task.status === "ready") continue;
    const input = { action: stage, request_id: id(), revision: task.revision };
    if (stage === "submit")
      input.submission = {
        sha: "sha-" + task.id,
        summary: "本轮开发完成",
        tests: "专项回归通过",
      };
    if (stage === "test_pass") {
      input.version = task.version;
      input.test_verification = {
        revision: task.revision,
        sha: task.submission.sha,
        environment: "fixture",
        note: "本轮测试通过",
      };
    }
    if (stage === "mark_deployed")
      input.deployment = {
        revision: task.revision,
        sha: task.submission.sha,
        environment: "fixture",
        body: "本轮实际部署完成",
      };
    if (stage === "accept")
      input.review = {
        revision: task.revision,
        sha: task.submission.sha,
        environment: "fixture",
        checks: task.acceptance.map(() => "passed"),
        note: "本轮业务验收通过",
      };
    task = (await api(`/tasks/${task.id}/actions`, input)).task;
  }
  return task;
}
async function endVersion(task) {
  const v = (await api(`/project-versions/${task.project_version_id}`))
    .project_version;
  await api(`/project-versions/${v.id}/actions`, {
    action: "complete",
    version: v.version,
    body: "本期已完成",
    request_id: id(),
  });
}
const report = {};
try {
  for (let n = 0; n < 100; n++) {
    try {
      if ((await fetch(base + "/api/health")).ok) break;
    } catch {}
    await new Promise((done) => setTimeout(done, 50));
  }
  const project = await api("/projects", {
    name: "退款能力持续迭代",
    branch: "main",
  });
  const profile = await api("/profiles", {
    name: "迭代评审测试",
    provider: "fixture",
    model: "fixture",
    base_url: "http://127.0.0.1:1",
    api: "openai-completions",
    supports_images: true,
  });
  await api("/platform/settings", { profile_id: profile.id }, "PUT");
  const png = await page.screenshot({
    clip: { x: 0, y: 0, width: 64, height: 64 },
  });
  const upload = async (name, bytes, type) => {
    const form = new FormData();
    form.append("file", new Blob([bytes], { type }), name);
    const response = await fetch(
      base + `/api/projects/${project.id}/attachments`,
      { method: "POST", body: form },
    );
    const v = await response.json();
    assert(response.ok, JSON.stringify(v));
    return v;
  };
  const image = await upload("上一轮截图.png", png, "image/png"),
    doc = await upload(
      "上一轮规则.md",
      Buffer.from("source_document_unique_417：上一轮原始规则。"),
      "text/markdown",
    );
  let source = (
    await api(`/projects/${project.id}/tasks`, {
      title: "已验收的退款能力",
      description: `## 上一轮完整业务规则\n\n保留退款和审计。\n\n![上一轮截图](/api/projects/${project.id}/attachments/${image.id}?preview=1)`,
      attachment_ids: [image.id, doc.id],
      acceptance: ["退款记录可追溯"],
      request_id: id(),
    })
  ).task;
  source = await finish(source);
  await endVersion(source);
  const oldVersion = await api(
    `/project-versions/${source.project_version_id}`,
  );
  await page.goto(base);
  await page
    .locator(".nav")
    .getByRole("button", { name: "需求池", exact: true })
    .click();
  // Normal creation and iteration each have their own recoverable draft.
  await page.locator("#page [data-action=new-task]").click();
  await page.locator("#task-form [name=title]").fill("普通新建需求的草稿");
  await page.locator(".requirement-prose").fill("普通草稿不可覆盖迭代草稿");
  await page.getByRole("button", { name: "暂存并关闭", exact: true }).click();
  await page.locator(`tr[data-id="${source.id}"]`).click();
  await page.getByRole("button", { name: "追加迭代", exact: true }).click();
  assert.equal(await page.locator("#task-form [name=title]").inputValue(), "");
  await page
    .locator(".milestone-association")
    .getByText("保存时自动创建里程碑", { exact: true })
    .waitFor();
  await page.locator(".iteration-snapshot summary").first().click();
  await page
    .locator(".iteration-snapshot-content")
    .getByText("上一轮完整业务规则", { exact: true })
    .waitFor();
  assert.equal(
    await page.locator(".iteration-snapshot-content img").count(),
    1,
  );
  await page.locator(".iteration-snapshot summary").first().click();
  await page.locator("#task-form [name=title]").fill("追加部分退款上限");
  await page
    .locator(".requirement-prose")
    .fill("本轮新增退款金额上限，原有规则继续保留。");
  await page.locator("[name=acceptance]").fill("部分退款不超过上限");
  await page.getByRole("button", { name: "暂存并关闭", exact: true }).click();
  await page.reload();
  await page
    .locator(".nav")
    .getByRole("button", { name: "需求池", exact: true })
    .click();
  await page.locator(`tr[data-id="${source.id}"]`).click();
  await page.getByRole("button", { name: "追加迭代", exact: true }).click();
  assert.equal(
    await page.locator("#task-form [name=title]").inputValue(),
    "追加部分退款上限",
  );
  assert(
    (await page.locator(".requirement-prose").textContent()).includes(
      "本轮新增退款金额上限",
    ),
  );
  let dropped = false;
  await page.route(`**/api/tasks/${source.id}/iterations`, async (route) => {
    if (route.request().method() === "POST" && !dropped) {
      dropped = true;
      await route.fetch();
      await route.abort("failed");
    } else await route.continue();
  });
  await page.locator("#task-form button[type=submit]").click();
  await page
    .locator("#task-form .form-error")
    .filter({ hasText: /fetch|Failed|网络/i })
    .waitFor();
  await page.getByRole("button", { name: "暂存并关闭", exact: true }).click();
  await page
    .locator(".nav")
    .getByRole("button", { name: "需求池", exact: true })
    .click();
  await page.locator(`tr[data-id="${source.id}"]`).click();
  await page.getByRole("button", { name: "追加迭代", exact: true }).click();
  await page.locator("#task-form button[type=submit]").click();
  await page.locator("#task-form").waitFor({ state: "detached" });
  assert.equal(await page.locator("dialog").count(), 0);
  let ctx = await api(`/projects/${project.id}/context`),
    children = ctx.tasks.filter((t) => t.iteration);
  assert.equal(children.length, 1);
  let child = children[0];
  assert.notEqual(child.project_version_id, source.project_version_id);
  assert.equal(child.project_version_id, ctx.current_milestone.id);
  assert.equal(child.status, "clarify");
  assert(!child.submission);
  assert.equal(child.iteration.source.description, source.description);
  assert.deepEqual(await current(source), source);
  assert.deepEqual(
    await api(`/project-versions/${source.project_version_id}`),
    oldVersion,
  );
  report.creation =
    "旧版本已结束时自动进入新当前版本；原需求和旧版本不变；创建先保存并关闭、草稿隔离和丢响应重试通过";
  let review;
  for (let n = 0; n < 150; n++) {
    review = await api(`/tasks/${child.id}/requirement-review`);
    if (!["queued", "running"].includes(review.status)) break;
    await new Promise((done) => setTimeout(done, 50));
  }
  assert.equal(review.status, "ready_to_confirm", JSON.stringify(review));
  assert(review.messages.some((m) => m.result?.body.includes("文档和截图")));
  report.context =
    "评审Worker实际收到上一轮业务范围与验收结论、提取的文档正文以及截图图片；本轮重新评审";
  await page
    .locator(".nav")
    .getByRole("button", { name: "需求池", exact: true })
    .click();
  await page.locator(`tr[data-id="${child.id}"]`).click();
  assert.equal(
    await page.getByRole("button", { name: "追加迭代", exact: true }).count(),
    0,
  );
  await page.getByRole("button", { name: "继续需求评审", exact: true }).click();
  await page.getByRole("button", { name: "需求原文", exact: true }).click();
  await page.locator(".review-source .iteration-context").waitFor();
  await close();
  await api(`/tasks/${child.id}/requirement-review`, {
    action: "confirm",
    generation: review.generation,
    request_id: id(),
  });
  child = await finish(await current(child));
  await endVersion(child);
  await page
    .locator(".nav")
    .getByRole("button", { name: "需求池", exact: true })
    .click();
  await page.locator(`tr[data-id="${child.id}"]`).click();
  await page.getByRole("button", { name: "追加迭代", exact: true }).click();
  await page.locator("#task-form").waitFor();
  assert.equal(await page.locator(".iteration-snapshot").count(), 2);
  await page.locator("#task-form [name=title]").fill("第二轮退款体验优化");
  await page.locator(".requirement-prose").fill("本轮优化退款结果展示。");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: join(output, "append-mobile.png") });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.locator("#task-form button[type=submit]").click();
  await page.locator("#task-form").waitFor({ state: "detached" });
  ctx = await api(`/projects/${project.id}/context`);
  const second = ctx.tasks.find((t) => t.title === "第二轮退款体验优化");
  assert.equal(second.iteration.parent_task_id, child.id);
  assert.equal(second.iteration.root_task_id, source.id);
  assert.equal(second.iteration.baseline.id, source.id);
  assert(!second.iteration.source.iteration);
  assert(!second.iteration.baseline.iteration);
  assert.notEqual(second.project_version_id, child.project_version_id);
  report.chain =
    "已验收的迭代可继续迭代，保留上一轮与最初基线，跨版本创建且不递归膨胀；390px表单通过";
  await page.setViewportSize({ width: 1440, height: 1000 });
  await api("/platform/settings", { profile_id: "" }, "PUT");
  for (let n = 0; n < 11; n++)
    await api(`/tasks/${source.id}/iterations`, {
      source_version: source.version,
      title: `后续迭代 ${n + 1}`,
      description: "保留本轮独立说明",
      request_id: id(),
    });
  await page
    .locator(".nav")
    .getByRole("button", { name: "需求池", exact: true })
    .click();
  await page.locator(`tr[data-id="${source.id}"]`).click();
  await page.locator(".iteration-history").waitFor();
  assert.equal(await page.locator(".iteration-history-row").count(), 11);
  await page.getByRole("button", { name: "加载更多迭代", exact: true }).click();
  await page.waitForFunction(
    () => document.querySelectorAll(".iteration-history-row").length === 14,
  );
  assert.equal(
    await page
      .getByRole("button", { name: "加载更多迭代", exact: true })
      .count(),
    0,
  );
  await page.screenshot({ path: join(output, "history-desktop.png") });
  const all = await api(`/tasks/${second.id}/iterations?limit=50`);
  assert.equal(all.total, 13);
  assert.equal(new Set(all.items.map((t) => t.id)).size, 13);
  report.history =
    "同一已验收需求可多次追加，13轮记录完整分页、反向查原需求、评审失败时图文与关联仍保存";
  assert.deepEqual(errors, []);
  console.log(JSON.stringify(report, null, 2));
} catch (e) {
  await page
    .screenshot({ path: join(output, "failure.png"), fullPage: true })
    .catch(() => {});
  throw e;
} finally {
  await browser.close();
  server.kill("SIGTERM");
  await exit;
  await rm(data, { recursive: true, force: true });
}
