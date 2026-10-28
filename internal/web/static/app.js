import { createID as id } from "./id.mjs";
import { chatConversations, conversationID } from "./conversations.mjs";
import { renderMarkdown } from "./markdown.mjs";
import { patchHTML, mergeRunRefresh } from "./dom-update.mjs";
import { requirementFormHTML, mountRequirementEditor } from "./requirement-editor.mjs";
import { requirementReviewHTML, mountRequirementReview } from "./requirement-review.mjs";
import { reviewAttention, taskActive, taskTerminated, taskFinished } from "./requirement-review-state.mjs";
import { requirementIntakeCards, mergeIntakes } from "./requirement-intake.mjs";
import { iterationContextHTML, iterationHistoryHTML, iterationRowsHTML } from './requirement-iterations.mjs';
import {mountProjectTimeline} from './project-timeline.mjs';
import {projectVersionsPage,mountProjectVersions,versionName,versionOptions} from './project-versions.mjs';
const busyIntakes = new Set();
let requirementEditor = null;
let requirementReview = null;
import { streamRun, waitForPoll } from "./run-stream.mjs";

const $ = (s, root = document) => root.querySelector(s);
const $$ = (s, root = document) => [...root.querySelectorAll(s)];
const escape = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const short = (value) =>
  value ? value.replace(/^\w+_/, "").slice(0, 6).toUpperCase() : "—";
const date = (value) =>
  value
    ? new Intl.DateTimeFormat("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      }).format(new Date(value))
    : "尚未同步";
const states = {
  clarify: "需求评审",
  ready: "待认领",
  doing: "处理中",
  aligning: "待对齐",
  testing: "测试中", fixing: "待修复", retest: "待复测", verifying: "待验证", resolved: "已解决",
  pending_release: "待上线",
  review: "待验收",
  done: "已验收",
  rejected: "已拒绝",
  closed: "已关闭",
  canceled: "已取消",
};
const priorities = { now: "现在做", next: "接下来做", later: "暂存" };
const kindNames = {
  question: "项目问答",
  progress: "进度分析",
  requirements: "需求整理",
  requirement_review: "需求评审",
  triage: "Bug 初诊",
  knowledge: "项目概况",
};
const runStates = {
  queued: "等待分析",
  running: "正在分析",
  succeeded: "分析完成",
  failed: "分析失败",
  stopped: "已停止",
  interrupted: "运行已中断",
};
const paths = {
  overview: "项目概览",
  tasks: "需求池",
  versions: "项目版本",
  timeline:"项目时间线",
  chat: "项目问答",
  reviews: "验收与交付",
  code: "代码与变更",
  settings: "项目设置",
  mcp: "我的 MCP",
  platform: "平台设置",
};
const icons = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  list: '<path d="M8 6h13M8 12h13M8 18h13M3 6h1M3 12h1M3 18h1"/>',
  spark:
    '<path d="m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5L12 3ZM20 2v4M18 4h4"/>',
  check:
    '<rect x="4" y="3" width="16" height="18" rx="3"/><path d="m8 12 3 3 5-6"/>',
  code: '<path d="m8 6-6 6 6 6m8-12 6 6-6 6M14 3l-4 18"/>',
  settings:
    '<path d="M4 7h16M4 17h16"/><circle cx="9" cy="7" r="3" fill="currentColor"/><circle cx="15" cy="17" r="3" fill="currentColor"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  chevron: '<path d="m8 10 4 4 4-4"/>',
  tick: '<path d="m5 12 4 4L19 6"/>',
  close: '<path d="m6 6 12 12M18 6 6 18"/>',
  arrow: '<path d="M5 12h14m-5-5 5 5-5 5"/>',
  refresh:
    '<path d="M20 7v5h-5M4 17v-5h5M5 8a8 8 0 0 1 14-2l1 2M4 16l1 2a8 8 0 0 0 14-2"/>',
  folder:
    '<path d="M3 7V5a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"/>',
  menu: '<path d="M4 6h16M4 12h16M4 18h16"/>',
  paperclip:
    '<path d="m8 13 6-6a3 3 0 0 1 4 4l-8 8a5 5 0 0 1-7-7l8-8a7 7 0 0 1 10 10l-8 8"/>',
  send: '<path d="m3 11 18-8-8 18-2-8-8-2ZM11 13l10-10"/>',
  edit: '<path d="m15 5 4 4M4 20l4-1L20 7a2.8 2.8 0 0 0-4-4L4 15v5Z"/>',
  trash: '<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7"/>',
  copy: '<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/>',
};
const icon = (name) =>
  `<svg class="icon" viewBox="0 0 24 24" aria-hidden="true">${icons[name] || icons.list}</svg>`;
const reviewInfo = task => reviewAttention(task, state.context?.requirement_reviews?.find(v => v.task_id === task.id));
const needsAttention = task => !taskFinished(task) && (task.blocker || ["testing", "retest", "verifying", "pending_release", "review"].includes(task.status) || reviewInfo(task)?.attention);
const pendingConfirmations = () => (state.context?.tasks || []).filter(t => ["待确认","待对齐","驳回待确认"].includes(reviewInfo(t)?.label)).length;
const reviewNavBadge = () => pendingConfirmations() ? `<span class="nav-count" title="${pendingConfirmations()} 个需求待确认或对齐">${pendingConfirmations()}</span>` : "";
const status = (task) => {
  const info = reviewInfo(task);
  if (info?.attention) return `<button type="button" class="badge review-attention ${["待确认", "待对齐"].includes(info.label) ? "pending" : "failed"}" data-action="requirement-review" data-id="${escape(task.id)}" title="${escape(info.detail)}"><i class="status-icon clarify"></i>${info.label}<span class="review-attention-detail">${info.detail}</span></button>`;
  return `<span class="badge ${escape(task.status)}"><i class="status-icon ${escape(task.status)}"></i>${info?.label || states[task.status]}</span>`;
};
const field = (label, control, hint = "") => {
  const fieldID = "field-" + id();
  const input = control.replace(
    /<(input|textarea|select)\b/,
    `<$1 id="${fieldID}"`,
  );
  return `<div class="field"><label for="${fieldID}">${label}</label>${input}${hint ? `<p class="hint">${hint}</p>` : ""}</div>`;
};
const empty = (title, detail = "", button = "") =>
  `<div class="empty"><div class="empty-symbol">${icon("folder")}</div><h3>${title}</h3><p>${detail}</p>${button}</div>`;
const action = (name, text, cls = "", attrs = "") =>
  `<button type="button" class="${cls}" data-action="${name}" ${attrs}>${text}</button>`;
