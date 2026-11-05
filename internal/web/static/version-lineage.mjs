import {patchKeyedHTML,pageQuery} from './pagination.mjs';

const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const short=id=>String(id||'').replace(/^\w+_/,'').slice(0,6).toUpperCase();
const statuses={clarify:'需求评审',ready:'待认领',doing:'处理中',aligning:'待对齐',testing:'测试中',fixing:'待修复',retest:'待复测',verifying:'待验证',resolved:'已解决',pending_release:'待上线',review:'待验收',done:'已验收',closed:'已关闭',rejected:'已拒绝',canceled:'已取消'};
const versionStatuses={active:'进行中',planned:'规划中',completed:'已结束',closed:'已关闭'};
const relations={bug:'关联 Bug',iteration:'追加迭代',subrequirement:'子需求',contains:'包含需求'};
const icon=name=>`<span class="ln-icon ln-icon--${name}" aria-hidden="true"></span>`;
const control=(name,label,body,extra='')=>`<button type="button" data-ln="${name}" aria-label="${esc(label)}" title="${esc(label)}" ${extra}>${body}</button>`;
const emptyPage=()=>({items:[],page:1,page_size:6,total:0});
const W=236,H=104,GAP=28,STEP=190,ROOT_BATCH=40,ROOT_LIMIT=200,BRANCH_BATCH=4,MIN_ZOOM=.05;

// Transport pages are accumulated into one canvas. No page replaces its predecessor.
export async function collectLineageRoots(fetchPage,{limit=ROOT_LIMIT,isCurrent=()=>true,onBatch=()=>{},restarts=1}={}){
 const items=new Map();let result=null;
 for(let page=1;isCurrent();page++){
  const batch=await fetchPage(page);if(!isCurrent())return null;
  // A concurrent removal may clamp a formerly valid final page. Restart once
  // rather than append a duplicate last page or claim a stale set is complete.
  if(batch.page!==page){
   if(restarts>0)return collectLineageRoots(fetchPage,{limit,isCurrent,onBatch,restarts:restarts-1});
   throw new Error('版本范围正在变化，请重新读取');
  }
  for(const item of batch.items)items.set(item.id,item);
  const more=batch.page*batch.page_size<batch.total;
  result={...batch,items:[...items.values()],has_more:more};
  await onBatch(result);if(!isCurrent())return null;
  if(!more||page*batch.page_size>=limit||!batch.items.length)break;
 }
 return result;
}

// Each subtree owns an interval. Forest rows share one version rail, while
// references stay with their own subtree instead of drifting below the forest.
export function layoutLineage(roots,children,expanded,{width:available=1100}={}){
 const flat=[],edges=[],seen=new Set();
 function build(node,depth=0){
  if(depth>24||seen.has(node.id))return null;seen.add(node.id);
  const branch=expanded.has(node.id)?children.get(node.id):null;
  const branchItems=branch?.items||[];
  const refs=branchItems.filter(n=>!n.in_scope&&!n.path_context);
  const kids=branchItems.filter(n=>n.in_scope||n.path_context).map(n=>build(n,depth+1)).filter(Boolean);
  if(branch&&branch.total>branch.items.length)kids.push({id:`more:${node.id}`,more:true,parent:node.id,count:branch.total-branch.items.length,width:W,kids:[]});
  return {...node,kids,refs,width:Math.max(W,kids.reduce((n,k)=>n+k.width,0)+Math.max(0,kids.length-1)*GAP)};
 }
 const trees=roots.map(n=>build(n)).filter(Boolean);
 const width=Math.max(380,available,...trees.map(t=>t.width+64)),rows=[];
 for(const tree of trees){
  let row=rows.at(-1);
  if(!row||row.width+GAP+tree.width>Math.max(W,available-64)){row={trees:[],width:0};rows.push(row);}
  row.width+=tree.width+(row.trees.length?GAP:0);row.trees.push(tree);
 }
 function place(tree,left,depth,parent,top,references,rail,rowLead){
  const node={...tree,x:left+(tree.width-W)/2,y:top+depth*STEP};flat.push(node);
  for(const ref of tree.refs||[])references.push({ref,parent:node,corridor:left+tree.width+GAP/2});
  edges.push({from:parent,to:node,relation:parent.id==='version'?'contains':node.relation||'subrequirement',cross:!node.in_scope&&!node.more,rail:parent.id==='version'?rail:null,rowLead});
  let start=left+(tree.width-(tree.kids.reduce((n,k)=>n+k.width,0)+Math.max(0,tree.kids.length-1)*GAP))/2;
  for(const child of tree.kids){place(child,start,depth+1,node,top,references,null,false);start+=child.width+GAP;}
 }
 const version={id:'version',x:(width-340)/2,y:36,width:340,height:92};
 let top=roots.length>8?200:240;
 for(const row of rows){
  let left=(width-row.width)/2,bottom=top+H;
  const rail=rows.length>1?{x:12,y:top-44}:null;
  for(const [index,tree] of row.trees.entries()){
   const references=[],first=flat.length;place(tree,left,0,version,top,references,rail,index===0);
   const primary=flat.slice(first),lastY=Math.max(top,...primary.map(n=>n.y));
   for(const {ref,parent,corridor} of references){
    if(seen.has(ref.id))continue;seen.add(ref.id);
    let x=Math.max(left,Math.min(left+tree.width-W,parent.x+W+GAP)),y=Math.max(parent.y+STEP,lastY);
    while(flat.some(n=>Math.abs(n.y-y)<H+16&&Math.abs(n.x-x)<W+GAP)){
     if(x+2*W+GAP<=left+tree.width)x+=W+GAP;else{x=left;y+=STEP;}
    }
    const node={...ref,x,y};flat.push(node);edges.push({from:parent,to:node,relation:node.relation||'iteration',cross:true,corridor});
   }
   bottom=Math.max(bottom,...flat.slice(first).map(n=>n.y+H));left+=tree.width+GAP;
  }
  top=bottom+64;
 }
 return {nodes:flat,edges,version,width,height:Math.max(430,top+6),rows:rows.length};
}

