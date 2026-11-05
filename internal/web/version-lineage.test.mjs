import test from 'node:test';
import assert from 'node:assert/strict';
import {layoutLineage,lineageEdgePath,collectLineageRoots} from './static/version-lineage.mjs';

test('lineage layout nests real relationships without overlapping nodes or mutating data',()=>{
 const roots=[{id:'a',in_scope:true},{id:'b',in_scope:true},{id:'c',in_scope:true}];
 const children=new Map([['a',{items:[{id:'a1',relation:'bug',in_scope:true},{id:'a2',relation:'subrequirement',in_scope:true}],total:3}],['a2',{items:[{id:'deep',relation:'iteration',in_scope:false}],total:1}]]);
 const graph=layoutLineage(roots,children,new Set(['a','a2']));
 assert.equal(graph.nodes.length,7);assert.equal(graph.edges.length,7);assert(!('x' in roots[0]));
 for(const n of graph.nodes){assert(Number.isFinite(n.x)&&Number.isFinite(n.y));for(const m of graph.nodes){if(n===m)continue;assert(n.y!==m.y||Math.abs(n.x-m.x)>=236)}}
 assert(graph.nodes.find(n=>n.id==='deep').y>graph.nodes.find(n=>n.id==='a2').y);
 assert(graph.edges.find(e=>e.to.id==='deep').cross);assert(graph.nodes.some(n=>n.more&&n.count===1));
 for(const e of graph.edges)assert.match(lineageEdgePath(e),/^M/);
});

test('collapsed and malformed cyclic branches stay bounded',()=>{
 const roots=[{id:'a',in_scope:true}];const children=new Map([['a',{items:[{id:'a',in_scope:true}],total:1}]]);
 assert.equal(layoutLineage(roots,children,new Set()).nodes.length,1);
 assert.equal(layoutLineage(roots,children,new Set(['a'])).nodes.length,1);
});

test('42 roots wrap on one version canvas and stay readable at narrow widths',()=>{
 const roots=Array.from({length:42},(_,i)=>({id:`root-${i}`,in_scope:true}));
 for(const width of [390,1093,2166]){
  const graph=layoutLineage(roots,new Map(),new Set(),{width});
  assert.equal(graph.nodes.length,42);assert.equal(graph.edges.length,42);
  assert(graph.rows>1);assert.equal(graph.width,width);
  assert(graph.edges.every(e=>e.from.id==='version'&&e.rail));
  for(const n of graph.nodes){
   assert(n.x>=0&&n.x+236<=width);assert(n.y+104<graph.height);
   for(const m of graph.nodes)if(n!==m)assert(Math.abs(n.y-m.y)>=104||Math.abs(n.x-m.x)>=236);
  }
 }
});

test('expanded subtrees and cross-version references do not overlap later forest rows',()=>{
 const roots=Array.from({length:12},(_,i)=>({id:`r${i}`,in_scope:true}));
 const kids=new Map([['r0',{items:[{id:'sub',in_scope:true,relation:'subrequirement'},{id:'bug',in_scope:true,relation:'bug'},{id:'future',in_scope:false,relation:'iteration'}],total:3}],['sub',{items:[{id:'nested',in_scope:true,relation:'bug'}],total:1}]]);
 const graph=layoutLineage(roots,kids,new Set(['r0','sub']),{width:1093});
 assert.equal(graph.nodes.length,16);
 assert(graph.nodes.find(n=>n.id==='future').y<graph.nodes.find(n=>n.id==='r4').y);
 for(const n of graph.nodes)for(const m of graph.nodes)if(n!==m)assert(Math.abs(n.y-m.y)>=104||Math.abs(n.x-m.x)>=236);
});

const rootPage=(records,page,size=40)=>({items:records.slice((page-1)*size,page*size),page,page_size:size,total:records.length,matching_total:records.length});
test('transport batches append all 42 roots instead of splitting the version into 14 views',async()=>{
 const records=Array.from({length:42},(_,i)=>({id:`r${i}`})),batches=[],requests=[];
 const result=await collectLineageRoots(async page=>{requests.push(page);return rootPage(records,page);},{onBatch:r=>batches.push(r.items.map(n=>n.id))});
 assert.deepEqual(requests,[1,2]);assert.deepEqual(batches.map(v=>v.length),[40,42]);
 assert.deepEqual(batches[1].slice(0,40),batches[0]);assert.equal(new Set(result.items.map(n=>n.id)).size,42);assert.equal(result.has_more,false);
});

test('large versions use a bounded initial read and continuation keeps earlier roots',async()=>{
 const records=Array.from({length:241},(_,i)=>({id:`r${i}`}));
 const first=await collectLineageRoots(async page=>rootPage(records,page));
 assert.equal(first.items.length,200);assert.equal(first.has_more,true);
 const full=await collectLineageRoots(async page=>rootPage(records,page),{limit:400});
 assert.deepEqual(full.items.slice(0,200),first.items);assert.equal(full.items.length,241);assert.equal(full.has_more,false);
});

test('late batches from a different version or search are discarded',async()=>{
 const records=Array.from({length:42},(_,i)=>({id:`r${i}`}));let current=true,resolve,started;
 const pending=new Promise(r=>started=r),batches=[];
 const read=collectLineageRoots(async page=>{if(page===2){started();return new Promise(r=>resolve=r);}return rootPage(records,page);},{isCurrent:()=>current,onBatch:r=>batches.push(r.items.length)});
 await pending;current=false;resolve(rootPage(records,2));
 assert.equal(await read,null);assert.deepEqual(batches,[40]);
});

test('a concurrent removal restarts a clamped read without appending duplicate roots',async()=>{
 const records=Array.from({length:42},(_,i)=>({id:`r${i}`}));let removed=false;const requests=[];
 const result=await collectLineageRoots(async page=>{requests.push(page);if(page===2){removed=true;return rootPage(records.slice(0,40),1);}return rootPage(removed?records.slice(0,40):records,page);});
 assert.deepEqual(requests,[1,2,1]);assert.equal(result.items.length,40);assert.equal(result.has_more,false);
});

test('failed later batches keep delivered content available for retry',async()=>{
 const records=Array.from({length:42},(_,i)=>({id:`r${i}`})),batches=[];
 await assert.rejects(collectLineageRoots(async page=>{if(page===2)throw Error('offline');return rootPage(records,page);},{onBatch:r=>batches.push(r.items.length)}),/offline/);
 assert.deepEqual(batches,[40]);
});
