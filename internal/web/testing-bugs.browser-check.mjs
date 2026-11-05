// Isolated HTTP + SQLite + Chromium, with a deterministic local review worker.
import assert from 'node:assert/strict';
import {spawn,execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,mkdir,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import net from 'node:net';
import {once} from 'node:events';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES ? join(process.env.AI_PM_QA_NODE_MODULES,'package.json') : import.meta.url);
const {chromium}=load('playwright');
const output=resolve('test-results/testing-bugs-ui');await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-testing-bugs-')),repo=join(data,'repo'),worker=join(data,'worker.mjs');
await mkdir(repo);
const git=(...args)=>execFileSync('git',args,{cwd:repo,encoding:'utf8',stdio:['ignore','pipe','pipe']}).trim();
git('init','-b','main');git('config','user.name','fixture');git('config','user.email','fixture@example.invalid');
await writeFile(join(repo,'behavior.txt'),'MAIN ONLY');git('add','behavior.txt');git('commit','-m','main');
git('checkout','-b','feature/testing');await writeFile(join(repo,'behavior.txt'),'FEATURE BUG BASELINE');git('commit','-am','feature baseline');const baseline=git('rev-parse','HEAD');
await writeFile(worker,`
import readline from 'node:readline';
const emit=x=>console.log(JSON.stringify(x));let source;
const finish=()=>{emit({type:'done',text:'评审已保存'});setTimeout(()=>process.exit(0),20)};
readline.createInterface({input:process.stdin}).on('line',line=>{
 const m=JSON.parse(line);
 if(m.type==='stop'){finish();return}
 if(m.type==='start'){
  source=m.review_context;
  if(!source?.requirement.parent_requirement_id && source?.requirement.requirement_change?.status!=='reviewing'){emit({type:'tool_call',id:'review',name:'submit_requirement_review',args:{reply:'保存原始需求',decision:'clarify',agenda:[{id:'scope',title:'需求范围',kind:'direction',status:'open'}],ask:{topic_id:'scope',kind:'direction',question:'这次要覆盖哪些场景？'}}});return}
  emit({type:'tool_call',id:'code',name:'repository_read',args:{action:'file',path:'behavior.txt',ref:'main'}});return;
 }
 if(m.type!=='tool_result')return;
 if(m.error){emit({type:'error',error:m.error});finish();return}
 if(m.id!=='code'){finish();return}
 if(!['FEATURE BUG BASELINE','FIXED FEATURE'].includes(m.data.text.trim())||m.data.sha!==(source.requirement.bug_context?.sha||source.requirement.requirement_change?.sha)){emit({type:'error',error:'review used wrong baseline'});finish();return}
 if(source.requirement.requirement_change?.status==='reviewing'){emit({type:'tool_call',id:'review',name:'submit_requirement_review',args:{reply:'本轮变更采用浏览器定位，保留原功能；无需先发布原实现，等待产品确认。',decision:'ready',agenda:[],proposal:{description:source.requirement.description+'\\n\\n本轮变更：使用浏览器定位，拒绝授权时明确提示。',acceptance:['浏览器授权后记录位置，拒绝或失败显示提示']}}});return;}
 const title=source.requirement.title,last=source.conversation.filter(v=>v.role==='user').at(-1)?.body||'';
 // Earlier topics stay on the agenda; a settled round closes them instead of dropping them silently.
 const closed=(source.agenda||[]).map(t=>({...t,status:'resolved',conclusion:t.conclusion||'产品已补充'}));
 let result={reply:'已核对开发分支 behavior.txt 的提测版本，问题存在。',decision:'ready',agenda:closed,proposal:{description:source.requirement.description,acceptance:['重复操作不再异常']}};
 if(title.includes('驳回')&&!last.includes('不同意'))result={reply:'提测代码与原始需求设计一致，建议驳回；需产品确认。',decision:'reject_bug',agenda:closed};
 if(title.includes('补充')&&!last.includes('连续两次'))result={reply:'请补充复现操作。',decision:'clarify',agenda:[{id:'steps',title:'复现操作',kind:'detail',status:'open'}],ask:{topic_id:'steps',kind:'form',questions:[{id:'steps',label:'怎样操作出现问题？',type:'textarea',options:[],recommended:[],reason:'',required:true}]}};
 emit({type:'tool_call',id:'review',name:'submit_requirement_review',args:result});
});
`);
const reserve=net.createServer().listen(0,'127.0.0.1');await once(reserve,'listening');const port=reserve.address().port;await new Promise(done=>reserve.close(done));
const base=`http://127.0.0.1:${port}`;
const server=spawn(resolve(process.env.AI_PM_QA_BIN || 'bin/ai-pm-requirements-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-pi-worker',worker,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});const serverExit=once(server,'exit');server.stderr.resume();
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1050},reducedMotion:'reduce'}),errors=[];
page.on('pageerror',e=>errors.push(e.message));
let seq=0;
async function api(path,body,method){const r=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const v=await r.json();assert(r.ok,JSON.stringify(v));return v;}
// Claiming is development work, so it only happens through the developer MCP.
let mcpSeq=0;
async function claim(token,args){const r=await fetch(base+'/mcp',{method:'POST',headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json',Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2025-11-25'},body:JSON.stringify({jsonrpc:'2.0',id:++mcpSeq,method:'tools/call',params:{name:'task_claim',arguments:args}})});const v=await r.json();assert(r.ok&&!v.error&&!v.result.isError,JSON.stringify(v));return JSON.parse(v.result.content[0].text);}
const task=id=>api(`/tasks/${id}`).then(v=>v.task);
async function waitTask(id,status){for(let n=0;n<160;n++){const t=await task(id);if(t.status===status)return t;await new Promise(r=>setTimeout(r,75));}const review=await api(`/tasks/${id}/requirement-review`);assert.fail(`task never reached ${status}; review=${review.status}: ${review.error}; source=${JSON.stringify(review.source?.bug_context)}`)}
async function open(id){await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();await page.locator(`[data-task-row][data-id="${id}"]`).click();await page.locator('.detail-title').waitFor();}
async function close(){await page.getByRole('button',{name:'关闭',exact:true}).click();}
const report={};
try{
 for(let n=0;n<100;n++){try{if((await fetch(base+'/api/health')).ok)break}catch{}await new Promise(r=>setTimeout(r,50))}
 const project=await api('/projects',{name:'需求测试 Bug 验证',repository:repo,branch:'main',agent_read_mode:'legacy'});
 for(let n=0;n<100;n++){const status=await api(`/projects/${project.id}/repository-status`);if(!status.syncing){assert.equal(status.sha,git('rev-parse','main'));break;}assert(n<99,'initial automatic sync did not finish');await new Promise(done=>setTimeout(done,50));}
 let {task:parent}=await api(`/projects/${project.id}/tasks`,{title:'订单退款需求',kind:'requirement',description:'订单退款测试',acceptance:['重复点击不得重复退款'],request_id:'parent'});
 await api(`/tasks/${parent.id}/actions`,{action:'ready',request_id:'ready'});
 const developer=(await api(`/projects/${project.id}/tokens`,{name:'开发客户端'})).secret;
 await claim(developer,{task_id:parent.id,branch:'feature/testing',request_id:'claim'});
 await api(`/tasks/${parent.id}/actions`,{action:'submit',revision:1,request_id:'submit',submission:{summary:'退款功能完成',sha:baseline,environment:'测试环境',tests:'本地检查通过'}});
 // Independent platform Bugs must also be able to fail product testing.
 let {task:platformBug}=await api(`/projects/${project.id}/tasks`,{title:'独立平台异常',kind:'bug',description:'平台功能异常',acceptance:['恢复正常'],request_id:'platform-bug'});
 await api(`/tasks/${platformBug.id}/actions`,{action:'ready',request_id:'platform-ready'});
 await claim(developer,{task_id:platformBug.id,branch:'feature/testing',request_id:'platform-claim'});
 await api(`/tasks/${platformBug.id}/actions`,{action:'submit',revision:1,request_id:'platform-submit',submission:{summary:'修复平台异常',sha:baseline,environment:'测试环境',tests:'自测通过'}});
 await open(platformBug.id);await page.getByRole('button',{name:'修复无效，退回 Bug',exact:true}).click();
 await page.locator('#testing-form [name=note]').fill('原操作仍会触发异常');await page.locator('#testing-form button[type=submit]').click();await waitTask(platformBug.id,'doing');
 assert.equal((await task(parent.id)).status,'testing');
 report.platformBug='独立平台 Bug 验证失败可退回原 Bug 修复，不影响需求状态';
 // Exercise post-release acceptance, including the to-do entry and review dialog.
 parent=await task(parent.id);
 await api(`/tasks/${parent.id}/actions`,{action:'test_pass',version:parent.version,request_id:'initial-test-pass',test_verification:{revision:parent.revision,sha:baseline,environment:'测试环境',note:'产品测试通过'}});
 parent=await task(parent.id);
 await api(`/tasks/${parent.id}/actions`,{action:'mark_deployed',version:parent.version,request_id:'initial-deploy',deployment:{revision:parent.revision,sha:baseline,environment:'生产环境',body:'已实际部署初始版本'}});
 parent=await task(parent.id);
 const todoID='td_abcdef987654';
 await page.route(base+'/api/todos/'+todoID,route=>route.fulfill({json:{todo:{id:todoID,project:project.name,title:'已上线，待验收',kind:'acceptance',subject:parent.title,created_at:Date.now(),fields:[],open:true},task:parent,recipient:true}}));
 await page.goto(base+'/todo/'+todoID);
 await page.getByRole('link',{name:'提起 Bug',exact:true}).click();
 await page.locator('.detail-title').waitFor();
 assert(await page.getByRole('button',{name:'提起 Bug',exact:true}).isVisible());
 report.acceptanceTodo='验收待办可进入原需求提起 Bug';
 const profile=await api('/profiles',{name:'本地测试模型',provider:'fixture',model:'fixture',base_url:'http://127.0.0.1:1',api:'openai-completions',supports_images:true});
 await api('/platform/settings',{profile_id:profile.id},'PUT');
 async function createBug(title,paste=false,acceptance=false){
  await open(parent.id);
  if(acceptance){
   await page.getByRole('button',{name:'开始验收',exact:true}).click();
   await page.setViewportSize({width:390,height:844});
   await page.screenshot({path:join(output,'acceptance-mobile.png')});
   assert(await page.locator('#review-form .modal-actions').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'acceptance actions overflow on mobile');
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  }
  await page.getByRole('button',{name:'提起 Bug',exact:true}).click();
  if(acceptance){await page.getByRole('heading',{name:'提起验收 Bug',exact:true}).waitFor();await page.setViewportSize({width:1440,height:1050});}
  await page.locator('#task-form [name=title]').fill(title);
  await page.locator('#requirement-editor [contenteditable]').fill('连续点击退款出现两条记录，期望只生成一条。');
  if(paste){const prose=page.locator('#requirement-editor [contenteditable]');const png=await prose.screenshot();await prose.click();await prose.evaluate((el,bytes)=>{const dt=new DataTransfer();dt.items.add(new File([new Uint8Array(bytes)],'bug.png',{type:'image/png'}));el.dispatchEvent(new ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true}));},[...png]);await page.locator('.requirement-file').waitFor();}
  await page.locator('#task-form button[type=submit]').click();await page.locator('#task-form').waitFor({state:'detached'});
  const ctx=await api(`/projects/${project.id}/context`);return ctx.tasks.find(t=>t.title===title);
 }
 let bug=await createBug('重复退款 Bug',true,true);assert(bug);bug=await waitTask(bug.id,'ready');assert.equal(bug.parent_requirement_id,parent.id);assert.equal(bug.attachments.length,1);assert.equal(bug.bug_context.sha,baseline);assert.equal((await task(parent.id)).status,'fixing');
 report.acceptance='待验收可从详情及验收弹窗提 Bug；保存后阻塞验收并进入待修复';
 report.baseline='Agent 请求 main 时服务端仍读取提测分支固定 SHA；图文及粘贴图片保存、自动待领取通过';
 const rejected=await createBug('驳回反馈');
 await open(rejected.id);await page.getByRole('button',{name:'继续需求评审',exact:true}).click();
 await page.getByRole('button',{name:'同意驳回，关闭 Bug',exact:true}).waitFor();assert.equal((await task(rejected.id)).status,'clarify');
 await page.screenshot({path:join(output,'rejection-desktop.png')});
 await page.getByRole('button',{name:'同意驳回，关闭 Bug',exact:true}).click();await waitTask(rejected.id,'closed');
 let question=await createBug('需要补充的 Bug');await open(question.id);await page.getByRole('button',{name:'继续需求评审',exact:true}).click();await page.getByText('怎样操作出现问题？',{exact:false}).first().waitFor();
 await page.locator('#review-input').fill('连续两次点击退款');await page.locator('.review-send').click();question=await waitTask(question.id,'ready');
 await api(`/tasks/${question.id}/actions`,{action:'close',request_id:'duplicate-close',body:'重复问题，已有对应 Bug 跟进'});
 // Create actual fix in the requirement branch; the review baseline stays pinned.
 await writeFile(join(repo,'behavior.txt'),'FIXED FEATURE');git('commit','-am','fix duplicate');const fixed=git('rev-parse','HEAD');await api(`/projects/${project.id}/sync`,{});
 await claim(developer,{task_id:bug.id,request_id:'fix-claim',branch:'feature/testing'});
 bug=await task(bug.id);await api(`/tasks/${bug.id}/actions`,{action:'submit',revision:bug.revision,request_id:'fix-submit',submission:{summary:'重复请求已去重',sha:fixed,environment:'测试环境',tests:'本地回归通过'}});
 assert.equal((await task(parent.id)).status,'fixing');await open(bug.id);await page.getByRole('button',{name:'确认修复有效',exact:true}).click();await page.locator('#testing-form [name=note]').fill('实际复现步骤回归通过');await page.locator('#testing-form button[type=submit]').click();await waitTask(bug.id,'resolved');assert.equal((await task(parent.id)).status,'retest');
 await open(parent.id);await page.getByRole('button',{name:'开始复测',exact:true}).click();await page.locator('#testing-form [name=sha]').fill(fixed);await page.locator('#testing-form [name=note]').fill('全部修复已合入，开始复测');await page.locator('#testing-form button[type=submit]').click();await waitTask(parent.id,'testing');
 await page.getByRole('button',{name:'确认测试通过',exact:true}).click();await page.locator('#testing-form [name=note]').fill('复测通过，全部验收条件满足');await page.locator('#testing-form button[type=submit]').click();await waitTask(parent.id,'pending_release');
 report.lifecycle='待修复 → 产品验证 Bug → 待复测 → 开始复测 → 测试通过 → 待上线通过';
 await page.getByRole('button',{name:'确认已上线',exact:true}).waitFor();
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:join(output,'parent-mobile.png')});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
 // Re-review the same unreleased requirement: direct entry can be canceled.
 await page.setViewportSize({width:1440,height:1050});await open(parent.id);
 await page.getByRole('button',{name:'变更当前需求',exact:true}).click();
 await page.locator('#requirement-change-form [name=body]').fill('本轮改用浏览器定位；先检查取消后原流程保留');
 await page.locator('#requirement-change-form button[type=submit]').click();
 await page.getByRole('button',{name:'取消本次变更',exact:true}).click();
 await waitTask(parent.id,'pending_release');
 assert.equal((await task(parent.id)).submission.sha,fixed);
 // A rejected Bug can become a product-confirmed change, without passing or publishing the old implementation.
 parent=await task(parent.id);
 await api(`/tasks/${parent.id}/actions`,{action:'rework',request_id:'change-before-release',body:'本轮尚未发布，继续测试当前实现'});
 await api(`/tasks/${parent.id}/actions`,{action:'submit',revision:parent.revision,request_id:'change-retest-fixture',submission:{summary:'原实现进入测试，尚未发布',sha:fixed,branch:'feature/testing',environment:'测试环境',tests:'自测完成'}});
 const changeBug=await createBug('驳回变更反馈',true);
 await open(changeBug.id);await page.getByRole('button',{name:'继续需求评审',exact:true}).click();
 await page.getByRole('button',{name:'同意驳回，关闭 Bug',exact:true}).waitFor();
 await page.locator('#review-input').fill('本轮改用浏览器定位，当前实现不放行，不先合入主线发布');await page.locator('.review-send').click();
 await page.getByRole('button',{name:'同意驳回，关闭 Bug',exact:true}).waitFor();
 await page.getByRole('button',{name:'转为当前需求变更',exact:true}).click();
 assert.match(await page.locator('#requirement-change-form [name=body]').inputValue(),/浏览器定位/);
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:join(output,'change-form-mobile.png')});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
 await page.locator('#requirement-change-form button[type=submit]').click();
 await page.getByRole('button',{name:'确认变更，交回开发',exact:true}).waitFor();
 assert.equal((await task(parent.id)).status,'clarify');assert.equal((await task(changeBug.id)).status,'clarify');
 const changeReview=await api(`/tasks/${parent.id}/requirement-review`);
 assert.equal(changeReview.source.requirement_change.sha,fixed);assert.equal(changeReview.source.requirement_change.branch,'feature/testing');
 await page.screenshot({path:join(output,'change-review-mobile.png')});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
 await page.setViewportSize({width:1440,height:1050});await page.screenshot({path:join(output,'change-review-desktop.png')});
 const revision=(await task(parent.id)).revision;
 await page.getByRole('button',{name:'确认变更，交回开发',exact:true}).click();
 parent=await waitTask(parent.id,'doing');assert.equal(parent.revision,revision+1);assert.equal(parent.branch,'feature/testing');assert.equal(parent.submission,undefined);assert.equal(parent.deployment,undefined);assert.equal((await task(changeBug.id)).status,'closed');assert(parent.attachments.some(file=>changeBug.attachments.some(x=>x.id===file.id)));
 await writeFile(join(repo,'behavior.txt'),'BROWSER LOCATION');git('commit','-am','apply current requirement change');const changed=git('rev-parse','HEAD');await api(`/projects/${project.id}/sync`,{});
 await api(`/tasks/${parent.id}/actions`,{action:'submit',revision:parent.revision,request_id:'change-submit',submission:{summary:'本轮改用浏览器定位',sha:changed,branch:'feature/testing',environment:'测试环境',tests:'授权和拒绝场景通过'}});
 parent=await waitTask(parent.id,'testing');assert.equal(parent.submission.sha,changed);assert.equal(parent.deployment,undefined);
 report.requirementChange='详情发起可取消；驳回反馈转原需求变更、图片与纠正保留、产品显式确认、原分支原开发继续、新修订重新提测通过，未放行或发布旧实现';
 assert.deepEqual(errors,[]);await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));
}catch(e){await page.screenshot({path:join(output,'failure.png')});throw e}
finally{await browser.close();server.kill('SIGTERM');await serverExit;await rm(data,{recursive:true,force:true})}
