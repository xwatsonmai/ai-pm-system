import test from "node:test";
import assert from "node:assert/strict";
import { roadmapModel, roadmapLayout, roadmapHTML, historyFor, historyHTML, grainFor } from "./static/project-timeline.mjs";
const at = (day, hour = 0, minute = 0) => +new Date(2026, 9, day, hour, minute);
const commit = (sha, title, when, task_ids = []) => ({ sha: sha.padEnd(40, "0"), title, at: when, author: "开发组", task_ids });
const fixture = () => ({
  from: at(14), until: at(21), versions: [],
  tasks: [
    { id: "a", title: "长周期需求", status: "done", created_at: at(14, 9), submitted_at: at(16, 15), deployed_at: at(17, 11), ended_at: at(18, 18), updated_at: at(20, 12) },
    { id: "b", title: "未完成需求", status: "doing", created_at: at(16, 13) },
    { id: "c", title: "范围之前已结束", status: "done", created_at: at(10), ended_at: at(12) },
    { id: "d", title: "当天创建当天验收", status: "done", created_at: at(19, 9), deployed_at: at(19, 14), ended_at: at(19, 18) },
  ],
  activity: { events: [{ task_id: "a", kind: "milestone_assigned", at: at(20, 10) }, { task_id: "b", kind: "claim", at: at(16, 16) }], total: 2 },
  git: { commits: [commit("a", "早上的提交", at(19, 9)), commit("b", "下午的提交", at(19, 15), ["a"])] },
});
const stop = (m, day) => m.stops.find((s) => s.day === `2026-10-${day}`);

test("each requirement gets one pin per day at its furthest step; bookkeeping stays off the map", () => {
  const m = roadmapModel(fixture());
  assert.deepEqual(m.stops.map((s) => s.day.slice(8)), ["14", "16", "17", "18", "19"]);
  assert.deepEqual(stop(m, 16).pins.map((p) => [p.task.id, p.kind]), [["a", "submit"], ["b", "claim"]]);
  const sameDay = stop(m, 19).pins.find((p) => p.task.id === "d");
  assert.equal(sameDay.kind, "accept");
  assert.deepEqual(sameDay.steps.map((s) => s.kind), ["created", "mark_deployed", "accept"]);
  assert(!m.stops.some((s) => s.pins.some((p) => p.task.id === "c")));
  assert.equal(m.dayCount, 7);
});

test("busy days sample linked and spread commits in time order and expand on demand", () => {
  const view = fixture();
  view.git.commits = Array.from({ length: 9 }, (_, i) => commit(`c${i}`, i === 0 ? "Merge branch main" : `改动 ${i}`, at(19, 9 + i), i === 6 ? ["a"] : []));
  let day = stop(roadmapModel(view), 19);
  assert.equal(day.commits.length, 9);
  assert.equal(day.shownCommits.length, 3);
  assert(day.shownCommits.some((c) => c.task_ids.length));
  assert(!day.shownCommits.some((c) => c.title.startsWith("Merge")));
  assert.deepEqual(day.shownCommits, [...day.shownCommits].sort((a, b) => a.at - b.at));
  day = stop(roadmapModel(view, { expanded: new Set(["2026-10-19T00"]) }), 19);
  assert.equal(day.shownCommits.length, 9);
  view.git.commits = view.git.commits.slice(0, 4);
  assert.equal(stop(roadmapModel(view), 19).shownCommits.length, 4, "one hidden commit is shown instead of a +1 toggle");
});

test("quiet days are counted between stops and after the last one", () => {
  const view = fixture();
  view.until = at(23);
  const m = roadmapModel(view);
  assert.deepEqual(m.stops.map((s) => s.quiet), [0, 1, 0, 0, 0]);
  assert.equal(m.tail, 3);
});

