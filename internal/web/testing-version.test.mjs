import test from 'node:test';
import assert from 'node:assert/strict';
import {retestVersion, testingRequest} from './static/testing-version.mjs';

test('candidate replaces the previous submission without modifying historical evidence', async () => {
  const task={id:'requirement',version:3,revision:2,submission:{sha:'old'}};
  const candidate=await retestVersion(task,async path=>{
    assert.equal(path,'/tasks/requirement/retest-version');
    return {sha:'fixed',branch:'feature',version:4,revision:2};
  });
  assert.equal(candidate.sha,'fixed');
  assert.equal(candidate.version,4);
  assert.match(candidate.hint,/实际部署/);
  assert.equal(task.submission.sha,'old');
  assert.equal(task.version,3);
});

test('unavailable candidate leaves the field empty and explains the failure', async () => {
  const result=await retestVersion({id:'requirement',version:3,revision:2,submission:{sha:'old'}},async()=>{throw Error('分支不可读取')});
  assert.equal(result.sha,'');
  assert.equal(result.version,3);
  assert.match(result.hint,/分支不可读取/);
});

test('unchanged transport retries retain identity; edits submit fresh SHA and evidence', () => {
  for (const entry of [{operation:'start_retest'},{form:'start_retest'}]) {
    const form={dataset:{...entry,version:'14',revision:'2'}};
    let serial=0;const id=()=>String(++serial);
    let data={sha:'old',environment:'test',note:'first'};
    const first=testingRequest(form,data,id);
    assert.deepEqual(testingRequest(form,data,id),first);
    for (const [key,value] of [['sha','fixed'],['environment','staging'],['note','updated evidence']]) {
      const previous=testingRequest(form,data,id);
      data={...data,[key]:value};
      const next=testingRequest(form,data,id);
      assert.notEqual(next.request_id,previous.request_id);
      assert.equal(next.test_verification[key],value);
      assert.deepEqual(testingRequest(form,data,id),next);
    }
    assert.equal(testingRequest(form,data,id).test_verification.sha,'fixed');
  }
});
