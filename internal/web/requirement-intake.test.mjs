import test from 'node:test';
import assert from 'node:assert/strict';
import {mergeIntakes,intakeCapabilities} from './static/requirement-intake.mjs';

test('a stale suggestion refresh cannot undo a durable create or dismiss response', () => {
 const old={id:'one',version:1,status:'suggested'};
 const created={...old,version:2,status:'created',task_id:'tsk_saved'};
 const dismissed={id:'two',version:2,status:'dismissed'};
 assert.deepEqual(mergeIntakes([old,{...dismissed,version:1,status:'suggested'}],[created,dismissed]),[created,dismissed]);
 assert.deepEqual(mergeIntakes([created],[old]),[created]);
});

test('personal suggestions use kind-specific actions without granting task-page reads',()=>{
 const access={pages:['chat'],actions:['agent.ask','bug.create']};
 assert.deepEqual(intakeCapabilities({kind:'bug'},access),{canCreate:true,canRead:false});
 assert.deepEqual(intakeCapabilities({kind:'requirement'},access),{canCreate:false,canRead:false});
 assert.deepEqual(intakeCapabilities({kind:'bug'},{...access,actions:[]}),{canCreate:false,canRead:false});
 assert.deepEqual(intakeCapabilities({kind:'bug'},{pages:['tasks'],actions:['bug.create.linked']}),{canCreate:false,canRead:true});
});