test("the road snakes: rows alternate direction and turn at the edge before the arrow", () => {
  const view = fixture();
  view.tasks = Array.from({ length: 10 }, (_, i) => ({ id: `t${i}`, title: `需求 ${i}`, status: "doing", created_at: at(1 + i * 2, 10) }));
  view.from = at(1); view.until = at(22); view.git.commits = [];
  const layout = roadmapLayout(roadmapModel(view), 1000);
  assert.deepEqual(layout.rows.map((r) => r.stops.length), [3, 3, 3, 1]);
  assert.deepEqual(layout.rows.map((r) => r.dir), [1, -1, 1, -1]);
  const [first, second] = layout.rows;
  assert(first.stops[0].x < first.stops[2].x && second.stops[0].x > second.stops[2].x);
  assert(Math.abs(second.stops[0].left - first.stops[2].left) < 1e-6, "the turn keeps the column");
  assert.equal(first.stops[0].x, first.stops[0].left + 12, "a day's node sits where the day begins");
  assert(Math.abs(second.stops[0].x - (second.stops[0].left + layout.column - 12)) < 1e-6);
  assert(layout.rows.every((r, i) => !i || r.y > layout.rows[i - 1].y + 80));
  assert.equal((layout.path.match(/ A /g) || []).length, 6);
  assert.equal(layout.tip.dir, -1);
  assert(layout.tip.x < first.stops[0].x);
  assert.equal(layout.markers.find((g) => g.vertical).quiet, 1);
  assert(layout.height > layout.rows.at(-1).y);
  assert(roadmapLayout(roadmapModel(view), 360).rows.every((r) => r.stops.length === 1));
});

