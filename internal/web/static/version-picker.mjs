import {pageQuery} from './pagination.mjs';
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
let sequence=0;
const mounts=new WeakMap();
// A bounded, searchable active-version selector. The selected ID is independent
// from the current option page and stays in the enclosing form's draft.
export async function mountVersionPicker(host,{api,projectID,selected='',name='project_version_id',label='目标版本',emptyLabel='使用项目默认版本',exclude='',onChange,status='active',compact=false}={}) {
 if(!host)return;
 const key=`pv-choice-${++sequence}`;
 mounts.set(host,key);host.classList.add('version-picker');host.classList.toggle('version-picker--compact',compact);
 const native=typeof host.showPopover==='function';
 host.innerHTML=`<label for="${key}" class="${compact?'sr-only':'version-picker-label'}">${esc(label)}</label><button type="button" id="${key}" class="version-picker-trigger" data-version-trigger aria-haspopup="listbox" aria-controls="${key}-options" aria-expanded="false"><span data-version-value>${esc(emptyLabel)}</span><svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true"><path d="m4 6 4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.5"/></svg></button><input type="hidden" name="${esc(name)}" value="${esc(selected)}"><div class="version-picker-panel" ${native?'popover="auto"':'hidden'}><input type="search" data-version-search placeholder="${status==='active'?'搜索进行中版本':'搜索版本'}" aria-label="搜索${esc(label)}" autocomplete="off"><select id="${key}-options" size="6" data-version-options aria-label="${esc(label)}"><option value="">${esc(emptyLabel)}</option></select><div data-version-pager></div><p class="hint" data-version-error role="status" hidden></p></div>`;
 const hidden=host.querySelector('input[type=hidden]'),select=host.querySelector('select'),search=host.querySelector('input[type=search]'),trigger=host.querySelector('[data-version-trigger]'),panel=host.querySelector('.version-picker-panel'),error=host.querySelector('[data-version-error]');
 let page=1,query='',selectedName=selected,generation=0,timer,keyboardSelection=false;
 const current=()=>host.isConnected&&mounts.get(host)===key;
 const opened=()=>native?panel.matches(':popover-open'):!panel.hidden;
 const updateLabel=()=>{const text=hidden.value?selectedName:emptyLabel;host.querySelector('[data-version-value]').textContent=text;trigger.setAttribute('aria-label',`${label}：${text}`)};
 const place=()=>{
  if(!native||!current()||!opened())return;
  const rect=trigger.getBoundingClientRect(),width=Math.min(Math.max(rect.width,300),innerWidth-24);
  panel.style.width=`${width}px`;panel.style.left=`${Math.max(12,Math.min(rect.left,innerWidth-width-12))}px`;
  const height=panel.getBoundingClientRect().height;
  panel.style.top=`${Math.max(12,Math.min(rect.bottom+6,innerHeight-height-12))}px`;
 };
 const close=()=>{if(native){if(opened())panel.hidePopover()}else{panel.hidden=true;trigger.setAttribute('aria-expanded','false')}};
 const toggled=()=>{
  const open=opened();trigger.setAttribute('aria-expanded',String(open));
  window.removeEventListener('resize',place);window.removeEventListener('scroll',place,true);
  if(open){place();if(native){window.addEventListener('resize',place);window.addEventListener('scroll',place,true)}search.focus()}
 };
 panel.addEventListener('toggle',toggled);
 host.addEventListener('focusout',()=>{if(!native)queueMicrotask(()=>{if(current()&&opened()&&!host.contains(document.activeElement))close()})});
 trigger.addEventListener('click',event=>{event.preventDefault();if(trigger.disabled||hidden.disabled||hidden.readOnly)return;if(native){panel.togglePopover()}else{panel.hidden=!panel.hidden;toggled()}});
 panel.addEventListener('keydown',event=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();close();trigger.focus()}});
 function commit(){
  if(select.disabled||trigger.disabled||hidden.disabled||hidden.readOnly)return;
  hidden.value=select.value;selectedName=select.selectedOptions[0]?.textContent||'';updateLabel();hidden.dispatchEvent(new Event('input',{bubbles:true}));onChange?.(hidden.value);
 }
 async function load(){
  const epoch=++generation,params=pageQuery(page,20);if(status)params.set('status',status);if(query)params.set('q',query);
  try{
   const result=await api(`/projects/${encodeURIComponent(projectID)}/version-options?${params}`);if(epoch!==generation||!current())return;
   page=result.page;
   const items=result.items.filter(v=>v.id!==exclude);
   const match=items.find(v=>v.id===hidden.value);if(match)selectedName=match.name;
   const pinned=hidden.value&&!items.some(v=>v.id===hidden.value)?`<option value="${esc(hidden.value)}" selected>${esc(selectedName)}</option>`:'';
   select.innerHTML=`<option value="">${esc(emptyLabel)}</option>${pinned}${items.map(v=>`<option value="${esc(v.id)}">${esc(v.name)}${v.is_default?' · 默认':''}</option>`).join('')}`;select.value=hidden.value;
   const pages=Math.max(1,Math.ceil(result.total/result.page_size));
   const pager=host.querySelector('[data-version-pager]');pager.hidden=pages<=1;
   pager.innerHTML=pages>1?`<div class="version-picker-pages" aria-label="版本选项分页"><span>${page} / ${pages}</span><button type="button" data-version-page="${page-1}" ${page<=1?'disabled':''}>上一页</button><button type="button" data-version-page="${page+1}" ${page>=pages?'disabled':''}>下一页</button></div>`:'';
   updateLabel();error.hidden=true;error.textContent='';place();
  }catch(e){if(epoch===generation&&current()){error.hidden=false;error.textContent=e.message;place()}}
 }
 search.addEventListener('input',()=>{clearTimeout(timer);timer=setTimeout(()=>{query=search.value.trim();page=1;void load()},250)});
 search.addEventListener('keydown',event=>{if(event.key==='ArrowDown'){event.preventDefault();select.focus()}else if(event.key==='Enter'){event.preventDefault();clearTimeout(timer);query=search.value.trim();page=1;void load().then(()=>{if(current()&&opened())select.focus()})}});
 select.addEventListener('keydown',event=>{keyboardSelection=['ArrowDown','ArrowUp','Home','End'].includes(event.key);if(event.key==='Enter'){event.preventDefault();commit();close();trigger.focus()}});
 select.addEventListener('keyup',()=>{keyboardSelection=false});
 select.addEventListener('change',()=>{commit();if(!keyboardSelection){close();trigger.focus()}});
 host.addEventListener('click',event=>{const button=event.target.closest('[data-version-page]');if(button&&host.contains(button)){event.preventDefault();event.stopPropagation();if(button.disabled||trigger.disabled)return;page=Number(button.dataset.versionPage);void load()}});
 updateLabel();
 if(selected)try{const v=await api(`/projects/${encodeURIComponent(projectID)}/version-options/${encodeURIComponent(selected)}`);if(current()&&hidden.value===selected){selectedName=v.name;updateLabel()}}catch{}
 await load();return {value:()=>hidden.value};
}
