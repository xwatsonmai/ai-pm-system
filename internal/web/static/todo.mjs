import { BASE, appURL } from "./base.mjs";
// The to-do page: what IM cards and the workspace link to. It works on a
// phone inside an IM app as well as on a desktop, and every action goes
// through the same APIs as the workspace.
import { createID as id } from "./id.mjs";
import { renderMarkdown } from "./markdown.mjs";
import { requirementReviewHTML, mountRequirementReview } from "./requirement-review.mjs";

const $ = (s, root = document) => root.querySelector(s);
const escape = (value) => String(value ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
const date = (value) => (value ? new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value)) : "");
const root = $("#todo-app");
const params = new URLSearchParams(location.search);
const reviewKinds = ["review_reply", "review_confirm", "bug_rejection", "alignment_reply"];
let session = {};

async function api(path, body, method) {
  const response = await fetch(appURL("/api" + path), { method: method || (body ? "POST" : "GET"), headers: body ? { "Content-Type": "application/json" } : {}, body: body ? JSON.stringify(body) : undefined });
  const data = await response.json().catch(() => ({}));
  if (response.status === 401) {
    signIn();
  }
  if (!response.ok) {
    const error = new Error(response.status === 409 ? "内容已更新，请刷新页面后再处理" : data.error || "请求失败");
    error.status = response.status;
    throw error;
  }
  return data;
}

const workbench = (task) => appURL(task ? `/?task=${encodeURIComponent(task.id)}` : "/");
const field = (label, control, hint = "") => `<label class="todo-field"><span>${escape(label)}</span>${control}${hint ? `<small class="hint">${escape(hint)}</small>` : ""}</label>`;

function banner() {
  if (params.get("error")) return `<p class="todo-banner error" role="alert">${escape(params.get("error"))}</p>`;
  if (params.get("bound")) return `<p class="todo-banner ok" role="status">绑定成功，之后的待办会推送给你，也可以在卡片里直接处理。</p>`;
  return "";
}

function show(content) {
  root.innerHTML = `<header class="todo-top"><a class="todo-brand" href="${escape(appURL("/todo"))}"><span class="todo-mark" aria-hidden="true">智</span><span>我的待办</span></a><a class="todo-link" href="${escape(appURL("/"))}">进入工作台</a></header>${banner()}<main class="todo-main">${content}</main>`;
}

function signIn() {
  const back = location.pathname.slice(BASE.length);
  const href = appURL(session.login === "redirect" ? `/auth/login?return_to=${encodeURIComponent(back)}` : `/?return_to=${encodeURIComponent(back)}`);
  const text = session.must_change_password ? "请先在工作台设置自己的密码，再回来处理。" : "登录后查看发给你的待办。";
  show(`<section class="todo-card todo-empty"><h1>${session.must_change_password ? "请先修改密码" : "请先登录"}</h1><p>${text}</p><a class="todo-button primary" href="${escape(href)}">${session.must_change_password ? "去修改密码" : "去登录"}</a></section>`);
}

function row(t) {
  return `<a class="todo-row${t.open ? "" : " closed"}" href="${escape(appURL("/todo"))}/${escape(t.id)}"><span class="todo-kind">${escape(t.title)}</span><strong>${escape(t.subject)}</strong><span class="hint">${escape(t.project)} · ${date(t.created_at)}${t.open ? "" : " · " + escape(t.done || "已处理")}</span></a>`;
}

async function list() {
  const closed = params.get("tab") === "closed";
  const [todos, channels] = await Promise.all([api(closed ? "/todos?status=closed" : "/todos"), api("/me/channels").catch(() => ({ items: [] }))]);
  const tabs = `<nav class="todo-tabs" aria-label="待办筛选"><a href="${escape(appURL("/todo"))}" ${closed ? "" : 'aria-current="page"'}>待处理</a><a href="${escape(appURL("/todo"))}?tab=closed" ${closed ? 'aria-current="page"' : ""}>已处理</a></nav>`;
  const items = todos.items.length ? `<div class="todo-list">${todos.items.map(row).join("")}</div>` : `<section class="todo-card todo-empty"><p>${closed ? "还没有已处理的待办。" : "没有等你处理的事项。"}</p></section>`;
  const notice = channels.items.length
    ? `<section class="todo-card todo-channels"><h2>消息通知</h2>${channels.items
        .map(
          (c) =>
            `<div class="todo-channel"><div><strong>${escape(c.label)}</strong><p class="hint">${c.bound ? `已绑定，待办会推送到${escape(c.label)}，通过、确认这类操作可以在卡片里直接完成。` : `绑定后，新的待办会推送到${escape(c.label)}。会跳转到${escape(c.label)}授权，不需要填写账号。`}</p></div>${c.bound ? `<button type="button" data-unbind="${escape(c.name)}" data-label="${escape(c.label)}">解除绑定</button>` : `<a class="todo-button primary" href="${escape(appURL(c.bind_url))}">绑定${escape(c.label)}</a>`}</div>`,
        )
        .join("")}</section>`
    : "";
  show(`<h1 class="todo-title">我的待办</h1>${tabs}${items}${notice}`);
}