export function lineageEdgePath(edge){
 const a=edge.from,b=edge.to,x=a.x+(a.id==='version'?340:W)/2,y=a.y+(a.id==='version'?92:H),tx=b.x+W/2,ty=b.y,mid=(y+ty)/2;
 if(edge.rail)return `M${x},${y}V156H${edge.rail.x}V${edge.rail.y}H${tx}V${ty}`;
 if(edge.cross&&edge.corridor){const side=edge.corridor,first=y+28,last=ty-28;return `M${x},${y}V${first}H${side}V${last}H${tx}V${ty}`;}
 if(Math.abs(tx-x)<1)return `M${x},${y}V${ty}`;
 const dir=tx>x?1:-1,r=Math.min(10,Math.abs(tx-x)/2);
 return `M${x},${y}V${mid-r}Q${x},${mid} ${x+dir*r},${mid}H${tx-dir*r}Q${tx},${mid} ${tx},${mid+r}V${ty}`;
}

export function mountVersionLineage({api,getProject,getDefaultVersion=()=>'',canReadTask=()=>false,onTask,modal}){
 let session=null,timer;
 const host=()=>document.querySelector('.lineage-page');
 const active=s=>session===s&&s.root?.isConnected&&s.project===getProject()?.id;
 const announce=(s,message)=>{if(active(s)){const box=s.root.querySelector('.ln-message'),text=box.querySelector('[data-ln-message-text]'),button=box.querySelector('button');if(box.hidden!==!message)box.hidden=!message;if(text.textContent!==message)text.textContent=message;const hidden=!message.includes('失败');if(button.hidden!==hidden)button.hidden=hidden;}};
 function page(){
  return `<section class="page lineage-page" data-project="${esc(getProject()?.id)}"><header class="ln-heading"><h1>版本谱系</h1><p>看清版本范围与需求演进</p><label class="ln-search">${icon('search')}<input type="search" maxlength="120" placeholder="搜索需求或 Bug" aria-label="搜索谱系需求" data-ln-search></label></header><div class="ln-workspace"><aside class="ln-version-index" aria-label="项目版本目录"><h2>版本</h2><div class="ln-version-list"></div><div class="ln-version-pages"></div></aside><section class="ln-stage" tabindex="0" aria-label="版本谱系画布，方向键移动，加减号缩放，数字 0 适应视图"><div class="ln-message" role="status" aria-live="polite"><span data-ln-message-text>正在加载版本…</span><button type="button" data-ln="retry" hidden>重新读取</button></div><div class="ln-scene"><svg class="ln-edges" aria-hidden="true"></svg><div class="ln-edge-labels"></div><div class="ln-version-root"></div><div class="ln-nodes"></div></div><div class="ln-load-status"></div><footer class="ln-canvas-footer"><div class="ln-legend"><span>关系图例</span><span class="ln-legend-item">包含 / 子需求</span><span class="ln-legend-item is-bug">关联 Bug</span><span class="ln-legend-item is-cross">跨版本引用 · 不计入统计</span></div><div class="ln-zoom">${control('zoom-out','缩小',icon('minus'))}<output aria-label="当前缩放">100%</output>${control('zoom-in','放大',icon('plus'))}${control('fit','适应视图',icon('arrows-maximize'))}</div><button type="button" class="ln-minimap" data-ln="map" aria-label="总览，点击定位画布"><svg aria-hidden="true"></svg></button></footer></section></div></section>`;
 }
 function pager(result,kind){
  if(result.total<=result.page_size)return '';
  return `${control(kind,'上一页',icon('chevron-left'),`data-page="${result.page-1}" ${result.page<=1?'disabled':''}`)}<span>${result.page} / ${Math.ceil(result.total/result.page_size)}</span>${control(kind,'下一页',icon('chevron-right'),`data-page="${result.page+1}" ${result.page*result.page_size>=result.total?'disabled':''}`)}`;
 }
 function renderVersions(s){
  if(!active(s))return;
  patchKeyedHTML(s.root.querySelector('.ln-version-list'),s.versions.items.map(v=>`<button type="button" class="ln-version-choice ${v.id===s.version?'is-selected':''}" data-key="${esc(v.id)}" data-ln="version" data-version="${esc(v.id)}" aria-current="${v.id===s.version?'true':'false'}"><span class="ln-version-dot" aria-hidden="true"></span><span><strong>${esc(v.name)}</strong><span class="ln-version-phase ${esc(v.status)}">${esc(versionStatuses[v.status]||v.status)}</span><small>${v.requirements} 需求 · ${v.bugs} Bug</small></span></button>`).join('')||'<p class="ln-empty-index">暂无项目版本</p>');
  patchKeyedHTML(s.root.querySelector('.ln-version-pages'),pager(s.versions,'versions-page'));
 }
 async function loadVersions(s,page=1){
  const epoch=++s.versionRequest;
  const result=await api(`/projects/${encodeURIComponent(s.project)}/lineage/versions?${pageQuery(page,12)}`);
  if(!active(s)||epoch!==s.versionRequest)return false;
  s.versions=result;renderVersions(s);return true;
 }
 function rejected(s,e){
  if(!active(s)||![401,403].includes(e.status))return false;
  s.graphRequest++;s.versionRequest++;
  s.graph=null;s.layout=null;s.signature='';s.graphContext='';s.rootLoading=false;s.children.clear();s.versions=emptyPage();
  for(const selector of ['.ln-version-list','.ln-version-pages','.ln-nodes','.ln-version-root','.ln-edge-labels','.ln-load-status'])patchKeyedHTML(s.root.querySelector(selector),'');
  s.root.querySelector('.ln-edges').innerHTML='';s.root.querySelector('.ln-scene').hidden=true;s.root.querySelector('.ln-minimap').hidden=true;
  announce(s,'访问权限已变化，当前无法查看版本谱系。');return true;
 }
 const fetchBranch=(s,parent,page=1)=>{const query=pageQuery(page,parent?BRANCH_BATCH:ROOT_BATCH);query.set('version_id',s.version);if(s.query)query.set('q',s.query);if(parent)query.set('parent_id',parent);return api(`/projects/${encodeURIComponent(s.project)}/lineage?${query}`);};
 function renderLoadStatus(s){
  const graph=s.graph;
  const text=s.rootLoading?'正在载入版本关系…':graph?.has_more?`已显示 ${graph.items.length} 个顶层事项`:(s.query&&graph?`找到 ${graph.matching_total} 个事项`:'');
  patchKeyedHTML(s.root.querySelector('.ln-load-status'),text?`<span data-key="status" role="status">${esc(text)}</span>${graph?.has_more?control('more-roots','继续加载事项','继续加载',`data-key="more" ${s.rootLoading?'disabled':''}`):''}`:'');
 }
 async function readGraph(s,{fit=false,anchor,progress=false}={}){
  const epoch=++s.graphRequest,version=s.version,query=s.query;
  if(!version){announce(s,'暂无版本。请先在项目版本中创建版本。');return;}
  const valid=()=>active(s)&&epoch===s.graphRequest&&version===s.version&&query===s.query;
  const context=JSON.stringify([version,query]);if(fit)s.viewportTouched=false;
  const sameContext=s.graphContext===context;
  if(!sameContext){s.viewportTouched=false;s.rootLimit=ROOT_LIMIT;s.initialized=false;s.pages.clear();s.expanded.clear();s.collapsed.clear();}
  const retained=sameContext?s.graph?.items||[]:[],oldChildren=sameContext?s.children:new Map();
  if(!sameContext||progress){s.rootLoading=true;renderLoadStatus(s);}
  const publish=(root,children)=>{
   if(!valid())return;
   s.graph=root;s.graphContext=context;s.children=children;
   const sig=JSON.stringify([root,[...children],[...s.expanded]]);
   if(sig!==s.signature){s.signature=sig;renderGraph(s,{anchor});}
   renderLoadStatus(s);
  };
  try{
   const root=await collectLineageRoots(page=>fetchBranch(s,'',page),{limit:s.rootLimit,isCurrent:valid,onBatch:batch=>{
    // During a refresh keep previously displayed roots until the complete new
    // set arrives. An intermediate transport page must never erase the canvas.
    const items=new Map(batch.items.map(n=>[n.id,n]));
    for(const n of retained)if(!items.has(n.id))items.set(n.id,n);
    if(!sameContext||batch.items.length>retained.length)publish({...batch,items:[...items.values()]},oldChildren);
   }});if(!valid()||!root)return;
   const next=new Map();let budget=240;
   async function descend(nodes,depth){
    for(const n of nodes){
     if(!valid())return;
     if(n.children_count>0&&(s.expanded.has(n.id)||(!s.initialized&&root.total<=8&&depth<2&&!s.collapsed.has(n.id)))&&depth<24&&budget>0){
      s.expanded.add(n.id);const requested=s.pages.get(n.id)||1,parts=[];let result;
      for(let page=1;page<=requested&&budget>0;page++){
       result=await fetchBranch(s,n.id,page);if(!valid())return;
       // A removed last page may be clamped by the server. Never append it twice.
       if(parts.some(p=>p.page===result.page))break;parts.push(result);budget-=result.items.length;
      }
      if(result){
       const branch={...result,items:parts.flatMap(p=>p.items)};next.set(n.id,branch);s.pages.set(n.id,result.page);
       publish(root,new Map([...oldChildren,...next]));await descend(branch.items,depth+1);
      }
     }
    }
   }
   await descend(root.items,0);if(!valid())return;
   s.initialized=true;publish(root,next);
   announce(s,root.total?'':(s.query?'没有匹配的事项。试试其他关键词。':'这个版本还没有需求或 Bug。'));
   if(budget<=0)announce(s,'当前展开内容较多，请收起部分分支或搜索具体事项后继续浏览。');
  }catch(e){if(valid()&&!rejected(s,e))announce(s,`读取失败：${e.message}。${s.graph?'已保留当前画布。':''} 点击重新读取可重试。`);}
  finally{if(valid()){s.rootLoading=false;renderLoadStatus(s);}}
 }
 function nodeHTML(s,n){

  if(n.more)return `<button type="button" class="ln-more-node" data-key="${esc(n.id)}" data-ln="more" data-parent="${esc(n.parent)}">${icon('plus')}再显示 ${Math.min(4,n.count)} 项<span>还有 ${n.count} 项</span></button>`;
  const cross=!n.in_scope,label=n.kind==='bug'?'Bug':n.relation==='subrequirement'?'子需求':n.relation==='iteration'?'追加迭代':'需求';
  return `<article class="ln-node ${n.kind==='bug'?'is-bug':''} ${cross?'is-reference':''} ${s.selected===n.id?'is-selected':''}" data-key="${esc(n.id)}" data-node-id="${esc(n.id)}"><button type="button" class="ln-node-open" data-ln="node" data-id="${esc(n.id)}" title="${esc(n.title)}" aria-label="查看${esc(label)}：${esc(n.title)}"><span class="ln-type-icon">${icon(n.kind==='bug'?'bug':'file-description')}</span><span class="ln-node-copy"><strong>${esc(n.title)}</strong><small>${short(n.id)} · ${esc(label)} · ${n.historical?'历史 ':''}R${n.revision}</small></span><span class="ln-node-status ${esc(n.status)}">${esc(statuses[n.status]||n.status)}</span></button><div class="ln-node-foot"><span class="ln-owner">${icon('user')}${esc(n.owner||'未认领')}</span><span class="ln-node-foot-actions">${cross?control('jump-version',n.path_context?'查看上级所属版本':'查看关联事项所属版本',esc(n.version_name||'历史版本')+icon('external-link'),`class="ln-jump-version" data-version="${esc(n.project_version_id)}"`):''}${n.children_count?control('toggle',`${s.expanded.has(n.id)?'收起':'展开'} ${n.title} 的关联事项`,`${n.children_count} 子项 ${icon('chevron-down')}`,`class="ln-expand" data-id="${esc(n.id)}" aria-expanded="${s.expanded.has(n.id)}"`):''}</span></div></article>`;
 }
 function renderGraph(s,{anchor}={}){
  if(!active(s)||!s.graph)return;
  const stage=s.root.querySelector('.ln-stage'),previous=s.layout;
  const layout=layoutLineage(s.graph.items,s.children,s.expanded,{width:stage.clientWidth});s.availableWidth=stage.clientWidth;
  if(previous){
   const cx=(stage.clientWidth/2-s.x)/s.zoom,cy=(stage.clientHeight/2-s.y)/s.zoom;
   const candidates=[previous.version,...previous.nodes];
   const old=candidates.find(n=>n.id===anchor)||candidates.reduce((best,n)=>Math.hypot(n.x+W/2-cx,n.y+H/2-cy)<Math.hypot(best.x+W/2-cx,best.y+H/2-cy)?n:best);
   const current=old.id==='version'?layout.version:layout.nodes.find(n=>n.id===old.id);
   if(current){s.x+=(old.x-current.x)*s.zoom;s.y+=(old.y-current.y)*s.zoom;}
   // Prefer the clicked parent as anchor, but keep the forest inside the
   // horizontal viewport when it fits; otherwise its left children can vanish.
   const spare=stage.clientWidth-layout.width*s.zoom;
   if(anchor&&spare>=0)s.x=Math.max(0,Math.min(spare,s.x));
  }
  s.layout=layout;s.root.querySelector('.ln-scene').hidden=false;s.root.querySelector('.ln-minimap').hidden=!layout.nodes.length;
  const v=s.graph.project_version,total=v.requirements+v.bugs,percent=total?Math.round(v.completed/total*100):0;
  patchKeyedHTML(s.root.querySelector('.ln-version-root'),`<div class="ln-root-node" ><div class="ln-root-symbol">${icon('cube')}</div><div class="ln-root-copy"><strong>${esc(v.name)}</strong><span>${v.requirements} 需求 · ${v.bugs} Bug</span><div class="ln-progress"><progress max="${Math.max(1,total)}" value="${v.completed}" aria-label="已完成 ${v.completed} / ${total}"></progress><span>${percent}%</span></div></div><span class="ln-root-status ${esc(v.status)}">${esc(versionStatuses[v.status]||v.status)}</span></div>`);
  patchKeyedHTML(s.root.querySelector('.ln-nodes'),layout.nodes.map(n=>nodeHTML(s,n)).join(''));
  const svg=s.root.querySelector('.ln-edges');svg.setAttribute('width',layout.width);svg.setAttribute('height',layout.height);
  const markers='<defs>'+[['contains','#b1a1df'],['bug','#c98396'],['cross','#9678d7']].map(([id,color])=>`<marker id="ln-arrow-${id}" markerWidth="6" markerHeight="6" refX="6" refY="3" orient="auto"><path d="M0,0 L6,3 L0,6 Z" fill="${color}"/></marker>`).join('')+'</defs>';
  const paths=markers+layout.edges.map(e=>`<path data-key="${esc(e.to.id)}" class="ln-edge ${e.relation==='bug'?'is-bug':''} ${e.cross?'is-cross':''} ${e.to.more?'is-more':''}" marker-end="url(#ln-arrow-${e.relation==='bug'?'bug':e.cross?'cross':'contains'})" d="${lineageEdgePath(e)}"/>`).join('');
  if(svg.innerHTML!==paths)svg.innerHTML=paths;
  patchKeyedHTML(s.root.querySelector('.ln-edge-labels'),layout.edges.filter(e=>!e.to.more&&(!e.rail||e.rowLead)).map(e=>`<span class="ln-edge-label ${e.relation==='bug'?'is-bug':''} ${e.cross?'is-cross':''}" data-key="${esc(e.to.id)}">${esc(e.rail?'版本范围':e.from.id==='version'&&!e.to.in_scope?'上级上下文':relations[e.relation]||'关联事项')}</span>`).join(''));
  // Assign layout through DOM style properties; inline HTML styles are
  // intentionally disallowed by the application's existing CSP.
  const position=(el,x,y)=>{if(el.style.left!==`${x}px`)el.style.left=`${x}px`;if(el.style.top!==`${y}px`)el.style.top=`${y}px`;};
  position(s.root.querySelector('.ln-root-node'),layout.version.x,36);
  const byID=new Map(layout.nodes.map(n=>[n.id,n]));
  for(const el of s.root.querySelector('.ln-nodes').children){const n=byID.get(el.dataset.key);if(n)position(el,n.x,n.y);}
  const edgeByID=new Map(layout.edges.map(e=>[e.to.id,e]));
  for(const el of s.root.querySelector('.ln-edge-labels').children){const e=edgeByID.get(el.dataset.key);if(e)position(el,e.rail?e.rail.x+60:e.to.x+W/2,e.rail?e.rail.y:e.cross&&e.corridor?e.to.y-28:(e.from.y+(e.from.id==='version'?92:H)+e.to.y)/2);}
  renderMap(s);if(s.viewportTouched)applyViewport(s);else fitView(s,true);
 }
 function renderMap(s){
  if(!s.layout)return;
  const svg=s.root.querySelector('.ln-minimap svg'),l=s.layout;
  svg.setAttribute('viewBox',`0 0 ${l.width} ${l.height}`);
  const mini=`<rect class="ln-mini-root" x="${l.version.x}" y="36" width="340" height="92" rx="10"/>`+l.nodes.map(n=>`<rect x="${n.x}" y="${n.y}" width="${W}" height="${H}" rx="8"/>`).join('')+'<rect class="ln-mini-view" />';
  if(svg.innerHTML!==mini)svg.innerHTML=mini;
 }
 function applyViewport(s){
  if(!active(s))return;
  s.root.querySelector('.ln-scene').style.transform=`translate(${s.x}px,${s.y}px) scale(${s.zoom})`;
  s.root.querySelector('.ln-zoom output').textContent=`${Math.round(s.zoom*100)}%`;
  if(s.layout){const stage=s.root.querySelector('.ln-stage'),box=s.root.querySelector('.ln-mini-view');if(box){box.setAttribute('x',-s.x/s.zoom);box.setAttribute('y',-s.y/s.zoom);box.setAttribute('width',stage.clientWidth/s.zoom);box.setAttribute('height',stage.clientHeight/s.zoom);}}
 }
 function fitView(s,initial=false){
  if(!active(s)||!s.layout)return;const stage=s.root.querySelector('.ln-stage');
  s.zoom=Math.max(initial?.85:MIN_ZOOM,Math.min(1,(stage.clientWidth-24)/s.layout.width,(stage.clientHeight-86)/s.layout.height));
  s.x=(stage.clientWidth-s.layout.width*s.zoom)/2;s.y=8;applyViewport(s);
 }
 function zoom(s,delta,point){
  s.viewportTouched=true;
  const stage=s.root.querySelector('.ln-stage'),r=stage.getBoundingClientRect(),x=point?point.x-r.left:stage.clientWidth/2,y=point?point.y-r.top:stage.clientHeight/2,old=s.zoom;
  s.zoom=Math.max(MIN_ZOOM,Math.min(1.6,old+delta));s.x=x-(x-s.x)*s.zoom/old;s.y=y-(y-s.y)*s.zoom/old;applyViewport(s);
 }
 async function selectVersion(s,id){
  if(!id)return; clearTimeout(timer);s.root.querySelector('.ln-scene').hidden=true;s.root.querySelector('.ln-minimap').hidden=true;patchKeyedHTML(s.root.querySelector('.ln-load-status'),'');s.layout=null;s.viewportTouched=false;s.version=id;s.rootLimit=ROOT_LIMIT;s.children=new Map();s.expanded=new Set();s.collapsed=new Set();s.pages=new Map();s.initialized=false;s.signature='';s.selected='';s.graph=null;s.graphContext='';s.query='';s.root.querySelector('[data-ln-search]').value='';
  renderVersions(s);announce(s,'正在加载关系…');await readGraph(s,{fit:true});
 }
 function findNode(s,id){return s.layout?.nodes.find(n=>n.id===id);}
 async function click(s,event){
  const button=event.target.closest('[data-ln]');if(!button||button.disabled)return;
  const a=button.dataset.ln;
  try{
   if(a==='version'||a==='jump-version'){await selectVersion(s,button.dataset.version);return;}
   if(a==='versions-page'){await loadVersions(s,Number(button.dataset.page));return;}
   if(a==='more-roots'){s.viewportTouched=true;s.rootLimit+=ROOT_LIMIT;await readGraph(s,{progress:true});return;}
   if(a==='toggle'){s.viewportTouched=true;const id=button.dataset.id;if(s.expanded.has(id)){s.expanded.delete(id);s.collapsed.add(id);}else{s.expanded.add(id);s.collapsed.delete(id);}await readGraph(s,{anchor:id});return;}
   if(a==='more'){s.viewportTouched=true;const id=button.dataset.parent;s.pages.set(id,(s.pages.get(id)||1)+1);await readGraph(s,{anchor:id});return;}
   if(a==='fit'){s.viewportTouched=true;fitView(s);return;}if(a==='zoom-in'||a==='zoom-out'){zoom(s,a==='zoom-in'?.1:-.1);return;}
   if(a==='map'){s.viewportTouched=true;const svg=button.querySelector('svg'),r=svg.getBoundingClientRect(),factor=Math.min(r.width/s.layout.width,r.height/s.layout.height),ox=(r.width-s.layout.width*factor)/2,oy=(r.height-s.layout.height*factor)/2;const gx=Math.max(0,Math.min(s.layout.width,(event.clientX-r.left-ox)/factor)),gy=Math.max(0,Math.min(s.layout.height,(event.clientY-r.top-oy)/factor));s.x=s.root.querySelector('.ln-stage').clientWidth/2-gx*s.zoom;s.y=s.root.querySelector('.ln-stage').clientHeight/2-gy*s.zoom;applyViewport(s);return;}
   if(a==='retry'){await refresh();return;}
   if(a==='node'){
    const n=findNode(s,button.dataset.id);if(!n)return;s.selected=n.id;renderGraph(s);
    if(n.historical)modal('版本内历史记录',`<p class="mono small muted">${short(n.id)} · 历史 R${n.revision}</p><h2>${esc(n.title)}</h2><p>${esc(statuses[n.status]||n.status)} · ${esc(n.owner||'未认领')}</p><p class="hint">${esc(n.version_name)}保留的历史范围；${n.current_available?'当前需求已有后续版本或修订。':'当前记录已不可用，此处保留历史范围信息。'}</p>${canReadTask()&&n.current_available?`<div class="modal-actions"><button type="button" data-action="open-task" data-id="${esc(n.id)}">查看当前需求</button></div>`:''}`);
    else if(canReadTask())await onTask(n.id);
    else modal('事项概览',`<p class="mono small muted">${short(n.id)} · R${n.revision}</p><h2>${esc(n.title)}</h2><p>${esc(statuses[n.status]||n.status)} · ${esc(n.owner||'未认领')}</p><p class="hint">所属版本：${esc(n.version_name||'历史版本')}</p><p class="hint">查看正文和处理事项需要需求池、验收与交付或项目版本的页面权限。</p>`);
   }
  }catch(e){if(!rejected(s,e))announce(s,e.message);}
 }
 async function load(){
  const root=host();if(!root)return;
  if(session?.root===root)return refresh();
  const previous=session?.project===getProject().id?session:null;
  if(session){session.graphRequest++;session.resize?.disconnect();}
  const s=session={root,project:getProject().id,versions:emptyPage(),version:'',versionRequest:0,graphRequest:0,rootLimit:ROOT_LIMIT,query:'',children:new Map(),expanded:new Set(),collapsed:new Set(),pages:new Map(),x:0,y:0,zoom:1,viewportTouched:false,initialized:false,signature:'',selected:'',busy:false,rootLoading:false,graphContext:''};
  const restored=!!previous?.graph;
  if(restored){for(const key of ['versions','version','rootLimit','query','children','expanded','collapsed','pages','x','y','zoom','viewportTouched','initialized','signature','selected','graph','graphContext','layout'])s[key]=previous[key];root.querySelector('[data-ln-search]').value=s.query;renderVersions(s);renderGraph(s);renderLoadStatus(s);announce(s,'');}
  root.addEventListener('click',e=>void click(s,e));
  root.querySelector('[data-ln-search]').addEventListener('input',e=>{clearTimeout(timer);s.graphRequest++;const value=e.target.value;s.query=value.trim();timer=setTimeout(()=>{if(!active(s))return;s.query=value.trim();s.rootLimit=ROOT_LIMIT;s.initialized=false;s.pages.clear();s.expanded.clear();s.collapsed.clear();void readGraph(s,{fit:true});},280);});
  const stage=root.querySelector('.ln-stage');let drag=null;
  stage.addEventListener('focusin',e=>{const node=e.target.closest('.ln-node');if(!node)return;const r=node.getBoundingClientRect(),frame=stage.getBoundingClientRect();if(r.left<frame.left+12||r.right>frame.right-12||r.top<frame.top+12||r.bottom>frame.bottom-80){s.viewportTouched=true;s.x+=frame.left+stage.clientWidth/2-(r.left+r.width/2);s.y+=frame.top+(stage.clientHeight-80)/2-(r.top+r.height/2);applyViewport(s);}});
  stage.addEventListener('pointerdown',e=>{if(e.target.closest('button,input')||e.button!==0)return;drag={id:e.pointerId,x:e.clientX,y:e.clientY,tx:s.x,ty:s.y};stage.setPointerCapture(e.pointerId);stage.classList.add('is-dragging');});
  stage.addEventListener('pointermove',e=>{if(!drag||drag.id!==e.pointerId)return;s.viewportTouched=true;s.x=drag.tx+e.clientX-drag.x;s.y=drag.ty+e.clientY-drag.y;applyViewport(s);});
  const stop=()=>{drag=null;stage.classList.remove('is-dragging');};stage.addEventListener('pointerup',stop);stage.addEventListener('pointercancel',stop);stage.addEventListener('lostpointercapture',stop);
  stage.addEventListener('wheel',e=>{if(e.target.closest('.ln-canvas-footer'))return;s.viewportTouched=true;e.preventDefault();if(e.ctrlKey||e.metaKey)zoom(s,e.deltaY<0?.05:-.05,{x:e.clientX,y:e.clientY});else{s.x-=e.deltaX;s.y-=e.deltaY;applyViewport(s);}},{passive:false});
  stage.addEventListener('keydown',e=>{if(e.target!==stage)return;const moves={ArrowLeft:[40,0],ArrowRight:[-40,0],ArrowUp:[0,40],ArrowDown:[0,-40]};if(moves[e.key]){s.viewportTouched=true;e.preventDefault();s.x+=moves[e.key][0];s.y+=moves[e.key][1];applyViewport(s);}else if(['+','=','-','0'].includes(e.key)){s.viewportTouched=true;e.preventDefault();if(e.key==='0')fitView(s);else zoom(s,e.key==='-'?-.1:.1);}});
  s.resize=new ResizeObserver(()=>{if(!active(s)){s.resize.disconnect();return;}if(s.graph&&s.availableWidth!==stage.clientWidth)renderGraph(s);else applyViewport(s);});s.resize.observe(stage);
  try{if(await loadVersions(s,s.versions.page)){if(restored)await readGraph(s);else{const desired=getDefaultVersion();await selectVersion(s,s.versions.items.find(v=>v.id===desired)?.id||s.versions.items[0]?.id);}if(!s.version)announce(s,'暂无版本。请先在项目版本中创建版本。');}}
  catch(e){if(!rejected(s,e))announce(s,`读取版本失败：${e.message}`);}
 }
 async function refresh(){
  const s=session;if(!active(s)||s.busy)return;s.busy=true;
  try{await loadVersions(s,s.versions.page);if(active(s)){if(!s.version)await selectVersion(s,s.versions.items[0]?.id);else await readGraph(s);}}catch(e){if(!rejected(s,e))announce(s,`刷新失败：${e.message}。已保留当前画布。`);}finally{s.busy=false;}
 }
 return {page,load,refresh};
}
