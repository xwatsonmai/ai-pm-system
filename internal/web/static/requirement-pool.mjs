const esc=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const short=value=>String(value||'').replace(/^\w+_/,'').slice(0,6).toUpperCase();
const priorityNames={now:'现在做',next:'接下来做',later:'暂存'};
const paths={
 chevron:'<path d="m9 5 7 7-7 7"/>',
 requirement:'<rect x="5" y="3" width="14" height="18" rx="3"/><path d="M9 8h6M9 12h6M9 16h4"/>',
 bug:'<path d="M8 9h8v7a4 4 0 0 1-8 0V9ZM9 9V7a3 3 0 0 1 6 0v2M3 12h5m8 0h5M4 18l4-2m8 0 4 2M4 6l4 3m8 0 4-3"/>',
 iteration:'<path d="M7 3v10a5 5 0 0 0 5 5h8m-5-5 5 5-5 5M3 7l4-4 4 4"/>',
 folder:'<path d="M3 7V5a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"/>',
};
const icon=name=>`<svg viewBox="0 0 24 24" aria-hidden="true">${paths[name]||paths.requirement}</svg>`;

// Turn the bounded, already ordered DFS window into semantic nested lists.
// Loading/errors/child pagers stay inside their owning parent's branch.
export function nestPoolRows(rows){
 const roots=[],stack=[];
 for(const row of rows){
  while(stack.length&&stack.at(-1).depth>=row.depth)stack.pop();
  const item={...row,children:[]};
  (stack.at(-1)?.children||roots).push(item);
  if(row.kind==='task')stack.push(item);
 }
 return roots;
}

