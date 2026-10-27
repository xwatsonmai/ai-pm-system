import test from 'node:test';
import assert from 'node:assert/strict';
import {answerRows, hasReviewDraft, questionIsActive, questionReplies, sameAnswerSheet, questionCardStatus} from './static/requirement-review-state.mjs';
import {relationHTML} from './static/requirement-review.mjs';

test('related requirement titles and reasons are escaped, and stale forms cannot be submitted',()=>{
  const html=relationHTML([{task_id:'task-"unsafe',title:'<img src=x onerror=alert(1)>',kind:'conflict',reason:'<script>wrong</script>'}]);
  assert(!html.includes('<img'));
  assert(!html.includes('<script>'));
  assert(html.includes('业务规则冲突'));
  assert.equal(questionIsActive({needs_recheck:true,generation:1,status:'needs_input',source:{status:'clarify'},messages:[]},{id:'m',generation:1}),false);
});

test('submitted answers belong to the original message even when later rounds reuse question IDs',()=>{
  const old={id:'first',generation:1},next={id:'second',generation:2};
  const review={generation:2,status:'needs_input',source:{status:'clarify'},messages:[
    {role:'assistant',...old},{role:'user',id:'answer-1',reply_to:'first',answers:{role:['财务','管理员'],edge:['第一行\n第二行']}},
    {role:'assistant',...next},{role:'user',id:'answer-2',reply_to:'second',answers:{role:['客服']}},
    {role:'user',id:'confirm',action:'confirm'},
  ]};
  assert.equal(questionReplies(review,'first').length,1);
  assert.deepEqual(questionReplies(review,'first')[0].answers.role,['财务','管理员']);
  assert.deepEqual(questionReplies(review,'second')[0].answers.role,['客服']);
  assert.equal(questionIsActive(review,old),false);
  assert.equal(questionIsActive(review,next),false);
  assert.equal(answerRows([{id:'edge',label:'例外'}],questionReplies(review,'first')[0].answers)[0].values[0],'第一行\n第二行');
});

test('expired forms remain read-only even when their generation is current',()=>{
  const message={id:'form',generation:1,form_expired_at:123,form_expired_reason:'Agent 已废弃旧问题'};
  const review={generation:1,status:'needs_input',source:{status:'clarify'},messages:[message]};
  assert.equal(questionIsActive(review,message),false);
  assert.equal(questionCardStatus(review,message),'expired');
  review.messages.push({role:'user',reply_to:'form',body:'理解不对'});
  assert.equal(questionCardStatus(review,message),'expired');
  review.messages.push({role:'user',reply_to:'form',question_notes:{scope:{body:'仅线上'}}});
  assert.equal(questionCardStatus(review,message),'submitted');
});

test('historical answer sheets keep skipped questions and legacy keys instead of falling back to recommendations',()=>{
  const questions=[{id:'choice',label:'范围',recommended:['全部']},{id:'note',label:'说明'}];
  assert.deepEqual(answerRows(questions,{note:['只覆盖旧订单'],legacy:['旧字段']}),[
    {id:'choice',label:'范围',values:[]},{id:'note',label:'说明',values:['只覆盖旧订单']},{id:'legacy',label:'legacy',values:['旧字段']},
  ]);
});

test('only the unanswered current round is editable; confirmation and free-form replies become history',()=>{
  const message={id:'questions',generation:4};
  const review={generation:4,status:'needs_input',source:{status:'clarify'},messages:[]};
  assert.equal(questionIsActive(review,message),true);
  for(const status of ['queued','running','failed','confirmed'])assert.equal(questionIsActive({...review,status},message),false);
  assert.equal(questionIsActive({...review,source:{status:'ready'}},message),false);
  review.messages.push({role:'user',reply_to:'questions',body:'理解不对，重新说明',answers:{}});
  assert.equal(questionIsActive(review,message),false);
});

test('unsent structured answers and uncertain saves block confirming an older proposal',()=>{
  assert.equal(hasReviewDraft({}),false);
  assert.equal(hasReviewDraft({answers:{old:{note:[],choice:[' ']}}}),false);
  for(const draft of [{body:'纠正说明'},{files:[{id:'attachment'}]},{answers:{old:{choice:['选项']}}},{pending:{request_id:'retry'}}])assert.equal(hasReviewDraft(draft),true);
});