test("small nodes place every requirement step and commit by time along the day's stretch", () => {
  const view = fixture();
  view.git.commits = [...Array.from({ length: 9 }, (_, i) => commit(`c${i}`, `改动 ${i}`, at(19, 9 + i))), commit("same", "同一分钟", at(19, 17))];
  const layout = roadmapLayout(roadmapModel(view), 1200);
  const day = layout.stops.find((s) => s.day === "2026-10-19");
  assert.equal(day.commitTicks.reduce((n, t) => n + t.items.length, 0), 10, "ticks show every commit, not the sample");
  assert.equal(day.shownCommits.length, 3);
  assert.equal(day.commitTicks.at(-1).items.length, 2, "simultaneous commits merge into one tick");
  const xs = day.commitTicks.map((t) => t.x);
  assert.deepEqual(xs, [...xs].sort((a, b) => (a - b) * day.dir));
  assert(xs.every((x) => (x - day.x) * day.dir > 0 && Math.abs(x - day.x) < layout.column - 20));
  assert.deepEqual(day.reqTicks.flatMap((t) => t.items).filter((e) => e.task.id === "d").map((e) => e.kind), ["created", "mark_deployed", "accept"]);
  assert.equal(day.reqTicks[0].x, day.commitTicks[0].x, "requirement and commit ticks share one clock");
  const html = roadmapHTML(view, 1200);
  assert.equal((html.match(/class="tl-tick is-commit/g) || []).length, day.commitTicks.length);
});

test("pins show the requirement's whole course and hovering can trace it along the road", () => {
  const view = fixture();
  const html = roadmapHTML(view, 1200);
  assert(html.includes("<b>10/14 提出</b> → 10/17 上线 → 10/18 验收"));
  assert(html.includes("10/14 提出 → <b>10/17 上线</b> → 10/18 验收"));
  assert(html.includes("10/16 提出 · 开发中"));
  let layout = roadmapLayout(roadmapModel(view), 1200);
  const a = layout.journeys.find((j) => j.task.id === "a"), b = layout.journeys.find((j) => j.task.id === "b");
  assert(!a.early && !a.open && a.from.x < a.to.x);
  assert(b.open && b.to.x === layout.tip.x - layout.tip.dir * 14);
  view.from = at(16);
  layout = roadmapLayout(roadmapModel(view), 1200);
  const early = layout.journeys.find((j) => j.task.id === "a");
  assert(early.early && early.from === layout.start, "requirements raised before the range start at the origin");
});

test("titles are escaped and empty ranges still draw the road", () => {
  const view = fixture();
  view.tasks[0].title = '<script>alert("x")</script>';
  const html = roadmapHTML(view, 1200);
  assert(!html.includes("<script>"));
  assert(html.includes("&lt;script&gt;"));
  assert(!/\sstyle=/.test(html), "the page CSP forbids inline style attributes");
  view.tasks = []; view.git.commits = []; view.activity.events = [];
  const empty = roadmapHTML(view, 1200);
  assert(empty.includes("tl-road-arrow") && empty.includes("暂无需求动态"));
});

test("two-hour stops split a busy day; only the first stop of a day carries its date", () => {
  const view = fixture();
  const m = roadmapModel(view, { grain: 2 });
  const day = m.stops.filter((s) => s.day === "2026-10-19");
  assert.deepEqual(day.map((s) => s.key.slice(11)), ["08", "14", "18"]);
  assert.deepEqual(day.map((s) => s.firstOfDay), [true, false, false]);
  assert.deepEqual(day.map((s) => s.quiet), [0, 0, 0]);
  assert.deepEqual(day.map((s) => s.pins.map((p) => p.kind)), [["created"], ["mark_deployed"], ["accept"]]);
  assert.deepEqual(day[0].commits.map((c) => c.title), ["早上的提交"]);
  assert.equal(m.activeDays, 5);
  assert.equal(stop(m, 16).quiet, 1);
  const layout = roadmapLayout(m, 1200), first = layout.stops.find((s) => s.key === "2026-10-19T08");
  assert.equal(first.place(at(19, 8)), first.x + first.dir * 16);
  assert(Math.abs(first.reqTicks[0].x - (first.place(at(19, 8)) + first.place(at(19, 10))) / 2) < 1e-6, "09:00 sits halfway along the 08:00–10:00 stretch");
  assert.deepEqual([7, 30, 90].map(grainFor), [2, 12, 24]);
  const html = roadmapHTML(view, 1200, { grain: 2 });
  assert(html.includes('<span class="tl-date-hour">14:00</span>'));
});

test("folded cards stay in the page so tracing can bring a requirement's other states out", () => {
  const view = fixture();
  view.tasks.push(...Array.from({ length: 5 }, (_, i) => ({ id: `n${i}`, title: `同时上线 ${i}`, status: "review", created_at: at(15, 10 + i), deployed_at: at(17, 11) })));
  const m = roadmapModel(view, { grain: 2 }), busy = m.stops.find((s) => s.key === "2026-10-17T10");
  assert.equal(busy.pins.length, 6);
  assert.equal(busy.shownPins.length + busy.foldedPins.length, 6);
  assert.equal(busy.foldedPins.length, 3);
  const html = roadmapHTML(view, 1200, { grain: 2 });
  assert.equal((html.match(/class="tl-pin is-folded"/g) || []).length, 3);
});

test("history lists every loaded change with notes, and lifecycle times from outside the range", () => {
  const view = fixture();
  view.from = at(16);
  view.activity.events.push({ task_id: "a", kind: "report", at: at(16, 10), actor: "开发组", body: "联调完成 <b>" }, { task_id: "a", kind: "mark_deployed", at: at(17, 11), actor: "运维", body: "已发布到生产" });
  const h = historyFor(view, "a");
  assert.deepEqual(h.entries.map((e) => e.kind), ["created", "report", "submit", "mark_deployed", "accept", "milestone_assigned"]);
  assert.equal(h.entries.find((e) => e.kind === "mark_deployed").actor, "运维", "a recorded event wins over the snapshot time");
  assert(h.partial, "created before the range, so earlier events are not loaded");
  const html = historyHTML(h);
  assert(html.includes("<b>进展记录</b>") && html.includes("联调完成 &lt;b&gt;") && html.includes("<b>归入里程碑</b>"));
  assert(html.includes("10/14 提出 → 10/17 上线 → 10/18 验收"));
  assert.equal(historyFor(view, "missing"), null);
});
