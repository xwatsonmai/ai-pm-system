import { createID } from "./id.mjs";
import { renderMarkdown } from "./markdown.mjs";
import { BASE } from "./base.mjs";
import {pagerHTML,pageQuery,patchKeyedHTML} from "./pagination.mjs";
import {mountVersionPicker} from "./version-picker.mjs";
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
          `<option value="${esc(v.id)}" ${v.id === selected ? "selected" : ""}>${esc(v.name)}${v.is_default ? " · 默认" : v.status === "active" ? " · 进行中" : v.status === "planned" ? " · 待开始" : " · 已结束"}</option>`,
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

const listState={project:'',page:1,size:20,query:'',status:'',result:null,error:'',generation:0};
function versionRows(result,canAccessPage=()=>true) {
 if(!result)return '<p class="context-note" role="status">正在加载版本…</p>';
 return (result.items.map(v=>`<article class="pv-version-row ${v.is_default?'pv-current':''}" data-key="${esc(v.id)}"><div class="pv-version-title"><span class="badge">${v.is_default?'默认版本 · ':''}${versionPhases[v.phase]||v.phase}</span><h2>${button('open',esc(v.name),`data-id="${esc(v.id)}"`,'ghost')}</h2><p>${esc(v.goal||'尚未填写目标')}</p><p class="small muted">${esc(v.owner||'负责人未指定')} · 计划完成 ${esc(v.target_date||'待安排')}</p></div><div class="pv-version-progress"><strong>${v.counts.included} 项需求</strong>${progress(v.counts)}${stageCounts(v.counts)}</div>${button('open','查看里程碑',`data-id="${esc(v.id)}"`)}</article>`).join('')||`<div class="pv-no-current"><h2>暂无匹配的版本</h2><p>可创建版本，独立规划并推进交付。</p>${canAccessPage('tasks')?'<button data-action="new-task" class="primary">创建需求</button>':''}</div>`)+pagerHTML(result,'pv-versions');
}
export function projectVersionsPage(context,user='local-owner',canAccessPage=()=>true) {
 const project=context.project.id;
 if(listState.project!==project){Object.assign(listState,{project,page:1,size:20,query:'',status:'',result:null,error:''})}
 return `<div class="page pv-page" data-project="${esc(project)}"><div class="page-heading"><div><div class="eyebrow">PROJECT VERSIONS</div><h1>项目版本</h1><p>多个版本可同时推进；默认版本接收未指定归属的新需求。</p></div><div class="row">${canAccessPage('tasks')?button('new-requirement','创建需求'):''}${button('new','创建版本','','primary')}</div></div>${pendingOperations(project,user)}<div class="row"><input type="search" data-version-list-search placeholder="搜索版本名称或目标" aria-label="搜索版本" value="${esc(listState.query)}"><select data-version-list-status aria-label="版本状态"><option value="">全部版本</option>${[['active','进行中'],['planned','待开始'],['completed','已结束'],['closed','已关闭']].map(([v,l])=>`<option value="${v}" ${listState.status===v?'selected':''}>${l}</option>`).join('')}</select></div><p class="form-error" data-version-list-error role="status">${esc(listState.error)}</p><div class="pv-version-results">${versionRows(listState.result,canAccessPage)}</div></div>`;
}

