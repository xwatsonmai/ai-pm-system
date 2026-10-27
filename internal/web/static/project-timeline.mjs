const esc = (v) =>
  String(v ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
// Requirement steps drawn on the map. A requirement gets one pin per stop showing
// the furthest step it reached there; bookkeeping events stay off the map.
const steps = {
  created: { label: "新建需求", tone: "new", rank: 1 },
  iteration_created: { label: "追加迭代", tone: "new", rank: 1 },
  requirements_reopened: { label: "重新评审", tone: "back", rank: 2 },
  reopen: { label: "重新打开", tone: "back", rank: 2 },
  ready: { label: "确认范围", tone: "progress", rank: 3 },
  requirements_confirmed: { label: "确认需求稿", tone: "progress", rank: 3 },
  claim: { label: "开始处理", tone: "progress", rank: 4 },
  request_alignment: { label: "发起开发对齐", tone: "back", rank: 4 },
  alignment_reply: { label: "产品回复对齐", tone: "progress", rank: 4 },
  resume_development: { label: "对齐完成，继续开发", tone: "progress", rank: 4 },
  reopen_claim: { label: "重新开放领取", tone: "back", rank: 4 },
  reject: { label: "退回修改", tone: "back", rank: 5 },
  rework: { label: "退回开发", tone: "back", rank: 5 },
  submit: { label: "提交测试", tone: "progress", rank: 6 },
  test_pass: {label:"测试通过",tone:"progress",rank:7},
  start_retest: {label:"开始复测",tone:"progress",rank:6},
  verify_bug: {label:"修复验证通过",tone:"done",rank:7},
  return_bug: {label:"Bug 退回修复",tone:"back",rank:5},
  bug_confirmed: {label:"Bug 确认存在",tone:"progress",rank:3},
  bug_rejection_confirmed: {label:"产品确认驳回",tone:"stop",rank:7},
  close: { label: "关闭需求", tone: "stop", rank: 7 },
  cancel: { label: "取消需求", tone: "stop", rank: 7 },
  decline: { label: "拒绝需求", tone: "stop", rank: 7 },
  mark_deployed: { label: "已上线", tone: "ship", rank: 8 },
  accept: { label: "验收通过", tone: "done", rank: 9 },
};
const landmarks = {
  project_version_created: "开始",
  project_version_start: "开始",
  project_version_publish: "登记上线",
  project_version_complete: "结束",
  project_version_close: "关闭",
  project_version_reopen: "重新开启",
};
// Bookkeeping kinds only appear in a requirement's history panel.
const notes = { revised: "更新需求", report: "进展记录", milestone_assigned: "归入里程碑", version_assigned: "调整里程碑" };
const endKinds = { resolved:"verify_bug", done: "accept", closed: "close", rejected: "decline", canceled: "cancel" };
const terminal = new Set(Object.keys(endKinds));
const statusNames = { clarify: "需求评审", ready: "待认领", doing: "开发中", aligning: "待对齐", testing:"测试中", fixing:"待修复", retest:"待复测", verifying:"待验证", resolved:"已解决", pending_release: "待上线", review: "待验收", done: "已验收", closed: "已关闭", rejected: "已拒绝", canceled: "已取消" };
const stepLabel = (kind, task) => (kind === "created" && task.kind === "bug" ? "新建 Bug" : steps[kind].label);
const time = (v) =>
  new Date(v).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
const dayKey = (v) => {
  const d = new Date(v);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
};
const dayStart = (v) => { const d = new Date(v); d.setHours(0, 0, 0, 0); return +d; };
const daysBetween = (a, b) => Math.round((dayStart(b) - dayStart(a)) / 86400000);
const shortDay = (v) => dayKey(v).slice(5).replace("-", "/");
const weekday = (v) => new Date(v).toLocaleDateString("zh-CN", { weekday: "short" });
const HOUR = 3600000;
// Stops are local-time buckets of `grain` hours (2, 12 or 24).
const bucketStart = (at, grain) => { const d = new Date(at); d.setHours(Math.floor(d.getHours() / grain) * grain, 0, 0, 0); return +d; };
const bucketKey = (at, grain) => `${dayKey(at)}T${String(new Date(bucketStart(at, grain)).getHours()).padStart(2, "0")}`;
export const GRAINS = { 2: "每 2 小时", 12: "每半天", 24: "每天" };
export const grainFor = (days) => (days <= 7 ? 2 : days <= 30 ? 12 : 24);
const chevron = (direction) => `<svg viewBox="0 0 20 20" aria-hidden="true"><path d="${direction === "left" ? "m12 5-5 5 5 5" : "m8 5 5 5-5 5"}"/></svg>`;
const flag = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 14V2.5M4 3h7.5l-1.8 2.75L11.5 8.5H4"/></svg>';
export const PIN_LIMIT = 3, COMMIT_LIMIT = 3;
const isMerge = (c) => /^Merge\b/.test(c.title || "");

// Keeps `limit` items, highest score first, spreading equal scores across the day.
// One hidden item costs as much room as its "+1" toggle, so it is shown instead.
function pick(list, limit, score) {
  if (list.length <= limit + 1) return list;
  const chosen = new Set();
  const ranked = list.map((item, index) => ({ index, score: score(item) }));
  for (const level of [...new Set(ranked.map((r) => r.score))].sort((a, b) => b - a)) {
    const need = limit - chosen.size;
    if (need <= 0) break;
    const group = ranked.filter((r) => r.score === level);
    if (group.length <= need) group.forEach((r) => chosen.add(r.index));
    else for (let i = 0; i < need; i++) chosen.add(group[Math.floor((i + 0.5) * group.length / need)].index);
  }
  return list.filter((_, i) => chosen.has(i));
}

// Each stop is one time bucket with recorded activity, in chronological order.
export function roadmapModel(view, { expanded = new Set(), grain = 24 } = {}) {
  const inRange = (at) => at && at >= view.from && at < view.until;
  const days = new Map();
  const day = (at) => {
    const key = bucketKey(at, grain);
    if (!days.has(key)) days.set(key, { key, day: dayKey(at), at: bucketStart(at, grain), pins: [], events: [], marks: [], commits: [] });
    return days.get(key);
  };
  const recorded = new Map();
  for (const e of view.activity.events) {
    if (!e.task_id) continue;
    if (!recorded.has(e.task_id)) recorded.set(e.task_id, []);
    recorded.get(e.task_id).push(e);
  }
  const journeys = [];
  for (const task of view.tasks) {
    const snapshots = [
      { kind: "created", at: task.created_at },
      { kind: "submit", at: task.submitted_at },
      { kind: "mark_deployed", at: task.deployed_at },
      { kind: endKinds[task.status], at: task.ended_at },
    ];
    const byDay = new Map(), seen = new Set();
    for (const e of [...(recorded.get(task.id) || []), ...snapshots]) {
      if (!steps[e.kind] || !inRange(e.at)) continue;
      const key = bucketKey(e.at, grain);
      if (seen.has(`${key}:${e.kind}`)) continue;
      seen.add(`${key}:${e.kind}`);
      if (!byDay.has(key)) byDay.set(key, []);
      byDay.get(key).push({ kind: e.kind, at: e.at });
    }
    if (!byDay.size) continue;
    let first = Infinity, last = -Infinity;
    for (const list of byDay.values()) {
      list.sort((a, b) => a.at - b.at);
      const top = list.reduce((best, s) => (steps[s.kind].rank >= steps[best.kind].rank ? s : best));
      const stop = day(top.at);
      stop.pins.push({ task, kind: top.kind, at: top.at, steps: list });
      for (const s of list) stop.events.push({ task, ...s });
      first = Math.min(first, list[0].at);
      last = Math.max(last, list.at(-1).at);
    }
    // null ends mean the requirement began before the range or is still under way at its end.
    const ended = (task.ended_at && task.ended_at < view.until) || (terminal.has(task.status) && !task.ended_at);
    journeys.push({ task, from: task.created_at && task.created_at < view.from ? null : first, to: ended ? last : null });
  }
  const versions = new Map(view.versions.map((v) => [v.id, v])), marked = new Set();
  const mark = (version, label, at) => {
    if (!version || !inRange(at)) return;
    const key = `${version.id}:${label}:${bucketKey(at, grain)}`;
    if (marked.has(key)) return;
    marked.add(key);
    day(at).marks.push({ version, label, at });
  };
  for (const v of view.versions) {
    mark(v, "开始", v.started_at);
    if (v.ended_at) mark(v, v.status === "closed" ? "关闭" : "结束", v.ended_at);
  }
  for (const e of view.activity.events) {
    if (!e.task_id && landmarks[e.kind]) mark(versions.get(e.project_version_id), landmarks[e.kind], e.at);
  }
  // Array sort is stable, so equal timestamps keep the Git-provided order.
  for (const c of view.git.commits.filter((c) => inRange(c.at)).sort((a, b) => a.at - b.at)) day(c.at).commits.push(c);
  const lastDay = view.until - 1;
  const stops = [...days.values()].sort((a, b) => a.at - b.at).map((d, i, list) => {
    d.pins.sort((a, b) => a.at - b.at);
    d.events.sort((a, b) => a.at - b.at);
    d.marks.sort((a, b) => a.at - b.at);
    const open = expanded.has(d.key), prev = list[i - 1];
    const shownPins = open ? d.pins : pick(d.pins, PIN_LIMIT, (p) => steps[p.kind].rank);
    return {
      ...d,
      open,
      firstOfDay: !prev || prev.day !== d.day,
      quiet: !prev ? daysBetween(view.from, d.at) : prev.day === d.day ? 0 : daysBetween(prev.at, d.at) - 1,
      shownPins,
      foldedPins: d.pins.filter((p) => !shownPins.includes(p)),
      shownCommits: open ? d.commits : pick(d.commits, COMMIT_LIMIT, (c) => (c.task_ids?.length ? 2 : isMerge(c) ? 0 : 1)),
    };
  });
  const tail = stops.length ? daysBetween(stops.at(-1).at, lastDay) : 0;
  return { from: view.from, until: view.until, grain, stops, journeys, tail, dayCount: daysBetween(view.from, lastDay) + 1, activeDays: new Set(stops.map((s) => s.day)).size };
}

// Every loaded change to one requirement, plus its recorded lifecycle times outside the range.
export function historyFor(view, id) {
  const task = view.tasks.find((t) => t.id === id);
  if (!task) return null;
  const entries = view.activity.events.filter((e) => e.task_id === id).map((e) => ({ kind: e.kind, at: e.at, actor: e.actor || "", body: e.body || "" }));
  for (const [kind, at] of [["created", task.created_at], ["submit", task.submitted_at], ["mark_deployed", task.deployed_at], [endKinds[task.status], task.ended_at]]) {
    if (at && kind && !entries.some((e) => e.kind === kind && Math.abs(e.at - at) < 60000)) entries.push({ kind, at, actor: "", body: "" });
  }
  entries.sort((a, b) => a.at - b.at);
  return { task, entries, partial: !!view.activity.has_more || (task.created_at > 0 && task.created_at < view.from) };
}

// Fixed item heights let the layout reserve room for each row before the browser renders it.
const PIN = 58, PIN_GAP = 6, LINE = 24, DATE = 24, ABOVE_ROAD = 18, BELOW_ROAD = 14, ROW_GAP = 44, TOP = 30, BOTTOM = 44;
const NODE_IN = 12, TICK_GAP = 6;
const pinToggle = (s) => s.pins.length > PIN_LIMIT + 1;
const commitToggle = (s) => s.commits.length > COMMIT_LIMIT + 1;
const aboveHeight = (s) => (s.shownPins.length ? s.shownPins.length * (PIN + PIN_GAP) - PIN_GAP : 0) + (pinToggle(s) ? LINE + PIN_GAP : 0);
const belowHeight = (s) => DATE + (s.marks.length + s.shownCommits.length + (commitToggle(s) ? 1 : 0)) * LINE;

// A stop's stretch of road is its time span; whole days and half days read as working
// hours, widened for anything earlier or later.
function stopScale(s, grain, from, to) {
  const morning = new Date(s.at).getHours() < 12;
  const [a, b] = grain === 24 ? [9, 19] : grain === 12 ? (morning ? [8, 12] : [0, 8]) : [0, grain];
  const times = [...s.events, ...s.commits].map((e) => e.at - s.at);
  const lo = Math.min(a * HOUR, ...times), hi = Math.max(b * HOUR, ...times);
  return (at) => from + (to - from) * (at - s.at - lo) / (hi - lo);
}
// Marks closer than a few pixels merge so simultaneous events stay legible.
function cluster(items, place) {
  const out = [];
  for (const item of items) {
    const x = place(item.at), group = out.at(-1);
    if (group && Math.abs(x - group.x) < TICK_GAP) group.items.push(item);
    else out.push({ x, items: [item] });
  }
  return out;
}

// The road runs left to right, turns down at the edge, and comes back the other way.
// Each stop owns one column: a large node where its time span begins, then its activity by time.
export function roadmapLayout(model, width) {
  const narrow = width < 640;
  const margin = narrow ? 40 : 60, inset = narrow ? 16 : 26, radius = narrow ? 14 : 24;
  const perRow = Math.max(1, Math.floor((width - margin * 2) / (narrow ? 200 : 236)));
  const column = (width - margin * 2) / perRow;
  const chunks = [];
  for (let i = 0; i < model.stops.length; i += perRow) chunks.push(model.stops.slice(i, i + perRow));
  if (!chunks.length) chunks.push([]);
  let top = TOP;
  const rows = chunks.map((stops, r) => {
    const dir = r % 2 ? -1 : 1;
    const y = top + Math.max(0, ...stops.map(aboveHeight)) + ABOVE_ROAD;
    top = y + BELOW_ROAD + Math.max(DATE, ...stops.map(belowHeight)) + ROW_GAP;
    return {
      dir, y, stops: stops.map((s, j) => {
        const left = dir > 0 ? margin + j * column : width - margin - (j + 1) * column;
        const x = dir > 0 ? left + NODE_IN : left + column - NODE_IN;
        const place = stopScale(s, model.grain, x + dir * 16, x + dir * (column - 26));
        return { ...s, row: r, dir, left, x, y, place, reqTicks: cluster(s.events, place), commitTicks: cluster(s.commits, place) };
      }),
    };
  });
  const first = rows[0], last = rows.at(-1);
  const turn = (row, next) => {
    const x = row.dir > 0 ? width - inset : inset, sweep = row.dir > 0 ? 1 : 0;
    return ` H ${x - row.dir * radius} A ${radius} ${radius} 0 0 ${sweep} ${x} ${row.y + radius} V ${next.y - radius} A ${radius} ${radius} 0 0 ${sweep} ${x - row.dir * radius} ${next.y}`;
  };
  const along = (a, b) => {
    let d = `M ${a.x} ${rows[a.row].y}`;
    for (let r = a.row; r < b.row; r++) d += turn(rows[r], rows[r + 1]);
    return `${d} H ${b.x}`;
  };
  const start = { row: 0, x: inset + 6, y: first.y };
  const tip = { row: rows.length - 1, x: last.dir > 0 ? width - inset : inset, y: last.y, dir: last.dir };
  const path = along(start, { row: tip.row, x: tip.x - tip.dir * 12 });
  const stops = rows.flatMap((row) => row.stops);
  const markers = stops.map((s, i) => {
    const prev = stops[i - 1];
    if (!prev) return { quiet: s.quiet, x: (start.x + s.x) / 2, y: s.y, vertical: false, dir: 1 };
    if (prev.row !== s.row) return { quiet: s.quiet, x: prev.dir > 0 ? width - inset : inset, y: (prev.y + s.y) / 2, vertical: true, dir: 1 };
    return { quiet: s.quiet, x: s.x - s.dir * 15, y: s.y, vertical: false, dir: s.dir };
  });
  if (stops.length && model.tail > 0) {
    const end = stops.at(-1);
    markers.push({ quiet: model.tail, x: end.x + end.dir * (column - 12), y: end.y, vertical: false, dir: end.dir });
  }
  const byDay = new Map(stops.map((s) => [s.key, s]));
  const point = (at) => { const s = byDay.get(bucketKey(at, model.grain)); return { row: s.row, x: s.place(at), y: s.y }; };
  const journeys = stops.length ? model.journeys.map((j) => {
    const from = j.from === null ? start : point(j.from);
    const to = j.to === null ? { row: tip.row, x: tip.x - tip.dir * 14, y: tip.y } : point(j.to);
    return { task: j.task, from, to, early: j.from === null, open: j.to === null, d: along(from, to) };
  }) : [];
  return { width, height: top - ROW_GAP + BOTTOM, column, rows, stops, start, tip, path, markers, journeys };
}

const endWords = { done: "验收", closed: "关闭", rejected: "拒绝", canceled: "取消" };
// A requirement's whole course on one line; the part this pin stands for is bold.
function lifecycleHTML(task, kind) {
  const part = (at, word, own) => (at ? [`${own ? "<b>" : ""}${shortDay(at)} ${word}${own ? "</b>" : ""}`] : []);
  const parts = [
    ...part(task.created_at, "提出", kind === "created" || kind === "iteration_created"),
    ...part(task.deployed_at, "上线", kind === "mark_deployed"),
    ...(endWords[task.status] ? part(task.ended_at, endWords[task.status], kind === endKinds[task.status]) : []),
  ];
  const now = terminal.has(task.status) ? "" : statusNames[task.status] || "处理中";
  return `${parts.join(" → ")}${now ? `${parts.length ? " · " : ""}${now}` : ""}`;
}
// Folded pins stay in the page so hovering their requirement elsewhere can bring them out.
const pinHTML = (p, folded = false) => {
  const life = lifecycleHTML(p.task, p.kind);
  return `<button type="button" class="tl-pin${folded ? " is-folded" : ""}" data-tone="${steps[p.kind].tone}" data-action="tl-task" data-id="${esc(p.task.id)}" data-task="${esc(p.task.id)}"><span class="tl-pin-step">${esc(stepLabel(p.kind, p.task))}<span class="tl-pin-time">${time(p.at)}</span></span><span class="tl-pin-title">${esc(p.task.title)}</span><span class="tl-pin-life">${life}</span></button>`;
};
const commitHTML = (c) => {
  const linked = c.task_ids?.length ? `关联 ${c.task_ids.length} 个需求` : "";
  return `<button type="button" class="tl-commit${linked ? " is-linked" : ""}" data-action="tl-commit" data-id="${esc(c.sha)}"${linked ? ` data-tasks="${esc(c.task_ids.join(" "))}"` : ""} title="${esc(`${c.sha.slice(0, 12)} · ${time(c.at)} · ${c.author}\n${c.title}${linked ? `\n${linked}` : ""}`)}"><span class="tl-commit-sha">${esc(c.sha.slice(0, 7))}</span><span class="tl-commit-title">${esc(c.title)}</span>${linked ? `<span class="sr-only">${linked}</span>` : ""}<span class="tl-commit-time">${time(c.at)}</span></button>`;
};
const toggleHTML = (s, part, hidden, noun) => `<button type="button" class="tl-more" data-action="tl-day" data-day="${s.key}" data-part="${part}" aria-expanded="${s.open}">${s.open ? "收起" : `+${hidden} ${noun}`}</button>`;
const listTitle = (items, line) => items.slice(0, 8).map(line).join("\n") + (items.length > 8 ? `\n…共 ${items.length} 条` : "");
// Requirement ticks sit on the upper edge of the road, commit ticks on the lower edge.
function reqTickSVG(t, y) {
  const tasks = [...new Set(t.items.map((e) => e.task.id))];
  const top = t.items.reduce((best, e) => (steps[e.kind].rank >= steps[best.kind].rank ? e : best));
  const size = t.items.length > 1 ? 4.4 : 3.7, cy = y - 5;
  const single = tasks.length === 1 ? ` data-task="${esc(tasks[0])}" data-action="tl-task" data-id="${esc(tasks[0])}"` : "";
  return `<g class="tl-tick is-req" data-tone="${steps[top.kind].tone}" data-tasks="${esc(tasks.join(" "))}" data-count="${t.items.length}"${single}><title>${esc(listTitle(t.items, (e) => `${time(e.at)} ${stepLabel(e.kind, e.task)} · ${e.task.title}`))}</title><circle cx="${t.x}" cy="${cy}" r="7" class="tl-tick-hit"/><rect x="${t.x - size}" y="${cy - size}" width="${size * 2}" height="${size * 2}" rx="1.2" transform="rotate(45 ${t.x} ${cy})"/></g>`;
}
function commitTickSVG(t, y) {
  const linked = [...new Set(t.items.flatMap((c) => c.task_ids || []))];
  const single = t.items.length === 1 ? ` data-action="tl-commit" data-id="${esc(t.items[0].sha)}"` : "";
  const cy = y + 5.5;
  return `<g class="tl-tick is-commit${linked.length ? " is-linked" : ""}" data-tasks="${esc(linked.join(" "))}" data-count="${t.items.length}"${single}><title>${esc(listTitle(t.items, (c) => `${time(c.at)} ${c.sha.slice(0, 7)} ${c.title}`))}</title><circle cx="${t.x}" cy="${cy}" r="6" class="tl-tick-hit"/><circle cx="${t.x}" cy="${cy}" r="${t.items.length > 1 ? 3.4 : 2.7}" class="tl-tick-dot"/></g>`;
}
function breakSVG(g) {
  const d = g.vertical
    ? `M ${g.x - 9} ${g.y - 5} L ${g.x + 9} ${g.y - 1} M ${g.x - 9} ${g.y + 1} L ${g.x + 9} ${g.y + 5}`
    : `M ${g.x - 5} ${g.y + 10} L ${g.x - 1} ${g.y - 10} M ${g.x + 1} ${g.y + 10} L ${g.x + 5} ${g.y - 10}`;
  return `<path d="${d}" class="tl-break-cut"/><path d="${d}" class="tl-break"/>`;
}

const hourLabel = (at, grain) => (grain >= 24 ? "" : grain === 12 ? (new Date(at).getHours() < 12 ? "上午" : "下午") : time(at));

export function roadmapHTML(view, width, options) {
  const model = roadmapModel(view, options), m = roadmapLayout(model, width);
  const today = dayKey(Date.now()), pad = m.column < 220 ? 4 : 8;
  const journeys = m.journeys.map((j) => `<g class="tl-journey" data-tasks="${esc(j.task.id)}"><path d="${j.d}" class="tl-journey-line"/>${j.early ? "" : `<circle cx="${j.from.x}" cy="${j.from.y}" r="4" class="tl-journey-end"/>`}${j.open ? "" : `<circle cx="${j.to.x}" cy="${j.to.y}" r="4" class="tl-journey-end"/>`}</g>`).join("");
  const markers = m.markers.map((g) => (g.quiet > 0 ? breakSVG(g) : g.vertical ? `<path d="M ${g.x - 4} ${g.y - 2} L ${g.x} ${g.y + 2} L ${g.x + 4} ${g.y - 2}" class="tl-chevron"/>` : "")).join("");
  const nodes = m.stops.map((s) => `${s.shownPins.length || pinToggle(s) ? `<line x1="${s.x}" x2="${s.x}" y1="${s.y - ABOVE_ROAD}" y2="${s.y - 8}" class="tl-stem"/>` : ""}${s.marks.length ? `<rect x="${s.x - 7}" y="${s.y - 7}" width="14" height="14" rx="3" transform="rotate(45 ${s.x} ${s.y})" class="tl-stop-dot is-landmark"/>` : `<circle cx="${s.x}" cy="${s.y}" r="${s.firstOfDay ? 7 : 5.5}" class="tl-stop-dot${s.day === today ? " is-today" : ""}${s.pins.length ? "" : " is-quiet"}"/>`}`).join("");
  const ticks = m.stops.map((s) => `${s.commitTicks.map((t) => commitTickSVG(t, s.y)).join("")}${s.reqTicks.map((t) => reqTickSVG(t, s.y)).join("")}`).join("");
  const road = `<svg class="tl-road" width="${m.width}" height="${m.height}" viewBox="0 0 ${m.width} ${m.height}" aria-hidden="true"><path d="${m.path}" class="tl-road-bed"/>${journeys}<path d="${m.path}" class="tl-road-trail"/><path d="M ${m.tip.x} ${m.tip.y} L ${m.tip.x - m.tip.dir * 15} ${m.tip.y - 8} L ${m.tip.x - m.tip.dir * 15} ${m.tip.y + 8} Z" class="tl-road-arrow"/><circle cx="${m.start.x}" cy="${m.start.y}" r="9" class="tl-start-ring"/><circle cx="${m.start.x}" cy="${m.start.y}" r="4.5" class="tl-start-dot"/>${markers}${nodes}${ticks}</svg>`;
  const reachesNow = dayKey(view.until - 1) === today;
  const tail = model.tail > 0 && m.stops.length ? ` · ${model.tail} 天无记录` : "";
  const terminals = `<span class="tl-terminal" data-left="${m.start.x}" data-top="${m.start.y + 14}" title="${shortDay(view.from)} ${weekday(view.from)}">出发</span><span class="tl-terminal ${m.tip.dir > 0 ? "is-end-right" : "is-end-left"}" data-left="${m.tip.x + m.tip.dir * 4}" data-top="${m.tip.y + 14}">${reachesNow ? "至今" : `截至 ${shortDay(view.until - 1)}`}${tail}</span>`;
  const stops = m.stops.map((s) => {
    const isToday = s.day === today, hours = hourLabel(s.at, model.grain);
    const label = `${isToday ? "今天 " : ""}${shortDay(s.at)} ${weekday(s.at)}${hours ? ` ${hours}` : ""}`;
    const summary = `${label}：${s.pins.length} 个需求动态，${s.commits.length} 条提交`;
    const gap = s.quiet > 0 ? `<span class="tl-date-gap" title="与上一站之间 ${s.quiet} 天没有记录">隔 ${s.quiet} 天</span>` : "";
    const date = s.firstOfDay ? `<b>${isToday ? "今天" : shortDay(s.at)}</b><small>${isToday ? shortDay(s.at) : weekday(s.at)}</small>` : "";
    return `<section class="tl-stop${isToday ? " is-today" : ""}${s.dir < 0 ? " is-rtl" : ""}" data-left="${s.left + pad}" data-top="${s.y}" data-width="${m.column - pad * 2}" aria-label="${esc(summary)}"><div class="tl-pins">${s.shownPins.map((p) => pinHTML(p)).join("")}${pinToggle(s) ? toggleHTML(s, "pins", s.pins.length - s.shownPins.length, "个需求动态") : ""}${s.foldedPins.map((p) => pinHTML(p, true)).join("")}</div><div class="tl-below"><div class="tl-date">${date}${hours ? `<span class="tl-date-hour">${hours}</span>` : ""}${gap}</div>${s.marks.map((k) => `<button type="button" class="tl-landmark" data-action="tl-version" data-id="${esc(k.version.id)}" title="${esc(`${k.version.name} · ${k.label} ${time(k.at)}`)}">${flag}<span>${esc(k.version.name)}</span><small>${esc(k.label)}</small></button>`).join("")}${s.shownCommits.map(commitHTML).join("")}${commitToggle(s) ? toggleHTML(s, "commits", s.commits.length - s.shownCommits.length, "条提交") : ""}</div></section>`;
  }).join("");
  const empty = m.stops.length ? "" : `<p class="tl-empty" data-top="${m.start.y + 16}">所选时间内暂无需求动态或代码提交</p>`;
  return `<div class="tl-map" data-height="${m.height}">${road}${terminals}${empty}${stops}</div>`;
}

// The page CSP forbids inline style attributes, so positions travel as data attributes.
export function placeRoadmap(root) {
  for (const el of root.querySelectorAll("[data-left],[data-top],[data-width],[data-height]")) {
    for (const side of ["left", "top", "width", "height"]) if (el.dataset[side]) el.style[side] = `${el.dataset[side]}px`;
  }
}
// Highlights one requirement's cards, ticks, linked commits and its course along the road.
// A folded card of the requirement takes the place of another visible card in its stop, so
// every state shows without changing the stack height.
export function traceRoadmap(root, id = "") {
  const map = root.querySelector(".tl-map");
  if (!map || (map.dataset.trace || "") === id) return false;
  map.dataset.trace = id;
  map.querySelectorAll(".is-traced,.is-swapped-in,.is-swapped-out").forEach((el) => el.classList.remove("is-traced", "is-swapped-in", "is-swapped-out"));
  map.classList.toggle("is-tracing", !!id);
  if (!id) return true;
  const key = CSS.escape(id);
  for (const folded of map.querySelectorAll(`.tl-pin.is-folded[data-task="${key}"]`)) {
    const other = [...folded.parentElement.querySelectorAll(".tl-pin:not(.is-folded)")].at(-1);
    if (!other) continue;
    other.classList.add("is-swapped-out");
    folded.classList.add("is-swapped-in");
  }
  map.querySelectorAll(`[data-task="${key}"],[data-tasks~="${key}"]`).forEach((el) => el.classList.add("is-traced"));
  return true;
}
const historyLabel = (e, task) => (steps[e.kind] ? stepLabel(e.kind, task) : notes[e.kind] || "");
// The panel keeps the first entry and the latest ones when a requirement has a long history.
export function historyHTML(h) {
  const { task, entries } = h;
  const items = entries.length > 12 ? [entries[0], null, ...entries.slice(-10)] : entries;
  const rows = items.map((e) => {
    if (!e) return `<li class="tl-history-gap">… 中间还有 ${entries.length - 11} 条</li>`;
    const label = historyLabel(e, task), main = label || e.body || e.kind;
    const detail = label && e.body && e.body !== label ? `<small>${esc(e.body)}</small>` : "";
    return `<li data-tone="${steps[e.kind]?.tone || "note"}"><time>${shortDay(e.at)} ${time(e.at)}</time><span><b>${esc(main)}</b>${e.actor ? `<em>${esc(e.actor)}</em>` : ""}${detail}</span></li>`;
  }).join("");
  return `<div class="tl-history-head"><strong>${esc(task.title)}</strong><span>${esc(statusNames[task.status] || "处理中")}</span></div><p class="tl-history-life">${lifecycleHTML(task, "").replace(/<\/?b>/g, "")}</p><ol class="tl-history-list">${rows}</ol><p class="tl-history-foot">${h.partial ? "只含所选范围内已加载的记录 · " : ""}点击打开需求详情</p>`;
}
export function timelineHTML(view, grain = 24) {
  const model = roadmapModel(view, { grain });
  return `${view.git.warning ? `<div class="notice info">${esc(view.git.warning)}</div>` : ""}<div class="tl-board"><div class="tl-board-heading"><div class="tl-legend"><span class="tl-key tl-key-pin">需求卡片</span><span class="tl-key tl-key-req">需求节点</span><span class="tl-key tl-key-commit">提交节点</span><span class="tl-key tl-key-flag">${flag}里程碑</span></div><span class="tl-range-summary">${shortDay(view.from)} — ${shortDay(view.until - 1)} · ${model.dayCount} 天 · ${model.activeDays} 天有进展 · ${model.stops.length} 个节点</span></div><div class="tl-roadmap" aria-label="项目路线图：每个节点是一段时间，按时间顺序蛇形排列"></div><div class="tl-history" id="tl-history" role="tooltip" hidden></div><div class="tl-board-note">${view.activity.has_more ? "当前仅显示已加载的需求动态 · " : ""}大节点${GRAINS[grain]}一个，其后的小节点按时间排列：路上沿是需求动态，下沿是全部 Git 提交（下方列表每个节点抽取 ${COMMIT_LIMIT} 条）。卡片底部是需求从提出到上线的日期；悬停卡片会点亮它在路线上的全程、调出被折叠的其他状态，并显示完整变更历史。</div></div><div class="tl-footer"><span class="muted small">需求事件 ${view.activity.events.length}/${view.activity.total} · 已加载 ${view.git.commits.length} 条提交${view.git.synced_at ? ` · 仓库同步 ${new Date(view.git.synced_at).toLocaleString("zh-CN")}` : ""}</span>${view.activity.has_more || view.git.has_more ? '<button data-action="tl-more">加载更多记录</button>' : ""}</div>`;
}
export function mountProjectTimeline({
  api,
  getProject,
  onTask,
  onVersion,
  onCommit,
  toast,
}) {
  let project = "",
    view = null,
    request = 0,
    days = 7,
    end = dayKey(Date.now()),
    busy = false,
    layoutObserver = null,
    grain = grainFor(7);
  const expanded = new Set();
  function draw(roadmap) {
    if (!roadmap?.clientWidth || !view) return;
    hideHistory();
    roadmap.innerHTML = roadmapHTML(view, roadmap.clientWidth, { expanded, grain });
    placeRoadmap(roadmap);
    roadmap.dataset.width = roadmap.clientWidth;
  }
  // The history panel floats above the hovered card, or below it near the top of the window.
  function showHistory(id, anchor) {
    const panel = document.querySelector("#tl-history"), history = view && historyFor(view, id);
    if (!panel || !history || !anchor) return;
    panel.innerHTML = historyHTML(history);
    panel.hidden = false;
    const r = anchor.getBoundingClientRect(), w = panel.offsetWidth, h = panel.offsetHeight;
    panel.style.left = `${Math.max(8, Math.min(innerWidth - w - 8, r.left))}px`;
    panel.style.top = `${r.top - h - 10 >= 8 ? r.top - h - 10 : Math.max(8, Math.min(innerHeight - h - 8, r.bottom + 10))}px`;
    anchor.setAttribute("aria-describedby", "tl-history");
  }
  function hideHistory() {
    const panel = document.querySelector("#tl-history");
    if (panel) panel.hidden = true;
  }
  addEventListener("scroll", hideHistory, { capture: true, passive: true });
  function connectLayout(target) {
    layoutObserver?.disconnect();
    const roadmap = target.querySelector(".tl-roadmap");
    if (!roadmap) return;
    draw(roadmap);
    const follow = (event) => {
      const anchor = event.target.closest?.("[data-task]"), id = anchor?.dataset.task || "";
      if (!traceRoadmap(roadmap, id)) return;
      if (id) showHistory(id, anchor);
      else hideHistory();
    };
    const leave = () => { traceRoadmap(roadmap); hideHistory(); };
    roadmap.addEventListener("mouseover", follow);
    roadmap.addEventListener("focusin", follow);
    roadmap.addEventListener("mouseleave", leave);
    roadmap.addEventListener("focusout", (event) => { if (!roadmap.contains(event.relatedTarget)) leave(); });
    const observer = new ResizeObserver(() => {
      if (!roadmap.isConnected) { observer.disconnect(); return; }
      if (String(roadmap.clientWidth) !== roadmap.dataset.width) draw(roadmap);
    });
    layoutObserver = observer;
    observer.observe(roadmap);
  }
  function page() {
    const current = getProject();
    if (project !== current.id) {
      project = current.id;
      view = null;
      end = dayKey(Date.now());
      busy = false;
      expanded.clear();
      request++;
    }
    return `<div class="page tl-page"><div class="page-heading"><div><div class="eyebrow">PROJECT TIMELINE</div><h1>项目时间线</h1><p>沿着一条路线回看每天的需求进展与代码提交。</p></div><button data-action="tl-refresh">刷新时间线</button></div><div class="tl-toolbar"><label>查看到 <input id="tl-end" type="date" max="${dayKey(Date.now())}" value="${end}" aria-label="时间线截止日期"></label><select id="tl-days" aria-label="时间范围">${[7, 30, 90].map((n) => `<option value="${n}" ${days === n ? "selected" : ""}>最近 ${n} 天</option>`).join("")}</select><label>节点 <select id="tl-grain" aria-label="节点间隔">${Object.entries(GRAINS).map(([n, name]) => `<option value="${n}" ${grain === Number(n) ? "selected" : ""}>${name}</option>`).join("")}</select></label><div class="tl-period-nav"><button data-action="tl-earlier">${chevron("left")}更早</button><button data-action="tl-later" ${end >= dayKey(Date.now()) ? "disabled" : ""}>更晚${chevron("right")}</button></div></div><div id="project-timeline">${view ? timelineHTML(view) : '<p class="context-note">正在读取项目变化…</p>'}</div></div>`;
  }
  async function load(more = false) {
    const id = getProject().id,
      seq = ++request,
      target = document.querySelector("#project-timeline");
    if (!target) return;
    busy = true;
    try {
      const endDay = new Date(end + "T00:00:00");
      endDay.setDate(endDay.getDate() + 1);
      const from = more && view ? new Date(view.from) : new Date(endDay);
      if (!more || !view) from.setDate(from.getDate() - days);
      const until =
        more && view
          ? new Date(view.until)
          : new Date(Math.min(endDay.getTime(), Date.now() + 1));
      const params = new URLSearchParams({
        from: String(from.getTime()),
        until: String(until.getTime()),
        limit: "100",
        offset: String(
          more && view?.activity.has_more ? view.activity.next_offset : 0,
        ),
        git_offset: String(
          more && view?.git.has_more ? view.git.next_offset : 0,
        ),
        git_anchor: more ? view.git.anchor : "",
      });
      const next = await api(`/projects/${id}/timeline?${params}`);
      if (seq !== request || getProject()?.id !== id || !target.isConnected) return;
      if (more && view) {
        if (view.activity.has_more)
          next.activity.events = [
            ...view.activity.events,
            ...next.activity.events,
          ];
        else next.activity = view.activity;
        if (view.git.has_more)
          next.git.commits = [...view.git.commits, ...next.git.commits];
        else next.git = view.git;
      }
      view = next;
      project = id;
      target.innerHTML = timelineHTML(view, grain);
      connectLayout(target);
    } catch (e) {
      if (seq !== request) return;
      toast(e.message);
      if (!view)
        target.innerHTML =
          '<div class="notice">读取失败，已保存的内容不会受影响。<button data-action="tl-refresh">重试</button></div>';
    } finally {
      if (seq === request) busy = false;
    }
  }
  async function click(action, node) {
    if (action === "tl-day") {
      const { day, part } = node.dataset;
      if (expanded.has(day)) expanded.delete(day);
      else expanded.add(day);
      const roadmap = document.querySelector(".tl-roadmap");
      draw(roadmap);
      roadmap?.querySelector(`[data-action="tl-day"][data-day="${day}"][data-part="${part}"]`)?.focus();
      return;
    }
    if (action === "tl-task") return onTask(node.dataset.id);
    if (action === "tl-version") {
      if (node.dataset.id) return onVersion(node.dataset.id);
      return;
    }
    if (action === "tl-commit") return onCommit(node.dataset.id);
    if (action === "tl-more") {
      if (!busy) await load(true);
      return;
    }
    if (["tl-earlier", "tl-later"].includes(action)) {
      const d = new Date(end + "T12:00:00");
      d.setDate(d.getDate() + (action === "tl-earlier" ? -days : days));
      end = dayKey(d) > dayKey(Date.now()) ? dayKey(Date.now()) : dayKey(d);
      document.querySelector("#tl-end").value = end;
      expanded.clear();
    }
    document.querySelector('[data-action="tl-later"]').disabled =
      end >= dayKey(Date.now());
    await load();
  }
  function change(node) {
    // Changing the node interval only regroups what is already loaded.
    if (node.id === "tl-grain") {
      grain = Number(node.value);
      expanded.clear();
      const target = document.querySelector("#project-timeline");
      if (view && target) {
        target.innerHTML = timelineHTML(view, grain);
        connectLayout(target);
      }
      return;
    }
    if (node.id === "tl-end") {
      end =
        node.value && node.value <= dayKey(Date.now())
          ? node.value
          : dayKey(Date.now());
      node.value = end;
    }
    if (node.id === "tl-days") {
      days = Number(node.value);
      grain = grainFor(days);
      const select = document.querySelector("#tl-grain");
      if (select) select.value = String(grain);
    }
    document.querySelector('[data-action="tl-later"]').disabled =
      end >= dayKey(Date.now());
    view = null;
    expanded.clear();
    return load();
  }
  return {
    page,
    load,
    click,
    change,
    refreshIfNeeded() {
      if (project !== getProject()?.id) {
        project = getProject().id;
        view = null;
        busy = false;
        expanded.clear();
        request++;
        end = dayKey(Date.now());
        const root = document.querySelector("#project-timeline");
        if (root)
          root.innerHTML = '<p class="context-note">正在读取项目变化…</p>';
      }
      if (!view && !busy) load();
    },
  };
}