export function requirementPoolHTML({result,tasks,hierarchy,groupByVersion,versionFilter,treeRows,statusHTML,versionLabel,canViewVersion,date}){
 const roots=hierarchy?result.items:tasks;
 if(!roots.length)return '<div class="empty"><h3>没有符合条件的需求</h3><p>调整筛选，或创建一个新需求。</p></div>';
 const grouped=groupByVersion&&!versionFilter;

 function extra(item){
  const key=`${item.kind}:${item.parent}`;
  let content='<span class="pool-loading-dot" aria-hidden="true"></span>正在加载关联事项…';
  if(item.kind==='error')content=`<span>${esc(item.text)}</span><button type="button" data-action="task-tree-retry">重试</button>`;
  if(item.kind==='page'){
   const v=item.result,pages=Math.ceil(v.total/v.page_size);
   content=`<span>关联事项 ${v.total} 项 · 第 ${v.page} / ${pages} 页</span><div><button type="button" data-action="task-tree-page" data-id="${esc(item.parent)}" data-page="${v.page-1}" ${v.page<=1?'disabled':''}>上一页</button><button type="button" data-action="task-tree-page" data-id="${esc(item.parent)}" data-page="${v.page+1}" ${v.page>=pages?'disabled':''}>下一页</button></div>`;
  }
  return `<li class="pool-branch-extra" data-key="${esc(key)}"><div class="pool-branch-feedback" ${item.kind==='page'?'role="group" aria-label="子项分页"':'role="status"'}>${content}</div></li>`;
 }

 function itemHTML(item,parentVersion){
  if(item.kind!=='task')return extra(item);
  const {node:t,depth=0,expanded=false}=item,hasChildren=hierarchy&&t.children_count>0,context=hierarchy&&t.matches_filter===false;
  const relation=hierarchy&&depth>0?t.tree_relation:'';
  const kind=relation==='iteration'||(!hierarchy&&t.iteration)?'iteration':t.kind==='bug'?'bug':'requirement';
  const label=relation==='bug'?'关联 Bug':kind==='iteration'?'追加迭代':t.kind==='bug'?(t.parent_requirement_id?'需求测试 Bug':'平台 Bug'):'需求';
  const showVersion=!grouped||Boolean(depth>0&&t.project_version_id!==parentVersion);
  const meta=[`<span class="pool-kind pool-kind--${kind}">${icon(kind)}${label}</span>`,`<span class="pool-task-code">${esc(short(t.id))}</span>`,`<span>R${t.revision}</span>`];
  if(showVersion)meta.push(`<span class="pool-task-version">${esc(versionLabel(t.project_version_id))}</span>`);
  if(t.priority&&priorityNames[t.priority])meta.push(`<span class="pool-priority pool-priority--${esc(t.priority)}"><i></i>${priorityNames[t.priority]}</span>`);
  if(hasChildren)meta.push(`<span class="pool-child-count">${t.children_count} 个关联事项</span>`);
  if(hasChildren&&!expanded&&t.pending_count)meta.push(`<span class="pool-pending-count">待确认 ${t.pending_count}</span>`);
  if(context)meta.push('<span class="pool-context-label">父级上下文</span>');
  const toggle=hasChildren?`<button type="button" class="pool-toggle" data-action="task-tree-toggle" data-id="${esc(t.id)}" data-depth="${depth}" aria-expanded="${expanded}" aria-controls="pool-children-${esc(t.id)}" aria-label="${expanded?'收起':'展开'} ${esc(t.title)} 的子项">${icon('chevron')}</button>`:'<span class="pool-leaf-mark" aria-hidden="true"></span>';
  const owner=t.owner||'未认领';
  const row=`<div class="pool-row" data-task-row data-action="open-task" data-id="${esc(t.id)}" data-key="row:${esc(t.id)}">${toggle}<div class="pool-main"><button type="button" class="pool-title" data-action="open-task" data-id="${esc(t.id)}">${esc(t.title)}${t.blocker?' <span class="pool-blocked">阻塞</span>':''}</button><div class="pool-meta">${meta.join('')}</div></div><div class="pool-status">${statusHTML(t)}</div><div class="pool-owner ${t.owner?'':'is-unassigned'}" title="${esc(owner)}">${t.owner?`<span class="pool-avatar" aria-hidden="true">${esc([...owner][0])}</span>`:''}<span>${esc(owner)}</span></div><time class="pool-updated" title="${esc(date(t.updated_at))}">${esc(date(t.updated_at))}</time></div>`;
  const branch=hasChildren&&expanded?`<div class="pool-branch" data-key="branch:${esc(t.id)}"><ul class="pool-children" id="pool-children-${esc(t.id)}" aria-label="${esc(t.title)}的关联事项">${item.children.map(child=>itemHTML(child,t.project_version_id)).join('')}</ul></div>`:hasChildren?`<ul id="pool-children-${esc(t.id)}" data-key="branch:${esc(t.id)}" aria-label="${esc(t.title)}的关联事项" hidden></ul>`:'';
  return `<li class="${depth?'pool-child':'pool-family'} ${hasChildren?'pool-has-children':''} ${expanded?'is-open':''} ${context?'task-tree-context':''} ${depth>=3?'pool-deep':''}" data-key="task:${esc(t.id)}">${row}${branch}</li>`;
 }

 function list(items){
  const records=hierarchy?nestPoolRows(treeRows(items)):items.map(node=>({kind:'task',node,depth:0,children:[]}));
  return `<div class="pool-column-labels" aria-hidden="true"><span>需求与关联事项</span><span>状态</span><span>负责人</span><span>更新</span></div><ul class="pool-families" aria-label="${hierarchy?'需求层级':'需求列表'}">${records.map(item=>itemHTML(item)).join('')}</ul>`;
 }
 let content;
 if(!grouped)content=list(roots);
 else content=[...new Set(roots.map(t=>t.project_version_id||''))].map(id=>{
  const items=roots.filter(t=>(t.project_version_id||'')===id);
  return `<section class="pool-version-group" data-key="version:${esc(id)}"><div class="pool-version-heading"><h2>${icon('folder')}<span class="pool-version-name">${esc(versionLabel(id))}</span><span class="pool-version-count">本页 ${items.length} ${hierarchy?'组':'项'}</span></h2>${id&&canViewVersion?`<button type="button" class="ghost" data-action="pv-open" data-id="${esc(id)}">查看版本 <span aria-hidden="true">↗</span></button>`:''}</div>${list(items)}</section>`;
 }).join('');
 const total=hierarchy?`${result.matching_total} 项匹配 · ${result.total} 个需求组`:`${result.total} 个需求`;
 return `<div class="requirement-pool ${hierarchy?'is-hierarchy':'is-flat'}">${content}</div><div class="queue-footer">${total}</div>`;
}
