// One room from review to development, delivery, questions and requirement change.
// Runs only against temporary Git/SQLite and a deterministic local worker.
import assert from 'node:assert/strict';
import {spawn,execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,mkdir,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import net from 'node:net';
import {once} from 'node:events';

const load=createRequire(join(process.env.AI_PM_QA_NODE_MODULES||resolve('node_modules'),'package.json'));
const {chromium}=load('playwright');
const output=resolve('test-results/requirement-discussion-ui');await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-discussion-')),repo=join(data,'repo'),worker=join(data,'worker.mjs');
await mkdir(repo);
const git=(...args)=>execFileSync('git',args,{cwd:repo,encoding:'utf8',stdio:['ignore','pipe','pipe']}).trim();
git('init','-b','main');git('config','user.name','fixture');git('config','user.email','fixture@example.invalid');
await writeFile(join(repo,'behavior.txt'),'MAIN ONLY');git('add','.');git('commit','-m','main');
git('checkout','-b','feature/location');await writeFile(join(repo,'behavior.txt'),'COUNTRY LEVEL BASELINE');git('commit','-am','submitted country precision');const baseline=git('rev-parse','HEAD');
// Move the branch head beyond the submitted version: the answer must still use baseline.
await writeFile(join(repo,'behavior.txt'),'UNSUBMITTED PROVINCE WORK');git('commit','-am','later development');
await writeFile(worker,`
import readline from 'node:readline';import fs from 'node:fs';
const emit=x=>console.log(JSON.stringify(x));let input,source;
const finish=()=>{emit({type:'done',text:'已保存'});setTimeout(()=>process.exit(0),20)};
const answer=()=>emit({type:'tool_call',id:'answer',name:'submit_requirement_review',args:{reply:'当前提测版本按已确认需求记录到国家级。要细化到省市，请使用「变更当前需求」继续讨论新的范围。',analysis:'读取的是固定提测版本，未使用主分支或尚未提测的后续代码。',decision:'answer',agenda:[]}});
readline.createInterface({input:process.stdin}).on('line',line=>{
 const m=JSON.parse(line);
 if(m.type==='stop'){finish();return}
 if(m.type==='start'){
  input=m;source=m.review_context;
  const room=m.directory+'/room-'+source.requirement.id;
  if(fs.existsSync(room)&&fs.readFileSync(room,'utf8')!==m.conversation_id){emit({type:'error',error:'forked requirement session'});finish();return}
  fs.writeFileSync(room,m.conversation_id);
  if(source.mode==='discussion'){
   if(!['confirm','alignment_reply','task_submit'].every(action=>source.conversation.some(x=>x.action===action))||!m.prompt.includes('日常答疑')){emit({type:'error',error:'missing continuous review/delivery context'});finish();return}
   emit({type:'tool_call',id:'unsafe',name:'create_bug',args:{title:'should never be created'}});return;
  }
  const changing=source.requirement.requirement_change?.status==='reviewing';
  emit({type:'tool_call',id:'formal',name:'submit_requirement_review',args:{reply:changing?'已整理省市级定位变更，请确认新的需求稿。':'国家级定位范围明确，请确认需求稿。',decision:'ready',agenda:[],proposal:{description:changing?'记录国家与省市级所在地，保留原有测试记录。':'记录国家地区，保留测试记录。',acceptance:[changing?'能显示省市级所在地':'能显示国家地区']}}});return;
 }
 if(m.type!=='tool_result')return;
 if(m.id==='unsafe'){
  if(!m.error){emit({type:'error',error:'discussion could mutate workflow'});finish();return}
  emit({type:'tool_call',id:'code',name:'repository_read',args:{action:'file',path:'behavior.txt',ref:'main'}});return;
 }
 if(m.id==='code'){
  if(m.error||m.data.sha!==source.requirement.submission.sha||m.data.text.trim()!=='COUNTRY LEVEL BASELINE'){emit({type:'error',error:'wrong implementation version: '+JSON.stringify(m)});finish();return}
  emit({type:'tool_call',id:'bad-decision',name:'submit_requirement_review',args:{reply:'已转为需求变更',decision:'reject_bug',agenda:[]}});return;
 }
 if(m.id==='bad-decision'){
  if(!m.error){emit({type:'error',error:'formal decision accepted as answer'});finish();return}
  answer();return;
 }
 if(m.error)emit({type:'error',error:m.error});finish();
});
`);
const reserve=net.createServer().listen(0,'127.0.0.1');await once(reserve,'listening');const port=reserve.address().port;await new Promise(done=>reserve.close(done));
const base=`http://127.0.0.1:${port}`;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-requirements-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-pi-worker',worker,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});const serverExit=once(server,'exit');server.stderr.resume();
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1050},reducedMotion:'reduce'}),errors=[];
page.on('pageerror',e=>errors.push(e.message));
async function api(path,body,method){const r=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const v=await r.json();assert(r.ok,JSON.stringify(v));return v}
async function mcp(token,name,args){
 const r=await fetch(base+'/mcp',{
  method:'POST',
  headers:{
   Authorization:`Bearer ${token}`,
   'Content-Type':'application/json',
   Accept:'application/json, text/event-stream',
   'MCP-Protocol-Version':'2025-11-25',
  },
  body:JSON.stringify({jsonrpc:'2.0',id:1,method:'tools/call',params:{name,arguments:args}}),
 });
 const response=await r.json();
 assert(r.ok&&!response.error&&!response.result.isError,JSON.stringify(response));
 return JSON.parse(response.result.content[0].text);
}
async function waitReview(id,status){for(let n=0;n<160;n++){const v=await api(`/tasks/${id}/requirement-review`);if(v.status===status)return v;if(v.status==='failed')assert.fail(v.error);await new Promise(done=>setTimeout(done,75))}assert.fail('review never reached '+status)}
const report={};
try{
 for(let n=0;n<100;n++){try{if((await fetch(base+'/api/health')).ok)break}catch{}await new Promise(done=>setTimeout(done,50))}
 const profile=await api('/profiles',{name:'Local fixture',provider:'fixture',model:'fixture',base_url:'http://127.0.0.1:1',api:'openai-completions',supports_images:true});
 await api('/platform/settings',{profile_id:profile.id},'PUT');
 const project=await api('/projects',{name:'需求聊天室验证',repository:repo,branch:'main',agent_read_mode:'legacy'});
 for(let n=0;n<100;n++){const s=await api(`/projects/${project.id}/repository-status`);if(!s.syncing&&s.sha)break;assert(n<99,'repository sync failed');await new Promise(done=>setTimeout(done,50))}
 const {task:created}=await api(`/projects/${project.id}/tasks`,{title:'测试记录所在地',description:'在原有测试记录上展示所在地',request_id:'create'});
 let room=await waitReview(created.id,'ready_to_confirm');
 const originalSession=room.conversation_id;
 await api(`/tasks/${created.id}/requirement-review`,{action:'confirm',generation:room.generation,request_id:'confirm'});
 const token=(await api(`/projects/${project.id}/tokens`,{name:'本地开发'})).secret;
 const claimed=await mcp(token,'task_claim',{task_id:created.id,branch:'feature/location',request_id:'claim'});
 let task=claimed.task;
 await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();await page.locator('[data-task-row]').filter({hasText:task.title}).click();
 await page.getByRole('button',{name:'问 Agent',exact:true}).click();
 await page.locator('.rc-event').filter({hasText:'认领了开发'}).waitFor();
 const alignmentArgs={task_id:task.id,claim_id:claimed.claim_id};
 await mcp(token,'task_request_alignment',{...alignmentArgs,version:String(task.version),revision:String(task.revision),request_id:'ask',body:'请确认本次展示国家还是省市。'});
 await page.getByText('请确认本次展示国家还是省市。',{exact:true}).waitFor();
 await page.locator('#review-input').fill('本次只展示国家。');
 await page.locator('.review-send').click();
 await page.getByText('回复已提交，等待开发确认',{exact:true}).waitFor();
 task=(await api(`/tasks/${task.id}`)).task;
 await mcp(token,'task_resume_development',{...alignmentArgs,version:String(task.version),revision:String(task.revision),request_id:'resume',body:'本次按国家级定位实现。'});
 const alignmentComplete=page.locator('.rc-event').filter({hasText:'开发代理确认对齐完成'});
 await alignmentComplete.waitFor();
 await api(`/tasks/${task.id}/actions`,{action:'submit',revision:task.revision,request_id:'submit',submission:{summary:'完成国家级定位',sha:baseline,environment:'本地隔离环境',tests:'定向自测'}});
 task=(await api(`/tasks/${task.id}`)).task;
 await page.locator('.rc-event').filter({hasText:'提交了开发结果'}).waitFor();
 assert(await page.locator('#review-input').isEditable());
 assert.equal(await page.locator('.rc-status').textContent(),'测试中');
 assert.equal(await alignmentComplete.count(),1);
 assert.equal(await page.locator('.rc-proposal[data-state=confirmed]').count(),1);
 const png=await page.locator('.rc-title').screenshot();
 await page.locator('.review-file-input').setInputFiles({name:'所在地截图.png',mimeType:'image/png',buffer:png});await page.locator('.review-pending-files').filter({hasText:'所在地截图.png'}).waitFor();
 // Lose the response after saving: retrying the same draft must not duplicate it.
 let lost=false;await page.route(`**/api/tasks/${task.id}/requirement-review*`,async route=>{if(route.request().method()==='POST'&&route.request().postDataJSON()?.action==='discuss'&&!lost){lost=true;await route.fetch();await route.abort('failed')}else await route.continue()});
 await page.locator('#review-input').fill('当前提测版本把所在地记录到什么粒度？');
 await page.locator('.review-send').click();await page.locator('.review-send-error').filter({hasText:'草稿已保留'}).waitFor();await page.locator('.review-send').click();
 room=await waitReview(task.id,'answered');await page.locator('.rc-bubble').filter({hasText:'当前提测版本按已确认需求记录到国家级'}).waitFor();
 assert.equal(room.conversation_id,originalSession);assert.equal(room.messages.filter(m=>m.action==='discuss').length,1);assert.equal(room.messages.find(m=>m.action==='discuss').attachments.length,1);
 assert.deepEqual((await api(`/tasks/${task.id}`)).task,task);assert.equal((await api(`/projects/${project.id}/tasks`)).length,1);
 assert(!room.messages.some(m=>m.body==='已转为需求变更'));
 await page.screenshot({path:join(output,'discussion-desktop.png')});
 await page.setViewportSize({width:390,height:844});await page.locator('.review-feed').evaluate(el=>el.scrollTop=el.scrollHeight);await page.screenshot({path:join(output,'discussion-mobile.png')});
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth&&document.querySelector('.requirement-review').scrollWidth<=innerWidth));
 assert(await page.locator('.review-send').isVisible());assert.equal(await page.locator('#review-input').evaluate(el=>el.readOnly),false);
 const followUp='那页面只显示国家是预期行为吗？';
 await page.locator('#review-input').fill(followUp);
 await page.locator('.review-send').click();
 await page.getByText(followUp,{exact:true}).waitFor();
 await page.locator('.rc-bubble').filter({hasText:'当前提测版本按已确认需求记录到国家级'}).nth(1).waitFor();
 assert.deepEqual((await api(`/tasks/${task.id}`)).task,task);
 report.discussion='原评审、开发对齐和提测后的答疑在同一聊天室；图文提问与丢响应重试幂等；任务及交付证据保持一致';
 report.permissions='真实 worker 调用写工具和正式评审结论均被服务端拒绝；仅保存纠正后的答疑，未新增 Bug';
 report.code='刻意请求 main、开发分支另有未提测提交，实际读取仍固定为原提测 SHA';
 await page.setViewportSize({width:1440,height:1050});await page.getByRole('button',{name:'变更当前需求',exact:true}).click();
 await page.locator('#requirement-change-form [name=body]').fill('本轮细化到省市，保留原记录');await page.locator('#requirement-change-form button[type=submit]').click();
 room=await waitReview(task.id,'ready_to_confirm');assert.equal(room.conversation_id,originalSession);assert.equal(room.mode,'testing_change');
 await page.locator('.review-confirm').click();
 for(let n=0;n<100;n++){if((await api(`/tasks/${task.id}`)).task.status==='doing')break;await new Promise(done=>setTimeout(done,50))}
 const changed=(await api(`/tasks/${task.id}`)).task;assert.equal(changed.status,'doing');assert.equal(changed.revision,task.revision+1);assert.equal(changed.submission,undefined);
 report.change='变更评审继续使用原 Pi 会话；产品确认后才增加修订并交回原开发';
 assert.deepEqual(errors,[]);await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
}catch(e){await page.screenshot({path:join(output,'failure.png'),fullPage:true}).catch(()=>{});console.error(errors);throw e}
finally{await browser.close();server.kill('SIGTERM');await serverExit;await rm(data,{recursive:true,force:true})}