test('server answer key ordering cannot make a saved form look like an unsent draft',()=>{
  assert.equal(sameAnswerSheet({scope:['线上'],role:['财务']},{role:['财务'],scope:['线上']}),true);
  assert.equal(sameAnswerSheet({scope:['线上']},{scope:['全部']}),false);
});

test('pool attention follows current revision and clears only when the workflow advances', async () => {
 const {reviewAttention}=await import('./static/requirement-review-state.mjs');
 const task={status:'clarify',revision:2};
 const summary={task_revision:2,status:'needs_input'};
 assert.equal(reviewAttention(task,summary).label,'待确认');
 assert.equal(reviewAttention(task,{...summary,status:'ready_to_confirm'}).detail,'确认需求稿');
 assert.equal(reviewAttention(task,{...summary,status:'running'}).attention,false);
 assert.equal(reviewAttention(task,{...summary,status:'queued'}).attention,false);
 assert.equal(reviewAttention(task,{...summary,status:'failed'}).label,'评审失败');
 assert.equal(reviewAttention(task,{...summary,task_revision:1}).attention,false);
 assert.equal(reviewAttention(task,null).attention,false);
 for(const status of ['ready','doing','pending_release','review','done','canceled']) assert.equal(reviewAttention({...task,status},summary),null);
});

test('per-question text and pictures are drafts even with no selected option',async()=>{
 const {hasQuestionNotes,sameQuestionNotes}=await import('./static/requirement-review-state.mjs');
 const note={scope:{body:'选项均不合适',files:[{id:'image1',name:'截图.png'}]}};
 assert.equal(hasReviewDraft({questionNotes:{round1:note}}),true);
 assert.equal(hasQuestionNotes({scope:{body:' ',files:[]}}),false);
 assert.equal(hasQuestionNotes({scope:{attachment_ids:['image1']}}),true);
 assert.equal(sameQuestionNotes(note,{scope:{body:'选项均不合适',attachments:[{id:'image1'}]}}),true);
 assert.equal(sameQuestionNotes(note,{scope:{body:'第二轮的说明',attachments:[{id:'image1'}]}}),false);
 const rows=answerRows([{id:'scope',label:'范围'}],{},note);
 assert.equal(rows[0].note.body,'选项均不合适');assert.deepEqual(rows[0].values,[]);
 assert.equal(answerRows([],{},note)[0].note.files[0].id,'image1');
});

test('closed, rejected and legacy canceled requirements are terminal and never need review attention',async()=>{
 const {taskActive,taskFinished,taskTerminated,reviewAttention}=await import('./static/requirement-review-state.mjs');
 for(const status of ['clarify','ready','doing','pending_release','review'])assert.equal(taskActive({status}),true);
 for(const status of ['closed','rejected','canceled']){
  const task={status,revision:1};assert.equal(taskActive(task),false);assert.equal(taskFinished(task),true);assert.equal(taskTerminated(task),true);
  assert.equal(reviewAttention(task,{task_revision:1,status:'ready_to_confirm'}),null);
 }
 assert.equal(taskFinished({status:'done'}),true);assert.equal(taskTerminated({status:'done'}),false);
});

test('development alignment accepts optional form input and leaves completion to the client',async()=>{
  const {alignmentActionState,reviewAttention,taskActive}=await import('./static/requirement-review-state.mjs');
  const question={id:'developer-form',generation:4};
  const review={mode:'development_alignment',generation:4,status:'needs_input',source:{status:'aligning'},messages:[]};
  assert(questionIsActive(review,question));
  assert.equal(alignmentActionState(review,false).action,'send');
  assert.equal(reviewAttention({status:'aligning'}).label,'待对齐');
  assert(taskActive({status:'aligning'}));
  review.status='alignment_replied';review.source.status='ready';
  assert(!questionIsActive(review,question));
  assert.equal(alignmentActionState(review,false).action,'details');
  assert.equal(alignmentActionState(review,true).action,'send');
  review.status='alignment_completed';review.source.status='doing';
  assert.equal(alignmentActionState(review,true).action,'details');
  assert.equal(alignmentActionState({mode:'',status:'confirmed'},false),null);
});

