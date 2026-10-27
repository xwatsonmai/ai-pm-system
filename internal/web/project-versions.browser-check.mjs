// Isolated loopback service, temporary SQLite and a real local Git repository.
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { createRequire } from "node:module";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import net from "node:net";
import { once } from "node:events";
const load = createRequire(
    process.env.AI_PM_QA_NODE_MODULES
      ? join(process.env.AI_PM_QA_NODE_MODULES, "package.json")
      : import.meta.url,
  ),
  { chromium } = load("playwright");
const output = resolve("test-results/milestone-timeline-ui");
await mkdir(output, { recursive: true });
const data = await mkdtemp(join(tmpdir(), "ai-pm-milestone-")),
  source = join(data, "source");
await mkdir(source);
const git = (...args) =>
  execFileSync("git", args, {
    cwd: source,
    encoding: "utf8",
    env: {
      ...process.env,
      GIT_AUTHOR_NAME: "产品开发组",
      GIT_AUTHOR_EMAIL: "qa@example.invalid",
      GIT_COMMITTER_NAME: "产品开发组",
      GIT_COMMITTER_EMAIL: "qa@example.invalid",
    },
  }).trim();
git("init", "-b", "main");
for (const title of [
  "建立质检基础",
  "修复图片选择 <script>",
  "补齐需求评审记录",
])
  git("commit", "--allow-empty", "-m", title);
