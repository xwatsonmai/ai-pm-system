import test from 'node:test';
import assert from 'node:assert/strict';
import {updateSelection,taskControlAllowed} from './static/project-access.mjs';

test('role selections survive option pagination and searches without mutating an earlier selection',()=>{
  const initial=new Set(['role-on-page-1']);
  const second=updateSelection(initial,'role-on-page-3',true);
  assert.deepEqual([...initial],['role-on-page-1']);
  assert.deepEqual([...second],['role-on-page-1','role-on-page-3']);
  assert.deepEqual([...updateSelection(second,'role-on-page-1',false)],['role-on-page-3']);
});

test('shared task details show controls belonging to the granted page',()=>{
  const versions={pages:['versions']},reviews={pages:['reviews']},tasks={pages:['tasks']};
  assert.equal(taskControlAllowed('edit-task',{},versions),false);
  assert.equal(taskControlAllowed('mark-deployed',{},versions),true);
  assert.equal(taskControlAllowed('test-pass',{},reviews),true);
  assert.equal(taskControlAllowed('test-pass',{},tasks),false);
  assert.equal(taskControlAllowed('file-testing-bug',{},reviews),true);
  assert.equal(taskControlAllowed('task-close',{parent_requirement_id:'parent'},reviews),true);
  assert.equal(taskControlAllowed('task-close',{},reviews),false);
});
