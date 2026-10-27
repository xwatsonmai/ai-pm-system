// Full browser + HTTP + SQLite flow for the group-chat review; deterministic local Pi protocol fixture.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,mkdir,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import net from 'node:net';
import {once} from 'node:events';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES?join(process.env.AI_PM_QA_NODE_MODULES,'package.json'):import.meta.url);
const {chromium}=load('playwright');
const output=resolve('test-results/requirement-review-ui');await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-review-'));
const worker=join(data,'fixture.mjs');
// The fixture follows the agenda it receives: one direction question first,
// then one small form per topic, then a proposal. It never asks two topics at once.
await writeFile(worker,`
import readline from 'node:readline';
const emit=x=>console.log(JSON.stringify(x));let timer,relation,later;
const topic=(id,title,kind)=>({id,title,kind,status:'open'});
const choice=(id,label,type,options,recommended=[],reason='')=>({id,label,type,options,recommended,reason,required:true});
const ITEMS=[choice('scope','先覆盖哪些订单？','radio',['全部订单','仅线上支付订单'],['仅线上支付订单'],'线上订单可以原路退回，先上线风险更小。'),choice('roles','哪些角色可以发起退款？','multiselect',['客服','财务','管理员'],['财务','管理员'],'涉及资金，建议只给财务和管理员。'),choice('channel','退款退到哪里？','select',['原支付渠道','账户余额'])];
const SCOPE=[choice('entry','入口名称是什么？','text',[]),choice('edge','有哪些例外情况？','textarea',[])];
const close=list=>list.map(t=>t.status==='open'?{...t,status:'resolved',conclusion:'已确认'}:t);
const ready=(reply,list)=>({reply,decision:'ready',agenda:close(list),proposal:{description:'## 订单退款\\n按订单查看可退金额，支持部分退款，保留原支付渠道。',acceptance:['退款总额不超过订单实付金额','每次操作保留审计记录']}});
readline.createInterface({input:process.stdin}).on('line',line=>{
 const m=JSON.parse(line);
 if(m.type==='stop'){clearTimeout(timer);emit({type:'done',text:'stopped'});process.exit(0);}
 if(m.type==='tool_result'&&m.id==='reference'){if(m.error){emit({type:'error',error:m.error});emit({type:'done'});process.exit(0);}emit({type:'tool_call',id:'review',name:'submit_requirement_review',args:relation});return;}
 if(m.type==='tool_result'&&m.id==='pool'){timer=setTimeout(later,15000);return;}
 if(m.type==='tool_result'){if(m.error)emit({type:'error',error:m.error});emit({type:'done',text:'评审已提交。'});process.exit(0);}
 if(m.type!=='start')return;
 const source=m.review_context,agenda=source.agenda||[],users=source.conversation.filter(x=>x.role==='user'),last=users.at(-1)||{},text=last.body||'';
 const status=id=>agenda.find(t=>t.id===id)?.status;
 if(source.requirement.description.startsWith('关联目标:')){const target=source.requirement.description.slice(5);relation={reply:'尺寸单位需要产品确认。',decision:'clarify',agenda:[topic('unit','尺寸单位','direction')],ask:{topic_id:'unit',kind:'direction',question:'显示和导出统一用哪种单位？'},relations:[{task_id:target,kind:'conflict',reason:'显示使用像素，导出要求毫米，需要确认统一规则。'}]};emit({type:'tool_call',id:'reference',name:'requirement_detail',args:{task_id:target}});return;}
 if(text.includes('触发评审失败')){emit({type:'error',error:'测试模型暂不可用'});emit({type:'done'});process.exit(0);}
 let result;
 if(text.includes('整体理解不对'))result=ready('已按你的说明重新理解：以订单为单位处理退款。原始记录和补充均已保存。',agenda);
 else if(source.requirement.title.includes('逐题'))result={reply:users.length?'收到逐题说明，请继续补充。':'请确认退款规则，选项不合适可以逐题说明。',decision:'clarify',agenda:agenda.length?agenda:[topic('items','退款规则','detail')],ask:{topic_id:'items',kind:'form',questions:ITEMS}};
 else if(!agenda.length)result={reply:'我读完了需求。整理需求稿前有 3 个话题要对齐，先问最关键的一个。',analysis:'**理解**\\n\\n'+'这是较长的核对依据。'.repeat(40),decision:'clarify',agenda:[topic('mode','退款方式','direction'),topic('items','退款规则','detail'),topic('scope','入口与例外','detail')],ask:{topic_id:'mode',kind:'direction',question:'退款是**原路退回**，还是**先退到账户余额**？',quick_replies:['原路退回','先退到账户余额','这两种有什么区别？']}};
 else if(status('mode')==='open'){
  if(/什么意思|区别/.test(text))result={reply:'我是在问钱退到哪里：原路退回是退回用户付款的银行卡或钱包；退到余额是先进入用户在平台的账户。',decision:'clarify',agenda,ask:{topic_id:'mode',kind:'direction',question:'退款原路退回，还是先退到余额？',quick_replies:['原路退回','先退到账户余额']}};
  else result={reply:'记下了退款方式。下一个话题是退款规则，3 个小问题：',decision:'clarify',agenda:agenda.map(t=>t.id==='mode'?{...t,status:'resolved',conclusion:text.slice(0,30)}:t),ask:{topic_id:'items',kind:'form',questions:ITEMS}};
 } else if(status('items')==='open'){
  const answered=new Set(users.flatMap(u=>Object.keys(u.answers||{}))),left=ITEMS.filter(q=>!answered.has(q.id));
  if(left.length)result={reply:'收到，还差 '+left.length+' 题，空着的我不会当作同意：',decision:'clarify',agenda,ask:{topic_id:'items',kind:'form',questions:left}};
  else result={reply:'退款规则已确认。最后一个话题，2 个问题：',decision:'clarify',agenda:agenda.map(t=>t.id==='items'?{...t,status:'resolved',conclusion:'线上订单，财务与管理员，原路退回'}:t),ask:{topic_id:'scope',kind:'form',questions:SCOPE}};
 } else if(status('scope')==='open')result=ready('3 个话题都确认了，需求稿整理好了，请看一下。',agenda);
 else result=ready('需求已按 R'+source.requirement.revision+' 重新核对，请再确认一次。',agenda);
 const done=()=>emit({type:'tool_call',id:'review',name:'submit_requirement_review',args:result});
 // A slow first round looks up the pool first, so its progress is visible while it runs.
 if(source.requirement.title.includes('慢速')&&!users.length){later=done;emit({type:'tool_call',id:'pool',name:'requirement_pool',args:{}});}else timer=setTimeout(done,120);
});
`);
const reserve=net.createServer().listen(0,'127.0.0.1');await once(reserve,'listening');const port=reserve.address().port;await new Promise(done=>reserve.close(done));
const base=`http://127.0.0.1:${port}`;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-requirements-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-pi-worker',worker,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});const serverExit=once(server,'exit');server.stderr.resume();
const browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:1050},reducedMotion:'reduce'});const errors=[];page.on('pageerror',e=>errors.push(e.message));
async function api(path,body,method){const r=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const result=await r.json();assert(r.ok,JSON.stringify(result));return result;}
const openReview=async(title,button='继续需求评审')=>{await page.reload();await page.getByRole('button',{name:'需求池',exact:true}).click();await page.locator('.task-table tbody tr').filter({hasText:title}).click();await page.getByRole('button',{name:button,exact:true}).click();await page.locator('.requirement-review').waitFor();};
const closeDialog=()=>page.getByRole('button',{name:'关闭',exact:true}).click();
const report={};
try{
 for(let i=0;i<100;i++){try{if((await fetch(base+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
 const profile=await api('/profiles',{name:'本地测试模型',provider:'fixture',model:'fixture',base_url:'http://127.0.0.1:1',api:'openai-completions',supports_images:true});
 await api('/platform/settings',{profile_id:profile.id},'PUT');
 const project=await api('/projects',{name:'需求评审体验验证',branch:'main'});
 await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();await page.locator('#page [data-action=new-task]').click();
 await page.locator('[name=title]').fill('订单退款范围与权限');await page.locator('.requirement-prose').fill('用户可以按订单申请退款。需求包含较长说明与原始附件，需要完整保存。');
 await page.locator('#task-form button[type=submit]').click();await page.locator('#task-form').waitFor({state:'detached'});
 assert.equal(await page.locator('dialog').count(),0);
 await page.locator('.task-table tbody tr').filter({hasText:'后台评审中'}).waitFor();
 // Completion is discovered by pool polling even though no task fields changed.
 await page.locator('.task-table .review-attention.pending').waitFor({timeout:20000});
 assert.equal(await page.locator('dialog').count(),0);
 assert.equal(await page.locator('.requirement-nav-badge .nav-count').innerText(),'1');
 await page.locator('#task-filter').selectOption('review_pending');
 assert.equal(await page.locator('.task-table tbody tr').count(),1);
 await page.locator('.task-table .review-attention.pending').click();await page.locator('.rc-ask').waitFor();
 report.background='创建后后台评审、待确认标志/导航数量/筛选、标志直达评审通过';
 const tasks=await api(`/projects/${project.id}/tasks`);const task=tasks.find(x=>x.title==='订单退款范围与权限');assert(task);const endpoint=`/tasks/${task.id}/requirement-review`;

 // One direction question, asked in plain text, with the agenda on top.
 assert.equal(await page.locator('.rc-topic').count(),3);
 assert.match(await page.locator('.rc-topic.is-current').innerText(),/退款方式/);
 assert.equal(await page.locator('.review-question-form').count(),0);
 assert.equal(await page.locator('.rc-analysis[open]').count(),0);
 assert.match(await page.locator('.rc-event').first().innerText(),/项目负责人 创建了需求/);
 await page.screenshot({path:join(output,'review-direction.png')});
 await page.locator('.rc-quick button').filter({hasText:'这两种有什么区别？'}).click();
 assert.equal(await page.locator('#review-input').inputValue(),'这两种有什么区别？');
 assert.equal((await api(endpoint)).messages.filter(m=>m.role==='user').length,0,'a quick reply only fills the input');
 await page.locator('.review-send').click();
 await page.getByText('我是在问钱退到哪里',{exact:false}).waitFor();
 assert.match(await page.locator('.rc-row.rc-mine').last().innerText(),/这两种有什么区别/);
 assert.match(await page.locator('.rc-ask').first().innerText(),/已回答/);
 await page.locator('.rc-quick button').filter({hasText:'原路退回'}).click();await page.locator('.review-send').click();
 let form=page.locator('.review-question-form');await form.waitFor();
 assert.equal(await form.locator('fieldset').count(),3);assert.equal(await form.locator('input:checked').count(),0);
 assert.equal(await page.locator('.rc-topic-resolved').count(),1);assert.match(await page.locator('.rc-topic.is-current').innerText(),/退款规则/);
 report.direction='方向问题直接问、快捷回复只填输入框、先解释再问、方向确定后才出细节表单、话题进度通过';

 // A small form, filled partly; drafts survive polling and reopening.
 await form.locator('fieldset').nth(0).getByRole('button',{name:'采用推荐'}).click();assert(await form.getByRole('radio',{name:'仅线上支付订单'}).isChecked());
 await form.locator('fieldset').nth(1).getByRole('button',{name:'采用推荐'}).click();assert(await form.getByRole('checkbox',{name:'财务'}).isChecked());
 await page.locator('.rc-composer-hint').filter({hasText:'已填的 2 项'}).waitFor();
 await page.waitForTimeout(1700);assert(await form.getByRole('checkbox',{name:'管理员'}).isChecked());
 await page.screenshot({path:join(output,'review-form.png')});
 await closeDialog();await openReview(task.title);form=page.locator('.review-question-form');await form.waitFor();
 assert(await form.getByRole('radio',{name:'仅线上支付订单'}).isChecked());
 await form.locator('.rc-form-submit').click();
 await page.waitForFunction(()=>document.querySelectorAll('.rc-submission').length===1&&document.querySelectorAll('.review-question-form fieldset').length===1);
 let v=await api(endpoint);let sheets=v.messages.filter(m=>m.role==='user'&&m.reply_to);assert.equal(sheets.length,1);assert.deepEqual(sheets[0].answers.roles,['财务','管理员']);assert.equal(sheets[0].answers.channel,undefined);
 assert.equal(await page.locator('.rc-submission .review-answer-empty').count(),1);
 assert.match(await page.locator('.rc-card-done').innerText(),/项目负责人 已提交/);
 // Server-side answers survive clearing browser drafts and reopening.
 await closeDialog();await page.evaluate(()=>{for(const key of Object.keys(localStorage))if(key.startsWith('ai-pm:review-draft:'))localStorage.removeItem(key)});
 await openReview(task.title);await page.locator('.rc-submission').waitFor();assert.match(await page.locator('.rc-submission').innerText(),/财务、管理员/);
 form=page.locator('.review-question-form');await form.getByRole('combobox').selectOption('原支付渠道');await form.locator('.rc-form-submit').click();
 await page.waitForFunction(()=>document.querySelectorAll('.review-question-form fieldset').length===2);
 form=page.locator('.review-question-form');
 await form.getByRole('textbox',{name:'入口名称是什么？'}).fill('订单详情退款');await form.getByRole('textbox',{name:'有哪些例外情况？'}).fill('已发货的订单走售后流程。');
 await page.locator('#review-input').fill('入口放在订单详情页右上角。');
 assert.match(await form.locator('.rc-card-tip').innerText(),/一并发送输入框里的文字/);
 await page.locator('.review-send').click();await page.locator('.rc-proposal[data-state=current]').waitFor();
 v=await api(endpoint);const last=v.messages.filter(m=>m.role==='user').at(-1);assert.equal(last.answers.entry[0],'订单详情退款');assert.equal(last.body,'入口放在订单详情页右上角。');
 await page.locator('.rc-topics-done').waitFor();assert.equal(await page.locator('.rc-topic-resolved').count(),3);
 report.forms='每张表单只谈一个话题、五种控件分两张表单、采用推荐、部分提交后只追问剩余题、卡片内提交、输入框连同表单一起发送、清空草稿后服务器回显通过';

 // Unsent text blocks confirming an older proposal.
 await page.locator('#review-input').fill('尚未发送的补充');assert(await page.locator('.review-confirm').isDisabled());assert.match(await page.locator('.rc-confirm-tip').innerText(),/还有没发送的内容/);
 await page.locator('#review-input').fill('');assert(!(await page.locator('.review-confirm').isDisabled()));
 await page.screenshot({path:join(output,'review-proposal.png')});
 // Another member edits the requirement: the edit appears, the old proposal stops being confirmable,
 // and the Agent rechecks on the new revision.
 const current=(await api(`/tasks/${task.id}`)).task;
 await api(`/tasks/${task.id}`,{title:current.title,kind:'requirement',description:current.description+'\n退款需要保留审计记录。',acceptance:current.acceptance,priority:current.priority,version:current.version,request_id:'edit-during-review'},'PUT');
 await page.locator('.rc-event').filter({hasText:'编辑了需求（R1 → R2）'}).waitFor();
 await page.locator('.rc-proposal[data-state=current]').filter({hasText:'需求稿 v2'}).waitFor({timeout:15000});
 assert.equal(await page.locator('.review-confirm').count(),1);
 await page.locator('.rc-event').filter({hasText:'编辑了需求'}).getByRole('button',{name:'查看变更'}).click();
 await page.locator('.review-source .rc-diff-add').filter({hasText:'退款需要保留审计记录'}).waitFor();
 await page.screenshot({path:join(output,'review-edit-diff.png')});
 await page.locator('[data-review-action=source-close]').click();
 report.edit='他人编辑实时出现在对话、旧需求稿失效、自动按新修订复核、查看变更显示新增行通过';

 // A lost confirmation response is recoverable with the same request after reopening.
 let lostConfirm=false;
 await page.route(`**/api${endpoint}`,async route=>{if(route.request().method()==='POST'&&route.request().postDataJSON()?.action==='confirm'&&!lostConfirm){lostConfirm=true;await route.fetch();await route.abort('failed')}else await route.continue()});
 await page.locator('.review-confirm').click();await page.locator('.review-send-error').filter({hasText:'输入已保留'}).waitFor();
 assert.equal((await api(`/tasks/${task.id}`)).task.status,'ready');const confirmedVersion=(await api(`/tasks/${task.id}`)).task.version;
 await closeDialog();await openReview(task.title,'评审记录');
 await page.locator('.rc-event').filter({hasText:'确认了需求稿与验收条件'}).waitFor();
 assert.equal(await page.locator('.rc-proposal[data-state=confirmed]').count(),1);
 assert.equal(await page.locator('.requirement-nav-badge .nav-count').count(),0);assert.equal((await api(`/tasks/${task.id}`)).task.version,confirmedVersion);v=await api(endpoint);assert.equal(v.messages.filter(m=>m.action==='confirm').length,1);
 report.confirmation='确认按钮在需求稿卡片里、未发送内容阻止确认、确认响应丢失后幂等、确认记录为一条带人名的事件通过';
 await closeDialog();

 // A slow previous run is canceled by new input, and cannot replace the correction.
 const slow=(await api(`/projects/${project.id}/tasks`,{title:'慢速评审纠正',description:'初始需求',request_id:'slow'})).task;
 await openReview(slow.title);
 await page.getByText('Agent 正在评审',{exact:true}).waitFor();
 await page.locator('.rc-activity').filter({hasText:'已完成：查找相关需求'}).waitFor();assert.match(await page.locator('.rc-activity').innerText(),/正在梳理查到的信息/);
 await page.screenshot({path:join(output,'review-progress.png')});
 await page.locator('#review-input').fill('整体理解不对，请按新的退款目标重新评审。');await page.locator('.review-send').click();await page.locator('.review-confirm').waitFor({timeout:7000});
 const slowReview=await api(`/tasks/${slow.id}/requirement-review`);assert.equal(slowReview.messages.filter(m=>m.role==='assistant').length,1);assert.equal(slowReview.status,'ready_to_confirm');
 report.superseded='评审中显示正在评审及实时进度（已查找相关需求）、补充先保存并取消旧轮、旧结果不覆盖新纠正通过';
 // A failed review preserves a long message and its document; both failures stay in the conversation.
 await page.locator('.review-file-input').setInputFiles({name:'补充规则.md',mimeType:'text/markdown',buffer:Buffer.from('重复退款必须有记录。')});await page.getByText('附件已上传，发送后进入评审对话',{exact:true}).waitFor();
 const long='触发评审失败。'+('产品补充内容，需要原样保留。'.repeat(500));await page.locator('#review-input').fill(long);
 let lost=false;const slowEndpoint=`/api/tasks/${slow.id}/requirement-review`;
 await page.route(`**${slowEndpoint}`,async route=>{const body=route.request().postDataJSON();if(route.request().method()==='POST'&&body?.action==='message'&&!lost){lost=true;await route.fetch();await route.abort('failed');}else await route.continue();});
 await page.locator('.review-send').click();await page.locator('.review-send-error').filter({hasText:'草稿已保留'}).waitFor();assert.equal(await page.locator('#review-input').inputValue(),long);
 await closeDialog();await openReview(slow.title);
 assert.equal(await page.locator('.review-send').innerText(),'重试保存');await page.locator('.rc-composer-hint').filter({hasText:'不会重复发送'}).waitFor();
 await page.locator('.review-send').click();await page.getByText('消息已保存',{exact:true}).waitFor();
 let failed=await api(`/tasks/${slow.id}/requirement-review`);assert.equal(failed.status,'failed');assert.equal(failed.messages.filter(m=>m.body===long).length,1);assert.equal(failed.messages.find(m=>m.body===long).attachments.length,1);
 await page.locator('.rc-event.warn').filter({hasText:'需求和已发送消息均已保存'}).waitFor();
 await page.getByRole('button',{name:'重试评审',exact:true}).click();
 await page.waitForFunction(()=>document.querySelectorAll('.rc-event.warn').length===2);
 assert.equal(await page.getByRole('button',{name:'重试评审',exact:true}).count(),1,'only the current failure offers a retry');
 await page.locator('.rc-event').filter({hasText:'项目负责人 重新发起了评审'}).waitFor();
 failed=await api(`/tasks/${slow.id}/requirement-review`);assert.equal(failed.messages.filter(m=>m.body===long).length,1);
 report.durability='模型失败、长文本和附件持久化、响应丢失后重开幂等重试、失败与重试都留在对话里且只有最新失败可重试通过';
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:join(output,'review-mobile.png')});
 assert(await page.evaluate(()=>document.querySelector('dialog').scrollWidth<=innerWidth));
 await page.locator('#review-input').fill('移动端继续补充');
 const send=await page.locator('.review-send').boundingBox();assert(send.y+send.height<=844);
 report.mobile='390px 下消息区、输入框和发送按钮可用且无横向溢出';
 await closeDialog();await page.setViewportSize({width:1440,height:1050});

 // Each choice question has an independent note; text and pictures survive failures and reopening.
 const noteTask=(await api(`/projects/${project.id}/tasks`,{title:'逐题图文说明',description:'逐题说明不覆盖原文',request_id:'question-notes'})).task;
 const openNotes=async()=>{await openReview(noteTask.title);await page.locator('.review-question-form').waitFor();};
 await openNotes();let noteForm=page.locator('.review-question-form');assert.equal(await noteForm.locator('.review-question-note-text').count(),3);
 for(const id of ['scope','roles','channel'])await noteForm.locator(`.review-question-note[data-question-id=${id}] > summary`).click();
 const notePNG=await page.locator('.rc-head').screenshot();
 const pasteNote=async(withText=false)=>{await noteForm.locator('[data-question-id=scope] textarea').evaluate((el,{bytes,withText})=>{const dt=new DataTransfer();dt.items.add(new File([new Uint8Array(bytes)],'题目截图.png',{type:'image/png'}));if(withText)dt.setData('text/plain','\n截图说明');el.dispatchEvent(new ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true}))},{bytes:[...notePNG],withText})};
 await noteForm.locator('[data-question-id=scope] textarea').fill('选项不合适，仅处理历史订单。');
 const uploadPath=`**/api/projects/${project.id}/attachments`;let uploadFailed=false;
 await page.route(uploadPath,async route=>{if(!uploadFailed){uploadFailed=true;await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'测试上传失败'})})}else await route.continue()});
 await pasteNote(true);await noteForm.locator('.review-note-status').filter({hasText:'测试上传失败'}).waitFor();
 assert.match(await noteForm.locator('[data-question-id=scope] textarea').inputValue(),/历史订单/);
 await pasteNote();await noteForm.locator('[data-question-id=scope] .review-note-image').waitFor();
 await noteForm.locator('[data-question-id=roles] textarea').fill('没有合适选项，由值班客服操作。');
 await noteForm.locator('[data-question-id=channel] .review-note-file-input').setInputFiles({name:'渠道截图.png',mimeType:'image/png',buffer:notePNG});
 await page.waitForFunction(()=>document.querySelectorAll('.review-question-form .review-note-image').length===2);
 assert.equal(await noteForm.locator('input:checked').count(),0);assert.equal(await page.locator('.review-pending-files img').count(),0);
 await page.screenshot({path:join(output,'review-question-notes-desktop.png')});
 await page.setViewportSize({width:390,height:844});await noteForm.locator('.review-question-note[data-question-id=scope]').scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'review-question-notes-mobile.png')});
 assert(await page.evaluate(()=>document.querySelector('dialog').scrollWidth<=innerWidth));await page.setViewportSize({width:1440,height:1050});
 await closeDialog();await openNotes();noteForm=page.locator('.review-question-form');
 assert.match(await noteForm.locator('[data-question-id=scope] textarea').inputValue(),/截图说明/);assert.equal(await noteForm.locator('.review-note-image').count(),2);
 const notesEndpoint=`/api/tasks/${noteTask.id}/requirement-review`;let lostNotes=false;
 await page.route(`**${notesEndpoint}`,async route=>{if(route.request().method()==='POST'&&route.request().postDataJSON()?.action==='message'&&!lostNotes){lostNotes=true;await route.fetch();await route.abort('failed')}else await route.continue()});
 await noteForm.locator('.rc-form-submit').click();await page.locator('.review-send-error').filter({hasText:'草稿已保留'}).waitFor();
 await closeDialog();await openNotes();await page.locator('.review-send').filter({hasText:'重试保存'}).click();await page.getByText('消息已保存',{exact:true}).waitFor();
 let noteReview=await api(notesEndpoint.slice(4)),noteMessage=noteReview.messages.find(m=>m.role==='user');
 assert.equal(noteReview.messages.filter(m=>m.role==='user').length,1);assert.equal(Object.keys(noteMessage.answers||{}).length,0);assert.equal(Object.keys(noteMessage.question_notes).length,3);assert.equal(noteMessage.question_notes.scope.attachments.length,1);
 await closeDialog();await page.evaluate(()=>{for(const key of Object.keys(localStorage))if(key.startsWith('ai-pm:review-draft:'))localStorage.removeItem(key)});await openNotes();noteForm=page.locator('.review-question-form');
 const savedNotes=page.locator('.rc-submission').first();assert.match(await savedNotes.innerText(),/值班客服/);assert.equal(await savedNotes.locator('.review-answer-question-note img').count(),2);assert.equal(await noteForm.locator('.review-note-image').count(),0);
 await noteForm.locator('.review-question-note[data-question-id=scope] > summary').click();
 await noteForm.locator('[data-question-id=scope] textarea').fill('第二轮只补充文字，不沿用第一轮图片。');await noteForm.locator('.rc-form-submit').click();await page.waitForFunction(()=>document.querySelectorAll('.rc-submission').length===2);
 noteReview=await api(notesEndpoint.slice(4));const noteMessages=noteReview.messages.filter(m=>m.role==='user');assert.match(noteMessages[0].question_notes.scope.body,/截图说明/);assert.equal(noteMessages[1].question_notes.scope.body,'第二轮只补充文字，不沿用第一轮图片。');assert.equal((noteMessages[1].question_notes.scope.attachments||[]).length,0);
 report.questionNotes='三类选择题逐题文字/粘贴图片/选图、无选择也可发送、上传失败不丢文字、关闭恢复、丢响应幂等重试、服务器历史回显和跨轮不串题通过';
 // Closing the requirement from the menu keeps unsent drafts and records who closed it.
 await page.locator('.review-question-form').waitFor();noteForm=page.locator('.review-question-form');
 await noteForm.locator('.review-question-note[data-question-id=scope] > summary').click();
 await noteForm.locator('[data-question-id=scope] textarea').fill('关闭前尚未发送的逐题说明');await pasteNote();await noteForm.locator('[data-question-id=scope] .review-note-image').waitFor();
 await page.getByRole('button',{name:'更多操作'}).click();await page.getByRole('menuitem',{name:'关闭需求'}).click();
 await page.locator('#task-resolution-form [name=body]').fill('阶段性结束，保留所有输入');await page.locator('#task-resolution-form button[type=submit]').click();await page.locator('.detail-meta .badge.closed').waitFor();
 await page.getByRole('button',{name:'评审记录',exact:true}).click();
 await page.locator('.rc-event').filter({hasText:'项目负责人 关闭了需求：阶段性结束，保留所有输入'}).getByRole('button',{name:'重新打开'}).waitFor();
 await page.getByText('需求已结束，评审记录只读',{exact:true}).waitFor();assert(await page.locator('#review-input').evaluate(el=>el.readOnly));
 await page.locator('.rc-card-expired').last().locator(':scope > summary').click();
 await page.locator('.review-unsent-sheet').filter({hasText:'关闭前尚未发送的逐题说明'}).waitFor();
 await page.screenshot({path:join(output,'review-closed.png')});
 report.closed='菜单关闭需求、关闭事件带人名与原因和重新打开入口、保留未发送的逐题文字和图片、输入框只读通过';
 await closeDialog();

 // Legacy escaped Markdown reads as a document in the source drawer at several sizes.
 await page.setViewportSize({width:1338,height:1010});
 const legacyDescription=String.raw`## 目标\n运营发现历史质检效果更好时，可以恢复当时的提示词和模型，重新走质检与发布。\n\n## 背景\n当前只允许从最新一轮完成质检发布，历史记录只能查看。\n\n## 业务规则\n- 恢复到模板当前编辑草稿\n- 保留历史质检记录\n- 草稿存在修改时先确认\n\n## 验收条件\n1. 选择历史轮次后可同步回模板\n2. 重新质检通过后可上架`;
 const layoutTask=(await api(`/projects/${project.id}/tasks`,{title:'历史质检提示词与模型同步回模板',description:legacyDescription,request_id:'legacy-layout'})).task;
 await openReview(layoutTask.title);await page.locator('.rc-ask').waitFor();
 assert.equal(await page.locator('.review-source-content').count(),0,'the original starts folded away');
 await page.getByRole('button',{name:'需求原文',exact:true}).click();
 assert.equal(await page.locator('.review-source-content h2').count(),4);
 assert.equal(await page.locator('.review-source-content li').count(),5);
 assert(!((await page.locator('.review-source-content').innerText()).includes('\\n')));
 assert.equal((await api(`/tasks/${layoutTask.id}`)).task.description,legacyDescription);
 for(const viewport of [{width:1338,height:1010},{width:1024,height:768},{width:390,height:844}]){
   await page.setViewportSize(viewport);
   const geometry=await page.evaluate(()=>{const box=s=>document.querySelector(s)?.getBoundingClientRect().toJSON();return {dialog:box('dialog'),source:box('.review-source'),feed:box('.review-feed'),composer:box('.review-composer'),send:box('.review-send'),scroll:document.querySelector('dialog').scrollWidth,sourceFixed:getComputedStyle(document.querySelector('.review-source')).position}});
   assert(geometry.dialog.width>=Math.min(viewport.width-34,1320),JSON.stringify(geometry));assert(geometry.dialog.height>=viewport.height-34);
   assert(geometry.scroll<=viewport.width);assert(geometry.send.bottom<=viewport.height);assert(geometry.composer.bottom<=viewport.height);
   assert(geometry.feed.height>=150,JSON.stringify(geometry));
   if(viewport.width>=1240){assert.equal(geometry.sourceFixed,'static');assert(geometry.feed.width>=600,JSON.stringify(geometry));}
   else assert.equal(geometry.sourceFixed,'absolute');
   await page.screenshot({path:join(output,`review-layout-${viewport.width}.png`)});
 }
 report.layout='1338/1024/390px 布局、原文抽屉默认收起、宽屏并排窄屏覆盖、正文层级与转义换行修复、原文未改写、无横向溢出、输入可见通过';

 // A later assessment notifies the earlier requirement without changing its saved version.
 await closeDialog();await page.setViewportSize({width:1338,height:1010});
 const beforeConflict=await api(`/tasks/${layoutTask.id}/requirement-review`);
 const related=(await api(`/projects/${project.id}/tasks`,{title:'素材尺寸导出口径',description:'关联目标:'+layoutTask.id,request_id:'cross-review'})).task;
 // Poll from the test process: an async page predicate would resolve immediately.
 for(let i=0;i<400&&(await api(`/tasks/${related.id}/requirement-review`)).status!=='needs_input';i++)await new Promise(done=>setTimeout(done,50));
 assert.equal((await api(`/tasks/${related.id}/requirement-review`)).status,'needs_input');
 await openReview(layoutTask.title);
 await page.locator('.rc-event.warn').getByRole('button',{name:'重新核对'}).waitFor();
 assert.equal(await page.locator('.rc-status').innerText(),'需要复核');
 const conflictView=await api(`/tasks/${layoutTask.id}/requirement-review`);
 assert.equal(conflictView.version,beforeConflict.version);assert(conflictView.needs_recheck);
 assert(conflictView.incoming_relations.some(r=>r.task_id===related.id&&r.kind==='conflict'));
 await page.getByRole('button',{name:'关联 1',exact:true}).click();
 await page.locator('.rc-pop').getByText('其他需求的关联判断',{exact:true}).waitFor();
 await page.screenshot({path:join(output,'related-conflict-recheck.png')});
 await page.locator('.rc-pop').getByRole('button',{name:related.title}).click();
 await page.locator('dialog').getByRole('heading',{name:related.title,exact:true}).waitFor();
 report.relatedReviews='后续冲突以复核事件出现在最新位置、关联按钮列出其他需求的判断并可跳转、原评审版本未改写通过';
 assert.equal(errors.length,0,errors.join('\n'));await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
}catch(e){await page.screenshot({path:join(output,'failure.png'),fullPage:true});console.error('Browser errors:',errors);throw e;}
finally{await browser.close();server.kill('SIGTERM');await serverExit;await rm(data,{recursive:true,force:true});}