function taskLine(item) {
  const t = item.task;
  return `<div class="pv-scope-row ${item.removed ? "pv-removed" : ""}" data-key="${esc(t.id)}"><div><span class="badge ${esc(t.status)}">${item.removed ? "已移出" : taskStates[t.status]}</span> <span class="small muted">${t.kind === "bug" ? "Bug 修复" : "迭代需求"} · R${t.revision}</span><h4>${button("task", esc(t.title), `data-id="${esc(t.id)}"`, "ghost")}</h4>${item.detached_revision ? `<p class="small muted">${item.detached_revision > t.revision ? `后续已产生 R${item.detached_revision}；此处保留 R${t.revision} 的交付结果。` : "后续工作已转入当前里程碑，此处保留原交付记录。"}</p>` : ""}${t.blocker && !item.removed ? `<p class="notice">阻塞：${esc(t.blocker)}</p>` : ""}${item.reason ? `<p class="small muted">${esc(item.reason)}</p>` : ""}${t.submission ? `<p class="small mono muted">${esc(t.submission.sha)}</p>` : ""}</div></div>`;
}
function releaseTask(t){return `<details class="pv-release-task" data-key="${esc(t.id)}"><summary>${esc(t.title)} · R${t.revision}</summary><p class="mono small">${esc(t.submission?.sha)}</p><div class="markdown-content">${renderMarkdown(t.description)}</div><ul>${(t.acceptance||[]).map(a=>`<li>${esc(a)}</li>`).join('')}</ul>${t.attachments?.length?`<p class="small muted">附件：${t.attachments.map(a=>`<a href="${BASE}/api/projects/${encodeURIComponent(t.project_id)}/attachments/${encodeURIComponent(a.id)}" target="_blank" rel="noopener">${esc(a.name)}</a>`).join('、')}</p>`:''}</details>`}
function historyRows(result){return result.items.map(h=>`<div class="record" data-key="${esc(h.at)}:${esc(h.action)}:${esc(h.body)}"><div class="record-header"><span>${esc(h.actor)}</span><span>${date(h.at)}</span></div><p class="prewrap">${esc(h.body)}</p>${h.task_ids?.length?`<p class="small muted">涉及 ${h.task_ids.length} 项需求</p>`:''}${h.user_instruction?`<p class="small prewrap">用户原话：${esc(h.user_instruction)}</p>`:''}</div>`).join('')+pagerHTML(result,'pv-history')}
export function projectVersionDetail(view,canAccessPage=()=>true) {
 const v=view.project_version,c=view.summary.counts,active=v.status==='active',ended=['closed','completed'].includes(v.status);
 const controls=active?button('edit','编辑里程碑',`data-id="${esc(v.id)}"`)+(canAccessPage('tasks')?button('new-requirement','创建需求',`data-id="${esc(v.id)}"`):'')+button('publish','登记本次已上线',`data-id="${esc(v.id)}"`)+(!view.summary.is_default?button('action-form','设为默认版本',`data-id="${esc(v.id)}" data-operation="set_default"`):'')+button('action-form','结束里程碑',`data-id="${esc(v.id)}" data-operation="complete"`,'primary')+button('action-form','关闭里程碑',`data-id="${esc(v.id)}" data-operation="close"`):button('action-form',ended?'重新打开里程碑':'开始里程碑',`data-id="${esc(v.id)}" data-operation="${ended?'reopen':'start'}"`);
 return `<section class="pv-detail" data-version="${esc(v.id)}"><div class="detail-meta"><span class="badge">${view.summary.is_default?'默认版本 · ':''}${versionPhases[view.summary.phase]}</span></div><h1 class="detail-title">${esc(v.name)}</h1><p class="prewrap">${esc(v.goal||'尚未填写目标')}</p><p class="muted small">${esc(v.owner||'负责人未指定')} · 计划完成 ${esc(v.target_date||'待安排')}</p><div class="detail-actions">${controls}${canAccessPage('tasks')?button('pool','查看需求',`data-id="${esc(v.id)}"`):''}${button('open','刷新进展',`data-id="${esc(v.id)}"`)}</div><section class="pv-detail-progress"><strong>需求范围 ${c.included} 项</strong>${progress(c)}${stageCounts(c)}<p class="small muted">拒绝/关闭 ${c.ended} 项，移出 ${c.removed} 项。</p></section><section class="detail-section"><h3>版本范围</h3><label class="hint"><input type="checkbox" data-version-include-removed> 包含已移出需求</label><div data-version-scope>正在加载…</div></section><section class="detail-section"><h3>发布记录</h3><div data-version-releases>正在加载…</div></section><details class="pv-history"><summary>范围与计划变更</summary><div data-version-history>正在加载…</div></details></section>`;
}