test('the chat timeline names every author and places the viewer on the right',async()=>{
  const {chatTimeline}=await import('./static/requirement-review-state.mjs');
  const review={status:'needs_input',generation:2,version:9,creator:{name:'林悦'},source:{status:'clarify',created_at:1},messages:[
    {id:'snap',role:'system',body:'需求内容已保存，以下评审以此版本为基础。',source:{revision:1},at:2},
    {id:'a1',role:'assistant',body:'先确认改价方式。',generation:1,at:3,result:{decision:'clarify',ask:{kind:'direction',topic_id:'mode',question:'一次性还是长期？'},questions:[]}},
    {id:'u1',role:'user',action:'message',actor:'林悦',actor_id:'u_lin',actor_kind:'human',body:'一次性',at:4},
    {id:'u2',role:'user',action:'message',actor:'林悦',actor_id:'u_lin',body:'特价也改',at:5},
    {id:'u3',role:'user',action:'message',actor:'周航',actor_id:'u_zhou',body:'IP 点数不在系统设置',authorization:{source:'mcp'},at:6},
    {id:'e1',role:'system',action:'task_edited',actor:'周航',body:'周航 编辑了需求（R1 → R2）',source:{revision:2},at:7},
    {id:'f1',role:'system',action:'review_failed',body:'评审没有完成：超时',at:8},
    {id:'c1',role:'user',action:'confirm',actor:'林悦',actor_id:'u_lin',body:'已确认需求稿与验收条件，需求进入待认领。',at:9},
    {id:'legacy',role:'user',actor:'老用户',body:'历史消息没有账号',at:10},
  ]};
  const items=chatTimeline(review,{viewerID:'u_zhou'});
  assert.deepEqual(items.map(i=>i.type),['event','agent','member','member','member','event','event','event','member']);
  assert.equal(items[0].text,'林悦 创建了需求');
  assert.deepEqual(items[0].button,{action:'snapshot',label:'查看原文',message:'snap'},'the original snapshot folds into the creation line');
  const [, agent, first, second, delegated, edit, failure, confirm, legacy]=items;
  assert.equal(agent.sender.kind,'platform');
  assert.equal(first.mine,false);assert.equal(second.grouped,true);assert.equal(first.grouped,false);
  assert.equal(delegated.mine,true);assert.equal(delegated.delegated,true);assert.equal(delegated.grouped,false);
  assert.equal(edit.button.action,'diff');
  assert.equal(failure.tone,'warn');assert.equal(failure.button,undefined,'retry belongs to the current failure only');
  assert.equal(confirm.text,'林悦 确认了需求稿与验收条件，需求进入待认领');
  assert.equal(legacy.mine,false);assert.equal(legacy.sender.name,'老用户');
});

test('derived states appear at the end: current failure, recheck, closure and a typing Agent',async()=>{
  const {chatTimeline}=await import('./static/requirement-review-state.mjs');
  const base={generation:1,version:3,source:{status:'clarify'},messages:[{id:'u',role:'user',body:'补充',at:1}]};
  const legacyFailure=chatTimeline({...base,status:'failed',error:'模型未配置'});
  assert.equal(legacyFailure.at(-1).text,'评审没有完成：模型未配置');assert.equal(legacyFailure.at(-1).button.label,'重试评审');
  const recorded=chatTimeline({...base,status:'failed',messages:[...base.messages,{id:'f',role:'system',action:'review_failed',body:'评审没有完成：超时',at:2}]});
  assert.equal(recorded.length,2);assert.equal(recorded.at(-1).button.action,'retry');
  assert.equal(chatTimeline({...base,status:'needs_input',needs_recheck:true,recheck_reason:'关联需求有更新'}).at(-1).button.label,'重新核对');
  assert.equal(chatTimeline({...base,status:'running'}).at(-1).text,'Agent 正在评审');
  assert.equal(chatTimeline({...base,status:'queued'}).at(-1).type,'typing');
  const closed=chatTimeline({...base,status:'closed',source:{status:'closed'},messages:[...base.messages,{id:'x',role:'system',action:'task_closed',body:'周航 关闭了需求：并入其他需求',at:2}]});
  assert.equal(closed.at(-1).button.action,'reopen');
  assert.equal(chatTimeline({...base,status:'closed',source:{status:'closed'}}).at(-1).text,'需求已结束');
});