function facts(todo, task) {
  const kind = task.kind === "bug" ? (task.parent_requirement_id ? "需求测试 Bug" : "平台 Bug") : "需求";
  return `<div class="todo-facts"><span>${escape(todo.project)}</span><span>${kind}</span><span>R${task.revision}</span>${(todo.fields || []).filter((f) => f.label !== "类型").map((f) => `<span>${escape(f.label)} ${escape(f.value)}</span>`).join("")}</div>`;
}

function requirement(task) {
  const acceptance = (task.acceptance || []).length ? `<h3>验收条件</h3><ol>${task.acceptance.map((a) => `<li>${escape(a)}</li>`).join("")}</ol>` : "";
  return `<details class="todo-card todo-source"><summary>需求内容</summary><div class="markdown">${renderMarkdown(task.description || "")}</div>${acceptance}</details>`;
}

const actionForm = (name, title, body, buttons) => `<form class="todo-card todo-form" data-form="${name}"><h2>${escape(title)}</h2>${body}<p class="form-error" role="alert"></p><div class="todo-actions">${buttons}</div></form>`;

function testingForms(todo, task) {
  const sub = task.submission || {};
  const verify = todo.kind === "verify";
  const retest = todo.kind === "retest";
  const action = retest ? "start_retest" : verify ? "verify_bug" : "test_pass";
  const title = retest ? "开始复测" : verify ? "验证 Bug 修复" : "确认测试结果";
  const body =
    (retest ? '<p class="hint">填写已合入全部 Bug 修复的需求分支提交 SHA，平台会校验版本包含关系。</p>' : "") +
    field("实际测试版本", `<input name="sha" required value="${escape(sub.sha)}" ${retest ? "" : "readonly"} autocomplete="off" spellcheck="false">`) +
    field("测试环境", `<input name="environment" required value="${escape(sub.environment)}">`) +
    field("测试说明与证据", '<textarea name="note" required rows="4" placeholder="说明验证过程、结果和证据链接"></textarea>');
  const bug = task.kind === "bug" && (verify || !task.parent_requirement_id);
  const secondary = retest ? "" : bug ? '<button type="button" data-reveal="return_bug">修复无效，退回</button>' : `<a class="todo-button" href="${workbench(task)}">提起 Bug</a>`;
  let html = actionForm(action, title, body, `<button class="primary" type="submit">${retest ? "开始复测" : verify ? "验证通过" : "测试通过"}</button>${secondary}`);
  if (bug) html += actionForm("return_bug", "退回修复", field("退回原因", '<textarea name="body" required rows="3" placeholder="说明哪里仍有问题，开发会按此继续修复"></textarea>'), '<button class="danger" type="submit">确认退回</button>').replace("<form ", "<form hidden ");
  return html;
}

function releaseForms(task) {
  const sub = task.submission || {};
  return (
    actionForm("mark_deployed", "确认已上线", field("上线版本", `<input value="${escape(sub.sha)}" readonly>`) + field("上线环境", `<input name="environment" required value="">`, "填写实际上线的环境，例如 production") + field("上线说明", '<textarea name="body" required rows="3" placeholder="上线结果或证据链接"></textarea>'), '<button class="primary" type="submit">确认已上线</button><button type="button" data-reveal="rework">退回开发</button>') +
    actionForm("rework", "退回开发", field("退回原因", '<textarea name="body" required rows="3"></textarea>'), '<button class="danger" type="submit">确认退回</button>').replace("<form ", "<form hidden ")
  );
}

function acceptanceForm(task) {
  const checks = (task.acceptance || []).map((a, i) => `<label class="todo-check"><input type="checkbox" name="check_${i}"><span>${escape(a)}</span></label>`).join("");
  return actionForm("acceptance", "业务验收", `<fieldset class="todo-checks"><legend>逐项确认验收条件</legend>${checks || '<p class="hint">这条需求没有验收条件。</p>'}</fieldset>` + field("验收环境", `<input value="${escape(task.deployment?.environment)}" readonly>`) + field("验收说明", '<textarea name="note" rows="3" placeholder="不通过时必须填写原因"></textarea>'), '<button class="primary" type="submit" name="decision" value="accept">验收通过</button><button class="danger" type="submit" name="decision" value="reject">验收不通过</button>');
}