let state = {
  auth: null,
  platform: {},
  platformProjects: [],
  admins: [],
  accounts: [],
  projects: [],
  profiles: [],
  project: null,
  context: null,
  runs: [],
  collaborations: [],
  intakes: [],
  page: "overview",
  search: "",
  filter: "",
  kind: "",
  versionFilter: "",
  versionGrouping: true,
  selectedRun: "",
  switchingProject: false,
  busy: false,
  files: [],
};
const drafts = new Map();
const projectVersionUI=mountProjectVersions({api,modal,closeModal,getContext:()=>state.context,getProject:()=>state.project,getUser:()=>state.auth?.user?.id||'local-owner',refresh:async()=>{await refresh();render();},onTask:openTask,onCreate:()=>taskForm(),onPool:async filter=>{await navigate('tasks');state.versionFilter=filter;renderPage();},toast});
const timelineUI = mountProjectTimeline({
  api, getProject: () => state.project, onTask: openTask,
  onVersion: id => projectVersionUI.open(id),
  onCommit: async sha => {
    const projectID = state.project.id;
    const result = await api(`/projects/${projectID}/code?action=diff&ref=${encodeURIComponent(sha)}`);
    if (state.project.id !== projectID || state.page !== 'timeline') return;
    modal('Git 提交 · ' + sha.slice(0, 12), `<pre class="code-block">${escape(result.text)}</pre>${result.truncated ? '<p class="hint">提交内容较多，当前显示部分内容。</p>' : ''}`, {drawer: true});
  }, toast,
});
const chatSelections = new Map();
let chatViewRevision = 0;
let conversationMutationRevision = 0;
const deletedConversations = new Set();
const conversationKey = (project, conversation) => `${project}:${conversation}`;
const chatScope = (projectID = state.project?.id) => `${state.auth?.user?.id || "local-owner"}:${projectID}`;
function selectedConversation(projectID = state.project?.id) {
  const scope = chatScope(projectID);
  if (!chatSelections.has(scope)) {
    const saved = localStorage.getItem(`pm-chat:${scope}`);
    const conversations = chatConversations(state.runs);
    chatSelections.set(scope, saved === "" ? "" : conversations.find((c) => c.id === saved)?.id || conversations[0]?.id || "");
  }
  const selected = chatSelections.get(scope);
  if (selected && !state.runs.some((r) => r.kind === "question" && conversationID(r) === selected)) {
    chatSelections.set(scope, "");
    localStorage.setItem(`pm-chat:${scope}`, "");
    return "";
  }
  return selected;
}
const chatDraft = (projectID = state.project?.id, conversation = selectedConversation(projectID)) => {
  const key = `${chatScope(projectID)}:${conversation || "new"}`;
  if (!drafts.has(key))
    drafts.set(key, { text: "", files: [], sending: false, error: "" });
  return drafts.get(key);
};
function selectConversation(id) {
  chatSelections.set(chatScope(), id);
  localStorage.setItem(`pm-chat:${chatScope()}`, id);
  chatViewRevision++;
  const input = $("textarea[name=question]");
  if (input) input.value = chatDraft().text;
  updateComposer();
  updateChat();
  $(".chat-page")?.classList.remove("history-open");
  $("textarea[name=question]")?.focus();
}
let chatComposing = false;
let toastTimer;
function toast(message) {
  const el = $("#toast");
  el.textContent = message;
  el.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove("show"), 4500);
}
async function api(path, body, method) {
  const response = await fetch("/api" + path, {
    method: method || (body ? "POST" : "GET"),
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await response.json();
  if (response.status === 401) {
    sessionExpired();
  }
  if (!response.ok) {
    const error = new Error(data.error || "请求失败");
    error.status = response.status;
    throw error;
  }
  return data;
}
function sessionExpired() {
  stopRunUpdates();
  state.auth = { ...state.auth, enabled: true, authenticated: false, must_change_password: false };
  state.project = null;
  renderLogin("登录已过期，请重新登录。");
}
const signInPending = () => state.auth?.enabled && (!state.auth.authenticated || state.auth.must_change_password);
const returnTo = () => new URLSearchParams(location.search).get("return_to") || "";
const passwordFields = (current = true, label = current ? "新密码" : "密码") =>
  (current ? field("当前密码", '<input name="current_password" type="password" autocomplete="current-password" required>') : "") +
  field(label, '<input name="new_password" type="password" autocomplete="new-password" minlength="8" maxlength="128" required>', "8-128 位") +
  field("确认" + label, '<input name="confirm_password" type="password" autocomplete="new-password" required>');
function temporaryPassword() {
  const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  return Array.from(crypto.getRandomValues(new Uint32Array(12)), (n) => chars[n % chars.length]).join("");
}
function signInBody(detail) {
  const auth = state.auth || {};
  const error = `<p class="form-error" role="alert">${escape(detail)}</p>`;
  if (auth.must_change_password)
    return ["设置新密码", `${escape(auth.user?.name || "")}，请先把初始密码改为只有你知道的新密码。`,
      `<form id="password-form" class="login-form">${passwordFields()}${error}<button type="submit" class="primary login-submit">保存并继续</button></form>${action("logout", "退出登录", "ghost login-alt")}`];
  if (auth.login === "password" && auth.setup_required)
    return ["初始化平台", "创建首位平台管理员。初始化口令打印在服务启动日志中，仅本次启动有效。",
      `<form id="setup-form" class="login-form">${field("初始化口令", '<input name="token" autocomplete="off" spellcheck="false" required>')}${field("用户名", '<input name="username" autocomplete="username" required>', "2-32 位字母、数字或 . _ -")}${field("姓名", '<input name="name" autocomplete="name" placeholder="可选，默认使用用户名">')}${passwordFields(false)}${error}<button type="submit" class="primary login-submit">创建管理员并登录</button></form>`];
  if (auth.login === "password")
    return ["登录智驱", "使用平台账号登录，继续项目协作。",
      `<form id="login-form" class="login-form">${field("用户名", '<input name="username" autocomplete="username" required>')}${field("密码", '<input name="password" type="password" autocomplete="current-password" required>')}${error}<button type="submit" class="primary login-submit">登录</button></form><p class="login-note">没有账号或忘记密码时，请联系平台管理员。</p>`];
  const label = escape(auth.login_label || "统一账号");
  const query = returnTo() ? "?" + new URLSearchParams({ return_to: returnTo() }) : "";
  return ["登录智驱", `使用${label}，继续项目协作。`,
    `<a class="login-link" href="/auth/login${escape(query)}">使用${label}登录 ${icon("arrow")}</a>${detail ? error : ""}<p class="login-note">将在统一身份认证页面完成登录。</p>`];
}
function renderLogin(message = "") {
  closeModal();
  const errors = {
    invalid_state: "登录请求已失效，请重新发起登录。",
    denied: "本次登录未完成，可以重新登录。",
    invalid_code: "未收到有效授权码，请重新登录。",
    exchange_failed: "统一登录授权未完成，请重试或检查客户端回调配置。",
    userinfo_failed: "暂时无法获取登录身份，请稍后重试。",
    session_unavailable: "暂时无法建立登录会话，请稍后重试。",
  };
  const detail =
    message ||
    errors[new URLSearchParams(location.search).get("auth_error")] ||
    "";
  const [title, intro, body] = signInBody(detail);
  $("#app").innerHTML =
    `<main class="login-page"><div class="login-brand"><div class="brand-mark">智</div><div><div class="brand-name">智驱</div><div class="brand-sub">AI-NATIVE PM</div></div></div><section class="login-content"><div class="eyebrow">工作空间</div><h1>${title}</h1><p>${intro}</p>${body}</section><span class="login-footer">智驱 · AI 原生需求管理</span></main>`;
  $(".login-form input")?.focus();
}

function accountHTML() {
  if (!state.auth?.enabled)
    return '<div class="avatar" title="本地项目负责人">我</div>';
  const user = state.auth.user;
  return `<div class="account"><div class="avatar" title="${escape(user.name)}${user.login_id ? " · " + escape(user.login_id) : ""}">${escape([...user.name][0] || "我")}</div><span class="account-name">${escape(user.name)}</span><a class="account-link" href="/todo">我的待办</a>${state.auth.login === "password" ? action("change-password", "改密", "ghost", 'aria-label="修改密码"') : ""}${action("logout", "退出", "ghost", 'aria-label="退出登录"')}</div>`;
}
function closeModal() {
  if (requirementEditor?.busy || requirementReview?.busy) { toast("正在上传附件或保存需求，请稍候"); return; }
  requirementReview?.destroy();
  requirementReview = null;
  requirementEditor?.destroy();
  requirementEditor = null;
  const dialog = $("dialog");
  if (dialog) {
    dialog.close();
    dialog.remove();
  }
}
function modal(title, body, { drawer = false, document = false, review = false } = {}) {
  closeModal();
  $("#overlays").innerHTML =
    `<dialog class="${drawer ? "drawer" : review ? "review-dialog" : document ? "requirement-dialog" : ""}"><div class="modal-header"><h2>${title}</h2>${action("close", icon("close"), "ghost icon-only", 'aria-label="关闭"')}</div><div class="modal-body">${body}</div></dialog>`;
  const d = $("dialog");
  d.addEventListener("cancel", (e) => {
    e.preventDefault();
    closeModal();
  });
  d.addEventListener("click", (e) => {
    if (e.target === d) {
      const rect = d.getBoundingClientRect();
      if (
        e.clientX < rect.left ||
        e.clientX > rect.right ||
        e.clientY < rect.top ||
        e.clientY > rect.bottom
      )
        closeModal();
    }
  });
  d.showModal();
}
async function refresh() {
  state.projects = await api("/projects");
  state.auth = await api("/auth/session");
  state.platform = await api("/agent/status");
  state.profiles = isPlatformAdmin() ? await api("/profiles") : [];
  if (state.page === "platform" && isPlatformAdmin()) {
    [state.platformProjects, state.admins, state.accounts, state.channels] = await Promise.all([api("/platform/projects"), api("/platform/admins"), state.auth?.login === "password" ? api("/platform/accounts") : [], api("/platform/channels").then((r) => r.items).catch(() => [])]);
  }
  const saved = localStorage.getItem("pm-project");
  state.project =
    state.projects.find((p) => p.id === (state.project?.id || saved)) ||
    state.projects[0] ||
    null;
  if (state.project) {
    localStorage.setItem("pm-project", state.project.id);
    [state.context, state.runs, state.collaborations, state.intakes] = await Promise.all([
      api(`/projects/${state.project.id}/context`),
      api(`/projects/${state.project.id}/runs`),
      api(`/projects/${state.project.id}/collaborations`),
      api(`/projects/${state.project.id}/requirement-intakes`),
    ]);
    state.project = state.context.project;
  } else {
    state.context = null;
    state.runs = [];
    state.collaborations = [];
    state.intakes = [];
  }
}
function projectSwitcher() {
  const project = state.project;
  const name = project?.name || "选择项目";
  return `<div class="project-picker"><button type="button" id="project-switch" class="project-switch" data-action="toggle-projects" aria-label="切换项目，当前：${escape(name)}" aria-haspopup="menu" aria-expanded="false" aria-controls="project-menu"><span class="project-symbol" aria-hidden="true">${project ? escape([...name][0]) : icon("folder")}</span><span class="project-switch-label"><small>当前项目</small><strong>${escape(name)}</strong></span>${icon("chevron")}</button><div class="project-menu" id="project-menu" role="menu" aria-label="项目列表" hidden><div class="project-menu-title" role="presentation">切换项目</div><div class="project-options" role="none">${state.projects.length ? state.projects.map((p) => `<button type="button" class="project-option" role="menuitemradio" aria-checked="${p.id === project?.id}" tabindex="-1" data-action="switch-project" data-id="${escape(p.id)}" title="${escape(p.name)}"><span class="project-symbol" aria-hidden="true">${escape([...p.name][0])}</span><span class="project-option-name">${escape(p.name)}</span>${p.id === project?.id ? icon("tick") : ""}</button>`).join("") : '<p class="project-menu-empty" role="presentation">还没有项目</p>'}</div><div class="project-menu-divider" role="separator"></div><button type="button" class="project-create" role="menuitem" tabindex="-1" data-action="new-project">${icon("plus")}<span>创建新项目</span></button></div></div>`;
}
function projectMenuOpen() {
  return $("#project-switch")?.getAttribute("aria-expanded") === "true";
}
function closeProjectMenu(restoreFocus = false) {
  const trigger = $("#project-switch"),
    menu = $("#project-menu");
  if (!trigger || !menu || menu.hidden) return;
  menu.hidden = true;
  trigger.setAttribute("aria-expanded", "false");
  if (restoreFocus) trigger.focus();
}
function openProjectMenu(last = false) {
  const trigger = $("#project-switch"),
    menu = $("#project-menu");
  if (!trigger || !menu || state.switchingProject) return;
  menu.hidden = false;
  trigger.setAttribute("aria-expanded", "true");
  const items = $$("button", menu);
  (last ? items.at(-1) : $("[aria-checked=true]", menu) || items[0])?.focus();
}
async function switchProject(projectID) {
  if (state.switchingProject) return;
  closeProjectMenu(true);
  if (
    !state.projects.some((p) => p.id === projectID) ||
    state.project?.id === projectID
  )
    return;
  state.switchingProject = true;
  $("#project-switch").disabled = true;
  try {
    const [context, runs, collaborations, intakes] = await Promise.all([
      api(`/projects/${projectID}/context`),
      api(`/projects/${projectID}/runs`),
      api(`/projects/${projectID}/collaborations`),
      api(`/projects/${projectID}/requirement-intakes`),
    ]);
    stopRunUpdates();
    state.project = context.project;
    state.versionFilter = "";
    state.context = context;
    state.runs = runs;
    state.collaborations = collaborations;
    state.intakes = intakes;
    localStorage.setItem("pm-project", projectID);
    render();
    pollActiveRuns();
    $("#project-switch")?.focus();
  } finally {
    state.switchingProject = false;
    if ($("#project-switch")) $("#project-switch").disabled = false;
  }
}
function render() {
  if (signInPending()) {
    renderLogin();
    return;
  }
  const project = state.project;
  const count =
    state.context?.tasks.filter((t) => t.status === "review").length || 0;
  $("#app").innerHTML =
    `<div class="layout"><aside class="sidebar"><div class="brand"><div class="brand-mark">智</div><div><div class="brand-name">智驱</div><div class="brand-sub">AI-NATIVE PM</div></div></div>${projectSwitcher()}<div class="side-caption">工作空间</div><nav class="nav">${Object.entries(paths).filter(([key]) => !["mcp", "platform"].includes(key))
      .map(
        ([key, title], i) =>
          `<button data-action="navigate" data-page="${key}" aria-label="${title}" class="${state.page === key ? "active" : ""}">${icon(({overview:'grid',tasks:'list',versions:'grid',timeline:'code',chat:'spark',reviews:'check',code:'code',settings:'settings'})[key])}${title}${key === "tasks" ? `<span class="requirement-nav-badge">${reviewNavBadge()}</span>` : ""}${key === "reviews" && count ? `<span class="nav-count">${count}</span>` : ""}</button>`,
      )
      .join(
        "",
      )}</nav><div class="side-caption">个人与平台</div><nav class="nav">${action("navigate", icon("code") + " 我的 MCP", state.page === "mcp" ? "active" : "", 'data-page="mcp"')}${isPlatformAdmin() ? action("navigate", icon("settings") + " 平台设置", state.page === "platform" ? "active" : "", 'data-page="platform"') : ""}</nav><div class="sidebar-bottom"><div class="agent-indicator"><span class="dot ${state.platform.configured ? "live" : ""}"></span>${state.platform.configured ? "平台 Agent 已配置" : "平台 Agent 待配置"}</div><div class="local-label"><span>项目工作台</span><span>v0.1</span></div></div></aside><main class="main"><header class="topbar"><div class="breadcrumbs">${action("menu", icon("menu"), "ghost icon-only mobile-menu", 'aria-label="打开导航"')}<span>${escape(project?.name || "工作空间")}</span><span>/</span><strong>${paths[state.page]}</strong></div><div class="row">${project && !["mcp", "platform"].includes(state.page) ? action("new-task", icon("plus") + " 创建需求", "header-new") : action("new-project", icon("plus") + " 创建项目", "header-new")}${accountHTML()}</div></header><div id="page"></div></main></div>`;
  renderPage();
}
function heading(title, description = "", buttons = "") {
  return `<div class="page-heading"><div><div class="eyebrow">${escape(state.project?.name || "WORKSPACE")}</div><h1>${title}</h1><p>${description}</p></div><div class="row">${buttons}</div></div>`;
}
function renderPage() {
  if (signInPending()) {
    renderLogin();
    return;
  }
  if (state.page === "mcp") { $("#page").innerHTML = mcpPage(); loadMCP(); return; }
  if (state.page === "platform") {
    $("#page").innerHTML = isPlatformAdmin() ? platformPage() : empty("需要平台管理员权限", "请联系平台管理员。");
    return;
  }
  if (!state.project) {
    $("#page").innerHTML =
      `<div class="welcome"><div class="eyebrow">GET STARTED</div><h1>你还未加入项目</h1><p>把需求、代码和开发进展放在同一个工作空间。<br>由人确认方向，让 Agent 整理上下文、跟进变化。</p><div class="welcome-flow"><div><span>01</span><h3>连接代码仓库</h3><p>读取项目结构与真实变更</p></div><div><span>02</span><h3>整理需求与反馈</h3><p>保留目标、决定和验收条件</p></div><div><span>03</span><h3>接入开发 Agent</h3><p>通过 MCP 领取和交付需求</p></div></div>${action("new-project", icon("plus") + " 创建项目", "primary")}<div class="welcome-foot">数据保存在服务端 · 可以先管理需求，再配置模型</div></div>`;
    return;
  }
  if (state.page === "chat" && $(".chat-page")?.dataset.projectId === state.project.id) {
    updateChat();
    return;
  }
  if(state.page==='timeline'&&$('.tl-page')){timelineUI.refreshIfNeeded();return;}
  const renderer = {
    overview: overview,
    tasks: tasksPage,
    timeline:()=>timelineUI.page(),
    versions:()=>projectVersionsPage(state.context,state.auth?.user?.id||'local-owner'),
    chat: chatPage,
    reviews: reviewsPage,
    settings: settingsPage,
    code: codePage,
  }[state.page];
  $("#page").innerHTML = renderer();
  if(state.page==='timeline')timelineUI.load();
  if (state.page === "chat") {
    updateComposer();
    const feed = $(".chat-feed");
    feed.scrollTop = feed.scrollHeight;
    window.scrollTo(0, 0);
  }
  if (state.page === "settings") { loadProjectMembers(state.project.id); loadRepositoryAuth(); }
  if (state.page === "code" && state.project.sha) loadFiles();
}
function taskRow(t) {
  return `<div class="list-row" role="button" tabindex="0" data-action="open-task" data-id="${t.id}"><i class="status-icon ${t.status}"></i><div class="grow"><div class="title">${escape(t.title)}</div><div class="subtitle">${t.kind === "bug" ? (t.parent_requirement_id ? "需求测试 Bug" : "平台 Bug") : "迭代需求"} · ${short(t.id)}${t.owner ? " · " + escape(t.owner) : ""}</div></div><div class="right">${t.blocker ? '<span class="badge blocked">阻塞</span>' : status(t)}</div></div>`;
}
function overview() {
  const tasks = state.context.tasks;
  const active = tasks.filter((t) => !taskFinished(t));
  const now = [...active]
    .sort(
      (a, b) =>
        ({ now: 0, next: 1, later: 2 })[a.priority] -
        { now: 0, next: 1, later: 2 }[b.priority],
    )
    .slice(0, 7);
  const evidence = state.context.artifacts.find(
    (a) => a.kind === "analysis" && !a.task_id,
  );
  const pending = tasks.filter(
    (t) => needsAttention(t),
  );
  const events = [...state.context.events].reverse().slice(0, 6);
  return `<div class="page">${heading("项目概览", escape(state.project.description || "跟进工作，确认结果。"), action("sync", icon("refresh") + " 同步仓库", "ghost"))}<div class="stats">${[
    ["推进中", active.length, "tasks"],
    ["需要我处理", pending.length, "attention"],
    ["测试中", tasks.filter((t) => ["testing","fixing","retest","verifying"].includes(t.status)).length, "reviews"],
    ["待上线", tasks.filter((t) => t.status === "pending_release").length, "pending_release"],
    ["等待验收", tasks.filter((t) => t.status === "review").length, "reviews"],
    ["已验收", tasks.filter((t) => t.status === "done").length, "done"],
  ]
    .map(
      ([label, value, page], i) =>
        `<button class="stat" data-action="stat" data-filter="${page}"><span class="stat-label">${label}</span><span class="stat-value ${i === 1 ? "accent" : ""}">${value.toString().padStart(2, "0")}</span></button>`,
    )
    .join(
      "",
    )}</div>${state.project.sync_error ? `<div class="notice">仓库同步未完成：${escape(state.project.sync_error)}</div>` : ""}<div class="overview-grid"><div><h2 class="section-label">当前工作 ${action("navigate", "查看全部 " + icon("arrow"), "ghost", 'data-page="tasks"')}</h2>${now.length ? now.map(taskRow).join("") : empty("当前没有推进中的需求", "提出一个需求，或记录一个需要处理的问题。", action("new-task", icon("plus") + " 创建需求"))}<div class="divider"></div><h2 class="section-label">项目进度解读 ${action("analyze", "更新分析", "ghost", 'data-kind="progress"')}</h2>${evidence ? `<div class="prewrap">${escape(evidence.body)}</div><p class="hint">平台 Agent · ${date(evidence.at)} · 结论依据可在记录中查看</p>` : `<div class="context-note">${state.platform.configured ? "点击更新分析，让平台 Agent 对照需求和 Git 变化解释进度。" : "由平台管理员配置 Agent 模型后，平台 Agent 将结合需求、Git 与开发汇报分析进度。"}</div>`}</div><aside class="context-column"><section><h2 class="section-label">仓库观察</h2><p class="context-note"><strong>${escape(state.project.branch)}</strong><br>${state.project.sha ? `<span class="mono">${state.project.sha.slice(0, 12)}</span>` : "尚未读取代码"}<br>最近同步 ${date(state.project.synced_at)}</p>${action("navigate", "查看代码与变更 " + icon("arrow"), "inline-link", 'data-page="code"')}<p class="page-note">代码状态与上线记录分别记录。<br>按需求确认上线版本和环境。</p></section><section><h2 class="section-label">最近动态</h2>${events.length ? events.map((e) => `<div class="event"><p>${escape(e.body || e.kind)}</p><time>${escape(e.actor)} · ${date(e.at)}</time></div>`).join("") : '<p class="context-note">项目活动会记录在这里。</p>'}</section></aside></div></div>`;
}
function filteredTasks() {
  return state.context.tasks.filter(
    (t) =>
      (!state.filter ||
        (state.filter === "attention"
          ? needsAttention(t)
          : state.filter === "review_pending" ? ["待确认","待对齐","驳回待确认"].includes(reviewInfo(t)?.label) : t.status === state.filter)) &&
      (!state.kind || t.kind === state.kind) &&
      (!state.versionFilter||(t.project_version_id||'unplanned')===state.versionFilter) &&
      (!state.search ||
        [t.title, t.description, t.owner, t.id]
          .join(" ")
          .toLowerCase()
          .includes(state.search.toLowerCase())),
  );
}
function tasksTable() {
  const tasks=filteredTasks();
  if(!tasks.length)return empty("没有符合条件的需求","调整筛选，或创建一个新需求。");
  const table=items=>`<div class="table-scroll"><table class="task-table"><thead><tr><th class="task-checkbox">选择</th><th>状态</th><th>需求</th><th>优先级</th><th>负责人</th><th>更新</th></tr></thead><tbody>${items.map(t=>`<tr tabindex="0" data-action="open-task" data-id="${t.id}"><td><input type="checkbox" data-plan-task value="${t.id}" aria-label="选择需求 ${escape(t.title)}" ${taskFinished(t)||t.status==='review'?'disabled':''}></td><td>${status(t)}</td><td class="task-title"><span class="badge ${t.kind==='bug'?'bug':''}">${t.kind==='bug'?(t.parent_requirement_id?'需求测试 Bug':'平台 Bug'):'迭代需求'}</span> ${escape(t.title)}${t.blocker?' <span class="badge blocked">阻塞</span>':''}<div class="task-id">${short(t.id)}${t.iteration?` · 迭代自 ${short(t.iteration.parent_task_id)}`:""} · R${t.revision} · ${escape(versionName(state.context.project_versions||[],t.project_version_id))}</div></td><td><span class="priority-mark ${t.priority}">${t.priority === "now" ? "▰▰▰" : t.priority === "next" ? "▰▰▱" : "▰▱▱"}</span> ${priorities[t.priority]}</td><td>${escape(t.owner||'未认领')}</td><td class="muted small">${date(t.updated_at)}</td></tr>`).join('')}</tbody></table></div>`;
  if(!state.versionGrouping||state.versionFilter)return table(tasks)+`<div class="queue-footer">${tasks.length} 个需求</div>`;
  const groups=[...(state.context.project_versions||[]).map(v=>({id:v.id,name:v.name})),{id:'',name:'未排期'}];
  return groups.map(g=>{const items=tasks.filter(t=>(t.project_version_id||'')===g.id);return items.length?`<section class="pv-pool-group"><div class="pv-pool-heading"><h2>${escape(g.name)} <span class="muted small">${items.length} 项</span></h2>${g.id?action('pv-open','查看版本','ghost',`data-id="${g.id}"`):''}</div>${table(items)}</section>`:''}).join('')+`<div class="queue-footer">${tasks.length} 个需求 · 以实际验收结果确认业务完成</div>`;
}
function tasksPage() {
  return `<div class="page">${heading("需求池", "集中管理迭代需求与 Bug 修复。评审在后台进行，有结论后标记待确认。", action("new-task", icon("plus") + " 创建需求", "primary"))}<div class="toolbar"><input id="task-search" placeholder="搜索标题、描述或负责人…" aria-label="搜索需求" value="${escape(state.search)}"><select id="task-filter" aria-label="筛选状态"><option value="">所有状态</option><option value="attention" ${state.filter === "attention" ? "selected" : ""}>需要我处理</option><option value="review_pending" ${state.filter === "review_pending" ? "selected" : ""}>待确认${pendingConfirmations() ? `（${pendingConfirmations()}）` : ""}</option>${Object.entries(
    states,
  )
    .map(
      ([v, l]) =>
        `<option value="${v}" ${state.filter === v ? "selected" : ""}>${l}</option>`,
    )
    .join(
      "",
    )}</select><select id="kind-filter" aria-label="筛选类型"><option value="">所有类型</option><option value="requirement" ${state.kind === "requirement" ? "selected" : ""}>迭代需求</option><option value="bug" ${state.kind === "bug" ? "selected" : ""}>Bug 修复</option></select><select id="version-filter" aria-label="筛选目标版本">${versionOptions(state.context.project_versions||[],state.versionFilter,{all:true})}</select></div><div class="pv-pool-tools">${action("pv-assign-selected","归入当前里程碑")}${action("toggle-version-group",state.versionGrouping?"切换平铺列表":"按版本分组","ghost")}</div><div id="task-table">${tasksTable()}</div></div>`;
}
function reviewsPage() {
  const tasks = state.context.tasks.filter((t) => t.status === "review");
  const releases = state.context.tasks.filter(t => t.status === "pending_release");
  const artifacts = [...state.context.artifacts, ...state.context.tasks.filter(t => t.deployment).map(t => ({...t.deployment, kind:"deployment", title:`${t.title} · 上线记录 · R${t.deployment.revision}`, source:t.deployment.source === "developer" ? "开发汇报" : t.deployment.actor}))].filter((a) =>
    ["acceptance", "deployment", "test"].includes(a.kind),
  ).sort((a,b) => b.at - a.at);
  return `<div class="page">${heading("验收与交付", "对照具体版本验证业务结果。", action("navigate", "按项目版本组织上线", "", 'data-page="versions"') + action("package", "建立验收包") + action("evidence", "添加交付证据", "primary"))}${testingQueueHTML()}<h2 class="section-label">等待上线 <span class="muted small">${releases.length} 个需求</span></h2><div class="review-list">${releases.length ? releases.map(t => `<div class="review-item"><div class="grow"><div class="title">${escape(t.title)}</div><div class="small muted">开发已完成 · ${escape(t.submission?.sha || "")} · R${t.revision}</div></div>${action("mark-deployed", "确认已上线", "", `data-id="${t.id}"`)}</div>`).join("") : empty("暂时没有等待上线的需求", "测试通过后进入这里，确认实际上线后再验收。")}</div><div class="divider"></div><h2 class="section-label">等待业务验收 <span class="muted small">${tasks.length} 个需求</span></h2><div class="review-list">${tasks.length ? tasks.map((t) => `<div class="review-item"><input type="checkbox" class="package-select" value="${t.id}" aria-label="选择 ${escape(t.title)}"><div class="grow"><div class="title">${escape(t.title)}</div><div class="small muted">${escape(t.submission?.sha || "")} · ${escape(t.deployment?.environment || "上线环境未说明")} · R${t.revision}</div></div>${action("review", "开始验收", "", 'data-id="' + t.id + '"')}</div>`).join("") : empty("暂时没有等待验收的需求", "当前版本确认已上线后，会出现在这里。")}</div><div class="divider"></div><h2 class="section-label">验收包与交付证据</h2>${
    artifacts.length
      ? artifacts
          .map(
            (a) =>
              `<div class="record"><div class="record-header"><span>${escape(a.title || a.kind)} · ${escape(a.source)}</span><time>${date(a.at)}</time></div><div class="small mono muted">${escape(a.environment)} ${escape(a.sha)}</div><div class="prewrap">${escape(a.body)}</div>${
                a.kind === "acceptance" && Array.isArray(a.data)
                  ? a.data
                      .map((snapshot) => {
                        const current = state.context.tasks.find(
                          (t) => t.id === snapshot.id,
                        );
                        const stale =
                          !current ||
                          current.revision !== snapshot.revision ||
                          current.submission?.sha !== snapshot.sha;
                        return `<div class="row small">${action("open-task", escape(current?.title || snapshot.id), "ghost", `data-id="${snapshot.id}"`)}<span class="badge ${stale ? "blocked" : current.status}">${stale ? "版本已变化，需复核" : states[current.status]}</span></div>`;
                      })
                      .join("")
                  : ""
              }</div>`,
          )
          .join("")
      : '<p class="context-note">尚未提供部署证据，环境状态为未知。</p>'
  }</div>`;
}
function runProcessHTML(r, open) {
  const active = ["running", "queued"].includes(r.status);
  if (!r.process_output && !r.steps?.length) return '<div class="run-process"></div>';
  return `<div class="run-process"><details class="run-process-details" data-active="${active}" ${open ? "open" : ""}><summary><span>思考过程${active ? " · 进行中" : ""}</span>${r.steps?.length ? `<span class="process-count">${r.steps.length} 次工具调用</span>` : ""}</summary><div class="run-process-body"><div class="process-content markdown-content">${renderMarkdown(r.process_output)}</div><div class="tool-tags">${(r.steps || []).map((s) => `<span title="${escape([s.path, s.sha?.slice(0, 12), s.error].filter(Boolean).join(" · "))}">${escape(s.name)} ${s.status === "done" ? "✓" : s.status === "failed" ? "!" : "…"}</span>`).join("")}</div></div></details></div>`;
}
function codeSnapshotHTML(r) {
  const s = r.code_snapshot;
  if (!s) return "";
  if (s.status === "preparing") return '<p class="hint code-snapshot">正在准备本次分析的代码版本…</p>';
  if (s.status === "failed") return '<p class="hint code-snapshot">代码版本准备失败</p>';
  if (s.status === "no_repository") return '<p class="hint code-snapshot">本轮仅使用项目资料，项目尚未连接代码仓库。</p>';
  const incomplete = (s.submodules?.length || 0) + (s.warnings?.length || 0);
  return `<p class="hint code-snapshot" title="${escape(s.sha)}">代码版本 <span class="mono">${escape(s.sha?.slice(0, 12))}</span> · ${escape(s.ref)} · 最近同步 ${date(s.synced_at)}${incomplete ? ` · ${incomplete} 项未展开，范围已告知 Agent` : ""}</p>`;
}
function runHTML(r, processOpen = ["running", "queued"].includes(r.status)) {
  const active = ["running", "queued"].includes(r.status);
  return `<div class="answer-label">${icon("spark")} 平台 Agent ${r.trigger_type === "collaboration_result" ? `<span class="collaboration-source">协同排查结果 · ${short(r.collaboration_id)}</span>` : ""}<span class="muted">· ${date(r.started_at)}</span></div>${codeSnapshotHTML(r)}${runProcessHTML(r, processOpen)}<div class="answer-content markdown-content">${renderMarkdown(r.output)}</div><div class="form-error run-error">${escape(r.error)}</div><div class="run-status ${active ? "running" : ""}">${runStates[r.status] || r.status}${active ? " · 正在读取项目上下文" : ""}</div><div class="run-actions">${active ? action("stop-run", "停止分析", "ghost", `data-id="${r.id}"`) : r.status !== "succeeded" && r.trigger_type !== "collaboration_result" ? action("retry-run", "重新分析", "ghost", `data-id="${r.id}"`) : ""}</div>`;
}
function chatTurns() {
  return chatConversations(state.runs).find((c) => c.id === selectedConversation())?.turns || [];
}
const collaborationStates = {pending:"待认领", investigating:"排查中", blocked:"被阻塞", completed:"已返回结果", canceled:"已取消"};
const replyStates = {pending:"等待补充回答", running:"正在生成补充回答", delivered:"已补充回答", failed:"补充回答未完成", skipped:"已停止续答"};
function collaborationCards(runID) {
  return state.collaborations.filter((c) => c.origin_run_id === runID).map((c) => {
    const latest = c.updates?.at(-1);
    return `<section class="collaboration-card" aria-label="协同排查"><div class="row"><strong>${escape(c.title)}</strong><span class="badge">${collaborationStates[c.status] || escape(c.status)}</span></div><p class="small muted">${short(c.id)} · ${escape(c.environment)} · ${c.claimant_name ? escape(c.claimant_name) : "等待开发 Agent 认领"} · ${date(c.created_at)}</p>${latest ? `<p class="small prewrap">${escape(latest.body)}</p>` : ""}${c.reply ? `<p class="small">${replyStates[c.reply.status] || escape(c.reply.status)}${c.reply.error ? " · "+escape(c.reply.error) : ""}</p>` : ""}<div class="row">${action("collaboration-detail", "查看排查详情", "ghost", `data-id="${escape(c.id)}"`)}${c.status !== "canceled" && c.reply?.status !== "delivered" ? action("cancel-collaboration", "取消协同", "ghost", `data-id="${escape(c.id)}"`) : ""}${c.status === "completed" && c.reply?.status === "failed" ? action("retry-collaboration", "重试补充回答", "ghost", `data-id="${escape(c.id)}"`) : ""}</div></section>`;
  }).join("");
}
function collaborationDetail(c) {
  const r = c.result;
  return `<p><strong>${escape(c.title)}</strong> · ${collaborationStates[c.status]}</p><p class="small muted">${escape(c.id)} · ${escape(c.environment)}</p><p class="prewrap">${escape(c.brief)}</p><p class="context-note">${escape(c.scope)}</p>${(c.updates || []).map((u) => `<div class="record"><p class="small muted">${escape(u.actor)} · ${date(u.at)}</p><p class="prewrap">${escape(u.body)}</p></div>`).join("")}${r ? `<div class="record"><strong>调查结果 · ${{confirmed:"已确认", inconclusive:"尚未确定", cannot_access:"无法访问环境"}[r.outcome] || escape(r.outcome)}</strong><p class="prewrap">${escape(r.summary)}</p><p class="small muted">${escape(r.actor)} · ${escape(r.environment)} · ${escape(r.observed_at)} · 部署版本：${escape(r.deployment_version)}</p><p class="prewrap">证据：${escape(r.evidence || "未提供")}</p><p class="prewrap">未确认项：${escape(r.limitations)}</p></div>` : ""}`;
}
function seenReplyIDs() {
  try { return new Set(JSON.parse(localStorage.getItem(`pm-replies-read:${chatScope()}`) || "[]")); } catch { return new Set(); }
}
function hasUnreadReply(conversation) {
  const seen = seenReplyIDs();
  return conversation.turns.some((r) => r.trigger_type === "collaboration_result" && r.status === "succeeded" && !seen.has(r.source_event_id));
}
function markVisibleRepliesRead() {
  if (document.hidden || state.page !== "chat") return;
  const feed = $(".chat-feed");
  if (!feed || feed.scrollHeight - feed.clientHeight - feed.scrollTop > 80) return;
  const seen = seenReplyIDs();
  let changed = false;
  for (const r of chatTurns()) if (r.trigger_type === "collaboration_result" && r.status === "succeeded" && !seen.has(r.source_event_id)) { seen.add(r.source_event_id); changed = true; }
  if (changed) {
    localStorage.setItem(`pm-replies-read:${chatScope()}`, JSON.stringify([...seen].slice(-2000)));
    const list = $(".conversation-list"); if (list) patchHTML(list, conversationListHTML());
  }
}
document.addEventListener("scroll", (e) => { if (e.target.matches?.(".chat-feed")) markVisibleRepliesRead(); }, true);

let chatRefreshing = false;
async function refreshChatActivity() {
  if (chatRefreshing || document.hidden || !state.project || state.switchingProject) return;
  chatRefreshing = true;
  const projectID = state.project.id, mutation = conversationMutationRevision;
  const before = new Map(state.runs.map((r) => [r.id, r]));
  try {
    const [runs, collaborations, context, intakes] = await Promise.all([api(`/projects/${projectID}/runs`), api(`/projects/${projectID}/collaborations`), api(`/projects/${projectID}/context`), api(`/projects/${projectID}/requirement-intakes`)]);
    if (state.project?.id !== projectID || mutation !== conversationMutationRevision) return;
    state.runs = mergeRunRefresh(runs, state.runs, before).filter((r) => !deletedConversations.has(conversationKey(projectID, conversationID(r))));
    state.intakes = mergeIntakes(intakes, state.intakes);
    state.collaborations = collaborations;
    state.context = context;
    patchHTML($(".requirement-nav-badge"), reviewNavBadge());
    if (state.page === "chat") updateChat();
    pollActiveRuns();
  } catch { /* Durable results are reconciled on the next refresh. */ }
  finally { chatRefreshing = false; }
}
function chatIntro() {
  return `<div class="chat-intro"><div class="chat-icon">${icon("spark")}</div><h2>开始一个新对话</h2><p>询问项目进度、现有规则，或描述你想改进的地方。<br>Agent 会整理需求或 Bug 并建议创建；你同意后，Bug 直接待开发认领，迭代需求进入评审。</p><div class="suggestions">${["当前还有哪些工作需要我处理？", "帮我整理一个需求", "最近的代码变化影响了什么？"].map((q) => action("suggestion", q, "", `data-question="${q}"`)).join("")}</div></div>`;
}
function conversationListHTML() {
  const conversations = chatConversations(state.runs);
  return conversations.length ? conversations.map((c) => `<div class="conversation-row ${c.id === selectedConversation() ? "selected" : ""}"><button type="button" class="conversation-item" data-action="select-conversation" data-id="${escape(c.id)}" aria-current="${c.id === selectedConversation()}" title="${escape(c.title)}"><span class="conversation-title">${escape(c.title)}${hasUnreadReply(c) ? ' <span class="reply-unread">新结果</span>' : ""}</span><span class="conversation-meta">${c.active ? '<i class="conversation-active" aria-label="正在回答"></i>' : ""}${c.naming ? "正在命名…" : c.legacy ? "历史对话 · " + date(c.updatedAt) : date(c.updatedAt)}</span></button><div class="conversation-actions">${action("rename-conversation", icon("edit"), "ghost icon-only", `data-id="${escape(c.id)}" aria-label="重命名：${escape(c.title)}" title="重命名"`)}${action("delete-conversation", icon("trash"), "ghost icon-only", `data-id="${escape(c.id)}" aria-label="删除：${escape(c.title)}" title="删除对话"`)}</div></div>`).join("") : '<p class="conversation-empty">发送第一条消息后，<br>对话会出现在这里。</p>';
}
function conversationDialog(id, remove) {
  const conversation = chatConversations(state.runs).find((c) => c.id === id);
  if (!conversation) return;
  modal(remove ? "删除对话" : "重命名对话", `<form id="conversation-form" data-project-id="${escape(state.project.id)}" data-id="${escape(id)}" data-remove="${remove}">${remove ? `<p class="context-note">确认删除「${escape(conversation.title)}」？</p><p class="hint">对话将从历史列表移除，无法继续。${conversation.active ? "正在生成的回答也会停止。" : ""}</p>` : field("对话名称", `<input name="title" required maxlength="80" value="${escape(conversation.title)}" autocomplete="off">`, "最多 80 个字，保存后不会被 AI 自动名称覆盖。")}<div class="form-error" role="alert"></div><div class="modal-actions">${action("close", "取消")}<button type="submit" class="${remove ? "danger" : "primary"}">${remove ? "删除对话" : "保存名称"}</button></div></form>`);
  $("#conversation-form input")?.select();
}
function forgetConversation(projectID, id) {
  stopRunUpdates((run) => run?.project_id === projectID && conversationID(run) === id);
  deletedConversations.add(conversationKey(projectID, id));
  const key = `${chatScope(projectID)}:${id}`;
  for (const item of drafts.get(key)?.files || []) if (item.preview) URL.revokeObjectURL(item.preview);
  drafts.delete(key);
  if (state.project?.id !== projectID) return;
  const current = selectedConversation() === id;
  state.runs = state.runs.filter((r) => r.kind !== "question" || conversationID(r) !== id);
  if (current) selectConversation("");
  else updateChat();
}
function chatTurnHTML(r) {
  return `<div class="chat-turn" data-run-id="${escape(r.id)}">${r.trigger_type === "collaboration_result" ? "" : `<div class="user-question">${escape(r.prompt)}${sentAttachments(r)}</div>`}<div class="agent-answer" id="run-${escape(r.id)}">${runHTML(r)}</div><div class="requirement-intakes">${requirementIntakeCards(state.intakes,r.id,busyIntakes,state.context)}</div><div class="collaboration-items">${collaborationCards(r.id)}</div></div>`;
}
const runViews = new WeakMap();
function updateRun(element, r) {
  const signature = JSON.stringify([r.started_at, r.output, r.process_output, r.error, r.status, r.steps, r.code_snapshot, r.trigger_type, r.collaboration_id]);
  if (runViews.get(element) === signature) return false;
  const active = ["running", "queued"].includes(r.status);
  const details = $(".run-process-details", element);
  // Collapse once on completion; subsequent refreshes respect manual toggles.
  const open = details && (details.dataset.active === "true") === active ? details.open : active;
  const changed = patchHTML(element, runHTML(r, open));
  runViews.set(element, signature);
  return changed;
}
function updateChat() {
  const page = $(".chat-page");
  if (!page) return;
  const feed = $(".chat-feed", page);
  if (!feed || page.dataset.projectId !== state.project?.id) return;
  patchHTML($(".conversation-list", page), conversationListHTML());
  const selected = selectedConversation();
  const conversation = chatConversations(state.runs).find((c) => c.id === selected);
  const title = $(".current-conversation-title", page);
  if (title.textContent !== (conversation?.title || "新会话")) title.textContent = conversation?.title || "新会话";
  if (page.dataset.conversationId !== selected) {
    page.dataset.conversationId = selected;
    feed.innerHTML = chatTurns().map(chatTurnHTML).join("") || chatIntro();
    $("textarea[name=question]", page).value = chatDraft().text;
    updateComposer();
    feed.scrollTop = feed.scrollHeight;
    markVisibleRepliesRead();
    return;
  }
  const scroll = feed;
  const follow = scroll.scrollHeight - scroll.clientHeight - scroll.scrollTop < 80;
  const scrollTop = scroll.scrollTop;
  const turns = chatTurns();
  const existing = new Map($$(".chat-turn", feed).map((turn) => [turn.dataset.runId, turn]));
  let changed = false;
  if (turns.length && $(".chat-intro", feed)) {
    $(".chat-intro", feed).remove();
    changed = true;
  }
  let cursor = feed.firstElementChild;
  for (const r of turns) {
    let turn = existing.get(r.id);
    existing.delete(r.id);
    if (!turn) {
      const template = document.createElement("template");
      template.innerHTML = chatTurnHTML(r);
      turn = template.content.firstElementChild;
      changed = true;
    } else {
      if (r.trigger_type !== "collaboration_result") changed = patchHTML($(".user-question", turn), `${escape(r.prompt)}${sentAttachments(r)}`) || changed;
      changed = patchHTML($(".collaboration-items", turn), collaborationCards(r.id)) || changed;
      changed = patchHTML($(".requirement-intakes", turn), requirementIntakeCards(state.intakes,r.id,busyIntakes,state.context)) || changed;
      changed = updateRun($(".agent-answer", turn), r) || changed;
    }
    if (turn !== cursor) {
      feed.insertBefore(turn, cursor);
      changed = true;
    }
    cursor = turn.nextElementSibling;
  }
  for (const turn of existing.values()) {
    turn.remove();
    changed = true;
  }
  if (!turns.length && !$(".chat-intro", feed)) feed.innerHTML = chatIntro();
  const send = $("#chat-send", page);
  const disabled = chatDraft().sending || Boolean(conversation?.active) || (!chatDraft().text.trim() && !chatDraft().files.length);
  if (send.disabled !== disabled) send.disabled = disabled;
  if (changed) scroll.scrollTop = follow ? scroll.scrollHeight : scrollTop;
  if (follow) markVisibleRepliesRead();
}
function chatPage() {
  const draft = chatDraft();
  const turns = chatTurns();
  return `<div class="page chat-page" data-project-id="${escape(state.project.id)}" data-conversation-id="${escape(selectedConversation())}"><aside class="chat-sidebar" aria-label="对话历史">${action("new-conversation", icon("plus") + " 新会话", "new-conversation-button")}<div class="conversation-list-heading">我的对话</div><nav class="conversation-list" aria-label="我的对话">${conversationListHTML()}</nav></aside><div class="chat-main">${heading("项目问答", "回答基于项目资料和已观察到的代码。", action("toggle-history", icon("list") + " 历史对话", "ghost history-toggle"))}<div class="conversation-heading"><span class="current-conversation-title">${escape(chatConversations(state.runs).find((c) => c.id === selectedConversation())?.title || "新会话")}</span>${action("new-conversation", icon("plus") + " 新会话", "ghost mobile-new-conversation")}</div><div class="chat-feed">${turns.length ? turns.map(chatTurnHTML).join("") : chatIntro()}</div><div class="composer"><form id="chat-form" novalidate><div class="composer-box" id="chat-dropzone"><textarea name="question" rows="3" placeholder="输入问题，也可以粘贴图片或拖入文件…" aria-label="项目问题" aria-describedby="composer-help">${escape(draft.text)}</textarea><div class="draft-attachments" id="draft-attachments" aria-label="待发送附件"></div><div class="composer-bottom"><button type="button" class="ghost attach-button" data-action="attach-files" aria-label="添加图片或文件" title="图片、PDF、DOCX、文本和代码文件">${icon("paperclip")}<span>添加附件</span></button><span class="composer-keyboard">Enter 发送 · Shift+Enter 换行</span><button class="primary send-button" id="chat-send" type="submit">${icon("send")}<span>发送</span></button></div><input type="file" id="chat-files" class="hidden" multiple accept="image/png,image/jpeg,image/gif,image/webp,.pdf,.docx,text/*,.md,.log,.json,.csv,.yaml,.yml,.xml,.html,.css,.js,.ts,.tsx,.jsx,.vue,.go,.py,.sql,.sh,.java,.c,.cpp,.h,.toml,.ini,.conf"><p id="chat-error" class="form-error" role="alert"></p></div></form><p class="composer-note" id="composer-help">支持粘贴或拖入附件 · 单个 10 MB · 最多 6 个，合计 20 MB · ${state.platform.configured ? "图片需使用支持视觉的模型" : "发送前请由平台管理员配置 Agent 模型"}</p></div></div></div>`;
}
function fileSize(bytes) {
  return bytes < 1024 * 1024
    ? `${Math.max(1, Math.round(bytes / 1024))} KB`
    : `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
function sentAttachments(run) {
  if (!run.attachments?.length) return "";
  return `<div class="sent-attachments">${run.attachments
    .map((a) => {
      const url = `/api/projects/${encodeURIComponent(run.project_id)}/attachments/${encodeURIComponent(a.id)}`;
      return `<a class="sent-attachment" href="${url}" download="${escape(a.name)}" title="下载 ${escape(a.name)}">${a.kind === "image" ? `<img src="${url}?preview=1" alt="${escape(a.name)}" loading="lazy">` : icon("paperclip")}<span>${escape(a.name)}<small>${fileSize(a.size)}${a.truncated ? " · 正文已截取" : ""}</small></span></a>`;
    })
    .join("")}</div>`;
}
function updateComposer() {
  const form = $("#chat-form");
  if (!form) return;
  const draft = chatDraft();
  const textarea = $("textarea[name=question]", form);
  textarea.readOnly = draft.sending;
  textarea.style.height = "auto";
  textarea.style.height =
    Math.min(240, Math.max(84, textarea.scrollHeight)) + "px";
  $("#draft-attachments").innerHTML = draft.files
    .map(
      (a) =>
        `<div class="draft-attachment" title="${escape(a.file.name)}"><div class="attachment-preview">${a.preview ? `<img src="${a.preview}" alt="${escape(a.file.name)}">` : icon("paperclip")}</div><div class="attachment-info"><strong>${escape(a.file.name)}</strong><span>${fileSize(a.file.size)}${a.uploading ? " · 上传中…" : a.uploaded?.truncated ? " · 正文已截取" : a.uploaded ? " · 已上传" : ""}</span></div><button type="button" class="ghost attachment-remove" data-action="remove-attachment" data-id="${a.id}" aria-label="移除 ${escape(a.file.name)}" ${draft.sending ? "disabled" : ""}>${icon("close")}</button></div>`,
    )
    .join("");
  $("#chat-error").textContent = draft.error;
  $("#chat-send").disabled =
    draft.sending || chatTurns().some((r) => ["running", "queued"].includes(r.status)) || (!draft.text.trim() && !draft.files.length);
  $("#chat-send span").textContent = draft.sending ? "发送中…" : "发送";
  $("[data-action=attach-files]", form).disabled = draft.sending;
  $("#chat-files").disabled = draft.sending;
}
function addChatFiles(files) {
  const draft = chatDraft();
  if (draft.sending) return;
  const errors = [];
  for (const file of files) {
    if (
      draft.files.some(
        (a) =>
          a.file.name === file.name &&
          a.file.size === file.size &&
          a.file.lastModified === file.lastModified,
      )
    )
      continue;
    if (draft.files.length >= 6) {
      errors.push("每条消息最多添加 6 个附件");
      break;
    }
    if (!file.size) {
      errors.push(`${file.name} 是空文件`);
      continue;
    }
    if (file.size > 10 * 1024 * 1024) {
      errors.push(`${file.name} 超过 10 MB`);
      continue;
    }
    if (
      draft.files.reduce((n, a) => n + a.file.size, 0) + file.size >
      20 * 1024 * 1024
    ) {
      errors.push("附件总大小不能超过 20 MB");
      continue;
    }
    draft.files.push({
      id: id(),
      file,
      preview: /^image\/(png|jpeg|gif|webp)$/.test(file.type)
        ? URL.createObjectURL(file)
        : "",
      uploaded: null,
      uploading: false,
    });
  }
  draft.error = [...new Set(errors)].join("；");
  updateComposer();
}
async function submitChat() {
  const projectID = state.project.id;
  const conversation = selectedConversation();
  const viewRevision = chatViewRevision;
  const parent = chatTurns().at(-1)?.id || "";
  const draft = chatDraft(projectID, conversation);
  if (chatTurns().some((r) => ["running", "queued"].includes(r.status))) return;
  if (draft.sending) return;
  draft.text = $("textarea[name=question]").value;
  draft.error = "";
  if (!draft.text.trim() && !draft.files.length) {
    draft.error = "请输入问题或添加附件";
    updateComposer();
    return;
  }
  if (!state.platform.configured) {
    draft.error = "请先由平台管理员配置 Agent 模型；问题和附件已保留。";
    updateComposer();
    return;
  }
  draft.sending = true;
  updateComposer();
  try {
    for (const item of draft.files) {
      if (item.uploaded) continue;
      item.uploading = true;
      if (state.project?.id === projectID) updateComposer();
      const body = new FormData();
      body.append("file", item.file, item.file.name);
      try {
        const response = await fetch(`/api/projects/${projectID}/attachments`, {
          method: "POST",
          body,
        });
        const result = await response.json();
        if (response.status === 401) sessionExpired();
        if (!response.ok)
          throw new Error(result.error || `无法上传 ${item.file.name}`);
        item.uploaded = result;
      } finally {
        item.uploading = false;
      }
    }
    const run = await startRun(
      "question",
      "",
      draft.text.trim(),
      parent,
      draft.files.map((a) => a.uploaded.id),
      projectID,
    );
    for (const item of draft.files)
      if (item.preview) URL.revokeObjectURL(item.preview);
    draft.files = [];
    draft.text = "";
    if (state.project?.id === projectID && chatViewRevision === viewRevision) {
      selectConversation(conversationID(run));
      if (state.page === "chat") $("textarea[name=question]").value = "";
    }
  } catch (error) {
    draft.error = error.message;
  } finally {
    draft.sending = false;
    if (state.project?.id === projectID) updateComposer();
  }
}
function isPlatformAdmin() { return !state.auth?.enabled || state.auth?.platform_admin === true; }
function canManageProject(p = state.project) {
  if (isPlatformAdmin()) return true;
  const user = state.auth?.user?.id;
  return p?.memberships?.length ? p.memberships.some((m) => m.user_id === user && memberRoles(m).includes("admin")) : p?.owner_id === user;
}
function repositoryFields(p) {
  const mode = p.auth_mode || (p.id ? "server" : "none");
  const serverAllowed = !state.auth?.enabled || (p.id && mode === "server");
  return `<section class="project-form-section"><div class="form-section-heading"><h3>代码仓库</h3><span>可稍后连接</span></div>${field("Git 仓库地址", `<input name="repository" value="${escape(p.repository)}" placeholder="https://git.example.com/team/project.git" ${p.id && p.repository ? "readonly" : ""}>`)}<fieldset class="auth-methods"><legend>认证方式</legend>${[
    ["none", "公开仓库"],
    ["token", "HTTPS Token"],
    ["ssh", "SSH 密钥"],
    ["server", "服务器已有凭据"],
  ]
    .map(
      ([value, label]) =>
        `<label class="auth-method ${value === "server" ? "advanced-method" : ""}"><input type="radio" name="auth_mode" value="${value}" ${mode === value ? "checked" : ""} ${value === "server" && !serverAllowed ? "disabled" : ""}><span>${label}</span></label>`,
    )
    .join(
      "",
    )}</fieldset><div data-auth-fields="none" ${mode !== "none" ? "hidden" : ""}><p class="hint">使用无需认证的公开仓库，不读取服务器上的账号凭据。</p></div><div data-auth-fields="server" ${mode !== "server" ? "hidden" : ""}><p class="hint">沿用运行服务那台机器的 Git / SSH 配置。统一登录模式仅兼容原有仓库，新项目请使用专用凭据。</p></div><div data-auth-fields="token" ${mode !== "token" ? "hidden" : ""}>${field("Git 用户名", `<input name="git_username" autocomplete="off" placeholder="仓库平台要求的用户名">`)}${field("访问令牌", `<input type="password" name="git_token" autocomplete="new-password" placeholder="${p.credential_id ? "已配置，留空保留" : "填写具有仓库读取权限的 Token"}">`, "建议使用仅当前仓库可用的只读令牌，通过 HTTPS 连接。")}</div><div data-auth-fields="ssh" ${mode !== "ssh" ? "hidden" : ""}><div class="row between"><span class="hint">使用仓库专用密钥</span>${action("generate-ssh-key", icon("plus") + " 生成密钥", "ghost")}</div>${field("SSH 私钥", `<textarea name="git_private_key" class="secret-input" autocomplete="off" spellcheck="false" placeholder="${p.credential_id ? "已配置，留空保留；也可粘贴新私钥替换" : "粘贴无口令的专用私钥，或点击生成密钥"}"></textarea>`)}<div class="ssh-public-key" hidden>${field("SSH 公钥", '<textarea name="git_public_key" class="mono" readonly></textarea>', "把这段公钥添加为仓库的只读 Deploy Key，再测试连接。")}${action("copy-ssh-key", icon("copy") + " 复制公钥", "ghost")}</div>${field("SSH 主机公钥（known_hosts）", '<textarea name="git_known_hosts" class="mono" spellcheck="false" placeholder="git.example.com ssh-ed25519 AAAA…"></textarea>', "由仓库管理员提供并确认，用于验证服务器身份；不会跳过主机校验。")}</div><div class="repository-test-row">${action("test-repository", icon("refresh") + " 测试连接")}<span class="repository-result" role="status" aria-live="polite"></span></div>${field("默认分支", `<input name="branch" value="${escape(p.branch || "main")}" required placeholder="测试成功后可选择分支">`, "测试连接后获取真实分支；空仓库可先填写预期分支。")}</section>`;
}
function projectFields(p = {}) {
  return `${field("项目名称", `<input name="name" value="${escape(p.name)}" placeholder="例如：客户服务中台" required>`)}${field("项目说明", `<textarea name="description" placeholder="项目服务谁，解决什么问题…">${escape(p.description)}</textarea>`)}${repositoryFields(p)}${field("代码阅读方式", `<select name="agent_read_mode"><option value="legacy" ${p.agent_read_mode !== "sandbox" ? "selected" : ""}>原有 Git 查询</option><option value="sandbox" ${p.agent_read_mode === "sandbox" ? "selected" : ""}>隔离源码阅读</option></select>`, "隔离阅读为每次分析提供独立的只读源码，需要管理员先配置运行环境。")}${field("分析代码版本", `<input name="agent_ref" value="${escape(p.agent_ref || "")}" placeholder="留空跟随默认分支，也可填写分支或完整提交 SHA">`, "隔离阅读在每轮开始时固定版本，分析期间不会随仓库同步变化。")}`;
}
function repositoryPayload(form) {
  const data = new FormData(form),
    mode = data.get("auth_mode");
  return {
    repository: data.get("repository").trim(),
    project_id: form.dataset.id || "",
    generated_credential_id:
      mode === "ssh" ? form.dataset.generatedCredential || "" : "",
    repository_auth: {
      mode,
      username: mode === "token" ? data.get("git_username") : "",
      token: mode === "token" ? data.get("git_token") : "",
      private_key: mode === "ssh" ? data.get("git_private_key") : "",
      known_hosts: mode === "ssh" ? data.get("git_known_hosts") : "",
    },
  };
}
async function loadRepositoryAuth() {
  const form = $("#project-form");
  if (!form?.dataset.id || !canManageProject()) return;
  const modeFields = $$(
    "[data-auth-fields] input, [data-auth-fields] textarea, [data-auth-fields] button",
    form,
  );
  modeFields.forEach((el) => (el.disabled = true));
  try {
    const meta = await api(`/projects/${form.dataset.id}/repository-auth`);
    if (!form.isConnected) return;
    form.elements.git_username.value = meta.username || "";
    form.elements.git_known_hosts.value = meta.known_hosts || "";
    if (meta.public_key) showPublicKey(form, meta.public_key);
  } catch (e) {
    toast(e.message);
  } finally {
    modeFields.forEach((el) => (el.disabled = false));
  }
}
function showPublicKey(form, key) {
  $(".ssh-public-key", form).hidden = false;
  form.elements.git_public_key.value = key;
}
async function repositoryAction(a, button) {
  const form = button.closest("form"),
    result = $(".repository-result", form);
  if (a === "copy-ssh-key") {
    await navigator.clipboard.writeText(form.elements.git_public_key.value);
    toast("公钥已复制");
    return;
  }
  if (a === "generate-ssh-key") {
    button.disabled = true;
    try {
      const key = await api("/repository/ssh-key", {});
      if (!form.isConnected) return;
      form.dataset.generatedCredential = key.credential_id;
      form.elements.git_private_key.value = "";
      form.elements.git_private_key.placeholder =
        "已生成并加密保存，不回显私钥";
      showPublicKey(form, key.public_key);
      result.textContent = "公钥已生成，添加到仓库并填写主机公钥后测试连接。";
      result.className = "repository-result";
    } finally {
      button.disabled = false;
    }
    return;
  }
  const payload = repositoryPayload(form),
    signature = JSON.stringify(payload);
  button.disabled = true;
  result.className = "repository-result";
  result.textContent = "正在验证读取权限…";
  try {
    const data = await api("/repository/test", payload);
    if (
      !form.isConnected ||
      signature !== JSON.stringify(repositoryPayload(form))
    )
      return;
    result.textContent = data.message;
    result.classList.add("success");
    if (data.branches.length) {
      const old = form.elements.branch,
        value = data.branches.includes(old.value)
          ? old.value
          : data.default_branch || data.branches[0];
      old.outerHTML = `<select name="branch" id="${old.id}">${data.branches.map((b) => `<option value="${escape(b)}" ${b === value ? "selected" : ""}>${escape(b)}</option>`).join("")}</select>`;
    }
  } catch (e) {
    if (
      form.isConnected &&
      signature === JSON.stringify(repositoryPayload(form))
    ) {
      result.textContent = e.message;
      result.classList.add("error");
    }
  } finally {
    button.disabled = false;
  }
}
const roleNames = { product: "产品", developer: "开发", tester: "测试", admin: "管理员" };
function memberRoles(m) { return m.roles || (m.role ? [m.role] : []); }
function roleChecks(selected = ["developer"], label = "项目角色") { return `<fieldset class="role-checks"><legend class="sr-only">${escape(label)}</legend>${Object.entries(roleNames).map(([role, name]) => `<label><input type="checkbox" name="roles" value="${role}" ${selected.includes(role) ? "checked" : ""}><span>${name}</span></label>`).join("")}</fieldset>`; }
function settingsPage() {
  return `<div class="page">${heading("项目设置", "管理项目资料、仓库和成员角色。")}<div class="settings-grid"><section class="settings-section"><h2>项目资料</h2><p>仓库用于读取和分析，发布由原有开发流程完成。</p><form id="project-form" data-id="${state.project.id}">${projectFields(state.project)}<div class="form-error"></div><button class="primary" type="submit">保存设置</button></form></section><section class="settings-section"><div class="row between"><h2>项目成员</h2>${canManageProject() ? action("invite-member", icon("plus") + " 添加成员", "ghost", `data-project="${escape(state.project.id)}"`) : ""}</div><p>一位成员可以同时担任多个角色。角色决定谁收到哪类待办：评审和验收给产品，认领和上线给开发，测试和 Bug 验证给测试（项目里没有测试时给产品）。各角色的业务协作能力相同，管理员还可以添加、移除成员和分配角色。</p><div id="project-members"><p class="hint">正在读取成员…</p></div><p class="hint">只有被分配到项目的成员可以访问。平台 Agent 由平台管理员统一配置；个人接入请进入「我的 MCP」。</p></section></div></div>`;
}
async function loadProjectMembers(projectID, target = "#project-members") {
  try {
    const root = $(target); if (!root) return;
    const data = await api(`/projects/${encodeURIComponent(projectID)}/members`);
    if (!root.isConnected) return;
    root.innerHTML = data.items.length ? `<div class="member-list">${data.items.map((m) => `<div class="member-row"><div class="grow"><strong>${escape(m.user.name)}</strong><p class="hint">${escape([m.user.login_id, m.department_name].filter(Boolean).join(" · "))}</p></div>${data.can_manage ? `${roleChecks(memberRoles(m), `${m.user.name}的项目角色`)}${action("save-member-role", "保存", "ghost", `data-project="${escape(projectID)}" data-user="${escape(m.user.id)}"`)}${action("remove-member", "移除", "ghost danger", `data-project="${escape(projectID)}" data-user="${escape(m.user.id)}" data-name="${escape(m.user.name)}"`)}` : `<span class="member-badges">${memberRoles(m).map((r) => `<span class="badge">${roleNames[r] || escape(r)}</span>`).join("")}</span>`}</div>`).join("")}</div>` : '<p class="context-note">尚未分配成员。请先添加一位项目管理员。</p>';
  } catch (e) { if ($(target)) $(target).textContent = e.message; }
}
function mcpPage() {
  return `<div class="page">${heading("我的 MCP", "使用自己的账号授权 Codex、Claude Code 等 Agent 客户端。")}<div class="settings-grid"><section class="settings-section"><div class="row between"><h2>个人接入</h2><span id="mcp-status" class="badge">读取中</span></div><p>开通后，在 Agent 客户端添加下方地址，通过浏览器登录并确认授权项目。</p><pre class="code-block">${escape(location.origin + "/mcp")}</pre>${action("copy-mcp-url", icon("copy") + " 复制地址")}${action("toggle-mcp", "开通个人 MCP", "primary", 'id="mcp-toggle" disabled')}<p class="hint">每次授权仅访问所选项目，权限随项目成员关系生效。服务端无需联网读取客户端元数据。</p><details class="mcp-instructions"><summary>接入方式与协作顺序</summary><pre class="code-block">${escape(JSON.stringify({mcpServers:{"ai-pm":{url:location.origin+"/mcp"}}},null,2))}</pre><p>在客户端选择 OAuth 登录。授权时选择项目；另一个项目需要另建连接并授权。</p><p>产品可以在客户端对话中创建需求、上传截图和文档、补充或确认评审、管理当前里程碑、查看项目时间线，无需认领开发任务。修改或推进前需由你明确提出要求，操作会保留本人身份和历史。</p><p class="hint">开发前先用 work_list 查看需求及协同任务。开发协作顺序：task_list → task_get_context → task_claim → task_report_work → task_submit_result。保留 claim_id，提交前读取最新修订。</p></details></section><section class="settings-section"><h2>已授权客户端</h2><p>撤销单个连接，或关闭个人 MCP 以撤销全部连接。</p><div id="mcp-grants"><p class="hint">正在读取授权…</p></div></section></div></div>`;
}
async function loadMCP() {
  try {
    const data = await api("/mcp"); if (!$("#mcp-toggle")) return;
    $("#mcp-status").textContent = data.enabled ? "已开通" : "未开通";
    const button = $("#mcp-toggle");button.textContent = data.enabled ? "关闭个人 MCP" : "开通个人 MCP";button.dataset.enabled = String(data.enabled);button.disabled = false;
    $("#mcp-grants").innerHTML = data.grants.length ? data.grants.map((g) => `<div class="profile-row"><div class="grow"><strong>${escape(state.projects.find((p) => p.id === g.project_id)?.name || "已失去访问权限的项目")}</strong><p class="hint client-id">${escape(g.client_id)}</p><p class="hint">授权于 ${date(g.created_at)}</p></div>${action("revoke-grant", "撤销", "ghost danger", `data-id="${escape(g.id)}"`)}</div>`).join("") : '<p class="context-note">暂无客户端授权。</p>';
  } catch (e) { if ($("#mcp-grants")) $("#mcp-grants").textContent = e.message; }
}
function platformPage() {
  return `<div class="page">${heading("平台设置", "统一配置平台 Agent，管理平台管理员和成员项目授权。")}<div class="settings-grid"><section class="settings-section"><h2>平台 Agent</h2><p>所有项目使用同一平台 Agent 模型配置。</p><form id="platform-form">${field("默认模型", `<select name="profile_id"><option value="">暂不启用 Agent</option>${state.profiles.map((p) => `<option value="${escape(p.id)}" ${state.platform.profile_id === p.id ? "selected" : ""}>${escape(p.name)} · ${escape(p.model)}</option>`).join("")}</select>`)}<div class="form-error"></div><button type="submit" class="primary">保存平台配置</button></form><div class="divider"></div><div class="row between"><h3>模型配置</h3>${action("new-profile", icon("plus") + " 添加", "ghost")}</div>${state.profiles.map((p) => `<div class="profile-row"><span class="dot ${p.has_key ? "live" : ""}"></span><div class="grow">${escape(p.name)}<p class="hint">${escape(p.provider)} / ${escape(p.model)}</p></div>${action("edit-profile", "编辑", "ghost", `data-id="${escape(p.id)}"`)}</div>`).join("") || '<p class="hint">先添加模型配置，再选为默认模型。</p>'}<p class="hint">API Key 仅保存在服务器，不会回显。</p></section><div><section class="settings-section"><div class="row between"><h2>成员项目授权</h2>${state.auth?.directory?.syncable ? action("sync-directory", icon("refresh") + ` 同步 ${escape(state.auth.directory.label)}`, "ghost") : ""}</div><p>选择项目，搜索成员并分配产品、开发、测试或管理员角色，可多选。</p>${state.platformProjects.map((p) => `<div class="profile-row"><div class="grow"><strong>${escape(p.name)}</strong><p class="hint">${escape(p.description)}</p></div>${action("manage-project-members", "管理成员", "", `data-project="${escape(p.id)}"`)}</div>`).join("") || '<p class="hint">还没有项目。</p>'}</section>${accountsSection()}${channelsSection()}<section class="settings-section"><div class="row between"><h2>平台管理员</h2>${action("add-platform-admin", icon("plus") + " 添加", "ghost")}</div><p>平台管理员可进入此页面、配置 Agent 并管理所有项目成员。</p>${state.admins.map((u) => `<div class="profile-row"><div class="grow">${escape(u.name)}<p class="hint">${escape(u.login_id)}</p></div>${u.id !== "local-owner" ? action("remove-platform-admin", "移除", "ghost danger", `data-user="${escape(u.id)}" data-name="${escape(u.name)}"`) : ""}</div>`).join("")}</section></div></div></div>`;
}
function channelsSection() {
  if (!state.channels?.length) return "";
  return `<section class="settings-section"><h2>消息推送</h2><p>把待办推送给对应的产品、开发和测试。通过、确认这类操作可以在卡片里直接完成，需要填写内容时会打开待办页面，电脑和手机都能用。成员在「我的待办」里绑定自己的账号。</p>${state.channels.map((c) => `<div class="profile-row"><span class="dot ${c.running ? "live" : ""}"></span><div class="grow">${escape(c.label)}<p class="hint">${c.running ? "已启用" : c.error ? "未能启用：" + escape(c.error) : "未启用"}</p></div>${action("configure-channel", "配置", "ghost", `data-name="${escape(c.name)}"`)}</div>`).join("")}</section>`;
}
function channelForm(name) {
  const c = state.channels.find((v) => v.name === name);
  if (!c) return;
  const inputs = c.fields.map((f) => field(f.required ? f.label : `${f.label}（可选）`, f.secret ? `<input type="password" name="v_${escape(f.key)}" autocomplete="new-password" placeholder="${c.secrets[f.key] ? "已配置，留空保留" : escape(f.placeholder || "")}">` : `<input name="v_${escape(f.key)}" value="${escape(c.values[f.key] || "")}" placeholder="${escape(f.placeholder || "")}" autocomplete="off" spellcheck="false">`, f.hint || "")).join("");
  const setup = c.setup?.length ? `<div class="channel-setup"><h3>在${escape(c.label)}管理后台填写</h3>${c.setup.map((s) => `<div class="channel-setup-row"><span>${escape(s.label)}</span><code>${escape(s.value)}</code></div>`).join("")}<p class="hint">修改公网地址并保存后，这里的地址会随之更新。</p></div>` : "";
  modal(`配置${escape(c.label)}`, `<form id="channel-form" data-name="${escape(name)}"><label class="check-row"><input type="checkbox" name="enabled" ${c.enabled ? "checked" : ""}> 启用${escape(c.label)}推送</label>${field("公网地址", `<input name="public_url" value="${escape(c.public_url)}" placeholder="${escape(c.default_url || "https://pm.example.com")}" autocomplete="off" spellcheck="false">`, `${c.label}的服务器回调和成员打开待办都使用这个地址，需要能从外网访问，且是${c.label}后台登记的可信域名。留空时使用平台访问地址${c.default_url ? " " + c.default_url : ""}。`)}${inputs}<label class="check-row"><input type="checkbox" name="sign_in" ${c.sign_in ? "checked" : ""}> 已绑定的成员从${escape(c.label)}打开时免登录</label>${setup}<div class="form-error" role="alert"></div><div class="modal-actions">${action("test-channel", "发送测试消息", "", `data-name="${escape(name)}"`)}<button type="submit" class="primary">保存</button></div></form>`);
}
function accountsSection() {
  if (state.auth?.login !== "password") return "";
  const me = state.auth.user?.id;
  return `<section class="settings-section"><div class="row between"><h2>平台账号</h2>${action("new-account", icon("plus") + " 新建", "ghost")}</div><p>创建账号并把用户名和初始密码告知成员，成员首次登录时改为自己的密码。停用后立即退出登录，并撤销其 MCP 授权。</p>${state.accounts.map((a) => `<div class="profile-row"><div class="grow">${escape(a.name)}${a.disabled ? ' <span class="badge">已停用</span>' : a.must_change_password ? ' <span class="badge">待改密</span>' : ""}<p class="hint">${escape(a.username)}${a.user_id === me ? " · 当前账号" : ""}</p></div>${a.user_id === me ? "" : action("reset-account-password", "重置密码", "ghost", `data-user="${escape(a.user_id)}" data-name="${escape(a.name)}"`) + action("toggle-account", a.disabled ? "启用" : "停用", a.disabled ? "ghost" : "ghost danger", `data-user="${escape(a.user_id)}" data-name="${escape(a.name)}" data-disabled="${a.disabled}"`)}</div>`).join("")}</section>`;
}
let directoryRevision = 0;
function departmentOptions(roots, prefix = "") {
  return roots.flatMap((u) => [(u.type === "department" ? `<option value="${u.id}">${escape(prefix + u.name)}</option>` : ""), departmentOptions(u.children || [], prefix + u.name + " / ")]).join("");
}
async function employeeDialog(projectID, platform = false) {
  const project = state.projects.find((p) => p.id === projectID) || state.platformProjects.find((p) => p.id === projectID);
  modal(platform ? "添加平台管理员" : `添加项目成员 · ${escape(project?.name || "项目")}`, `<form id="employee-search" data-project="${escape(projectID || "")}" data-platform="${platform}"><div class="row employee-search-row"><input name="q" placeholder="搜索姓名、账号或部门" aria-label="搜索成员"><button type="submit">搜索</button></div><select id="department-filter" name="department_id" aria-label="按部门筛选"><option value="">全部部门</option></select></form><p class="hint" id="directory-status" role="status">正在读取成员目录…</p><div id="employee-results"></div><form id="member-form" data-project="${escape(projectID || "")}" data-platform="${platform}" hidden><div class="selected-employee" id="selected-employee"></div>${platform ? '<p class="notice">该成员将可以配置平台 Agent，并管理所有项目的成员授权。</p>' : field("项目角色", roleChecks(), "可多选。")}<div class="form-error"></div><div class="modal-actions"><button type="submit" class="primary">确认${platform ? "授予平台管理权限" : "加入项目"}</button></div></form>`);
  await searchEmployees(1);
}
async function searchEmployees(page = 1) {
  const form = $("#employee-search");if (!form) return;
  const revision = ++directoryRevision;
  const data = new FormData(form);const base = form.dataset.platform === "true" ? "/platform" : `/projects/${encodeURIComponent(form.dataset.project)}`;
  const params = new URLSearchParams({q: data.get("q"),department_id:data.get("department_id"),page:String(page)});
  try {
    const result = await api(`${base}/directory?${params}`);
    if (revision !== directoryRevision || $("#employee-search") !== form) return;
    const selected = data.get("department_id");$("#department-filter").innerHTML = '<option value="">全部部门</option>' + departmentOptions(result.roots);$("#department-filter").value = selected;$("#department-filter").hidden = !result.roots?.length;
    $("#directory-status").textContent = `${result.label || "成员目录"} · ${result.total} 人${result.synced_at ? " · 同步于 " + date(result.synced_at) : ""}`;
    $("#employee-results").innerHTML = result.items.map((u) => `<button type="button" class="employee-result" data-action="choose-employee" data-user="${escape(u.id)}" data-name="${escape(u.name)}" data-detail="${escape([u.login_id,...(u.org_path?.length ? u.org_path : [u.department_name])].filter(Boolean).join(" · "))}"><strong>${escape(u.name)}</strong><span>${escape([u.login_id,u.department_name,u.job_title].filter(Boolean).join(" · "))}</span>${icon("plus")}</button>`).join("") || '<p class="context-note">未找到匹配成员。</p>';
    if (result.total > result.page_size) $("#employee-results").innerHTML += `<div class="row between directory-pagination">${page > 1 ? action("directory-page", "上一页", "ghost", `data-page="${page - 1}"`) : "<span></span>"}<span class="hint">第 ${page} / ${Math.ceil(result.total/result.page_size)} 页</span>${page * result.page_size < result.total ? action("directory-page", "下一页", "ghost", `data-page="${page + 1}"`) : "<span></span>"}</div>`;
  } catch (e) { if (revision === directoryRevision && $("#employee-search") === form) {$("#directory-status").textContent = e.message;$("#employee-results").innerHTML = action("retry-directory", "重新读取", "ghost");} }
}
function codePage() {
  return `<div class="page">${heading("代码与变更", `默认分支 ${escape(state.project.branch)} · ${date(state.project.synced_at)}`, action("sync", icon("refresh") + " 同步仓库", "primary"))}${state.project.sync_error ? `<div class="notice">${escape(state.project.sync_error)}</div>` : ""}${!state.project.sha ? empty("尚未读取代码", "在项目设置中填写仓库地址，然后同步。", action("navigate", "项目设置", "", 'data-page="settings"')) : `<div class="toolbar"><input id="code-search" placeholder="搜索代码内容…" aria-label="代码搜索">${action("search-code", "搜索")}${action("git-log", "最近提交", "ghost")}<span class="mono muted">${state.project.sha.slice(0, 12)}</span></div><div class="file-browser"><div class="file-list" id="file-list"><p class="hint">正在读取目录…</p></div><div class="file-content" id="file-content"><p class="context-note">选择文件查看当前版本内容。</p></div></div>`}</div>`;
}
async function loadFiles() {
  try {
    const data = await api(`/projects/${state.project.id}/code?action=tree`);
    if (!$("#file-list")) return;
    state.files = data.text.trim().split("\n").filter(Boolean);
    $("#file-list").innerHTML =
      state.files
        .slice(0, 600)
        .map((path) =>
          action(
            "open-file",
            escape(path),
            "",
            `data-path="${escape(path)}" title="${escape(path)}"`,
          ),
        )
        .join("") +
      (data.truncated
        ? '<p class="hint">目录已截断，可用搜索定位文件。</p>'
        : "");
  } catch (e) {
    if ($("#file-list")) $("#file-list").textContent = e.message;
  }
}

async function openTask(taskID) {
  const ctx = await api(`/tasks/${taskID}`);
  const t = ctx.task;
  if (t.project_id !== state.project.id) return;
  const artifacts = ctx.artifacts.filter((a) => a.task_id === t.id);
  const runs = state.runs.filter((r) => r.task_id === t.id);
  let buttons = "";
  if (t.status === "done") buttons += action("iterate-task", "追加迭代", "primary", `data-id="${t.id}"`);
  if (t.status === "clarify")
    buttons += action("requirement-review", "继续需求评审", "primary", `data-id="${t.id}"`);
  if (t.status === "ready" && t.alignment?.status !== "replied")
    buttons += action("claim", "开始处理", "primary", `data-id="${t.id}"`);
  if (t.status === "aligning") buttons += action("requirement-review", "回复开发对齐", "primary", `data-id="${t.id}"`);
  if (t.status === "ready" && t.alignment?.status === "replied") buttons += action("requirement-review", "查看对齐回复", "primary", `data-id="${t.id}"`) + action("reopen-claim", "重新开放领取", "", `data-id="${t.id}" data-version="${t.version}" data-revision="${t.revision}"`);
  if (t.status === "doing")
    buttons +=
      action("submit-result", "提交开发结果", "primary", `data-id="${t.id}"`) +
      action("release", "交接任务", "", `data-id="${t.id}"`);
  if (t.kind === "requirement" && ["testing","fixing","retest"].includes(t.status)) buttons += action("file-testing-bug","提起 Bug","",`data-id="${t.id}"`);
  if (t.status === "testing") buttons += action("test-pass","确认测试通过","primary",`data-id="${t.id}"`);
  if (t.status === "retest") buttons += action("start-retest","开始复测","primary",`data-id="${t.id}"`);
  if (t.status === "verifying") buttons += action("verify-bug","确认修复有效","primary",`data-id="${t.id}"`);
  if (t.kind === "bug" && (["verifying","resolved"].includes(t.status) || (!t.parent_requirement_id && t.status === "testing"))) buttons += action("return-bug","修复无效，退回 Bug","",`data-id="${t.id}"`);
  if (t.status === "pending_release")
    buttons += action("mark-deployed", "确认已上线", "primary", `data-id="${t.id}"`) + action("rework", "退回开发", "", `data-id="${t.id}"`);
  if (t.status === "review")
    buttons += action("review", "开始验收", "primary", `data-id="${t.id}"`);
  if(taskActive(t)&&t.status!=="review")buttons+=action("pv-assign","调整目标版本","",`data-task="${t.id}"`);
  if(t.project_version_id)buttons+=action("pv-open",escape(versionName(state.context.project_versions||[],t.project_version_id)),"",`data-id="${t.project_version_id}"`);
  if (taskActive(t)) buttons += action("task-decline","拒绝需求","danger-outline",`data-id="${t.id}"`) + action("task-close","关闭需求","ghost",`data-id="${t.id}"`);
  if (taskTerminated(t)) buttons += action("task-reopen","重新打开需求","",`data-id="${t.id}"`);
  buttons +=
    (t.status !== "clarify" ? action("requirement-review", "评审记录", "", `data-id="${t.id}"`) : "") +
    (!taskTerminated(t) ? action("edit-task", "编辑", "", `data-id="${t.id}"`) : "") +
    action(
      "task-analyze",
      icon("spark") + " 分析",
      "",
      `data-id="${t.id}" data-kind="${t.kind === "bug" ? "triage" : "requirements"}"`,
    );
  modal(
    `<span class="mono muted">${short(t.id)} · ${t.kind === "bug" ? (t.parent_requirement_id ? "需求测试 Bug" : "平台 Bug") : "迭代需求"}</span>`,
    `<h1 class="detail-title">${escape(t.title)}</h1><div class="detail-meta">${status(t)}<span class="badge">${priorities[t.priority]}</span><span class="badge">修订 R${t.revision}</span><span class="badge">${escape(t.owner || "未认领")}</span>${t.reviewer ? `<span class="badge">验收：${escape(t.reviewer)}</span>` : ""}</div>${t.blocker ? `<div class="notice">阻塞：${escape(t.blocker)}</div>` : ""}${t.status === "ready" && t.alignment?.status === "replied" ? `<div class="notice">产品已回复，等待原开发客户端确认。原客户端无法继续时，可重新开放领取；已有对齐记录会追加进需求正文。</div>` : ""}<div class="detail-actions">${buttons}</div>${t.resolution?`<section class="task-resolution notice"><strong>${states[t.status]} · ${states[t.resolution.from_status]}阶段结束</strong><p class="prewrap">${escape(t.resolution.reason)}</p><span class="small muted">${escape(t.resolution.actor)} · ${date(t.resolution.at)}</span></section>`:""}${testingDetails(t,ctx.tasks)}${iterationContextHTML(t.iteration)}<section class="detail-section"><h3>需求与背景</h3><div class="requirement-description markdown-content">${renderMarkdown(t.description || "尚未补充描述")}</div></section>${t.attachments?.length ? `<section class="detail-section"><h3>附件 · ${t.attachments.length}</h3>${sentAttachments(t)}</section>` : ""}<section class="detail-section"><h3>验收条件</h3>${t.acceptance.length ? `<ul class="acceptance-list">${t.acceptance.map((v) => `<li><i class="square ${t.status === "done" ? "checked" : ""}" aria-hidden="true">${t.status === "done" ? "✓" : ""}</i>${escape(v)}</li>`).join("")}</ul>` : '<p class="context-note">补充预期行为后，再进入待认领队列。</p>'}</section>${t.submission ? `<section class="detail-section"><h3>最近一次提交 · R${t.submission.revision}</h3><div class="prewrap">${escape(t.submission.summary)}</div><p class="mono muted">${escape(t.submission.sha)} · ${escape(t.submission.environment || "环境未说明")}</p><p class="prewrap">${escape(t.submission.tests)}</p>${t.submission.limitations ? `<div class="notice info">未完成或未验证：${escape(t.submission.limitations)}</div>` : ""}<p class="hint">本次提交版本的验证记录；后续测试、实际上线与业务验收分别记录。</p></section>` : ""}${t.deployment ? `<section class="detail-section"><h3>最近一次上线 · R${t.deployment.revision}</h3><p class="mono muted">${escape(t.deployment.sha)} · ${escape(t.deployment.environment)}</p><p class="prewrap">${escape(t.deployment.body)}</p><p class="hint">${escape(t.deployment.actor)} · ${t.deployment.source === "developer" ? "开发汇报" : "人工确认"} · ${date(t.deployment.at)}${t.deployment.revision !== t.revision ? " · 历史版本，当前需求需重新开发和上线" : ""}</p></section>` : ""}${t.review ? `<section class="detail-section"><h3>验收记录</h3><div class="prewrap">${t.review.accepted ? "通过" : "退回"} · ${escape(t.review.environment)} · ${date(t.review.at)}\n${escape(t.review.note)}</div></section>` : ""}${iterationHistoryHTML(ctx.iteration_history,t,state.context.project_versions)}<section class="detail-section"><h3>分析与证据</h3>${artifacts.map((a) => `<div class="record"><div class="record-header"><span>${escape(a.title)} · ${escape(a.source)}</span><span>${date(a.at)}</span></div><div class="prewrap">${escape(a.body)}</div></div>`).join("") || '<p class="context-note">平台分析和相关证据会保留在这里。</p>'}${runs
      .slice(0, 4)
      .map(
        (r) =>
          `<div class="record row between"><span class="small muted">${kindNames[r.kind]} · ${runStates[r.status]}</span>${action("open-run", "查看运行", "ghost", `data-id="${r.id}"`)}</div>`,
      )
      .join(
        "",
      )}</section><section class="detail-section"><h3>工作记录与决定</h3><form id="report-form" data-id="${t.id}"><textarea name="body" aria-label="新增工作记录" placeholder="记录进展、补充决定或说明阻塞…" required></textarea><div class="row between"><select name="report_kind" aria-label="记录类型"><option value="progress">工作进展</option><option value="decision">确认的决定</option><option value="blocked">遇到阻塞</option><option value="unblocked">解除阻塞</option></select><button type="submit">添加记录</button></div><div class="form-error"></div></form>${[
      ...ctx.events,
    ]
      .reverse()
      .map(
        (e) =>
          `<div class="record"><div class="record-header"><span>${escape(e.actor)} · ${escape(e.kind)}</span><span>${date(e.at)}</span></div><div class="prewrap">${escape(e.body)}</div></div>`,
      )
      .join("")}</section>`,
    { drawer: true },
  );
}
// Links such as /?task=<id> from the to-do page open that task in its project.
async function openLinkedTask(taskID) {
  history.replaceState(null, "", "/");
  const { task } = await api(`/tasks/${encodeURIComponent(taskID)}`);
  if (state.project?.id !== task.project_id) await switchProject(task.project_id);
  if (state.project?.id === task.project_id) await openTask(task.id);
}
async function openRequirementReview(taskID, savedTask, stillCurrent = () => true) {
 const task = savedTask || (await api(`/tasks/${taskID}`)).task;
 if (!stillCurrent()) return;
 modal(task.parent_requirement_id ? "测试 Bug 评审" : task.alignment ? "开发对齐" : "需求评审",requirementReviewHTML(task),{review:true});
 requirementReview = mountRequirementReview($(".requirement-review"),{task,api,userID:state.auth?.user?.id || "local-owner",onDetails:id=>openTask(id || taskID).catch(e=>toast(e.message)),onResolve:action=>taskResolutionForm(taskID,action).catch(e=>toast(e.message)),onConfirmed:()=>{toast("评审状态已更新");void refresh().then(render).catch(e=>toast(e.message));}});
}
async function taskResolutionForm(taskID, operation) {
  const task=(await api(`/tasks/${taskID}`)).task;
  const title=({close:"关闭需求",decline:"拒绝需求",reopen:"重新打开需求"})[operation];
  if (!title) return;
  if (operation==='reopen' ? !taskTerminated(task) : !taskActive(task)) {toast("需求状态已更新，请查看最新状态");return;}
  const draftKey=`ai-pm:resolution-draft:${state.auth?.user?.id || "local-owner"}:${task.project_id}:${task.id}:${operation}`;
  let draft;try {draft=JSON.parse(localStorage.getItem(draftKey) || 'null');}catch{}
  modal(title,`<form id="task-resolution-form" data-id="${escape(task.id)}" data-operation="${operation}" data-version="${task.version}"><p class="context-note"><strong>${escape(task.title)}</strong><br>当前阶段：${states[task.status]}</p><p class="hint">${operation==='reopen'?'重新进入需求评审，历史记录保留；原开发认领凭据不会恢复。':'操作后结束推进、撤销开发认领，原始需求、评审和交付记录均保留。'}</p>${field(operation==='reopen'?'重新打开的原因':operation==='decline'?'拒绝原因':'关闭原因',`<textarea name="body" required rows="4" placeholder="说明作出这个决定的原因，方便产品与开发回看"></textarea>`)}<div class="form-error" role="alert"></div><div class="modal-actions">${action("close","返回") }<button type="submit" class="${operation==='reopen'?'primary':'danger'}">确认${title}</button></div></form>`);
  const form=$("#task-resolution-form"),reason=$("textarea",form);form.dataset.draftKey=draftKey;
  reason.value=draft?.body || '';
  if (draft?.payload?.version===task.version) {form.dataset.payload=JSON.stringify(draft.payload);reason.readOnly=true;}
  reason.addEventListener('input',()=>{try {localStorage.setItem(draftKey,JSON.stringify({body:reason.value}));}catch{}});

}
async function taskForm(taskID, iterationSourceID, bugParentID) {
  const projectID = state.project.id;
  const source = iterationSourceID ? (await api(`/tasks/${iterationSourceID}`)).task : null;
  if (state.project.id !== projectID) return;
  if (source && source.status !== 'done') throw Error('只有已验收的需求才能追加迭代');
  const parent = bugParentID ? (await api(`/tasks/${bugParentID}`)).task : null;
  const t = taskID ? (await api(`/tasks/${taskID}`)).task : parent ? {kind:"bug"} : {};
  if (state.project.id !== projectID) return;
  modal(parent ? "提起测试 Bug" : source ? "追加迭代" : taskID ? "编辑需求" : "创建需求", requirementFormHTML(t), {document: true});
  requirementEditor = mountRequirementEditor($("#task-form"), {
    task: t, projectID, userID: state.auth?.user?.id || "local-owner",
    draftScope: parent ? 'testing-bug:' + parent.id : source ? 'iteration:' + source.id : undefined,
    sourceVersion: source?.version, parentRequirement:parent,
    onDiscard: () => { closeModal(); taskForm(taskID,iterationSourceID,bugParentID); },
    onUnauthorized: () => { toast("登录已过期，请保存草稿后重新登录"); },
  });
  const form=$('#task-form'), current=state.context.current_milestone;
  if (parent) {
    form.dataset.bugParent = parent.id; form.dataset.parentVersion = parent.version;
    form.elements.kind.value = 'bug';
    $('.requirement-kind',form).hidden = true;
    $('.requirement-state-note',form).textContent = '先保存报告和图片，Agent 将按此需求的提测代码评审。问题成立后进入待认领，驳回须由你确认。';
    $('button[type=submit]',form).textContent = '保存 Bug 并评审';
    $('.requirement-document',form).insertAdjacentHTML('afterbegin',`<div class="notice info">所属需求：${escape(parent.title)} · R${parent.revision}<br>开发分支：${escape(parent.branch || '未登记')}<br>提测版本：${escape(parent.submission?.sha || '未登记')}</div>`);
  }
  if (t.parent_requirement_id) $('.requirement-kind',form).hidden = true;
  if (source) {
    form.dataset.iterationSource = source.id;
    $('button[type=submit]',form).textContent = '保存迭代需求';
    $('[name=title]',form).placeholder = '这次迭代要改进什么？';
    $('.requirement-state-note',form).textContent = '本轮独立评审、开发与验收，上一轮需求和交付不变。';
    const lineage = {parent_task_id:source.id,root_task_id:source.iteration?.root_task_id||source.id,source:{...source,iteration:undefined},baseline:source.iteration ? source.iteration.baseline||source.iteration.source : null};
    $('.requirement-document',form).insertAdjacentHTML('afterbegin',iterationContextHTML(lineage));
  }
  if (!source && t.iteration) $('.requirement-document',form).insertAdjacentHTML('afterbegin',iterationContextHTML(t.iteration));
  const associated=taskID ? (state.context.project_versions||[]).find(v=>v.id===t.project_version_id) : current;
  const holder=document.createElement('div');holder.className='field milestone-association';
  holder.innerHTML=`<span>所属里程碑</span><strong>${escape(associated?.name||'保存时自动创建里程碑')}</strong><small class="hint">${source?'新迭代自动归入当前里程碑。上一轮所属版本和交付记录保留。':taskID?'已结束里程碑产生的后续工作会自动归入当前里程碑。':'新需求自动归入当前里程碑，没有时随需求一起保存并创建。'}</small>`;
  $('.requirement-properties',form).prepend(holder);
}
async function submitForm(taskID) {
  const t = (await api(`/tasks/${taskID}`)).task;
  modal(
    "提交开发结果",
    `<form id="submit-form" data-id="${t.id}" data-revision="${t.revision}" data-version="${t.version}"><p class="context-note">${escape(t.title)} · 当前需求 R${t.revision}</p>${field("开发分支", `<input name="branch" value="${escape(t.branch || '')}" required placeholder="本需求实际开发分支">`)}${field("实现说明", `<textarea name="summary" required placeholder="本次实现了哪些行为…"></textarea>`)}<div class="field-row">${field("代码版本", `<input name="sha" required placeholder="提交 SHA 或确定版本">`)}${field("验证环境", `<input name="environment" placeholder="例如：本地、测试环境">`)}</div>${field("验证情况", `<textarea name="tests" required placeholder="运行了什么检查，结果如何；未测试请明确说明"></textarea>`)}${field("尚未完成或未验证", `<textarea name="limitations" placeholder="可选"></textarea>`)}${field("证据链接", `<textarea name="references" placeholder="每行一个提交、PR 或报告链接"></textarea>`)}<div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button class="primary" type="submit">${t.parent_requirement_id ? "提交修复，交给产品验证" : "提交至测试中"}</button></div></form>`,
  );
}
async function deploymentForm(taskID) {
  const t = (await api(`/tasks/${taskID}`)).task;
  if (t.status !== "pending_release" || !t.submission) throw new Error("需求尚未进入待上线");
  modal("确认已上线", `<form id="deployment-form" data-id="${t.id}" data-version="${t.version}" data-revision="${t.revision}" data-sha="${escape(t.submission.sha)}"><p class="context-note">${escape(t.title)} · R${t.revision}<br>请在实际发布成功后登记，保存后进入待验收。</p>${field("本次上线版本", `<input value="${escape(t.submission.sha)}" readonly>`)}${field("实际上线环境", `<input name="environment" required placeholder="例如：国内生产">`)}${field("上线说明与证据", `<textarea name="body" required placeholder="说明发布时间、结果，可附发布记录或日志链接。"></textarea>`)}<div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button type="submit" class="primary">确认已上线并进入待验收</button></div></form>`);
}
async function reviewForm(taskID) {
  const t = (await api(`/tasks/${taskID}`)).task;
  if (t.status !== "review") throw new Error("需求尚未进入待验收");
  modal(
    "业务验收",
    `<form id="review-form" data-id="${t.id}" data-revision="${t.revision}" data-sha="${escape(t.submission.sha)}" data-count="${t.acceptance.length}"><p class="context-note">${escape(t.title)} · R${t.revision}<br><span class="mono">${escape(t.submission.sha)}</span></p>${field("验收环境", `<input name="environment" value="${escape(t.deployment?.environment || "")}" readonly required>`)}<label>逐项确认</label>${t.acceptance.map((v, i) => `<label class="check-row"><input type="checkbox" name="check_${i}"><span>${escape(v)}</span></label>`).join("")}${field("验收说明", `<textarea name="note" placeholder="通过说明，或退回的具体原因"></textarea>`)}<div class="form-error"></div><div class="modal-actions"><button name="verdict" value="reject" type="submit">退回修改</button><button name="verdict" value="accept" type="submit" class="primary">确认验收通过</button></div></form>`,
  );
}
function profileForm(profileID) {
  const p = state.profiles.find((p) => p.id === profileID) || {};
  modal(
    profileID ? "编辑模型配置" : "添加模型配置",
    `<form id="profile-form" data-id="${p.id || ""}">${field("配置名称", `<input name="name" value="${escape(p.name)}" required placeholder="例如：项目分析模型">`)}<div class="field-row">${field("提供方", `<input name="provider" value="${escape(p.provider || "openai")}" required placeholder="openai / anthropic / 自定义名称">`)}${field("模型 ID", `<input name="model" value="${escape(p.model)}" required placeholder="服务商提供的模型 ID">`)}</div>${field("API 类型", `<select name="api">${["openai-completions", "openai-responses", "anthropic-messages"].map((v) => `<option value="${v}" ${(p.api || "openai-completions") === v ? "selected" : ""}>${v}</option>`).join("")}</select>`)}${field("自定义 API 地址", `<input name="base_url" value="${escape(p.base_url)}" placeholder="例如：https://你的服务/v1">`, "使用提供方默认地址时可留空。自定义服务请填写完整 API 基址。")}<label class="check-row"><input type="checkbox" name="supports_images" ${p.supports_images ? "checked" : ""}><span>自定义模型支持图片输入</span></label><p class="hint">使用自定义 API 时，选择视觉模型后勾选；内置提供方按模型能力识别。</p><div class="field-row">${field("上下文窗口（tokens）", `<input type="number" name="context_window" min="4096" value="${p.context_window || ''}" placeholder="默认 128000">`, "自定义模型请按实际服务支持的窗口填写，用于自动压缩判断。")}${field("最大输出（tokens）", `<input type="number" name="max_tokens" min="1" value="${p.max_tokens || ''}" placeholder="默认最多 8192">`)}</div>${field("API Key", `<input name="key" type="password" autocomplete="new-password" placeholder="${p.has_key ? "已保存，留空保留原值" : "API Key；本地免鉴权服务可留空"}">`)}<div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button class="primary" type="submit">保存模型配置</button></div></form>`,
  );
}
function evidenceForm() {
  modal(
    "添加交付证据",
    `<form id="evidence-form">${field("类型", `<select name="kind"><option value="deployment">部署记录</option><option value="test">测试结果</option></select>`)}${field("关联需求", `<select name="task_id"><option value="">整个项目</option>${state.context.tasks.map((t) => `<option value="${t.id}">${escape(t.title)}</option>`).join("")}</select>`)}<div class="field-row">${field("代码版本", `<input name="sha" required>`)}${field("环境", `<input name="environment" required placeholder="例如：国内生产">`)}</div>${field("证据说明", `<textarea name="body" required placeholder="执行结果、时间，以及日志或报告链接"></textarea>`)}<div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button type="submit" class="primary">保存证据</button></div></form>`,
  );
}
async function startRun(
  kind,
  taskID = "",
  prompt = "",
  parent = "",
  attachmentIDs = [],
  projectID = state.project.id,
) {
  const r = await api(`/projects/${projectID}/runs`, {
    kind,
    task_id: taskID,
    prompt,
    parent_id: parent,
    attachment_ids: attachmentIDs,
  });
  if (state.project?.id !== projectID) return r;
  if (kind === "question" && deletedConversations.has(conversationKey(projectID, conversationID(r)))) return r;
  state.runs.unshift(r);
  if (kind === "question") {
    if (state.page === "chat") updateChat();
  } else {
    modal(kindNames[kind], `<div id="run-${r.id}">${runHTML(r)}</div>`);
  }
  pollRun(r.id);
  return r;
}
const pollingRuns = new Map();
const streamingRuns = new Set();
function stopRunUpdates(matches = () => true) {
  for (const [id, controller] of pollingRuns) {
    if (matches(state.runs.find((r) => r.id === id))) {
      controller.abort();
      pollingRuns.delete(id);
    }
  }
}
let runRenderFrame;
const pendingRunRenders = new Set();
function scheduleRunRender(runID) {
  pendingRunRenders.add(runID);
  if (runRenderFrame) return;
  runRenderFrame = requestAnimationFrame(() => {
    runRenderFrame = 0;
    if (state.page === "chat") updateChat();
    for (const id of pendingRunRenders) {
      const run = state.runs.find((r) => r.id === id);
      const element = document.getElementById(`run-${id}`);
      if (run && element && !element.closest(".chat-feed")) updateRun(element, run);
    }
    pendingRunRenders.clear();
  });
}
function pollActiveRuns() {
  for (const run of state.runs) {
    if (["running", "queued"].includes(run.status)) pollRun(run.id);
  }
}
async function pollRun(runID) {
  if (pollingRuns.has(runID)) return;
  const controller = new AbortController();
  pollingRuns.set(runID, controller);
  const projectID = state.project?.id;
  let retryStreamAt = 0;
  const receive = (r, mutation) => {
    if (controller.signal.aborted || r.project_id !== state.project?.id) return false;
    if (deletedConversations.has(conversationKey(r.project_id, conversationID(r)))) return false;
    if (mutation !== conversationMutationRevision) return false;
    const old = state.runs.findIndex((v) => v.id === r.id);
    if (old >= 0) state.runs[old] = r;
    else state.runs.unshift(r);
    scheduleRunRender(r.id);
    return true;
  };
  try {
    while (!controller.signal.aborted && projectID === state.project?.id) {
      const mutationAtStart = conversationMutationRevision;
      let r;
      if (Date.now() >= retryStreamAt && streamingRuns.size < 4) {
        streamingRuns.add(controller);
        try {
          await streamRun(runID, (run) => { r = run; return receive(run, mutationAtStart); }, controller.signal);
        } catch {
          // Proxy/network failures keep the existing polling path usable. A
          // fresh stream later resumes with a complete snapshot, without gaps.
          retryStreamAt = Date.now() + 10000;
          r = undefined;
        } finally { streamingRuns.delete(controller); }
      }
      if (controller.signal.aborted || projectID !== state.project?.id) return;
      if (mutationAtStart !== conversationMutationRevision) continue;
      if (!r) {
        r = await api(`/runs/${runID}`);
        const local = state.runs.find((run) => run.id === runID);
        r = mergeRunRefresh([r], local ? [local] : [], new Map(local ? [[runID, local]] : []))[0];
        if (!receive(r, mutationAtStart)) continue;
      }
      if (!["running", "queued"].includes(r.status)) {
        const context = await api(`/projects/${r.project_id}/context`);
        if (r.project_id === state.project?.id) state.context = context;
        return;
      }
      await waitForPoll(controller.signal);
    }
  } catch (e) {
    const run = state.runs.find((r) => r.id === runID);
    if (e.status === 404 && run?.kind === "question") forgetConversation(run.project_id, conversationID(run));
    else if (e.status !== 404) toast(e.message);
  } finally {
    if (pollingRuns.get(runID) === controller) pollingRuns.delete(runID);
  }
}

// Refresh external MCP activity without replacing a form the user is editing.
let refreshing = false;
setInterval(async () => {
  if (state.page === "chat") { await refreshChatActivity(); return; }
  if (
    refreshing ||
    state.switchingProject ||
    projectMenuOpen() ||
    document.hidden ||
    !state.project ||
    $("dialog") ||
    ["settings", "code", "mcp", "platform"].includes(state.page) ||
    document.activeElement?.matches("input, textarea, select") ||
    $("textarea[name=question]")?.value.trim() ||
    (state.page === "chat" &&
      (chatDraft().files.length || chatDraft().sending)) ||
    ($$(".package-select:checked").length||$$("[data-plan-task]:checked").length)
  )
    return;
  refreshing = true;
  const projectID = state.project.id;
  const mutationAtStart = conversationMutationRevision;
  const runsAtStart = new Map(state.runs.map((run) => [run.id, run]));
  try {
    const [context, runs] = await Promise.all([
      api(`/projects/${projectID}/context`),
      api(`/projects/${projectID}/runs`),
    ]);
    if (
      projectID !== state.project?.id ||
      mutationAtStart !== conversationMutationRevision ||
      $("dialog") ||
      state.switchingProject ||
      projectMenuOpen() ||
      ["settings", "code", "mcp", "platform"].includes(state.page) ||
      document.activeElement?.matches("input, textarea, select") ||
      $("textarea[name=question]")?.value.trim() ||
      (state.page === "chat" &&
        (chatDraft().files.length || chatDraft().sending)) ||
      ($$(".package-select:checked").length||$$("[data-plan-task]:checked").length)
    )
      return;
    const signature = (ctx, rs) =>
      JSON.stringify([
        ctx?.project,
        ctx?.tasks,
        ctx?.requirement_reviews,
        ctx?.project_versions,
        ctx?.artifacts,
        ctx?.events,
        rs,
      ]);
    const mergedRuns = mergeRunRefresh(runs, state.runs, runsAtStart).filter((r) => !deletedConversations.has(conversationKey(projectID, conversationID(r))));
    if (signature(context, mergedRuns) !== signature(state.context, state.runs)) {
      state.context = context;
      state.project = context.project;
      state.runs = mergedRuns;
      if (state.page === "chat") updateChat();
      else if(state.page==='timeline')timelineUI.refreshIfNeeded();
      else render();
    }
    pollActiveRuns();
  } catch {
    /* The next successful refresh will reconcile transient failures. */
  } finally {
    refreshing = false;
  }
}, 6000);
async function navigate(page) {
  if (page === "platform" && !isPlatformAdmin()) { toast("需要平台管理员权限"); return; }
  state.page = page;
  state.search = "";
  state.filter = "";
  state.versionFilter = "";
  closeModal();
  await refresh();
  render();
  pollActiveRuns();
}
document.addEventListener("click", async (event) => {
  if(event.target.matches('input[type=checkbox][data-plan-task]'))return;
  if (!event.target.closest(".project-picker")) closeProjectMenu();
  const button = event.target.closest("[data-action]");
  if (!button) return;
  const a = button.dataset.action;
  try {
    if(a==='toggle-version-group'){state.versionGrouping=!state.versionGrouping;renderPage();return;}
    if(a==='iterate-task'){await taskForm(null,button.dataset.id);return;}
    if(a==='iteration-more'){
      const section=button.closest('.iteration-history'),projectID=state.project.id;
      button.disabled=true;
      try {
        const history=await api(`/tasks/${button.dataset.id}/iterations?offset=${button.dataset.offset}&limit=10`);
        if(!section.isConnected || state.project.id!==projectID)return;
        $('.iteration-history-items',section).insertAdjacentHTML('beforeend',iterationRowsHTML(history.items,section.dataset.task,state.context.project_versions));
        if(history.has_more)button.dataset.offset=history.next_offset;else button.remove();
      } finally {button.disabled=false;}
      return;
    }
    if(a.startsWith('tl-')){await timelineUI.click(a,button);return;}
    if(a.startsWith('pv-')){await projectVersionUI.click(a,button);return;}
    if (a === "create-intake" || a === "dismiss-intake") {
      const intakeID = button.dataset.id, projectID = state.project.id;
      if (busyIntakes.has(intakeID)) return;
      const item = state.intakes.find(v => v.id === intakeID);
      if (!item) return;
      const actionName = a === "create-intake" ? "create" : "dismiss";
      let problem = "";
      busyIntakes.add(intakeID); updateChat();
      try {
        // A stable request id survives lost HTTP responses and browser reloads.
        const next = await api(`/requirement-intakes/${encodeURIComponent(intakeID)}/resolve`, {action:actionName,request_id:`intake:${intakeID}:${actionName}`});
        if (state.project?.id !== projectID) return;
        state.intakes = state.intakes.map(v => v.id === next.id ? next : v);
        updateChat();
        if (next.status === "created") {
          if (next.kind === "bug") {
            toast("Bug 已保存，进入待认领，开发领取后即可修复");
            return;
          }
          toast("需求已保存，Agent 将在后台评审，有结论后会标记待确认");
        }
      } catch (e) {
        problem = `${e.message}。整理内容已保留，可以重试；已保存的需求不会重复创建。`;
      } finally {
        busyIntakes.delete(intakeID); updateChat();
        const card = $$('[data-intake-id]').find(el => el.dataset.intakeId === intakeID);
        if (card && problem) $(".intake-error",card).textContent = problem;
      }
      return;
    }
    if (a === "collaboration-detail") {
      const item = await api(`/collaborations/${button.dataset.id}`);
      modal("协同排查详情", collaborationDetail(item));
      return;
    }
    if (a === "cancel-collaboration" || a === "retry-collaboration") {
      button.disabled = true;
      try {
        await api(`/collaborations/${button.dataset.id}/${a === "cancel-collaboration" ? "cancel" : "retry"}`, { request_id: id() });
        await refreshChatActivity();
      } finally { button.disabled = false; }
      return;
    }
    if (["test-repository", "generate-ssh-key", "copy-ssh-key"].includes(a)) {
      await repositoryAction(a, button);
      return;
    }
    if (a === "invite-member" || a === "add-platform-admin") { await employeeDialog(button.dataset.project, a === "add-platform-admin"); return; }
    if (a === "manage-project-members") {
      const p = state.platformProjects.find((p) => p.id === button.dataset.project);
      modal(`项目成员 · ${escape(p?.name)}`, `<div class="row between"><p class="hint">仅分配到项目的成员可以访问。</p>${action("invite-member", icon("plus") + " 添加成员", "primary", `data-project="${escape(p.id)}"`)}</div><div id="project-members">正在读取…</div>`);
      await loadProjectMembers(p.id);return;
    }
    if (a === "choose-employee") {
      const form = $("#member-form");form.dataset.user = button.dataset.user;form.hidden = false;
      $("#selected-employee").innerHTML = `<strong>${escape(button.dataset.name)}</strong><p class="hint">${escape(button.dataset.detail)}</p>`;
      $$(".employee-result").forEach((b) => b.classList.toggle("selected", b === button));
      $("select", form)?.focus();return;
    }
    if (a === "directory-page" || a === "retry-directory") { await searchEmployees(Number(button.dataset.page) || 1);return; }
    if (a === "save-member-role") {
      button.disabled = true;
      const roles = $$("input[name=roles]:checked", button.closest(".member-row")).map((v) => v.value);
      try { const p = await api(`/projects/${encodeURIComponent(button.dataset.project)}/members/${encodeURIComponent(button.dataset.user)}`, {roles}, "PUT");if (state.project?.id === p.id) state.project = p;if (!$("dialog")) render();await loadProjectMembers(button.dataset.project);toast("成员角色已更新"); }
      finally {button.disabled = false;} return;
    }
    if (a === "remove-member" || a === "remove-platform-admin") {
      modal("移除权限", `<p>确认移除 ${escape(button.dataset.name)} 的${a === "remove-member" ? "项目访问" : "平台管理"}权限？</p><form id="remove-access-form" data-user="${escape(button.dataset.user)}" data-project="${escape(button.dataset.project || "")}" data-platform="${a === "remove-platform-admin"}"><div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button type="submit" class="danger">确认移除</button></div></form>`);return;
    }
    if (a === "sync-directory") {button.disabled = true;try {const result = await api("/platform/directory/sync", {});toast(`${state.auth?.directory?.label || "通讯录"}已同步，共 ${result.total} 人`);}finally{button.disabled = false;}return;}
    if (a === "copy-mcp-url") {await navigator.clipboard.writeText(location.origin + "/mcp");toast("MCP 地址已复制");return;}
    if (a === "toggle-mcp") {
      if (button.dataset.enabled === "true") {modal("关闭个人 MCP", '<p>关闭后将撤销所有客户端授权。重新开通时需重新授权。</p><form id="disable-mcp-form"><div class="form-error"></div><div class="modal-actions">'+ action("close", "取消") + '<button type="submit" class="danger">确认关闭</button></div></form>');return;}
      await api("/mcp", {enabled:true}, "PUT");await loadMCP();toast("个人 MCP 已开通");return;
    }
    if (a === "revoke-grant") {await api(`/mcp/grants/${encodeURIComponent(button.dataset.id)}`, null, "DELETE");await loadMCP();toast("客户端授权已撤销");return;}
    if (a === "toggle-projects") {
      if (projectMenuOpen()) closeProjectMenu(true);
      else openProjectMenu();
      return;
    }
    if (a === "switch-project") {
      await switchProject(button.dataset.id);
      return;
    }
    if (a === "configure-channel") { channelForm(button.dataset.name); return; }
    if (a === "test-channel") {
      button.disabled = true;
      try { await api(`/platform/channels/${encodeURIComponent(button.dataset.name)}/test`, {}); toast("测试消息已发送，请在手机或电脑上查看"); }
      catch (e) { const error = $("#channel-form .form-error"); if (error) error.textContent = e.message; else toast(e.message); }
      finally { button.disabled = false; }
      return;
    }
    if (a === "change-password") {
      modal("修改密码", `<form id="password-form" data-modal="true">${passwordFields()}<p class="hint">保存后其他设备上的登录会失效。</p><div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button type="submit" class="primary">保存新密码</button></div></form>`);
      return;
    }
    if (a === "new-account") {
      modal("新建平台账号", `<form id="account-form">${field("用户名", '<input name="username" autocomplete="off" required>', "2-32 位字母、数字或 . _ -，用于登录")}${field("姓名", '<input name="name" autocomplete="off" placeholder="可选，默认使用用户名">')}${field("初始密码", `<input name="password" autocomplete="off" spellcheck="false" minlength="8" required value="${temporaryPassword()}">`, "已随机生成，可修改。请告知成员，其首次登录时需改为自己的密码。")}<div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button type="submit" class="primary">创建账号</button></div></form>`);
      return;
    }
    if (a === "reset-account-password") {
      modal(`重置密码 · ${escape(button.dataset.name)}`, `<form id="account-reset-form" data-user="${escape(button.dataset.user)}">${field("新的初始密码", `<input name="password" autocomplete="off" spellcheck="false" minlength="8" required value="${temporaryPassword()}">`, "保存后该成员立即退出登录，下次登录需改为自己的密码。")}<div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button type="submit" class="primary">重置密码</button></div></form>`);
      return;
    }
    if (a === "toggle-account") {
      if (button.dataset.disabled === "true") {
        button.disabled = true;
        try { await api(`/platform/accounts/${encodeURIComponent(button.dataset.user)}`, {disabled:false}, "PATCH"); await refresh(); render(); toast("账号已启用"); }
        finally { button.disabled = false; }
        return;
      }
      modal("停用账号", `<p>确认停用 ${escape(button.dataset.name)}？停用后立即退出登录，并撤销其全部 MCP 授权；项目成员关系保留，重新启用后恢复。</p><form id="account-disable-form" data-user="${escape(button.dataset.user)}"><div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button type="submit" class="danger">确认停用</button></div></form>`);
      return;
    }
    if (a === "logout") {
      await api("/auth/logout", {});
      location.assign("/");
      return;
    }
    if (a === "attach-files") {
      $("#chat-files").click();
      return;
    }
    if (a === "remove-attachment") {
      const draft = chatDraft();
      if (draft.sending) return;
      const item = draft.files.find((f) => f.id === button.dataset.id);
      if (item?.preview) URL.revokeObjectURL(item.preview);
      draft.files = draft.files.filter((f) => f.id !== button.dataset.id);
      draft.error = "";
      updateComposer();
      return;
    }
    if (a === "close") {
      closeModal();
      return;
    }
    if (a === "menu") {
      $(".sidebar").classList.toggle("open");
      return;
    }
    if (a === "navigate") {
      await navigate(button.dataset.page);
      return;
    }
    if (a === "new-project") {
      closeProjectMenu(true);
      modal(
        "创建项目",
        `<form id="project-form">${projectFields()}<div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button class="primary" type="submit">创建项目</button></div></form>`,
      );
      return;
    }
    if (a === "new-task") {
      await taskForm();
      return;
    }
    if (a === "requirement-review") { await openRequirementReview(button.dataset.id); return; }
    if (["task-close","task-decline","task-reopen"].includes(a)) { await taskResolutionForm(button.dataset.id,a.slice(5)); return; }
    if (a === "edit-task") {
      await taskForm(button.dataset.id);
      return;
    }
    if (a === "open-task") {
      await openTask(button.dataset.id);
      return;
    }
    if (a === "stat") {
      state.filter = button.dataset.filter;
      state.page = state.filter === "reviews" ? "reviews" : "tasks";
      if (state.filter === "reviews" || state.filter === "tasks")
        state.filter = "";
      render();
      return;
    }
    if (a === "sync") {
      button.disabled = true;
      button.textContent = "正在同步…";
      await api(`/projects/${state.project.id}/sync`, {});
      await refresh();
      render();
      toast("仓库已同步");
      return;
    }
    if (a === "reopen-claim") {
      const payload=JSON.parse(button.dataset.payload ||= JSON.stringify({action:"reopen_claim",request_id:id(),version:Number(button.dataset.version),revision:Number(button.dataset.revision),body:"产品重新开放领取"}));
      button.disabled=true;
      try {
        await api(`/tasks/${button.dataset.id}/actions`,payload);
        await refresh();render();await openTask(button.dataset.id);
        toast("已追加对齐内容并重新开放领取");
      } finally { if (button.isConnected) button.disabled=false; }
      return;
    }
    if (a === "ready" || a === "claim") {
      await api(`/tasks/${button.dataset.id}/actions`, {
        action: a,
        request_id: id(),
      });
      await refresh();
      render();
      await openTask(button.dataset.id);
      return;
    }
    if (a === "release") {
      modal(
        "交接任务",
        `<form id="release-form" data-id="${button.dataset.id}">${field("交接说明", `<textarea name="body" required placeholder="已经完成的内容、已有提交、未解决问题与下一步"></textarea>`)}<div class="form-error"></div><div class="modal-actions"><button type="submit" class="primary">释放并等待重新认领</button></div></form>`,
      );
      return;
    }
    if (a === "submit-result") {
      await submitForm(button.dataset.id);
      return;
    }
    if (a === "mark-deployed") {
      await deploymentForm(button.dataset.id);
      return;
    }
    if (a === "rework") {
      modal("退回开发", `<form id="rework-form" data-id="${button.dataset.id}">${field("退回原因", `<textarea name="body" required placeholder="需要继续修改或验证的内容"></textarea>`)}<div class="form-error"></div><div class="modal-actions">${action("close", "取消")}<button type="submit" class="primary">退回处理中</button></div></form>`);
      return;
    }
    if (a === "file-testing-bug") { await taskForm(null,null,button.dataset.id); return; }
    if (["test-pass","start-retest","verify-bug","return-bug"].includes(a)) { await testingForm(button.dataset.id,a.replaceAll("-","_")); return; }
    if (a === "review") {
      await reviewForm(button.dataset.id);
      return;
    }
    if (a === "new-profile" || a === "edit-profile") {
      profileForm(button.dataset.id);
      return;
    }
    if (a === "evidence") {
      evidenceForm();
      return;
    }
    if (a === "analyze" || a === "task-analyze") {
      await startRun(button.dataset.kind, button.dataset.id || "");
      return;
    }
    if (a === "open-run") {
      const r = await api(`/runs/${button.dataset.id}`);
      modal(kindNames[r.kind], `<div id="run-${r.id}">${runHTML(r)}</div>`);
      if (["running", "queued"].includes(r.status)) pollRun(r.id);
      return;
    }
    if (a === "stop-run") {
      await api(`/runs/${button.dataset.id}/stop`, {});
      toast("正在停止，已完成的记录会保留");
      return;
    }
    if (a === "retry-run") {
      const r = await api(`/runs/${button.dataset.id}`);
      await startRun(
        r.kind,
        r.task_id,
        r.prompt,
        r.kind === "question" ? chatConversations(state.runs).find((c) => c.id === conversationID(r))?.latest.id || r.id : "",
        (r.attachments || []).map((a) => a.id),
      );
      return;
    }
    if (a === "rename-conversation" || a === "delete-conversation") {
      conversationDialog(button.dataset.id, a === "delete-conversation");
      return;
    }
    if (a === "new-conversation") {
      if (chatDraft().sending) return;
      const draft = chatDraft(state.project.id, "");
      if (draft.sending) return;
      for (const item of draft.files) if (item.preview) URL.revokeObjectURL(item.preview);
      draft.text = ""; draft.files = []; draft.error = "";
      selectConversation("");
      return;
    }
    if (a === "select-conversation") {
      selectConversation(button.dataset.id);
      return;
    }
    if (a === "toggle-history") {
      $(".chat-page").classList.toggle("history-open");
      return;
    }
    if (a === "suggestion") {
      chatDraft().text = button.dataset.question;
      $("textarea[name=question]").value = button.dataset.question;
      updateComposer();
      $("textarea[name=question]").focus();
      return;
    }
    if (a === "package") {
      const selected = $$(".package-select:checked").map((el) =>
        state.context.tasks.find((t) => t.id === el.value),
      );
      if (!selected.length) throw new Error("请先勾选需要共同验收的需求");
      await api(`/projects/${state.project.id}/artifacts`, {
        kind: "acceptance",
        title: "验收包 · " + date(Date.now()),
        body: selected
          .map(
            (t) =>
              `${t.title}\n版本：${t.submission.sha} / R${t.revision}\n${t.acceptance.map((a) => "□ " + a).join("\n")}`,
          )
          .join("\n\n"),
        data: selected.map((t) => ({
          id: t.id,
          revision: t.revision,
          sha: t.submission.sha,
        })),
      });
      await refresh();
      renderPage();
      toast("已建立验收包，每个需求仍独立确认结果");
      return;
    }

    if (["open-file", "search-code", "git-log"].includes(a)) {
      let params = new URLSearchParams({
        action:
          a === "open-file" ? "file" : a === "search-code" ? "search" : "log",
      });
      if (a === "open-file") params.set("path", button.dataset.path);
      if (a === "search-code") params.set("query", $("#code-search").value);
      $("#file-content").textContent = "正在读取…";
      const v = await api(`/projects/${state.project.id}/code?${params}`);
      $("#file-content").innerHTML =
        `<div class="mono muted small">${escape(v.path || "")} · ${v.sha.slice(0, 12)}</div><div class="divider"></div><pre>${escape(v.text || "没有匹配结果")}</pre>${v.truncated ? '<p class="hint">内容较长，已截断。</p>' : ""}`;
      return;
    }
  } catch (e) {
    toast(e.message);
    if (a === "sync") {
      button.disabled = false;
      button.innerHTML = icon("refresh") + " 同步仓库";
    }
  }
});
document.addEventListener("keydown", (event) => {
  if (
    event.target.id === "project-switch" &&
    ["ArrowDown", "ArrowUp"].includes(event.key)
  ) {
    event.preventDefault();
    openProjectMenu(event.key === "ArrowUp");
    return;
  }
  if (projectMenuOpen()) {
    if (event.key === "Escape") {
      event.preventDefault();
      closeProjectMenu(true);
      return;
    }
    if (event.key === "Tab") {
      closeProjectMenu(true);
      return;
    }
    if (event.target.closest("#project-menu")) {
      const items = $$("#project-menu button");
      const index = items.indexOf(document.activeElement);
      const next = {
        ArrowDown: (index + 1) % items.length,
        ArrowUp: (index - 1 + items.length) % items.length,
        Home: 0,
        End: items.length - 1,
      }[event.key];
      if (next !== undefined) {
        event.preventDefault();
        items[next].focus();
      }
      // Native buttons handle Enter and Space once; avoid the generic row handler.
      return;
    }
  }
  if (event.target.matches("#chat-form textarea") && event.key === "Enter") {
    if (event.isComposing || event.keyCode === 229 || chatComposing) return;
    if (!event.shiftKey && !event.altKey) {
      event.preventDefault();
      if (!event.repeat && !chatDraft().sending)
        event.target.form.requestSubmit();
    }
    return;
  }
  if (event.key === "Enter" && event.target.matches("[data-action][tabindex]"))
    typeof event.target.click==='function'?event.target.click():event.target.dispatchEvent(new MouseEvent("click",{bubbles:true}));
  if (event.key === "Escape") $(".sidebar")?.classList.remove("open");
});
document.addEventListener("compositionstart", (event) => {
  if (event.target.matches("#chat-form textarea")) chatComposing = true;
});
document.addEventListener("compositionend", (event) => {
  if (event.target.matches("#chat-form textarea")) chatComposing = false;
});
document.addEventListener("paste", (event) => {
  if (!event.target.closest("#chat-form")) return;
  const clipboard = event.clipboardData;
  const files = clipboard?.files.length
    ? [...clipboard.files]
    : [...(clipboard?.items || [])]
        .filter((item) => item.kind === "file")
        .map((item) => item.getAsFile())
        .filter(Boolean);
  if (!files.length) return;
  event.preventDefault();
  const text = clipboard.getData("text/plain");
  const textarea = $("#chat-form textarea");
  if (text && !chatDraft().sending) {
    textarea.setRangeText(
      text,
      textarea.selectionStart,
      textarea.selectionEnd,
      "end",
    );
    chatDraft().text = textarea.value;
  }
  addChatFiles(files);
});
let dragDepth = 0;
document.addEventListener("dragenter", (event) => {
  const box = event.target.closest("#chat-dropzone");
  if (box && [...(event.dataTransfer?.types || [])].includes("Files")) {
    event.preventDefault();
    dragDepth++;
    box.classList.add("is-dragging");
  }
});
document.addEventListener("dragover", (event) => {
  if (
    event.target.closest("#chat-dropzone") &&
    [...(event.dataTransfer?.types || [])].includes("Files")
  ) {
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  }
});
document.addEventListener("dragleave", (event) => {
  const box = event.target.closest("#chat-dropzone");
  if (box && --dragDepth <= 0) {
    dragDepth = 0;
    box.classList.remove("is-dragging");
  }
});
document.addEventListener("drop", (event) => {
  const box = event.target.closest("#chat-dropzone");
  if (!box) return;
  event.preventDefault();
  dragDepth = 0;
  box.classList.remove("is-dragging");
  addChatFiles([...(event.dataTransfer?.files || [])]);
});
document.addEventListener("change", async (event) => {
 if(['tl-end','tl-days','tl-grain'].includes(event.target.id)){await timelineUI.change(event.target);return;}
 if (event.target.id === "department-filter") {await searchEmployees(1);return;}
  if (event.target.name === "auth_mode") {
    const form = event.target.form;
    $$("[data-auth-fields]", form).forEach(
      (el) => (el.hidden = el.dataset.authFields !== event.target.value),
    );
    $(".repository-result", form).textContent = "";
  }
  if (event.target.id === "chat-files") {
    addChatFiles([...event.target.files]);
    event.target.value = "";
    return;
  }
  if(event.target.id==='version-filter'){state.versionFilter=event.target.value;$('#task-table').innerHTML=tasksTable();return;}
  if (event.target.id === "task-filter") {
    state.filter = event.target.value;
    $("#task-table").innerHTML = tasksTable();
  }
  if (event.target.id === "kind-filter") {
    state.kind = event.target.value;
    $("#task-table").innerHTML = tasksTable();
  }
});
let employeeSearchTimer;
document.addEventListener("input", (event) => {
 if (event.target.matches("#employee-search input[name=q]")) {clearTimeout(employeeSearchTimer);employeeSearchTimer = setTimeout(() => searchEmployees(1), 300);}
  if (
    event.target.closest("#project-form") &&
    [
      "repository",
      "git_username",
      "git_token",
      "git_private_key",
      "git_known_hosts",
      "auth_mode",
    ].includes(event.target.name)
  ) {
    const form = event.target.form;
    $(".repository-result", form).textContent = "";
    if (event.target.name === "git_private_key" && event.target.value.trim()) {
      delete form.dataset.generatedCredential;
      $(".ssh-public-key", form).hidden = true;
    }
  }
  if (event.target.matches("#chat-form textarea")) {
    chatDraft().text = event.target.value;
    chatDraft().error = "";
    updateComposer();
  }
  if (event.target.id === "task-search") {
    state.search = event.target.value;
    $("#task-table").innerHTML = tasksTable();
  }
});
document.addEventListener("submit", async (event) => {
  const form = event.target;
  event.preventDefault();
  if(form.id.startsWith('pv-')){await projectVersionUI.submit(form);return;}
  if (form.id === "task-form" && requirementEditor?.busy) return;
  if (form.id === "chat-form") {
    await submitChat();
    return;
  }
  const data = Object.fromEntries(new FormData(form));
  const submitter = event.submitter;
  const error = $(".form-error", form);
  if (error) error.textContent = "";
  if (submitter) submitter.disabled = true;
  try {
    let afterTask = "";
    if (form.id === "conversation-form") {
      const projectID = form.dataset.projectId, conversation = form.dataset.id;
      const remove = form.dataset.remove === "true";
      const result = await api(`/projects/${encodeURIComponent(projectID)}/conversations/${encodeURIComponent(conversation)}`, remove ? undefined : {title: data.title}, remove ? "DELETE" : "PATCH");
      conversationMutationRevision++;
      stopRunUpdates();
      if (remove) forgetConversation(projectID, conversation);
      else if (state.project?.id === projectID) {
        state.runs = state.runs.map((r) => r.kind === "question" && conversationID(r) === conversation ? {...r, conversation_title: result.title, title_status: "manual"} : r);
        updateChat();
      }
      closeModal();
      toast(remove ? "对话已删除" : "对话名称已更新");
      pollActiveRuns();
      return;
    } else if (form.id === "employee-search") {await searchEmployees(1);return;
    } else if (form.id === "member-form") {
      if (!form.dataset.user) throw new Error("请先选择成员");
      if (form.dataset.platform === "true") await api(`/platform/admins/${encodeURIComponent(form.dataset.user)}`, {enabled:true}, "PUT");
      else await api(`/projects/${encodeURIComponent(form.dataset.project)}/members/${encodeURIComponent(form.dataset.user)}`, {roles:new FormData(form).getAll("roles")}, "PUT");
      toast(form.dataset.platform === "true" ? "平台管理员已添加" : "成员已加入项目");
    } else if (form.id === "remove-access-form") {
      if (form.dataset.platform === "true") await api(`/platform/admins/${encodeURIComponent(form.dataset.user)}`, {enabled:false}, "PUT");
      else await api(`/projects/${encodeURIComponent(form.dataset.project)}/members/${encodeURIComponent(form.dataset.user)}`, null, "DELETE");
      toast("权限已移除");
    } else if (form.id === "login-form" || form.id === "setup-form" || form.id === "password-form") {
      if (form.id !== "login-form" && data.new_password !== data.confirm_password) throw new Error("两次输入的密码不一致");
      const result = form.id === "login-form" ? await api("/auth/login", {username:data.username, password:data.password, return_to:returnTo()})
        : form.id === "setup-form" ? await api("/auth/setup", {token:data.token, username:data.username, name:data.name, password:data.new_password})
        : await api("/auth/password", {current_password:data.current_password, new_password:data.new_password, return_to:returnTo()});
      if (form.dataset.modal) { closeModal(); toast("密码已更新"); return; }
      if (result.must_change_password) location.reload();
      else location.assign(result.redirect || "/");
      return;
    } else if (form.id === "account-form") {await api("/platform/accounts", {username:data.username, name:data.name, password:data.password});toast("账号已创建，请把用户名和初始密码告知成员");
    } else if (form.id === "account-reset-form") {await api(`/platform/accounts/${encodeURIComponent(form.dataset.user)}`, {password:data.password}, "PATCH");toast("密码已重置，请把新的初始密码告知成员");
    } else if (form.id === "account-disable-form") {await api(`/platform/accounts/${encodeURIComponent(form.dataset.user)}`, {disabled:true}, "PATCH");toast("账号已停用");
    } else if (form.id === "platform-form") {await api("/platform/settings", {profile_id:data.profile_id}, "PUT");toast("平台 Agent 配置已保存");
    } else if (form.id === "channel-form") {
      const values = Object.fromEntries(Object.entries(data).filter(([k]) => k.startsWith("v_")).map(([k, v]) => [k.slice(2), v]));
      await api(`/platform/channels/${encodeURIComponent(form.dataset.name)}`, {enabled:data.enabled === "on", sign_in:data.sign_in === "on", public_url:data.public_url, values}, "PUT");
      toast("消息推送配置已保存");
    } else if (form.id === "disable-mcp-form") {await api("/mcp", {enabled:false}, "PUT");closeModal();await loadMCP();toast("个人 MCP 已关闭，授权已撤销");return;
    } else if (form.id === "project-form") {
      const project = {
        name: data.name,
        description: data.description,
        ...repositoryPayload(form),
        branch: data.branch,
        agent_read_mode: data.agent_read_mode,
        agent_ref: data.agent_ref,
      };
      delete project.project_id;
      const p = await api(
        form.dataset.id ? `/projects/${form.dataset.id}` : "/projects",
        project,
        form.dataset.id ? "PUT" : "POST",
      );
      state.project = p;
      toast("项目已保存");
    } else if (form.id === "task-form") {
      const payload = requirementEditor.payload(id);
      if (form.dataset.bugParent) payload.kind="bug";
      requirementEditor.setSubmitting(true);
      const result = await api(
        form.dataset.iterationSource ? `/tasks/${form.dataset.iterationSource}/iterations` : form.dataset.id
          ? `/tasks/${form.dataset.id}`
          : `/projects/${state.project.id}/tasks`,
        payload,
        form.dataset.id ? "PUT" : "POST",
      );
      requirementEditor.complete();
      afterTask = form.dataset.id ? result.task.id : "";
      toast(form.dataset.iterationSource ? "迭代需求已保存到当前里程碑，Agent 将在后台评审" : form.dataset.id ? "需求已保存，历史交付记录保留" : "需求已保存到当前里程碑，Agent 将在后台评审，有结论后会标记待确认");
    } else if (form.id === "task-resolution-form") {
      const payload=JSON.parse(form.dataset.payload ||= JSON.stringify({action:form.dataset.operation,request_id:id(),version:Number(form.dataset.version),body:data.body}));
      $("textarea",form).readOnly=true;
      try {localStorage.setItem(form.dataset.draftKey,JSON.stringify({body:data.body,payload}));}catch{}
      await api(`/tasks/${form.dataset.id}/actions`,payload);
      afterTask=form.dataset.id;
      try {localStorage.removeItem(form.dataset.draftKey);}catch{}
      toast(({close:"需求已关闭",decline:"需求已拒绝",reopen:"已重新打开，进入需求评审"})[form.dataset.operation]);
    } else if (form.id === "report-form") {
      await api(`/tasks/${form.dataset.id}/actions`, {
        action: "report",
        request_id: id(),
        body: data.body,
        report_kind: data.report_kind,
      });
      afterTask = form.dataset.id;
    } else if (form.id === "release-form") {
      await api(`/tasks/${form.dataset.id}/actions`, {
        action: "release",
        request_id: id(),
        body: data.body,
      });
      afterTask = form.dataset.id;
    } else if (form.id === "submit-form") {
      await api(`/tasks/${form.dataset.id}/actions`, {
        action: "submit",
        request_id: form.dataset.requestId ||= id(),
        version: Number(form.dataset.version),
        revision: Number(form.dataset.revision),
        submission: {
          ...data,
          references: data.references.split("\n").filter(Boolean),
        },
      });
      afterTask = form.dataset.id;
      toast("开发结果已保存，等待测试验证");
    } else if (form.id === "testing-form") {
      const payload = JSON.parse(form.dataset.payload ||= JSON.stringify({action:form.dataset.operation,request_id:id(),version:Number(form.dataset.version),body:data.note,test_verification:{revision:Number(form.dataset.revision),sha:data.sha,environment:data.environment,note:data.note}}));
      await api(`/tasks/${form.dataset.id}/actions`,payload);
      afterTask = form.dataset.id;
      toast("测试记录已保存");
    } else if (form.id === "deployment-form") {
      await api(`/tasks/${form.dataset.id}/actions`, {
        action: "mark_deployed",
        request_id: form.dataset.requestId ||= id(),
        version: Number(form.dataset.version),
        deployment: {revision: Number(form.dataset.revision), sha: form.dataset.sha, environment: data.environment, body: data.body},
      });
      afterTask = form.dataset.id;
      toast("上线记录已保存，进入待验收");
    } else if (form.id === "rework-form") {
      await api(`/tasks/${form.dataset.id}/actions`, {action:"rework", request_id:form.dataset.requestId ||= id(), body:data.body});
      afterTask = form.dataset.id;
      toast("已退回处理中");
    } else if (form.id === "review-form") {
      await api(`/tasks/${form.dataset.id}/actions`, {
        action: submitter.value,
        request_id: id(),
        review: {
          revision: Number(form.dataset.revision),
          sha: form.dataset.sha,
          environment: data.environment,
          note: data.note,
          checks: Array.from({ length: Number(form.dataset.count) }, (_, i) =>
            data["check_" + i] ? "passed" : "unverified",
          ),
        },
      });
      afterTask = form.dataset.id;
      toast(submitter.value === "accept" ? "验收通过" : "已退回修改");
    } else if (form.id === "profile-form") {
      await api("/profiles", {
        ...data,
        supports_images: data.supports_images === "on",
        context_window: Number(data.context_window || 0),
        max_tokens: Number(data.max_tokens || 0),
        id: form.dataset.id,
      });
      toast("模型配置已保存，请在平台设置中选为默认模型");
    } else if (form.id === "evidence-form") {
      await api(`/projects/${state.project.id}/artifacts`, {
        ...data,
        title: data.kind === "deployment" ? "部署记录" : "测试结果",
      });
      toast("证据已保存");
    } else if (form.id === "chat-form") {
      await submitChat();
      return;
    } else return;
    closeModal();
    try { await refresh(); render(); }
    catch(e) { toast(`内容已保存，但刷新列表失败：${e.message}`); }
    if (afterTask) {
      try { await openTask(afterTask); }
      catch(e) { toast(`需求已保存，打开详情暂时失败：${e.message}。请从需求池重新进入。`); }
    }
  } catch (e) {
    if (form.id==='task-resolution-form' && e.status>=400 && e.status<500) {delete form.dataset.payload;$("textarea",form).readOnly=false;try{localStorage.setItem(form.dataset.draftKey,JSON.stringify({body:$("textarea",form).value}));}catch{}}
    if (error) error.textContent = e.message;
    else toast(e.message);
  } finally {
    if (form.id === "task-form") requirementEditor?.setSubmitting(false);
    if (submitter) submitter.disabled = false;
  }
});
try {
  state.auth = await api("/auth/session");
  if (signInPending()) renderLogin();
  else {
    await refresh();
    render();
    pollActiveRuns();
    const linked = new URLSearchParams(location.search).get("task");
    if (linked) openLinkedTask(linked).catch((e) => toast(e.message));
  }
} catch (e) {
  if (signInPending()) renderLogin(e.message);
  else
    $("#app").innerHTML =
      `<div class="welcome"><h1>工作台暂时无法连接</h1><p>${escape(e.message)}</p><p>请确认 Go 服务已启动，然后刷新页面。</p></div>`;
}

function testingDetails(t,tasks=[]) {
 let html = '';
 if (t.parent_requirement_id) html += `<section class="detail-section"><h3>需求测试 Bug</h3>${action('open-task',escape(tasks.find(v=>v.id===t.parent_requirement_id)?.title || t.parent_requirement_id),'',`data-id="${escape(t.parent_requirement_id)}"`)}<p class="mono small">${escape(t.bug_context?.branch)} · ${escape(t.bug_context?.sha)}</p><p class="hint">评审基于提起时的需求 R${t.bug_context?.revision} 与提测版本；修复需合回所属需求的开发分支。</p></section>`;
 else if (t.kind === 'bug') html += '<p class="hint">平台 Bug · 不关联某个需求</p>';
 const bugs = tasks.filter(b=>b.parent_requirement_id === t.id);
 if (bugs.length) html += `<section class="detail-section"><h3>关联 Bug · ${bugs.filter(b=>!taskFinished(b)).length} 个未解决 / 共 ${bugs.length} 个</h3><p class="hint">全部 Bug 由产品验证解决或明确关闭后，需求进入待复测；测试通过后才能上线。</p>${bugs.map(b=>`<div class="record"><div class="row between">${action('open-task',escape(b.title),'',`data-id="${escape(b.id)}"`)}${status(b)}</div></div>`).join('')}</section>`;
 if (t.test_verification) html += `<section class="detail-section"><h3>${t.parent_requirement_id?'修复验证':'测试通过记录'}</h3><p class="mono small">${escape(t.test_verification.sha)} · ${escape(t.test_verification.environment)}</p><p class="prewrap">${escape(t.test_verification.note)}</p><p class="hint">${escape(t.test_verification.actor)} · ${date(t.test_verification.at)}</p></section>`;
 return html;
}
async function testingForm(taskID,operation) {
 const t=(await api(`/tasks/${taskID}`)).task;
 const title=({test_pass:'确认测试通过',start_retest:'开始复测',verify_bug:'确认修复有效',return_bug:'修复无效，退回 Bug'})[operation];
 modal(title,`<form id="testing-form" data-id="${escape(t.id)}" data-version="${t.version}" data-revision="${t.revision}" data-operation="${operation}"><p>${escape(t.title)}</p>${operation==='start_retest'?'<p class="hint">填写已合入全部 Bug 修复的需求分支提交 SHA，平台将校验版本包含关系。</p>':''}${operation==='return_bug'?'':field('实际测试版本',`<input name="sha" required value="${escape(t.submission?.sha)}" ${operation==='start_retest'?'':'readonly'}>`)+field('测试环境',`<input name="environment" required value="${escape(t.submission?.environment || '')}">`)}${field(operation==='return_bug'?'退回原因':'测试说明与证据',`<textarea name="note" required rows="4" placeholder="说明验证过程、结果和证据链接"></textarea>`)}<div class="form-error" role="alert"></div><div class="modal-actions">${action('close','取消')}<button class="primary" type="submit">${title}</button></div></form>`);
}

function testingQueueHTML() {
 const tasks=(state.context?.tasks || []).filter(t=>['testing','fixing','retest','verifying'].includes(t.status));
 return `<h2 class="section-label">测试与修复 <span class="muted small">${tasks.length} 项</span></h2><div class="review-list">${tasks.length ? tasks.map(t=>`<div class="review-item"><div class="grow"><div class="title">${escape(t.title)}</div><p class="hint">${states[t.status]} · ${t.parent_requirement_id?'需求测试 Bug':'需求 / 平台 Bug'}</p></div>${action('open-task','查看与处理','',`data-id="${escape(t.id)}"`)}</div>`).join('') : '<p class="context-note">暂无等待测试或修复的事项。</p>'}</div><div class="divider"></div>`;
}
