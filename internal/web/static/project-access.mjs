import {projectPages} from './navigation.mjs';

export const responsibilities = {product:'产品',developer:'开发',tester:'测试'};
export function memberPayload(form, picked, version) {
  return {roles:[...picked], responsibilities:new FormData(form).getAll('responsibilities'), version};
}

// Role selection is independent of the visible page of options. Moving between
// option pages or searching must not drop selections made on another page.
export function updateSelection(selected, id, checked) {
  const next = new Set(selected);
  if (checked) next.add(id); else next.delete(id);
  return next;
}

export function mountProjectAccess({api,modal,closeModal,getProject,getAccess,onAccessChange,heading,field,action,escape,toast}) {
  const caches = new Map(), pickers = new WeakMap();
  let revision = 0, searchTimer;
  const cache = project => {
    if (!caches.has(project)) caches.set(project,{rolePage:1,memberPage:1,roleSize:20,memberSize:20,roleQuery:'',memberQuery:'',roles:[],members:[]});
    return caches.get(project);
  };
  const dutiesHTML = selected => `<fieldset class="role-checks responsibility-checks"><legend>待办职责</legend>${Object.entries(responsibilities).map(([id,name])=>`<label><input type="checkbox" name="responsibilities" value="${id}" ${selected.includes(id)?'checked':''}>${name}</label>`).join('')}<p class="hint">职责只决定收到哪类待办，与页面访问权限独立。</p></fieldset>`;
  const pager = (data,kind,project) => {
    const total = Number(data.total ?? data.items?.length ?? 0), size=Number(data.page_size || 20), page=Number(data.page || 1), pages=Math.max(1,Math.ceil(total/size));
    return `<div class="pagination access-pagination" aria-label="${kind==='roles'?'角色':'成员'}分页"><span>共 ${total} 项 · 第 ${page} / ${pages} 页</span><div class="row"><label>每页 <select data-access-size="${kind}" data-project="${escape(project)}" aria-label="${kind==='roles'?'角色':'成员'}每页数量">${[20,50,100].map(n=>`<option ${n===size?'selected':''}>${n}</option>`).join('')}</select></label>${action('access-page','上一页','ghost',`data-kind="${kind}" data-project="${escape(project)}" data-page="${page-1}" ${page<=1?'disabled':''}`)}${action('access-page','下一页','ghost',`data-kind="${kind}" data-project="${escape(project)}" data-page="${page+1}" ${page>=pages?'disabled':''}`)}</div></div>`;
  };
  const accessFor = async project => {
    const data = await api(`/projects/${encodeURIComponent(project)}/access`);
    cache(project).access = data;
    return data;
  };
  const changed = async project => {
    await accessFor(project);
    await onAccessChange(project);
  };
  function page() {
    const c=cache(getProject().id);
    return `<div class="page access-page">${heading('角色与权限','为成员分配角色，按页面控制访问；待办职责单独设置。')}<section class="settings-section"><div class="row between"><h2>项目角色</h2>${action('access-create-role','添加角色','primary')}</div><p>成员拥有多个角色时，页面权限取并集。获得某页权限后，可使用该页已有的业务能力。</p><input data-access-search="roles" value="${escape(c.roleQuery)}" aria-label="搜索角色" placeholder="搜索角色名称"><div id="project-roles" aria-live="polite"><p class="hint">正在读取角色…</p></div></section><section class="settings-section"><div class="row between"><h2>项目成员</h2>${action('invite-member','添加成员','',`data-project="${escape(getProject().id)}"`)}</div><input data-access-search="members" value="${escape(c.memberQuery)}" aria-label="搜索项目成员" placeholder="搜索姓名或账号"><div id="project-members" aria-live="polite"><p class="hint">正在读取成员…</p></div></section></div>`;
  }
  async function loadRoles(project=getProject()?.id) {
    const root=document.querySelector('#project-roles'); if(!root || !project)return;
    const c=cache(project), generation=++revision;
    const data=await api(`/projects/${encodeURIComponent(project)}/roles?${new URLSearchParams({page:c.rolePage,page_size:c.roleSize,q:c.roleQuery})}`);
    if(!root.isConnected || getProject()?.id!==project || generation!==revision)return;
    c.roles=data.items; c.version=data.version;
    if(c.rolePage>1 && !data.items.length){c.rolePage--;return loadRoles(project);}
    root.innerHTML = `<div class="role-list">${data.items.map(role=>`<div class="role-row"><div class="grow"><strong>${escape(role.name)}</strong><p class="hint">${escape(role.description||'未填写说明')}</p><div class="member-badges">${(role.pages||[]).map(id=>`<span class="badge">${escape(projectPages.find(p=>p.id===id)?.title||id)}</span>`).join('')||'<span class="hint">未分配页面权限</span>'}</div></div><div class="row">${action('access-edit-role','编辑','ghost',`data-id="${escape(role.id)}"`)}${action('access-delete-role','删除','ghost danger',`data-id="${escape(role.id)}"`)}</div></div>`).join('')||'<p class="context-note">没有匹配的角色。</p>'}</div>${pager(data,'roles',project)}`;
  }
  async function loadMembers(project=getProject()?.id, target='#project-members') {
    const root=document.querySelector(target); if(!root || !project)return;
    const c=cache(project),generation=(c.memberGeneration||0)+1;c.memberGeneration=generation;
    const [data,access]=await Promise.all([
      api(`/projects/${encodeURIComponent(project)}/members?${new URLSearchParams({page:c.memberPage,page_size:c.memberSize,q:c.memberQuery})}`),accessFor(project),
    ]);
    if(!root.isConnected || generation!==c.memberGeneration)return;
    c.members=data.items; c.version=data.version??access.version;
    if(c.memberPage>1 && !data.items.length){c.memberPage--;return loadMembers(project,target);}
    const names=new Map(access.roles.map(role=>[role.id,role.name]));
    root.innerHTML=`<div class="member-list">${data.items.map(m=>`<div class="member-row"><div class="grow"><strong>${escape(m.user.name)}</strong><p class="hint">${escape([m.user.login_id,m.department_name].filter(Boolean).join(' · '))}</p><div class="member-badges">${(m.roles||[]).map(id=>`<span class="badge">${escape(names.get(id)||id)}</span>`).join('')}</div><p class="hint">待办职责：${escape((m.responsibilities||[]).map(id=>responsibilities[id]||id).join('、')||'无')}</p></div>${access.can_manage?`<div class="row">${action('access-edit-member','编辑','ghost',`data-project="${escape(project)}" data-user="${escape(m.user.id)}"`)}${action('access-remove-member','移除','ghost danger',`data-project="${escape(project)}" data-user="${escape(m.user.id)}"`)}</div>`:''}</div>`).join('')||'<p class="context-note">没有匹配的成员。</p>'}</div>${pager(data,'members',project)}`;
  }
  async function load() {
    const project=getProject()?.id; if(!project)return;
    await accessFor(project);
    await Promise.all([loadRoles(project),loadMembers(project)]);
  }
  function memberFields() {
    return '<div class="member-role-picker"><p class="hint">正在读取可选角色…</p></div>';
  }
  async function mountMemberForm(form,project,selected=[],duties=[]) {
    const access=await accessFor(project); if(!form.isConnected)return;
    const available=new Set(access.roles.map(r=>r.id));
    const s={project,selected:new Set(selected.filter(id=>available.has(id))),page:1,q:'',size:20,version:access.version,names:new Map(access.roles.map(r=>[r.id,r.name])),generation:0};
    pickers.set(form,s);
    const root=form.querySelector('.member-role-picker');
    root.innerHTML=`<fieldset class="member-role-fieldset"><legend>项目角色</legend><input data-role-search aria-label="搜索可选角色" placeholder="搜索角色名称"><div class="role-selected" aria-live="polite"></div><div class="role-options"></div></fieldset>${dutiesHTML(duties)}`;
    await loadPicker(form);
  }
  const selectedHTML = s => [...s.selected].map(id=>`<button type="button" class="badge role-selected-item" data-action="access-unpick-role" data-id="${escape(id)}" aria-label="移除角色 ${escape(s.names.get(id)||id)}">${escape(s.names.get(id)||id)} ×</button>`).join('')||'<span class="hint">请至少选择一个角色。</span>';
  async function loadPicker(form) {
    const s=pickers.get(form);if(!s)return;
    const generation=++s.generation;
    const data=await api(`/projects/${encodeURIComponent(s.project)}/roles?${new URLSearchParams({page:s.page,page_size:s.size,q:s.q})}`);
    if(!form.isConnected||generation!==s.generation)return;
    for(const r of data.items)s.names.set(r.id,r.name);
    form.querySelector('.role-selected').innerHTML=selectedHTML(s);
    form.querySelector('.role-options').innerHTML=`<div class="role-checks">${data.items.map(r=>`<label><input type="checkbox" data-role-choice value="${escape(r.id)}" ${s.selected.has(r.id)?'checked':''}>${escape(r.name)}</label>`).join('')||'<p class="hint">没有匹配的角色。</p>'}</div><div class="row between"><span class="hint">第 ${data.page||s.page} / ${Math.max(1,Math.ceil((data.total??data.items.length)/s.size))} 页</span><div class="row">${action('access-picker-page','上一页','ghost',`data-page="${s.page-1}" ${s.page<=1?'disabled':''}`)}${action('access-picker-page','下一页','ghost',`data-page="${s.page+1}" ${(s.page*s.size >= (data.total??data.items.length))?'disabled':''}`)}</div></div>`;
  }
  function memberInput(form) {
    const s=pickers.get(form); if(!s)throw Error('角色仍在读取，请稍后重试');
    if(!s.selected.size)throw Error('请至少选择一个角色');
    return memberPayload(form,s.selected,s.version);
  }
  function roleForm(role,version) {
    return `<form id="access-role-form" data-id="${escape(role?.id||'')}" data-version="${version}">${field('角色名称',`<input name="name" required maxlength="80" value="${escape(role?.name||'')}">`)}${field('说明',`<textarea name="description" maxlength="1000">${escape(role?.description||'')}</textarea>`)}<fieldset class="page-permissions"><legend>可访问页面</legend>${projectPages.map(p=>`<label><input type="checkbox" name="pages" value="${p.id}" ${role?.pages?.includes(p.id)?'checked':''}>${p.title}</label>`).join('')}</fieldset><p class="hint">页面内沿用现有业务操作规则。待办职责在成员上单独设置。</p><div class="form-error" role="alert"></div><div class="modal-actions">${action('close','取消')}<button type="submit" class="primary">保存角色</button></div></form>`;
  }
  async function click(button) {
    const a=button.dataset.action;if(!a?.startsWith('access-'))return false;
    const project=button.dataset.project||getProject()?.id,c=cache(project);
    if(a==='access-page'){
      if(button.dataset.kind==='roles'){c.rolePage=Number(button.dataset.page);await loadRoles(project);}
      else{c.memberPage=Number(button.dataset.page);await loadMembers(project);}
    }else if(a==='access-picker-page'){
      const form=button.closest('form'),s=pickers.get(form);s.page=Number(button.dataset.page);await loadPicker(form);
    }else if(a==='access-unpick-role'){
      const form=button.closest('form'),s=pickers.get(form);s.selected.delete(button.dataset.id);await loadPicker(form);
    }else if(a==='access-create-role'||a==='access-edit-role'){
      const access=await accessFor(project),role=c.roles.find(r=>r.id===button.dataset.id);
      modal(role?'编辑角色':'添加角色',roleForm(role,access.version));
    }else if(a==='access-delete-role'){
      const access=await accessFor(project),role=c.roles.find(r=>r.id===button.dataset.id);if(!role)return true;
      modal('删除角色',`<form id="access-delete-role-form" data-id="${escape(role.id)}" data-version="${access.version}"><p>删除「${escape(role.name)}」。已分配该角色的成员需要转为其他角色。</p>${field('替代角色',`<select name="replacement_role_id"><option value="">无成员引用时直接删除</option>${access.roles.filter(r=>r.id!==role.id).map(r=>`<option value="${escape(r.id)}">${escape(r.name)}</option>`).join('')}</select>`)}<div class="form-error" role="alert"></div><div class="modal-actions">${action('close','取消')}<button type="submit" class="danger">删除角色</button></div></form>`);
    }else if(a==='access-edit-member'){
      const m=c.members.find(m=>m.user.id===button.dataset.user);if(!m)return true;
      modal(`编辑成员 · ${escape(m.user.name)}`,`<form id="access-member-form" data-project="${escape(project)}" data-user="${escape(m.user.id)}">${memberFields()}<div class="form-error" role="alert"></div><div class="modal-actions">${action('close','取消')}<button type="submit" class="primary">保存成员</button></div></form>`);
      await mountMemberForm(document.querySelector('#access-member-form'),project,m.roles||[],m.responsibilities||[]);
    }else if(a==='access-remove-member'){
      const m=c.members.find(m=>m.user.id===button.dataset.user);if(!m)return true;
      const access=await accessFor(project);
      modal('移除项目成员',`<form id="access-remove-member-form" data-project="${escape(project)}" data-user="${escape(m.user.id)}" data-version="${access.version}"><p>确认移除 ${escape(m.user.name)} 的项目访问权限？</p><div class="form-error" role="alert"></div><div class="modal-actions">${action('close','取消')}<button type="submit" class="danger">移除成员</button></div></form>`);
    }
    return true;
  }
  async function submit(form) {
    if(!form.id.startsWith('access-'))return false;
    const project=form.dataset.project||getProject()?.id,base=`/projects/${encodeURIComponent(project)}`,data=new FormData(form),version=Number(form.dataset.version);
    if(form.id==='access-role-form'){
      await api(base+'/roles'+(form.dataset.id?'/'+encodeURIComponent(form.dataset.id):''),{name:data.get('name'),description:data.get('description'),pages:data.getAll('pages'),version},form.dataset.id?'PUT':'POST');
    }else if(form.id==='access-delete-role-form'){
      await api(base+'/roles/'+encodeURIComponent(form.dataset.id),{version,replacement_role_id:data.get('replacement_role_id')||''},'DELETE');
    }else if(form.id==='access-member-form'){
      await api(base+'/members/'+encodeURIComponent(form.dataset.user),memberInput(form),'PUT');
    }else if(form.id==='access-remove-member-form'){
      await api(base+'/members/'+encodeURIComponent(form.dataset.user),{version},'DELETE');
    }else return false;
    closeModal();await changed(project);
    if(document.querySelector('.access-page'))await load();else await loadMembers(project);
    toast('角色与成员权限已更新');return true;
  }
  document.addEventListener('change',event=>{
    if(event.target.matches('[data-role-choice]')){
      const form=event.target.closest('form'),s=pickers.get(form);if(!s)return;
      s.selected=updateSelection(s.selected,event.target.value,event.target.checked);
      form.querySelector('.role-selected').innerHTML=selectedHTML(s);
    }
    if(event.target.matches('[data-access-size]')){
      const el=event.target,project=el.dataset.project,c=cache(project);
      if(el.dataset.accessSize==='roles'){c.roleSize=Number(el.value);c.rolePage=1;loadRoles(project).catch(e=>toast(e.message));}
      else{c.memberSize=Number(el.value);c.memberPage=1;loadMembers(project).catch(e=>toast(e.message));}
    }
  });
  document.addEventListener('input',event=>{
    const el=event.target;
    if(el.matches('[data-role-search]')){
      const form=el.closest('form'),s=pickers.get(form);if(!s)return;
      s.q=el.value;s.page=1;clearTimeout(searchTimer);searchTimer=setTimeout(()=>loadPicker(form).catch(e=>toast(e.message)),250);
    }else if(el.matches('[data-access-search]')){
      const project=getProject()?.id,c=cache(project),kind=el.dataset.accessSearch;
      if(kind==='roles'){c.roleQuery=el.value;c.rolePage=1;}else{c.memberQuery=el.value;c.memberPage=1;}
      clearTimeout(searchTimer);searchTimer=setTimeout(()=> (kind==='roles'?loadRoles(project):loadMembers(project)).catch(e=>toast(e.message)),250);
    }
  });
  return {page,load,loadMembers,memberFields,mountMemberForm,memberInput,click,submit};
}