const sha = git("rev-parse", "HEAD");
const reserve = net.createServer().listen(0, "127.0.0.1");
await once(reserve, "listening");
const port = reserve.address().port;
await new Promise((done) => reserve.close(done));
const base = `http://127.0.0.1:${port}`;
const server = spawn(
  resolve(process.env.AI_PM_QA_BIN || "bin/ai-pm-milestones-check"),
  [
    "-addr",
    `127.0.0.1:${port}`,
    "-data",
    join(data, "state"),
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
const close = async () => page.locator("#overlays [data-action=close]").click();
const nav = async (name) => {
  if (
    await page
      .getByRole("button", { name: "打开导航", exact: true })
      .isVisible()
  )
    await page.getByRole("button", { name: "打开导航", exact: true }).click();
  await page.locator(".nav").getByRole("button", { name, exact: true }).click();
};
const report = {};
try {
  for (let n = 0; n < 100; n++) {
    try {
      if ((await fetch(base + "/api/health")).ok) break;
    } catch {}
    await new Promise((done) => setTimeout(done, 50));
  }
  const project = await api("/projects", {
    name: "质检体验项目",
    repository: source,
    repository_auth: { mode: "server" },
    branch: "main",
  });
  await api(`/projects/${project.id}/sync`, {});
  await page.goto(base);
  await nav("项目版本");
  await page
    .getByRole("heading", { name: "暂无进行中的里程碑", exact: true })
    .waitFor();
  assert.equal(
    await page.getByRole("button", { name: "查看未排期", exact: true }).count(),
    0,
  );
  await page.locator("#page [data-action=new-task]").click();
  await page
    .locator(".milestone-association")
    .getByText("保存时自动创建里程碑", { exact: true })
    .waitFor();
  assert.equal(await page.locator("[name=planned_version_id]").count(), 0);
  await page.locator("#task-form [name=title]").fill("AI 质检结果补充原因");
  await page
    .locator(".requirement-prose")
    .fill("完整需求原文：产品填写原因后，可以再次发起质检。");
  await page.locator("#task-form [name=acceptance]").fill("保留每次质检记录");
  await page.locator("#task-form button[type=submit]").click();
  await page.locator("#task-form").waitFor({ state: "detached" });
  assert.equal(await page.locator("dialog").count(), 0);
  let ctx = await api(`/projects/${project.id}/context`),
    original = ctx.tasks.find((t) => t.title === "AI 质检结果补充原因");
  assert(original.project_version_id);
  assert.equal(ctx.project_versions.length, 1);
  const versionID = original.project_version_id;
  assert.equal(ctx.current_milestone.id, versionID);
  await nav("项目版本");
  await page
    .locator(`.pv-current [data-action=pv-open][data-id="${versionID}"]`)
    .first()
    .click();
  await page.getByRole("button", { name: "编辑里程碑", exact: true }).click();
  await page.locator("#pv-form [name=name]").fill("v2.0 · 质检体验");
  await page
    .locator("#pv-form [name=goal]")
    .fill("产品可以直接处理质检失败并保留上下文");
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await page
    .locator(`.pv-current [data-action=pv-open][data-id="${versionID}"]`)
    .first()
    .click();
  await page.getByRole("button", { name: "编辑里程碑", exact: true }).click();
  assert.equal(
    await page.locator("#pv-form [name=name]").inputValue(),
    "v2.0 · 质检体验",
  );
  await page.locator("#pv-form button[type=submit]").click();
  await page.locator(".pv-detail .detail-title").waitFor();
  const action = async (t, kind, extra = {}) =>
    (
      await api(`/tasks/${t.id}/actions`, {
        action: kind,
        request_id: id(),
        ...extra,
      })
    ).task;
  let ready = (
    await api(`/projects/${project.id}/tasks`, {
      title: "修复质检图片选择",
      kind: "bug",
      description: "保存原始截图描述",
      acceptance: ["可以选择图片"],
      request_id: id(),
    })
  ).task;
  ready = await action(ready, "ready");
  ready = await action(ready, "claim");
  ready = await action(ready, "submit", {
    revision: ready.revision,
    submission: { sha, summary: "开发完成", tests: "专项回归通过" },
  });
  await page.getByRole("button", { name: "刷新进展", exact: true }).click();
  await page
    .getByRole("button", { name: "登记本次已上线", exact: true })
    .click();
  await page.locator("#pv-publish-form [name=task_ids]").check();
  await page.locator("#pv-publish-form [name=environment]").fill("国内生产");
  await page
    .locator("#pv-publish-form [name=body]")
    .fill("build-200 实际发布成功");
  let dropped = false;
  await page.route(
    `**/api/project-versions/${versionID}/actions`,
    async (route) => {
      if (!dropped && route.request().postDataJSON()?.action === "publish") {
        dropped = true;
        await route.fetch();
        await route.abort("failed");
      } else await route.continue();
    },
  );
  await page.locator("#pv-publish-form button[type=submit]").click();
  await page
    .locator("#pv-publish-form .form-error")
    .filter({ hasText: /fetch|网络|Failed/i })
    .waitFor();
  await close();
  await nav("项目版本");
  await page.getByRole("button", { name: "核对保存结果", exact: true }).click();
  await page.locator(".pv-detail .detail-title").waitFor();
  let view = await api(`/project-versions/${versionID}`);
  assert.equal(view.project_version.releases.length, 1);
  assert.equal(view.project_version.status, "active");
  assert.equal(view.project_version.scope_frozen, false);
  await page
    .locator("#overlays")
    .getByRole("button", { name: "创建需求", exact: true })
    .click();
  await page
    .locator(".milestone-association")
    .getByText("v2.0 · 质检体验", { exact: true })
    .waitFor();
  await page.locator("#task-form [name=title]").fill("上线后追加的需求");
  await page.locator(".requirement-prose").fill("里程碑结束前继续补齐细节");
  await page.locator("#task-form button[type=submit]").click();
  await page.locator("#task-form").waitFor({ state: "detached" });
  ctx = await api(`/projects/${project.id}/context`);
  assert(ctx.tasks.every((t) => t.project_version_id === versionID));
  assert.equal(ctx.project_versions.length, 1);
  report.milestone =
    "无当前版本自动创建、需求强制归属、图文先保存后关闭、里程碑编辑草稿及上线后持续追加通过";
  await nav("项目时间线");
  await page.locator(".tl-commit").first().waitFor();
  await page.locator(".tl-range-summary").filter({ hasText: "7 天" }).waitFor();
  assert.equal(await page.locator(".tl-commit").count(), 3);
  assert((await page.locator(".tl-pin").count()) > 0);
  assert.equal(
    await page.locator("script").filter({ hasText: "修复图片选择" }).count(),
    0,
  );
  assert(
    (await page
      .locator(".tl-commit")
      .filter({ hasText: "关联 1 个需求" })
      .count()) >= 1,
  );
  await page.locator(".tl-commit-title").first().click();
  await page
    .getByRole("heading", {
      name: "Git 提交 · " + sha.slice(0, 12),
      exact: true,
    })
    .waitFor();
  await close();
  await page.locator("#tl-days").selectOption("30");
  await page.locator(".tl-range-summary").filter({ hasText: "30 天" }).waitFor();
  await page.screenshot({ path: join(output, "timeline-desktop.png") });
  await page.locator(".tl-landmark").first().focus();
  await page.keyboard.press("Enter");
  await page.locator(".pv-detail .detail-title").waitFor();
  await close();
  assert(
    await page.getByRole("button", { name: "更晚", exact: true }).isDisabled(),
  );
  await page.getByRole("button", { name: "更早", exact: true }).click();
  await page.waitForFunction(
    () => !document.querySelector('[data-action="tl-later"]').disabled,
  );
  await page.getByRole("button", { name: "更晚", exact: true }).click();
  await page.locator(".tl-commit").first().waitFor();
  // Git may need more pages even when the requirement events are exhausted.
  // Loading the next Git page must not duplicate or reactivate the events page.
  for (let n = 0; n < 102; n++)
    git("commit", "--allow-empty", "-m", `分支变更记录 ${n + 1}`);
  await api(`/projects/${project.id}/sync`, {});
  const eventsBefore = (await api(`/projects/${project.id}/timeline`)).activity
    .total;
  // The roadmap samples commits per day, so the footer reports loaded totals.
  await page.getByRole("button", { name: "刷新时间线", exact: true }).click();
  await page.locator(".tl-footer").getByText("已加载 100 条提交", { exact: false }).waitFor();
  await page.getByRole("button", { name: "加载更多记录", exact: true }).click();
  await page.locator(".tl-footer").getByText("已加载 105 条提交", { exact: false }).waitFor();
  await page
    .locator(".tl-footer")
    .getByText(`需求事件 ${eventsBefore}/${eventsBefore}`, { exact: false })
    .waitFor();
  assert.equal(
    await page
      .getByRole("button", { name: "加载更多记录", exact: true })
      .count(),
    0,
  );
  report.pagination =
    "Git 105 条真实提交分页完整，需求事件不重复，翻页固定范围和分支快照";
  await nav("项目版本");
  await page
    .locator(`.pv-current [data-action=pv-open][data-id="${versionID}"]`)
    .first()
    .click();
  await page.getByRole("button", { name: "结束里程碑", exact: true }).click();
  await page.locator("#pv-action-form [name=body]").fill("完成本期");
  await page.locator("#pv-action-form button[type=submit]").click();
  await page
    .locator("#pv-action-form .form-error")
    .filter({ hasText: "仍有未结束" })
    .waitFor();
  assert.equal(
    await page.locator("#pv-action-form [name=body]").inputValue(),
    "完成本期",
  );
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await page
    .locator(`.pv-current [data-action=pv-open][data-id="${versionID}"]`)
    .first()
    .click();
  await page.getByRole("button", { name: "关闭里程碑", exact: true }).click();
  await page
    .locator("#pv-action-form [name=body]")
    .fill("剩余工作安排到下一里程碑");
  await page.locator("#pv-action-form button[type=submit]").click();
  await page.locator(".pv-detail .detail-title").waitFor();
  ctx = await api(`/projects/${project.id}/context`);
  assert.equal(
    ctx.project_versions.filter((v) => v.status === "active").length,
    1,
  );
  assert.notEqual(ctx.current_milestone.id, versionID);
  assert.equal(
    (await api(`/tasks/${ready.id}`)).task.project_version_id,
    versionID,
  );
  assert.notEqual(
    (await api(`/tasks/${original.id}`)).task.project_version_id,
    versionID,
  );
  assert.equal(
    (await api(`/tasks/${original.id}`)).task.description,
    original.description,
  );
  report.lifecycle =
    "未完成不能误结束，关闭自动转移后续工作且待验收留在原版本；原文和发布快照保留";
  await close();
  await nav("项目时间线");
  await page.locator(".tl-date").first().waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForFunction(
    () => !document.querySelector("#toast").classList.contains("show"),
  );
  await page.screenshot({ path: join(output, "timeline-mobile.png") });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.locator("#tl-days").selectOption("7");
  await page.locator(".tl-range-summary").filter({ hasText: "7 天" }).waitFor();
  await page.screenshot({ path: join(output, "timeline-mobile-7d.png") });
  report.timeline =
    "真实Git三条提交、明确SHA关联、上下轨道、7/30天范围、提交详情、键盘打开里程碑及390px布局通过";
  await nav("需求池");
  await page.locator("#version-filter").selectOption(versionID);
  await page.locator(`tr[data-id="${ready.id}"]`).click();
  await page.getByRole("button", { name: "开始验收", exact: true }).waitFor();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
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