async function detail(todoID) {
  const { todo, task, recipient } = await api(`/todos/${encodeURIComponent(todoID)}`);
  const head = `<a class="todo-back" href="${escape(appURL("/todo"))}">← 我的待办</a><section class="todo-head"><span class="todo-kind">${escape(todo.title)}</span><h1>${escape(task.title)}</h1>${facts(todo, task)}${recipient ? "" : '<p class="hint">这项待办发给了其他成员，你也可以查看和处理。</p>'}</section>`;
  if (!todo.open) {
    show(`${head}<section class="todo-card todo-empty"><strong>${escape(todo.done || "已处理")}</strong><p class="hint">这项待办已经结束。</p><a class="todo-button" href="${workbench(task)}">在工作台查看</a></section>`);
    return;
  }
  if (reviewKinds.includes(todo.kind)) {
    show(`${head}<section class="todo-review">${requirementReviewHTML(task)}</section>`);
    document.body.classList.add("todo-chat");
    const leave = () => location.assign(workbench(task));
    mountRequirementReview($(".todo-review .requirement-review"), { task, api, userID: session.user?.id || "local-owner", onDetails: leave, onResolve: leave, onConfirmed: () => location.reload() });
    return;
  }
  const body =
    { test: testingForms, verify: testingForms, retest: testingForms }[todo.kind]?.(todo, task) ??
    (todo.kind === "release" ? releaseForms(task) : todo.kind === "acceptance" ? acceptanceForm(task) : `<section class="todo-card"><h2>等待开发认领</h2><p>开发在自己的 Agent 客户端（例如 Codex、Claude Code）里通过个人 MCP 认领这条需求，认领后这项待办会自动结束。</p><a class="todo-button" href="${workbench(task)}">在工作台查看</a></section>`);
  show(head + body + requirement(task));
  root.addEventListener("click", (e) => {
    const name = e.target.closest("[data-reveal]")?.dataset.reveal;
    if (name) {
      const form = $(`form[data-form="${name}"]`);
      form.hidden = false;
      form.querySelector("textarea, input")?.focus();
    }
  });
  root.addEventListener("submit", async (e) => {
    e.preventDefault();
    const form = e.target;
    const data = Object.fromEntries(new FormData(form));
    const button = e.submitter;
    const error = form.querySelector(".form-error");
    form.dataset.requestId ||= id();
    const base = { request_id: form.dataset.requestId, version: task.version };
    let payload;
    switch (form.dataset.form) {
      case "test_pass":
      case "verify_bug":
      case "start_retest":
        payload = { ...base, action: form.dataset.form, body: data.note, test_verification: { revision: task.revision, sha: data.sha.trim(), environment: data.environment.trim(), note: data.note } };
        break;
      case "return_bug":
      case "rework":
        payload = { ...base, action: form.dataset.form, body: data.body };
        break;
      case "mark_deployed":
        payload = { ...base, action: "mark_deployed", deployment: { revision: task.revision, sha: task.submission?.sha, environment: data.environment.trim(), body: data.body } };
        break;
      case "acceptance": {
        const decision = button?.value || "accept";
        const checks = (task.acceptance || []).map((_, i) => (data["check_" + i] ? "passed" : "unverified"));
        if (decision === "accept" && checks.some((c) => c !== "passed")) return void (error.textContent = "请逐项确认所有验收条件都已通过");
        if (decision === "reject" && !data.note?.trim()) return void (error.textContent = "请填写不通过的原因");
        form.dataset.requestId = form.dataset.decision === decision ? form.dataset.requestId : id();
        form.dataset.decision = decision;
        payload = { request_id: form.dataset.requestId, version: task.version, action: decision, review: { revision: task.revision, sha: task.submission?.sha, environment: task.deployment?.environment, note: data.note || "", checks } };
        break;
      }
    }
    for (const b of form.querySelectorAll("button")) b.disabled = true;
    error.textContent = "";
    try {
      await api(`/tasks/${encodeURIComponent(task.id)}/actions`, payload);
      location.reload();
    } catch (err) {
      error.textContent = err.message;
      for (const b of form.querySelectorAll("button")) b.disabled = false;
    }
  });
}

root.addEventListener("click", async (e) => {
  const button = e.target.closest("[data-unbind]");
  if (!button || !confirm(`解除绑定后，不再向${button.dataset.label}推送待办。确定解除吗？`)) return;
  button.disabled = true;
  try {
    await api(`/me/channels/${encodeURIComponent(button.dataset.unbind)}`, null, "DELETE");
    location.assign(appURL("/todo"));
  } catch (err) {
    button.disabled = false;
    alert(err.message);
  }
});

try {
  session = await (await fetch(appURL("/api/auth/session"))).json();
  if (session.enabled && (!session.authenticated || session.must_change_password)) signIn();
  else {
    const match = location.pathname.slice(BASE.length).match(/^\/todo\/(td_[0-9a-f_]+)$/);
    await (match ? detail(match[1]) : list());
  }
} catch (e) {
  show(`<section class="todo-card todo-empty"><h1>暂时无法读取待办</h1><p>${escape(e.message)}</p></section>`);
}
