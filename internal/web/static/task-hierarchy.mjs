// Only expanded branches are read. A result belongs to one project/filter/root
// page; callers commit it after their own request-generation check.
export function createTaskHierarchy({storage=globalThis.localStorage}={}) {
 let scope='',generation=0,preference='',filtered=false,roots=[],children=new Map();
 let choices=new Map(),pages=new Map(),preferred=new Set(),selected=new Map();
 const finished=t=>['done','resolved','closed','rejected','canceled','review'].includes(t.status);
 function begin(key,info) {
  const next=`pm-task-expanded:${info.user}:${info.project}`;
  if(next!==preference){preference=next;try{preferred=new Set(JSON.parse(storage?.getItem(next)||'[]'));}catch{preferred=new Set();}}
  if(scope!==key){scope=key;generation++;roots=[];children=new Map();choices=new Map();pages=new Map();selected=new Map();}
  filtered=info.filtered;
 }
 const expanded=(node,depth=0)=>node.children_count>0&&(choices.has(node.id)?choices.get(node.id):preferred.has(node.id)||(filtered&&depth<8&&(depth===0||!node.matches_filter)));
 const allNodes=()=>[...roots,...[...children.values()].flatMap(value=>value.result?.items||[])];
 function choose(id,checked){
  if(!checked){selected.delete(id);return;}
  const known=new Map(allNodes().map(node=>[node.id,node])),seen=new Set();let root=id;
  while(known.get(root)?.tree_parent_id&&known.has(known.get(root).tree_parent_id)&&!seen.has(root)){seen.add(root);root=known.get(root).tree_parent_id;}
  selected.set(id,root);
 }
 function syncSelection(nodes){for(const node of nodes)if(finished(node)||node.matches_filter===false)selected.delete(node.id);}
 function toggle(id,depth){const node=allNodes().find(n=>n.id===id);if(!node)return;const open=!expanded(node,depth);choices.set(id,open);if(open)preferred.add(id);else preferred.delete(id);try{storage?.setItem(preference,JSON.stringify([...preferred].slice(-1000)));}catch{}}
 function setPage(id,page){pages.set(id,page);}
 async function read(result,fetchChildren,current=()=>true){
  const token=++generation,stage=new Map(children);let queue=result.items.map(node=>({node,depth:0}));const visited=new Set();
  while(queue.length&&current()&&token===generation){
   const batch=queue;queue=[];
   const jobs=batch.filter(({node,depth})=>{if(visited.has(node.id)||!expanded(node,depth))return false;visited.add(node.id);return true;});
   let offset=0;
   await Promise.all(Array.from({length:Math.min(4,jobs.length)},async()=>{
    while(offset<jobs.length&&current()&&token===generation){
     const {node,depth}=jobs[offset++];
     try{
      const value=await fetchChildren(node.id,pages.get(node.id)||1);
      if(!current()||token!==generation)return;
      stage.set(node.id,{result:value,error:''});
      queue.push(...value.items.map(node=>({node,depth:depth+1})));
     }catch(error){if(current()&&token===generation)stage.set(node.id,{...stage.get(node.id),error:error.message});}
    }
   }));
  }
  // Drop cached branches that left this root window or their parent's child
  // window. Folded, reachable branches remain cached for selection and detail.
  const reachable=new Map(),pending=[...result.items],seen=new Set();
  while(pending.length){const node=pending.pop();if(seen.has(node.id))continue;seen.add(node.id);const value=stage.get(node.id);if(node.children_count&&value){reachable.set(node.id,value);pending.push(...(value.result?.items||[]));}}
  return current()&&token===generation?{scope,generation:token,result,children:reachable}:null;
 }
 function commit(snapshot){if(!snapshot||snapshot.scope!==scope||snapshot.generation!==generation)return false;roots=snapshot.result.items;children=snapshot.children;const rootIDs=new Set(roots.map(node=>node.id));for(const [id,root]of selected)if(!rootIDs.has(root))selected.delete(id);syncSelection(allNodes());return true;}
 function rows(items=roots){
  const out=[],seen=new Set();
  function visit(nodes,depth){for(const node of nodes){
   if(seen.has(node.id))continue;seen.add(node.id);const open=expanded(node,depth);out.push({kind:'task',node,depth,expanded:open});
   if(!open)continue;
   const value=children.get(node.id);
   if(value?.result)visit(value.result.items,depth+1);
   if(value?.error)out.push({kind:'error',parent:node.id,depth:depth+1,text:value.error});
   else if(!value?.result)out.push({kind:'loading',parent:node.id,depth:depth+1});
   if(value?.result&&value.result.total>value.result.page_size)out.push({kind:'page',parent:node.id,depth:depth+1,result:value.result});
  }}
  visit(items,0);return out;
 }
 function data(snapshot){
  const nodes=[...snapshot.result.items],reviews=[...(snapshot.result.requirement_reviews||[])],versions=[...(snapshot.result.version_options||[])];
  for(const {result}of snapshot.children.values())if(result){nodes.push(...result.items);reviews.push(...(result.requirement_reviews||[]));versions.push(...(result.version_options||[]));}
  return {nodes:[...new Map(nodes.map(n=>[n.id,n])).values()],reviews:[...new Map(reviews.map(n=>[n.task_id,n])).values()],versions:[...new Map(versions.map(n=>[n.id,n])).values()]};
 }
 return {begin,read,commit,data,rows,toggle,setPage,choose,syncSelection,selectedIDs:()=>[...selected.keys()],isSelected:id=>selected.has(id),clearSelection:()=>selected.clear()};
}
