import test from 'node:test';
import assert from 'node:assert/strict';
import {updateSelection} from './static/project-access.mjs';

test('role selections survive option pagination and searches without mutating an earlier selection',()=>{
  const initial=new Set(['role-on-page-1']);
  const second=updateSelection(initial,'role-on-page-3',true);
  assert.deepEqual([...initial],['role-on-page-1']);
  assert.deepEqual([...second],['role-on-page-1','role-on-page-3']);
  assert.deepEqual([...updateSelection(second,'role-on-page-1',false)],['role-on-page-3']);
});
