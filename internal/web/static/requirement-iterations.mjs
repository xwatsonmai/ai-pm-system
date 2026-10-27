import { renderMarkdown } from "./markdown.mjs";
const esc = (v) =>
  String(v ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const short = (v) =>
  String(v || "")
    .replace(/^\w+_/, "")
    .slice(0, 6)
    .toUpperCase();
const states = {
  clarify: "需求评审",
  ready: "待认领",
  doing: "处理中",
  pending_release: "待上线",
  review: "待验收",
  done: "已验收",
  closed: "已关闭",
  rejected: "已拒绝",
  canceled: "已取消",
};
const date = (v) =>
  new Date(v).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
function files(task) {
  return (task.attachments || [])
    .map(
      (f) =>
        `<a class="iteration-file" href="/api/projects/${encodeURIComponent(task.project_id)}/attachments/${encodeURIComponent(f.id)}" target="_blank" rel="noopener">${esc(f.name)}</a>`,
    )
    .join("");
}
function frozen(task, label) {
  return `<details class="iteration-snapshot"><summary>${label} · ${esc(task.title)} <span class="muted small">R${task.revision}</span></summary><div class="iteration-snapshot-content"><div class="markdown-content">${renderMarkdown(task.description || "没有描述")}</div>${files(task)}${task.acceptance?.length ? `<h4>已验收的条件</h4><ul>${task.acceptance.map((v) => `<li>${esc(v)}</li>`).join("")}</ul>` : ""}${task.submission ? `<h4>上一轮交付</h4><p class="prewrap">${esc(task.submission.summary)}</p><p class="mono small muted">${esc(task.submission.sha)}</p><p class="prewrap">${esc(task.submission.tests)}</p>` : ""}${task.deployment ? `<p class="small muted">已上线：${esc(task.deployment.environment)} · ${date(task.deployment.at)}</p>` : ""}${task.review ? `<h4>验收结论</h4><p class="prewrap">${esc(task.review.note || "验收通过")}</p>` : ""}</div></details>`;
}
export function iterationContextHTML(iteration) {
  if (!iteration) return "";
  return `<section class="iteration-context" aria-label="已验收的迭代上下文"><div class="iteration-context-heading"><span class="badge">基于已验收需求</span><button type="button" class="ghost" data-action="open-task" data-id="${esc(iteration.parent_task_id)}">查看上一轮详情</button></div><p>以下保留创建迭代时的需求与交付结果，本轮只需说明新增变化。</p>${frozen(iteration.source, "上一轮已验收快照")}${iteration.baseline ? frozen(iteration.baseline, "最初需求基线") : ""}</section>`;
}
export function iterationRowsHTML(items, currentID, versions = []) {
  return items
    .map(
      (t) =>
        `<div class="iteration-history-row ${t.id === currentID ? "is-current" : ""}"><div><button type="button" class="ghost" data-action="open-task" data-id="${esc(t.id)}" ${t.id === currentID ? 'aria-current="true"' : ""}>${esc(t.title)}</button><p class="small muted">${t.iteration ? `基于 ${short(t.iteration.parent_task_id)} · ` : "最初需求 · "}${esc(versions.find((v) => v.id === t.project_version_id)?.name || "历史版本")}${t.updated_at ? ` · ${date(t.updated_at)}` : ""}</p></div><span class="badge ${esc(t.status)}">${esc(states[t.status] || t.status)}</span></div>`,
    )
    .join("");
}
export function iterationHistoryHTML(history, task, versions = []) {
  if (!history || (!history.total && task.status !== "done")) return "";
  return `<section class="detail-section iteration-history" data-task="${esc(task.id)}"><h3>迭代记录 <span class="muted small">${history.total} 轮后续迭代</span></h3>${iterationRowsHTML([history.root], task.id, versions)}<div class="iteration-history-items">${iterationRowsHTML(history.items, task.id, versions)}</div>${history.total ? "" : '<p class="context-note">可以基于这条已验收需求追加迭代，原需求和历史交付保留。</p>'}${history.has_more ? `<button type="button" data-action="iteration-more" data-id="${esc(task.id)}" data-offset="${history.next_offset}">加载更多迭代</button>` : ""}</section>`;
}