export function mountProjectVersions({
  api,
  modal,
  closeModal,
  getModalRevision=()=>0,
  refresh,
  getContext,
  getProject,
  getUser,
  onTask,
  onPool,
  toast,
  onCreate,
  canAccessPage=()=>true,
}) {
  const formEnd = (label) =>
    `<div class="form-error" role="alert"></div><p class="small muted">输入暂存在本浏览器；请求失败不会清空。</p><div class="modal-actions">${button("refresh-form", "刷新清单并保留输入")}${button("cancel", "取消")}<button type="submit" class="primary">${label}</button></div>`;
  let detailState=null,modalRequest=0;
  const sectionRequests=new WeakMap();
  const beginSection=root=>{const generation=(sectionRequests.get(root)||0)+1;sectionRequests.set(root,generation);return generation;};
  const currentSection=(root,generation)=>root.isConnected&&sectionRequests.get(root)===generation;
  const beginModalRequest=()=>({sequence:++modalRequest,revision:getModalRevision(),project:getProject()?.id});
  const currentModalRequest=token=>token.sequence===modalRequest&&token.revision===getModalRevision()&&token.project===getProject()?.id&&canAccessPage('versions');
  async function loadPage(){
   const root=document.querySelector('.pv-page');if(!root||root.dataset.project!==getProject()?.id)return;
   const project=getProject().id,epoch=++listState.generation,params=pageQuery(listState.page,listState.size);
   if(listState.query)params.set('q',listState.query);if(listState.status)params.set('status',listState.status);
   try{const result=await api(`/projects/${project}/versions?${params}`);if(epoch!==listState.generation||!root.isConnected||project!==getProject()?.id)return;listState.result=result;listState.error='';patchKeyedHTML(root.querySelector('.pv-version-results'),versionRows(result,canAccessPage));root.querySelector('[data-version-list-error]').textContent=''}catch(e){if(root.isConnected){listState.error=e.message;root.querySelector('[data-version-list-error]').textContent=e.message}}
  }
  async function detailPart(kind,page=1,size=20){
   const root=document.querySelector('.pv-detail');if(!root||!detailState||root.dataset.version!==detailState.id)return;
   const target=root.querySelector(`[data-version-${kind}]`),generation=beginSection(target),id=detailState.id,params=pageQuery(page,size);if(kind==='scope'&&root.querySelector('[data-version-include-removed]').checked)params.set('include_removed','true');
   const result=await api(`/project-versions/${encodeURIComponent(id)}/${kind}?${params}`);if(!root.isConnected||detailState?.id!==id||!currentSection(target,generation))return;
   detailState[kind]={page,size};let html;
   if(kind==='scope')html=(result.items.map(taskLine).join('')||'<p class="context-note">暂无需求范围。</p>')+pagerHTML(result,'pv-scope');
   if(kind==='releases')html=(result.items.map(r=>`<details class="pv-release" data-key="${esc(r.id)}"><summary>${esc(r.environment)} · ${date(r.at)} · ${r.task_count||0} 项</summary><p class="prewrap">${esc(r.body)}</p>${button('release-tasks','查看上线快照',`data-id="${esc(id)}" data-release="${esc(r.id)}"`)}<div data-release-tasks="${esc(r.id)}"></div><p class="small muted">${esc(r.actor)} · 上线时的范围快照，后续修订不会改写。</p></details>`).join('')||'<p class="context-note">暂无发布记录。</p>')+pagerHTML(result,'pv-releases');
   if(kind==='history')html=historyRows(result);
   patchKeyedHTML(target,html);
  }
  const detail=async id=>{
   const token=beginModalRequest(),view=await api(`/project-versions/${encodeURIComponent(id)}?summary=1`);
   if(!currentModalRequest(token))return;
   detailState={id};modal('版本详情',pendingOperations(getProject().id,getUser())+projectVersionDetail(view,canAccessPage),{drawer:true});
   await Promise.all(['scope','releases','history'].map(kind=>detailPart(kind)));
  };
  async function releaseTasks(id,release,page=1,size=20){
   const root=document.querySelector(`[data-release-tasks="${CSS.escape(release)}"]`);if(!root)return;
   const generation=beginSection(root),result=await api(`/project-versions/${encodeURIComponent(id)}/releases/${encodeURIComponent(release)}/tasks?${pageQuery(page,size)}`);if(!currentSection(root,generation))return;
   root.dataset.version=id;root.dataset.release=release;patchKeyedHTML(root,result.items.map(releaseTask).join('')+pagerHTML(result,'pv-release-tasks'));
  }
  function attachDraft(form, kind, entity = "") {
    const key = `ai-pm:version-draft:${getUser()}:${getProject().id}:${kind}:${entity}`;
    form.dataset.draftKey = key;
    form.dataset.kind = kind;
    let draft;
    try {
      draft = JSON.parse(localStorage.getItem(key) || "null");
    } catch {}
    const restore = draft?.values || {};
    form.dataset.draftValues=JSON.stringify(restore);
    for (const el of form.elements) {
      if (!el.name || restore[el.name] === undefined) continue;
      const values = Array.isArray(restore[el.name])
        ? restore[el.name]
        : [restore[el.name]];
      if (el.type === "checkbox") el.checked = values.includes(el.value);
      else el.value = values[0] ?? "";
    }
    if (draft?.selected_refs) form.dataset.selectedRefs=JSON.stringify(draft.selected_refs);
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
            selected_refs: form.dataset.selectedRefs ? JSON.parse(form.dataset.selectedRefs) : undefined,
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
    for (const control of form.querySelectorAll('[data-version-trigger],[data-version-page]')) control.disabled=value;
    for (const el of form.elements) {
      if (el.type === "checkbox" || el.tagName === "SELECT")
        el.disabled = value;
      else if (el.tagName === "INPUT" || el.tagName === "TEXTAREA")
        el.readOnly = value;
    }
  }
  const openForm = async (id) => {
    const token=beginModalRequest();
    const v = id
      ? (await api(`/project-versions/${encodeURIComponent(id)}?summary=1`))
          .project_version
      : {};
    if(!currentModalRequest(token))return;
    if (v.status === "closed" || v.status === "completed")
      throw Error("里程碑已结束，不能修改计划");
    modal(
      id ? "编辑里程碑" : "创建里程碑",
      `<form id="pv-form" data-id="${esc(id || "")}" data-version="${v.version || 0}">${field("版本名称", `<input name="name" required maxlength="100" value="${esc(v.name)}" placeholder="例如 v1.8 · AI质检体验优化">`)}${field("本次交付目标", `<textarea name="goal" rows="3" placeholder="这一版为用户解决什么问题？">${esc(v.goal)}</textarea>`)}<div class="field-row">${field("负责人", `<input name="owner" value="${esc(v.owner)}" placeholder="可选">`)}${field("计划完成日期", `<input name="target_date" type="date" value="${esc(v.target_date)}">`)}</div>${id?'':'<label class="hint"><input type="checkbox" name="set_default" value="true"> 设为新需求的默认版本</label>'}${formEnd("保存里程碑")}</form>`,
    );
    attachDraft(document.querySelector("#pv-form"), "plan", id);
  };
  function selectedRefs(form) {
   return new Map(JSON.parse(form.dataset.selectedRefs||'[]').map(ref=>[ref.id,ref]));
  }
  function updateSelection(form) {
   const selected=selectedRefs(form),available=new Map(JSON.parse(form.dataset.items||'[]').map(ref=>[ref.id,ref]));
   for(const input of form.querySelectorAll('[name=task_ids]')){if(input.checked&&available.has(input.value))selected.set(input.value,available.get(input.value));else selected.delete(input.value)}
   form.dataset.selectedRefs=JSON.stringify([...selected.values()]);form.querySelector('[data-selection-count]').textContent=`已选择 ${selected.size} 项（跨页保留）`;form._saveVersionDraft?.();
  }
  async function selectionPage(form,page=1,size=20){
   if(!form?.isConnected)return;updateSelection(form);
   const generation=beginSection(form),id=form.dataset.id,params=pageQuery(page,size);let result,items;
   if(form.dataset.payload){const refs=JSON.parse(form.dataset.payload).items||[];result={items:refs.slice((page-1)*size,page*size).map(ref=>({task:{id:ref.id,title:ref.id,version:ref.version,revision:ref.revision,submission:{sha:ref.sha}}})),total:refs.length,page,page_size:size};items=result.items.map(i=>i.task)}
   else if(form.id==='pv-publish-form'){params.set('status','pending_release');result=await api(`/project-versions/${encodeURIComponent(id)}/scope?${params}`);items=result.items.map(i=>i.task)}
   else if(form.dataset.fixedIds){const tasks=JSON.parse(form.dataset.fixedTasks);result={items:tasks.slice((page-1)*size,page*size),total:tasks.length,page,page_size:size};items=result.items}
   else {if(form.dataset.origin)params.set('project_version_id',form.dataset.origin);result=await api(`/projects/${getProject().id}/tasks?${params}`);if(Array.isArray(result))result={items:result.slice((page-1)*size,page*size),total:result.length,page,page_size:size};items=result.items.filter(t=>!finished(t)&&t.status!=='review')}
   if(!currentSection(form,generation))return;
   const selected=selectedRefs(form),refs=items.map(t=>({id:t.id,version:t.version,revision:t.revision,sha:t.submission?.sha}));form.dataset.items=JSON.stringify(refs);
   const key=form.id==='pv-publish-form'?'pv-publish':'pv-assign';
   const html=items.map(t=>`<label class="pv-check" data-key="${esc(t.id)}"><input type="checkbox" name="task_ids" value="${esc(t.id)}" ${selected.has(t.id)?'checked':''} ${form.dataset.payload?'disabled':''}><span><strong>${esc(t.title)}</strong><small>${taskStates[t.status]||''} · R${t.revision||1}${form.id==='pv-publish-form'?` · ${esc(t.submission?.sha||'')}`:''}</small></span></label>`).join('')||'<p class="context-note">本页没有可选择的需求。</p>';
   // Paging is an explicit user action; selected IDs live outside these nodes.
   form.querySelector('[data-selection-items]').innerHTML=html;
   patchKeyedHTML(form.querySelector('[data-selection-pager]'),pagerHTML(result,key));form.dataset.selectionPage=page;form.dataset.selectionSize=size;
  }
  const assign=async({taskIDs=[],target='',fromVersion=''})=>{
   const token=beginModalRequest();
   const tasks=taskIDs.length?await Promise.all(taskIDs.map(async id=>(await api(`/tasks/${encodeURIComponent(id)}`)).task)):[];
   if(!currentModalRequest(token))return;
   const candidates=tasks.filter(t=>!finished(t)&&t.status!=='review');if(taskIDs.length&&!candidates.length)throw Error('没有可以调整的需求；已上线需求继续在原版本验收');
   target=target||getContext().current_milestone?.id||'';
   modal('安排目标版本',`<form id="pv-assign-form" data-origin="${esc(fromVersion)}" ${taskIDs.length?`data-fixed-ids="true" data-fixed-tasks="${esc(JSON.stringify(candidates))}"`:''}><div data-assignment-target></div><p class="hint">关联需求与未结束 Bug 会一起调整版本，保留原发布快照。</p><p class="hint" data-selection-count>已选择 0 项（跨页保留）</p><div class="pv-select-list" data-selection-items></div><div data-selection-pager></div>${field('调整原因','<textarea name="body" required rows="2"></textarea>')}${formEnd('确认安排')}</form>`);
   const form=document.querySelector('#pv-assign-form');attachDraft(form,'assign',taskIDs.length?taskIDs.slice().sort().join(','):target);
   if(!form.dataset.selectedRefs&&taskIDs.length)form.dataset.selectedRefs=JSON.stringify(candidates.map(t=>({id:t.id,version:t.version})));
   await mountVersionPicker(form.querySelector('[data-assignment-target]'),{api,projectID:getProject().id,selected:JSON.parse(form.dataset.draftValues||'{}').project_version_id||target});
   if(form.dataset.payload)lock(form,true);
   await selectionPage(form);updateSelection(form);
  };
  const publish=async id=>{
   const token=beginModalRequest();
   const view=await api(`/project-versions/${encodeURIComponent(id)}?summary=1`),v=view.project_version;
   if(!currentModalRequest(token))return;
   if(v.status!=='active')throw Error('版本尚未开始或已结束');
   modal('登记版本已上线',`<form id="pv-publish-form" data-id="${esc(id)}" data-version="${v.version}"><div class="notice info">请在实际发布成功后登记。只推进勾选的需求；本操作不会执行部署。待核对的原请求将原样重试。</div><p><strong>${esc(v.name)}</strong></p><p class="hint" data-selection-count>已选择 0 项（跨页保留）</p><div class="pv-select-list" data-selection-items></div><div data-selection-pager></div>${field('实际上线环境','<input name="environment" required placeholder="例如 生产">')}${field('发布结果与证据','<textarea name="body" required rows="3"></textarea>')}${formEnd('确认所选需求已上线')}</form>`);
   const form=document.querySelector('#pv-publish-form');attachDraft(form,'publish',id);
   if(form.dataset.payload)form.dataset.selectedRefs=JSON.stringify(JSON.parse(form.dataset.payload).items||[]);
   await selectionPage(form);updateSelection(form);
  };
  const actionForm=async(id,operation)=>{
   const token=beginModalRequest();
   const view=await api(`/project-versions/${encodeURIComponent(id)}?summary=1`),v=view.project_version;
   if(!currentModalRequest(token))return;
   const labels={start:'开始里程碑',close:'关闭里程碑',reopen:'重新打开里程碑',complete:'结束里程碑',set_default:'设为默认版本'};
   const choose=operation==='close'||(operation==='complete'&&view.summary.is_default);
   modal(labels[operation],`<form id="pv-action-form" data-id="${esc(id)}" data-version="${v.version}" data-default-version="${view.summary.default_version}" data-operation="${esc(operation)}"><p><strong>${esc(v.name)}</strong></p>${operation==='close'?'<div class="notice">选择剩余开发工作的目标版本，或创建后继版本。待验收需求保留原版继续验收；其他版本继续推进。</div>':operation==='set_default'?'<p class="context-note">仅改变未指定归属的新需求去向，已有需求保持原版本。</p>':'<p class="context-note">各版本独立推进，原需求及发布历史保留。</p>'}${choose?'<div data-successor-picker></div>':''}${field('说明 / 原因','<textarea name="body" rows="3" required></textarea>')}${formEnd('确认'+labels[operation])}</form>`);
   const form=document.querySelector('#pv-action-form');attachDraft(form,operation,id);
   if(choose)await mountVersionPicker(form.querySelector('[data-successor-picker]'),{api,projectID:getProject().id,name:'destination_version_id',label:operation==='close'?'剩余工作去向':'后继默认版本',emptyLabel:operation==='close'?'创建后继版本':'自动选择唯一进行中版本（没有时留空）',exclude:id,selected:JSON.parse(form.dataset.draftValues||'{}').destination_version_id||''});
   if(form.dataset.payload)lock(form,true);
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
        if(!canAccessPage('tasks'))throw Error('没有需求池访问权限');
        await onCreate(id);
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
      case "pv-release-tasks": await releaseTasks(id,node.dataset.release);break;
      case "pv-task":
        await onTask(id);
        break;
      case "pv-pool":
        if(!canAccessPage('tasks'))throw Error('没有需求池访问权限');
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
            taskIDs: JSON.parse(f.dataset.selectedRefs||'[]').map((t) => t.id),
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
            set_default: fields.has('set_default'),
            request_id,
          };
          path = form.dataset.id
            ? `/project-versions/${form.dataset.id}`
            : `/projects/${getProject().id}/versions`;
          if (form.dataset.id) method = "PUT";
        }
        if(form.id==='pv-assign-form'||form.id==='pv-publish-form'){
         updateSelection(form);const refs=[...selectedRefs(form).values()];if(!refs.length)throw Error('请明确勾选本次需求');if(refs.length>100)throw Error('单次最多选择100项，请分批操作');
         if(form.id==='pv-assign-form'){
          const target=data.project_version_id;if(!target)throw Error('请选择目标进行中版本');const v=(await api(`/project-versions/${encodeURIComponent(target)}?summary=1`)).project_version;
          payload={project_version_id:target,version:v.version,tasks:refs.map(ref=>({id:ref.id,version:ref.version})),body:data.body,request_id};path=`/projects/${getProject().id}/version-assignments`;
         }else{payload={action:'publish',version:Number(form.dataset.version),items:refs,environment:data.environment,body:data.body,request_id};path=`/project-versions/${form.dataset.id}/actions`}
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
            create_successor:form.dataset.operation==='close'&&!dest,
            default_version:Number(form.dataset.defaultVersion||0),
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
  const report=e=>toast(e.message);
  document.addEventListener('click',event=>{
   const node=event.target.closest('[data-action="list-page"]');if(!node||!node.dataset.list?.startsWith('pv-')||node.dataset.list.startsWith('pv-choice-'))return;
   const key=node.dataset.list,page=Number(node.dataset.page);event.preventDefault();event.stopPropagation();
   if(key==='pv-versions'){listState.page=page;void loadPage().catch(report)}
   else if(['pv-scope','pv-releases','pv-history'].includes(key)){const kind=key.slice(3);void detailPart(kind,page,detailState?.[kind]?.size||20).catch(report)}
   else if(key==='pv-publish'||key==='pv-assign'){const form=node.closest('form');void selectionPage(form,page,Number(form.dataset.selectionSize||20)).catch(report)}
   else if(key==='pv-release-tasks'){const host=node.closest('[data-release-tasks]');void releaseTasks(host.dataset.version,host.dataset.release,page,20).catch(report)}
  });
  let searchTimer;
  document.addEventListener('input',event=>{if(event.target.matches('[data-version-list-search]')){clearTimeout(searchTimer);searchTimer=setTimeout(()=>{listState.query=event.target.value.trim();listState.page=1;void loadPage().catch(report)},250)}});
  document.addEventListener('change',event=>{
   const node=event.target,key=node.dataset.pageSize;
   if(node.matches('[data-version-list-status]')){listState.status=node.value;listState.page=1;void loadPage().catch(report)}
   if(node.matches('[data-version-include-removed]'))void detailPart('scope').catch(report);
   if(node.name==='task_ids'&&node.closest('#pv-assign-form,#pv-publish-form'))updateSelection(node.closest('form'));
   if(key==='pv-versions'){listState.size=Number(node.value);listState.page=1;void loadPage().catch(report)}
   else if(['pv-scope','pv-releases','pv-history'].includes(key))void detailPart(key.slice(3),1,Number(node.value)).catch(report);
   else if(key==='pv-publish'||key==='pv-assign')void selectionPage(node.closest('form'),1,Number(node.value)).catch(report);
   else if(key==='pv-release-tasks'){const host=node.closest('[data-release-tasks]');void releaseTasks(host.dataset.version,host.dataset.release,1,Number(node.value)).catch(report)}
  });
  return { click, submit, open: detail, loadPage };
}