test('direction questions stay open until anyone replies; proposals are numbered and superseded',async()=>{
  const {askIsOpen,currentTopic,proposalStates}=await import('./static/requirement-review-state.mjs');
  const ask={id:'a',role:'assistant',generation:2,result:{ask:{kind:'direction',topic_id:'mode',question:'?'},questions:[]}};
  const review={generation:2,status:'needs_input',source:{status:'clarify'},agenda:[{id:'mode',status:'open'},{id:'items',status:'open'}],messages:[ask]};
  assert.equal(askIsOpen(review,ask),true);assert.equal(currentTopic(review),'mode');
  assert.equal(askIsOpen({...review,needs_recheck:true},ask),false);
  review.messages=[ask,{id:'u',role:'user',body:'一次性'}];
  assert.equal(askIsOpen(review,ask),false);assert.equal(currentTopic(review),'mode','falls back to the first open topic');
  const p=(id,generation)=>({id,role:'assistant',generation,result:{proposal:{description:id,acceptance:['a']}}});
  const proposals={generation:3,status:'ready_to_confirm',source:{status:'clarify'},messages:[p('v1',1),{id:'c',role:'user',action:'confirm'},p('v2',2),p('v3',3)]};
  const states=proposalStates(proposals);
  assert.deepEqual(states.get('v1'),{number:1,state:'confirmed',supersededBy:2});
  assert.deepEqual(states.get('v2'),{number:2,state:'superseded',supersededBy:3});
  assert.equal(states.get('v3').state,'current');
  assert.equal(proposalStates({...proposals,status:'queued'}).get('v3').state,'draft');
  assert.equal(proposalStates({...proposals,status:'confirmed'}).get('v3').state,'confirmed');
  const edited={...proposals,task_revision:1,messages:[...proposals.messages,{id:'e',role:'system',action:'task_edited',source:{revision:2}}]};
  assert.equal(proposalStates(edited).get('v3').state,'draft','an edit after the proposal blocks confirming it');
});

test('requirement edits show added and removed lines',async()=>{
  const {lineDiff}=await import('./static/requirement-review-state.mjs');
  assert.deepEqual(lineDiff('系统配置\n币值数\n结尾','系统配置\n点数\n结尾').map(l=>l.type+':'+l.text),['same:系统配置','del:币值数','add:点数','same:结尾']);
  assert.equal(lineDiff('a\n'.repeat(600),'b\n'.repeat(600)),null);
});

test('the reviewing bubble shows what the Agent is doing and what it already finished',async()=>{
  const {reviewActivity,chatTimeline}=await import('./static/requirement-review-state.mjs');
  assert.deepEqual(reviewActivity([]),{activity:'正在阅读需求和讨论记录',done:[]});
  const steps=[{label:'查找相关需求',status:'done'},{label:'阅读「模板积分」',status:'done'},{label:'查找相关需求',status:'done'},{label:'阅读代码（List.vue）',status:'running'}];
  assert.deepEqual(reviewActivity(steps),{activity:'正在阅读代码（List.vue）',done:['查找相关需求','阅读「模板积分」']});
  assert.equal(reviewActivity([...steps.slice(0,2)]).activity,'正在梳理查到的信息');
  assert.equal(reviewActivity([{label:'整理回复',status:'failed'}]).activity,'回复不符合评审规则，正在修改');
  assert.equal(reviewActivity([{label:'整理回复',status:'done'}]).activity,'正在保存评审结果');
  const running=chatTimeline({status:'running',generation:1,source:{status:'clarify'},progress:steps,messages:[]}).at(-1);
  assert.equal(running.text,'Agent 正在评审');assert.equal(running.activity,'正在阅读代码（List.vue）');
  assert.equal(chatTimeline({status:'queued',generation:1,source:{status:'clarify'},progress:steps,messages:[]}).at(-1).activity,undefined,'queued rounds show no stale progress');
});
