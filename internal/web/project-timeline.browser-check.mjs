// Snake roadmap visual and interaction checks use only temporary local data.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import net from "node:net";
import { once } from "node:events";
const load = createRequire(process.env.AI_PM_QA_NODE_MODULES ? join(process.env.AI_PM_QA_NODE_MODULES, "package.json") : import.meta.url);
const { chromium } = load("playwright");
const output = resolve("test-results/timeline-redesign");
await mkdir(output, { recursive: true });
const data = await mkdtemp(join(tmpdir(), "ai-pm-timeline-"));
const reserve = net.createServer().listen(0, "127.0.0.1");
await once(reserve, "listening");
const port = reserve.address().port;
await new Promise((done) => reserve.close(done));
const base = `http://127.0.0.1:${port}`;
const server = spawn(resolve(process.env.AI_PM_QA_BIN || "bin/ai-pm-timeline-check"), ["-addr", `127.0.0.1:${port}`, "-data", join(data, "state"), "-sync-interval", "0"], { stdio: "ignore" });
const exit = once(server, "exit");
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 2386, height: 1281 }, reducedMotion: "reduce", timezoneId: "Asia/Shanghai" });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
const report = {};
try {
  for (let n = 0; n < 100; n++) {
    try { if ((await fetch(base + "/api/health")).ok) break; } catch {}
    await new Promise((done) => setTimeout(done, 50));
  }
  const result = await fetch(base + "/api/projects", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name: "AI 打印 · 时间线预览" }) });
  assert(result.ok);
  const now = new Date(), midnight = new Date(now);
  midnight.setHours(0, 0, 0, 0);
  const atDay = (offset, hour = 12, minute = 0) => { const d = new Date(midnight); d.setDate(d.getDate() + offset); d.setHours(hour, minute); return +d; };
  const titles = ["加大图片最大存储时间", "模板 AI 质检超长时间未出结果（昨天的任务仍在排队）", "模板编辑界面设置 IP 方：选择 IP 时联动展示", "模版-生图测试的优化", "AI质检的优化", "国外平台", "模板导出的功能", "AI质检详情界面查看效果图的交互优化", "点数业务的调整", "扫码支付账户配置界面优化：微信/支付宝分开配置", "订单管理页整合人工异常展示与处理", "合作分成数据统计的优化", "模板二级类型增加图片", "修复超长标题与特殊字符 <script> 的完整显示", "点数充值明细导出", "机器离线告警", "模板批量上下架"];
  const plan = [[-26, 10, -24, -23, -22], [-25, 11, -21, -20, -18], [-19, 9, -17, -16, -15], [-14, 16, -12, -11, -9], [-13, 17, -10, -8, -6], [-6, 13, -1, -1], [-6, 13, -1, -1], [-6, 14, -2, -1, 0], [-1, 9, -1, -1], [-1, 9], [-1, 11, 0], [-1, 11, 0], [0, 11], [0, 8], [-2, 10, -1, -1], [-2, 12, -1, -1], [-2, 14, -1, -1]];
  const tasks = titles.map((title, i) => {
    const [created, hour, submitted, deployed, ended] = plan[i];
    return { id: `task-${i}`, title, kind: i === 13 ? "bug" : "requirement", status: ended !== undefined ? (i === 9 ? "closed" : "done") : deployed !== undefined ? "review" : submitted !== undefined ? "doing" : "clarify", created_at: atDay(created, hour, 20), ...(submitted !== undefined ? { submitted_at: atDay(submitted, 15) } : {}), ...(deployed !== undefined ? { deployed_at: atDay(deployed, 16, 16) } : {}), ...(ended !== undefined ? { ended_at: atDay(ended, 18) } : {}) };
  });
  const messages = ["fix(generation): preserve queue timeouts", "feat(admin): review all generated images", "fix(payments): configure cashier entry", "Merge origin/main into codex/templates", "feat(templates): complete quality review", "fix(images): prefer native image sizes", "refactor(payments): separate channels", "feat(points): add admin recharge"];
  const busy = { [-1]: 14, [-5]: 40, [-12]: 9, [-20]: 6 };
  const quiet = new Set([-3, -4, -7, -27, -28]);
  let requests = 0;
  await page.route("**/api/projects/*/timeline?*", async (route) => {
    requests++;
    const params = new URL(route.request().url()).searchParams;
    const until = Number(params.get("until")), from = Number(params.get("from"));
    const events = [], commits = [];
    for (const task of tasks) {
      for (const [field, kind] of [["created_at", "created"], ["submitted_at", "submit"], ["deployed_at", "mark_deployed"], ["ended_at", task.status === "closed" ? "close" : "accept"]]) {
        if (task[field] >= from && task[field] < until) events.push({ task_id: task.id, kind, at: task[field], actor: "产品开发组" });
      }
      events.push({ task_id: task.id, kind: "milestone_assigned", at: atDay(0, 10, 7), actor: "系统" });
    }
    events.push({ task_id: "", project_version_id: "version-preview", kind: "project_version_publish", at: atDay(-1, 16, 20), actor: "产品开发组" });
    for (let d = -59; d <= 0; d++) {
      if (quiet.has(d)) continue;
      const count = busy[d] ?? (Math.abs(d) % 4) + 1;
      for (let i = 0; i < count; i++) {
        const at = atDay(d, 9) + Math.floor(i * 600 / count) * 60000;
        if (at < from || at >= until || at > +now) continue;
        commits.push({ sha: `${d + 60}${i}a83e13`.padEnd(40, "0"), title: messages[(((i + d) % messages.length) + messages.length) % messages.length], at, author: "xwatsonmai", task_ids: i === 1 ? [tasks[Math.abs(d) % tasks.length].id] : [] });
      }
    }
    await route.fulfill({ json: { from, until, tasks, versions: [{ id: "version-preview", name: "默认版本", status: "active", started_at: atDay(-8, 8) }], activity: { events: events.filter((e) => e.at >= from && e.at < until), total: events.length, has_more: false }, git: { commits, has_more: false, synced_at: +now } } });
  });
  await page.goto(base);
  await page.locator(".nav").getByRole("button", { name: "项目时间线", exact: true }).click();
  await page.locator(".tl-range-summary").filter({ hasText: "7 天" }).waitFor();
  await page.locator(".tl-date").first().waitFor();
  const geometry = async () => page.evaluate(() => {
    const map = document.querySelector(".tl-roadmap"), bounds = map.getBoundingClientRect();
    const items = [...map.querySelectorAll(".tl-pin,.tl-date,.tl-landmark,.tl-commit,.tl-more,.tl-terminal")].filter((el) => el.getClientRects().length).map((el) => ({ el, r: el.getBoundingClientRect(), stop: el.closest(".tl-stop") }));
    const outside = items.filter(({ r }) => r.left < bounds.left - 0.5 || r.right > bounds.right + 0.5 || r.top < bounds.top - 0.5 || r.bottom > bounds.bottom + 0.5).length;
    let overlaps = 0;
    const collisions = [];
    for (let i = 0; i < items.length; i++) for (let j = i + 1; j < items.length; j++) {
      const a = items[i].r, b = items[j].r;
      if (a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1) {
        overlaps++;
        if (collisions.length < 3) collisions.push([items[i], items[j]].map(({ el }) => `${el.className}:${el.textContent.slice(0, 16)}`).join(" × "));
      }
    }
    const truncatedLabels = [...map.querySelectorAll(".tl-date,.tl-terminal")].filter((el) => el.scrollWidth > el.clientWidth + 1).length;
    // Road ticks must stay inside the canvas and clear of every card, date and list row.
    const ticks = [...map.querySelectorAll(".tl-tick rect,.tl-tick-dot")].map((el) => el.getBoundingClientRect());
    const tickClashes = ticks.filter((t) => t.left < bounds.left || t.right > bounds.right || items.some(({ r }) => t.left < r.right && r.left < t.right && t.top < r.bottom && r.top < t.bottom)).length;
    const tickedCommits = [...map.querySelectorAll(".tl-tick.is-commit")].reduce((n, el) => n + Number(el.dataset.count), 0);
    const dayCommits = [...map.querySelectorAll(".tl-stop")].reduce((n, el) => n + Number(el.getAttribute("aria-label").match(/(\d+) 条提交/)[1]), 0);
    const stops = [...map.querySelectorAll(".tl-stop")].map((el) => ({ x: el.getBoundingClientRect().left, y: parseFloat(el.style.top) }));
    const rows = [];
    for (const s of stops) { if (!rows.length || rows.at(-1).y !== s.y) rows.push({ y: s.y, xs: [] }); rows.at(-1).xs.push(s.x); }
    const directions = rows.filter((r) => r.xs.length > 1).map((r) => Math.sign(r.xs[1] - r.xs[0]));
    return { stops: stops.length, rows: rows.length, directions, outside, overlaps, collisions, truncatedLabels, tickClashes, tickedCommits, dayCommits, overflow: document.documentElement.scrollWidth > innerWidth, arrow: !!map.querySelector(".tl-road-arrow"), commits: map.querySelectorAll(".tl-commit").length };
  });
  const check = async (name, width, height) => {
    await page.setViewportSize({ width, height });
    await page.waitForFunction((w) => document.querySelector(".tl-roadmap").dataset.width === String(document.querySelector(".tl-roadmap").clientWidth), width);
    await page.evaluate(() => window.scrollTo(0, 0));
    const g = await geometry();
    assert(!g.overflow && g.arrow && g.outside === 0 && g.overlaps === 0 && g.truncatedLabels === 0 && g.tickClashes === 0 && g.tickedCommits === g.dayCommits, `${name} ${JSON.stringify(g)}`);
    for (let i = 1; i < g.directions.length; i++) assert.notEqual(g.directions[i], g.directions[i - 1], `${name} rows must alternate ${JSON.stringify(g)}`);
    report[name] = g;
    await page.screenshot({ path: join(output, name + ".png"), fullPage: true });
    return g;
  };
  await check("7d-wide-2386", 2386, 1281);
  const before = requests;
  await page.locator(".tl-more").filter({ hasText: "条提交" }).first().click();
  await page.locator('.tl-more[aria-expanded="true"]').first().waitFor();
  assert.equal(await page.locator('.tl-more[aria-expanded="true"]').first().evaluate((el) => el === document.activeElement), true);
  const expanded = await check("7d-expanded-1440", 1440, 1080);
  await page.locator('.tl-more[aria-expanded="true"]').first().click();
  assert((await geometry()).commits < expanded.commits);
  assert.equal(requests, before, "expanding a day must not refetch");
  assert.equal(await page.locator(".tl-map script").count(), 0);
  // Hovering a requirement brings its folded states out in place and shows its full history.
  await page.evaluate(() => window.scrollTo(0, 0));
  const fold = await page.evaluate(() => {
    const folded = [...document.querySelectorAll(".tl-pin.is-folded")].find((el) => document.querySelector(`.tl-pin:not(.is-folded)[data-task="${el.dataset.task}"]`));
    return folded && { task: folded.dataset.task, stop: [...document.querySelectorAll(".tl-stop")].indexOf(folded.closest(".tl-stop")) };
  });
  assert(fold, "the fixture must fold a card of a requirement that is visible elsewhere");
  const visibleIn = (i) => page.evaluate((i) => [...document.querySelectorAll(".tl-stop")[i].querySelectorAll(".tl-pin")].filter((el) => el.getClientRects().length).map((el) => el.dataset.task), i);
  const beforeTrace = await visibleIn(fold.stop);
  assert(!beforeTrace.includes(fold.task));
  await page.locator(`.tl-pin:not(.is-folded)[data-task="${fold.task}"]`).first().hover();
  const afterTrace = await visibleIn(fold.stop);
  assert(afterTrace.includes(fold.task) && afterTrace.length === beforeTrace.length, JSON.stringify({ beforeTrace, afterTrace }));
  const panel = await page.evaluate(() => { const p = document.querySelector("#tl-history"), r = p.getBoundingClientRect(); return { hidden: p.hidden, items: p.querySelectorAll("li").length, inside: r.top >= 0 && r.bottom <= innerHeight && r.left >= 0 && r.right <= innerWidth }; });
  assert(!panel.hidden && panel.items >= 3 && panel.inside, JSON.stringify(panel));
  assert.equal((await geometry()).overlaps, 0, "a swapped-in card keeps the stack height");
  await page.screenshot({ path: join(output, "trace-history-1440.png") });
  await page.screenshot({ path: join(output, "trace-fold-1440.png"), fullPage: true });
  await page.mouse.move(5, 5);
  assert.deepEqual(await visibleIn(fold.stop), beforeTrace);
  assert(await page.locator("#tl-history").isHidden());
  report.fold = { ...fold, panel };
  // Switching the node interval regroups loaded data without another request.
  const twoHourStops = (await geometry()).stops;
  await page.locator("#tl-grain").selectOption("24");
  await page.waitForFunction((n) => document.querySelectorAll(".tl-stop").length < n, twoHourStops);
  await check("7d-daily-1440", 1440, 1080);
  await page.locator("#tl-grain").selectOption("2");
  await page.waitForFunction((n) => document.querySelectorAll(".tl-stop").length === n, twoHourStops);
  assert.equal(requests, before, "changing the node interval must not refetch");
  await page.locator("#tl-days").selectOption("30");
  await page.locator(".tl-range-summary").filter({ hasText: "30 天" }).waitFor();
  assert.equal(await page.locator("#tl-grain").inputValue(), "12");
  const month = await check("30d-desktop-1440", 1440, 1080);
  assert(month.rows >= 3, JSON.stringify(month));
  await check("30d-tablet-1024", 1024, 1000);
  await check("30d-mobile-390", 390, 844);
  await page.getByRole("button", { name: "更早", exact: true }).click();
  await page.locator(".tl-terminal").filter({ hasText: "截至" }).waitFor();
  await check("30d-earlier-mobile-390", 390, 844);
  await check("30d-earlier-desktop-1440", 1440, 1080);
  await page.getByRole("button", { name: "更晚", exact: true }).click();
  await page.locator(".tl-terminal").filter({ hasText: "至今" }).waitFor();
  await page.locator("#tl-days").selectOption("90");
  await page.locator(".tl-range-summary").filter({ hasText: "90 天" }).waitFor();
  assert.equal(await page.locator("#tl-grain").inputValue(), "24");
  await check("90d-wide-2386", 2386, 1281);
  // Hovering a requirement card lights its whole course and dims everything else.
  await page.setViewportSize({ width: 1440, height: 1080 });
  const card = page.locator(".tl-pin").filter({ hasText: "国外平台" }).first();
  assert.match(await card.locator(".tl-pin-life").innerText(), /提出 → .*上线/);
  await card.hover();
  const traced = await page.evaluate(() => ({ tracing: document.querySelector(".tl-map").classList.contains("is-tracing"), journeys: document.querySelectorAll(".tl-journey.is-traced").length, cards: document.querySelectorAll(".tl-pin.is-traced").length, ticks: document.querySelectorAll(".tl-tick.is-req.is-traced").length }));
  assert(traced.tracing && traced.journeys === 1 && traced.cards >= 2 && traced.ticks >= 2, JSON.stringify(traced));
  await page.screenshot({ path: join(output, "trace-1440.png"), fullPage: true });
  await page.mouse.move(5, 5);
  assert.equal(await page.locator(".tl-map.is-tracing").count(), 0);
  await page.locator(".tl-pin").first().focus();
  assert.equal(await page.locator(".tl-map.is-tracing").count(), 1, "keyboard focus traces too");
  report.trace = traced;
  assert.deepEqual(errors, []);
  report.interactions = "2小时/半天/每天节点切换不请求API、蛇形折返、上下分区无重叠、路上小节点覆盖全部提交且不压卡片、悬停调出折叠状态与完整变更历史、悬停/聚焦点亮需求全程、抽样与展开收起、7/30/90天、四种宽度及HTML转义通过";
  await writeFile(join(output, "checks.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
} catch (e) {
  await page.screenshot({ path: join(output, "failure.png"), fullPage: true }).catch(() => {});
  throw e;
} finally {
  await browser.close();
  server.kill("SIGTERM");
  await exit;
  await rm(data, { recursive: true, force: true });
}
