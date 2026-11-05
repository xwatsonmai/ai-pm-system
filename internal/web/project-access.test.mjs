import test from 'node:test';
import assert from 'node:assert/strict';
import {runStartAllowed,reviewConfirmationAllowed,actionAllowed,creationKinds,creationLabel,updateSelection,taskControlAllowed} from './static/project-access.mjs';

test('role selections survive option pagination and searches without mutating an earlier selection',()=>{
  const initial=new Set(['role-on-page-1']);
  const second=updateSelection(initial,'role-on-page-3',true);
  assert.deepEqual([...initial],['role-on-page-1']);
  assert.deepEqual([...second],['role-on-page-1','role-on-page-3']);
  assert.deepEqual([...updateSelection(second,'role-on-page-1',false)],['role-on-page-3']);
});

test('shared task details show controls belonging to the granted page',()=>{
  const versions={pages:['versions']},reviews={pages:['reviews'],actions:['bug.create.linked']},tasks={pages:['tasks']};
  assert.equal(taskControlAllowed('edit-task',{},versions),false);
  assert.equal(taskControlAllowed('mark-deployed',{},versions),true);
  assert.equal(taskControlAllowed('test-pass',{},reviews),true);
  assert.equal(taskControlAllowed('test-pass',{},tasks),false);
  assert.equal(taskControlAllowed('file-testing-bug',{},reviews),true);
  assert.equal(taskControlAllowed('task-close',{parent_requirement_id:'parent'},reviews),true);
  assert.equal(taskControlAllowed('task-close',{},reviews),false);
});

 test('page access never substitutes for action grants, and linked Bug permission stays scoped',()=>{
  const operator={pages:['chat'],actions:['agent.ask','bug.create','collaboration.create']};
  assert.equal(actionAllowed(operator,'requirement.create'),false);assert.deepEqual(creationKinds(operator),['bug']);assert.equal(creationLabel(operator),'提起 Bug');
  assert.equal(taskControlAllowed('iterate-task',{},operator),false);assert.equal(taskControlAllowed('file-testing-bug',{},operator),false);
  const tester={pages:['reviews'],actions:['bug.create.linked']};assert.deepEqual(creationKinds(tester),[]);assert.equal(taskControlAllowed('file-testing-bug',{},tester),true);
  assert.deepEqual(creationKinds({pages:['tasks'],actions:[]}),[]);assert.equal(actionAllowed({pages:['chat']},'agent.ask'),false);
 });

test('creation action grants also require access to the parent workflow page',()=>{
 const grants=['requirement.create','bug.create'];
 assert.equal(taskControlAllowed('iterate-task',{}, {pages:['chat'],actions:grants}),false);
 assert.equal(taskControlAllowed('file-testing-bug',{}, {pages:['chat'],actions:grants}),false);
 assert.equal(taskControlAllowed('iterate-task',{}, {pages:['tasks'],actions:['requirement.create']}),true);
 assert.equal(taskControlAllowed('iterate-task',{}, {pages:['versions'],actions:['requirement.create']}),false);
 assert.equal(taskControlAllowed('file-testing-bug',{}, {pages:['reviews'],actions:['bug.create.linked']}),true);
 assert.equal(taskControlAllowed('file-testing-bug',{}, {pages:['versions'],actions:['bug.create']}),true);
 assert.equal(taskControlAllowed('file-testing-bug',{}, {pages:['tasks'],actions:[]}),false);
});

test('Agent starts require their owning page and change confirmations retain their separate grant',()=>{
 assert.equal(runStartAllowed({pages:['overview'],actions:['analysis.run']},'progress'),false);
 assert.equal(runStartAllowed({pages:['tasks'],actions:['analysis.run']},'progress'),true);
 assert.equal(runStartAllowed({pages:['tasks'],actions:['agent.ask']},'question'),false);
 assert.equal(runStartAllowed({pages:['chat'],actions:['agent.ask']},'question'),true);
 assert.equal(taskControlAllowed('request-change',{}, {pages:['tasks'],actions:[]}),false);
 assert.equal(taskControlAllowed('request-change',{}, {pages:['tasks'],actions:['requirement.create']}),true);
 assert.equal(taskControlAllowed('request-change',{parent_requirement_id:'parent'}, {pages:['reviews'],actions:['requirement.create']}),true);
 assert.equal(reviewConfirmationAllowed(false,false),true);
 assert.equal(reviewConfirmationAllowed(true,false),false);
 assert.equal(reviewConfirmationAllowed(true,true),true);
});
