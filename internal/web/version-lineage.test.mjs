import test from 'node:test';
import assert from 'node:assert/strict';
import {layoutLineage,lineageEdgePath} from './static/version-lineage.mjs';

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
