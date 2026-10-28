// Isolated real UI + HTTP + SQLite; all releases and model replies here are fixtures.
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
const data=await mkdtemp(join(tmpdir(),'ai-pm-resolution-')),output=resolve('test-results/task-resolution-ui');await mkdir(output,{recursive:true});
const worker=join(data,'fixture.mjs');await writeFile(worker,`import readline from 'node:readline';let timer;const emit=x=>console.log(JSON.stringify(x));readline.createInterface({input:process.stdin}).on('line',line=>{const m=JSON.parse(line);if(m.type==='stop'){clearTimeout(timer);emit({type:'done',stopped:true});process.exit(0)}if(m.type==='start')timer=setTimeout(()=>emit({type:'tool_call',id:'late',name:'submit_requirement_review',args:{reply:'晚到的评审',decision:'ready',agenda:[],proposal:{description:'旧需求稿',acceptance:['旧条件']}}}),12000);if(m.type==='tool_result'){emit({type:'done'});process.exit(0)}});`);
const reserve=net.createServer().listen(0,'127.0.0.1');await once(reserve,'listening');const port=reserve.address().port;await new Promise(done=>reserve.close(done));
const base=`http://127.0.0.1:${port}`;
const server=spawn(resolve(process.env.AI_PM_QA_BIN || 'bin/ai-pm-requirements-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0','-pi-worker',worker],{stdio:['ignore','ignore','pipe']});const serverExit=once(server,'exit');server.stderr.resume();
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1050},reducedMotion:'reduce'}),errors=[];page.on('pageerror',e=>errors.push(e.message));
async function api(path,body,expected=200){const r=await fetch(base+'/api'+path,{method:body?'POST':'GET',headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const result=await r.json();assert.equal(r.status,expected,JSON.stringify(result));return result;}
let seq=0;const action=(task,operation,extra={})=>api(`/tasks/${task.id}/actions`,{action:operation,request_id:`op-${++seq}`,...extra});
// Claiming is development work, so it only happens through the developer MCP.
const developers=new Map();
async function mcpClaim(task){if(!developers.has(task.project_id))developers.set(task.project_id,(await api(`/projects/${task.project_id}/tokens`,{name:'开发客户端'})).secret);const r=await fetch(base+'/mcp',{method:'POST',headers:{Authorization:`Bearer ${developers.get(task.project_id)}`,'Content-Type':'application/json',Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2025-11-25'},body:JSON.stringify({jsonrpc:'2.0',id:++seq,method:'tools/call',params:{name:'task_claim',arguments:{task_id:task.id,request_id:`claim-${task.id}`}}})});const v=await r.json();assert(r.ok&&!v.error&&!v.result.isError,JSON.stringify(v));return JSON.parse(v.result.content[0].text);}
const stages=['clarify','ready','doing','pending_release','review'];const report={};
try{
 for(let n=0;n<100;n++){try{if((await fetch(base+'/api/health')).ok)break}catch{}await new Promise(done=>setTimeout(done,50))}
 const project=await api('/projects',{name:'需求结束分支验证',branch:'main'});
 async function make(stage,title){let {task}=await api(`/projects/${project.id}/tasks`,{title,description:'原始内容必须保留',acceptance:['验证条件'],request_id:`create-${++seq}`});if(stage==='clarify')return task;task=(await action(task,'ready')).task;if(stage==='ready')return task;task=(await mcpClaim(task)).task;if(stage==='doing')return task;task=(await action(task,'submit',{revision:task.revision,submission:{summary:'本地测试开发结果',sha:'fixture-v1',tests:'fixture passed'}})).task;task=(await action(task,'test_pass',{version:task.version,test_verification:{revision:task.revision,sha:task.submission.sha,environment:'isolated-fixture',note:'产品验证通过'}})).task;if(stage==='pending_release')return task;return (await action(task,'mark_deployed',{deployment:{revision:task.revision,sha:'fixture-v1',environment:'isolated-fixture',body:'仅模拟上线记录'}})).task;}
 async function openTask(task){await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();await page.locator('.task-table tbody tr').filter({hasText:task.title}).click();await page.locator('.detail-title').waitFor()}
 let savedForReopen;
 for(const stage of stages)for(const operation of ['close','decline']){
  const task=await make(stage,`${stage}-${operation}`),label=operation==='close'?'关闭需求':'拒绝需求',status=operation==='close'?'closed':'rejected';
  await openTask(task);assert.equal(await page.getByRole('button',{name:'关闭需求',exact:true}).count(),1);assert.equal(await page.getByRole('button',{name:'拒绝需求',exact:true}).count(),1);
  await page.getByRole('button',{name:label,exact:true}).click();const reason=`${stage}阶段业务调整\n保留原文和已完成的工作。`;await page.locator('#task-resolution-form [name=body]').fill(reason);
  if(stage==='clarify'&&operation==='close'){await page.getByRole('button',{name:'返回',exact:true}).click();await openTask(task);await page.getByRole('button',{name:label,exact:true}).click();assert.equal(await page.locator('#task-resolution-form [name=body]').inputValue(),reason)}
  if(stage==='doing'&&operation==='close'){
   let lost=false;await page.route(`**/api/tasks/${task.id}/actions`,async route=>{if(!lost){lost=true;await route.fetch();await route.abort('failed')}else await route.continue()});
   await page.locator('#task-resolution-form button[type=submit]').click();await page.locator('#task-resolution-form .form-error').filter({hasText:/fetch|网络|Failed|请求/}).waitFor();assert.equal(await page.locator('#task-resolution-form [name=body]').inputValue(),reason);await page.locator('#task-resolution-form button[type=submit]').click();
  }else await page.locator('#task-resolution-form button[type=submit]').click();
  await page.locator(`.detail-meta .badge.${status}`).waitFor();assert.match(await page.locator('.task-resolution').innerText(),/业务调整/);assert.equal(await page.getByRole('button',{name:'编辑',exact:true}).count(),0);
  const current=(await api(`/tasks/${task.id}`)).task;assert.equal(current.status,status);assert.equal(current.resolution.from_status,stage);assert.equal(current.description,task.description);if(task.submission)assert.equal(current.submission.sha,task.submission.sha);if(task.deployment)assert.equal(current.deployment.sha,task.deployment.sha);
  if(stage==='doing'&&operation==='close'){savedForReopen=current;await page.screenshot({path:join(output,'closed-development-desktop.png')});await page.setViewportSize({width:390,height:844});await page.screenshot({path:join(output,'closed-development-mobile.png')});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.setViewportSize({width:1440,height:1050})}
  await page.getByRole('button',{name:'评审记录',exact:true}).click();await page.getByText('需求已结束，评审记录只读',{exact:true}).first().waitFor();assert(await page.locator('#review-input').evaluate(el=>el.readOnly));assert(await page.locator('.review-send').isDisabled());assert.equal(await page.locator('.review-confirm').count(),0);
 }
 report.stages='五个推进阶段均有拒绝/关闭入口、原因暂存与服务器回显、开发中结束、结束后评审只读及交付证据保留通过';
 await page.getByRole('button',{name:'关闭',exact:true}).click();await page.getByRole('button',{name:'项目概览',exact:true}).click();assert.equal(await page.locator('.stat[data-filter=tasks] .stat-value').innerText(),'00');assert.equal(await page.locator('.stat[data-filter=attention] .stat-value').innerText(),'00');
 await openTask(savedForReopen);await page.getByRole('button',{name:'重新打开需求',exact:true}).click();await page.locator('#task-resolution-form [name=body]').fill('范围已调整，重新讨论');await page.locator('#task-resolution-form button[type=submit]').click();await page.getByRole('button',{name:'继续需求评审',exact:true}).waitFor();const reopened=(await api(`/tasks/${savedForReopen.id}`)).task;assert.equal(reopened.status,'clarify');assert.equal(reopened.revision,savedForReopen.revision+1);await action(reopened,'close',{body:'验证后再次关闭'});
 report.reopen='结束需求不计入推进/待办，明确重新打开后进入新修订评审通过';
 // A stale confirmation form must not close a task whose version has changed.
 const stale=await make('doing','过期关闭表单');await openTask(stale);await page.getByRole('button',{name:'关闭需求',exact:true}).click();await page.locator('#task-resolution-form [name=body]').fill('过期决定');await action(stale,'report',{body:'新增了开发进展'});await page.locator('#task-resolution-form button[type=submit]').click();await page.locator('#task-resolution-form .form-error').filter({hasText:/变化|刷新/}).waitFor();assert.equal((await api(`/tasks/${stale.id}`)).task.status,'doing');await page.getByRole('button',{name:'返回',exact:true}).click();
 report.conflict='关闭前核对最新 version，旧表单不覆盖新进展通过';
 // Closing an actively running Agent review preserves the unsent user draft.
 const profile=await api('/profiles',{name:'Fixture',provider:'fixture',model:'fixture',base_url:'http://127.0.0.1:1',api:'openai-completions'});await fetch(base+'/api/platform/settings',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({profile_id:profile.id})});
 const slow=await make('clarify','关闭正在思考的评审');await openTask(slow);await page.getByRole('button',{name:'继续需求评审',exact:true}).click();await page.getByText('Agent 正在评审',{exact:true}).waitFor();await page.locator('#review-input').fill('尚未发送的产品说明');await page.getByRole('button',{name:'更多操作'}).click();await page.getByRole('menuitem',{name:'关闭需求'}).click();await page.locator('#task-resolution-form [name=body]').fill('不再继续评审');await page.locator('#task-resolution-form button[type=submit]').click();await page.locator('.detail-meta .badge.closed').waitFor();
 await page.getByRole('button',{name:'评审记录',exact:true}).click();assert.equal(await page.locator('#review-input').inputValue(),'尚未发送的产品说明');assert(await page.locator('.review-send').isDisabled());
 await page.waitForFunction(async({projectID,taskID})=>{const runs=await(await fetch(`/api/projects/${projectID}/runs`)).json();return runs.filter(r=>r.task_id===taskID&&r.kind==='requirement_review').every(r=>!['queued','running'].includes(r.status))},{projectID:project.id,taskID:slow.id},{timeout:8000});
 const closedReview=await api(`/tasks/${slow.id}/requirement-review`);assert.equal(closedReview.status,'closed');assert.equal(closedReview.messages.filter(m=>m.role==='assistant').length,0);assert.equal((await api(`/tasks/${slow.id}`)).task.status,'closed');
 await page.screenshot({path:join(output,'closed-review-readonly.png')});report.runningReview='结束正在运行的评审、保留未发送文字、评审只读且旧结论不恢复任务通过';
 assert.deepEqual(errors,[]);await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
}catch(e){await page.screenshot({path:join(output,'failure.png'),fullPage:true}).catch(()=>{});console.error(errors);throw e}
finally{await browser.close();server.kill('SIGTERM');await serverExit;await rm(data,{recursive:true,force:true})}
