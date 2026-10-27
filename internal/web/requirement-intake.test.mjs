import test from 'node:test';
import assert from 'node:assert/strict';
import {mergeIntakes} from './static/requirement-intake.mjs';

test('a stale suggestion refresh cannot undo a durable create or dismiss response', () => {
 const old={id:'one',version:1,status:'suggested'};
 const created={...old,version:2,status:'created',task_id:'tsk_saved'};
 const dismissed={id:'two',version:2,status:'dismissed'};
 assert.deepEqual(mergeIntakes([old,{...dismissed,version:1,status:'suggested'}],[created,dismissed]),[created,dismissed]);
 assert.deepEqual(mergeIntakes([created],[old]),[created]);
});
