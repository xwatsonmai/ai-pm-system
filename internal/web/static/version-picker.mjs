import {pagerHTML,pageQuery,patchKeyedHTML} from './pagination.mjs';
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
let sequence=0;
// A bounded, searchable active-version selector. The selected ID is independent
// from the current option page and stays in the enclosing form's draft.
export async function mountVersionPicker(host,{api,projectID,selected='',name='project_version_id',label='目标版本',emptyLabel='使用项目默认版本',exclude='',onChange,status='active'}={}) {
 if(!host)return;
 const key=`pv-choice-${++sequence}`;
 host.innerHTML=`<label class="field"><span>${esc(label)}</span><input type="search" data-version-search placeholder="搜索进行中版本" aria-label="搜索${esc(label)}"><select data-version-options aria-label="${esc(label)}"><option value="">${esc(emptyLabel)}</option></select><input type="hidden" name="${esc(name)}" value="${esc(selected)}"></label><div data-version-pager></div><p class="hint" data-version-error role="status"></p>`;
 const hidden=host.querySelector('input[type=hidden]'),select=host.querySelector('select'),search=host.querySelector('input[type=search]');
 let page=1,size=20,query='',selectedName=selected,generation=0,timer;
 if(selected)try{const v=await api(`/projects/${encodeURIComponent(projectID)}/version-options/${encodeURIComponent(selected)}`);selectedName=v.name}catch{}
 async function load(){
  const epoch=++generation,params=pageQuery(page,size);if(status)params.set('status',status);if(query)params.set('q',query);
  try{
   const result=await api(`/projects/${encodeURIComponent(projectID)}/version-options?${params}`);if(epoch!==generation||!host.isConnected)return;
   const items=result.items.filter(v=>v.id!==exclude);
   const match=items.find(v=>v.id===hidden.value);if(match)selectedName=match.name;
   const pinned=hidden.value&&!items.some(v=>v.id===hidden.value)?`<option value="${esc(hidden.value)}" selected>${esc(selectedName)}</option>`:'';
   select.innerHTML=`<option value="">${esc(emptyLabel)}</option>${pinned}${items.map(v=>`<option value="${esc(v.id)}">${esc(v.name)}${v.is_default?' · 默认':''}</option>`).join('')}`;select.value=hidden.value;
   patchKeyedHTML(host.querySelector('[data-version-pager]'),pagerHTML(result,key));host.querySelector('[data-version-error]').textContent='';
  }catch(e){if(epoch===generation)host.querySelector('[data-version-error]').textContent=e.message}
 }
 search.addEventListener('input',()=>{clearTimeout(timer);timer=setTimeout(()=>{query=search.value.trim();page=1;void load()},250)});
 select.addEventListener('change',()=>{hidden.value=select.value;selectedName=select.selectedOptions[0]?.textContent||'';hidden.dispatchEvent(new Event('input',{bubbles:true}));onChange?.(hidden.value)});
 host.addEventListener('click',event=>{const button=event.target.closest(`[data-list="${key}"]`);if(button){event.preventDefault();event.stopPropagation();page=Number(button.dataset.page);void load()}});
 host.addEventListener('change',event=>{if(event.target.dataset.pageSize===key){event.stopPropagation();size=Number(event.target.value);page=1;void load()}});
 await load();return {value:()=>hidden.value};
}
