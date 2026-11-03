import test from 'node:test';
import assert from 'node:assert/strict';
import {mentionQuery,rebaseMentions} from './static/review-mentions.mjs';
import {reviewMessageHTML,requirementReviewHTML} from './static/requirement-review.mjs';

const mention={user_id:'mai',name:'麦青',start:2,end:5};
test('typing @ searches without implicitly selecting exact names or treating emails as mentions',()=>{
  assert.deepEqual(mentionQuery('请 @麦',4),{start:2,end:4,query:'麦'});
  assert.equal(mentionQuery('mail@example.com',16),null);
  assert.equal(mentionQuery('请 @麦青 ',5,5,[mention]),null);
  assert.equal(mentionQuery('请 @麦',2,4),null);
  assert.equal(mentionQuery('请 @麦\n',5),null);
  assert.deepEqual(mentionQuery('@123',4),{start:0,end:4,query:'123'});
  assert(!reviewMessageHTML({body:'@麦青'}).includes('rc-mention'));
});
test('editing preserves selected identities only outside the edit and rebases UTF-16 offsets',()=>{
  const before='请 @麦青 看看';
  assert.deepEqual(rebaseMentions(before,'😀'+before,[mention]),[{...mention,start:4,end:7}]);
  assert.deepEqual(rebaseMentions(before,'请 @麦红 看看',[mention]),[]);
  assert.deepEqual(rebaseMentions(before,'请 麦青 看看',[mention]),[]);
  assert.deepEqual(rebaseMentions(before,'请  看看',[mention]),[]);
  assert.deepEqual(rebaseMentions(before,before+'明天',[mention]),[mention]);
  assert.deepEqual(rebaseMentions(before,'请 @麦青  @麦青',[mention]),[mention]);
});
test('only explicit mention spans are highlighted and message/name markup stays escaped',()=>{
  const html=reviewMessageHTML({body:'请 @麦青 看看 @麦青 <script>',mentions:[mention]});
  assert.equal((html.match(/class="rc-mention"/g)||[]).length,1);
  assert(html.endsWith('看看 @麦青 &lt;script&gt;'));
  assert(!reviewMessageHTML({body:'@<img>',mentions:[{user_id:'x',name:'<img>',start:0,end:6}]}).includes('<img>'));
  assert(requirementReviewHTML({title:'评审',revision:1}).includes('输入 @ 提及项目成员'));
});
