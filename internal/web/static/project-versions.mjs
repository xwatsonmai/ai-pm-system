import { createID } from "./id.mjs";
import { renderMarkdown } from "./markdown.mjs";
import { BASE } from "./base.mjs";
const esc = (v) =>
  String(v ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
export const versionPhases = {
  planning: "待开始",
  in_progress: "进行中",
  testing: "测试中",
  pending_release: "待上线",
  acceptance: "验收中",
  awaiting_completion: "待结束",
  completed: "已结束",
  closed: "已关闭",
};
const taskStates = {
  clarify: "需求评审",
  ready: "待认领",
  doing: "处理中",
  aligning: "待对齐",
  testing: "测试中", fixing: "待修复", retest: "待复测", verifying: "待验证", resolved: "已解决",
  pending_release: "待上线",
  review: "待验收",
  done: "已验收",
  closed: "已关闭",
  rejected: "已拒绝",
  canceled: "已取消",
};
const finished = (t) =>
  ["done", "resolved", "closed", "rejected", "canceled"].includes(t.status);
const button = (action, label, extra = "", cls = "") =>
  `<button type="button" data-action="pv-${action}" ${extra} class="${cls}">${label}</button>`;
const field = (label, body) =>
  `<label class="field"><span>${label}</span>${body}</label>`;
const date = (v) =>
  v
    ? new Date(v).toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "—";
export function versionName(versions, id) {
  return (
    versions.find((v) => v.id === id)?.name || (id ? "历史里程碑" : "历史需求")
  );
}
export function versionOptions(
  versions,
  selected = "",
  { all = false, available = false } = {},
) {
  return (
    (all ? '<option value="">所有里程碑</option>' : "") +
    versions
      .filter((v) => !available || v.status === "active")
      .map(
        (v) =>
          `<option value="${esc(v.id)}" ${v.id === selected ? "selected" : ""}>${esc(v.name)}${v.status === "active" ? " · 当前" : v.status === "planned" ? " · 待开始" : " · 已结束"}</option>`,
      )
      .join("")
  );
}

function progress(c) {
  return `<progress class="pv-progress" value="${c.development_done}" max="${Math.max(1, c.included)}" aria-label="开发完成 ${c.development_done} / ${c.included}"></progress><p class="small muted">开发完成 ${c.development_done}/${c.included} · 已上线 ${c.released}/${c.included} · 已验收 ${c.accepted}/${c.included}</p>`;
}

function stageCounts(c) {
  return `<div class="pv-stage-counts">${Object.entries(c.by_status)
    .filter(([, count]) => count > 0)
    .map(
      ([status, count]) =>
        `<span class="badge ${esc(status)}">${esc(taskStates[status] || status)} ${count}</span>`,
    )
    .join("")}</div>`;
}

function pendingOperations(project, user) {
  const prefix = `ai-pm:version-draft:${user}:${project}:`,
    out = [];
  try {
    for (const key of Object.keys(localStorage)) {
      if (!key.startsWith(prefix)) continue;
      const v = JSON.parse(localStorage.getItem(key));
      if (v?.payload?.request_id)
        out.push({
          key,
          name: v.values?.name || v.values?.body || "版本范围调整",
        });
    }
  } catch {}
  return out.length
    ? `<div class="pv-pending-operations notice"><strong>有上次提交的结果尚待确认</strong><p class="small">原请求已暂存，核对保存结果不会重复推进。</p>${out.map((v) => `<div class="pv-pending-row"><span>${esc(v.name.slice(0, 160))}</span>${button("retry-draft", "核对保存结果", `data-draft="${esc(v.key)}"`)}</div>`).join("")}</div>`
    : "";
}

export function projectVersionsPage(context, user = "local-owner") {
  const versions = context.project_versions || [],
    current = versions.find((v) => v.status === "active");
  const row = (v) =>
    `<article class="pv-version-row ${v.status === "active" ? "pv-current" : ""}"><div class="pv-version-title"><span class="badge">${v.status === "active" ? "当前里程碑 · " : ""}${versionPhases[v.phase] || v.phase}</span><h2>${button("open", esc(v.name), `data-id="${esc(v.id)}"`, "ghost")}</h2><p>${esc(v.goal || "尚未填写目标")}</p><p class="small muted">${esc(v.owner || "负责人未指定")} · 计划完成 ${esc(v.target_date || "待安排")}</p></div><div class="pv-version-progress"><strong>${v.counts.included} 项需求</strong>${progress(v.counts)}${stageCounts(v.counts)}</div>${button("open", "查看里程碑", `data-id="${esc(v.id)}"`)}</article>`;
  return `<div class="page pv-page"><div class="page-heading"><div><div class="eyebrow">PROJECT MILESTONES</div><h1>项目里程碑</h1><p>当前只推进一个里程碑，结束前新需求持续归入这里。</p></div>${current ? button("new-requirement", "创建需求", "", "primary") : button("new", "创建里程碑", "", "primary")}</div>${pendingOperations(context.project.id, user)}${current ? row(current) : '<div class="pv-no-current"><h2>暂无进行中的里程碑</h2><p>直接创建需求，保存时会自动开启下一个里程碑。</p><button data-action="new-task" class="primary">创建需求</button></div>'}<h2 class="section-label">历史与待开始计划</h2>${
    versions
      .filter((v) => v.id !== current?.id)
      .map(row)
      .join("") || '<p class="context-note">结束的里程碑会保留在这里。</p>'
  }</div>`;
}

function taskLine(item) {
  const t = item.task;
  return `<div class="pv-scope-row ${item.removed ? "pv-removed" : ""}"><div><span class="badge ${esc(t.status)}">${item.removed ? "已移出" : taskStates[t.status]}</span> <span class="small muted">${t.kind === "bug" ? "Bug 修复" : "迭代需求"} · R${t.revision}</span><h4>${button("task", esc(t.title), `data-id="${esc(t.id)}"`, "ghost")}</h4>${item.detached_revision ? `<p class="small muted">${item.detached_revision > t.revision ? `后续已产生 R${item.detached_revision}；此处保留 R${t.revision} 的交付结果。` : "后续工作已转入当前里程碑，此处保留原交付记录。"}</p>` : ""}${t.blocker && !item.removed ? `<p class="notice">阻塞：${esc(t.blocker)}</p>` : ""}${item.reason ? `<p class="small muted">${esc(item.reason)}</p>` : ""}${t.submission ? `<p class="small mono muted">${esc(t.submission.sha)}</p>` : ""}</div></div>`;
}
export function projectVersionDetail(view) {
  const v = view.project_version,
    c = view.summary.counts;
  const active = v.status === "active",
    ended = ["closed", "completed"].includes(v.status);
  const controls = active
    ? button("edit", "编辑里程碑", `data-id="${esc(v.id)}"`) +
      button("new-requirement", "创建需求") +
      button("publish", "登记本次已上线", `data-id="${esc(v.id)}"`) +
      button(
        "action-form",
        "结束里程碑",
        `data-id="${esc(v.id)}" data-operation="complete"`,
        "primary",
      ) +
      button(
        "action-form",
        "关闭里程碑",
        `data-id="${esc(v.id)}" data-operation="close"`,
      )
    : button(
        "action-form",
        ended ? "重新打开里程碑" : "开始里程碑",
        `data-id="${esc(v.id)}" data-operation="${ended ? "reopen" : "start"}"`,
      );
  return `<section class="pv-detail"><div class="detail-meta"><span class="badge">${active ? "当前里程碑 · " : ""}${versionPhases[view.summary.phase]}</span></div><h1 class="detail-title">${esc(v.name)}</h1><p class="prewrap">${esc(v.goal || "尚未填写目标")}</p><p class="muted small">${esc(v.owner || "负责人未指定")} · 计划完成 ${esc(v.target_date || "待安排")}</p><div class="detail-actions">${controls}${button("pool", "查看需求", `data-id="${esc(v.id)}"`)}${button("open", "刷新进展", `data-id="${esc(v.id)}"`)}</div><section class="pv-detail-progress"><strong>需求范围 ${c.included} 项</strong>${progress(c)}${stageCounts(c)}<p class="small muted">拒绝/关闭 ${c.ended} 项，移出 ${c.removed} 项。</p></section>${active ? '<p class="context-note">结束前可以持续追加需求；每次实际上线的清单都会独立保存。</p>' : ""}<section class="detail-section"><h3>版本范围</h3>${
    v.scope
      .filter((i) => !i.removed)
      .map(taskLine)
      .join("") ||
    '<p class="context-note">还没有需求，直接创建即可自动归入。</p>'
  }</section>${
    v.scope.some((i) => i.removed)
      ? `<details class="pv-history"><summary>已移出范围 · ${c.removed}</summary>${v.scope
          .filter((i) => i.removed)
          .map(taskLine)
          .join("")}</details>`
      : ""
  }<section class="detail-section"><h3>发布记录 · ${v.releases.length}</h3>${
    [...v.releases]
      .reverse()
      .map(
        (r) =>
          `<details class="pv-release"><summary>${esc(r.environment)} · ${date(r.at)} · ${r.tasks.length} 项</summary><p class="prewrap">${esc(r.body)}</p>${r.tasks.map((t) => `<details class="pv-release-task"><summary>${esc(t.title)} · R${t.revision}</summary><p class="mono small">${esc(t.submission?.sha)}</p><div class="markdown-content">${renderMarkdown(t.description)}</div><ul>${t.acceptance.map((a) => `<li>${esc(a)}</li>`).join("")}</ul>${t.attachments?.length ? `<p class="small muted">附件：${t.attachments.map((a) => `<a href="${BASE}/api/projects/${encodeURIComponent(t.project_id)}/attachments/${encodeURIComponent(a.id)}" target="_blank" rel="noopener">${esc(a.name)}</a>`).join("、")}</p>` : ""}</details>`).join("")}<p class="small muted">${esc(r.actor)} · 上线时的范围快照，后续修订不会改写。</p></details>`,
      )
      .join("") ||
    '<p class="context-note">开发完成后，实际发布成功再登记环境、结果与本次包含的需求。</p>'
  }</section><details class="pv-history"><summary>范围与计划变更 · ${v.history.length}</summary>${[
    ...v.history,
  ]
    .reverse()
    .map(
      (h) =>
        `<div class="record"><div class="record-header"><span>${esc(h.actor)}</span><span>${date(h.at)}</span></div><p class="prewrap">${esc(h.body)}</p>${h.task_ids?.length ? `<p class="small muted">涉及 ${h.task_ids.length} 项需求</p>` : ""}${h.user_instruction ? `<p class="small prewrap">用户原话：${esc(h.user_instruction)}</p>` : ""}</div>`,
    )
    .join("")}</details></section>`;
}
export function mountProjectVersions({
  api,
  modal,
  closeModal,
  refresh,
  getContext,
  getProject,
  getUser,
  onTask,
  onPool,
  toast,
  onCreate,
}) {
  const formEnd = (label) =>
    `<div class="form-error" role="alert"></div><p class="small muted">输入暂存在本浏览器；请求失败不会清空。</p><div class="modal-actions">${button("refresh-form", "刷新清单并保留输入")}${button("cancel", "取消")}<button type="submit" class="primary">${label}</button></div>`;
  const detail = async (id) => {
    const view = await api(`/project-versions/${encodeURIComponent(id)}`);
    modal(
      "版本详情",
      pendingOperations(getProject().id, getUser()) +
        projectVersionDetail(view),
      { drawer: true },
    );
  };
  function attachDraft(form, kind, entity = "") {
    const key = `ai-pm:version-draft:${getUser()}:${getProject().id}:${kind}:${entity}`;
    form.dataset.draftKey = key;
    form.dataset.kind = kind;
    let draft;
    try {
      draft = JSON.parse(localStorage.getItem(key) || "null");
    } catch {}
    const restore = draft?.values || {};
    for (const el of form.elements) {
      if (!el.name || restore[el.name] === undefined) continue;
      const values = Array.isArray(restore[el.name])
        ? restore[el.name]
        : [restore[el.name]];
      if (el.type === "checkbox") el.checked = values.includes(el.value);
      else el.value = values[0] ?? "";
    }
    if (draft?.payload) {
      form.dataset.payload = JSON.stringify(draft.payload);
      form.dataset.endpoint = draft.endpoint;
      form.dataset.method = draft.method;
      lock(form, true);
    }
    const save = () => {
      const values = {};
      for (const el of form.elements) {
        if (el.name && el.type === "checkbox") values[el.name] = [];
      }
      for (const el of form.elements) {
        if (!el.name) continue;
        if (el.type === "checkbox") {
          if (el.checked) values[el.name].push(el.value);
        } else values[el.name] = el.value;
      }
      try {
        localStorage.setItem(
          key,
          JSON.stringify({
            values,
            payload: form.dataset.payload
              ? JSON.parse(form.dataset.payload)
              : undefined,
            endpoint: form.dataset.endpoint,
            method: form.dataset.method,
          }),
        );
      } catch {}
    };
    form.addEventListener("input", save);
    form.addEventListener("change", save);
    form._saveVersionDraft = save;
  }
  function lock(form, value) {
    for (const el of form.elements) {
      if (el.type === "checkbox" || el.tagName === "SELECT")
        el.disabled = value;
      else if (el.tagName === "INPUT" || el.tagName === "TEXTAREA")
        el.readOnly = value;
    }
  }
  const openForm = async (id) => {
    const v = id
      ? (await api(`/project-versions/${encodeURIComponent(id)}`))
          .project_version
      : {};
    if (v.status === "closed" || v.status === "completed")
      throw Error("里程碑已结束，不能修改计划");
    modal(
      id ? "编辑里程碑" : "创建里程碑",
      `<form id="pv-form" data-id="${esc(id || "")}" data-version="${v.version || 0}">${field("版本名称", `<input name="name" required maxlength="100" value="${esc(v.name)}" placeholder="例如 v1.8 · AI质检体验优化">`)}${field("本次交付目标", `<textarea name="goal" rows="3" placeholder="这一版为用户解决什么问题？">${esc(v.goal)}</textarea>`)}<div class="field-row">${field("负责人", `<input name="owner" value="${esc(v.owner)}" placeholder="可选">`)}${field("计划完成日期", `<input name="target_date" type="date" value="${esc(v.target_date)}">`)}</div>${formEnd("保存里程碑")}</form>`,
    );
    attachDraft(document.querySelector("#pv-form"), "plan", id);
  };
  const assign = async ({ taskIDs = [], target = "", fromVersion = "" }) => {
    const context = await api(`/projects/${getProject().id}/context`);
    target =
      (context.project_versions || []).find((v) => v.status === "active")?.id ||
      "";
    if (!target) throw Error("暂无当前里程碑，请先创建里程碑或需求");
    const candidates = taskIDs.length
      ? context.tasks.filter(
          (t) =>
            taskIDs.includes(t.id) && !finished(t) && t.status !== "review",
        )
      : context.tasks.filter(
          (t) =>
            !finished(t) &&
            t.status !== "review" &&
            t.project_version_id !== target,
        );
    if (!candidates.length)
      throw Error("没有可以调整的需求；已上线需求继续在原版本验收");
    modal(
      "安排目标版本",
      `<form id="pv-assign-form" data-target="${esc(target)}" data-origin="${esc(fromVersion)}" data-tasks="${esc(JSON.stringify(candidates.map((t) => ({ id: t.id, version: t.version }))))}">${field("目标版本", `<select name="project_version_id">${versionOptions(context.project_versions || [], target, { available: true })}</select>`)}<div class="pv-select-list">${candidates.map((t) => `<label class="pv-check"><input type="checkbox" name="task_ids" value="${esc(t.id)}" ${taskIDs.length ? "checked" : ""}><span><strong>${esc(t.title)}</strong><small>${taskStates[t.status]} · 当前：${esc(versionName(context.project_versions || [], t.project_version_id))}</small></span></label>`).join("")}</div>${field("调整原因", `<textarea name="body" required rows="2" placeholder="例如：纳入本次体验优化；或延期至下一个版本"></textarea>`)}${formEnd("确认安排")}</form>`,
    );
    attachDraft(
      document.querySelector("#pv-assign-form"),
      "assign",
      taskIDs.length ? taskIDs.slice().sort().join(",") : target,
    );
  };
  const publish = async (id) => {
    const view = await api(`/project-versions/${encodeURIComponent(id)}`),
      v = view.project_version;
    if (v.status !== "active") throw Error("里程碑尚未开始或已结束");
    let items = v.scope.filter(
      (i) =>
        !i.removed &&
        i.task.status === "pending_release" &&
        i.task.submission?.revision === i.task.revision,
    );
    let pending;
    try {
      pending = JSON.parse(
        localStorage.getItem(
          `ai-pm:version-draft:${getUser()}:${getProject().id}:publish:${id}`,
        ) || "null",
      )?.payload;
    } catch {}
    if (pending?.items?.length)
      items = pending.items.map((ref) => ({
        task: {
          ...(v.scope.find((item) => item.task.id === ref.id)?.task || {
            id: ref.id,
            title: ref.id,
          }),
          version: ref.version,
          revision: ref.revision,
          submission: { sha: ref.sha },
        },
      }));
    if (!items.length) throw Error("没有当前已开发完成、等待上线的需求");
    modal(
      "登记版本已上线",
      `<form id="pv-publish-form" data-id="${esc(id)}" data-version="${v.version}" data-items="${esc(JSON.stringify(items.map((i) => ({ id: i.task.id, version: i.task.version, revision: i.task.revision, sha: i.task.submission.sha }))))}"><div class="notice info">${pending ? "上次提交结果尚未核对。下方保留原发布清单，本次将使用原请求核对保存结果。" : "请在实际发布成功后登记。只推进你勾选的需求，其余需求保持原阶段；本操作不会执行部署。"}</div><p><strong>${esc(v.name)}</strong></p><div class="pv-select-list">${items.map((i) => `<label class="pv-check"><input type="checkbox" name="task_ids" value="${esc(i.task.id)}"><span><strong>${esc(i.task.title)}</strong><small>R${i.task.revision} · ${esc(i.task.submission.sha)}</small></span></label>`).join("")}</div>${field("实际上线环境", `<input name="environment" required placeholder="例如 国内生产">`)}${field("发布结果与证据", `<textarea name="body" required rows="3" placeholder="填写实际上线结果、时间与发布记录链接"></textarea>`)}${formEnd("确认所选需求已上线")}</form>`,
    );
    attachDraft(document.querySelector("#pv-publish-form"), "publish", id);
  };
  const actionForm = async (id, operation) => {
    const view = await api(`/project-versions/${encodeURIComponent(id)}`),
      v = view.project_version;
    const labels = {
      start: "开始里程碑",
      close: "关闭里程碑",
      reopen: "重新打开里程碑",
      complete: "结束里程碑",
    };
    modal(
      labels[operation],
      `<form id="pv-action-form" data-id="${esc(id)}" data-version="${v.version}" data-operation="${esc(operation)}"><p><strong>${esc(v.name)}</strong></p>${operation === "close" ? '<div class="notice">剩余开发工作会自动转入下一个里程碑；待验收需求保留在这里继续验收。需求和历史不会被关闭或删除。</div>' : operation === "complete" ? '<p class="context-note">全部需求已验收、拒绝或关闭后可结束。下一条新需求会自动开启新里程碑。</p>' : operation === "reopen" || operation === "start" ? '<p class="context-note">项目当前不能有其他进行中的里程碑。</p>' : ""}${field("说明 / 原因", '<textarea name="body" rows="3" required></textarea>')}${formEnd("确认" + labels[operation])}</form>`,
    );
    attachDraft(document.querySelector("#pv-action-form"), operation, id);
  };
  async function click(action, node) {
    const id = node.dataset.id;
    switch (action) {
      case "pv-retry-draft": {
        const key = node.dataset.draft,
          prefix = `ai-pm:version-draft:${getUser()}:${getProject().id}:`;
        if (!key?.startsWith(prefix)) throw Error("草稿不属于当前用户与项目");
        const draft = JSON.parse(localStorage.getItem(key) || "null");
        if (!draft?.payload?.request_id)
          throw Error("原请求已确认，请刷新查看");
        if (
          !draft.endpoint?.startsWith("/project-versions/") &&
          !draft.endpoint?.startsWith(`/projects/${getProject().id}/`)
        )
          throw Error("草稿请求范围不正确");
        node.disabled = true;
        try {
          const result = await api(draft.endpoint, draft.payload, draft.method);
          localStorage.removeItem(key);
          closeModal();
          await refresh();
          toast("原操作已保存，未重复推进");
          if (result.project_version) await detail(result.project_version.id);
        } catch (e) {
          if (e.status >= 400 && e.status < 500) {
            delete draft.payload;
            localStorage.setItem(key, JSON.stringify(draft));
            throw Error(
              "原请求未保存或已冲突；输入仍在草稿，请重新打开表单核对清单。" +
                e.message,
            );
          }
          throw e;
        } finally {
          node.disabled = false;
        }
        break;
      }
      case "pv-new-requirement":
        await onCreate();
        break;
      case "pv-new":
        await openForm();
        break;
      case "pv-open":
        await detail(id);
        break;
      case "pv-edit":
        await openForm(id);
        break;
      case "pv-task":
        await onTask(id);
        break;
      case "pv-pool":
        await onPool(id);
        break;
      case "pv-cancel":
        closeModal();
        break;
      case "pv-add":
        await assign({ target: id, fromVersion: id });
        break;
      case "pv-assign":
        await assign({ taskIDs: [node.dataset.task] });
        break;
      case "pv-assign-selected": {
        const ids = [
          ...document.querySelectorAll("[data-plan-task]:checked"),
        ].map((x) => x.value);
        if (!ids.length) throw Error("先勾选需要安排版本的需求");
        await assign({ taskIDs: ids });
        break;
      }
      case "pv-publish":
        await publish(id);
        break;
      case "pv-action-form":
        await actionForm(id, node.dataset.operation);
        break;
      case "pv-refresh-form": {
        const f = node.closest("form");
        if (f.dataset.payload)
          throw Error("上次提交结果尚未确认，请保持原请求重试");
        if (f.id === "pv-form") await openForm(f.dataset.id);
        if (f.id === "pv-publish-form") await publish(f.dataset.id);
        if (f.id === "pv-action-form")
          await actionForm(f.dataset.id, f.dataset.operation);
        if (f.id === "pv-assign-form")
          await assign({
            taskIDs: JSON.parse(f.dataset.tasks).map((t) => t.id),
            target: f.elements.project_version_id.value,
            fromVersion: f.dataset.origin,
          });
        break;
      }
    }
  }
  async function submit(form) {
    let committed = false;
    if (form.dataset.saving) return;
    form.dataset.saving = "true";
    const error = form.querySelector(".form-error");
    error.textContent = "";
    const submit = form.querySelector("[type=submit]");
    submit.disabled = true;
    try {
      if (!form.dataset.payload) {
        const fields = new FormData(form),
          data = Object.fromEntries(fields),
          request_id = createID();
        form._saveVersionDraft();
        lock(form, true);
        let payload,
          path,
          method = "POST";
        if (form.id === "pv-form") {
          payload = {
            ...data,
            version: Number(form.dataset.version),
            request_id,
          };
          path = form.dataset.id
            ? `/project-versions/${form.dataset.id}`
            : `/projects/${getProject().id}/versions`;
          if (form.dataset.id) method = "PUT";
        }
        if (form.id === "pv-assign-form") {
          const ids = new Set(fields.getAll("task_ids"));
          if (!ids.size) throw Error("请选择需求");
          const target = data.project_version_id;
          const v = target
            ? (await api(`/project-versions/${target}`)).project_version
            : null;
          payload = {
            project_version_id: target,
            version: v?.version || 0,
            tasks: JSON.parse(form.dataset.tasks).filter((t) => ids.has(t.id)),
            body: data.body,
            request_id,
          };
          path = `/projects/${getProject().id}/version-assignments`;
        }
        if (form.id === "pv-publish-form") {
          const ids = new Set(fields.getAll("task_ids"));
          if (!ids.size) throw Error("请明确勾选本次实际上线的需求");
          payload = {
            action: "publish",
            version: Number(form.dataset.version),
            items: JSON.parse(form.dataset.items).filter((t) => ids.has(t.id)),
            environment: data.environment,
            body: data.body,
            request_id,
          };
          path = `/project-versions/${form.dataset.id}/actions`;
        }
        if (form.id === "pv-action-form") {
          const dest = data.destination_version_id;
          const v = dest
            ? (await api(`/project-versions/${dest}`)).project_version
            : null;
          payload = {
            action: form.dataset.operation,
            version: Number(form.dataset.version),
            body: data.body,
            destination_version_id: dest || "",
            destination_version: v?.version || 0,
            request_id,
          };
          path = `/project-versions/${form.dataset.id}/actions`;
        }
        form.dataset.payload = JSON.stringify(payload);
        form.dataset.endpoint = path;
        form.dataset.method = method;
        form._saveVersionDraft();
        lock(form, true);
      }
      const result = await api(
        form.dataset.endpoint,
        JSON.parse(form.dataset.payload),
        form.dataset.method,
      );
      committed = true;
      const stillOpen = form.isConnected;
      try {
        localStorage.removeItem(form.dataset.draftKey);
      } catch {}
      if (stillOpen) closeModal();
      await refresh();
      toast("版本信息已保存");
      if (stillOpen) {
        if (result.project_version) await detail(result.project_version.id);
        else await onPool("");
      }
    } catch (e) {
      if (committed) {
        toast("版本信息已保存，刷新暂时失败，请从项目版本重新查看。");
        return;
      }
      error.textContent = e.message;
      if (e.status >= 400 && e.status < 500) {
        delete form.dataset.payload;
        lock(form, false);
        form._saveVersionDraft();
      } else if (!form.dataset.payload) lock(form, false);
    } finally {
      delete form.dataset.saving;
      submit.disabled = false;
    }
  }
  return { click, submit, open: detail };
}
