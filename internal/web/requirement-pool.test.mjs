import test from 'node:test';
import assert from 'node:assert/strict';
import {nestPoolRows,requirementPoolHTML} from './static/requirement-pool.mjs';

test('nested rows keep grandchildren and child-page feedback under their real parent',()=>{
 const rows=[
  {kind:'task',node:{id:'root'},depth:0},
  {kind:'task',node:{id:'iteration'},depth:1},
  {kind:'task',node:{id:'bug'},depth:2},
  {kind:'page',parent:'iteration',depth:2},
  {kind:'task',node:{id:'other-child'},depth:1},
  {kind:'error',parent:'root',depth:1},
  {kind:'task',node:{id:'independent'},depth:0},
 ];
 const tree=nestPoolRows(rows);
 assert.deepEqual(tree.map(r=>r.node.id),['root','independent']);
 assert.deepEqual(tree[0].children.map(r=>r.node?.id||r.kind),['iteration','other-child','error']);
 assert.deepEqual(tree[0].children[0].children.map(r=>r.node?.id||r.kind),['bug','page']);
 assert.equal(tree[1].children.length,0);
});

const root={id:'root',title:'Main',status:'ready',kind:'requirement',revision:2,project_version_id:'v1',children_count:1,matches_filter:true};
const child={...root,id:'child',title:'Child',kind:'bug',parent_requirement_id:'root',tree_relation:'bug',children_count:0,project_version_id:'v2'};
const base={result:{items:[root],total:1,matching_total:2},tasks:[root,child],hierarchy:true,groupByVersion:true,versionFilter:'',treeRows:()=>[{kind:'task',node:root,depth:0,expanded:true},{kind:'task',node:child,depth:1}],statusHTML:()=>'<span>Ready</span>',versionLabel:id=>id==='v1'?'Current':'Next',canViewVersion:true,date:()=>''};

test('hierarchy gets a semantic branch with one relation label and cross-version context',()=>{
 const html=requirementPoolHTML(base);
 assert.match(html,/aria-controls="pool-children-root"/);
 assert.match(html,/<ul class="pool-children" id="pool-children-root" aria-label="Main的关联事项">/);
 assert.equal((html.match(/关联 Bug/g)||[]).length,1);
 assert.match(html,/pool-task-version">Next/);
 assert.doesNotMatch(html,/需求测试 Bug/);
});

test('flat mode removes grouping affordances but keeps the linked Bug identity',()=>{
 const html=requirementPoolHTML({...base,hierarchy:false,groupByVersion:false});
 assert.doesNotMatch(html,/class="pool-branch"|data-action="task-tree-toggle"/);
 assert.match(html,/需求测试 Bug/);
 assert.equal((html.match(/data-task-row /g)||[]).length,2);
});

test('user content is escaped and the browsing list has no selection controls',()=>{
 const bad='<img src=x onerror=alert(1)>',node={...root,title:bad,owner:bad,matches_filter:false,children_count:0};
 const html=requirementPoolHTML({...base,result:{items:[node],total:1,matching_total:1},treeRows:()=>[{kind:'task',node,depth:0}],versionLabel:()=>bad});
 assert.doesNotMatch(html,/<img/);assert.match(html,/&lt;img/);
 assert.doesNotMatch(html,/type="checkbox"|data-plan-task/);assert.match(html,/父级上下文/);
});
