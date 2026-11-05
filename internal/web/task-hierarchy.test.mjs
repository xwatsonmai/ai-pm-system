import test from 'node:test';
import assert from 'node:assert/strict';
import {createTaskHierarchy} from './static/task-hierarchy.mjs';

const node=(id,extra={})=>({id,status:'ready',matches_filter:true,children_count:0,...extra});
const page=(items,extra={})=>({items,total:items.length,page:1,page_size:20,...extra});
const info={user:'u',project:'p',filtered:false};
const root=node('root',{children_count:22}),iteration=node('iteration',{children_count:1,matches_filter:false});

test('folds, child pages and selection survive refresh; root scope changes clear selection',async()=>{
 const saved=new Map(),storage={getItem:k=>saved.get(k),setItem:(k,v)=>saved.set(k,v)};
 const tree=createTaskHierarchy({storage});tree.begin('root-page-1',info);
 let calls=0;
 const read=async(id,p)=>{calls++;return page([node('child-'+p,{tree_parent_id:'root'})],{total:22,page:p});};
 tree.commit(await tree.read(page([root]),read));assert.equal(calls,0);assert.equal(tree.rows().length,1);
 tree.toggle('root',0);assert.equal(tree.rows()[1].kind,'loading');tree.commit(await tree.read(page([root]),read));
 assert.deepEqual(tree.rows().map(r=>r.kind),['task','task','page']);
 tree.choose('child-1',true);tree.setPage('root',2);tree.commit(await tree.read(page([root]),read));
 assert.deepEqual(tree.selectedIDs(),['child-1']);assert.equal(tree.rows()[1].node.id,'child-2');
 tree.toggle('root',0);tree.commit(await tree.read(page([root]),read));assert.equal(tree.rows().length,1);
 assert.deepEqual(tree.selectedIDs(),['child-1']);
 tree.toggle('root',0);tree.commit(await tree.read(page([root]),read));assert.equal(tree.rows()[1].node.id,'child-2');
 tree.begin('root-page-2',info);assert.deepEqual(tree.selectedIDs(),[]);
 const restored=createTaskHierarchy({storage});restored.begin('new',info);
 restored.commit(await restored.read(page([root]),read));assert.equal(restored.rows()[1].kind,'task');
});

test('filters reveal ancestor paths; explicit collapse overrides automatic expansion',async()=>{
 const tree=createTaskHierarchy({storage:null});tree.begin('filter',{...info,filtered:true});
 const reads=[];
 const fetch=async id=>{reads.push(id);return page(id==='root'?[iteration]:[node('match')]);};
 tree.commit(await tree.read(page([{...root,matches_filter:false}]),fetch));
 assert.deepEqual(reads,['root','iteration']);assert.deepEqual(tree.rows().map(r=>r.node?.id),['root','iteration','match']);
 tree.toggle('root',0);reads.length=0;tree.commit(await tree.read(page([root]),fetch));
 assert.equal(reads.length,0);assert.equal(tree.rows().length,1);
});

test('late child responses cannot cross projects, filters or a newer request',async()=>{
 const tree=createTaskHierarchy({storage:null});tree.begin('first',info);
 tree.commit(await tree.read(page([root]),async()=>page([])));tree.toggle('root',0);
 let release;
 const slow=tree.read(page([root]),()=>new Promise(done=>release=done));
 tree.begin('other',{...info,project:'q'});release(page([node('private')]));
 assert.equal(await slow,null);assert.deepEqual(tree.rows(),[]);
 tree.commit(await tree.read(page([root]),async()=>page([])));tree.toggle('root',0);
 const old=tree.read(page([root]),()=>new Promise(done=>release=done));
 const latest=await tree.read(page([root]),async()=>page([node('current')]));
 release(page([node('stale')]));assert.equal(await old,null);tree.commit(latest);
 assert.equal(tree.rows()[1].node.id,'current');
});

test('failed branches retain visible data with retry feedback; finished/context selections clear',async()=>{
 const tree=createTaskHierarchy({storage:null});tree.begin('scope',info);
 tree.commit(await tree.read(page([root]),async()=>page([])));tree.toggle('root',0);
 tree.commit(await tree.read(page([root]),async()=>page([node('child',{tree_parent_id:'root'})])));tree.choose('child',true);
 tree.commit(await tree.read(page([root]),async()=>{throw Error('network');}));
 assert.equal(tree.rows().at(-1).kind,'error');assert.equal(tree.rows()[1].node.id,'child');
 tree.commit(await tree.read(page([root]),async()=>page([node('child',{status:'resolved',tree_parent_id:'root'})])));
 assert.deepEqual(tree.selectedIDs(),[]);
 tree.choose('root',true);tree.syncSelection([{...root,matches_filter:false}]);assert.deepEqual(tree.selectedIDs(),[]);
});

test('a family leaving the root window releases its cache and hidden selections',async()=>{
 const tree=createTaskHierarchy({storage:null});tree.begin('scope',info);
 tree.commit(await tree.read(page([root]),async()=>page([])));tree.toggle('root',0);
 tree.commit(await tree.read(page([root]),async()=>page([node('child',{tree_parent_id:'root'})])));tree.choose('child',true);
 const moved=await tree.read(page([node('new-family')]),async()=>page([]));
 assert.equal(moved.children.size,0);assert.deepEqual(tree.data(moved).nodes.map(n=>n.id),['new-family']);
 tree.commit(moved);assert.deepEqual(tree.selectedIDs(),[]);
 assert.equal(tree.commit({...moved,generation:moved.generation-1}),false);
});
